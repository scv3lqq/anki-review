---
name: setup-anki-review
description: Set up anki-review from a fresh clone, locally (macOS, Linux) or on a Linux server over ssh with headless Anki, and update that server. Fixes prerequisites, writes .env, builds, starts, verifies. Use for "set up anki-review", "deploy to my server", "update the server", "check my setup", "turn auto-deploy / CD on or off", "why didn't main deploy".
argument-hint: "[check|local|server|server-update]"
allowed-tools: Read, Edit, Write, Bash(.claude/skills/setup-anki-review/check.sh:*), Bash(.claude/skills/setup-anki-review/server.sh:*), Bash(.claude/skills/setup-anki-review/install-ankiconnect.sh:*), Bash(make:*), Bash(go:*), Bash(curl -sS --fail-with-body --retry 10 --retry-connrefused --retry-delay 1 http://127.0.0.1:*), Bash(curl -sS --fail-with-body http://127.0.0.1:*), Bash(open vnc://127.0.0.1:5901:*), Bash(tmux capture-pane:*), Bash(cp .env.example .env:*), Bash(git check-ignore:*), Bash(claude -p --safe-mode:*), Bash(brew install go:*), Bash(plutil -lint:*)
---

# Set up anki-review

The operating agent does not determine the grading provider. The owner selects Claude Code
(default) or Codex CLI 0.166.0 on the page. `GRADER_PROVIDER` sets only the initial setup default.

Argument: $ARGUMENTS

- `local` (also no argument): this machine, steps 1 to 6 below.
- `server`, `server-update`: a Linux server, [references/server.md](references/server.md); also
  turning auto-deploy of `main` on or off (`DEPLOY_AUTO`).
- `check`: step 1, plus `server.sh status` and `server.sh kit-diff` when `DEPLOY_SSH` is set;
  then the report. Nothing changes.

README.md and deploy/README.md own the facts, `internal/config` and the deploy scripts win where
they disagree; this skill only orders them. Run every command from the repository root.

Rules for every mode:

- Claude does every step itself. The user types the AnkiWeb password or pastes the self-hosted sync
  password (server), the sudo password (server, once) and the setup form on the page: owner login,
  owner password, selected provider credentials, AnkiConnect key. Logging in, installing a GUI app and anything with a
  password stay theirs; installs Claude runs (Go, AnkiConnect) take one yes first.
- Claude never sees the owner login, the owner password or grading credentials. Before the user fills the
  page, Claude explains in chat how to get each token (the user runs the commands on their machine):
  - Claude token: on a computer with Claude Code logged in to the account, run `claude setup-token`
    in a terminal, approve it in the browser, paste the `sk-ant-oat01-...` token that the terminal
    prints. An Anthropic API key (`sk-ant-api03-...`) works too. Empty only when this server runs on
    the user's own computer, as the account that already has `claude` working.
  - Codex: use a dedicated ChatGPT device session and import the entire `auth.json` via the
    setup/settings file picker. Follow [deploy/README.md](../../../deploy/README.md#codex-device-session)
    for the exact temporary-home login, secure SSH copy, successful-save check and cleanup.
    Never read the file or user code. Revoked refresh tokens require a new device login.
  - AnkiConnect key: in Anki, Tools > Add-ons, select AnkiConnect, Config, copy `apiKey`; `null`
    means empty. On a server, the key is `server.sh anki-key` (clipboard, never shown).
- Secrets (AnkiConnect key, VNC password, sync server password, Claude token, Codex auth.json, owner password) never
  appear in argv, in output or in this conversation: they move through pipes, files or the page
  inside the helper scripts. Don't `cat` them.
  ssh, scp, the clipboard and AppleScript run only inside `server.sh`, so no raw `ssh` or `curl`
  call is pre-approved: any other one gets a permission prompt.
- Setup never writes to the user's Anki: no `POST` to the API (`/api/confirm` above all), never the
  page's button that writes grades, AnkiConnect only through the scripts here (`version`,
  `deckNames`). Unlocking a server on the page (setup or login) can resume a saved pass: step 4.
- Settings live in `.env` (gitignored; `make run` reads it, the scripts here too, and it wins over
  the environment). Missing it: `git check-ignore -q .env` must succeed, then
  `cp .env.example .env`. Missing values: ask for all of them in one message, then set the lines
  with Edit. Values can't hold `#` or `$` (make syntax). The AnkiConnect key, Claude token and Codex auth.json
  never go into `.env`: the setup page takes them. Never commit `.env`; never put a secret into a
  tracked file.

## 1. Prerequisites (read-only)

```sh
.claude/skills/setup-anki-review/check.sh
```

One line per item (`ok`, `info`, `warn`, `FAIL`), exit 1 on any `FAIL`. It validates the settings
by the server's rules, uses no Claude quota, does not contact a running anki-review, and prints
only the stage of a saved pass. `GRADER_PROVIDER` is only the initial default; saved selection
is encrypted. Diagnostics check the configured initial provider, not the saved selection. Run the Claude smoke test only outside `check` mode and only when the owner has explicitly identified the current or intended provider as Claude Code. With an existing vault and an unknown saved selection, skip the quota smoke until the owner chooses a provider; never infer it from `GRADER_PROVIDER`. Announce the small Claude quota use before running:

```sh
claude -p --safe-mode --tools "" --no-session-persistence "Reply with OK"
```

For Codex, diagnostics run only the configured `CODEX_BIN --version` and require 0.166.0.
Never run the Claude quota probe when Codex is selected. The owner's setup/settings save checks
the imported session with one small Codex call; diagnostics do not read auth or send cards.
Expected `OK` from the Claude probe (use configured `CLAUDE_BIN` and add `--model "$CLAUDE_MODEL"`
when set). In `check` mode go directly to the report without repairs or grading calls.
In setup modes, fix every `FAIL` and `warn`:

| Item | Fix |
| --- | --- |
| config | Correct the named value in `.env` |
| go | macOS with Homebrew: one AskUserQuestion, then `brew install go`. Otherwise <https://go.dev/doc/install>. Go 1.21+ fetches the go.mod toolchain itself unless `GOTOOLCHAIN=local` |
| anki | The user installs it: <https://apps.ankiweb.net/> (or `brew install --cask anki`, Flatpak `net.ankiweb.Anki`). AnkiConnect needs Anki 26.08 or later |
| ankiconn, add-on not found | Anki `ok`: one AskUserQuestion, then `.claude/skills/setup-anki-review/install-ankiconnect.sh` (the release pinned in `deploy/anki/Dockerfile`, sha256 checked, into `addons21/2055492159`); the user restarts Anki; rerun `check.sh`. Or the user installs it: Tools, Add-ons, Get Add-ons, `2055492159` |
| ankiconn, add-on found | Anki is closed or not restarted since the install: the user starts it. Still failing: `webBindPort` in the add-on config must match `ANKI_CONNECT_URL` |
| env | `ANKI_CONNECT_KEY` is set in `.env` or the environment: it is ignored now. Remove the line; the key goes into the setup page |
| ankiconn, wants apiKey | AnkiConnect needs its key: the user copies `apiKey` from Anki (Tools, Add-ons, AnkiConnect, Config), or on a server `server.sh anki-key`; the key goes into the setup page (step 4) |
| owner | `no owner yet` is normal before setup; the page opens with the setup form |
| codex | Install Codex CLI 0.166.0; use a dedicated device login and import auth.json on the page; revoked refresh requires reauthentication |
| claude | Not logged in or smoke test fails: the user runs `claude` in a terminal and logs in; usage limit: wait for the reset |
| lint | Optional: `brew install golangci-lint` (v2); without it step 2 runs `go test ./...` |
| decks | Step 3 |
| port, data | Step 4 |

`config` or `go` FAIL stops before step 2; `ankiconn` FAIL stops before step 3; a grading CLI FAIL
lets setup go on, grading fails until it is fixed. Mode `check`: the report (step 6).

## 2. Test and build

```sh
make check      # when `lint` is ok (its -race needs a C compiler); otherwise: go test ./...
make build      # bin/anki-review: static, the page embedded (for autostart)
```

A failing test stops the run: show it, don't build around it. When this checkout's
`bin/anki-review` already serves the port, skip `make build` if `build` says up to date,
otherwise ask: it replaces the binary under the running server.

## 3. Configure

The one real choice is `KNOWLEDGE_DECKS`: top-level decks graded by meaning (facts, concepts);
every other deck is graded as language cards, where the exact word, form and spelling count.

```sh
.claude/skills/setup-anki-review/check.sh decks
```

Prints each top-level deck with its kind now and `missing` for a configured name with no deck. When
AnkiConnect wants its apiKey, it prints one `info` line instead: the deck list needs the key, so name
the knowledge decks by hand, as they show in Anki's deck list.

Ask only on a first setup (the `.env` was just created) or when the output has a `missing` line. Then
explain the two kinds in one line each, ask which decks are knowledge decks (one AskUserQuestion,
multiSelect; more than 4 decks: the likeliest 4, the rest via Other), then set
`KNOWLEDGE_DECKS=<names, comma-separated, as Anki shows them>` in `.env` (create it as in the
rules). A name with a comma can't be listed; every deck as language: `KNOWLEDGE_DECKS=,`. Other
values only when they apply: `ANKI_SYNC=false` (Anki logged in to no sync server: neither AnkiWeb nor a
self-hosted one), `CLAUDE_MODEL`, `CODEX_MODEL`, `GRADER_PROVIDER` (initial default only), `PORT`. Locally, Anki syncs with whatever server its Preferences
name (AnkiWeb or a self-hosted one); anki-review only presses Sync, so nothing here changes.

## 4. Run, set up and verify

A `data` warn first: a saved pass with confirmed grades goes to Anki, one waiting for grading goes
to the saved grading provider, once the owner unlocks the server on the page (setup or login). The server starts
locked, so starting it alone does nothing. Tell the user; ask whether to unlock anyway or move
`data/state.json` aside. The risky moment is pressing **Set up** or **Log in**.

By the step 1 `port` line:

- free: `make run` with Bash `run_in_background` (it reads `.env`);
- an anki-review already serving: start nothing, verify it. When `.env`'s `KNOWLEDGE_DECKS` changed
  since that server started, or step 2 rebuilt the binary, ask before restarting it (one the user
  started in a terminal is theirs to restart);
- another program: ask for a free `PORT`, set it in `.env`, use it below.

```sh
curl -sS --fail-with-body --retry 10 --retry-connrefused --retry-delay 1 http://127.0.0.1:8767/api/session
```

A fresh checkout answers `"state":"setup","unlocked":false`; a set-up owner answers
`"state":"login"` with `"unlocked":false` after a restart. The review routes need the login cookie,
so the skill checks only this GET. Give the URL <http://127.0.0.1:8767/> and explain the setup form
(the rules above): owner login, password of at least 12 characters, selected provider credentials, AnkiConnect key.
The user fills it in and presses **Set up**; the server checks AnkiConnect and the selected grader, which takes up to a minute.
Then rerun the same GET without the retry flags: `"unlocked":true` (`"state":"login"` is normal for
a request without the cookie). `"stage"` appears with it: `idle` (another stage: a saved pass
resumed). A failed check comes back on the page under the field: wrong key, Anki not running, or the
selected grading credential refused; fix that field or repeat Codex device login.
Server gone: its output names the cause. This server lives as long as the session (lasting: `make
run` in a terminal, or step 5).

## 5. Autostart (only when the user asks)

Needs `bin/anki-review` and a free port (rerun `check.sh`; stop the step 4 server first). The file
names each installed grading CLI by absolute path (`command -v claude`, `command -v codex`;
include each executable's directory in `PATH`), and takes `KNOWLEDGE_DECKS` and the other set values from `.env`. The file holds no
secret: the tokens go into the setup page. Show it, write it after a yes.

macOS, `~/Library/LaunchAgents/local.anki-review.plist` (absolute paths, `&` as `&amp;`, `<` as
`&lt;`):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>local.anki-review</string>
  <key>ProgramArguments</key><array><string>@REPO@/bin/anki-review</string></array>
  <key>WorkingDirectory</key><string>@REPO@</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>CLAUDE_BIN</key><string>@CLAUDE_BIN@</string>
    <key>CODEX_BIN</key><string>@CODEX_BIN@</string>
    <key>GRADER_PROVIDER</key><string>@GRADER_PROVIDER@</string>
    <key>PATH</key><string>@CLAUDE_DIR@:/usr/bin:/bin</string>
    <key>KNOWLEDGE_DECKS</key><string>@DECKS@</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>StandardErrorPath</key><string>@HOME@/Library/Logs/anki-review.log</string>
</dict>
</plist>
```

`plutil -lint` it, `launchctl bootstrap gui/$(id -u) <plist>`; state:
`launchctl print gui/$(id -u)/local.anki-review | awk '/^\t(state|pid|last exit code) = /'`.
Restart after a build: `launchctl kickstart -k gui/$(id -u)/local.anki-review`. Remove:
`launchctl bootout gui/$(id -u)/local.anki-review`, delete the plist.

Linux, `~/.config/systemd/user/anki-review.service` (each `Environment=` quoted whole, `%` as `%%`):

```ini
[Unit]
Description=anki-review

[Service]
WorkingDirectory=@REPO@
ExecStart=@REPO@/bin/anki-review
Environment="CLAUDE_BIN=@CLAUDE_BIN@"
Environment="CODEX_BIN=@CODEX_BIN@"
Environment="GRADER_PROVIDER=@GRADER_PROVIDER@"
Environment="PATH=@CLAUDE_DIR@:/usr/local/bin:/usr/bin:/bin"
Environment="KNOWLEDGE_DECKS=@DECKS@"
Restart=on-failure

[Install]
WantedBy=default.target
```

`systemd-analyze --user verify` it, `systemctl --user daemon-reload && systemctl --user enable --now
anki-review`; state: `systemctl --user show -p ActiveState -p MainPID anki-review`; logs: `journalctl
--user -u anki-review`. Restart: `systemctl --user restart anki-review`. Remove: `systemctl --user
disable --now anki-review`, delete the unit, `daemon-reload`.

Verify: `check.sh` shows this checkout's binary serving with the pid launchd or systemd reports,
and the `/api/session` check of step 4 passes. After any restart the server is locked until the user
logs in on the page: say so when you restart it.

## 6. Report

Each check as ok, warn or failed (only checks that ran); what Claude installed or wrote (`.env`
lines, add-on, autostart file); the URL and how the server runs (this session, autostart, already
running, not running); the owner (set up or not, unlocked or locked); what is left to the user, with
the fix from the table.
