#!/usr/bin/env bash
# Server steps of deploy/README.md for the setup skill, from DEPLOY_SSH, DEPLOY_SSH_PORT, DEPLOY_DIR
# and DEPLOY_URL in .env (or the environment). Secrets (VNC password, Claude token, AnkiConnect key)
# travel only through pipes and files: never argv, never this script's output.
#
#   server.sh precheck [update]  read-only host checks (exit 1 on FAIL)
#   server.sh upload             the kit and bin/anki-review-linux-amd64 into DEPLOY_DIR (0700)
#   server.sh upload-bin         only the binary, swapped in atomically
#   server.sh kit-diff           kit files that differ from the host's copy
#   server.sh up                 DEPLOY_DIR/anki/up.sh on the host (no sudo)
#   server.sh decks              deck count through the host's AnkiConnect
#   server.sh deck-names         its top-level decks and their kind under KNOWLEDGE_DECKS
#   server.sh wait-decks N       until the deck count exceeds N (30 min at most)
#   server.sh tunnel             VNC tunnel 127.0.0.1:5901 to the host's 5900, until killed
#   server.sh vnc-password       the VNC password into the clipboard
#   server.sh clip-clear         empties the clipboard
#   server.sh window NAME CMD    CMD in a new tmux window or Terminal window; prints its exit file
#   server.sh token              the Claude token from the clipboard to the host, then clears it
#   server.sh install            sudo install.sh in a new window; prints the exit file
#   server.sh wait-install FILE  until that window ends or the service restarts (30 min at most)
#   server.sh status             service, API stage, due decks, binary match, token, polkit
#   server.sh restart            systemctl restart without sudo (the polkit rule from install.sh)
#   server.sh sudo-restart       the same with sudo, in a new window; prints the exit file
#   server.sh verify-url         DEPLOY_URL/api/cards and /api/decks
set -euo pipefail

here=$(dirname -- "$0")
# shellcheck source=SCRIPTDIR/lib.sh
. "$here/lib.sh"
load_dotenv

die() {
  echo "server.sh: $*" >&2
  exit 1
}

svc="anki-review"
bin_local=$root/bin/anki-review-linux-amd64
kit_top="install.sh smoke.sh"
kit_anki="Dockerfile compose.yaml entrypoint.sh setup-profile.py up.sh"
kit_all="$kit_top systemd/anki-review.service anki/Dockerfile anki/compose.yaml anki/entrypoint.sh anki/setup-profile.py anki/up.sh"
host=$(trim "${DEPLOY_SSH:-}")
port=$(trim "${DEPLOY_SSH_PORT:-}")
port=${port:-22}
need_host() {
  [ -n "$host" ] || die "DEPLOY_SSH is not set: put user@host into .env"
  case $host in -* | *[[:space:]\'\"\\]*) die "DEPLOY_SSH looks wrong: $host" ;; esac
  case $port in '' | *[!0-9]*) die "DEPLOY_SSH_PORT must be a number: $port" ;; esac
}

# BatchMode: a key or agent is needed, ssh never stops at a password prompt nobody sees.
rsh() {
  need_host
  ssh -p "$port" -o BatchMode=yes -o ConnectTimeout=15 "$host" "$@"
}
rcp() {
  need_host
  scp -q -p -P "$port" -o BatchMode=yes "$@"
}

# Single quotes for a POSIX shell on the other side.
q() {
  local r="'\\''"
  printf "'%s'" "${1//\'/$r}"
}

dir=""
resolve_dir() {
  [ -z "$dir" ] || return 0
  dir=$(trim "${DEPLOY_DIR:-}")
  if [ -z "$dir" ]; then
    # shellcheck disable=SC2016 # $HOME belongs to the host
    dir=$(rsh 'printf "%s/anki-review-deploy" "$HOME"') || die "ssh -p $port $host failed"
  fi
  # A local ~ would be expanded here, not on the host; install.sh also puts the path into the unit.
  [[ $dir =~ ^/[A-Za-z0-9._/-]+$ ]] || die "DEPLOY_DIR must be absolute, letters, digits and . _ - / only: $dir"
}

clip_copy() {
  if command -v pbcopy >/dev/null 2>&1; then
    pbcopy
  elif command -v wl-copy >/dev/null 2>&1; then
    wl-copy
  elif command -v xclip >/dev/null 2>&1; then
    xclip -selection clipboard
  else
    die "no clipboard tool (pbcopy, wl-copy, xclip)"
  fi
}

clip_paste() {
  if command -v pbpaste >/dev/null 2>&1; then
    pbpaste
  elif command -v wl-paste >/dev/null 2>&1; then
    wl-paste -n
  elif command -v xclip >/dev/null 2>&1; then
    xclip -selection clipboard -o
  else
    die "no clipboard tool (pbpaste, wl-paste, xclip)"
  fi
}

sha() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}

count_decks() {
  python3 -I -c '
import json, sys
try:
    r = json.load(sys.stdin)
except ValueError:
    sys.exit("no JSON from AnkiConnect")
if r.get("error"):
    sys.exit("AnkiConnect: %s" % r["error"])
print(len(r["result"]))
'
}

cmd_precheck() {
  local mode=${1:-new}
  resolve_dir
  # shellcheck disable=SC2016 # the script runs on the host
  rsh "sh -s -- $(q "$dir") $(q "$mode")" <<'EOF'
dir=$1 mode=$2 fails=0
line() { printf '%-4s  %-9s %s\n' "$1" "$2" "$3"; }
fail() { line FAIL "$1" "$2"; fails=$((fails + 1)); }
listening() { [ -n "$(ss -Htln "sport = :$1" 2>/dev/null)" ]; }

hw=$(uname -sm)
if [ "$hw" = "Linux x86_64" ]; then
  line ok host "$hw, $(. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME"), user $(id -un)"
else
  fail host "$hw: the kit needs Linux x86_64"
fi
if [ -d /run/systemd/system ]; then line ok systemd "$(systemctl --version | head -n 1)"; else fail systemd "systemd is not running"; fi
if ! command -v docker >/dev/null 2>&1; then
  fail docker "not installed"
elif ! docker info >/dev/null 2>&1; then
  fail docker "$(id -un) can't reach docker: add it to the docker group and log in again"
elif ! docker compose version >/dev/null 2>&1; then
  fail docker "the compose plugin is missing"
else
  line ok docker "$(docker version -f '{{.Server.Version}}' 2>/dev/null), $(docker compose version --short 2>/dev/null)"
fi
for c in curl ss sudo; do command -v "$c" >/dev/null 2>&1 || fail tools "$c is missing"; done
if id -nG | tr ' ' '\n' | grep -Eqx 'sudo|wheel|admin'; then
  line ok sudo "$(id -un) is in a sudo group"
else
  line warn sudo "$(id -un) is in no sudo group: install.sh needs sudo (a sudoers entry works too)"
fi
pk=$(pkaction --version 2>/dev/null | awk '{print $NF}')
case $pk in
'') line warn polkit "not installed: updates will need sudo (apt install polkitd, then rerun install.sh)" ;;
0.10[0-5] | 0.10[0-5].* | 0.[0-9] | 0.[0-9][0-9]) line warn polkit "$pk reads no JavaScript rules: updates will need sudo" ;;
*) line ok polkit "$pk: updates restart without sudo" ;;
esac

ours_c=0 ours_u=0
wd=$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' anki-desktop 2>/dev/null || true)
if [ -n "$wd" ] || docker inspect anki-desktop >/dev/null 2>&1; then
  if [ "$wd" = "$dir/anki" ]; then
    ours_c=1
    line info anki "container anki-desktop from $dir/anki: $(docker inspect -f '{{.State.Status}}' anki-desktop)"
  else
    fail anki "a container named anki-desktop from ${wd:-elsewhere} exists: remove or rename it first"
  fi
fi
unit=/etc/systemd/system/anki-review.service
if [ -f "$unit" ]; then
  if grep -qF "$dir/bin/anki-review" "$unit"; then
    ours_u=1
    line info service "anki-review.service from $dir: $(systemctl is-active anki-review)"
  else
    fail service "anki-review.service exists for another deploy dir: remove it first"
  fi
elif [ "$mode" = update ]; then
  fail service "no anki-review.service yet: run the full server setup first"
fi
[ "$ours_c" = 1 ] || for p in 8765 5900; do
  if listening "$p"; then fail ports "127.0.0.1:$p is in use (AnkiConnect 8765, VNC 5900)"; fi
done
if [ "$ours_u" = 0 ] && listening 8767; then fail ports "8767 is in use (the service port)"; fi

if [ -d "$dir" ]; then
  line info dir "$dir exists, owner $(stat -c %U "$dir"), mode $(stat -c %a "$dir")"
else
  line info dir "$dir will be created"
fi
parent=$dir
while [ ! -d "$parent" ]; do parent=$(dirname "$parent"); done
free=$(df -Pk "$parent" | awk 'NR == 2 { print int($4 / 1024) }')
if [ "${free:-0}" -lt 2048 ]; then line warn disk "${free} MB free under $parent: the image needs about 1.5 GB"; else line ok disk "${free} MB free"; fi
mem=$(awk '/^MemAvailable:/ { print int($2 / 1024) }' /proc/meminfo)
if [ "${mem:-0}" -lt 1200 ]; then line warn memory "${mem} MB available: Anki gets up to 1 GB"; else line ok memory "${mem} MB available"; fi
if [ "$mode" = new ]; then
  bad=""
  for u in https://github.com https://git.sr.ht https://claude.ai https://ankiweb.net https://api.anthropic.com; do
    curl -sS -o /dev/null -m 10 -I "$u" 2>/dev/null || bad="$bad $u"
  done
  if [ -n "$bad" ]; then line warn net "no HTTPS answer from$bad"; else line ok net "GitHub, git.sr.ht, claude.ai, AnkiWeb, Anthropic API reachable"; fi
fi
echo
if [ "$fails" -gt 0 ]; then echo "$fails required check(s) failed"; exit 1; fi
echo "host ready"
EOF
}

need_bin() {
  [ -f "$bin_local" ] || die "missing $bin_local: run make build-linux"
  [ "$(head -c 4 "$bin_local" | od -An -c | tr -d ' ')" = '177ELF' ] || die "$bin_local is not a Linux binary"
}

cmd_upload_bin() {
  need_bin
  resolve_dir
  rsh "mkdir -p $(q "$dir/bin")"
  rcp "$bin_local" "$host:$dir/bin/anki-review.new"
  rsh "chmod 0755 $(q "$dir/bin/anki-review.new") && mv -f $(q "$dir/bin/anki-review.new") $(q "$dir/bin/anki-review")"
  echo "binary uploaded: $dir/bin/anki-review ($(sha "$bin_local" | cut -c1-12))"
}

cmd_upload() {
  local f anki=()
  need_bin
  resolve_dir
  for f in $kit_all; do [ -f "$root/deploy/$f" ] || die "missing deploy/$f"; done
  for f in $kit_anki; do anki+=("$root/deploy/anki/$f"); done
  rsh "mkdir -p $(q "$dir/bin") $(q "$dir/anki") $(q "$dir/systemd") && chmod 0700 $(q "$dir")"
  # -p keeps the modes: install.sh, smoke.sh, up.sh and entrypoint.sh stay executable.
  rcp "$root/deploy/install.sh" "$root/deploy/smoke.sh" "$host:$dir/"
  rcp "$root/deploy/systemd/anki-review.service" "$host:$dir/systemd/"
  rcp "${anki[@]}" "$host:$dir/anki/"
  echo "kit uploaded to $dir"
  cmd_upload_bin
}

cmd_kit_diff() {
  local f remote diff=0 rc=0
  resolve_dir
  # sha256sum exits 1 for a missing file; 255 is ssh itself failing.
  remote=$(rsh "cd $(q "$dir") 2>/dev/null && sha256sum $kit_all 2>/dev/null") || rc=$?
  [ "$rc" != 255 ] || die "ssh -p $port $host failed"
  for f in $kit_all; do
    if ! printf '%s\n' "$remote" | grep -qxF "$(sha "$root/deploy/$f" | cut -d' ' -f1)  $f"; then
      echo "differs: deploy/$f"
      diff=1
    fi
  done
  [ "$diff" = 1 ] || echo "kit on the host matches deploy/"
}

cmd_up() {
  resolve_dir
  rsh "$(q "$dir/anki/up.sh")"
}

deck_names_json() {
  resolve_dir
  # The key is read on the host and handed to printf, a shell builtin there: never in argv.
  # shellcheck disable=SC2016 # the script runs on the host
  rsh "sh -s -- $(q "$dir")" <<'EOF'
k=$(tr -d '[:space:]' <"$1/anki/secrets/ankiconnect-key") || exit 1
printf '{"action":"deckNames","version":6,"key":"%s"}' "$k" |
  curl -sS -m 20 --data-binary @- http://127.0.0.1:8765
EOF
}

cmd_decks() { deck_names_json | count_decks; }

# Top-level decks of the server's collection with the kind KNOWLEDGE_DECKS gives them, as check.sh decks.
cmd_deck_names() {
  deck_names_json | KD=${KNOWLEDGE_DECKS:-Tech,Career} python3 -I -c '
import json, os, sys
try:
    r = json.load(sys.stdin)
except ValueError:
    sys.exit("no JSON from AnkiConnect")
if r.get("error"):
    sys.exit("AnkiConnect: %s" % r["error"])
kd = [s.strip() for s in os.environ["KD"].split(",") if s.strip()]
tops = sorted({n.split("::")[0] for n in r["result"] if n != "Default"})
for t in tops:
    print("%s\t%s" % ("knowledge" if t in kd else "language", t))
for k in kd:
    if k not in tops:
        print("missing\t%s" % k)
'
}

cmd_wait_decks() {
  local base=${1:-1} n i
  case $base in '' | *[!0-9]*) die "wait-decks needs a number" ;; esac
  resolve_dir
  for i in $(seq 1 180); do
    n=$(cmd_decks 2>/dev/null || true)
    if [ -n "$n" ] && [ "$n" -gt "$base" ]; then
      echo "decks: $n"
      return 0
    fi
    [ "$i" = 180 ] || sleep 10
  done
  echo "decks: still ${n:-unknown} after 30 minutes"
  return 1
}

cmd_tunnel() {
  need_host
  if command -v lsof >/dev/null 2>&1 && lsof -nP -iTCP:5901 -sTCP:LISTEN >/dev/null 2>&1; then
    die "local port 5901 is in use"
  fi
  # Its own connection: through a shared ControlMaster the forward would outlive this process.
  exec ssh -p "$port" -o BatchMode=yes -o ControlMaster=no -o ControlPath=none \
    -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -N -L 127.0.0.1:5901:127.0.0.1:5900 "$host"
}

cmd_vnc_password() {
  local pw
  resolve_dir
  pw=$(rsh "cat $(q "$dir/anki/secrets/vnc-password")") || die "can't read the VNC password: did up.sh run?"
  pw=$(trim "$pw")
  [ "${#pw}" -eq 8 ] || die "the VNC password file looks wrong"
  printf '%s' "$pw" | clip_copy
  unset pw
  echo "VNC password copied to the clipboard"
}

cmd_clip_clear() {
  printf '' | clip_copy
  echo "clipboard cleared"
}

# For AppleScript string literals.
as_escape() {
  local s=${1//\\/\\\\}
  printf '%s' "${s//\"/\\\"}"
}

cmd_window() {
  local name=${1:?window name} cmd st full
  shift
  cmd=$*
  [ -n "$cmd" ] || die "window needs a command"
  st=$(mktemp -d "${TMPDIR:-/tmp}/anki-review-setup.XXXXXX")
  full="$cmd; echo \$? > '$st/exit'; echo; echo 'Finished: go back to Claude Code. Press Enter to close.'; read -r _line"
  if [ -n "${TMUX:-}" ] && command -v tmux >/dev/null 2>&1; then
    tmux new-window -n "$name" "$full"
  elif [ "$os" = Darwin ]; then
    osascript -e "tell application \"Terminal\" to do script \"$(as_escape "$full")\"" \
      -e 'tell application "Terminal" to activate' >/dev/null
  else
    die "no tmux session and not macOS: run this yourself: $cmd"
  fi
  echo "$st/exit"
}

cmd_token() {
  local t size
  resolve_dir
  t=$(trim "$(clip_paste)")
  case $t in sk-ant-?*) ;; *) die "the clipboard holds no Claude token (sk-ant-...): copy the one claude setup-token printed" ;; esac
  case $t in *[[:space:]]*) die "the clipboard holds more than one token" ;; esac
  printf '%s\n' "$t" | rsh "umask 077; cat > $(q "$dir/claude-token")"
  unset t
  size=$(rsh "wc -c < $(q "$dir/claude-token")")
  printf '' | clip_copy
  echo "token uploaded: $(trim "$size") bytes in $dir/claude-token, clipboard cleared"
}

invocation() { rsh "systemctl show -p InvocationID --value $svc 2>/dev/null" || true; }

cmd_install() {
  local env_args="" remote exitf inv0
  resolve_dir
  [ -z "${KNOWLEDGE_DECKS:-}" ] || env_args="$env_args KNOWLEDGE_DECKS=$(q "$KNOWLEDGE_DECKS")"
  [ -z "${CLAUDE_MODEL:-}" ] || env_args="$env_args CLAUDE_MODEL=$(q "$CLAUDE_MODEL")"
  remote="sudo env$env_args $dir/install.sh $dir"
  inv0=$(invocation)
  exitf=$(cmd_window anki-review-install "ssh -t -p $port $(q "$host") $(q "$remote")")
  printf '%s\n' "$inv0" >"${exitf%/exit}/invocation"
  echo "$exitf"
}

cmd_wait_install() {
  local exitf=${1:?exit file} inv0="" inv i
  [ ! -f "${exitf%/exit}/invocation" ] || inv0=$(cat "${exitf%/exit}/invocation")
  for i in $(seq 1 360); do
    if [ -f "$exitf" ]; then
      echo "window ended, exit code $(cat "$exitf")"
      return 0
    fi
    if [ $((i % 6)) = 0 ]; then
      inv=$(invocation)
      if [ -n "$inv" ] && [ "$inv" != "$inv0" ] && [ "$(rsh "systemctl is-active $svc" 2>/dev/null || true)" = active ]; then
        echo "service restarted (the window is still open)"
        return 0
      fi
    fi
    [ "$i" = 360 ] || sleep 5
  done
  echo "no result after 30 minutes"
  return 1
}

status_script() {
  cat <<'EOF'
echo "service=$(systemctl is-active anki-review 2>/dev/null)"
echo "bin=$(sha256sum /usr/local/bin/anki-review 2>/dev/null | cut -c1-64)"
echo "cards=$(curl -sS -m 5 http://127.0.0.1:8767/api/cards 2>/dev/null | head -c 100000 | tr -d '\n')"
echo "decks=$(curl -sS -m 30 http://127.0.0.1:8767/api/decks 2>/dev/null | head -c 100000 | tr -d '\n')"
# install.sh leaves this file, no secrets in it
[ ! -f "$1/install-state" ] || sed -n 's/^\(token\|polkit\)=/state_&/p' "$1/install-state"
EOF
}

cmd_status() {
  local out want=""
  resolve_dir
  out=$(status_script | rsh "sh -s -- $(q "$dir")") || die "ssh -p $port $host failed"
  [ ! -f "$bin_local" ] || want=$(sha "$bin_local" | cut -d' ' -f1)
  printf '%s\n' "$out" | WANT=$want python3 -I -c '
import json, os, sys
v = dict(l.split("=", 1) for l in sys.stdin.read().splitlines() if "=" in l)
print("service:", v.get("service") or "unknown")
try:
    print("api stage:", json.loads(v.get("cards", "")).get("stage"))
except ValueError:
    print("api stage: no answer on 127.0.0.1:8767")
try:
    decks = json.loads(v.get("decks", ""))["decks"]
    print("decks with cards due:", len(decks))
    print("knowledge decks among them:", ", ".join(d["name"] for d in decks if d.get("kind") == "knowledge" and "::" not in d["name"]) or "none")
except (ValueError, KeyError, TypeError):
    print("decks with cards due: no answer (AnkiConnect down or the key is wrong?)")
print("token:", v.get("state_token", "not installed yet (no install-state)"))
print("polkit rule:", v.get("state_polkit", "unknown (no install-state)"))
want, got = os.environ.get("WANT"), v.get("bin", "")
if not got:
    print("binary: not installed")
elif want:
    print("binary:", "matches bin/anki-review-linux-amd64" if got == want else "differs from bin/anki-review-linux-amd64")
'
}

cmd_restart() {
  local err rc=0
  err=$(rsh "systemctl --no-ask-password restart $svc" 2>&1) || rc=$?
  if [ "$rc" = 0 ]; then
    echo "restarted $svc without sudo"
  elif [ "$rc" = 255 ]; then
    die "ssh -p $port $host failed: $err"
  else
    echo "$err" >&2
    die "restart refused: no polkit rule yet (rerun the full server setup once) or polkit is missing"
  fi
}

cmd_sudo_restart() {
  local inv0 exitf
  inv0=$(invocation)
  exitf=$(cmd_window anki-review-restart "ssh -t -p $port $(q "$host") $(q "sudo systemctl restart $svc")")
  printf '%s\n' "$inv0" >"${exitf%/exit}/invocation"
  echo "$exitf"
}

cmd_verify_url() {
  local url p body code
  url=$(trim "${DEPLOY_URL:-}")
  [ -n "$url" ] || die "DEPLOY_URL is not set"
  url=${url%/}
  for p in /api/cards /api/decks; do
    body=$(curl -sS -m 30 -w '\n%{http_code}' "$url$p" 2>&1) || true
    code=${body##*$'\n'}
    body=${body%$'\n'*}
    if [ "$code" = 200 ]; then
      printf '%s' "$body" | WHAT="$url$p" python3 -I -c '
import json, os, sys
what = os.environ["WHAT"]
try:
    r = json.load(sys.stdin)
    print(what + ": 200,", "stage %s" % r["stage"] if "stage" in r else "%d deck(s) due" % len(r["decks"]))
except (ValueError, KeyError, TypeError):
    print(what + ": 200 but not the API (a login page?)")
'
    else
      echo "$url$p: HTTP ${code:-none}, $(printf '%s' "$body" | head -c 200)"
    fi
  done
}

case ${1:-} in
precheck | upload | upload-bin | kit-diff | up | decks | deck-names | wait-decks | tunnel | vnc-password | token | \
  install | wait-install | status | restart | sudo-restart) need_host ;;
esac
case ${1:-} in
precheck) cmd_precheck "${2:-new}" ;;
upload) cmd_upload ;;
upload-bin) cmd_upload_bin ;;
kit-diff) cmd_kit_diff ;;
up) cmd_up ;;
decks) cmd_decks ;;
deck-names) cmd_deck_names ;;
wait-decks) cmd_wait_decks "${2:-1}" ;;
tunnel) cmd_tunnel ;;
vnc-password) cmd_vnc_password ;;
clip-clear) cmd_clip_clear ;;
window)
  shift
  cmd_window "$@"
  ;;
token) cmd_token ;;
install) cmd_install ;;
wait-install) cmd_wait_install "${2:-}" ;;
status) cmd_status ;;
restart) cmd_restart ;;
sudo-restart) cmd_sudo_restart ;;
verify-url) cmd_verify_url ;;
*)
  sed -n 's/^#   //p' "$0" >&2
  exit 2
  ;;
esac
