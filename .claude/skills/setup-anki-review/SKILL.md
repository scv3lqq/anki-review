---
name: setup-anki-review
description: Set up, check or run anki-review locally (macOS, Linux) from a fresh clone. Checks Go, Anki with AnkiConnect and Claude Code, tests, builds, picks knowledge decks, starts and verifies the server; autostart on request. Use for "set up anki-review", "install it", "run it locally", "check my setup".
argument-hint: "[check|autostart]"
allowed-tools: Read, Bash(.claude/skills/setup-anki-review/check.sh:*), Bash(go:*), Bash(make:*), Bash(curl -sS --fail-with-body:*), Bash(claude -p --safe-mode:*), Bash(git check-ignore:*), Bash(plutil -lint:*)
---

# Set up anki-review

Argument: $ARGUMENTS

No argument: steps 1 to 4, then offer step 5. `check`: step 1, then the report; nothing
changes. `autostart`: steps 1 and 3, then step 5 (no `bin/anki-review` yet: step 2 first).
README.md owns the facts (requirements, variables), `internal/config` wins where they
disagree; this skill only orders them. Run every command from the repository root.

Rules for every step:

- Nothing gets installed, logged in, started, stopped or written outside the checkout without
  the user's yes. GUI apps, the Anki add-on and logins are always the user's to do.
- Setup never writes to the user's Anki: no `POST` to the server's API (`/api/confirm`
  above all), never the page's button that writes grades, no AnkiConnect action besides the
  read-only ones `check.sh` sends (`version`, `deckNames`). Starting the server can write a
  saved pass on its own: see step 4.
- No secret (`ANKI_CONNECT_KEY`) in a tracked file. Before creating any file inside the
  checkout, `git check-ignore -q <path>` must succeed.

Out of scope: deployment to a server or VPS (headless Anki, reverse proxy).

## 1. Prerequisites (read-only)

```sh
.claude/skills/setup-anki-review/check.sh
```

One line per item (`ok`, `info`, `warn`, `FAIL`), exit 1 on any `FAIL`. It reads the same
environment variables as the server and validates them by the same rules, uses no Claude
quota, does not contact a running anki-review, and prints only the stage of a saved pass.

Then the Claude smoke test, the only real proof that grading can run. Tell the user it uses
a little of their Claude quota, then:

```sh
claude -p --safe-mode --tools "" --no-session-persistence "Reply with OK"
```

Expected: `OK` (add `--model "$CLAUDE_MODEL"` when it is set). An auth or usage-limit error
here is what grading would hit.

For each `FAIL` or `warn`, give the fix from this table; offer to run a `brew install` for a
CLI tool, only after a yes.

| Item | Fix |
| --- | --- |
| config | Fix the named variable: the server exits on it at start |
| go | macOS `brew install go`; Linux the tarball from <https://go.dev/doc/install> (distro packages lag). Go 1.21+ fetches the go.mod toolchain itself unless `GOTOOLCHAIN=local` |
| anki | <https://apps.ankiweb.net/> (macOS also `brew install --cask anki`, Linux also Flatpak `net.ankiweb.Anki`). The current AnkiConnect needs Anki 26.08 or later |
| ankiconn unreachable | Start Anki and open the profile to review. Add-on not found: Tools, Add-ons, Get Add-ons, code `2055492159`, then restart Anki. Found: restart Anki, and check that `webBindPort` (Tools, Add-ons, AnkiConnect, Config) matches `ANKI_CONNECT_URL` |
| ankiconn key | `ANKI_CONNECT_KEY` must equal the add-on's `apiKey`, unset when the add-on has none |
| claude missing | `curl -fsSL https://claude.ai/install.sh \| bash` or `brew install --cask claude-code` (<https://code.claude.com/docs/en/setup>) |
| claude login, smoke test | The user runs `claude` in a terminal and follows the browser login (or `claude auth login`); `ANTHROPIC_API_KEY` works too. Usage limit: wait for the reset |
| lint | Optional: `brew install golangci-lint` (v2). Without it step 2 runs `go test ./...` |
| decks | Step 3 |
| port, data | Step 4 |

`config` or `go` FAIL: stop before step 2. `ankiconn` FAIL: step 2 can run, steps 3 and 4
wait until the user fixes it and `check.sh` passes. `claude` FAIL: go on, but grading fails
until it is fixed. Mode `check`: go to step 6.

## 2. Test and build

```sh
make check      # when `lint` is ok (its -race needs a C compiler); otherwise: go test ./...
make build      # bin/anki-review: static, the page embedded
```

When the `port` line names this checkout's `bin/anki-review` as already serving: skip
`make build` if `build` says up to date; otherwise ask first, since it replaces the binary
under the running server (which keeps the old code until restarted).

Tests fake Anki and Claude, so they need neither. A failing test stops the run: show it,
don't build around it.

## 3. Configure

Defaults fit a stock Anki with AnkiConnect on 8765 (README `## Configuration` lists every
variable). The one real choice is `KNOWLEDGE_DECKS`: top-level decks graded by meaning
(facts, concepts); every other deck is graded as language cards, where the exact word, its
form and spelling count. Default `Tech,Career`.

```sh
.claude/skills/setup-anki-review/check.sh decks
```

Prints each top-level deck with the kind it gets now (`knowledge`, `language`) and
`missing` for a `KNOWLEDGE_DECKS` name with no such deck. Explain the two kinds in one line
each, then ask which decks are knowledge decks (one AskUserQuestion, multiSelect; with more
than 4 decks offer the likeliest 4, the user types the rest in Other).

- Same set as the default: nothing to set.
- Otherwise: the server reads only its environment (it loads no `.env` file), so give the
  exact line, names as Anki shows them, case-sensitive, comma-separated, single-quoted
  (a `'` inside a name as `'\''`): `KNOWLEDGE_DECKS='Tech,Career,Books' make run`. A deck
  name with a comma can't be listed. An empty value means the default; to grade every deck
  as language: `KNOWLEDGE_DECKS=,`.
- To keep it: the user adds `export KNOWLEDGE_DECKS='...'` to their shell profile, or it goes
  into the autostart file of step 5. Don't edit the profile yourself.

Other variables only when they apply: `ANKI_CONNECT_KEY` (step 1 key error), `ANKI_SYNC=false`
(no AnkiWeb account: the syncs before a pass and after writing would only fail),
`CLAUDE_MODEL` (for example `sonnet`), `PORT` (step 4).

## 4. Run and verify

A `data` warn comes first: a saved pass with confirmed grades goes to Anki, and one waiting
for grading goes to `claude -p`, as soon as the server starts. Tell the user and ask: start
anyway (they are their own grades), or move `data/state.json` aside first.

Then by the step 1 `port` line:

- free: start `bin/anki-review` with Bash `run_in_background`, the env line of step 3 in
  front;
- an anki-review already serving (this checkout's binary, a `make run` or another
  checkout): start nothing, verify that one. Ask before restarting it when step 2 rebuilt
  the binary or its `kind`s in `/api/decks` differ from the step 3 choice; a server the user
  started in a terminal is theirs to restart;
- another program: ask for a free port, start with `PORT=<port>` and use it below.

```sh
curl -sS --fail-with-body --retry 10 --retry-connrefused --retry-delay 1 http://127.0.0.1:8767/api/cards
curl -sS --fail-with-body http://127.0.0.1:8767/api/decks
```

Expected: `/api/cards` shows `"stage":"idle"` (another stage: the saved pass was resumed);
`/api/decks` is `{"decks":[...]}`, the decks with cards due today and their `kind` (empty:
nothing due). HTTP 502 means AnkiConnect failed: the body says why. If the server exited,
its output names the cause (port in use, a bad variable).

These two GETs are the whole check: nothing that starts a pass or writes grades.

Give the URL <http://127.0.0.1:8767/>. A server started here lives only as long as this
session; for a lasting one, `make run` in a terminal, or step 5.

## 5. Autostart (only when the user asks)

Needs `bin/anki-review`, and the port free: re-run `check.sh` first. A server this session
started: stop it. Any other one: ask the user to stop it, or put another `PORT` in the file;
otherwise the service can't bind and keeps restarting.

launchd and systemd don't use the shell's `PATH`, so the file names `claude` by its absolute
path (`command -v claude`), plus node's directory in `PATH` if claude came from npm. Fill the
placeholders: `@REPO@` the checkout, `@CLAUDE_BIN@` and `@CLAUDE_DIR@` the claude binary and
its directory, `@HOME@` the home directory (absolute paths: neither file expands `~`), `@DECKS@`
the step 3 value. Add `ANKI_CONNECT_KEY` and other step 3 variables the same way. Show the
file, write it after a yes; mode `0600` when it holds the key. Anki still has to run for
reviews; the server starting before it is fine.

macOS, `~/Library/LaunchAgents/local.anki-review.plist` (in values write `&` as `&amp;` and
`<` as `&lt;`):

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

```sh
plutil -lint ~/Library/LaunchAgents/local.anki-review.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/local.anki-review.plist
launchctl print gui/$(id -u)/local.anki-review | awk '/^\t(state|pid|last exit code) = /'
```

Restart after `make build`: `launchctl kickstart -k gui/$(id -u)/local.anki-review`.
Remove: `launchctl bootout gui/$(id -u)/local.anki-review`, then delete the plist.

Linux, `~/.config/systemd/user/anki-review.service` (each `Environment=` quoted whole, so a
space in a deck name survives; write `%` as `%%`):

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

```sh
systemd-analyze --user verify ~/.config/systemd/user/anki-review.service
systemctl --user daemon-reload && systemctl --user enable --now anki-review
systemctl --user show -p ActiveState -p MainPID anki-review
```

Logs: `journalctl --user -u anki-review`. Restart after `make build`:
`systemctl --user restart anki-review`. Remove: `systemctl --user disable --now anki-review`,
delete the unit, `systemctl --user daemon-reload`.

Then verify: `check.sh` shows this checkout's `bin/anki-review` serving with the same pid as
`launchctl print` or `MainPID`, and the two `curl` checks of step 4 pass.

## 6. Report

Each `check.sh` item and the smoke test as ok, warn or failed (only checks that ran); the
test and build result; the env line from step 3, if any; the URL and how the server runs
(started by this session, autostart, already running before, or not running); what is left
to the user (installs, add-on, login), with the fix from the table.
