#!/usr/bin/env bash
# Installs AnkiConnect into the local Anki as add-on 2055492159, from the release the deploy kit pins
# in deploy/anki/Dockerfile (git.sr.ht archive, commit and sha256 checked). Refuses when the add-on
# folder exists already. Anki loads it after a restart. Writes nothing else and never talks to Anki.
#
# Usage: install-ankiconnect.sh   (ANKI_BASE: Anki's base folder, when it is not the default one)
set -euo pipefail

die() {
  echo "install-ankiconnect.sh: $*" >&2
  exit 1
}

root=$(cd -- "$(dirname -- "$0")/../../.." && pwd -P)
dockerfile=$root/deploy/anki/Dockerfile
[ -f "$dockerfile" ] || die "missing $dockerfile"
commit=$(sed -n 's/^ARG ANKICONNECT_COMMIT=//p' "$dockerfile")
want=$(sed -n 's/^ARG ANKICONNECT_SHA256=//p' "$dockerfile")
[[ $commit =~ ^[0-9a-f]{40}$ ]] || die "no ANKICONNECT_COMMIT in $dockerfile"
[[ $want =~ ^[0-9a-f]{64}$ ]] || die "no ANKICONNECT_SHA256 in $dockerfile"

if [ -n "${ANKI_BASE:-}" ]; then
  base=$ANKI_BASE
elif [ "$(uname -s)" = Darwin ]; then
  base="$HOME/Library/Application Support/Anki2"
elif [ -d "$HOME/.var/app/net.ankiweb.Anki/data/Anki2" ]; then
  base="$HOME/.var/app/net.ankiweb.Anki/data/Anki2"
else
  base="${XDG_DATA_HOME:-$HOME/.local/share}/Anki2"
fi
[ -d "$base" ] || die "no Anki folder at $base: start Anki once, or set ANKI_BASE"
dest=$base/addons21/2055492159
[ ! -e "$dest" ] || die "$dest exists already: restart Anki, or check Tools, Add-ons"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/ankiconnect.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
curl -fsSL --retry 3 -m 120 -o "$tmp/ankiconnect.tar.gz" \
  "https://git.sr.ht/~foosoft/anki-connect/archive/$commit.tar.gz"
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/ankiconnect.tar.gz")
else
  got=$(shasum -a 256 "$tmp/ankiconnect.tar.gz")
fi
got=${got%% *}
[ "$got" = "$want" ] || die "sha256 mismatch: got $got, want $want"
mkdir "$tmp/src"
tar -xzf "$tmp/ankiconnect.tar.gz" -C "$tmp/src" --strip-components=1
[ -f "$tmp/src/plugin/__init__.py" ] || die "the archive has no plugin/__init__.py"

mkdir -p "$base/addons21"
cp -R "$tmp/src/plugin" "$dest.partial"
mv "$dest.partial" "$dest"
echo "installed AnkiConnect ${commit:0:12} (sha256 ok) into $dest: restart Anki to load it"
