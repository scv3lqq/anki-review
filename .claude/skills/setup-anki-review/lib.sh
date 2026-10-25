# Shared by check.sh and server.sh (sourced, bash 3.2 compatible).
# shellcheck shell=bash

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)
os=$(uname -s)
fails=0

trim() {
  local s=$1
  s=${s#"${s%%[![:space:]]*}"}
  s=${s%"${s##*[![:space:]]}"}
  printf '%s' "$s"
}

line() { printf '%-4s  %-9s %s\n' "$1" "$2" "$3"; }
ok() { line ok "$1" "$2"; }
info() { line info "$1" "$2"; }
warn() { line warn "$1" "$2"; }
fail() {
  line FAIL "$1" "$2"
  fails=$((fails + 1))
}

# Loads the known settings from .env the way `make run` sees them (-include .env): NAME=value
# lines, a value ends at #, blanks around it dropped, and .env wins over the environment.
dotenv_count=0
load_dotenv() {
  local f=$root/.env l name value
  [ -f "$f" ] || return 0
  while IFS= read -r l || [ -n "$l" ]; do
    l=${l#"${l%%[![:space:]]*}"}
    l=${l#export }
    case $l in '' | '#'*) continue ;; esac
    name=$(trim "${l%%=*}")
    [ "$name" != "$l" ] || continue
    case $name in
    PORT | DATA_DIR | KNOWLEDGE_DECKS | ANKI_CONNECT_URL | ANKI_CONNECT_KEY | ANKI_SYNC | CLAUDE_BIN | \
      CLAUDE_MODEL | GRADE_TIMEOUT | LOG_LEVEL | DEPLOY_SSH | DEPLOY_SSH_PORT | DEPLOY_DIR | DEPLOY_PORT | DEPLOY_URL) ;;
    *) continue ;;
    esac
    # make drops the blanks after = but keeps those before a # or the line end: the server gets
    # them too, and only ANKI_CONNECT_KEY is not trimmed there.
    value=${l#*=}
    value=${value%$'\r'}
    value=${value%%#*}
    value=${value#"${value%%[![:space:]]*}"}
    printf -v "$name" '%s' "$value"
    export "${name?}"
    dotenv_count=$((dotenv_count + 1))
  done <"$f"
}

# On a Mac without the command line tools /usr/bin/python3 is a stub that opens an installer.
have_python() {
  command -v python3 >/dev/null 2>&1 || return 1
  [ "$os" != Darwin ] || xcode-select -p >/dev/null 2>&1
}
