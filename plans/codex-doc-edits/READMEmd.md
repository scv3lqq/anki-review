# Mechanical edits: README.md

Base: 3422c81bf8c0a9a57d99dd3a2f43bb0c24a862bc. Generated with the canonical plan_edits helper.

**E1.** (lines 1-6)

````md E1-old
# anki-review

Review your due Anki cards on a web page: type the answers, Claude Code grades them against each
card's answer key, you confirm or change every grade, and the grades go back into Anki through its own
scheduler, the same as pressing the buttons in the reviewer.

````

````md E1-new
# anki-review

Review your due Anki cards on a web page: type the answers, Claude Code or Codex grades them against each
card's answer key, you confirm or change every grade, and the grades go back into Anki through its own
scheduler, the same as pressing the buttons in the reviewer.

````

**E2.** (lines 29-42)

````md E2-old

## How it works

The first time, the page asks you to set up an owner login and enter the two tokens:
[First start: the setup page](#first-start-the-setup-page). After that you log in on the page.

1. Pick a deck and how many cards to take. Cards come in Anki's order: learning, reviews, then new
   cards within the deck's daily limit.
2. Type the answers, one card at a time or as a list. `?` means "I don't remember", an empty answer
   skips the card (it stays due).
3. **Check answers** sends the answers. The server grades the whole batch with one `claude -p` call.
   The answer keys reach the page only now, together with the grades.
4. Change any grade you disagree with and press **Save to Anki**. The server answers the cards through
   AnkiConnect and syncs Anki with its sync server (AnkiWeb or a self-hosted one). A card reviewed
````

````md E2-new

## How it works

The first time, the page asks you to set up an owner login, choose a grader and enter its credentials plus the AnkiConnect key:
[First start: the setup page](#first-start-the-setup-page). After that you log in on the page.

1. Pick a deck and how many cards to take. Cards come in Anki's order: learning, reviews, then new
   cards within the deck's daily limit.
2. Type the answers, one card at a time or as a list. `?` means "I don't remember", an empty answer
   skips the card (it stays due).
3. **Check answers** sends the answers. The server grades the whole batch with the selected CLI (Claude Code by default).
   The answer keys reach the page only now, together with the grades.
4. Change any grade you disagree with and press **Save to Anki**. The server answers the cards through
   AnkiConnect and syncs Anki with its sync server (AnkiWeb or a self-hosted one). A card reviewed
````

**E3.** (lines 52-59)

````md E3-old
- [Anki](https://apps.ankiweb.net/) desktop with the [AnkiConnect](https://ankiweb.net/shared/info/2055492159)
  add-on (code `2055492159`). Anki must be running while you review. It may sync with AnkiWeb or with a
  self-hosted sync server: anki-review only presses Sync.
- [Claude Code](https://docs.claude.com/en/docs/claude-code/overview), logged in: `claude -p "hi"`
  answers in a terminal. Grading uses your Claude subscription or API key.

## Run locally

````

````md E3-new
- [Anki](https://apps.ankiweb.net/) desktop with the [AnkiConnect](https://ankiweb.net/shared/info/2055492159)
  add-on (code `2055492159`). Anki must be running while you review. It may sync with AnkiWeb or with a
  self-hosted sync server: anki-review only presses Sync.
- A grading CLI: [Claude Code](https://docs.claude.com/en/docs/claude-code/overview) using your
  Claude subscription or API key, or [Codex](https://developers.openai.com/codex/auth) **0.166.0**
  with a dedicated ChatGPT device session. The deploy kit installs both; local installs need
  the CLI selected on the setup page. An operating agent does not choose the grading provider.

## Run locally

````

**E4.** (lines 63-69)

````md E4-old
make run
```

Open <http://127.0.0.1:8767/> and set up the owner login and the tokens:
[First start](#first-start-the-setup-page). Later, log in, pick a deck and press **Start**.

To get a binary instead: `make build`, then run `bin/anki-review`. The page is embedded, the binary
````

````md E4-new
make run
```

Open <http://127.0.0.1:8767/> and set up the owner login and grading credentials:
[First start](#first-start-the-setup-page). Later, log in, pick a deck and press **Start**.

To get a binary instead: `make build`, then run `bin/anki-review`. The page is embedded, the binary
````

**E5.** (lines 72-78)

````md E5-old
With Claude Code: open the clone in Claude Code and run `/setup-anki-review`, or just ask it to set
the app up. It checks the requirements, builds, asks which decks are knowledge decks and starts the
server. `/setup-anki-review check` only reports what is missing. You type the login, the password and
both tokens into the setup page yourself; the skill explains where each token comes from and never
sees them.

## First start: the setup page
````

````md E5-new
With Claude Code: open the clone in Claude Code and run `/setup-anki-review`, or just ask it to set
the app up. It checks the requirements, builds, asks which decks are knowledge decks and starts the
server. `/setup-anki-review check` only reports what is missing. You type the login, the password and
the selected grading credentials and AnkiConnect key into the setup page yourself; the skill explains where each token comes from and never
sees them.

## First start: the setup page
````

**E6.** (lines 81-88)

````md E6-old

| Field | What to enter |
| --- | --- |
| Login, Password, Repeat password | The owner login (up to 64 characters) and a password of at least 12 characters. The password unlocks the server after each restart and encrypts both tokens. |
| Claude token | See below |
| AnkiConnect key | See below |

**Claude token.** On a computer with Claude Code, where `claude` is logged in to your Claude account
````

````md E6-new

| Field | What to enter |
| --- | --- |
| Login, Password, Repeat password | The owner login (up to 64 characters) and a password of at least 12 characters. The password unlocks the server after each restart and encrypts the credentials. |
| Grading provider | Claude Code (default) or Codex; saved selection takes precedence over the environment |
| Claude token | For Claude Code: see below |
| Codex auth.json | For Codex: import the entire file from a dedicated device login, see [device session](deploy/README.md#codex-device-session) |
| AnkiConnect key | See below |

**Claude token.** On a computer with Claude Code, where `claude` is logged in to your Claude account
````

**E7.** (lines 96-105)

````md E7-old
and copy the value of `apiKey`. If it is `null`, leave the field empty. On a server set up with the
[deploy kit](deploy/README.md), the key was generated during the deploy.

Setup checks the AnkiConnect key first (Anki must be running), then makes one tiny Claude call (a
little of your quota). Only then it seals both tokens in `DATA_DIR/vault.json` and logs you in. After a
restart the page shows **Log in**: until you log in, the server is locked and grading waits. Once
logged in, **Settings** changes the password or either token; an empty field keeps its current value.

## Run on a server

````

````md E7-new
and copy the value of `apiKey`. If it is `null`, leave the field empty. On a server set up with the
[deploy kit](deploy/README.md), the key was generated during the deploy.

Setup checks the AnkiConnect key first (Anki must be running), then makes one tiny call to the
selected grader (a little of that provider's quota). Only then it seals the credentials and provider
choice in `DATA_DIR/vault.json` and logs you in. After a
restart the page shows **Log in**: until you log in, the server is locked and grading waits. Once
logged in, **Settings** changes the password, grading provider or credentials; omitted credentials keep their current value.
For Codex, import a new `auth.json` to reauthenticate. Follow the exact
[device login, secure import and cleanup steps](deploy/README.md#codex-device-session).

## Run on a server

````

**E8.** (lines 121-127)

````md E8-old
| <kbd>Cmd</kbd>/<kbd>Ctrl</kbd>+<kbd>Enter</kbd> | send all answers now |

- Typed answers are kept in the browser, so a reload loses nothing.
- If grading fails (Claude usage limit, timeout), the page says why and offers **Check again**.
- If Anki refuses the answers because a deck's study screen is open, go back to Anki's deck list and
  press **Save to Anki** again.
- A pass left unfinished for 12 hours is dropped on the next visit.
````

````md E8-new
| <kbd>Cmd</kbd>/<kbd>Ctrl</kbd>+<kbd>Enter</kbd> | send all answers now |

- Typed answers are kept in the browser, so a reload loses nothing.
- If grading fails (provider usage limit, timeout), the page says why and offers **Check again**.
- If Anki refuses the answers because a deck's study screen is open, go back to Anki's deck list and
  press **Save to Anki** again.
- A pass left unfinished for 12 hours is dropped on the next visit.
````

**E9.** (lines 139-150)

````md E9-old
| `KNOWLEDGE_DECKS` | `Tech,Career` | Top-level decks graded by meaning, comma-separated |
| `ANKI_CONNECT_URL` | `http://127.0.0.1:8765` | AnkiConnect endpoint |
| `ANKI_SYNC` | `true` | Sync Anki with its sync server (AnkiWeb or self-hosted) before a pass and after writing grades |
| `CLAUDE_BIN` | `claude` | Claude Code binary |
| `CLAUDE_MODEL` | the CLI's default | Model for grading, for example `sonnet` |
| `GRADE_TIMEOUT` | `5m` | Longest time one grading call may take |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

The Claude token and the AnkiConnect key are not settings: enter them on the setup page. If
`ANKI_CONNECT_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` is set in the environment, the server ignores it, logs a
warning and removes it from its own environment.

````

````md E9-new
| `KNOWLEDGE_DECKS` | `Tech,Career` | Top-level decks graded by meaning, comma-separated |
| `ANKI_CONNECT_URL` | `http://127.0.0.1:8765` | AnkiConnect endpoint |
| `ANKI_SYNC` | `true` | Sync Anki with its sync server (AnkiWeb or self-hosted) before a pass and after writing grades |
| `GRADER_PROVIDER` | `claude` | Initial setup default: `claude` or `codex`; encrypted saved selection wins |
| `CODEX_BIN` | `codex` | Codex CLI binary, must be version 0.166.0 |
| `CODEX_MODEL` | the CLI's default | Codex model for grading |
| `CLAUDE_BIN` | `claude` | Claude Code binary |
| `CLAUDE_MODEL` | the CLI's default | Model for grading, for example `sonnet` |
| `GRADE_TIMEOUT` | `5m` | Longest time one grading call may take |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

The Claude token, Codex auth.json and AnkiConnect key are credentials: enter them on the setup page. If
`ANKI_CONNECT_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` is set in the environment, the server ignores it, logs a
warning and removes it from its own environment.

````

**E10.** (lines 164-182)

````md E10-old
- **Sessions.** A random cookie: `HttpOnly`, `SameSite=Strict`, 30 days, kept in memory only, so a
  restart logs everyone out. The cookie is `Secure` when the proxy sends `X-Forwarded-Proto: https`.
  Changing the password logs out the other sessions.
- **Sealed tokens.** The Claude token and the AnkiConnect key live in `DATA_DIR/vault.json`, encrypted
  with XChaCha20-Poly1305 under a key from Argon2id (3 passes, 64 MiB) of the owner's password. Every
  save uses a fresh salt and nonce, and the file is replaced atomically.
- **Locked after a restart.** The tokens are in plaintext only in the server's memory, and only after a
  login. Until then nothing that needs Anki or Claude runs.
- **What it protects.** The tokens stay unreadable on a copy of the disk, in a backup or snapshot, in
  an env file, and for other users of the machine. A stolen `vault.json` costs one Argon2id run per
  password guess.
- **What it does not protect.** Root, or the service user, on the machine that runs the server can read
  the server's memory while it is unlocked, and so the tokens. On a deploy-kit server the AnkiConnect
  key also sits in `anki/secrets/` for the Anki container, so the vault mainly guards the Claude token.
- **Forgotten password.** There is no recovery. Stop the server, delete `DATA_DIR/vault.json`, start it
  and set up again with both tokens. On a server: `sudo rm /var/lib/anki-review/data/vault.json`, then
  `systemctl restart anki-review` (the restart drops the tokens still in memory).
- Requests with any `Host` other than `127.0.0.1` or `localhost` are refused, so a foreign site can't
  reach it through DNS rebinding. A reverse proxy in front must send `Host: 127.0.0.1:<port>`, and
````

````md E10-new
- **Sessions.** A random cookie: `HttpOnly`, `SameSite=Strict`, 30 days, kept in memory only, so a
  restart logs everyone out. The cookie is `Secure` when the proxy sends `X-Forwarded-Proto: https`.
  Changing the password logs out the other sessions.
- **Sealed credentials.** The selected provider, Claude token, Codex session and AnkiConnect key live in `DATA_DIR/vault.json`, encrypted
  with XChaCha20-Poly1305 under a key from Argon2id (3 passes, 64 MiB) of the owner's password. Password saves use a fresh salt and nonce; OAuth refresh retains the current KDF metadata
  and uses a fresh nonce. The file is replaced atomically.
- **Locked after a restart.** Credentials are decrypted only after owner login; Codex call files
  are temporary, with the filesystem boundary described below. Until then nothing that needs Anki or a grader runs.
- **What it protects.** The tokens stay unreadable on a copy of the disk, in a backup or snapshot, in
  an env file, and for other users of the machine. A stolen `vault.json` costs one Argon2id run per
  password guess.
- **What it does not protect.** Root, or the service user, on the machine that runs the server can read
  the server's memory while it is unlocked, and so the tokens. On a deploy-kit server the AnkiConnect
  key also sits in `anki/secrets/` for the Anki container, so the vault mainly guards the grading credentials.
- **Forgotten password.** There is no recovery. Stop the server, delete `DATA_DIR/vault.json`, start it
  and set up again with grading credentials and the AnkiConnect key. On a server: `sudo rm /var/lib/anki-review/data/vault.json`, then
  `systemctl restart anki-review` (the restart drops the tokens still in memory).
- Requests with any `Host` other than `127.0.0.1` or `localhost` are refused, so a foreign site can't
  reach it through DNS rebinding. A reverse proxy in front must send `Host: 127.0.0.1:<port>`, and
````

**E11.** (lines 186-192)

````md E11-old
  Content Security Policy.
- Claude runs as `claude -p --safe-mode --tools ""`: no tools, MCP servers, hooks or CLAUDE.md, so text
  on a card can't make it act. The prompt goes through stdin, not the command line.
- Card text and your answers are sent to Anthropic through Claude Code.

To report a vulnerability, see [SECURITY.md](SECURITY.md).

````

````md E11-new
  Content Security Policy.
- Claude runs as `claude -p --safe-mode --tools ""`: no tools, MCP servers, hooks or CLAUDE.md, so text
  on a card can't make it act. The prompt goes through stdin, not the command line.
- Codex runs with strict config, ignored user config/rules, ephemeral sessions, a read-only
  sandbox and all execution tools, MCP, hooks and inherited credentials disabled. It receives
  prompts on stdin and uses an isolated temporary home. See the
  [credential boundaries](deploy/README.md#codex-device-session).
- Card text and your answers are sent to Anthropic for Claude Code, or OpenAI for Codex.

To report a vulnerability, see [SECURITY.md](SECURITY.md).

````

**E12.** (lines 199-210)

````md E12-old
| Path | What |
| --- | --- |
| `cmd/anki-review` | entry point: config from the environment, logger, signals |
| `internal/domain` | the pass, its stages and the rules that need neither Anki nor Claude |
| `internal/usecase/review` | the review flow; ports for Anki, the grader and the state store |
| `internal/usecase/owner` | setup, login, sessions, settings, throttling and the lock |
| `internal/repository/vault` | `vault.json`: the tokens sealed under the owner's password |
| `internal/repository/ankiconnect` | AnkiConnect client, card rendering, the due queue |
| `internal/repository/claude` | grading prompts and the `claude -p` call |
| `internal/repository/filestore` | the state file |
| `internal/handler/httpapi` | JSON API, static page, host guard |
| `internal/app` | wiring and the HTTP server |
````

````md E12-new
| Path | What |
| --- | --- |
| `cmd/anki-review` | entry point: config from the environment, logger, signals |
| `internal/domain` | the pass, its stages and the rules independent of Anki and the grading providers |
| `internal/usecase/review` | the review flow; ports for Anki, the grader and the state store |
| `internal/usecase/owner` | setup, login, sessions, settings, throttling and the lock |
| `internal/repository/vault` | `vault.json`: the tokens sealed under the owner's password |
| `internal/repository/ankiconnect` | AnkiConnect client, card rendering, the due queue |
| `internal/repository/grading` | shared rubric, schema and result validation |
| `internal/repository/claude` | the Claude Code grading call |
| `internal/repository/codex` | isolated Codex calls and credential refresh |
| `internal/repository/filestore` | the state file |
| `internal/handler/httpapi` | JSON API, static page, host guard |
| `internal/app` | wiring and the HTTP server |
````

**E13.** (lines 213-219)

````md E13-old
| `deploy` | the server deploy kit, see its README |
| `.claude/skills/setup-anki-review` | the Claude Code setup skill and its helper scripts |

Tests fake Anki with an in-memory AnkiConnect server and Claude with the test binary itself
(`internal/testutil`), so `make check` needs neither.

[pre-commit](https://pre-commit.com/) runs gitleaks before each commit: `pre-commit install` once.
````

````md E13-new
| `deploy` | the server deploy kit, see its README |
| `.claude/skills/setup-anki-review` | the Claude Code setup skill and its helper scripts |

Tests fake Anki with an in-memory AnkiConnect server and both grading CLIs with the test binary itself
(`internal/testutil`), so `make check` needs neither.

[pre-commit](https://pre-commit.com/) runs gitleaks before each commit: `pre-commit install` once.
````
