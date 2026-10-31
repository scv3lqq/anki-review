# Mechanical edits: AGENTS.md

Base: 3422c81bf8c0a9a57d99dd3a2f43bb0c24a862bc. Generated with the canonical plan_edits helper.

**E1.** (lines 4-17)

````md E1-old

anki-review is a Go service with a plain HTML, CSS, and JavaScript page embedded
in the binary. It reads due cards through AnkiConnect, grades answers through
Claude Code, and writes grades only after the owner confirms them. The page and
grading feedback are in English.

Use README.md and deploy/README.md for workflow and deployment facts. The
configuration code and deploy scripts take precedence when documentation differs.

- Keep `internal/domain` independent of infrastructure. Use cases define ports
  and depend inward; AnkiConnect, Claude grading, vault, and state-file adapters
  belong in `internal/repository`.
- Keep HTTP handling in `internal/handler/httpapi`, wiring in `internal/app`, and
  the entry point in `cmd/anki-review`. The embedded page lives in `web`.
````

````md E1-new

anki-review is a Go service with a plain HTML, CSS, and JavaScript page embedded
in the binary. It reads due cards through AnkiConnect, grades answers through
Claude Code or Codex CLI, and writes grades only after the owner confirms them. The page and
grading feedback are in English.

Use README.md and deploy/README.md for workflow and deployment facts. The
configuration code and deploy scripts take precedence when documentation differs.

- Keep `internal/domain` independent of infrastructure. Use cases define ports
  and depend inward; AnkiConnect, grading-provider, vault, and state-file adapters
  belong in `internal/repository`.
- Keep HTTP handling in `internal/handler/httpapi`, wiring in `internal/app`, and
  the entry point in `cmd/anki-review`. The embedded page lives in `web`.
````

**E2.** (lines 24-30)

````md E2-old
Run commands from the repository root.

- `make check`: golangci-lint v2 and `go test -race ./...`; run before each commit.
  Tests use fake AnkiConnect and Claude implementations in `internal/testutil`,
  so they need no live Anki or Claude session.
- `make build`: build the local static binary with the embedded page.
- `make build-linux`: build the Linux amd64 binary for the deploy kit.
````

````md E2-new
Run commands from the repository root.

- `make check`: golangci-lint v2 and `go test -race ./...`; run before each commit.
  Tests use fake AnkiConnect and grading CLI implementations in `internal/testutil`,
  so they need no live Anki or Claude session.
- `make build`: build the local static binary with the embedded page.
- `make build-linux`: build the Linux amd64 binary for the deploy kit.
````

**E3.** (lines 39-51)

````md E3-old
workflow for autostart. The `check` mode diagnoses and reports without repairs,
installs, configuration writes, builds, or starting the app.

Claude Code remains the application's grading dependency when Codex operates the
workflow. Preserve `claude`, `claude setup-token`, `CLAUDE_BIN`, `CLAUDE_MODEL`,
and Anthropic token semantics. A Codex configuration change does not replace the
grader or rename application settings.

Keep credentials out of commands, logs, tracked files, and conversation output.
The owner enters the login, password, Claude token, and AnkiConnect key directly
on the setup page. Tokens do not belong in `.env`; never commit `.env`, collection
data, saved answer keys, or deployment secrets.

````

````md E3-new
workflow for autostart. The `check` mode diagnoses and reports without repairs,
installs, configuration writes, builds, or starting the app.

The grading provider is selected by the owner, independently of the operating agent.
Claude Code remains the default; preserve `claude`, `claude setup-token`, `CLAUDE_BIN`,
`CLAUDE_MODEL`, and Anthropic token semantics. Codex CLI 0.166.0 uses `CODEX_BIN`,
`CODEX_MODEL`, and a dedicated device-login auth.json imported into the encrypted vault.
`GRADER_PROVIDER` sets only the initial default; the encrypted saved choice wins.
Never reuse a device session or expose auth snapshots; refreshed credentials must be
resealed before accepting grading results. See deploy/README.md for login and cleanup.

Keep credentials out of commands, logs, tracked files, and conversation output.
The owner enters the login, password, selected grading credentials, and AnkiConnect key directly
on the setup page. Tokens do not belong in `.env`; never commit `.env`, collection
data, saved answer keys, or deployment secrets.

````
