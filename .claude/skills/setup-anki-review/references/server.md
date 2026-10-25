# Server modes

The flow of deploy/README.md, driven from this machine. `S` below is
`.claude/skills/setup-anki-review/server.sh` (run `S` alone for its commands). It reads
`DEPLOY_SSH`, `DEPLOY_SSH_PORT`, `DEPLOY_DIR`, `DEPLOY_PORT` (the service's PORT on the host, default
8767) and `DEPLOY_URL` from `.env`, runs ssh in batch mode and keeps every secret out of argv and
output. Out of scope: the reverse proxy and its access control, which stay the user's
(deploy/README.md).

## Settings and access

- Missing `DEPLOY_SSH` (`user@host` of the deploy user: docker group, sudo) or `DEPLOY_SSH_PORT`
  (default 22): ask once, together with `DEPLOY_URL` (optional: the public URL behind the user's
  proxy), and write them into `.env` (rules in SKILL.md). After the first `S precheck`, write the
  absolute dir its `dir` line names into `DEPLOY_DIR`.
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
   Its output ends with `decks: N`, plus a hint line when N is 0 or 1.
5. AnkiWeb login, only when N is 0 or 1 (an empty profile):
   - `S tunnel` with `run_in_background` (keep its task id), `S vnc-password` (into the clipboard,
     never shown), `open vnc://127.0.0.1:5901` (Linux: a VNC viewer on `127.0.0.1:5901`).
   - Tell the user: "Paste the password (Cmd+V) into Screen Sharing. In Anki click Sync, log in to
     AnkiWeb, and when it asks, choose **Download from AnkiWeb**. Never Upload: that would replace
     your AnkiWeb collection with this empty one. I watch for the decks."
   - `S wait-decks N` with `run_in_background`, end the turn. When it prints the new count:
     `S clip-clear`, stop the tunnel task (TaskStop), tell the user the media keeps syncing on the
     server and the Screen Sharing window can close. Timeout: ask what Anki shows.
6. Knowledge decks: `S deck-names` lists the server collection's top-level decks with the kind
   `KNOWLEDGE_DECKS` from `.env` gives them. First deploy, or a `missing` line: ask as in SKILL.md
   step 3 and set `KNOWLEDGE_DECKS` in `.env`. Note whether it changed in this run.
7. `install.sh`. Skip when step 3 showed `service: active`, a `polkit rule:` that is installed (or
   skipped for missing or old polkit, which a rerun won't change), `kit-diff` matching, and
   `KNOWLEDGE_DECKS` unchanged. Otherwise, with a pass in progress (see Pass in progress), ask first.
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
   user why). `DEPLOY_URL` set: `S verify-url` (a 401, 403 or a login page is the proxy's access
   control: fine, the user checks in a browser). Unset: the service listens on the host's
   `127.0.0.1:<DEPLOY_PORT>` (8767 by default); the app tunnel of step 8 reaches it from here.

## Mode `server-update`

No human step when the host has the polkit rule (install.sh from this kit, polkit 0.106+).

1. `make check` (or `go test ./...`), `make build-linux`, `S precheck update` (no service from this
   `DEPLOY_DIR`: mode `server`).
2. `S kit-diff`: only `anki/` files differ: `S upload` and `S up` (no sudo). `install.sh`,
   `smoke.sh` or the unit differ: mode `server` (one sudo password).
3. `S status`: a pass in progress (see Pass in progress) is asked about first.
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

## Mode `check`, server part

`S status` and `S kit-diff`, read-only.

## Report

Each step as done, skipped (already there) or failed; what the user still has to do (the setup
page, the login after a restart); the URL.
