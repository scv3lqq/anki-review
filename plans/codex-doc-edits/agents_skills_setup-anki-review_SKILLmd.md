# Mechanical edits: .agents/skills/setup-anki-review/SKILL.md

Base: 3422c81bf8c0a9a57d99dd3a2f43bb0c24a862bc. Generated with the canonical plan_edits helper.

**E1.** (lines 5-18)

````md E1-old

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

````

````md E1-new

# Set up anki-review

Choose the mode from the user's request. The operating agent does not determine the grading
provider. The owner selects Claude Code (default) or Codex CLI 0.166.0 on the page; preserve
existing Claude commands and credentials. `GRADER_PROVIDER` is only the initial setup default.

- `local` (also no argument): this machine, steps 1 to 6 below.
- `server`, `server-update`: a Linux server, [references/server.md](references/server.md); also
  turning auto-deploy of `main` on or off (`DEPLOY_AUTO`).
- `check`: step 1, plus `server.sh status` and `server.sh kit-diff` when `DEPLOY_SSH` is set;
  then the report. Diagnostics only, without grading quota: never create `.env`,
  install prerequisites, repair settings, build, or start the app. Missing files or prerequisites
  are reported with their fixes. This rule takes precedence over all repair instructions below.

````

**E2.** (lines 23-42)

````md E2-old

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
````

````md E2-new

- Codex performs authorized setup steps itself. The user types the AnkiWeb password or pastes the self-hosted sync
  password (server), the sudo password (server, once) and the setup form on the page: owner login,
  owner password, selected provider credentials, AnkiConnect key. Automate the non-secret parts of installation and
  login when tools permit; leave secret entry to the user. Do not ask again for actions already
  authorized by the setup request. Ask only for missing choices or new consequential actions.
- Follow the active session's permissions and available tools. This skill grants no tool access
  or pre-approval. Use an available question tool, or one concise question, for required input.
- Codex never sees the owner login, the owner password or grading credentials. Before the user fills the
  page, Codex explains in chat how to get each token (the user runs the commands on their machine):
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
  Use `server.sh` for ssh, scp, clipboard and AppleScript operations. Any separately documented
````

**E3.** (lines 48-54)

````md E3-old
- Settings live in `.env` (gitignored; `make run` reads it, the scripts here too, and it wins over
  the environment). Outside `check` mode, when it is missing: `git check-ignore -q .env` must succeed, then
  `cp .env.example .env`. Missing values: ask for all of them in one message, then set the lines
  with the available file-editing tool. Values can't hold `#` or `$` (make syntax). The AnkiConnect key and the Claude token
  never go into `.env`: the setup page takes them. Never commit `.env`; never put a secret into a
  tracked file.

````

````md E3-new
- Settings live in `.env` (gitignored; `make run` reads it, the scripts here too, and it wins over
  the environment). Outside `check` mode, when it is missing: `git check-ignore -q .env` must succeed, then
  `cp .env.example .env`. Missing values: ask for all of them in one message, then set the lines
  with the available file-editing tool. Values can't hold `#` or `$` (make syntax). The AnkiConnect key, Claude token and Codex auth.json
  never go into `.env`: the setup page takes them. Never commit `.env`; never put a secret into a
  tracked file.

````

**E4.** (lines 70-82)

````md E4-old

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
````

````md E4-new

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
Expected `OK` (use the configured `CLAUDE_BIN` when set, and add `--model "$CLAUDE_MODEL"` when set).
If the CLI is missing, report the smoke test as not run. In `check` mode report every `FAIL` and
`warn`, run only the server diagnostics named above when configured, then go directly to step 6.
````

**E5.** (lines 92-103)

````md E5-old
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
````

````md E5-new
| env | `ANKI_CONNECT_KEY` is set in `.env` or the environment: it is ignored now. Remove the line; the key goes into the setup page |
| ankiconn, wants apiKey | AnkiConnect needs its key: the user copies `apiKey` from Anki (Tools, Add-ons, AnkiConnect, Config), or on a server `server.sh anki-key`; the key goes into the setup page (step 4) |
| owner | `no owner yet` is normal before setup; the page opens with the setup form |
| codex | Install Codex CLI 0.166.0; use a dedicated device login and import auth.json on the page; revoked refresh requires reauthentication |
| claude | Not logged in or smoke test fails: guide the user through `claude` login without reading credentials; usage limit: wait for the reset |
| lint | Optional: `brew install golangci-lint` (v2); without it step 2 runs `go test ./...` |
| decks | Step 3 |
| port, data | Step 4 |

`config` or `go` FAIL stops before step 2; `ankiconn` FAIL stops before step 3; a grading CLI FAIL
lets setup go on, grading fails until it is fixed. Mode `check`: the report (step 6).

## 2. Test and build
````

**E6.** (lines 130-142)

````md E6-old
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

````

````md E6-new
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

````

**E7.** (lines 155-174)

````md E7-old
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

````

````md E7-new
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
secret: the tokens go into the setup page. Show it and apply the requested autostart configuration;
ask only if an existing unrelated configuration would be replaced.

````

**E8.** (lines 186-191)

````md E8-old
  <key>EnvironmentVariables</key>
  <dict>
    <key>CLAUDE_BIN</key><string>@CLAUDE_BIN@</string>
    <key>PATH</key><string>@CLAUDE_DIR@:/usr/bin:/bin</string>
    <key>KNOWLEDGE_DECKS</key><string>@DECKS@</string>
  </dict>
````

````md E8-new
  <key>EnvironmentVariables</key>
  <dict>
    <key>CLAUDE_BIN</key><string>@CLAUDE_BIN@</string>
    <key>CODEX_BIN</key><string>@CODEX_BIN@</string>
    <key>GRADER_PROVIDER</key><string>@GRADER_PROVIDER@</string>
    <key>PATH</key><string>@CLAUDE_DIR@:/usr/bin:/bin</string>
    <key>KNOWLEDGE_DECKS</key><string>@DECKS@</string>
  </dict>
````

**E9.** (lines 211-216)

````md E9-old
WorkingDirectory=@REPO@
ExecStart=@REPO@/bin/anki-review
Environment="CLAUDE_BIN=@CLAUDE_BIN@"
Environment="PATH=@CLAUDE_DIR@:/usr/local/bin:/usr/bin:/bin"
Environment="KNOWLEDGE_DECKS=@DECKS@"
Restart=on-failure
````

````md E9-new
WorkingDirectory=@REPO@
ExecStart=@REPO@/bin/anki-review
Environment="CLAUDE_BIN=@CLAUDE_BIN@"
Environment="CODEX_BIN=@CODEX_BIN@"
Environment="GRADER_PROVIDER=@GRADER_PROVIDER@"
Environment="PATH=@CLAUDE_DIR@:/usr/local/bin:/usr/bin:/bin"
Environment="KNOWLEDGE_DECKS=@DECKS@"
Restart=on-failure
````
