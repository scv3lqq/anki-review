# Server modes

The flow of deploy/README.md, driven from this machine. `S` below is
`.agents/skills/setup-anki-review/server.sh` (run `S` alone for its commands). It reads
`DEPLOY_SSH`, `DEPLOY_SSH_PORT`, `DEPLOY_DIR`, `DEPLOY_PORT` (the service's PORT on the host, default
8767), `DEPLOY_URL`, `DEPLOY_SYNC` and `DEPLOY_SYNC_URL` from `.env`, runs ssh in batch mode and
keeps every secret out of argv and output. Out of scope: the reverse proxy and its access control,
which stay the user's (deploy/README.md).

## Process sessions

Run helpers through Codex's available shell tools under the current session's permissions.
For a long operation, use `exec_command` with `tty: true` and a short `yield_time_ms`
(for example, 1000). Record the returned `session_id` together with its command and purpose.
Poll that session with `write_stdin`, empty `chars`, and waits of at most 30 seconds; handle
progress and user input between polls. For finite commands, a pending session is not a successful
exit: check the final exit code and the helper's reported result before dependent steps.

If a local script later streams input to SSH, redirect preliminary non-streaming SSH or
helper probes with `</dev/null`; they can otherwise consume the pending script. Preserve
stdin for intended script, archive and secret streams. For a multi-step remote backup or
repair, require exit zero, expected artifacts and a unique terminal completion marker
emitted after all postconditions pass. EOF alone can exit zero without doing the work.

Start each tunnel in its own process session. Keep its ID separate from install or wait-helper
IDs. Tunnels deliberately keep running: verify the expected local forwarding listener
(`127.0.0.1:5901` for VNC, `127.0.0.1:8768` for the app) with a read-only local listener check,
check the session for forwarding errors, and continue while its tracked session remains live.
Do not wait for the tunnel to exit before using it or add a raw SSH or credential-bearing probe.
To stop a tunnel created by this workflow, send Ctrl-C (`chars: "\u0003"`) with
`write_stdin` to that tunnel's PTY session and verify that it exits. Never stop a tunnel or
service that predates this workflow. Use this cleanup on completion, cancellation, or failure.

Do not end a turn merely because a process is pending. Ask for required human input through
an available question tool and continue independent work. If the runtime needs a new user
turn to receive that input, retain the owned session IDs, or close the owned tunnel and reopen
it when work resumes. Never leave an untracked process. If process sessions are unavailable,
report that limitation instead of assuming a background-task API exists.

## Runtime investigations

Outside `check` mode, before an authorized isolated runtime experiment, record a compact
contract in its brief: identical synthetic workload, image and resource limits, exact flags,
completion/failure signals, memory metric, required API/VNC checks, isolation and cleanup.
For WebEngine include renderer-termination signals, not just process exit or OOM status.
Compare short runs first; extend only the selected candidate. Freeze the contract before
expensive runs, revising it only when new evidence changes the question. Identify diagnostic
flags and validate the exact shipping flags separately.

## Grading provider

The operating agent is independent of the grading provider. The kit installs Claude Code and
Codex CLI 0.166.0; keep existing Claude token commands for Claude users. `S install` forwards
`GRADER_PROVIDER`, `CODEX_MODEL` and existing Claude model settings. Local binary paths are
not sent to the server; set custom server paths explicitly with `sudo env CODEX_BIN=...`
or `CLAUDE_BIN=...` when running its installer. The environment
selects only the first setup default; a saved encrypted provider choice wins. A changed
`provider.sh` or `test-provider.sh` requires full server mode, like an installer/unit change.

For Codex, the owner follows the exact [device session procedure](../../../../deploy/README.md#codex-device-session):
private tmpfs `CODEX_HOME`, `-c 'cli_auth_credentials_store="file"' login --device-auth`,
browser user code with device authentication enabled in ChatGPT security settings, secure
SCP of the whole auth.json to a private local staging file, then the setup/settings file picker
through the authenticated page/SSH tunnel. Never read auth, user codes or CLI login output.
Delete only the named staging directories after successful save. Keep the dedicated session
exclusive to this service. Refresh is resealed automatically; revoked refresh needs a new login.
The macOS local staging file lives on disk until deleted; Linux call credentials require tmpfs.
A configured recovery key preserves valid browser sessions across restart; login and recovery load saved passes passively until explicit Resume.

## Settings and access

- Missing `DEPLOY_SSH` (`user@host` of the deploy user: docker group, sudo) or `DEPLOY_SSH_PORT`
  (default 22): ask once, together with `DEPLOY_URL` (optional: the public URL behind the user's
  proxy), and write them into `.env` (rules in SKILL.md). After the first `S precheck`, write the
  absolute dir its `dir` line names into `DEPLOY_DIR`.
- `DEPLOY_SYNC`: `ankiweb` (the default) or `self-hosted`, Anki's own sync server on the host
  (deploy/README.md, "Sync server"). First deploy without it: ask once, together with `DEPLOY_SSH`
  (offer AnkiWeb or self-hosted in the same question), and write it into `.env`. Unset on a rerun:
  the host keeps its mode. `DEPLOY_SYNC_URL` (self-hosted only): the HTTPS URL the user's proxy gives the
  sync server, ending in `/`; ask for it when the proxy is set up (see Switching to self-hosted).
- `DEPLOY_AUTO`: `on` or `off`, the host pulls and ships signed commits of `main` itself
  (deploy/README.md, "Auto-deploy"). Unset: the host keeps what it has. Ask only when the user asks
  for auto-deploy or CD; `on` needs SSH-signed commits (`git config gpg.format` is `ssh`).
- `Permission denied (publickey)`: ssh needs a key or an agent; the user runs
  `ssh-copy-id -p <port> <host>` once (their password). `Host key verification failed`: ask, then
  run `ssh -o StrictHostKeyChecking=accept-new -p <port> <host> true`, which records the key the
  host shows now.
- Before a sudo handoff, check whether current instructions explicitly authorize an
  existing credential source such as a named `gopass` entry. That authorization permits
  using that source for the authorized operation only; it does not expand maintenance
  permissions, enable writes in `check` mode, or authorize owner/grader credential access.
  Discover only relevant entry names, never display their values. Prefer an already
  authorized source to asking for manual entry again.
- For authorized stored sudo credentials, finish non-streaming SSH probes first with
  stdin redirected from `/dev/null`. Stage the static maintenance script before piping
  the password directly from the store to `sudo -S -p ''` over non-PTY SSH. Keep shell
  tracing off and never put the value in argv, logs, chat, `.env`, a temporary file or
  the clipboard. Preserve the configured SSH target/port, quoting and saved-pass/mode
  checks when adapting the helper transport; a sudo route never bypasses those guards.
- Without an authorized stored source, use a visible terminal for manual sudo. In tmux,
  inspect the current session and `tmux list-clients -F '#{client_session}'` before
  prompting. `S window` can create its task window in the agent's detached session.
  Move only the newly created task window into the intended attached session and select
  it before telling the owner to type. If the intended client is ambiguous, establish
  the intended terminal first; do not guess or wait on an invisible prompt.
- Outside tmux, `S window` drives Terminal through AppleScript; the first time macOS may ask
  whether the current terminal app may control Terminal: the user clicks OK.

## Pass in progress

`S status` prints `api stage`; anonymous status cannot prove a saved pass is idle when locked.
Before maintenance inspect saved passes privately using `check-passes.py` and preserve backups.
Restart, login and recovery load current saved passes passively; checking/confirmed work requires
the owner's explicit Resume. Diagnostics never unlock accounts or press Resume. Old binaries may
resume automatically on login: inspect their saved passes before any login or upgrade.

Installers provision `/etc/anki-review/session.key` only when no key setting exists. Preserve the
remote configured path; do not forward a local `SESSION_KEY_FILE` path to a different host. Existing
accounts need one password login after first enabling recovery. Inspect key metadata only, never
bytes; back up the key separately and protect full host backups containing key plus data. Configured
missing/unsafe keys and recovery with an empty key path fail closed. Follow the explicit offline
lost-key/rotation migration in deploy/README.md; never regenerate keys or reset vaults as repair.

## Mode `server`

Every step is safe to rerun; on a rerun skip what is already there, as noted per step.

1. `.agents/skills/setup-anki-review/check.sh` (needs `go` ok; the local Anki lines don't matter
   here), `make check` (or `go test ./...` without golangci-lint), `make build-linux`.
2. `S precheck`: any `FAIL` stops; show the lines and the fix. Docker group: the user runs
   `sudo usermod -aG docker <user>` on the host and logs in again. A container `anki-desktop` or a
   service `anki-review` from another dir is not ours: never touch it. Ours (this `DEPLOY_DIR`)
   means a rerun.
3. Rerun only: `S status` and `S kit-diff`, before anything is uploaded; keep both results.
4. `S upload`, then `S up` in a process session (the first build downloads about 200 MB).
   Its output ends with `decks: N`, plus a hint line when N is 0 or 1. The line before it is
   `sync: AnkiWeb` or `sync: self-hosted on 127.0.0.1:8766, ...`.
5. Sync login, only when N is 0 or 1 (an empty profile):
   Before closing either login tunnel, complete the persisted-auth check in
   [Sync login and VNC clipboard](#sync-login-and-vnc-clipboard).
   - AnkiWeb (`DEPLOY_SYNC` unset or `ankiweb`):
     - `S tunnel` in its own process session, `S vnc-password` (into the clipboard,
       never shown), `open vnc://127.0.0.1:5901` (Linux: a VNC viewer on `127.0.0.1:5901`).
     - Tell the user: "Paste the password (Cmd+V) into Screen Sharing. In Anki click Sync, log in to
       AnkiWeb, and when it asks, choose **Download from AnkiWeb**. Never Upload: that would replace
       your AnkiWeb collection with this empty one. I watch for the decks."
     - Run and poll `S wait-decks N` in a process session. When it prints the new count:
       `S clip-clear`, stop the owned tunnel session, tell the user the media keeps syncing on the
       server and the Screen Sharing window can close. Timeout: ask what Anki shows.
   - Self-hosted:
     - `S status` shows `no collection yet` on its `sync:` line: the collection has to reach the server
       first, from a device that has it: steps 1 to 5 of Switching to self-hosted, then come back here.
       A user starting with no cards at all skips that.
     - `S tunnel` in its own process session, `S vnc-password`,
       `open vnc://127.0.0.1:5901`. Tell the user: "Paste the password (Cmd+V) into Screen Sharing. In
       Anki: Tools, Preferences, Syncing (the field is at the bottom of the AnkiWeb Account box): Log
       Out if it shows an account, set Self-hosted sync server to `http://sync:8766/`, close
       Preferences and click Sync. Tell me when the login form shows."
     - `S sync-password`, then follow [Sync login and VNC clipboard](#sync-login-and-vnc-clipboard).
       If shared-clipboard controls are unavailable, use its temporary X11 helper before the handoff.
       Tell the user: "Log in as `anki` and paste with Ctrl+V or the password field's Paste menu.
       Choose **Download**: the server has your collection. I watch for the decks."
     - Run and poll `S wait-decks N` in a process session. When it prints the new count:
       `S clip-clear`, stop the owned tunnel session. Timeout: ask what Anki shows.
6. Knowledge decks: `S deck-names` lists the server collection's top-level decks with the kind
   `KNOWLEDGE_DECKS` from `.env` gives them. First deploy, or a `missing` line: ask as in SKILL.md
   step 3 and set `KNOWLEDGE_DECKS` in `.env`. Note whether it changed in this run.
7. `install.sh`. Skip when step 3 showed `service: active`, a `polkit rule:` that is installed (or
   skipped for missing or old polkit, which a rerun won't change), `kit-diff` matching,
   `KNOWLEDGE_DECKS` unchanged, and an `auto-deploy:` line that matches `DEPLOY_AUTO` (unset matches
   anything). Otherwise, with a pass in progress (see Pass in progress), ask first. `DEPLOY_AUTO=on`:
   `S signers` first (it writes `DIR/allowed_signers` from the signing key and checks it against
   HEAD).
   Choose the credential route above before opening an interactive window. With an
   explicitly authorized stored sudo source, run the same checked installer operation
   through a tracked non-PTY SSH session and require its actual exit code. Otherwise,
   `S install` opens a window with
   `ssh -t ... sudo env KNOWLEDGE_DECKS=... DIR/install.sh DIR` (absolute paths, quoted for
   the host) and prints an exit file. Verify that window is visible before asking for
   manual sudo; run and poll `S wait-install <file>` in a process session. Inspect its printed
   `window ended, exit code N`: `N` is the install window's result; the helper's own exit code 0
   only means its wait completed. A nonzero printed `N` means the install failed: inside tmux
   read `tmux capture-pane -p -J -S -200 -t anki-review-install` (install.sh prints no secrets),
   else ask for the last lines; name the failing step, fix it, and rerun within the authorized
   scope. Ask only if the fix needs a new consequential action.
   If it prints `service restarted (the window is still open)`, verify with `S status` and report
   the observed restart separately; that message does not prove the install window exited successfully.
   - No token is involved: install.sh takes none. An older deploy that kept the Claude token and the
     AnkiConnect key in `/etc/anki-review/env` gets them removed by this run, and says so in one
     line.
8. Setup page (the owner, selected grading credentials and the AnkiConnect key are entered here, not in the
   shell). Skip when the latest `S status` shows `owner: unlocked`: run it after step 7, since a run
   that installed restarted the service and the step 3 result is stale (when step 7 was skipped,
   step 3 holds). An owner already set up gets the login form instead: the user logs in, and the
   wait below is the same.
   - `S anki-key`: the host's AnkiConnect key goes into the clipboard, never shown. The user pastes
     it into the page's AnkiConnect key field.
   - The page: with `DEPLOY_URL` set, the user opens that URL. Unset: `S app-tunnel` in its
     own process session (it forwards the host's service to
     <http://127.0.0.1:8768/>), and the user opens that URL.
   - Tell the user: "Fill in a login and a password of at least 12 characters (twice). Paste the
     AnkiConnect key (Cmd+V). Select your grading provider. For Codex, import the dedicated auth.json using the procedure above. For Claude, run `claude setup-token` in a terminal on your
     computer, approve it in the browser and paste the `sk-ant-oat01-...` token it prints. Press
     Set up. I won't see any of it." Before that, explain the token source as in SKILL.md (the rules
     list how to get each one).
   - Run and poll `S wait-setup` in a process session. It prints `owner: unlocked` when the
     owner is set up and logged in on the page (30 min at most). Then `S clip-clear` and stop the
     owned app tunnel session. Timeout: ask what the page shows.
9. `S status`: expect `service: active`, `owner: unlocked`, `api stage: idle`, `decks:` (a number),
   `binary: matches`, `polkit rule: installed for <user>` (skipped: updates will need sudo; tell the
   user why), `sync:` matching `DEPLOY_SYNC` (self-hosted: `running on 127.0.0.1:8766, collection
   ...`), `auto-deploy: on, deployed ...` with `DEPLOY_AUTO=on` (the first run comes a minute
   after the install). `DEPLOY_URL` set: `S verify-url` (a 401, 403 or a login page is the proxy's access control:
   fine, the user checks in a browser; it also checks `DEPLOY_SYNC_URL` when set: `health: 200`).
   Unset: the service listens on the host's `127.0.0.1:<DEPLOY_PORT>` (8767 by default); the app
   tunnel of step 8 reaches it from here.

## Mode `server-update`

Updating the binary needs no sudo prompt when the host has the polkit rule (install.sh from
this kit, polkit 0.106+). Valid browser sessions recover with the configured server key after a
restart until their original expiry; legacy mode or an expired session needs password login.
Existing accounts need one password login after first enabling recovery. Step 5 verifies public
status without requiring login, and interrupted checking/saving still waits for explicit Resume.

`S status` shows `auto-deploy: on`: a signed commit pushed to `main` ships itself, so a commit plus
`git push` is the update. This mode is then for a build that is not on `main`, or for what
auto-deploy holds (`auto-deploy: holding ...`): a changed kit is mode `server`, a pass in progress
waits on its own.

1. `make check` (or `go test ./...`), `make build-linux`, `S precheck update` (no service from this
   `DEPLOY_DIR`: mode `server`).
2. `S status` before `S kit-diff`, any upload or image/setup command. Detect
   `mode: multi-account` or enabled `ACCOUNT_SOCKET` first: stop the ordinary update
   flow and use Multi-account maintenance below and deploy/README.md's native audio
   image maintenance. If account mode cannot be established, stop before mutations.
   Public multi-account status cannot prove saved passes idle: stop HTTP for private
   `check-passes.py` inspection of every account, then stop the broker and take the
   account-aware offline backups before image/binary changes. For a single owner,
   follow Pass in progress before a restart; retain matching old binaries, kit,
   image and settings for rollback. A `sync:` line differing
   from `DEPLOY_SYNC` requires Switching to self-hosted, not a plain update.
3. Only after those checks, run `S kit-diff`. For an audio-only `anki/` change,
   stop HTTP and gracefully stop the owner desktop, then take consistent
   application/collection backups before `S upload` transfers the matching kit.
   Refresh only the stopped desktop as the deploy user with the recorded UID/GID/port environment:
   `docker compose --project-directory DIR/anki -f DIR/anki/compose.yaml up -d --build --no-deps anki`.
   Keep the same named collection volume, endpoint, secrets, network and sync settings;
   do not restart sync for an audio-only update. Do not run `S up` for an audio-only update.
   The pinned native adapter resolves HTML markers using the same native render's
   ordered AV lists, never guessed note fields; unsupported TTS displays `Audio unavailable`.
   Do not reset the owner's pass or unlock automatically to refresh saved HTML:
   existing saved HTML stays intact until the normal next pass. Follow the matching
   image/binary rollback procedure in deploy/README.md. For an audio-only desktop
   refresh, start the matching HTTP binary, verify public status without cookies and
   finish here without a login request or `S wait-setup`. For an ordinary full setup
   change to `anki/`, use mode `server` and its existing `S up` semantics after these
   checks. `install.sh`, `smoke.sh`, `provider.sh`, `test-provider.sh` or unit changes
   also use mode `server` (one sudo password); multi-account mode always uses explicit maintenance.
4. `S upload-bin`, then `S restart`. Refused (an older deploy without the rule, or no
   polkit): follow the credential route above. For manual sudo, `S sudo-restart` opens
   a window; verify its visibility, then run and poll `S wait-install <file>` in a
   process session. Inspect the printed `window ended, exit code N`; the helper's own
   exit code 0 only means the wait completed. With an authorized stored source, require
   the actual tracked SSH/sudo exit code instead. A nonzero operation result means the
   restart failed: follow the install-window diagnostic flow in mode `server`, step 7,
   before retrying within scope.
   `service restarted (the window is still open)` confirms an observed restart; verify with
   `S status` and do not report it as a successful final window exit.
   Afterwards suggest mode `server` once, which adds the rule (no polkit:
   `apt install polkitd` on
   the host first). An ssh error is not a refusal: fix access and rerun.
5. Verify `S status` reports `binary: matches` and `service: active`, then `S verify-url` when
   `DEPLOY_URL` is set. Public status may still say locked before a valid browser session recovers;
   do not require login or wait for public unlock as an update diagnostic. Existing sessions retain
   their original expiry. First enablement needs one owner password login later on the normal page.
   Tell the owner that interrupted checking/saving waits for their explicit Resume. If the owner
   requests page access, start only the needed `S app-tunnel` when `DEPLOY_URL` is unset and stop
   that owned tunnel after they finish. Never handle their password or trigger Resume.

## Switching to self-hosted

An existing deploy on AnkiWeb, `DEPLOY_SYNC=self-hosted` in `.env`. The cards keep their state: the
first sync with the empty server uploads the whole collection (schedules, review history, FSRS
state, deck options, media).

1. Tell the user: "Sync every device with AnkiWeb now (computer, phones; the server's Anki syncs on
   every pass), then review nowhere until I say so. On your computer, export a backup: File, Export,
   Anki Collection Package, with media and scheduling." Wait for their yes.
2. `S status`: a pass in progress is asked about first (see Pass in progress). `S upload`, then
   `S up` in a process session: its `sync:` line says `self-hosted on 127.0.0.1:8766`.
3. The proxy is the user's. Tell them what it needs: an HTTPS URL forwarding to `127.0.0.1:8766` on
   the host (its own name, or a path such as `/sync/` on an existing one, which then forwards to
   `http://127.0.0.1:8766/` with the path stripped); no login of the proxy in front (Anki can't pass
   one, the sync login guards the server); TLS 1.2 enabled (AnkiMobile fails with error -9836
   without it); a request body limit above the collection size (nginx: `client_max_body_size`, 1 MB
   by default; the server takes 100 MB by default); with Caddy, `read_buffer 512k` in the
   `reverse_proxy` transport. Ask for the URL, write `DEPLOY_SYNC_URL` (ending in `/`) into `.env`,
   run `S verify-url`: expect `health: 200`.
4. `S sync-password` (clipboard), then tell the user: "In Anki on your computer: Tools, Preferences,
   Syncing (the field is at the bottom of the AnkiWeb Account box): Log Out, set Self-hosted sync
   server to <DEPLOY_SYNC_URL>, close Preferences, click Sync and log in as `anki` with the password
   (Cmd+V). The server is empty, so Anki uploads your collection. Tell me when the media has finished (the sync button stops spinning)."
5. `S clip-clear`, `S status`: the `sync:` line shows `collection X MB, N media files`. N should match
   the number of files in the computer's `collection.media` folder
   (`ls <Anki2>/<profile>/collection.media | wc -l`, read-only); ask the user to sync once more when
   it is lower.
6. The server's Anki: mode `server` step 5, Self-hosted (VNC, `http://sync:8766/`, **Download**),
   even though its profile is not empty: it still points at AnkiWeb. The deck count does not change
   here, so instead of `S wait-decks` ask the user to tell you when the download has finished; then
   `S clip-clear`, stop the owned tunnel session, and `S decks` shows the same count as before.
7. Tell the user to switch the other devices: Log Out of AnkiWeb, set the custom sync server to
   <DEPLOY_SYNC_URL> (AnkiDroid 2.16 or newer: Settings, Sync, Custom sync server; AnkiMobile: the
   custom sync server in its sync settings), log in as `anki`, choose **Download**. Reviews can
   resume. AnkiWeb keeps its last copy until they delete the account there.

Back to AnkiWeb: `DEPLOY_SYNC=ankiweb`, `S up` (stops `anki-sync`, its volume stays), then every
device clears the self-hosted URL and logs in to AnkiWeb; the device with the latest reviews uploads,
the others download.

## Mode `check`, server part

`S status` and `S kit-diff`, read-only, when `DEPLOY_SSH` is already configured. Report missing
settings or failed checks without applying the Settings and access fixes, writing `.env`,
installing, uploading, restarting, or opening tunnels.

## Reliable interactive handoffs

Before a device-login retry, check the previous operation's metadata and cancel only that
owned login. After enabling device authentication, start a fresh login and have the owner
use the new code. Never capture the terminal while a code or credential may be visible.
Track the root pane, tunnels, helper process IDs and staging paths separately. Observed
completion advances the workflow; a late answer to an earlier question does not restart it.

Keep status metadata separate from credential files: only state, exit code and owned paths.
Write retry status atomically to a newly created 0600 file, set its final owner, then use
`os.replace`. Do not truncate a user-owned file in sticky `/tmp` from a root process:
Linux protected-regular rules can reject this even for root. For multiline remote Python,
prefer a named static script and compile it before transfer, or build argv with
`shlex.join`; do not nest shell quotes or turn literal `\n` into executable Python.

## Sync login and VNC clipboard

`Saved to Anki` followed by `sync: auth not configured` means the grades are already written
and the Anki profile lacks sync authentication. Do not press Save to Anki again. Use the
existing pass and backup checks before repairing login; never submit API writes as a probe.
The self-hosted account is `anki`, not the owner's app login or email. The internal URL
`http://sync:8766/` belongs in remote Anki Preferences, Syncing; it is not a browser URL on
the owner's computer. Check the endpoint before changing it. Report credential comparisons
only as booleans, never values, hashes, process environments or auth snapshots.

After GUI sync login, verify the active profile's saved `syncKey` presence in `prefs21.db`
using read-only SQLite and restricted pickle decoding that rejects globals; output only a
boolean. Do not guess the active profile when it is ambiguous. A successful sync may leave
authentication only in memory. If unsaved, use the pinned version's native profile-save path,
such as accepting Preferences or a clean shutdown, then verify again. Any restart still
follows the saved-pass and backup rules. Prefer native persistence before adding autologin
code; one missing stored key does not establish a need for repeated manual login.

Try `S sync-password` and Screen Sharing's Edit, Use Shared Clipboard, then Ctrl+V inside
Linux Anki. If sharing fails, place the password directly into the container's X11 clipboard
with a temporary Qt process; the owner still focuses the password field and pastes it.
Anki's standalone Python does not automatically include its bundled Qt packages: the pinned
image needs `/opt/anki/app_packages` on `sys.path`. Run this static script through an owned
process session in `anki-desktop`, using `/opt/anki/python/bin/python3 -I`. Send the existing
sync password file through stdin on the VPS; its value must never enter argv or output.

```python
import os
import signal
import sys
from types import FrameType


def main() -> None:
    # The bundled GUI packages are separate from the standalone Python runtime.
    sys.path.insert(0, "/opt/anki/app_packages")
    from PyQt6.QtCore import QTimer
    from PyQt6.QtGui import QClipboard
    from PyQt6.QtWidgets import QApplication

    app = QApplication([])
    clipboard = app.clipboard()
    password = sys.stdin.buffer.read(256).strip()
    if len(password) != 24 or not password.isalnum():
        raise SystemExit("Invalid sync password input")
    clipboard.setText(password.decode("ascii"), QClipboard.Mode.Clipboard)

    def cleanup() -> None:
        if clipboard.ownsClipboard():
            clipboard.clear(QClipboard.Mode.Clipboard)

    def stop(_signal: int, _frame: FrameType | None) -> None:
        app.quit()

    def heartbeat() -> None:
        pass

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    timer = QTimer()
    timer.timeout.connect(heartbeat)
    timer.start(200)
    app.aboutToQuit.connect(cleanup)
    QTimer.singleShot(300_000, app.quit)
    print(f"clipboard_ready pid={os.getpid()}", flush=True)
    app.exec()


main()
```

Pass only the static script as `-c` or as a named script file; the secret is the redirected
stdin of `docker exec -i`, not the script text. Execute the redirection on the VPS, where
`DIR/anki/secrets/sync-password` exists. Wait for `clipboard_ready` before asking the owner
to paste. Tell them to use login `anki`, clear the password field, then use Ctrl+V or its
right-click Paste menu. Copying a URL afterward can overwrite the password through shared
clipboard. On success stop only the reported helper PID and run `S clip-clear`. The process
also exits after five minutes and clears the clipboard only if it still owns that selection.

## Consistent backups before sync repair

Back up both collections before login can cause a full sync. Use SQLite's backup API for
an open client collection and preferences; bound busy retries with a progress callback and
a deadline rather than waiting indefinitely. A running sync server may hold an exclusive
lock. If that prevents an online backup, briefly pause only `anki-sync`, copy its database
and any existing `-wal` and rollback `-journal` sidecars while frozen, and unpause in a
guaranteed cleanup path. Resume the server before inspecting the copied database. Never
copy a changing live database without its journals. Recover and create canonical snapshots
through SQLite from the writable private copy, not from live files.

Container tmpfs may be visible to `docker exec` while `docker cp` cannot archive it. Stream
only the task-owned backup directory with `docker exec ... tar -cf -` into a root-only host
backup directory, with pipe failure handling. Remove only that named temporary directory.
Anki uses the custom SQLite collation `unicase`: generic Python may reject a whole-database
`quick_check` without it. A table-scoped `quick_check(revlog)` and comparison of review IDs
can verify the review transfer without changing a live database or rebuilding indexes.

A forced Upload/Download prompt must first be cancelled. Compare client-only and server-only
review IDs in the snapshots and check for other collection changes before choosing a
direction. New client reviews must not be overwritten by Download; Upload can overwrite
changes from other devices. After ordinary sync, a read-only snapshot comparison can prove
that the server contains the pending client reviews. Print only counts and safe booleans.

## Multi-account maintenance

When `ACCOUNT_SOCKET` is enabled, follow the multi-account section of deploy/README.md.
For the pinned native audio image, stop HTTP and the broker after saved-pass checks,
then complete the offline backups before image changes. Stop/remove only exact,
verified managed guest containers; retain their volumes, networks, backing files,
keys and reservations. Refresh the owner desktop with its existing named volume,
secrets, endpoint, preferences and sync settings using desktop-only compose
`--no-deps`; leave sync running for this audio-only change. Install the matching new
broker/image and let its reconciliation call `Ensure` on existing reservations to
recreate the removed guests without replacing their storage. Preserve all saved
passes and vaults, never reset the owner's pass or unlock accounts automatically.
Existing saved HTML stays intact until the normal next pass. Keep the matching old
kit, image and both binaries for rollback as described in deploy/README.md.

The ordinary wait-setup/restart/status assumptions do not apply: unauthenticated
status intentionally omits every account's unlocked/stage. Helpers must detect and
report multi-account mode, refuse automated restart and stop waiting for public unlock.
Auto-deploy remains held. Build and transfer both Linux binaries, the broker unit,
installer and check-passes.py; administrator installation updates the broker only.

Do not change the owner's collection, endpoint, credentials or pass during migration.
Guests start empty and no-sync, and import compatible packages through their own page.
Use explicit dedicated credentials per account; never reuse host Claude login or a
Codex device snapshot. No setup/verification call should register a real account or
import into the existing collection. Maintenance first stops HTTP, then inspects every
saved account pass privately with check-passes.py. Keep backups and obtain the owner's
decision about checking/confirmed states before any account presses Resume; login and recovery are passive.
Root-only broker metadata, keys, quota backing filesystems and all guest app data
are part of backup/restore; do not run broad resource cleanup.

Before running a custom maintenance script, independently check these recovery points:

- Rollback restores both installed binaries and their deploy-kit staging copies.
  The HTTP unit's `ExecStartPre` installs `DIR/bin/anki-review` on every start; restoring
  only `/usr/local/bin/anki-review` is undone by the next start. Keep the matching broker
  configuration and unit, restore them on failure, and verify both restored binaries.
- An active broker unit is not proof that asynchronous guest reconciliation succeeded.
  Before reporting recovery, wait within a finite bound for every saved reservation's
  expected ext4 UUID, exact loop backing file, matching container labels and running
  container. These are read-only checks; zero reservations is a valid empty set.
- Require the real process exit code and the final postcondition marker. Any non-secret
  stage/result file consumed by the deploy user must be readable by that user, while
  backup keys, vaults and auth remain administrator-only.

## Report

Each step as done, skipped (already there) or failed; what the user still has to do (the setup
page, the login after a restart); the URL.
