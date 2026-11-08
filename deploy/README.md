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
   `scp -r deploy/anki deploy/systemd deploy/install.sh deploy/smoke.sh deploy/provider.sh deploy/test-provider.sh deploy/session-key.py deploy/test-session-key.py deploy/autodeploy.sh <user>@<server>:DIR/`,
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
### Get the file on your own computer (recommended)

For Codex, upload the whole `auth.json` file; do not extract or paste a token.
A plain `codex login --device-auth` can use the OS credential store, so a file
may not appear. The steps below force a separate file-based login for this service.
Use your own ChatGPT account and keep this terminal open until settings are saved.
They work even when anki-review itself runs on a remote server; no SSH is needed.

1. Install Codex CLI 0.166.0 if needed, then check `codex --version` in Terminal
   (macOS/Linux) or PowerShell (Windows). See the
   [official Codex guide](https://developers.openai.com/codex/quickstart).
2. Enable device code authentication in your ChatGPT security settings. Copy and
   run only the block for your computer. These create a private temporary folder
   before login; they do not reuse your usual Codex session.

macOS, Terminal:

```sh
umask 077
anki_codex_dir=$(mktemp -d "${TMPDIR:-/tmp}/anki-review-codex.XXXXXX") &&
CODEX_HOME="$anki_codex_dir" codex -c 'cli_auth_credentials_store="file"' login --device-auth &&
test -s "$anki_codex_dir/auth.json" &&
printf 'File to upload: %s/auth.json\n' "$anki_codex_dir" &&
open -R "$anki_codex_dir/auth.json"
```

Linux, Terminal (uses memory-backed `/dev/shm`):

```sh
umask 077
anki_codex_dir=$(mktemp -d /dev/shm/anki-review-codex.XXXXXX) &&
CODEX_HOME="$anki_codex_dir" codex -c 'cli_auth_credentials_store="file"' login --device-auth &&
test -s "$anki_codex_dir/auth.json" &&
printf 'File to upload: %s/auth.json\n' "$anki_codex_dir"
```

Windows, PowerShell:

```powershell
$ankiCodexDir = Join-Path $env:LOCALAPPDATA ("anki-review-codex-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $ankiCodexDir -ErrorAction Stop | Out-Null
icacls $ankiCodexDir /inheritance:r /grant:r "$([Security.Principal.WindowsIdentity]::GetCurrent().Name):(OI)(CI)F"
if ($LASTEXITCODE -ne 0) { throw "Could not protect the temporary folder." }
$ankiPreviousCodexHome = $env:CODEX_HOME
$env:CODEX_HOME = $ankiCodexDir
try { codex -c 'cli_auth_credentials_store="file"' login --device-auth } finally { $env:CODEX_HOME = $ankiPreviousCodexHome }
if ($LASTEXITCODE -ne 0 -or !(Test-Path -LiteralPath (Join-Path $ankiCodexDir "auth.json"))) { throw "Login did not create auth.json. Complete browser approval and try again." }
Write-Output ("File to upload: " + (Join-Path $ankiCodexDir "auth.json"))
```

3. While the command waits, open the URL shown in the terminal, sign in to your
   own ChatGPT account, enter the displayed one-time code and approve access.
   Wait until the terminal reports successful login. Never share the code or file.
4. The terminal prints **File to upload** with the exact `auth.json` path. On macOS,
   Finder also selects it. On the anki-review page select **Codex**, choose this
   complete file using the file picker, then submit registration/setup or press
   **Save** in Settings (enter your current password when requested).
   To enter the printed path in a file picker: macOS **Cmd+Shift+G**, Linux
   **Ctrl+L**, Windows the **File name** box. Do not open the file or paste its contents.
5. Wait for a successful registration/save on the page. Only then, in the same
   terminal, delete the temporary folder. Keep it for retry if the page reports an error.

macOS/Linux cleanup, only after successful save:

```sh
rm -rf -- "$anki_codex_dir"
```

Windows cleanup, only after successful save:

```powershell
Remove-Item -LiteralPath $ankiCodexDir -Recurse -Force
```

If no file appears, check that browser approval completed, use the entire command
block including `CODEX_HOME` and `cli_auth_credentials_store="file"`, and look at
**File to upload**, not the current folder or your usual `.codex` directory.
Do not run `codex logout` on your normal session. Imported credentials refresh
inside the service; do not continue using this temporary session elsewhere.
If the saved session is rejected later, repeat this dedicated login and import a new file.
See [Codex authentication](https://developers.openai.com/codex/auth).

### Advanced: create the file directly on the VPS

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

For a local service, use the same personal-computer steps above. Temporary files on
macOS and Windows reside on local disk until deleted; use a private local account
and disk encryption. Linux login uses a private memory-backed folder under `/dev/shm`.

The CLI refreshes the imported session during checks and grading. The service reseals every
changed snapshot before accepting results, including refresh after a failed call. A revoked or
expired refresh token requires a new dedicated device login and another file import in Settings.
A configured recovery key lets valid browser sessions recover after restart; otherwise password
login is needed. Saved passes load passively and require explicit **Resume** for interrupted work. During Linux grading, credentials exist only in a private
0700 temporary home on tmpfs (0600 auth file), removed after the call. The service verifies the
filesystem and fails closed if its runtime is not memory-backed. It does not keep CLI sessions
or card logs. Root and the unlocked service user can still read live credentials.

## Persistent browser sessions

The installers enable recovery on first installation or upgrade only when the existing environment
has no `SESSION_KEY_FILE` entry. They generate an independent 32-byte key on the host at
`/etc/anki-review/session.key`, owned by `root:anki-review` mode `0640` in a `0750` directory.
They validate existing configured key metadata before changing the installation and never overwrite,
rotate or regenerate a missing key. Only the path is stored in `/etc/anki-review/env`.
A custom existing absolute path is retained. Keep keys outside the repository and application data;
a regular non-symlink file, exactly 32 bytes, mode `0600` or `0640`, and protected ancestors are required.
Do not display key bytes. `session-key.py` reports validation only, without key contents.

Each existing account needs one successful password login after enabling recovery. Later valid
browser cookies restore only their own account until the original 30-day expiry; restart never
extends it. Logout durably revokes the current session. Password changes revoke other sessions
while preserving the initiating session's original expiry. Every account's saved review loads
passively: status, login, recovery and polling never resume grading or confirmed writes. The owner
must click **Resume checking** or **Resume saving to Anki**; diagnostics never press those buttons.

The recovery envelope wraps the password-derived vault key and hashed session metadata.
Application data plus the server key decrypts provider credentials without the password. A data-only
backup without the server key retains password protection. Store key backups separately with
restricted access; encrypt and protect any full host backup containing both. Include the configured
key in a deliberate full recovery set, retain ownership/modes, and test restore on disposable copies.

An empty `SESSION_KEY_FILE` preserves legacy password-only operation only before any recovery
envelope exists. If recovery already exists, an empty setting fails closed too. A configured missing,
unsafe or wrong key prevents startup; never rerun an installer expecting a replacement key.

For a lost key, restore the exact key from a protected backup first. If that is impossible, an
administrator may deliberately migrate offline, revoking every browser session:

1. Stop HTTP and preserve protected backups of the entire application data, environment and any
   remaining key. In multi-account mode inspect every saved pass privately and follow the consistent
   broker/collection backup procedure below. Preserve account registry, preferences and all passes.
2. In each existing account's `vault.json`, atomically remove only the optional top-level `recovery`
   field, preserving version, login, KDF and password ciphertext byte values and private file metadata.
   Do not delete vaults, reset secrets, change providers or modify review state. No password is read
   by the administrator or tool. With HTTP stopped, removing all recovery envelopes revokes sessions.
3. To disable recovery, explicitly clear `SESSION_KEY_FILE`. To rotate or re-enable it, move the old
   key to protected backup storage, deliberately provision a fresh key with the helper's
   `--allow-create --uid 0 --gid <anki-review-group-id>` arguments and the chosen protected directory,
   and configure its absolute path. This is an intentional administrator action, never installer repair.
4. Start the matching HTTP binary and verify public status without cookies. Accounts remain
   inaccessible until their owners enter their existing passwords on the normal login page. That
   login recreates recovery when enabled. Do not claim restored access before it succeeds.
   Saved passes remain passive and need explicit **Resume**.

Use the same offline procedure for deliberate key rotation, even when the old key is available;
removing every recovery envelope revokes old browser sessions. Rotation alone does not replace
the password-derived vault key. Old data plus its old server key can expose that derived key and
may decrypt later credential reseals that keep the same KDF. If exposure is suspected, after
password login the owner must deliberately change the password through Settings to reseal with
a fresh KDF. Keep old backups protected because their old data and old key remain decryptable.

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
- A restart clears process memory. With recovery configured, valid browser sessions recover lazily
  until their original expiry; otherwise log in on the page. Interrupted passes require **Resume**.
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
  rerun `up.sh` for an ordinary full setup. For an audio-only image update, use the maintenance
  procedure below. Host ports: `ANKI_CONNECT_PORT` (8765), `VNC_PORT` (5900) and `SYNC_PORT` (8766)
  for `up.sh`; pass the same `ANKI_CONNECT_PORT` to `install.sh`.

### Native card audio and desktop image refresh

The pinned image `anki-desktop:26.09.3-ac26.10.14.0-audio1` includes the owned
`anki/native_audio.py` adapter. `anki/patch_ankiconnect.py` verifies the pinned
AnkiConnect source hash and unique patch anchors before installing it. The adapter
uses one native `Card.render_output()` for HTML and ordered question/answer AV lists,
so template filters, conditionals and FrontSide retain Anki's ordering. Filenames
come from those lists, never guessed from note fields. Question HTML cannot resolve
answer-list audio. Unsupported TTS and invalid references display `Audio unavailable`;
the adapter does not synthesize speech. Audio/video controls use the existing
authenticated `/api/media/` endpoint after the Go sanitizer removes active content.

An image refresh does not rewrite saved pass HTML: existing saved HTML stays intact
until the normal next pass. Do not reset the owner's pass to refresh cached HTML,
and do not unlock any account automatically. Login and session recovery load saved passes
passively; interrupted grading or confirmed writes require the owner's explicit **Resume**. Detect multi-account mode before uploading or running setup;
use [multi-account image maintenance](#native-audio-image-maintenance) for that mode.

For a single owner, complete the setup skill's status/saved-pass checks first and
resolve any active pass before maintenance. Retain matching previous binaries, kit,
image ID/tag and configuration for rollback. Stop HTTP, gracefully stop the owner's
desktop and take consistent application/collection backups before replacing anything.
Record the existing named volume (`anki-data` in this kit), endpoint/ports, image
UID/GID, network, secrets and sync settings. Preserve `data/vault.json`,
`data/state.json`, preferences and the existing collection; never create a replacement
profile or change sync direction. Keep the sync service running for an audio-only
desktop refresh; use the documented sync backup procedure separately if needed.

Transfer the matching kit, then, as the same deploy user with the recorded UID/GID
and port environment, run only the desktop service:
`docker compose --project-directory DIR/anki -f DIR/anki/compose.yaml up -d --build --no-deps anki`.
Verify the recreated desktop has the same volume, secrets, network and endpoint,
then start the matching HTTP binary and verify public status without an authenticated cookie.
Do not run `S up` for an audio-only update. It uses the ordinary full setup path,
which can also recreate or stop the sync service. Roll back with the matching old
kit/image and binary while HTTP and the desktop are stopped, retaining the same data.

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

After deployment, valid browser sessions can recover with the configured server key until their
original expiry. Password-only mode requires login. Interrupted checking or saving waits for **Resume**.

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
  `no-new-privileges` and a 2 GB memory limit. The base image is pinned by digest, and the Anki and
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
  under a key from Argon2id of the owner's password, with XChaCha20-Poly1305. With recovery enabled,
  an independent server key also wraps the password-derived key and hashed session metadata.
  Valid browser sessions can recover credentials after restart without entering the password;
  anonymous requests cannot recover them. The env file holds only the key path, never key bytes.
  Codex auth snapshots exist in private temporary tmpfs files during calls and are removed afterward.
- What this protects: a data-only backup without the independent server key retains password
  protection. Application data plus the server key decrypts credentials without the password;
  encrypt and protect full host backups that contain both. Root and the `anki-review` user can read
  live credentials. Because the AnkiConnect key also exists in `DIR/anki/secrets/`, the vault mainly
  guards grading credentials.
- There is no password-reset flow. A missing configured server key prevents startup; restore its
  protected backup or follow the explicit [offline recovery migration](#persistent-browser-sessions).
  Migration removes only recovery envelopes, preserving password ciphertext and all saved account
  data, then the owner enters their existing password on the normal page. Never delete a vault to
  repair a lost key. Login and recovery are passive; interrupted work still requires **Resume**.

## Multi-account deployment

This opt-in Linux mode preserves the existing owner's `data/vault.json`,
`data/state.json`, Anki endpoint and collection volume. It adds a private account
registry and `data/accounts/<opaque-id>/` for guest vaults, passes and preferences.
The page and all feedback remain English. Do not copy or rename the owner's volume.

A separate root-owned `anki-review-provisioner` controls Docker through a group-only
Unix socket. The public `anki-review` process keeps its existing system user and
hardening, without Docker membership, socket mounts or sudo. The broker accepts only
generated account IDs. It chooses endpoints, random keys, names and container options
from administrator settings; users never enter AnkiConnect infrastructure details.

Each guest uses the pinned Anki image, a separate network and fixed-size ext4 backing
filesystem, a private key and a fresh empty profile. No VNC is published and no sync
login is required. Limits per runtime: 2 GiB RAM/swap, one CPU, 1024 PIDs and 2 GiB
storage by default, plus bounded tmpfs and 256 MiB shared memory. Reserve at least
2.5 GiB additional RAM and 2 GiB disk per guest, plus host and grader headroom; three
guests require at least 7.5 GiB extra RAM. Sparse backing files still need free host
disk as they grow. Hard filesystem capacity prevents a guest exhausting unbounded
storage. Linux loop devices, ext4, mount/losetup/mkfs tools and root are required;
provisioning fails closed if quota mounting or resource ownership cannot be proved.

### Enable invitations on an existing deployment

1. Follow the setup skill's saved-pass checks before stopping or restarting HTTP.
   Stop `anki-review` for the maintenance window; valid browser sessions can recover after
   restart when configured. Inspect all saved passes with the administrator-only helper described
   below. Preserve an application/collection backup before changing configuration.
   No verification step should unlock an account, grade cards or write confirmed grades.
2. Build and transfer `make build-linux build-provisioner-linux`, the deploy kit,
   `install-multi-account.sh`, its systemd unit and both Linux binaries. Keep the
   ordinary owner image installed through `anki/up.sh`; do not run sync setup for guests.
3. As root run `DIR/install-multi-account.sh anki-review`. Set `ANKI_UID` and
   `ANKI_GID` to the user IDs built into the Anki image when they differ from 1000.
   `MAX_GUESTS=3` and `STORAGE_BYTES=2147483648` are the defaults. The installer
   installs/starts only the broker and does not restart or unlock the public service.
4. In `/etc/anki-review/env` add
   `ACCOUNT_SOCKET=/run/anki-review-provisioner/control.sock` and `MAX_ACCOUNTS=4`.
   Keep this total at one plus the broker's guest cap; supported total range is 2 to 8.
   Keep existing owner settings and secrets unchanged. Do not put keys/tokens in env.
5. Stage the new HTTP binary and start `anki-review` using the normal maintenance
   procedure. Verify anonymous `/api/session` reports `multi_account:true` and login
   state without another account's provider/stage. The owner enters their password
   and, if needed, their own explicit Claude token or Codex session on the page.
   Empty Claude credentials no longer use the host login in this mode.
6. The owner creates an invitation in Settings and sends the fragment link privately.
   It expires in 24 hours and is single-use. The friend registers, supplies their own
   grader credential and imports a compatible `.apkg` using **Import cards**. Export
   with **Support older Anki versions** enabled; limits are 8 MiB compressed,
   256 MiB expanded and 4096 entries. Finish/reset any active batch before import.
   No AnkiWeb login, VNC interaction, endpoint or key copy is needed for the friend.

The broker reserves IDs durably and checks its own guest cap independently of HTTP.
Registration failures reuse the reservation; an existing sealed vault is never
replaced on retry. If the response was lost after success, log in with the chosen
credentials. Cleanup failures retain a bounded pending record for retry. Inspect
broker logs by operation/account ID, never by printing key or credential files.

### Maintenance, updates and rollback

Auto-deploy holds in multi-account mode, including commits that change only the
provisioner. Setup helpers refuse automated restarts and do not wait for a publicly
visible unlocked account. Update both binaries explicitly: build, transfer the kit,
read the existing root-only `/etc/anki-review-provisioner.conf` and preserve all four
administrator values (ANKI_UID, ANKI_GID, MAX_GUESTS and STORAGE_BYTES). Repeat them
explicitly with `sudo env ANKI_UID=<saved-uid> ANKI_GID=<saved-gid>
MAX_GUESTS=<saved-cap> STORAGE_BYTES=<saved-size> DIR/install-multi-account.sh anki-review`
on one shell line. The installer retains saved values when overrides are omitted;
changing an existing quota is refused and changing image IDs can prevent startup. This updates
only the privileged daemon. Then perform the HTTP maintenance restart. Preserve
prior binaries and the matching daemon configuration for rollback.

After stopping HTTP, an administrator runs `python3 DIR/check-passes.py
/var/lib/anki-review/data` locally on the server. It reports stages/counts only,
including all guest states. An active checking/confirmed pass needs the owner's
explicit decision before **Resume checking** or **Resume saving to Anki**. Login and session
recovery load passes passively. Never move/delete saved passes or log users in as a diagnostic.
Restart can preserve valid browser sessions; restarting the broker alone does not authorize
review writes. Broker storage and keys must remain root-only and survive restarts.

Rollback: stop HTTP and the broker, gracefully stop the explicitly selected guest
containers whose labels match their reservation IDs and
`io.anki-review.managed=provisioner-v1`, and retain their storage. Restore the matching
previous binaries/configuration and remove `ACCOUNT_SOCKET` from HTTP to return
to single-account operation. Keep all guest registry/directories and backing storage
for recovery. The owner remains on the original endpoint/volume. Never restore a
guest collection over the owner's volume, and never use broad Docker prune/volume rm.

### Native audio image maintenance

An audio-only update also changes the broker's pinned guest image. Ordinary
`S up`, `S restart` and single-owner setup are not the multi-account update path.
Use an explicit maintenance window:

1. Run `S status` and detect `mode: multi-account` (or configured `ACCOUNT_SOCKET`)
   before kit comparison/upload. Complete the saved-pass checks above, stop HTTP,
   and inspect every saved account pass with `check-passes.py`. Obtain the relevant
   owner's decision for checking/confirmed states before any later unlock. Stop the
   broker, then gracefully stop only verified managed guest containers and the owner
   desktop for the consistent offline backups below. Retain matching old binaries,
   kit, image ID/tag and daemon configuration before replacing anything.
2. Verify each selected guest's container name and `io.anki-review.managed=provisioner-v1`,
   `io.anki-review.account` and `io.anki-review.reservation` labels against its private
   reservation. Remove only those stopped, verified guest containers by exact ID,
   without `-v`. Retain each volume, network, `storage.img`, filesystem metadata,
   mount/backing identity, keys and `reservation.json`; never call broker DELETE or
   remove a reservation to force an image change. Keep unselected containers intact.
3. Transfer the matching kit and both binaries. Build the pinned new image and refresh
   the owner desktop using the desktop-only `compose up ... --no-deps anki` command
   above, with the same named owner volume, endpoint, secrets, network, UID/GID and
   sync settings. Do not restart sync merely for this audio-only update.
4. Install the new broker with the four saved administrator values as above. Its
   reconciliation calls `Ensure` for the existing reservations, verifies/remounts
   their existing storage and recreates the removed guest containers from the new
   image with the same endpoints and keys. Verify image, labels and storage identity
   before starting the matching HTTP binary. Preserve every account's vault, pass
   and preferences. Accounts load passively; no diagnostic should unlock them or press **Resume**.

Do not reset the owner's pass or replace its collection to refresh old rendered HTML.
Saved HTML remains unchanged until a normal next pass. For rollback, stop HTTP and
the broker again, stop/remove only the verified replacement guest containers, retain
their storage/networks/keys/reservations, restore matching old kit/image and binaries,
and let the old broker's `Ensure` recreate them. Restore the owner desktop with its
original volume and settings. Never use broad Docker cleanup.

### Account-aware backups and restore

For a consistent offline backup, stop HTTP first, then the broker, and gracefully
stop only the relevant labelled Anki containers. The existing owner may have a
separate sync container: preserve its documented backup/sync direction checks.
Archive the entire application DATA_DIR, including `accounts.json` and each account's
vault/state/preferences. For every broker-owned opaque ID, the complete recovery set
is `/var/lib/anki-review-provisioner/<id>/reservation.json`, `filesystem.json`,
`storage.img`, `ankiconnect_key` and `vnc_password`, plus the saved daemon configuration.
The collection and media live inside storage.img, mounted at `<id>/mount`.

Before copying storage.img, keep HTTP/broker and that guest container stopped. Verify
that findmnt reports the expected ext4 UUID from filesystem.json at the exact mount
path, and that losetup reports storage.img as the exact backing file for its source
loop device. Unmount only that verified `<id>/mount`, then detach only its verified
loop device if it remains attached. Abort if either identity check or unmount fails.
Archive the complete per-ID directory only after it is unmounted and detached; this
preserves the filesystem UUID, SQLite journals and media together. Do not call the
broker DELETE operation for backup: it deletes the collection. Leave containers
stopped until the broker verifies/remounts their quota filesystem before starting them.

Use encrypted root-only backup storage; keys remain sensitive even though application
vaults are encrypted. Never copy a changing SQLite database or a mounted backing
image. An alternative online backup needs SQLite's backup API, consistent media and
a separately tested logical restore procedure; it is not interchangeable with this
offline filesystem-image recovery set.

Restore each complete per-ID directory to its original absolute broker root and
restore matching daemon UID/GID/quota settings. Preserve file ownership, private
permissions, filesystem UUID and original storage.img backing path before starting the
broker and HTTP. Do not regenerate keys against restored vaults. Verify only public status
without cookies; valid sessions may recover, while interrupted saved passes await **Resume**. Test recovery on disposable copies. Deleting an application vault destroys
access to its encrypted grader session; there is no password recovery. Deprovision
only explicitly selected broker-labelled guest resources after backup, never the
reserved owner account or another guest's mount. The broker's Remove operation
is for uncommitted provisioning cleanup and must not bypass a sealed account vault.
