#!/bin/sh
# Runs the sync server built into Anki on /sync, for the login anki with the password from the
# sync_password secret. Anki takes its users only from SYNC_USER1..N, so the password has to reach it
# through the environment.
set -eu
umask 077

pw=$(tr -d '[:space:]' </run/secrets/sync_password)
if [ -z "$pw" ]; then
  echo "syncserver: the sync_password secret is empty" >&2
  exit 1
fi
export SYNC_USER1="anki:$pw" SYNC_BASE=/sync SYNC_HOST=0.0.0.0 SYNC_PORT=8766
unset pw
exec /opt/anki/anki --syncserver
