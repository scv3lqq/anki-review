#!/usr/bin/env bash
# Starts headless Anki + AnkiConnect for anki-review as the current user (a member of the docker
# group), and in the self-hosted mode Anki's own sync server next to it: creates the AnkiConnect key,
# the VNC password and the sync server password if missing, builds and starts the compose project,
# waits for AnkiConnect (and the sync server) and reports the deck count. Prints no secrets.
# Idempotent. The mode is kept in anki/sync-mode: without an argument the last one, AnkiWeb at first.
# Usage: /abs/path/anki/up.sh [ankiweb|self-hosted]
#   (env: ANKI_CONNECT_PORT=8765, VNC_PORT=5900, SYNC_PORT=8766, all on 127.0.0.1)
set -euo pipefail

fail() { echo "up.sh: $*" >&2; exit 1; }
[[ $EUID -ne 0 ]] || fail "run as the deploy user, not root: compose maps the secrets to its uid"

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
secrets="$here/secrets"
key_file="$secrets/ankiconnect-key"
vnc_file="$secrets/vnc-password"
sync_file="$secrets/sync-password"
mode_file="$here/sync-mode"
container=anki-desktop
sync_container=anki-sync

mode="${1:-}"
if [[ -z $mode && -s $mode_file ]]; then
  mode="$(<"$mode_file")"
  mode="${mode//[[:space:]]/}"
fi
mode="${mode:-ankiweb}"
[[ $mode == ankiweb || $mode == self-hosted ]] || fail "bad sync mode: $mode (ankiweb or self-hosted)"

ANKI_UID="$(id -u)"
ANKI_GID="$(id -g)"
ANKI_CONNECT_PORT="${ANKI_CONNECT_PORT:-8765}"
VNC_PORT="${VNC_PORT:-5900}"
SYNC_PORT="${SYNC_PORT:-8766}"
export ANKI_UID ANKI_GID ANKI_CONNECT_PORT VNC_PORT SYNC_PORT
for p in "$ANKI_CONNECT_PORT" "$VNC_PORT" "$SYNC_PORT"; do
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
if [[ $mode == self-hosted && -z "$(docker ps -aq --filter "name=^${sync_container}$")" ]] && command -v ss >/dev/null 2>&1; then
  [[ -z "$(ss -Htln "sport = :$SYNC_PORT")" ]] || fail "port $SYNC_PORT is already in use"
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
# Created in both modes: compose wants the secret's file even while the sync service is off.
if [[ ! -s $sync_file ]]; then
  pw="$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24 || true)"
  [[ ${#pw} -eq 24 ]] || fail "could not generate the sync server password"
  printf '%s\n' "$pw" >"$sync_file"
  unset pw
  echo "created $sync_file"
fi
chmod 0600 "$key_file" "$vnc_file" "$sync_file"
printf '%s\n' "$mode" >"$mode_file"
key="$(<"$key_file")"
key="${key//[[:space:]]/}"

compose=(docker compose --project-directory "$here" -f "$here/compose.yaml")
if [[ $mode == self-hosted ]]; then
  "${compose[@]}" --profile self-hosted up -d --build
else
  "${compose[@]}" up -d --build
  # Back on AnkiWeb: the sync server stops, its volume (the server's copy) stays.
  if [[ -n "$(docker ps -aq --filter "name=^${sync_container}$")" ]]; then
    "${compose[@]}" --profile self-hosted rm -s -f sync
  fi
fi

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

if [[ $mode == self-hosted ]]; then
  sync_ready=0
  for _ in $(seq 1 30); do
    if curl -fsS -m 5 -o /dev/null "http://127.0.0.1:$SYNC_PORT/health" 2>/dev/null; then
      sync_ready=1
      break
    fi
    sleep 2
  done
  [[ $sync_ready -eq 1 ]] || fail "the sync server did not answer on 127.0.0.1:$SYNC_PORT in a minute: docker logs $sync_container"
fi

decks="$(anki_call deckNames || true)"
count="?"
if command -v python3 >/dev/null 2>&1; then
  count="$(printf '%s' "$decks" | python3 -I -c 'import json, sys; print(len(json.load(sys.stdin)["result"]))' 2>/dev/null || echo "?")"
fi
unset key

echo
echo "AnkiConnect: ready on 127.0.0.1:$ANKI_CONNECT_PORT, key in $key_file"
echo "VNC: 127.0.0.1:$VNC_PORT on this host, password in $vnc_file"
if [[ $mode == self-hosted ]]; then
  echo "sync: self-hosted on 127.0.0.1:$SYNC_PORT, login anki, password in $sync_file; Anki here uses http://sync:8766/"
else
  echo "sync: AnkiWeb"
fi
echo "decks: $count"
if [[ $count == "1" || $count == "0" ]]; then
  if [[ $mode == self-hosted ]]; then
    echo "The profile looks empty: in the VNC GUI set the self-hosted sync server to http://sync:8766/, log in as anki and download the collection (deploy/README.md)."
  else
    echo "The profile looks empty: log in to AnkiWeb in the VNC GUI and download the collection (deploy/README.md)."
  fi
fi
