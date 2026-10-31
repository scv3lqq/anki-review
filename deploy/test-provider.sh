#!/usr/bin/env bash
# Offline deployment contracts. Never sources installers or invokes either grading API.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$root/deploy/provider.sh"
assert_equal() { [[ $1 == "$2" ]] || { echo "FAIL: $3" >&2; exit 1; }; }
assert_equal "$(resolve_provider '' '')" claude default
if resolve_provider invalid '' >/dev/null 2>&1; then echo 'FAIL: invalid provider accepted'; exit 1; fi
assert_equal "$(resolve_provider '' codex)" codex retention
assert_equal "$(resolve_provider claude codex)" claude override
assert_equal "$(provider_bin '' '' /srv/service claude)" /srv/service/.local/bin/claude claude
assert_equal "$(provider_bin '' '' /srv/service codex)" /srv/service/.local/share/codex/0.166.0/bin/codex codex
assert_equal "$(provider_bin /opt/custom/codex /old /srv/service codex)" /opt/custom/codex explicit
assert_equal "$(provider_bin '' /opt/old/claude /srv/service claude)" /opt/old/claude retained
assert_equal "$(codex_archive_url)" https://github.com/openai/codex/releases/download/rust-v0.166.0/codex-package-x86_64-unknown-linux-musl.tar.gz release
assert_equal "$(codex_archive_sha256)" 1d44b28bc4ac9937d7d3532264baa68a507ce364956a0d4ebb856f331cdee254 checksum
unit="$root/deploy/systemd/anki-review.service"
for property in RuntimeDirectory=anki-review RuntimeDirectoryMode=0700 Environment=TMPDIR=/run/anki-review Environment=HOME=/var/lib/anki-review NoNewPrivileges=yes ProtectSystem=strict ProtectHome=yes RestrictNamespaces=yes; do
  grep -Fxq "$property" "$unit" || { echo "FAIL: missing $property"; exit 1; }
done
for tree in .agents .claude; do
  grep -q 'provider.sh test-provider.sh' "$root/$tree/skills/setup-anki-review/server.sh"
  grep -q 'CODEX_BIN' "$root/$tree/skills/setup-anki-review/lib.sh"
done
grep -q 'for provider in claude codex' "$root/deploy/smoke.sh"
grep -q 'codex_archive_sha256' "$root/deploy/install.sh"
fixture="$(mktemp -d)"
trap 'rm -rf -- "$fixture"' EXIT
# Match the official full package, not the unrelated legacy resources/ layout.
package="$fixture/package"
mkdir -p "$package/bin" "$package/codex-resources"
touch "$package/bin/codex" "$package/bin/codex-exec-server" "$package/codex-package.json"
chmod +x "$package/bin/codex" "$package/bin/codex-exec-server"
codex_package_valid "$package" || { echo 'FAIL: official package rejected'; exit 1; }
rmdir "$package/codex-resources"
if codex_package_valid "$package"; then echo 'FAIL: missing resources accepted'; exit 1; fi
mkdir "$package/resources"
if codex_package_valid "$package"; then echo 'FAIL: legacy resources-only layout accepted'; exit 1; fi
mkdir "$package/codex-resources"
rm "$package/codex-package.json"
if codex_package_valid "$package"; then echo 'FAIL: missing manifest accepted'; exit 1; fi
touch "$package/codex-package.json"
rm "$package/bin/codex-exec-server"
if codex_package_valid "$package"; then echo 'FAIL: missing sibling accepted'; exit 1; fi
touch "$package/bin/codex-exec-server"
if codex_package_valid "$package"; then echo 'FAIL: non-executable sibling accepted'; exit 1; fi
chmod +x "$package/bin/codex-exec-server"
chmod -x "$package/bin/codex"
if codex_package_valid "$package"; then echo 'FAIL: non-executable CLI accepted'; exit 1; fi
cat >"$fixture/codex" <<'CODEX'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$CALL_LOG"
[[ $* == --version ]] || exit 1
printf 'codex-cli 0.166.0\n'
CODEX
cat >"$fixture/claude" <<'CLAUDE'
#!/usr/bin/env bash
echo 'FAIL: Claude invoked for selected Codex' >&2
exit 1
CLAUDE
chmod +x "$fixture/codex" "$fixture/claude"
export CALL_LOG="$fixture/calls"
for tree in .agents .claude; do
  source "$root/$tree/skills/setup-anki-review/lib.sh"
  check_grader codex "$fixture/claude" "$fixture/codex" >/dev/null
  [[ $fails == 0 ]] || exit 1
done
assert_equal "$(cat "$CALL_LOG")" $'--version\n--version' 'Codex diagnostics use only --version'
printf 'PASS: provider defaults, validation, retention, override, paths, release, runtime and kit contracts\n'
