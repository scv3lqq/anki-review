# Deploy kit

Runs anki-review on a Linux server. Anki desktop with AnkiConnect runs headless in Docker. The server
runs as a hardened systemd service under its own system user, with its own Claude Code install.
Anki syncs through AnkiWeb, or through Anki's own sync server in a second container
([Sync server](#sync-server-ankiweb-or-self-hosted)).

With Claude Code, `/setup-anki-review server` runs every step below from your machine; you log in to
the sync server in the VNC window and type the sudo password and the setup form on the server's page
(owner login, password, Claude token, AnkiConnect key). `/setup-anki-review server-update` ships a
new build without any of them, and [Auto-deploy](#auto-deploy) lets the server ship `main` itself.

| Path | What |
| --- | --- |
| `anki/` | Anki image: `Dockerfile`, `compose.yaml`, `entrypoint.sh`, `setup-profile.py`, `syncserver.sh`. `up.sh` creates the secrets and starts it, `up.sh self-hosted` with the sync server |
| `install.sh` | root: service user, Claude Code, `/etc/anki-review/env`, the unit, a polkit rule for restarts, smoke test, start |
| `smoke.sh` | root: `claude --version` as the service user, in the service's sandbox |
| `autodeploy.sh` | deploy user, from the timer: ships the signed head of `main` ([Auto-deploy](#auto-deploy)) |
| `systemd/anki-review.service` | unit template, `@DEPLOY_DIR@` is filled in by `install.sh` |
| `systemd/anki-review-deploy.{service,timer}` | auto-deploy units, installed by `install.sh` with `AUTO_DEPLOY=on` |

## Prerequisites

- On the server: Linux x86_64 with systemd, Docker Engine with the compose plugin, curl. A deploy
  user in the `docker` group with sudo. Outbound HTTPS to GitHub, git.sr.ht, the Debian mirrors,
  claude.ai, the Anthropic API and AnkiWeb (AnkiWeb mode only). Optional: polkit 0.106 or newer, for
  updates without sudo.
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
3. Start Anki as the deploy user, no sudo: `SSH DIR/anki/up.sh` (AnkiWeb) or
   `SSH DIR/anki/up.sh self-hosted`. It creates the AnkiConnect key, the VNC password and the sync
   server password in `DIR/anki/secrets/` (0600, never printed), builds the image (the first build
   downloads about 200 MB), waits for AnkiConnect and prints the deck count.
4. **Human:** log in to the sync server once. Open a tunnel with `ssh -p <port> -N -L 5901:127.0.0.1:5900
   <user>@<server>`. Read the password with `SSH cat DIR/anki/secrets/vnc-password` (append `| pbcopy`
   on macOS). Connect a VNC viewer to `127.0.0.1:5901` (`open vnc://127.0.0.1:5901` on macOS: Screen
   Sharing refuses local port 5900). AnkiWeb: in Anki, press Sync, log in, choose "Download from
   AnkiWeb" and wait for the media. Self-hosted: first bring the collection to the server
   ([Moving from AnkiWeb](#moving-from-ankiweb), steps 1 to 4), then in Preferences, Syncing set the
   self-hosted sync server to `http://sync:8766/`, press Sync, log in as `anki` with
   `SSH cat DIR/anki/secrets/sync-password` and choose **Download**. A rerun of `up.sh` then shows
   more than one deck.
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

## Sync server: AnkiWeb or self-hosted

Anki here syncs with AnkiWeb, or with Anki's own sync server on the same host (`up.sh self-hosted`).
`up.sh` keeps the mode in `DIR/anki/sync-mode`: a run without an argument keeps the last one,
AnkiWeb on a new host.

- The server is the one built into Anki (`anki --syncserver`), from the same image, in the container
  `anki-sync`. It listens on `127.0.0.1:8766` (`SYNC_PORT` for `up.sh`) and keeps its copy of the
  collection and the media in the volume `anki-sync`.
- One login, `anki`. The password is in `DIR/anki/secrets/sync-password` (24 random letters and
  digits, created by `up.sh`). To change it, write a new one of the same kind into the file, run
  `docker restart anki-sync` (the server reads it only at start) and log in again on every device.
- Anki here reaches it as `http://sync:8766/`. Your devices need HTTPS: a reverse proxy name for
  `http://127.0.0.1:8766` on the host, with no proxy login in front (Anki can't pass one; the sync
  login guards the server). AnkiMobile needs TLS 1.2 enabled on the proxy (error -9836 otherwise).
  The proxy's body limit must exceed the collection (nginx: `client_max_body_size`, 1 MB by
  default); the server takes up to 100 MB, more with `SYNC_MAX_PAYLOAD_MEGS` for `up.sh`. Caddy
  needs `read_buffer 512k` in its `reverse_proxy` transport, or media downloads break.
- On each device the URL goes in with its trailing `/`: Anki desktop, Preferences, Syncing,
  Self-hosted sync server; AnkiDroid 2.16 or newer, Settings, Sync, Custom sync server; AnkiMobile,
  the custom sync server in its sync settings. Then log in as `anki`.

### Moving from AnkiWeb

The first sync with an empty server uploads the whole collection file: schedules, review history,
FSRS parameters and memory state, deck options, suspended and buried cards, flags, tags, then the
media. Nothing is converted, so every card keeps its state.

1. Sync every device with AnkiWeb once more, then review nowhere until step 5.
2. On your main computer, export a backup: File, Export, Anki Collection Package, with media and
   scheduling.
3. `SSH DIR/anki/up.sh self-hosted`, then set up the proxy. `curl https://sync.example.com/health`
   answers 200 with an empty body.
4. Main computer: Preferences, Syncing: Log Out, set the self-hosted sync server, close Preferences,
   press Sync, log in as `anki`. The server is empty, so Anki uploads the collection. Wait for the
   media to finish.
5. Anki here (VNC, as in step 4 above), then your other devices: Log Out, set the URL
   (`http://sync:8766/` here), Sync, log in, choose **Download**.

AnkiWeb keeps its last copy until you delete the account there.

Back to AnkiWeb: `SSH DIR/anki/up.sh ankiweb` stops `anki-sync` (its volume stays). On every device,
clear the self-hosted URL and log in to AnkiWeb; upload from the device with the latest reviews and
download on the others.

Backups: every Anki desktop keeps automatic backups of its profile, and any device can rebuild the
server's copy with a full upload. A copy of the server's data, from `DIR`:
`docker stop anki-sync && docker run --rm --user "$(id -u):$(id -g)" -v anki-sync:/sync:ro -v "$PWD":/out debian:trixie-slim tar -C /sync -czf /out/anki-sync.tgz . ; docker start anki-sync`.

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
  rerun `up.sh`. Host ports: `ANKI_CONNECT_PORT` (8765), `VNC_PORT` (5900) and `SYNC_PORT` (8766) for
  `up.sh`; pass the same `ANKI_CONNECT_PORT` to `install.sh`.

## Auto-deploy

Optional: the server pulls `main` and ships it, so a push needs no step on your machine. Nothing
reaches into the server and GitHub holds no credential of it: every 5 minutes
`anki-review-deploy.timer` runs `DIR/autodeploy.sh` as the deploy user, which

1. fetches `main` over HTTPS from the public repo (no token) into `DIR/src`; an unchanged head ends
   the run;
2. checks the head commit's SSH signature against `DIR/allowed_signers`: a commit no key there signed
   never deploys. A head that does not descend from the deployed commit (a force push) waits until
   you delete the `deployed=` line in `DIR/autodeploy-state`;
3. waits for the CI checks `test` and `secrets` to pass on that commit (the public GitHub API, no
   token);
4. holds while `deploy/` in the commit differs from the kit in `DIR` (`install.sh`, the units,
   `anki/`): those changes need root or a new Anki image, so they still go through the full setup;
5. builds the binary with the flags of `make build-linux`; the same bytes as the running binary are
   recorded without a restart, so commits outside the Go code (docs, the skill) restart nothing;
6. holds while a pass is in progress (an unlocked server at a stage other than `idle` or `written`);
7. stages the binary, restarts the service through the polkit rule and waits for `/api/session`. No
   answer in 30 s: the previous binary goes back and the commit is skipped.

Each deploy locks the service until you log in on the page, like any restart.

Turn it on:

- On the server: git, jq, Go 1.21 or newer (it downloads the version `go.mod` asks for) and the
  polkit rule (polkit 0.106 or newer). Your commits on `main` are SSH-signed.
- `DIR/allowed_signers`, one line per key whose commits may deploy, in the format of
  `ssh-keygen -Y verify` (`you@example.com namespaces="git" ssh-ed25519 AAAA...`).
  `/setup-anki-review` writes it from your git signing key (`server.sh signers`).
- `sudo env AUTO_DEPLOY=on DIR/install.sh DIR`. `AUTO_DEPLOY=off` removes the units; a run without it
  keeps them.

Follow it: `sudo journalctl -u anki-review-deploy` (sudo unless the deploy user is in `adm` or
`systemd-journal`), which says once per commit why it waits.
`DIR/autodeploy-state` holds the last deployed, held and rejected commit. A fork sets
`AUTO_DEPLOY_REPO` (and `AUTO_DEPLOY_BRANCH`) with `systemctl edit anki-review-deploy.service`.

Commits that GitHub signs, such as a merge in the web UI, carry GitHub's key, not yours: they wait for
your next signed commit on top.

## What protects what

- AnkiConnect and VNC are published on 127.0.0.1 only: Docker's port publishing bypasses ufw, so the
  bind is the protection. AnkiConnect needs its apiKey and VNC needs a password, which keeps out
  other local users and containers. The AnkiConnect key also sits in `DIR/anki/secrets/` (0600), where
  the Anki container reads it.
- The sync server (self-hosted mode) is published on 127.0.0.1 only, with the same container
  hardening and a 512 MB memory limit; TLS comes from your proxy. Its login is its only access
  control: whoever has the password can read or replace the collection. The password is 24 random
  letters and digits (about 143 bits), in `DIR/anki/secrets/sync-password` (0600) and in the
  server container's environment.
- The Anki container runs as a non-root user with a read-only root filesystem, no capabilities,
  `no-new-privileges` and a 1 GB memory limit. The base image is pinned by digest, and the Anki and
  AnkiConnect downloads by sha256.
- The polkit rule grants one user three verbs on one unit: no other unit, no enable, disable or
  edit. The deploy user already owns `DIR`, whose binary the unit runs, and has sudo.
- Auto-deploy trusts the keys in `DIR/allowed_signers`, not GitHub: the signature covers the tree it
  builds, so neither a takeover of the GitHub account nor of the repo ships code without your key,
  and a force push back to an older signed commit waits for you. The CI check only keeps red builds
  out. Its unit sees no other path in `/home` than `DIR`, writes
  only there, has no Docker socket (the deploy user's docker group would make it root) and can't gain
  privileges, so no sudo.
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
