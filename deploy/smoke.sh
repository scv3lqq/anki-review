#!/usr/bin/env bash
# Runs `claude --version` once as anki-review with the service's env file and sandbox, which proves
# the binary starts under the hardening. No token is needed for that: the tokens arrive later through
# the setup page, so the env file holds none yet. install.sh runs it before it enables the service.
# Usage: sudo /abs/path/smoke.sh
set -euo pipefail

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

systemd-run --quiet --pipe --wait --collect --service-type=exec "${props[@]}" \
  /var/lib/anki-review/.local/bin/claude --version </dev/null
