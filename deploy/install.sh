#!/usr/bin/env bash
# Installs and starts the anki-review service from a deploy dir: system user, both grading CLIs, /etc/anki-review/env with the settings, the hardened unit, a polkit rule that lets the deploy
# user restart the service without sudo, CLI version checks in the service sandbox, then enable
# --now. The Claude token and the AnkiConnect key are not handled here: the owner enters them on the
# setup page, where the server seals them with the owner's password. Idempotent: rerun it to change a
# setting or move Claude Code to CLAUDE_VERSION. Codex stays pinned to the validated release. A restart leaves the service locked until a login.
#
# Usage: sudo /abs/path/install.sh [DEPLOY_DIR]
#   DEPLOY_DIR: argument, else ANKI_REVIEW_DEPLOY_DIR, else <home of SUDO_USER>/anki-review-deploy.
#   It holds this kit, bin/anki-review (linux/amd64) and anki/secrets/ankiconnect-key (from up.sh;
#   only its presence is checked here, the Anki container reads it).
# Settings, through `sudo env NAME=value ... install.sh`: PORT, KNOWLEDGE_DECKS, ANKI_CONNECT_URL
# (or ANKI_CONNECT_PORT), ANKI_SYNC, CLAUDE_MODEL, CODEX_MODEL, GRADER_PROVIDER, GRADE_TIMEOUT,
# LOG_LEVEL go into the env file; explicit CLAUDE_BIN/CODEX_BIN paths are retained and validated;
# CLAUDE_VERSION=x.y.z|stable|latest overrides the pinned Claude Code version. AUTO_DEPLOY=on installs
# anki-review-deploy.timer (autodeploy.sh, it needs DEPLOY_DIR/allowed_signers, git, jq and go),
# AUTO_DEPLOY=off removes it, unset keeps it as it is.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/provider.sh"

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
claude_default="$home/.local/bin/claude"
claude_bin="$(provider_bin "${CLAUDE_BIN:-}" "$(provider_env_value "$env_file" CLAUDE_BIN)" "$home" claude)"
codex_bin="$(provider_bin "${CODEX_BIN:-}" "$(provider_env_value "$env_file" CODEX_BIN)" "$home" codex)"
provider="$(resolve_provider "${GRADER_PROVIDER:-}" "$(provider_env_value "$env_file" GRADER_PROVIDER)")" || fail "GRADER_PROVIDER must be claude or codex"
# Saved provider choice is encrypted in vault.json; this is only the initial setup default.
settings=(PORT KNOWLEDGE_DECKS ANKI_CONNECT_URL ANKI_SYNC CLAUDE_MODEL CODEX_MODEL GRADE_TIMEOUT LOG_LEVEL)
auto_deploy="${AUTO_DEPLOY:-}"
deploy_svc=anki-review-deploy
deploy_script="$deploy_dir/autodeploy.sh"
deploy_signers="$deploy_dir/allowed_signers"
deploy_unit_dst="/etc/systemd/system/$deploy_svc.service"
deploy_timer_dst="/etc/systemd/system/$deploy_svc.timer"

for f in "$bin_src" "$unit_src" "$smoke" "$deploy_dir/provider.sh"; do
  [[ -f $f ]] || fail "missing $f"
done
[[ -f $key_file ]] || fail "missing $key_file: run $deploy_dir/anki/up.sh as $deploy_user first"
[[ $claude_version =~ ^(stable|latest|[0-9]+\.[0-9]+\.[0-9]+)$ ]] || fail "bad CLAUDE_VERSION: $claude_version"
if [[ -n ${PORT:-} && ! $PORT =~ ^[0-9]+$ ]]; then
  fail "bad PORT: $PORT"
fi
[[ -z $auto_deploy || $auto_deploy == on || $auto_deploy == off ]] || fail "AUTO_DEPLOY must be on or off: $auto_deploy"
if [[ -z $auto_deploy && -f $deploy_unit_dst ]]; then
  auto_deploy=keep
fi
if [[ -n ${ANKI_CONNECT_PORT:-} && -z ${ANKI_CONNECT_URL:-} ]]; then
  [[ $ANKI_CONNECT_PORT =~ ^[0-9]+$ ]] || fail "bad ANKI_CONNECT_PORT: $ANKI_CONNECT_PORT"
  ANKI_CONNECT_URL="http://127.0.0.1:$ANKI_CONNECT_PORT"
fi

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

# Env files from the token-in-env kit hold the Claude token and the AnkiConnect key: the page takes
# them now, so the lines go. mktemp + mv like set_env_line, and no rewrite when nothing matches.
drop_secret_lines() {
  local tmp line
  grep -qE '^(CLAUDE_CODE_OAUTH_TOKEN|ANKI_CONNECT_KEY)=' "$env_file" || return 0
  tmp="$(mktemp "$env_dir/.env.XXXXXX")"
  chown root:"$svc" "$tmp"
  chmod 0640 "$tmp"
  while IFS= read -r line || [[ -n $line ]]; do
    case $line in
    CLAUDE_CODE_OAUTH_TOKEN=* | ANKI_CONNECT_KEY=*) ;;
    *) printf '%s\n' "$line" ;;
    esac
  done <"$env_file" >"$tmp"
  mv -f "$tmp" "$env_file"
  echo "removed the Claude token and the AnkiConnect key from the env file: enter them on the setup page"
}

# Before any change: everything autodeploy.sh needs on the host.
if [[ $auto_deploy == on || $auto_deploy == keep ]]; then
  for f in "$deploy_script" "$deploy_dir/systemd/$deploy_svc.service" "$deploy_dir/systemd/$deploy_svc.timer"; do
    [[ -f $f ]] || fail "missing $f"
  done
  check_upload "$deploy_signers"
  [[ -s $deploy_signers ]] || fail "$deploy_signers is empty: it lists the keys whose signed commits may deploy"
  for c in git jq go ssh-keygen; do
    command -v "$c" >/dev/null 2>&1 || fail "auto-deploy needs $c on the host"
  done
  # The name lands verbatim in the unit file.
  [[ $deploy_user =~ ^[a-z_][a-z0-9_-]*$ ]] || fail "the user name $deploy_user does not fit a unit file safely"
fi

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
if [[ $claude_bin != "$claude_default" || -n ${CLAUDE_BIN:-} ]]; then
  [[ $claude_bin == /* && -x $claude_bin ]] || fail "configured CLAUDE_BIN must be an executable absolute path"
  as_svc "$claude_bin" --version >/dev/null || fail "configured CLAUDE_BIN cannot start as $svc"
elif [[ $installed == "$claude_version" ]]; then
  echo "already installed"
else
  # The official native installer checks the binary's sha256 against the release manifest.
  # shellcheck disable=SC2016 # $1 belongs to the inner bash
  as_svc bash -c 'set -euo pipefail; curl -fsSL https://claude.ai/install.sh | bash -s -- "$1"' installer "$claude_version"
  [[ -x $claude_bin ]] || fail "claude is not at $claude_bin after the install"
  as_svc "$claude_bin" --version
fi

echo "== Codex CLI 0.166.0 for $svc"
codex_default="$(provider_bin '' '' "$home" codex)"
if [[ $codex_bin != "$codex_default" || -n ${CODEX_BIN:-} ]]; then
  [[ $codex_bin == /* && -x $codex_bin ]] || fail "configured CODEX_BIN must be an executable absolute path"
else
  installed="$(as_svc "$codex_bin" --version 2>/dev/null || true)"
  if [[ $installed != 'codex-cli 0.166.0' ]]; then
    # Preserve the complete package: codex needs its sibling exec server and resources.
    package_dir="${codex_default%/bin/codex}"
    stage="$(mktemp -d "$home/.codex-install.XXXXXX")"
    trap 'rm -rf -- "$stage"' EXIT
    curl -fsSL "$(codex_archive_url)" -o "$stage/package.tar.gz"
    printf '%s  %s\n' "$(codex_archive_sha256)" "$stage/package.tar.gz" | sha256sum --check --status || fail "Codex archive checksum mismatch"
    mkdir "$stage/package"
    tar -xzf "$stage/package.tar.gz" -C "$stage/package"
    [[ -x $stage/package/bin/codex && -x $stage/package/bin/codex-exec-server && -d $stage/package/resources ]] || fail "incomplete Codex package"
    install -d -o root -g root -m 0755 "${package_dir%/*}"
    rm -rf -- "$package_dir"
    mv "$stage/package" "$package_dir"
    chown -R root:root "$package_dir"
    chmod -R go-w "$package_dir"
    rm -rf -- "$stage"
    trap - EXIT
  fi
fi
[[ $(as_svc "$codex_bin" --version 2>/dev/null) == 'codex-cli 0.166.0' ]] || fail "CODEX_BIN must run codex-cli 0.166.0 as $svc"

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
ANKI_SYNC=true
CLAUDE_BIN=$claude_bin
CODEX_BIN=$codex_bin
GRADER_PROVIDER=$provider
GRADE_TIMEOUT=5m
LOG_LEVEL=info
# Updates come from rerunning install.sh with CLAUDE_VERSION, not from claude itself.
DISABLE_AUTOUPDATER=1
EOF
  mv -f "$tmp" "$env_file"
fi
drop_secret_lines
set_env_line GRADER_PROVIDER "$provider"
set_env_line CLAUDE_BIN "$claude_bin"
set_env_line CODEX_BIN "$codex_bin"
set_env_line HOME "$home"
set_env_line TMPDIR /run/anki-review
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

echo "== auto-deploy $deploy_svc.timer"
case $auto_deploy in
on | keep)
  # autodeploy.sh restarts the service as the deploy user, through the rule above.
  [[ $polkit_state == installed* && $manager == "$deploy_user" ]] ||
    fail "auto-deploy needs the polkit rule for $deploy_user (owner of $deploy_dir): $polkit_state"
  for f in service timer; do
    unit="$(<"$deploy_dir/systemd/$deploy_svc.$f")"
    unit="${unit//@DEPLOY_DIR@/"$deploy_dir"}"
    unit="${unit//@DEPLOY_USER@/"$deploy_user"}"
    tmp="$(mktemp /etc/systemd/system/.anki-review.XXXXXX)"
    printf '%s\n' "${unit//@PORT@/"$port"}" >"$tmp"
    chmod 0644 "$tmp"
    mv -f "$tmp" "/etc/systemd/system/$deploy_svc.$f"
  done
  systemctl daemon-reload
  systemd-analyze verify "$deploy_unit_dst" "$deploy_timer_dst" ||
    echo "install.sh: systemd-analyze reported the issues above" >&2
  [[ $auto_deploy == keep ]] || systemctl enable --now --quiet "$deploy_svc.timer"
  auto_deploy_state="timer $(systemctl is-enabled "$deploy_svc.timer" 2>/dev/null || true), deploys as $deploy_user"
  ;;
off)
  if [[ -f $deploy_timer_dst ]]; then
    systemctl disable --now --quiet "$deploy_svc.timer" || true
  fi
  rm -f "$deploy_unit_dst" "$deploy_timer_dst"
  systemctl daemon-reload
  auto_deploy_state="off"
  ;;
*) auto_deploy_state="off" ;;
esac
echo "$auto_deploy_state"

# The old upload flow may have left a token here: shredded, the owner enters it on the setup page.
echo "== leftover Claude token upload"
if [[ -e $token_file || -L $token_file ]]; then
  check_upload "$token_file"
  if command -v shred >/dev/null 2>&1; then
    shred -u "$token_file"
  else
    rm -f "$token_file"
  fi
  echo "removed $token_file: enter the token on the setup page"
fi

# Progress the deploy user can read (the env file is root:anki-review 0640). No secrets in it.
# mktemp + mv: a link planted in the deploy dir gets replaced, never written through.
tmp="$(mktemp "$deploy_dir/.install-state.XXXXXX")"
printf '# written by install.sh, no secrets\nkit=2\npolkit=%s\nautodeploy=%s\n' "$polkit_state" "$auto_deploy_state" >"$tmp"
chmod 0644 "$tmp"
mv -f "$tmp" "$deploy_dir/install-state"

# No credentials are required: smoke checks only CLI versions under the service restrictions.
echo "== smoke test: both grading CLIs as $svc in the service sandbox"
version_re='[0-9]+\.[0-9]+\.[0-9]+'
if ! out="$("$smoke" 2>&1)" || [[ ! $out =~ $version_re ]]; then
  echo "install.sh: smoke test failed, service not started. Output:" >&2
  # ${out: -2000} would be empty for a shorter output
  ((${#out} <= 2000)) || out="${out: -2000}"
  printf '%s\n' "$out" >&2
  exit 1
fi
printf '%s\n' "$out"

echo "== start $svc"
systemctl enable --quiet "$svc"
if systemctl is-active --quiet "$svc"; then
  systemctl restart "$svc"
else
  systemctl start "$svc"
fi
# /api/session needs no login, so it answers before the owner exists; the review routes need one.
session=""
for _ in $(seq 1 30); do
  if session="$(curl -fsS -m 2 "$api/api/session" 2>/dev/null)"; then
    break
  fi
  session=""
  sleep 1
done
[[ -n $session ]] || fail "$api/api/session gave no answer in 30s: journalctl -u $svc -n 50"

echo
echo "service: $(systemctl is-active "$svc"), listening on $api"
if command -v python3 >/dev/null 2>&1; then
  # Exits 0 on any answer: a failure here must not look like a failed install after the start.
  printf '%s' "$session" | python3 -I -c '
import json, sys
try:
    s = json.load(sys.stdin)
    if not isinstance(s, dict):
        raise ValueError
except ValueError:
    print("owner: no JSON from /api/session, see journalctl -u anki-review")
    sys.exit()
if s.get("state") == "setup":
    print("owner: setup needed (open the setup page)")
elif s.get("unlocked"):
    print("owner: unlocked")
else:
    print("owner: locked (log in on the page)")
if "stage" in s:
    print("api stage:", s["stage"])
'
else
  echo "api session: ${session:0:200}"
fi
