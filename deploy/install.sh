#!/usr/bin/env bash
# Installs and starts the anki-review service from a deploy dir: system user, Claude Code for that
# user, /etc/anki-review/env with the AnkiConnect key and the uploaded Claude token, the hardened unit,
# a polkit rule that lets the deploy user restart the service without sudo, a smoke test of claude in
# the service sandbox, then enable --now. Idempotent: rerun it to rotate the token, change a setting
# or move Claude Code to CLAUDE_VERSION.
#
# Usage: sudo /abs/path/install.sh [DEPLOY_DIR]
#   DEPLOY_DIR: argument, else ANKI_REVIEW_DEPLOY_DIR, else <home of SUDO_USER>/anki-review-deploy.
#   It holds this kit, bin/anki-review (linux/amd64) and anki/secrets/ankiconnect-key (from up.sh).
# Settings, through `sudo env NAME=value ... install.sh`: PORT, KNOWLEDGE_DECKS, ANKI_CONNECT_URL
# (or ANKI_CONNECT_PORT), ANKI_SYNC, CLAUDE_MODEL, GRADE_TIMEOUT, LOG_LEVEL go into the env file;
# CLAUDE_VERSION=x.y.z|stable|latest overrides the pinned Claude Code version.
set -euo pipefail

fail() { echo "install.sh: $*" >&2; exit 1; }
[[ $EUID -eq 0 ]] || fail "run as root: sudo $0"
umask 022

deploy_dir="${1:-${ANKI_REVIEW_DEPLOY_DIR:-}}"
if [[ -z $deploy_dir ]]; then
  [[ -n ${SUDO_USER:-} && $SUDO_USER != root ]] || fail "pass the deploy dir: sudo $0 /home/<user>/anki-review-deploy"
  user_home="$(getent passwd "$SUDO_USER" | cut -d: -f6)"
  [[ -n $user_home ]] || fail "no home directory for $SUDO_USER"
  deploy_dir="$user_home/anki-review-deploy"
fi
deploy_dir="$(realpath -e -- "$deploy_dir")" || fail "deploy dir not found: $deploy_dir"
# The path lands verbatim in the unit file.
[[ $deploy_dir =~ ^/[A-Za-z0-9._/-]+$ ]] || fail "deploy dir path may hold only letters, digits and . _ - /: $deploy_dir"
deploy_user="$(stat -c %U "$deploy_dir")"

svc=anki-review
home=/var/lib/anki-review
bin_src="$deploy_dir/bin/anki-review"
bin_dst=/usr/local/bin/anki-review
unit_src="$deploy_dir/systemd/anki-review.service"
unit_dst=/etc/systemd/system/anki-review.service
smoke="$deploy_dir/smoke.sh"
key_file="$deploy_dir/anki/secrets/ankiconnect-key"
token_file="$deploy_dir/claude-token"
env_dir=/etc/anki-review
env_file="$env_dir/env"
polkit_rule=/etc/polkit-1/rules.d/50-anki-review.rules
claude_version="${CLAUDE_VERSION:-2.1.310}"
claude_bin="$home/.local/bin/claude"
settings=(PORT KNOWLEDGE_DECKS ANKI_CONNECT_URL ANKI_SYNC CLAUDE_MODEL GRADE_TIMEOUT LOG_LEVEL)

for f in "$bin_src" "$unit_src" "$smoke"; do
  [[ -f $f ]] || fail "missing $f"
done
[[ -f $key_file ]] || fail "missing $key_file: run $deploy_dir/anki/up.sh as $deploy_user first"
[[ $claude_version =~ ^(stable|latest|[0-9]+\.[0-9]+\.[0-9]+)$ ]] || fail "bad CLAUDE_VERSION: $claude_version"
if [[ -n ${PORT:-} && ! $PORT =~ ^[0-9]+$ ]]; then
  fail "bad PORT: $PORT"
fi
if [[ -n ${ANKI_CONNECT_PORT:-} && -z ${ANKI_CONNECT_URL:-} ]]; then
  [[ $ANKI_CONNECT_PORT =~ ^[0-9]+$ ]] || fail "bad ANKI_CONNECT_PORT: $ANKI_CONNECT_PORT"
  ANKI_CONNECT_URL="http://127.0.0.1:$ANKI_CONNECT_PORT"
fi

# Strips leading and trailing whitespace with expansions only: secrets never reach a process argv.
trim() {
  local s="$1"
  s="${s#"${s%%[![:space:]]*}"}"
  printf '%s' "${s%"${s##*[![:space:]]}"}"
}

# Sets NAME=VALUE in the env file (replacing the line or appending it), root:anki-review 0640.
# printf is a builtin, so VALUE never shows up in a process list.
set_env_line() {
  local name="$1" value="$2" tmp line found=0
  [[ $value != *$'\n'* ]] || fail "$name: the value must be one line"
  tmp="$(mktemp "$env_dir/.env.XXXXXX")"
  chown root:"$svc" "$tmp"
  chmod 0640 "$tmp"
  while IFS= read -r line || [[ -n $line ]]; do
    if [[ $line == "$name="* ]]; then
      printf '%s=%s\n' "$name" "$value"
      found=1
    else
      printf '%s\n' "$line"
    fi
  done <"$env_file" >"$tmp"
  if [[ $found -eq 0 ]]; then
    printf '%s=%s\n' "$name" "$value" >>"$tmp"
  fi
  mv -f "$tmp" "$env_file"
}

env_value() {
  local line
  while IFS= read -r line || [[ -n $line ]]; do
    if [[ $line == "$1="* ]]; then
      printf '%s' "${line#*=}"
      return
    fi
  done <"$env_file"
}

# Root reads and then shreds files in a directory the deploy user owns: refuse links.
check_upload() {
  local owner links
  [[ -f $1 && ! -L $1 ]] || fail "$1 must be a regular file"
  read -r owner links < <(stat -c '%U %h' "$1")
  [[ $owner == "$deploy_user" || $owner == root ]] || fail "$1 is owned by $owner"
  [[ $links -eq 1 ]] || fail "$1 has more than one hard link"
}

echo "== user $svc"
if ! id -u "$svc" >/dev/null 2>&1; then
  useradd --system --user-group --home-dir "$home" --no-create-home \
    --shell /usr/sbin/nologin --comment "anki-review service" "$svc"
fi
install -d -o "$svc" -g "$svc" -m 0700 "$home"

echo "== binary $bin_dst"
install -o root -g root -m 0755 "$bin_src" "$bin_dst.new"
mv -f "$bin_dst.new" "$bin_dst"

as_svc() {
  # Runs from the service home: root's cwd may be a directory the service user can't read.
  (cd "$home" && runuser -u "$svc" -- env -i HOME="$home" PATH=/usr/local/bin:/usr/bin:/bin \
    LANG=C.UTF-8 DISABLE_AUTOUPDATER=1 "$@")
}

echo "== Claude Code $claude_version for $svc"
installed=""
if [[ -x $claude_bin ]]; then
  installed="$(as_svc "$claude_bin" --version 2>/dev/null | awk '{print $1}' || true)"
fi
if [[ $installed == "$claude_version" ]]; then
  echo "already installed"
else
  # The official native installer checks the binary's sha256 against the release manifest.
  # shellcheck disable=SC2016 # $1 belongs to the inner bash
  as_svc bash -c 'set -euo pipefail; curl -fsSL https://claude.ai/install.sh | bash -s -- "$1"' installer "$claude_version"
  [[ -x $claude_bin ]] || fail "claude is not at $claude_bin after the install"
  as_svc "$claude_bin" --version
fi

echo "== env $env_file"
install -d -o root -g "$svc" -m 0750 "$env_dir"
if [[ ! -f $env_file ]]; then
  tmp="$(mktemp "$env_dir/.env.XXXXXX")"
  chown root:"$svc" "$tmp"
  chmod 0640 "$tmp"
  cat >"$tmp" <<EOF
# anki-review service environment (systemd EnvironmentFile). Edit: sudoedit $env_file
PORT=8767
DATA_DIR=$home/data
HOME=$home
KNOWLEDGE_DECKS=Tech,Career
ANKI_CONNECT_URL=http://127.0.0.1:8765
ANKI_CONNECT_KEY=
ANKI_SYNC=true
CLAUDE_BIN=$claude_bin
GRADE_TIMEOUT=5m
LOG_LEVEL=info
# Updates come from rerunning install.sh with CLAUDE_VERSION, not from claude itself.
DISABLE_AUTOUPDATER=1
# Filled in by install.sh from $token_file.
CLAUDE_CODE_OAUTH_TOKEN=
EOF
  mv -f "$tmp" "$env_file"
fi
check_upload "$key_file"
key="$(trim "$(<"$key_file")")"
[[ -n $key ]] || fail "$key_file is empty"
set_env_line ANKI_CONNECT_KEY "$key"
unset key
for name in "${settings[@]}"; do
  if [[ -n ${!name:-} ]]; then
    set_env_line "$name" "${!name}"
  fi
done
port="$(env_value PORT)"
[[ $port =~ ^[0-9]+$ ]] || fail "PORT in $env_file is not a number"
api="http://127.0.0.1:$port"

echo "== unit $unit_dst"
unit="$(<"$unit_src")"
tmp="$(mktemp /etc/systemd/system/.anki-review.XXXXXX)"
printf '%s\n' "${unit//@DEPLOY_DIR@/"$deploy_dir"}" >"$tmp"
chmod 0644 "$tmp"
mv -f "$tmp" "$unit_dst"
systemctl daemon-reload
systemd-analyze verify "$unit_dst" || echo "install.sh: systemd-analyze reported the issues above" >&2

echo "== polkit rule $polkit_rule"
# Lets the deploy user restart the service after a binary swap without sudo. Nothing else: one
# user, one unit, three verbs.
manager="${SUDO_USER:-}"
[[ -n $manager && $manager != root ]] || manager="$deploy_user"
pk_version="$(pkaction --version 2>/dev/null | awk '{print $NF}' || true)"
pk_minor="${pk_version#0.}"
pk_minor="${pk_minor%%.*}"
if [[ $manager == root ]]; then
  polkit_state="skipped: no deploy user (run install.sh through sudo as that user)"
elif [[ ! $manager =~ ^[a-z_][a-z0-9_-]*$ ]]; then
  polkit_state="skipped: the user name $manager does not fit a polkit rule safely"
elif [[ -z $pk_version ]]; then
  polkit_state="skipped: polkit is not installed, updates need sudo systemctl restart $svc"
elif [[ $pk_version == 0.* && (! $pk_minor =~ ^[0-9]+$ || $pk_minor -lt 106) ]]; then
  polkit_state="skipped: polkit $pk_version reads no JavaScript rules, updates need sudo systemctl restart $svc"
else
  [[ -d ${polkit_rule%/*} ]] || install -d -o root -g root -m 0755 "${polkit_rule%/*}"
  tmp="$(mktemp "${polkit_rule%/*}/.anki-review.XXXXXX")"
  cat >"$tmp" <<EOF
// Installed by anki-review's install.sh: $manager may start, stop and restart $svc.service
// without sudo, so a binary update needs no password. Nothing else is granted.
polkit.addRule(function (action, subject) {
  if (action.id === "org.freedesktop.systemd1.manage-units" &&
      action.lookup("unit") === "$svc.service" &&
      subject.user === "$manager") {
    var verb = action.lookup("verb");
    if (verb === "start" || verb === "stop" || verb === "restart") {
      return polkit.Result.YES;
    }
  }
});
EOF
  chmod 0644 "$tmp"
  if cmp -s "$tmp" "$polkit_rule"; then
    rm -f "$tmp"
    polkit_state="installed for $manager, unchanged"
  else
    mv -f "$tmp" "$polkit_rule"
    polkit_state="installed for $manager (polkit $pk_version)"
  fi
fi
echo "$polkit_state"

echo "== Claude token"
if [[ -e $token_file || -L $token_file ]]; then
  check_upload "$token_file"
fi
if [[ -s $token_file ]]; then
  token="$(trim "$(<"$token_file")")"
  [[ -n $token ]] || fail "$token_file holds only whitespace"
  [[ $token != *[[:space:]]* ]] || fail "$token_file holds more than one token"
  set_env_line CLAUDE_CODE_OAUTH_TOKEN "$token"
  unset token
  if command -v shred >/dev/null 2>&1; then
    shred -u "$token_file"
  else
    rm -f "$token_file"
  fi
  echo "token moved into $env_file, upload removed"
fi
if ! grep -qE '^CLAUDE_CODE_OAUTH_TOKEN=[^[:space:]]' "$env_file"; then
  echo "install.sh: no Claude token yet. Run \`claude setup-token\` where you are logged in to Claude," >&2
  echo "copy the token and upload it from that machine (macOS shown; any stdin works):" >&2
  echo "  pbpaste | ssh [-p <ssh-port>] $deploy_user@<this-host> 'umask 077; cat > $token_file'" >&2
  echo "then rerun: sudo $deploy_dir/install.sh $deploy_dir" >&2
  exit 1
fi

# Progress the deploy user can read (the env file is root:anki-review 0640). No secrets in it.
# mktemp + mv: a link planted in the deploy dir gets replaced, never written through.
tmp="$(mktemp "$deploy_dir/.install-state.XXXXXX")"
printf '# written by install.sh, no secrets\ntoken=present\npolkit=%s\n' "$polkit_state" >"$tmp"
chmod 0644 "$tmp"
mv -f "$tmp" "$deploy_dir/install-state"

echo "== smoke test: claude as $svc in the service sandbox"
if ! out="$("$smoke" 2>&1)" || [[ ! ${out^^} =~ (^|[^[:alpha:]])OK([^[:alpha:]]|$) ]]; then
  echo "install.sh: smoke test failed, service not started. Output:" >&2
  # ${out: -2000} would be empty for a shorter output
  ((${#out} <= 2000)) || out="${out: -2000}"
  printf '%s\n' "$out" >&2
  exit 1
fi
echo "claude answered: $out"

echo "== start $svc"
systemctl enable --quiet "$svc"
if systemctl is-active --quiet "$svc"; then
  systemctl restart "$svc"
else
  systemctl start "$svc"
fi
cards=""
for _ in $(seq 1 30); do
  if cards="$(curl -fsS -m 2 "$api/api/cards" 2>/dev/null)"; then
    break
  fi
  cards=""
  sleep 1
done
[[ -n $cards ]] || fail "$api/api/cards gave no answer in 30s: journalctl -u $svc -n 50"
decks="$(curl -fsS -m 30 "$api/api/decks" 2>/dev/null || true)"

echo
echo "service: $(systemctl is-active "$svc"), listening on $api"
if command -v python3 >/dev/null 2>&1; then
  printf '%s' "$cards" | python3 -I -c 'import json, sys; print("api stage:", json.load(sys.stdin).get("stage"))'
  printf '%s' "$decks" | python3 -I -c '
import json, sys
try:
    print("decks via AnkiConnect:", len(json.load(sys.stdin)["decks"]))
except (ValueError, KeyError, TypeError):
    print("decks via AnkiConnect: no answer, see journalctl -u anki-review")
'
else
  echo "api cards: ${cards:0:200}"
  echo "api decks: ${decks:0:200}"
fi
