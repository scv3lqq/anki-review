# Deploy kit

Runs anki-review on a Linux server. Anki desktop with AnkiConnect runs headless in Docker. The server
runs as a hardened systemd service under its own system user, with its own Claude Code and pinned Codex CLI installs.
Anki syncs through AnkiWeb, or through Anki's own sync server in a second container
([Sync server](#sync-server-ankiweb-or-self-hosted)).

With Claude Code, `/setup-anki-review server` runs every step below from your machine; you log in to
the sync server in the VNC window and type the sudo password and the setup form on the server's page
(owner login, password, provider credentials, AnkiConnect key). `/setup-anki-review server-update` ships a
new build without any of them, and [Auto-deploy](#auto-deploy) lets the server ship `main` itself.

| Path | What |
| --- | --- |
| `anki/` | Anki image: `Dockerfile`, `compose.yaml`, `entrypoint.sh`, `setup-profile.py`, `syncserver.sh`. `up.sh` creates the secrets and starts it, `up.sh self-hosted` with the sync server |
| `install.sh` | root: service user, both grading CLIs, `/etc/anki-review/env`, the unit, a polkit rule for restarts, smoke test, start |
| `smoke.sh` | root: both CLI `--version` checks as the service user, in the service's sandbox, without authentication or quota calls |
| `provider.sh`, `test-provider.sh` | pure provider/config decisions and offline fixture checks (`bash deploy/test-provider.sh`) |
| `autodeploy.sh` | deploy user, from the timer: ships the signed head of `main` ([Auto-deploy](#auto-deploy)) |
| `systemd/anki-review.service` | unit template, `@DEPLOY_DIR@` is filled in by `install.sh` |
| `systemd/anki-review-deploy.{service,timer}` | auto-deploy units, installed by `install.sh` with `AUTO_DEPLOY=on` |

## Prerequisites

- On the server: Linux x86_64 with systemd, Docker Engine with the compose plugin, curl. A deploy
  user in the `docker` group with sudo. Outbound HTTPS to GitHub, git.sr.ht, the Debian mirrors,
  claude.ai, the Anthropic API, OpenAI/ChatGPT and AnkiWeb (AnkiWeb mode only). Optional: polkit 0.106 or newer, for
  updates without sudo.
- On your machine: Go, SSH access as the deploy user, and the selected grader's account. Claude uses `claude setup-token` (step 6); Codex uses a dedicated
  [headless device session](#codex-device-session).

## Steps

Below, `SSH` stands for `ssh -p <port> <user>@<server>` and `DIR` for the deploy dir,
`/home/<user>/anki-review-deploy` by default. Always write `DIR` as an absolute path: a local
shell expands `~` before ssh sends the command.

1. Build the binary: `make build-linux`.
2. Copy the kit and the binary (`scp` takes `-P` for the port):
   `SSH 'mkdir -p DIR/bin && chmod 700 DIR'`,
   `scp -r deploy/anki deploy/systemd deploy/install.sh deploy/smoke.sh deploy/provider.sh deploy/test-provider.sh deploy/autodeploy.sh <user>@<server>:DIR/`,
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
   script installs the service user, both grading CLIs and the polkit rule (below), writes
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
   - Grading provider: Claude Code (default) or Codex. For Codex, follow the
     [device-session import](#codex-device-session) below.
   - Claude token (Claude only): on your machine, run `claude setup-token`, approve it in the browser and paste the
     `sk-ant-oat01-...` token the terminal prints. Not on the server: the token is for the account
     that grades.

   Setup checks AnkiConnect and the selected grader, seals credentials under your password and logs you
   in. The service is unlocked from then on.

The service listens on `127.0.0.1:8767` only. A reverse proxy with HTTPS is up to you (its own access
control is optional now that the page has a login). It must send `Host: 127.0.0.1:<port>`, and should
send `X-Forwarded-Proto` (the session cookie is `Secure` only then) and `X-Forwarded-For` (login
throttling counts per client by its last hop).

## Grading provider installation

The kit installs both supported grading CLIs so the owner can switch in Settings without root.
`GRADER_PROVIDER=claude|codex` sets only the first setup default. On reruns, the existing env setting
is retained unless explicitly overridden; an encrypted saved choice always wins. `CLAUDE_MODEL`
and `CODEX_MODEL` are independent. Existing `CLAUDE_VERSION=x.y.z|stable|latest` behavior remains.
Codex is pinned to **0.166.0**: the official full Linux x86_64 musl package, including
`bin/codex-exec-server`, `codex-resources` and `codex-package.json`, is verified before extraction with SHA256
`1d44b28bc4ac9937d7d3532264baa68a507ce364956a0d4ebb856f331cdee254`.
See the [official release](https://github.com/openai/codex/releases/tag/rust-v0.166.0).

The installer retains explicit `CLAUDE_BIN` and `CODEX_BIN` paths, requires absolute executable
paths, and verifies they run as the service user. It never silently replaces a custom binary;
custom Codex must report 0.166.0. Upgrade existing deployments with the full kit once, including
`provider.sh` and the unit, before switching to Codex. A binary-only update is insufficient.
The smoke test reports the initial setup provider and both available CLI versions; it cannot
inspect the encrypted saved provider. It submits no cards and reads no authentication files.

## Codex device session

Codex grading requires codex-cli **0.166.0** and a dedicated ChatGPT device session.
Do not reuse the session of another CLI or service: refresh rotates its credentials.
Enable device code authentication in your ChatGPT security settings first. During login,
open the displayed URL in your browser, enter the displayed user code, and approve access.
The owner performs these steps in a private terminal; never paste codes or credentials into chat.
See [Codex authentication](https://developers.openai.com/codex/auth).

For a headless VPS, connect with `ssh -t -p <port> <user>@<server> bash` and run there:

```sh
umask 077
login_dir=$(sudo -u anki-review mktemp -d /dev/shm/anki-review-login.XXXXXX)
sudo -u anki-review env CODEX_HOME="$login_dir" \
  /var/lib/anki-review/.local/share/codex/0.166.0/bin/codex \
  -c 'cli_auth_credentials_store="file"' login --device-auth
transfer_dir=$(mktemp -d /dev/shm/anki-review-import.XXXXXX)
sudo install -o "$(id -un)" -g "$(id -gn)" -m 0600 \
  "$login_dir/auth.json" "$transfer_dir/auth.json"
printf 'Transfer path: %s/auth.json\n' "$transfer_dir"
```

Keep that shell open. If `CODEX_BIN` was explicitly configured, use that executable instead.
Both remote directories are private and memory-backed. In a separate terminal on your computer,
copy the whole file over SSH (replace `<transfer-dir>` with the printed path):

```sh
umask 077
local_auth_dir=$(mktemp -d "${TMPDIR:-/tmp}/anki-review-import.XXXXXX")
scp -P <port> <user>@<server>:<transfer-dir>/auth.json "$local_auth_dir/auth.json"
chmod 0600 "$local_auth_dir/auth.json"
ssh -p <port> -N -L 8768:127.0.0.1:8767 <user>@<server>
```

Open <http://127.0.0.1:8768/>, select **Codex** in setup or settings, and choose the copied
`auth.json` with the file picker. Do not paste an access token or use an API-key-only auth file.
The authenticated setup/settings request imports the whole session into the encrypted vault;
credentials are never returned by the API. Check for a saved success on the page first.
Only after successful save, remove the local staging directory in its original terminal:
`rm -rf -- "$local_auth_dir"`. In the original remote shell, remove only the two directories
created above: `rm -rf -- "$transfer_dir"` and `sudo -u anki-review rm -rf -- "$login_dir"`.
Close only the SSH tunnel created for this import. If save fails, retry before removing staging.

For a local install, use the same temporary `CODEX_HOME` and explicit file-store option with
`codex -c 'cli_auth_credentials_store="file"' login --device-auth`, then select its `auth.json`
on the local setup page and remove that temporary home after successful save. On Linux use a
private directory under `/dev/shm`. On macOS temporary auth files reside on local disk until
removed; rely on the local account permissions and disk encryption for this boundary.

The CLI refreshes the imported session during checks and grading. The service reseals every
changed snapshot before accepting results, including refresh after a failed call. A revoked or
expired refresh token requires a new dedicated device login and another file import in Settings.
A restart always locks the app until owner login; unlocking may resume a saved pass. Review the
saved-pass warning before unlocking. During Linux grading, credentials exist only in a private
0700 temporary home on tmpfs (0600 auth file), removed after the call. The service verifies the
filesystem and fails closed if its runtime is not memory-backed. It does not keep CLI sessions
or card logs. Root and the unlocked service user can still read live credentials.

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
  capabilities, private `/tmp` and devices. Its persistent writable path is `/var/lib/anki-review`; `RuntimeDirectory=anki-review`
  creates private `/run/anki-review` (0700), with `TMPDIR` pointing there for tmpfs auth files.
  The service `HOME` remains `/var/lib/anki-review`; CLI calls isolate their own temporary home.
- Grading credentials and the AnkiConnect key are sealed in `/var/lib/anki-review/data/vault.json` (0600),
  under a key from Argon2id of the owner's password, with XChaCha20-Poly1305. The env file holds no
  secret. Credentials are decrypted after owner login. Codex auth snapshots also exist in temporary
  private tmpfs files during calls, and are removed afterward.
- What this does not protect: root on the host, and the `anki-review` user, can read the memory of the
  running service and so the tokens. A copy of the disk, a backup or a snapshot is protected only by
  the password. Because the AnkiConnect key is also in `DIR/anki/secrets/`, the vault mainly guards the
  grading credentials.
- No recovery for a forgotten password: `sudo rm /var/lib/anki-review/data/vault.json`, then
  `systemctl restart anki-review`, then set up again on the page (grading credentials and AnkiConnect key again).
