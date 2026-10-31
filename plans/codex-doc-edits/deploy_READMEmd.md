# Mechanical edits: deploy/README.md

Base: 3422c81bf8c0a9a57d99dd3a2f43bb0c24a862bc. Generated with the canonical plan_edits helper.

**E1.** (lines 1-20)

````md E1-old
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
````

````md E1-new
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
````

**E2.** (lines 23-32)

````md E2-old

- On the server: Linux x86_64 with systemd, Docker Engine with the compose plugin, curl. A deploy
  user in the `docker` group with sudo. Outbound HTTPS to GitHub, git.sr.ht, the Debian mirrors,
  claude.ai, the Anthropic API and AnkiWeb (AnkiWeb mode only). Optional: polkit 0.106 or newer, for
  updates without sudo.
- On your machine: Go, SSH access as the deploy user, and Claude Code logged in to your account (for
  `claude setup-token`, see step 6).

## Steps

````

````md E2-new

- On the server: Linux x86_64 with systemd, Docker Engine with the compose plugin, curl. A deploy
  user in the `docker` group with sudo. Outbound HTTPS to GitHub, git.sr.ht, the Debian mirrors,
  claude.ai, the Anthropic API, OpenAI/ChatGPT and AnkiWeb (AnkiWeb mode only). Optional: polkit 0.106 or newer, for
  updates without sudo.
- On your machine: Go, SSH access as the deploy user, and the selected grader's account. Claude uses `claude setup-token` (step 6); Codex uses a dedicated
  [headless device session](#codex-device-session).

## Steps

````

**E3.** (lines 37-43)

````md E3-old
1. Build the binary: `make build-linux`.
2. Copy the kit and the binary (`scp` takes `-P` for the port):
   `SSH 'mkdir -p DIR/bin && chmod 700 DIR'`,
   `scp -r deploy/anki deploy/systemd deploy/install.sh deploy/smoke.sh <user>@<server>:DIR/`,
   `scp bin/anki-review-linux-amd64 <user>@<server>:DIR/bin/anki-review`.
3. Start Anki as the deploy user, no sudo: `SSH DIR/anki/up.sh` (AnkiWeb) or
   `SSH DIR/anki/up.sh self-hosted`. It creates the AnkiConnect key, the VNC password and the sync
````

````md E3-new
1. Build the binary: `make build-linux`.
2. Copy the kit and the binary (`scp` takes `-P` for the port):
   `SSH 'mkdir -p DIR/bin && chmod 700 DIR'`,
   `scp -r deploy/anki deploy/systemd deploy/install.sh deploy/smoke.sh deploy/provider.sh deploy/test-provider.sh deploy/autodeploy.sh <user>@<server>:DIR/`,
   `scp bin/anki-review-linux-amd64 <user>@<server>:DIR/bin/anki-review`.
3. Start Anki as the deploy user, no sudo: `SSH DIR/anki/up.sh` (AnkiWeb) or
   `SSH DIR/anki/up.sh self-hosted`. It creates the AnkiConnect key, the VNC password and the sync
````

**E4.** (lines 53-59)

````md E4-old
   `SSH cat DIR/anki/secrets/sync-password` and choose **Download**. A rerun of `up.sh` then shows
   more than one deck.
5. **Human, sudo password:** `ssh -t -p <port> <user>@<server> sudo DIR/install.sh DIR`. The
   script installs the service user, Claude Code and the polkit rule (below), writes
   `/etc/anki-review/env` (root:anki-review 0640) without any token, leaves `DIR/install-state` (no
   secrets: the kit version and whether the rule is in place), runs `smoke.sh` and only then enables
   the service. It ends with `service: active` and `owner: setup needed (open the setup page)`.
````

````md E4-new
   `SSH cat DIR/anki/secrets/sync-password` and choose **Download**. A rerun of `up.sh` then shows
   more than one deck.
5. **Human, sudo password:** `ssh -t -p <port> <user>@<server> sudo DIR/install.sh DIR`. The
   script installs the service user, both grading CLIs and the polkit rule (below), writes
   `/etc/anki-review/env` (root:anki-review 0640) without any token, leaves `DIR/install-state` (no
   secrets: the kit version and whether the rule is in place), runs `smoke.sh` and only then enables
   the service. It ends with `service: active` and `owner: setup needed (open the setup page)`.
````

**E5.** (lines 64-80)

````md E5-old
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

````

````md E5-new
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
`bin/codex-exec-server` and `resources`, is verified before extraction with SHA256
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

````

**E6.** (lines 211-223)

````md E6-old
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
````

````md E6-new
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
````
