# syntax=docker/dockerfile:1
# Headless Anki desktop with AnkiConnect for the anki-review service. The display is Xvnc with a VNC
# password, kept for the one-time AnkiWeb login and full download, which AnkiConnect can't do.

ARG DEBIAN=debian:trixie-slim@sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad

FROM ${DEBIAN} AS fetch
# Official Linux bundle; the sha256 is the digest GitHub lists for the release asset.
ARG ANKI_VERSION=26.09.3
ARG ANKI_SHA256=d70e01971a7aa190b189927573ee643f2525ff840eafaecc7a8f8cde505650b3
# AnkiConnect 26.10.14.0, the release of add-on 2055492159 on AnkiWeb at the time of writing.
ARG ANKICONNECT_COMMIT=760ff48d8bbcfbc956f57c40475a8ab960c36a9c
ARG ANKICONNECT_SHA256=0caee0ff09f60b902e2e324bcc63bece1ffdca24249f53dfb53539cedb26c894

RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl zstd \
  && rm -rf /var/lib/apt/lists/*
RUN set -eux; \
  curl -fsSL -o /tmp/anki.tar.zst \
    "https://github.com/ankitects/anki/releases/download/${ANKI_VERSION}/anki-${ANKI_VERSION}-linux-x86_64.tar.zst"; \
  echo "${ANKI_SHA256}  /tmp/anki.tar.zst" | sha256sum -c -; \
  mkdir /opt/anki; \
  tar -x --zstd -f /tmp/anki.tar.zst -C /opt/anki --strip-components=1; \
  curl -fsSL -o /tmp/ankiconnect.tar.gz \
    "https://git.sr.ht/~foosoft/anki-connect/archive/${ANKICONNECT_COMMIT}.tar.gz"; \
  echo "${ANKICONNECT_SHA256}  /tmp/ankiconnect.tar.gz" | sha256sum -c -; \
  mkdir /tmp/ankiconnect; \
  tar -xzf /tmp/ankiconnect.tar.gz -C /tmp/ankiconnect --strip-components=1; \
  cp -R /tmp/ankiconnect/plugin /opt/ankiconnect

FROM ${DEBIAN}
# Qt/xcb and QtWebEngine libraries from Anki's install.sh, plus the VNC server and a window manager.
RUN apt-get update \
  && apt-get install -y --no-install-recommends \
    ca-certificates fonts-dejavu-core openbox tigervnc-standalone-server tigervnc-tools \
    libasound2t64 libdbus-1-3 libegl1 libfontconfig1 libfreetype6 libgl1 libglib2.0-0t64 libnss3 \
    libxcb-cursor0 libxcb-icccm4 libxcb-image0 libxcb-keysyms1 libxcb-randr0 libxcb-render-util0 \
    libxcb-shape0 libxcb-xinerama0 libxcb-xkb1 libxcomposite1 libxcursor1 libxdamage1 libxi6 \
    libxkbcommon0 libxkbcommon-x11-0 libxkbfile1 libxrandr2 libxrender1 libxtst6 \
  && rm -rf /var/lib/apt/lists/*

# The ids of the host user that runs compose: it bind-mounts secret files with their host owner and
# mode 0600, so the container user must be the same uid to read them. up.sh passes them in.
ARG ANKI_UID=1000
ARG ANKI_GID=1000
RUN groupadd -g "${ANKI_GID}" anki \
  && useradd -u "${ANKI_UID}" -g "${ANKI_GID}" -d /home/anki -s /usr/sbin/nologin anki \
  && install -d -o anki -g anki -m 0700 /data /home/anki

COPY --from=fetch /opt/anki /opt/anki
COPY --from=fetch /opt/ankiconnect /opt/ankiconnect
# --chmod also sets the mode of the directory COPY creates: 0644 would leave it without the x bit
# and the anki user could not open the entrypoint.
COPY --chmod=0755 entrypoint.sh setup-profile.py /usr/local/lib/anki/
RUN /opt/anki/python/bin/python3 -I -c 'import pickle, sqlite3' \
  && setpriv --reuid=anki --regid=anki --clear-groups \
    test -r /usr/local/lib/anki/entrypoint.sh -a -r /usr/local/lib/anki/setup-profile.py

# The container is the sandbox: Chromium's own needs user namespaces, which Docker's seccomp blocks.
ENV LANG=C.UTF-8 HOME=/home/anki DISPLAY=:1 QTWEBENGINE_DISABLE_SANDBOX=1
USER anki
VOLUME /data
EXPOSE 5900 8765
ENTRYPOINT ["/bin/sh", "/usr/local/lib/anki/entrypoint.sh"]
