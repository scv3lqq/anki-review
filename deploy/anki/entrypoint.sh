#!/bin/sh
# Starts Xvnc (password from the vnc_password secret), openbox and Anki on the profile in /data.
set -eu
umask 077

base=/data
addon="$base/addons21/ankiconnect"

# A fresh pinned copy on every start: a GUI update or a config edit can't change the key or the bind.
mkdir -p "$base/addons21"
rm -rf "$addon"
cp -R /opt/ankiconnect "$addon"
/opt/anki/python/bin/python3 -I /usr/local/lib/anki/setup-profile.py "$base" /run/secrets/ankiconnect_key

vncpasswd -f </run/secrets/vnc_password >/tmp/vncpasswd
# Listens on all container interfaces: compose publishes 5900 on 127.0.0.1 only.
Xvnc :1 -rfbport 5900 -SecurityTypes VncAuth -rfbauth /tmp/vncpasswd -AlwaysShared \
  -geometry 1440x900 -depth 24 -desktop anki -nolisten tcp &

i=0
while [ ! -S /tmp/.X11-unix/X1 ]; do
  i=$((i + 1))
  if [ "$i" -gt 100 ]; then
    echo "entrypoint: Xvnc did not start" >&2
    exit 1
  fi
  sleep 0.1
done

openbox --sm-disable >/dev/null 2>&1 &
exec /opt/anki/anki -b "$base"
