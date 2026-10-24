# anki-review

Review your due Anki cards on a web page: type the answers, Claude Code grades them against each
card's answer key, you confirm or change every grade, and the grades go back into Anki through its own
scheduler, the same as pressing the buttons in the reviewer.

The page and the grading feedback are in English.

## How it works

1. Pick a deck and how many cards to take. Cards come in Anki's order: learning, reviews, then new
   cards within the deck's daily limit.
2. Type the answers, one card at a time or as a list. `?` means "I don't remember", an empty answer
   skips the card (it stays due).
3. **Check answers** sends the answers. The server grades the whole batch with one `claude -p` call.
   The answer keys reach the page only now, together with the grades.
4. Change any grade you disagree with and press **Save to Anki**. The server answers the cards through
   AnkiConnect and syncs with AnkiWeb. A card reviewed elsewhere in the meantime is skipped, not
   answered twice.
5. Take more cards from the same deck (**More cards**) or go back to the deck list (**Decks**).

Decks listed in `KNOWLEDGE_DECKS` (top-level names) are graded by meaning. All other decks are graded
as language cards: the exact word, its form, collocation and spelling count.

## Requirements

- [Go](https://go.dev/dl/) 1.27 or newer, to build.
- [Anki](https://apps.ankiweb.net/) desktop with the [AnkiConnect](https://ankiweb.net/shared/info/2055492159)
  add-on (code `2055492159`). Anki must be running while you review.
- [Claude Code](https://docs.claude.com/en/docs/claude-code/overview), logged in: `claude -p "hi"`
  answers in a terminal. Grading uses your Claude subscription or API key.

## Run locally

```sh
git clone https://github.com/scv3lqq/anki-review.git
cd anki-review
make run
```

Open <http://127.0.0.1:8767/>, pick a deck and press **Start**.

To get a binary instead: `make build`, then run `bin/anki-review`. The page is embedded, the binary
needs no other files.

With Claude Code: open the clone in Claude Code and run `/setup-anki-review`, or just ask it to set
the app up. It checks the requirements, builds, asks which decks are knowledge decks and starts the
server. `/setup-anki-review check` only reports what is missing.

## Use

| Key | Action |
| --- | --- |
| <kbd>Enter</kbd> | next card |
| <kbd>Shift</kbd>+<kbd>Enter</kbd> | line break in an answer |
| <kbd>?</kbd> in an empty answer | "I don't remember" (graded Again) |
| <kbd>Cmd</kbd>/<kbd>Ctrl</kbd>+<kbd>Enter</kbd> | send all answers now |

- Typed answers are kept in the browser, so a reload loses nothing.
- If grading fails (Claude usage limit, timeout), the page says why and offers **Check again**.
- If Anki refuses the answers because a deck's study screen is open, go back to Anki's deck list and
  press **Save to Anki** again.
- A pass left unfinished for 12 hours is dropped on the next visit.
- The server keeps the current pass in `DATA_DIR`: a restart resumes it, grading or writing included.

## Configuration

Environment variables, all optional.

| Variable | Default | Meaning |
| --- | --- | --- |
| `PORT` | `8767` | Port on 127.0.0.1 |
| `DATA_DIR` | `./data` | State of the current pass. It holds the answer keys: the directory is `0700`, the file `0600` |
| `KNOWLEDGE_DECKS` | `Tech,Career` | Top-level decks graded by meaning, comma-separated |
| `ANKI_CONNECT_URL` | `http://127.0.0.1:8765` | AnkiConnect endpoint |
| `ANKI_CONNECT_KEY` | none | AnkiConnect `apiKey`, if you set one in the add-on config |
| `ANKI_SYNC` | `true` | Sync with AnkiWeb before a pass and after writing grades |
| `CLAUDE_BIN` | `claude` | Claude Code binary |
| `CLAUDE_MODEL` | the CLI's default | Model for grading, for example `sonnet` |
| `GRADE_TIMEOUT` | `5m` | Longest time one grading call may take |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

Logs go to stderr: readable in a terminal, JSON lines otherwise. They never contain card text or answers.

## Security

The server listens on 127.0.0.1 only and has no login of its own.

- The page gets the questions first and the answer keys only together with the grades.
- Requests with any `Host` other than `127.0.0.1` or `localhost` are refused, so a foreign site can't
  reach it through DNS rebinding. A reverse proxy in front must send `Host: 127.0.0.1:<port>` and must
  restrict who gets in on its own.
- POST requests must be JSON, so other sites can't submit forms to it. The page runs under a strict
  Content Security Policy.
- Claude runs as `claude -p --safe-mode --tools ""`: no tools, MCP servers, hooks or CLAUDE.md, so text
  on a card can't make it act. The prompt goes through stdin, not the command line.
- Card text and your answers are sent to Anthropic through Claude Code.

To report a vulnerability, see [SECURITY.md](SECURITY.md).

## Development

```sh
make check   # golangci-lint v2 and go test -race
```

| Path | What |
| --- | --- |
| `cmd/anki-review` | entry point: config from the environment, logger, signals |
| `internal/domain` | the pass, its stages and the rules that need neither Anki nor Claude |
| `internal/usecase/review` | the review flow; ports for Anki, the grader and the state store |
| `internal/repository/ankiconnect` | AnkiConnect client, card rendering, the due queue |
| `internal/repository/claude` | grading prompts and the `claude -p` call |
| `internal/repository/filestore` | the state file |
| `internal/handler/httpapi` | JSON API, static page, host guard |
| `internal/app` | wiring and the HTTP server |
| `web` | the page: plain HTML, CSS and JS, embedded into the binary |

Tests fake Anki with an in-memory AnkiConnect server and Claude with the test binary itself
(`internal/testutil`), so `make check` needs neither.

[pre-commit](https://pre-commit.com/) runs gitleaks before each commit: `pre-commit install` once.

## License

[MIT](LICENSE)
