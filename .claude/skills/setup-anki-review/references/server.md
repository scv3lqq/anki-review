# Server modes

The flow of deploy/README.md, driven from this machine. `S` below is
`.claude/skills/setup-anki-review/server.sh` (run `S` alone for its commands). It reads
`DEPLOY_SSH`, `DEPLOY_SSH_PORT`, `DEPLOY_DIR`, `DEPLOY_PORT` (the service's PORT on the host, default
8767), `DEPLOY_URL`, `DEPLOY_SYNC` and `DEPLOY_SYNC_URL` from `.env`, runs ssh in batch mode and
keeps every secret out of argv and output. Out of scope: the reverse proxy and its access control,
which stay the user's (deploy/README.md).

## Settings and access

- Missing `DEPLOY_SSH` (`user@host` of the deploy user: docker group, sudo) or `DEPLOY_SSH_PORT`
  (default 22): ask once, together with `DEPLOY_URL` (optional: the public URL behind the user's
  proxy), and write them into `.env` (rules in SKILL.md). After the first `S precheck`, write the
  absolute dir its `dir` line names into `DEPLOY_DIR`.
- `DEPLOY_SYNC`: `ankiweb` (the default) or `self-hosted`, Anki's own sync server on the host
  (deploy/README.md, "Sync server"). First deploy without it: ask once, together with `DEPLOY_SSH`
  (AskUserQuestion: AnkiWeb, or self-hosted), and write it into `.env`. Unset on a rerun: the host
  keeps its mode. `DEPLOY_SYNC_URL` (self-hosted only): the HTTPS URL the user's proxy gives the
  sync server, ending in `/`; ask for it when the proxy is set up (see Switching to self-hosted).
- `DEPLOY_AUTO`: `on` or `off`, the host pulls and ships signed commits of `main` itself
  (deploy/README.md, "Auto-deploy"). Unset: the host keeps what it has. Ask only when the user asks
  for auto-deploy or CD; `on` needs SSH-signed commits (`git config gpg.format` is `ssh`).
- `Permission denied (publickey)`: ssh needs a key or an agent; the user runs
  `ssh-copy-id -p <port> <host>` once (their password). `Host key verification failed`: ask, then
  run `ssh -o StrictHostKeyChecking=accept-new -p <port> <host> true`, which records the key the
  host shows now.
- Outside tmux, `S window` drives Terminal through AppleScript; the first time macOS may ask
  whether the current terminal app may control Terminal: the user clicks OK.

## Pass in progress

`S status` prints `api stage`. A restart (install.sh, `S restart`, `S sudo-restart`) can resume a
saved pass only after the owner unlocks the server, so an `api stage` of a pass stage (anything
other than `idle`, `written`, `unknown (locked)` or `none (no owner yet)`) or `no answer` from the
host means: ask the user before a restart. A locked server resumes nothing on restart: no question.
A host still on the binary from before the login (`owner: n/a (older binary without a login)`)
reports its stage through the old `/api/cards`: the same rule applies. Its first restart onto the new
binary leaves the service with no owner, and the pass resumes after the setup on the page.

## Mode `server`

Every step is safe to rerun; on a rerun skip what is already there, as noted per step.

1. `.claude/skills/setup-anki-review/check.sh` (needs `go` ok; the local Anki lines don't matter
   here), `make check` (or `go test ./...` without golangci-lint), `make build-linux`.
2. `S precheck`: any `FAIL` stops; show the lines and the fix. Docker group: the user runs
   `sudo usermod -aG docker <user>` on the host and logs in again. A container `anki-desktop` or a
   service `anki-review` from another dir is not ours: never touch it. Ours (this `DEPLOY_DIR`)
   means a rerun.
3. Rerun only: `S status` and `S kit-diff`, before anything is uploaded; keep both results.
4. `S upload`, then `S up` with Bash `run_in_background` (the first build downloads about 200 MB).
   Its output ends with `decks: N`, plus a hint line when N is 0 or 1. The line before it is
   `sync: AnkiWeb` or `sync: self-hosted on 127.0.0.1:8766, ...`.
5. Sync login, only when N is 0 or 1 (an empty profile):
   - AnkiWeb (`DEPLOY_SYNC` unset or `ankiweb`):
     - `S tunnel` with `run_in_background` (keep its task id), `S vnc-password` (into the clipboard,
       never shown), `open vnc://127.0.0.1:5901` (Linux: a VNC viewer on `127.0.0.1:5901`).
     - Tell the user: "Paste the password (Cmd+V) into Screen Sharing. In Anki click Sync, log in to
       AnkiWeb, and when it asks, choose **Download from AnkiWeb**. Never Upload: that would replace
       your AnkiWeb collection with this empty one. I watch for the decks."
     - `S wait-decks N` with `run_in_background`, end the turn. When it prints the new count:
       `S clip-clear`, stop the tunnel task (TaskStop), tell the user the media keeps syncing on the
       server and the Screen Sharing window can close. Timeout: ask what Anki shows.
   - Self-hosted:
     - `S status` shows `no collection yet` on its `sync:` line: the collection has to reach the server
       first, from a device that has it: steps 1 to 5 of Switching to self-hosted, then come back here.
       A user starting with no cards at all skips that.
     - `S tunnel` with `run_in_background` (keep its task id), `S vnc-password`,
       `open vnc://127.0.0.1:5901`. Tell the user: "Paste the password (Cmd+V) into Screen Sharing. In
       Anki: Tools, Preferences, Syncing (the field is at the bottom of the AnkiWeb Account box): Log
       Out if it shows an account, set Self-hosted sync server to `http://sync:8766/`, close
       Preferences and click Sync. Tell me when the login form shows."
     - Then `S sync-password` and tell the user: "In Screen Sharing turn on Edit, Use Shared Clipboard.
       Log in as `anki` and paste the password with Ctrl+V (inside the session it is Ctrl, not Cmd; if
       it still won't paste, paste it into a local note and type it). Choose **Download**: the server
       has your collection. I watch for the decks."
     - `S wait-decks N` with `run_in_background`, end the turn. When it prints the new count:
       `S clip-clear`, stop the tunnel task (TaskStop). Timeout: ask what Anki shows.
6. Knowledge decks: `S deck-names` lists the server collection's top-level decks with the kind
   `KNOWLEDGE_DECKS` from `.env` gives them. First deploy, or a `missing` line: ask as in SKILL.md
   step 3 and set `KNOWLEDGE_DECKS` in `.env`. Note whether it changed in this run.
7. `install.sh`. Skip when step 3 showed `service: active`, a `polkit rule:` that is installed (or
   skipped for missing or old polkit, which a rerun won't change), `kit-diff` matching,
   `KNOWLEDGE_DECKS` unchanged, and an `auto-deploy:` line that matches `DEPLOY_AUTO` (unset matches
   anything). Otherwise, with a pass in progress (see Pass in progress), ask first. `DEPLOY_AUTO=on`:
   `S signers` first (it writes `DIR/allowed_signers` from the signing key and checks it against
   HEAD).
   `S install` opens a new window with
   `ssh -t ... sudo env KNOWLEDGE_DECKS=... DIR/install.sh DIR` (absolute paths, quoted for the
   host) and prints an exit file. Tell the user to type their sudo password there, run
   `S wait-install <file>` with `run_in_background`, end the turn. Exit code not 0: inside tmux
   read `tmux capture-pane -p -J -S -200 -t anki-review-install` (install.sh prints no secrets),
   else ask for the last lines; name the failing step, fix it, rerun after a yes.
   - No token is involved: install.sh takes none. An older deploy that kept the Claude token and the
     AnkiConnect key in `/etc/anki-review/env` gets them removed by this run, and says so in one
     line.
8. Setup page (the owner, the Claude token and the AnkiConnect key are entered here, not in the
   shell). Skip when the latest `S status` shows `owner: unlocked`: run it after step 7, since a run
   that installed restarted the service and the step 3 result is stale (when step 7 was skipped,
   step 3 holds). An owner already set up gets the login form instead: the user logs in, and the
   wait below is the same.
   - `S anki-key`: the host's AnkiConnect key goes into the clipboard, never shown. The user pastes
     it into the page's AnkiConnect key field.
   - The page: with `DEPLOY_URL` set, the user opens that URL. Unset: `S app-tunnel` with
     `run_in_background` (keep its task id; it forwards the host's service to
     <http://127.0.0.1:8768/>), and the user opens that URL.
   - Tell the user: "Fill in a login and a password of at least 12 characters (twice). Paste the
     AnkiConnect key (Cmd+V). For the Claude token, run `claude setup-token` in a terminal on your
     computer, approve it in the browser and paste the `sk-ant-oat01-...` token it prints. Press
     Set up. I won't see any of it." Before that, explain the token source as in SKILL.md (the rules
     list how to get each one).
   - `S wait-setup` with `run_in_background`, end the turn. It prints `owner: unlocked` when the
     owner is set up and logged in on the page (30 min at most). Then `S clip-clear` and stop the
     app tunnel task (TaskStop). Timeout: ask what the page shows.
9. `S status`: expect `service: active`, `owner: unlocked`, `api stage: idle`, `decks:` (a number),
   `binary: matches`, `polkit rule: installed for <user>` (skipped: updates will need sudo; tell the
   user why), `sync:` matching `DEPLOY_SYNC` (self-hosted: `running on 127.0.0.1:8766, collection
   ...`), `auto-deploy: on, deployed ...` with `DEPLOY_AUTO=on` (the first run comes a minute
   after the install). `DEPLOY_URL` set: `S verify-url` (a 401, 403 or a login page is the proxy's access control:
   fine, the user checks in a browser; it also checks `DEPLOY_SYNC_URL` when set: `health: 200`).
   Unset: the service listens on the host's `127.0.0.1:<DEPLOY_PORT>` (8767 by default); the app
   tunnel of step 8 reaches it from here.

## Mode `server-update`

No human step when the host has the polkit rule (install.sh from this kit, polkit 0.106+).

`S status` shows `auto-deploy: on`: a signed commit pushed to `main` ships itself, so a commit plus
`git push` is the update. This mode is then for a build that is not on `main`, or for what
auto-deploy holds (`auto-deploy: holding ...`): a changed kit is mode `server`, a pass in progress
waits on its own.

1. `make check` (or `go test ./...`), `make build-linux`, `S precheck update` (no service from this
   `DEPLOY_DIR`: mode `server`).
2. `S kit-diff`: only `anki/` files differ: `S upload` and `S up` (no sudo). `install.sh`,
   `smoke.sh` or the unit differ: mode `server` (one sudo password).
3. `S status`: a pass in progress (see Pass in progress) is asked about first. A `sync:` line that
   differs from `DEPLOY_SYNC` (`sync: AnkiWeb` with `DEPLOY_SYNC=self-hosted`): Switching to
   self-hosted, not a plain update.
4. `S upload-bin`, then `S restart`. Refused (an older deploy without the rule, or no polkit): `S
   sudo-restart` opens a window for the sudo password, `S wait-install <file>` in the background;
   afterwards suggest mode `server` once, which adds the rule (no polkit: `apt install polkitd` on
   the host first). An ssh error is not a refusal: fix access and rerun.
5. The restart locks the service: `S status` shows `owner: locked (log in on the page)`. Unlock it:
   - `DEPLOY_URL` unset: `S app-tunnel` with `run_in_background` (keep its task id; the page is at
     <http://127.0.0.1:8768/>). `DEPLOY_URL` set: the user uses that URL, no tunnel.
   - Tell the user: "Log in on the page with your login and password. I won't see them. A pass in
     progress resumes then." End the turn after `S wait-setup` with `run_in_background` (it prints
     `owner: unlocked` once the login is done, 30 min at most).
   - When it prints `owner: unlocked`: stop the app tunnel task (TaskStop), then `S status`
     (`binary: matches`, `service: active`, `owner: unlocked`, `api stage: idle`), and `S verify-url`
     when `DEPLOY_URL` is set.

## Switching to self-hosted

An existing deploy on AnkiWeb, `DEPLOY_SYNC=self-hosted` in `.env`. The cards keep their state: the
first sync with the empty server uploads the whole collection (schedules, review history, FSRS
state, deck options, media).

1. Tell the user: "Sync every device with AnkiWeb now (computer, phones; the server's Anki syncs on
   every pass), then review nowhere until I say so. On your computer, export a backup: File, Export,
   Anki Collection Package, with media and scheduling." Wait for their yes.
2. `S status`: a pass in progress is asked about first (see Pass in progress). `S upload`, then
   `S up` with `run_in_background`: its `sync:` line says `self-hosted on 127.0.0.1:8766`.
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
   `S clip-clear`, stop the tunnel task (TaskStop), and `S decks` shows the same count as before.
7. Tell the user to switch the other devices: Log Out of AnkiWeb, set the custom sync server to
   <DEPLOY_SYNC_URL> (AnkiDroid 2.16 or newer: Settings, Sync, Custom sync server; AnkiMobile: the
   custom sync server in its sync settings), log in as `anki`, choose **Download**. Reviews can
   resume. AnkiWeb keeps its last copy until they delete the account there.

Back to AnkiWeb: `DEPLOY_SYNC=ankiweb`, `S up` (stops `anki-sync`, its volume stays), then every
device clears the self-hosted URL and logs in to AnkiWeb; the device with the latest reviews uploads,
the others download.

## Mode `check`, server part

`S status` and `S kit-diff`, read-only.

## Report

Each step as done, skipped (already there) or failed; what the user still has to do (the setup
page, the login after a restart); the URL.
