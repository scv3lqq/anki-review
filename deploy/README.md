# Deploy kit

Runs anki-review on a Linux server. Anki desktop with AnkiConnect runs headless in Docker. The server
runs as a hardened systemd service under its own system user, with its own Claude Code install.

With Claude Code, `/setup-anki-review server` runs every step below from your machine; you type the
AnkiWeb password in the VNC window, the sudo password and the setup form on the server's page (owner
login, password, Claude token, AnkiConnect key). `/setup-anki-review server-update` ships a new build
without any of them.

| Path | What |
| --- | --- |
| `anki/` | Anki image: `Dockerfile`, `compose.yaml`, `entrypoint.sh`, `setup-profile.py`. `up.sh` creates the secrets and starts it |
| `install.sh` | root: service user, Claude Code, `/etc/anki-review/env`, the unit, a polkit rule for restarts, smoke test, start |
| `smoke.sh` | root: `claude --version` as the service user, in the service's sandbox |
| `systemd/anki-review.service` | unit template, `@DEPLOY_DIR@` is filled in by `install.sh` |

## Prerequisites

- On the server: Linux x86_64 with systemd, Docker Engine with the compose plugin, curl. A deploy
  user in the `docker` group with sudo. Outbound HTTPS to GitHub, git.sr.ht, the Debian mirrors,
  claude.ai, AnkiWeb and the Anthropic API. Optional: polkit 0.106 or newer, for updates without
  sudo.
- On your machine: Go, SSH access as the deploy user, and Claude Code logged in to your account (for
  `claude setup-token`, see step 6).

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
5. **Human, sudo password:** `ssh -t -p <port> <user>@<server> sudo DIR/install.sh DIR`. The
   script installs the service user, Claude Code and the polkit rule (below), writes
   `/etc/anki-review/env` (root:anki-review 0640) without any token, leaves `DIR/install-state` (no
   secrets: the kit version and whether the rule is in place), runs `smoke.sh` and only then enables
   the service. It ends with `service: active` and `owner: setup needed (open the setup page)`.
6. **Human, setup page:** the service has no owner yet, so it stays locked until you set one up.
   Reach the page through `DEPLOY_URL` (your reverse proxy), or with an ssh tunnel:
   `ssh -p <port> -N -L 8768:127.0.0.1:8767 <user>@<server>`, then open <http://127.0.0.1:8768/>.
   (8767 is the service's `PORT`.) Fill in:
   - Login, and Password twice (at least 12 characters).
   - AnkiConnect key: `SSH cat DIR/anki/secrets/ankiconnect-key` (append `| pbcopy` on macOS), or copy
     it with `/setup-anki-review server`.
   - Claude token: on your machine, run `claude setup-token`, approve it in the browser and paste the
     `sk-ant-oat01-...` token the terminal prints. Not on the server: the token is for the account
     that grades.

   Setup checks the AnkiConnect key and the Claude token, seals both under your password and logs you
   in. The service is unlocked from then on.

The service listens on `127.0.0.1:8767` only. A reverse proxy with HTTPS is up to you (its own access
control is optional now that the page has a login). It must send `Host: 127.0.0.1:<port>`, and should
send `X-Forwarded-Proto` (the session cookie is `Secure` only then) and `X-Forwarded-For` (login
throttling counts per client by its last hop).

## Settings and updates

- The binary: copy a new build to `DIR/bin/anki-review`, then `systemctl restart anki-review`, no
  sudo. The unit installs the staged binary to `/usr/local/bin` on every start. `install.sh` writes
  `/etc/polkit-1/rules.d/50-anki-review.rules`, which lets the deploy user (the one who ran it through
  sudo) start, stop and restart `anki-review.service` and nothing else. It needs polkit 0.106 or
  newer (`polkitd`); without it `install.sh` says so and a restart needs `sudo`.
- A restart locks the service: the tokens leave memory with the process. Log in on the page to unlock
  grading again. A pass in progress resumes after the login.
- Tokens and password: the Settings screen on the page (log in first). An empty field keeps its
  current value. `install.sh` takes no token.
- Server settings: `sudo env KNOWLEDGE_DECKS=Tech,Career DIR/install.sh DIR` (also `PORT`,
  `ANKI_CONNECT_URL`, `ANKI_SYNC`, `CLAUDE_MODEL`, `GRADE_TIMEOUT`, `LOG_LEVEL`), or
  `sudoedit /etc/anki-review/env` and a restart.
- Claude Code: `sudo env CLAUDE_VERSION=x.y.z DIR/install.sh DIR`. Its own auto-updater is off.
- An older deploy kept the Claude token and the AnkiConnect key in `/etc/anki-review/env`: rerun
  `install.sh`, which removes those two lines and says so, then set up on the page (step 6). A
  leftover `DIR/claude-token` from the old upload step is shredded by the same run.
- Anki or AnkiConnect: change the version, commit and sha256 arguments in `anki/Dockerfile`, then
  rerun `up.sh`. Host ports: `ANKI_CONNECT_PORT` (8765) and `VNC_PORT` (5900) for `up.sh`; pass the
  same `ANKI_CONNECT_PORT` to `install.sh`.

## What protects what

- AnkiConnect and VNC are published on 127.0.0.1 only: Docker's port publishing bypasses ufw, so the
  bind is the protection. AnkiConnect needs its apiKey and VNC needs a password, which keeps out
  other local users and containers. The AnkiConnect key also sits in `DIR/anki/secrets/` (0600), where
  the Anki container reads it.
- The Anki container runs as a non-root user with a read-only root filesystem, no capabilities,
  `no-new-privileges` and a 1 GB memory limit. The base image is pinned by digest, and the Anki and
  AnkiConnect downloads by sha256.
- The polkit rule grants one user three verbs on one unit: no other unit, no enable, disable or
  edit. The deploy user already owns `DIR`, whose binary the unit runs, and has sudo.
- The service runs as `anki-review` under a hardened unit: `ProtectSystem=strict`, `ProtectHome`, no
  capabilities, private `/tmp` and devices. Its only writable path is `/var/lib/anki-review`.
- The Claude token and the AnkiConnect key are sealed in `/var/lib/anki-review/data/vault.json` (0600),
  under a key from Argon2id of the owner's password, with XChaCha20-Poly1305. The env file holds no
  secret. The tokens are plaintext only in the service's memory, and only after a login.
- What this does not protect: root on the host, and the `anki-review` user, can read the memory of the
  running service and so the tokens. A copy of the disk, a backup or a snapshot is protected only by
  the password. Because the AnkiConnect key is also in `DIR/anki/secrets/`, the vault mainly guards the
  Claude token.
- No recovery for a forgotten password: `sudo rm /var/lib/anki-review/data/vault.json`, then
  `systemctl restart anki-review`, then set up again on the page (both tokens again).
