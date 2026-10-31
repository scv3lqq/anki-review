#!/usr/bin/env bash
# Runs both grading CLI --version checks as anki-review with the service's environment and sandbox.
# No auth or grading request is made. Saved provider selection stays encrypted in the vault.
# Usage: sudo /abs/path/smoke.sh
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/provider.sh"

fail() { echo "smoke.sh: $*" >&2; exit 1; }
[[ $EUID -eq 0 ]] || fail "run as root: sudo $0"

unit=/etc/systemd/system/anki-review.service
[[ -f $unit ]] || fail "$unit is missing: run install.sh first"

# Every [Service] setting except the start/restart lines becomes a property of a transient unit.
props=()
while IFS= read -r line; do
  case "$line" in
    '' | '#'* | '['* | ExecStart* | Restart* | Type=*) continue ;;
  esac
  props+=(-p "$line")
done < <(sed -n '/^\[Service\]/,/^\[/p' "$unit")

env_file=/etc/anki-review/env
selected="$(resolve_provider '' "$(provider_env_value "$env_file" GRADER_PROVIDER)")" || fail "invalid GRADER_PROVIDER"
echo "initial setup provider: $selected (saved selection stays encrypted)"
for provider in claude codex; do
  case $provider in claude) name=CLAUDE_BIN ;; codex) name=CODEX_BIN ;; esac
  binary="$(provider_bin '' "$(provider_env_value "$env_file" "$name")" /var/lib/anki-review "$provider")"
  [[ $binary == /* && -x $binary ]] || fail "$name must be an executable absolute path"
  # RuntimeDirectoryPreserve avoids removing the live application's runtime on an offline smoke run.
  version="$(systemd-run --quiet --pipe --wait --collect --service-type=exec "${props[@]}" \
    -p RuntimeDirectoryPreserve=yes "$binary" --version </dev/null)" || fail "$provider cannot start in the service sandbox"
  if [[ $provider == codex ]]; then
    [[ $version == 'codex-cli 0.166.0' ]] || fail "Codex version must be 0.166.0"
  else
    [[ $version =~ [0-9]+\.[0-9]+\.[0-9]+ ]] || fail "Claude version unavailable"
  fi
  printf '%s version: %s\n' "$provider" "$version"
done
