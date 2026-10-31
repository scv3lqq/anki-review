#!/usr/bin/env bash
# Pure deployment decisions, safe to source in offline fixtures. No environment file is executed.
resolve_provider() {
  local provider="${1:-${2:-claude}}"
  case $provider in claude | codex) printf '%s' "$provider" ;; *) return 1 ;; esac
}

provider_bin() {
  local explicit="$1" retained="$2" service_home="$3" provider="$4"
  if [[ -n $explicit ]]; then printf '%s' "$explicit"; return; fi
  if [[ -n $retained ]]; then printf '%s' "$retained"; return; fi
  case $provider in
  claude) printf '%s/.local/bin/claude' "$service_home" ;;
  codex) printf '%s/.local/share/codex/0.166.0/bin/codex' "$service_home" ;;
  *) return 1 ;;
  esac
}

codex_archive_url() {
  printf '%s' 'https://github.com/openai/codex/releases/download/rust-v0.166.0/codex-package-x86_64-unknown-linux-musl.tar.gz'
}
codex_archive_sha256() {
  printf '%s' '1d44b28bc4ac9937d7d3532264baa68a507ce364956a0d4ebb856f331cdee254'
}

# The official full archive keeps resources and metadata beside the bin directory.
codex_package_valid() {
  local directory="$1"
  [[ -x $directory/bin/codex && -x $directory/bin/codex-exec-server &&
    -d $directory/codex-resources && -f $directory/codex-package.json ]]
}

# Read only the requested non-secret setting; never source an EnvironmentFile.
provider_env_value() {
  local file="$1" name="$2" line
  [[ -f $file ]] || return 0
  while IFS= read -r line || [[ -n $line ]]; do
    if [[ $line == "$name="* ]]; then printf '%s' "${line#*=}"; return; fi
  done <"$file"
}
