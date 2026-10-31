# Mechanical edits: .claude/skills/setup-anki-review/references/server.md

Base: 3422c81bf8c0a9a57d99dd3a2f43bb0c24a862bc. Generated with the canonical plan_edits helper.

**E1.** (lines 6-11)

````md E1-old
8767), `DEPLOY_URL`, `DEPLOY_SYNC` and `DEPLOY_SYNC_URL` from `.env`, runs ssh in batch mode and
keeps every secret out of argv and output. Out of scope: the reverse proxy and its access control,
which stay the user's (deploy/README.md).

## Settings and access

````

````md E1-new
8767), `DEPLOY_URL`, `DEPLOY_SYNC` and `DEPLOY_SYNC_URL` from `.env`, runs ssh in batch mode and
keeps every secret out of argv and output. Out of scope: the reverse proxy and its access control,
which stay the user's (deploy/README.md).

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
A restart leaves the app locked, and unlocking can resume a saved pass as described below.

## Settings and access

````

**E2.** (lines 95-101)

````md E2-old
   - No token is involved: install.sh takes none. An older deploy that kept the Claude token and the
     AnkiConnect key in `/etc/anki-review/env` gets them removed by this run, and says so in one
     line.
8. Setup page (the owner, the Claude token and the AnkiConnect key are entered here, not in the
   shell). Skip when the latest `S status` shows `owner: unlocked`: run it after step 7, since a run
   that installed restarted the service and the step 3 result is stale (when step 7 was skipped,
   step 3 holds). An owner already set up gets the login form instead: the user logs in, and the
````

````md E2-new
   - No token is involved: install.sh takes none. An older deploy that kept the Claude token and the
     AnkiConnect key in `/etc/anki-review/env` gets them removed by this run, and says so in one
     line.
8. Setup page (the owner, selected grading credentials and the AnkiConnect key are entered here, not in the
   shell). Skip when the latest `S status` shows `owner: unlocked`: run it after step 7, since a run
   that installed restarted the service and the step 3 result is stale (when step 7 was skipped,
   step 3 holds). An owner already set up gets the login form instead: the user logs in, and the
````

**E3.** (lines 106-112)

````md E3-old
     `run_in_background` (keep its task id; it forwards the host's service to
     <http://127.0.0.1:8768/>), and the user opens that URL.
   - Tell the user: "Fill in a login and a password of at least 12 characters (twice). Paste the
     AnkiConnect key (Cmd+V). For the Claude token, run `claude setup-token` in a terminal on your
     computer, approve it in the browser and paste the `sk-ant-oat01-...` token it prints. Press
     Set up. I won't see any of it." Before that, explain the token source as in SKILL.md (the rules
     list how to get each one).
````

````md E3-new
     `run_in_background` (keep its task id; it forwards the host's service to
     <http://127.0.0.1:8768/>), and the user opens that URL.
   - Tell the user: "Fill in a login and a password of at least 12 characters (twice). Paste the
     AnkiConnect key (Cmd+V). Select your grading provider. For Codex, import the dedicated auth.json using the procedure above. For Claude, run `claude setup-token` in a terminal on your
     computer, approve it in the browser and paste the `sk-ant-oat01-...` token it prints. Press
     Set up. I won't see any of it." Before that, explain the token source as in SKILL.md (the rules
     list how to get each one).
````

**E4.** (lines 134-140)

````md E4-old
1. `make check` (or `go test ./...`), `make build-linux`, `S precheck update` (no service from this
   `DEPLOY_DIR`: mode `server`).
2. `S kit-diff`: only `anki/` files differ: `S upload` and `S up` (no sudo). `install.sh`,
   `smoke.sh` or the unit differ: mode `server` (one sudo password).
3. `S status`: a pass in progress (see Pass in progress) is asked about first. A `sync:` line that
   differs from `DEPLOY_SYNC` (`sync: AnkiWeb` with `DEPLOY_SYNC=self-hosted`): Switching to
   self-hosted, not a plain update.
````

````md E4-new
1. `make check` (or `go test ./...`), `make build-linux`, `S precheck update` (no service from this
   `DEPLOY_DIR`: mode `server`).
2. `S kit-diff`: only `anki/` files differ: `S upload` and `S up` (no sudo). `install.sh`,
   `smoke.sh`, `provider.sh`, `test-provider.sh` or the unit differ: mode `server` (one sudo password).
3. `S status`: a pass in progress (see Pass in progress) is asked about first. A `sync:` line that
   differs from `DEPLOY_SYNC` (`sync: AnkiWeb` with `DEPLOY_SYNC=self-hosted`): Switching to
   self-hosted, not a plain update.
````
