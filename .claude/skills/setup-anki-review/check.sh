#!/usr/bin/env bash
# Read-only status of everything anki-review needs, from the same environment variables the
# server reads. It sends AnkiConnect only `version` and `deckNames`, never calls the Claude API
# (no quota), never contacts a running anki-review, never starts or stops a process. From the
# saved pass in DATA_DIR it prints only the stage.
#
#   check.sh         one line per prerequisite; exit 1 when a required one fails
#   check.sh decks   top-level Anki decks and the kind the server would grade each as
set -u

root=$(cd -- "$(dirname -- "$0")/../../.." && pwd -P)
os=$(uname -s)

trim() {
  local s=$1
  s=${s#"${s%%[![:space:]]*}"}
  s=${s%"${s##*[![:space:]]}"}
  printf '%s' "$s"
}

# Defaults mirror internal/config: a blank value means the default.
env_or() {
  local v
  v=$(trim "${!1:-}")
  printf '%s' "${v:-$2}"
}

port=$(env_or PORT 8767)
anki_url=$(env_or ANKI_CONNECT_URL http://127.0.0.1:8765)
anki_key=${ANKI_CONNECT_KEY:-}
claude_bin=$(env_or CLAUDE_BIN claude)
data_dir=$(env_or DATA_DIR data)
knowledge=$(env_or KNOWLEDGE_DECKS Tech,Career)
case $data_dir in /*) ;; *) data_dir=$root/$data_dir ;; esac

fails=0
line() { printf '%-4s  %-9s %s\n' "$1" "$2" "$3"; }
ok() { line ok "$1" "$2"; }
info() { line info "$1" "$2"; }
warn() { line warn "$1" "$2"; }
fail() {
  line FAIL "$1" "$2"
  fails=$((fails + 1))
}

# $1 >= $2 for dotted versions; suffixes like rc1 are ignored.
version_ge() {
  local IFS=. i x y
  local -a a b
  read -r -a a <<<"$1"
  read -r -a b <<<"$2"
  for i in 0 1 2; do
    x=${a[i]:-0} y=${b[i]:-0}
    x=${x%%[!0-9]*} y=${y%%[!0-9]*}
    x=${x:-0} y=${y:-0}
    if ((10#$x > 10#$y)); then return 0; fi
    if ((10#$x < 10#$y)); then return 1; fi
  done
  return 0
}

# On a Mac without the command line tools /usr/bin/python3 is a stub that opens an installer.
have_python() {
  command -v python3 >/dev/null 2>&1 || return 1
  [ "$os" != Darwin ] || xcode-select -p >/dev/null 2>&1
}

# The body goes through stdin, so the apiKey never shows up in `ps`.
anki_call() {
  local key=""
  if [ -n "$anki_key" ]; then
    key=${anki_key//\\/\\\\}
    key=${key//\"/\\\"}
    key=",\"key\":\"$key\""
  fi
  printf '{"action":"%s","version":6%s}' "$1" "$key" |
    curl -sS -m 5 -H 'Content-Type: application/json' --data-binary @- "$anki_url" 2>&1
}

# Lines "<kind>\t<deck>": knowledge, language, or missing (named in KNOWLEDGE_DECKS, no such deck).
# Same rules as the server: exact top-level name, case-sensitive, the bare Default deck left out.
deck_kinds() {
  anki_call deckNames | KD=$knowledge python3 -I -c '
import json, os, sys
try:
    r = json.load(sys.stdin)
except ValueError:
    sys.exit("no JSON answer from AnkiConnect")
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

# Stage of the saved pass, derived like domain.Pass.Stage; the cards and keys are not printed.
pass_stage() {
  python3 -I -c '
import json, sys
try:
    with open(sys.argv[1], encoding="utf-8") as f:
        st = json.load(f)
except (OSError, ValueError):
    st = None
if not isinstance(st, dict) or st.get("version") != 1:
    print("unreadable")
    sys.exit()
p = st.get("pass")
if not isinstance(p, dict):
    s = "idle"
elif (p.get("status") or {}).get("written"):
    s = "written"
elif p.get("confirmed") is not None:
    s = "confirmed"
elif p.get("results") is not None:
    s = "results"
elif p.get("answers") is not None:
    s = "grading-failed" if p.get("gradeError") else "checking"
else:
    s = "input"
print(s)
' "$1"
}

if ! command -v curl >/dev/null 2>&1; then
  echo "check.sh: curl is required" >&2
  exit 1
fi

case ${1:-} in
"") ;;
decks)
  if have_python; then
    deck_kinds
  else
    echo "python3 not found, raw AnkiConnect answer:" >&2
    anki_call deckNames
    echo
  fi
  exit
  ;;
*)
  echo "usage: check.sh [decks]" >&2
  exit 2
  ;;
esac

case $os in
Darwin) info os "macOS $(sw_vers -productVersion 2>/dev/null) $(uname -m)" ;;
Linux) info os "$(sed -n 's/^PRETTY_NAME=//p' /etc/os-release 2>/dev/null | tr -d '"') $(uname -m)" ;;
*) fail os "$os: this setup covers macOS and Linux only" ;;
esac
info repo "$root"

# internal/config refuses to start on these; same rules, all reported at once.
bad="" port_ok=1 url_ok=1
p=${port#+}
p=${p#"${p%%[!0]*}"}
case $p in
'' | *[!0-9]*) port_ok=0 ;;
*) if [ ${#p} -gt 5 ] || ((10#$p > 65535)); then port_ok=0; fi ;;
esac
if [ "$port_ok" = 1 ]; then port=$p; else bad="$bad, PORT=$port (1 to 65535)"; fi
sync=$(env_or ANKI_SYNC true)
case $sync in
1 | t | T | TRUE | true | True | 0 | f | F | FALSE | false | False) ;;
*) bad="$bad, ANKI_SYNC=$sync (true or false)" ;;
esac
gt=$(env_or GRADE_TIMEOUT 5m)
if ! printf '%s' "$gt" | grep -Eq '^[+]?(([0-9]+(\.[0-9]*)?|\.[0-9]+)(ns|us|ms|s|m|h))+$' ||
  ! printf '%s' "$gt" | grep -q '[1-9]'; then
  bad="$bad, GRADE_TIMEOUT=$gt (a positive duration like 5m)"
fi
ll=$(env_or LOG_LEVEL info)
case $(printf '%s' "$ll" | tr '[:upper:]' '[:lower:]') in
trace | debug | info | warn | error | fatal | panic | disabled) ;;
*) printf '%s' "$ll" | grep -Eq '^[+-]?[0-9]{1,3}$' || bad="$bad, LOG_LEVEL=$ll (debug, info, warn or error)" ;;
esac
if ! printf '%s' "$anki_url" | grep -Eiq '^https?://[^/?#]+'; then
  url_ok=0
  bad="$bad, ANKI_CONNECT_URL=$anki_url (an http or https URL)"
fi
if [ -n "$bad" ]; then
  fail config "the server would refuse to start: ${bad#, }"
else
  ok config "PORT, ANKI_CONNECT_URL, ANKI_SYNC, GRADE_TIMEOUT, LOG_LEVEL are valid"
fi

need=$(awk '$1 == "go" { print $2; exit }' "$root/go.mod" 2>/dev/null)
need=${need:-1.27}
if command -v go >/dev/null 2>&1; then
  # outside the module and with GOTOOLCHAIN=local, so the check itself never fetches a toolchain
  have=$(cd / && GOTOOLCHAIN=local go env GOVERSION 2>/dev/null)
  have=${have#go}
  if version_ge "$have" "$need"; then
    ok go "$have (go.mod wants $need)"
  elif version_ge "$have" 1.21; then
    warn go "$have is older than go.mod's $need: the build fetches go$need itself unless GOTOOLCHAIN=local"
  else
    fail go "$have is too old: install $need or newer"
  fi
else
  fail go "not found: install $need or newer"
fi

if command -v golangci-lint >/dev/null 2>&1; then
  gl=$(golangci-lint --version 2>/dev/null | sed -nE 's/.*version v?([0-9][0-9.]*).*/\1/p' | head -n 1)
  case $gl in
  2.*) ok lint "golangci-lint $gl: make check works" ;;
  *) warn lint "golangci-lint ${gl:-unknown}: .golangci.yml needs v2, use go test ./... instead of make check" ;;
  esac
else
  info lint "golangci-lint not found: only make check needs it, go test ./... works without"
fi

anki_where="" anki_ver=""
case $os in
Darwin)
  for app in /Applications/Anki.app "$HOME/Applications/Anki.app"; do
    [ -d "$app" ] || continue
    anki_where=$app
    anki_ver=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/Info.plist" 2>/dev/null)
    break
  done
  ;;
Linux)
  if command -v anki >/dev/null 2>&1; then
    anki_where=$(command -v anki)
  elif command -v flatpak >/dev/null 2>&1 && flatpak info net.ankiweb.Anki >/dev/null 2>&1; then
    anki_where="flatpak net.ankiweb.Anki"
    anki_ver=$(flatpak info net.ankiweb.Anki 2>/dev/null | awk '$1 == "Version:" { print $2; exit }')
  fi
  ;;
esac
if pgrep -x Anki >/dev/null 2>&1 || pgrep -x anki >/dev/null 2>&1; then
  anki_run=running
else
  anki_run="no process named Anki or anki"
fi
if [ -z "$anki_where" ]; then
  warn anki "app not found in the usual places ($anki_run)"
elif [ -n "$anki_ver" ] && ! version_ge "$anki_ver" 26.8; then
  warn anki "$anki_ver at $anki_where ($anki_run): the current AnkiConnect needs Anki 26.08 or later"
else
  ok anki "${anki_ver:+$anki_ver at }$anki_where ($anki_run)"
fi

api_ok=0 resp=""
[ "$url_ok" = 0 ] || resp=$(anki_call version)
if [ "$url_ok" = 0 ]; then
  info ankiconn "skipped: ANKI_CONNECT_URL is not valid"
elif printf '%s' "$resp" | grep -Eq '"result"[[:space:]]*:[[:space:]]*6[^0-9]'; then
  api_ok=1
  ok ankiconn "$anki_url answers API version 6${anki_key:+ (with ANKI_CONNECT_KEY)}"
elif printf '%s' "$resp" | grep -qi 'api key'; then
  fail ankiconn "$anki_url wants another key: ANKI_CONNECT_KEY must equal the add-on's apiKey (unset if it has none)"
elif printf '%s' "$resp" | grep -q '"result"'; then
  fail ankiconn "$anki_url answered: $(printf '%s' "$resp" | head -c 200)"
else
  case $os in
  Darwin) bases=("${ANKI_BASE:-$HOME/Library/Application Support/Anki2}") ;;
  *) bases=("${ANKI_BASE:-${XDG_DATA_HOME:-$HOME/.local/share}/Anki2}" "$HOME/.var/app/net.ankiweb.Anki/data/Anki2") ;;
  esac
  addon=""
  for b in "${bases[@]}"; do
    if [ -d "$b/addons21/2055492159" ]; then
      addon=$b/addons21/2055492159
      break
    fi
  done
  if [ -n "$addon" ]; then
    fail ankiconn "$anki_url unreachable; add-on installed at $addon: start or restart Anki"
  else
    fail ankiconn "$anki_url unreachable; add-on 2055492159 not found under ${bases[*]}"
  fi
fi

if [ "$api_ok" = 1 ]; then
  if ! have_python; then
    info decks "python3 not found: deck check skipped"
  elif kinds=$(deck_kinds 2>&1); then
    known=$(printf '%s\n' "$kinds" | awk -F'\t' '$1 == "knowledge" { printf "%s%s", s, $2; s = ", " }')
    lang=$(printf '%s\n' "$kinds" | grep -c '^language' || true)
    missing=$(printf '%s\n' "$kinds" | awk -F'\t' '$1 == "missing" { printf "%s%s", s, $2; s = ", " }')
    info decks "KNOWLEDGE_DECKS=$knowledge; graded by meaning: ${known:-none}; as language: $lang other top-level deck(s)"
    if [ -n "$missing" ]; then
      warn decks "no top-level deck named $missing (names are exact and case-sensitive)"
    fi
  else
    warn decks "deckNames failed: $kinds"
  fi
fi

if cb=$(command -v -- "$claude_bin" 2>/dev/null); then
  cv=$("$cb" --version 2>/dev/null | head -n 1)
  st=$("$cb" auth status --json 2>/dev/null)
  if printf '%s' "$st" | grep -Eq '"loggedIn"[[:space:]]*:[[:space:]]*true'; then
    how=$(printf '%s' "$st" | sed -nE 's/.*"authMethod"[[:space:]]*:[[:space:]]*"([^"]*)".*/\1/p' | head -n 1)
    ok claude "${cv:-unknown version} at $cb, logged in (${how:-unknown method})"
  else
    warn claude "${cv:-unknown version} at $cb, claude auth status says not logged in (an API key or a cloud provider may still work: the smoke test decides)"
  fi
else
  fail claude "$claude_bin not found"
fi

pid="" can_tell=1
if [ "$port_ok" = 0 ]; then
  can_tell=0
  info port "skipped: PORT is not valid"
elif command -v lsof >/dev/null 2>&1; then
  pid=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -Fp 2>/dev/null | sed -n 's/^p//p' | head -n 1)
elif command -v ss >/dev/null 2>&1; then
  pid=$(ss -Hltnp "sport = :$port" 2>/dev/null | sed -nE 's/.*pid=([0-9]+).*/\1/p' | head -n 1)
else
  can_tell=0
  info port "$port: lsof and ss not found, can't tell if it is free"
fi
if [ -n "$pid" ]; then
  exe=$(readlink "/proc/$pid/exe" 2>/dev/null || ps -p "$pid" -o comm= 2>/dev/null)
  # Linux marks a binary replaced by a rebuild under the running process
  exe=${exe% (deleted)}
  # macOS ps shows the path as typed: a relative one is resolved against the process's cwd
  case $exe in
  /* | "") ;;
  */*)
    cwd=$(lsof -a -p "$pid" -d cwd -Fn 2>/dev/null | sed -n 's/^n//p' | head -n 1)
    [ -z "$cwd" ] || exe=$cwd/${exe#./}
    ;;
  esac
  case $exe in
  "$root/bin/anki-review") info port "$port: this checkout's bin/anki-review is already serving (pid $pid)" ;;
  */anki-review | anki-review) info port "$port: an anki-review is already serving (pid $pid, $exe)" ;;
  *) warn port "$port is taken by ${exe:-pid $pid} (pid $pid): run with PORT=<free port>" ;;
  esac
elif [ "$can_tell" = 1 ]; then
  ok port "$port is free"
fi

bin=$root/bin/anki-review
if [ -x "$bin" ]; then
  newer=$(find "$root/cmd" "$root/internal" "$root/web" "$root/go.mod" -type f -newer "$bin" 2>/dev/null | head -n 1)
  if [ -n "$newer" ]; then
    info build "bin/anki-review from $(date -r "$bin" '+%Y-%m-%d %H:%M'), older than ${newer#"$root"/}: rebuild"
  else
    info build "bin/anki-review from $(date -r "$bin" '+%Y-%m-%d %H:%M'), up to date"
  fi
else
  info build "bin/anki-review not built yet"
fi

if [ -f "$data_dir/state.json" ]; then
  stage=unknown
  if have_python; then stage=$(pass_stage "$data_dir/state.json"); fi
  case $stage in
  confirmed) warn data "$data_dir holds a pass with confirmed grades: the server writes them to Anki as soon as it starts" ;;
  checking) warn data "$data_dir holds a pass waiting for grading: the server runs claude -p on it as soon as it starts" ;;
  idle) info data "$data_dir exists, no saved pass" ;;
  unreadable) warn data "$data_dir/state.json is not a version 1 state file: the server refuses to start with it" ;;
  unknown) warn data "$data_dir holds a saved pass, stage unknown without python3: confirmed grades in it go to Anki as soon as the server starts" ;;
  *) info data "$data_dir holds a saved pass (stage $stage): nothing runs on it until the page acts" ;;
  esac
elif [ -d "$data_dir" ]; then
  info data "$data_dir exists, no saved pass"
else
  info data "$data_dir does not exist yet: the server creates it (mode 0700)"
fi

echo
if [ "$fails" -gt 0 ]; then
  echo "$fails required check(s) failed"
  exit 1
fi
echo "all required checks passed"
