#!/usr/bin/env bash
# Starts headless Anki + AnkiConnect for anki-review as the current user (a member of the docker
# group): creates the AnkiConnect key and the VNC password if missing, builds and starts the compose
# project, waits for AnkiConnect and reports the deck count. Prints no secrets. Idempotent.
# Usage: /abs/path/anki/up.sh   (env: ANKI_CONNECT_PORT=8765, VNC_PORT=5900, both on 127.0.0.1)
set -euo pipefail

fail() { echo "up.sh: $*" >&2; exit 1; }
[[ $EUID -ne 0 ]] || fail "run as the deploy user, not root: compose maps the secrets to its uid"

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
secrets="$here/secrets"
key_file="$secrets/ankiconnect-key"
vnc_file="$secrets/vnc-password"
container=anki-desktop

ANKI_UID="$(id -u)"
ANKI_GID="$(id -g)"
ANKI_CONNECT_PORT="${ANKI_CONNECT_PORT:-8765}"
VNC_PORT="${VNC_PORT:-5900}"
export ANKI_UID ANKI_GID ANKI_CONNECT_PORT VNC_PORT
for p in "$ANKI_CONNECT_PORT" "$VNC_PORT"; do
  [[ $p =~ ^[0-9]+$ ]] || fail "bad port: $p"
done

command -v docker >/dev/null 2>&1 || fail "docker is not installed"
docker compose version >/dev/null 2>&1 || fail "the docker compose plugin is missing"
docker info >/dev/null 2>&1 || fail "docker is not reachable for $(id -un): add the user to the docker group"

# A first start must not collide with something else on the ports; a rerun owns them already.
if [[ -z "$(docker ps -aq --filter "name=^${container}$")" ]] && command -v ss >/dev/null 2>&1; then
  for p in "$ANKI_CONNECT_PORT" "$VNC_PORT"; do
    [[ -z "$(ss -Htln "sport = :$p")" ]] || fail "port $p is already in use"
  done
fi

umask 077
chmod go-rwx "$here"
mkdir -p "$secrets"
chmod 0700 "$secrets"
# Secrets go through builtins (printf) into files: never into argv, the environment or the output.
if [[ ! -s $key_file ]]; then
  key="$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')"
  [[ ${#key} -eq 64 ]] || fail "could not generate the AnkiConnect key"
  printf '%s\n' "$key" >"$key_file"
  echo "created $key_file"
fi
if [[ ! -s $vnc_file ]]; then
  # VNC authentication uses at most 8 characters.
  pw="$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 8 || true)"
  [[ ${#pw} -eq 8 ]] || fail "could not generate the VNC password"
  printf '%s\n' "$pw" >"$vnc_file"
  unset pw
  echo "created $vnc_file"
fi
chmod 0600 "$key_file" "$vnc_file"
key="$(<"$key_file")"
key="${key//[[:space:]]/}"

docker compose --project-directory "$here" -f "$here/compose.yaml" up -d --build

anki_call() {
  printf '{"action":"%s","version":6,"key":"%s"}' "$1" "$key" |
    curl -fsS -m 5 --data-binary @- "http://127.0.0.1:$ANKI_CONNECT_PORT" 2>/dev/null
}

ready=0
for _ in $(seq 1 90); do
  if [[ "$(anki_call version || true)" == *'"result": 6'* ]]; then
    ready=1
    break
  fi
  sleep 2
done
[[ $ready -eq 1 ]] || fail "AnkiConnect did not answer on 127.0.0.1:$ANKI_CONNECT_PORT in 3 minutes: docker logs $container"

decks="$(anki_call deckNames || true)"
count="?"
if command -v python3 >/dev/null 2>&1; then
  count="$(printf '%s' "$decks" | python3 -I -c 'import json, sys; print(len(json.load(sys.stdin)["result"]))' 2>/dev/null || echo "?")"
fi
unset key

echo
echo "AnkiConnect: ready on 127.0.0.1:$ANKI_CONNECT_PORT, key in $key_file"
echo "VNC: 127.0.0.1:$VNC_PORT on this host, password in $vnc_file"
echo "decks: $count"
if [[ $count == "1" || $count == "0" ]]; then
  echo "The profile looks empty: log in to AnkiWeb in the VNC GUI and download the collection (deploy/README.md)."
fi
