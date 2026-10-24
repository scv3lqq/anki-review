# Deploy kit

Runs anki-review on a Linux server. Anki desktop with AnkiConnect runs headless in Docker. The server
runs as a hardened systemd service under its own system user, with its own Claude Code install.

With Claude Code, `/setup-anki-review server` runs every step below from your machine; you only type
the AnkiWeb password in the VNC window, authorize `claude setup-token` in the browser and type the
sudo password. `/setup-anki-review server-update` ships a new build without any of them.

| Path | What |
| --- | --- |
| `anki/` | Anki image: `Dockerfile`, `compose.yaml`, `entrypoint.sh`, `setup-profile.py`. `up.sh` creates the secrets and starts it |
| `install.sh` | root: service user, Claude Code, `/etc/anki-review/env`, the unit, a polkit rule for restarts, smoke test, start |
| `smoke.sh` | root: one `claude -p` call as the service user, in the service's sandbox |
| `systemd/anki-review.service` | unit template, `@DEPLOY_DIR@` is filled in by `install.sh` |

## Prerequisites

- On the server: Linux x86_64 with systemd, Docker Engine with the compose plugin, curl. A deploy
  user in the `docker` group with sudo. Outbound HTTPS to GitHub, git.sr.ht, the Debian mirrors,
  claude.ai, AnkiWeb and the Anthropic API. Optional: polkit 0.106 or newer, for updates without
  sudo.
- On your machine: Go, SSH access as the deploy user, and Claude Code logged in to your account.

## Steps

Below, `SSH` stands for `ssh -p <port> <user>@<server>` and `DIR` for the deploy dir,
`/home/<user>/anki-review-deploy` by default. Always write `DIR` as an absolute path: a local
shell expands `~` before ssh sends the command.

1. Build the binary: `make build-linux`.
2. Copy the kit and the binary (`scp` takes `-P` for the port):
   `SSH 'mkdir -p DIR/bin && chmod 700 DIR'`,
   `scp -r deploy/anki deploy/systemd deploy/install.sh deploy/smoke.sh <user>@<server>:DIR/`,
   `scp bin/anki-review-linux-amd64 <user>@<server>:DIR/bin/anki-review`.
3. Start Anki as the deploy user, no sudo: `SSH DIR/anki/up.sh`. It creates the AnkiConnect key and
   the VNC password in `DIR/anki/secrets/` (0600, never printed), builds the image (the first build
   downloads about 200 MB), waits for AnkiConnect and prints the deck count.
4. **Human:** log in to AnkiWeb once. Open a tunnel with `ssh -p <port> -N -L 5901:127.0.0.1:5900
   <user>@<server>`. Read the password with `SSH cat DIR/anki/secrets/vnc-password` (append `| pbcopy`
   on macOS). Connect a VNC viewer to `127.0.0.1:5901` (`open vnc://127.0.0.1:5901` on macOS: Screen
   Sharing refuses local port 5900). In Anki, press Sync, log in, choose "Download from AnkiWeb" and
   wait for the media. A rerun of `up.sh` then shows more than one deck.
5. **Human:** run `claude setup-token`, authorize it in the browser and copy the token. Upload it
   straight from the clipboard, never as an argument:
   `pbpaste | SSH 'umask 077; cat > DIR/claude-token'`.
6. **Human, sudo password:** `ssh -t -p <port> <user>@<server> sudo DIR/install.sh DIR`. The
   script moves the token into `/etc/anki-review/env` (root:anki-review 0640), shreds the upload,
   installs the polkit rule (below), leaves `DIR/install-state` (no secrets: whether a token and the
   rule are in place), runs `smoke.sh` and only then enables the service. It ends with
   `service: active`, `api stage: idle` and the deck count. Without a token it stops and prints the
   upload command.

The service listens on `127.0.0.1:8767` only. A reverse proxy with HTTPS and access control is up to
you, and it must send `Host: 127.0.0.1:<port>`.

## Settings and updates

- The binary: copy a new build to `DIR/bin/anki-review`, then `systemctl restart anki-review`, no
  sudo. The unit installs the staged binary to `/usr/local/bin` on every start. `install.sh` writes
  `/etc/polkit-1/rules.d/50-anki-review.rules`, which lets the deploy user (the one who ran it through
  sudo) start, stop and restart `anki-review.service` and nothing else. It needs polkit 0.106 or
  newer (`polkitd`); without it `install.sh` says so and a restart needs `sudo`.
- Server settings: `sudo env KNOWLEDGE_DECKS=Tech,Career DIR/install.sh DIR` (also `PORT`,
  `ANKI_CONNECT_URL`, `ANKI_SYNC`, `CLAUDE_MODEL`, `GRADE_TIMEOUT`, `LOG_LEVEL`), or
  `sudoedit /etc/anki-review/env` and a restart.
- Claude Code: `sudo env CLAUDE_VERSION=x.y.z DIR/install.sh DIR`. Its own auto-updater is off.
- A new token: upload it as in step 5 and rerun `install.sh`.
- Anki or AnkiConnect: change the version, commit and sha256 arguments in `anki/Dockerfile`, then
  rerun `up.sh`. Host ports: `ANKI_CONNECT_PORT` (8765) and `VNC_PORT` (5900) for `up.sh`; pass the
  same `ANKI_CONNECT_PORT` to `install.sh`.

## What protects what

- AnkiConnect and VNC are published on 127.0.0.1 only: Docker's port publishing bypasses ufw, so the
  bind is the protection. AnkiConnect needs its apiKey and VNC needs a password, which keeps out
  other local users and containers.
- The Anki container runs as a non-root user with a read-only root filesystem, no capabilities,
  `no-new-privileges` and a 1 GB memory limit. The base image is pinned by digest, and the Anki and
  AnkiConnect downloads by sha256.
- The polkit rule grants one user three verbs on one unit: no other unit, no enable, disable or
  edit. The deploy user already owns `DIR`, whose binary the unit runs, and has sudo.
- The service runs as `anki-review` under a hardened unit: `ProtectSystem=strict`, `ProtectHome`, no
  capabilities, private `/tmp` and devices. Its only writable path is `/var/lib/anki-review`. The
  token lives only in the env file and never passes through a command line.
