---
name: setup-anki-review
description: Set up anki-review from a fresh clone, locally (macOS, Linux) or on a Linux server over ssh with headless Anki, and update that server. Fixes prerequisites, writes .env, builds, starts, verifies. Use for "set up anki-review", "deploy to my server", "update the server", "check my setup", "turn auto-deploy / CD on or off", "why didn't main deploy".
---

# Set up anki-review

Choose the mode from the user's request. Codex runs this workflow; Claude Code remains the
application's grading runtime, with its existing commands, credentials and `CLAUDE_*` settings.

- `local` (also no argument): this machine, steps 1 to 6 below.
- `server`, `server-update`: a Linux server, [references/server.md](references/server.md); also
  turning auto-deploy of `main` on or off (`DEPLOY_AUTO`).
- `check`: step 1, plus `server.sh status` and `server.sh kit-diff` when `DEPLOY_SSH` is set;
  then the report. Diagnostics and the announced grading smoke test only: never create `.env`,
  install prerequisites, repair settings, build, or start the app. Missing files or prerequisites
  are reported with their fixes. This rule takes precedence over all repair instructions below.

README.md and deploy/README.md own the facts, `internal/config` and the deploy scripts win where
they disagree; this skill only orders them. Run every command from the repository root.

Rules for every mode:

- Codex performs authorized setup steps itself. The user types the AnkiWeb password or pastes the self-hosted sync
  password (server), the sudo password (server, once) and the setup form on the page: owner login,
  owner password, Claude token, AnkiConnect key. Automate the non-secret parts of installation and
  login when tools permit; leave secret entry to the user. Do not ask again for actions already
  authorized by the setup request. Ask only for missing choices or new consequential actions.
- Follow the active session's permissions and available tools. This skill grants no tool access
  or pre-approval. Use an available question tool, or one concise question, for required input.
- Codex never sees the owner login, the owner password or either token. Before the user fills the
  page, Codex explains in chat how to get each token (the user runs the commands on their machine):
  - Claude token: on a computer with Claude Code logged in to the account, run `claude setup-token`
    in a terminal, approve it in the browser, paste the `sk-ant-oat01-...` token that the terminal
    prints. An Anthropic API key (`sk-ant-api03-...`) works too. Empty only when this server runs on
    the user's own computer, as the account that already has `claude` working.
  - AnkiConnect key: in Anki, Tools > Add-ons, select AnkiConnect, Config, copy `apiKey`; `null`
    means empty. On a server, the key is `server.sh anki-key` (clipboard, never shown).
- Secrets (AnkiConnect key, VNC password, sync server password, Claude token, owner password) never
  appear in argv, in output or in this conversation: they move through pipes, files or the page
  inside the helper scripts. Don't `cat` them.
  Use `server.sh` for ssh, scp, clipboard and AppleScript operations. Any separately documented
  access-repair command still follows the active session's permission policy; never infer approval
  from a tool name or assume the runtime will display a permission prompt.
- Setup never writes to the user's Anki: no `POST` to the API (`/api/confirm` above all), never the
  page's button that writes grades, AnkiConnect only through the scripts here (`version`,
  `deckNames`). Unlocking a server on the page (setup or login) can resume a saved pass: step 4.
- Settings live in `.env` (gitignored; `make run` reads it, the scripts here too, and it wins over
  the environment). Outside `check` mode, when it is missing: `git check-ignore -q .env` must succeed, then
  `cp .env.example .env`. Missing values: ask for all of them in one message, then set the lines
  with the available file-editing tool. Values can't hold `#` or `$` (make syntax). The AnkiConnect key and the Claude token
  never go into `.env`: the setup page takes them. Never commit `.env`; never put a secret into a
  tracked file.

Long-running commands use native process sessions: start them with `exec_command`, keep the returned
`session_id`, and poll with `write_stdin` at short intervals while reporting progress. Use `tty: true`
when the process must later be interrupted, then send Ctrl-C through `write_stdin` only to the
session this workflow created. Track servers and tunnels separately; never stop an unrelated process.
Do not end the turn merely because a process is pending. Continue independent work while waiting for
required human input. If no asynchronous question tool is available, ending a turn to obtain that
input is allowed: retain the owned session IDs, or close and later reopen the workflow's own tunnel.
If native process sessions are unavailable, explain that limitation instead of assuming a background
task API exists.

## 1. Prerequisites (read-only)

```sh
.agents/skills/setup-anki-review/check.sh
```

One line per item (`ok`, `info`, `warn`, `FAIL`), exit 1 on any `FAIL`. It validates the settings
by the server's rules, uses no Claude quota, does not contact a running anki-review, and prints
only the stage of a saved pass. When Claude Code is available, run the Claude smoke test, the proof
that grading can run; first say it uses a little of the user's Claude quota:

```sh
claude -p --safe-mode --tools "" --no-session-persistence "Reply with OK"
```

Expected `OK` (use the configured `CLAUDE_BIN` when set, and add `--model "$CLAUDE_MODEL"` when set).
If the CLI is missing, report the smoke test as not run. In `check` mode report every `FAIL` and
`warn`, run only the server diagnostics named above when configured, then go directly to step 6.
In setup modes, fix every `FAIL` and `warn`:

| Item | Fix |
| --- | --- |
| config | Correct the named value in `.env` |
| go | macOS with Homebrew: `brew install go` when setup authorizes it; otherwise request the missing authorization. Otherwise <https://go.dev/doc/install>. Go 1.21+ fetches the go.mod toolchain itself unless `GOTOOLCHAIN=local` |
| anki | Install using an available supported path: <https://apps.ankiweb.net/> (or `brew install --cask anki`, Flatpak `net.ankiweb.Anki`); ask the user only for steps the tools cannot perform. AnkiConnect needs Anki 26.08 or later |
| ankiconn, add-on not found | Anki `ok`: `.agents/skills/setup-anki-review/install-ankiconnect.sh` when setup authorizes it (the release pinned in `deploy/anki/Dockerfile`, sha256 checked, into `addons21/2055492159`); arrange an authorized Anki restart and rerun `check.sh`. Manual fallback: Tools, Add-ons, Get Add-ons, `2055492159` |
| ankiconn, add-on found | Anki is closed or not restarted since the install: the user starts it. Still failing: `webBindPort` in the add-on config must match `ANKI_CONNECT_URL` |
| env | `ANKI_CONNECT_KEY` is set in `.env` or the environment: it is ignored now. Remove the line; the key goes into the setup page |
| ankiconn, wants apiKey | AnkiConnect needs its key: the user copies `apiKey` from Anki (Tools, Add-ons, AnkiConnect, Config), or on a server `server.sh anki-key`; the key goes into the setup page (step 4) |
| owner | `no owner yet` is normal before setup; the page opens with the setup form |
| claude | Not logged in or smoke test fails: guide the user through `claude` login without reading credentials; usage limit: wait for the reset |
| lint | Optional: `brew install golangci-lint` (v2); without it step 2 runs `go test ./...` |
| decks | Step 3 |
| port, data | Step 4 |

`config` or `go` FAIL stops before step 2; `ankiconn` FAIL stops before step 3; a `claude` FAIL
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
.agents/skills/setup-anki-review/check.sh decks
```

Prints each top-level deck with its kind now and `missing` for a configured name with no deck. When
AnkiConnect wants its apiKey, it prints one `info` line instead: the deck list needs the key, so name
the knowledge decks by hand, as they show in Anki's deck list.

Ask only on a first setup (the `.env` was just created) or when the output has a `missing` line. Then
explain the two kinds in one line each, ask which decks are knowledge decks in one grouped question
(use multiple selection if the available tool supports it, otherwise ask for the deck names), then set
`KNOWLEDGE_DECKS=<names, comma-separated, as Anki shows them>` in `.env` (create it as in the
rules). A name with a comma can't be listed; every deck as language: `KNOWLEDGE_DECKS=,`. Other
values only when they apply: `ANKI_SYNC=false` (Anki logged in to no sync server: neither AnkiWeb nor a
self-hosted one), `CLAUDE_MODEL`, `PORT`. Locally, Anki syncs with whatever server its Preferences
name (AnkiWeb or a self-hosted one); anki-review only presses Sync, so nothing here changes.

## 4. Run, set up and verify

A `data` warn first: a saved pass with confirmed grades goes to Anki, one waiting for grading goes
to `claude -p`, once the owner unlocks the server on the page (setup or login). The server starts
locked, so starting it alone does nothing. Tell the user; ask whether to unlock anyway or move
`data/state.json` aside. The risky moment is pressing **Set up** or **Log in**.

By the step 1 `port` line:

- free: `make run` in a tracked native process session as described above (it reads `.env`);
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
(the rules above): owner login, password of at least 12 characters, Claude token, AnkiConnect key.
The user fills it in and presses **Set up**; the server checks both tokens, which takes up to a minute.
Then rerun the same GET without the retry flags: `"unlocked":true` (`"state":"login"` is normal for
a request without the cookie). `"stage"` appears with it: `idle` (another stage: a saved pass
resumed). A failed check comes back on the page under the field: wrong key, Anki not running, or the
Claude token refused; fix that field.
Server gone: its output names the cause. This server lives as long as the session (lasting: `make
run` in a terminal, or step 5).

## 5. Autostart (only when the user asks)

Needs `bin/anki-review` and a free port (rerun `check.sh`; stop the step 4 server first). The file
names `claude` by absolute path (`command -v claude`; add node's directory to `PATH` if claude came
from npm), and takes `KNOWLEDGE_DECKS` and the other set values from `.env`. The file holds no
secret: the tokens go into the setup page. Show it and apply the requested autostart configuration;
ask only if an existing unrelated configuration would be replaced.

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

Each check as ok, warn or failed (only checks that ran); what Codex installed or wrote (`.env`
lines, add-on, autostart file); the URL and how the server runs (this session, autostart, already
running, not running); the owner (set up or not, unlocked or locked); what is left to the user, with
the fix from the table.
