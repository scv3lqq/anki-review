# Server modes

The flow of deploy/README.md, driven from this machine. `S` below is
`.claude/skills/setup-anki-review/server.sh` (run `S` alone for its commands). It reads
`DEPLOY_SSH`, `DEPLOY_SSH_PORT`, `DEPLOY_DIR` and `DEPLOY_URL` from `.env`, runs ssh in batch mode
and keeps every secret out of argv and output. Out of scope: the reverse proxy and its access
control, which stay the user's (deploy/README.md).

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
7. Claude token. Skip when `S status` shows `token: present`, unless the last `install.sh` failed
   its smoke test with an auth error (401, invalid token): then rotate. Also skip when it shows
   `not installed yet` next to `service: active` (a deploy from an older kit, no `install-state`):
   the token is likely in the env file already, `install.sh` keeps it and, if there is none, stops
   with the upload command; only then come back here.
   - `S window claude-token claude setup-token` opens it in a new window. Tell the user: authorize
     in the browser, copy the `sk-ant-...` token the window prints, say done. Never read that
     window.
   - `S token`: checks the clipboard holds one token, uploads it to `DEPLOY_DIR/claude-token`
     (0600), prints only its size, clears the clipboard. Refused: ask them to copy it again.
8. `install.sh`. Skip when step 3 showed `service: active`, `token: present`, a `polkit rule:` that
   is installed (or skipped for missing or old polkit, which a rerun won't change) and `kit-diff`
   matching, no new token came in step 7, and `KNOWLEDGE_DECKS` did not change. Otherwise, on a rerun with an `api stage` other than `idle` or `written`, ask first (the
   restart resumes the pass). `S install` opens a new window with
   `ssh -t ... sudo env KNOWLEDGE_DECKS=... DIR/install.sh DIR` (absolute paths, quoted for the
   host) and prints an exit file. Tell the user to type their sudo password there, run
   `S wait-install <file>` with `run_in_background`, end the turn. Exit code not 0: inside tmux
   read `tmux capture-pane -p -J -S -200 -t anki-review-install` (install.sh prints no secrets),
   else ask for the last lines; name the failing step, fix it, rerun after a yes.
9. `S status`: expect `service: active`, `api stage: idle`, `decks with cards due:` (any number),
   `binary: matches`, `token: present`, `polkit rule: installed for <user>` (skipped: updates will
   need sudo; tell the user why). `DEPLOY_URL` set: `S verify-url` (a 401, 403 or a login page is
   the proxy's access control: fine, the user checks in a browser). Unset: the service listens on
   the host's `127.0.0.1:8767`; `ssh -N -L 8767:127.0.0.1:8767 <host>` reaches it from here.

## Mode `server-update`

No human step when the host has the polkit rule (install.sh from this kit, polkit 0.106+).

1. `make check` (or `go test ./...`), `make build-linux`, `S precheck update` (no service from this
   `DEPLOY_DIR`: mode `server`).
2. `S kit-diff`: only `anki/` files differ: `S upload` and `S up` (no sudo). `install.sh`,
   `smoke.sh` or the unit differ: mode `server` (one sudo password).
3. `S status`: an `api stage` other than `idle` or `written` is a pass in progress: ask first (it
   resumes after the restart).
4. `S upload-bin`, then `S restart`. Refused (an older deploy without the rule, or no polkit): `S
   sudo-restart` opens a window for the sudo password, `S wait-install <file>` in the background;
   afterwards suggest mode `server` once, which adds the rule (no polkit: `apt install polkitd` on
   the host first). An ssh error is not a refusal: fix access and rerun.
5. `S status` (`binary: matches`, `service: active`, `api stage: idle`), and `S verify-url` when
   `DEPLOY_URL` is set.

## Mode `check`, server part

`S status` and `S kit-diff`, read-only.

## Report

Each step as done, skipped (already there) or failed; what the user still has to do; the URL.
