# anki-review project instructions

## Project and architecture

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
- Follow the Go toolchain version in go.mod. Use zerolog for logging and wrap
  errors with operation context and `%w`. Keep functions focused and dependencies
  explicit; add meaningful tests before changing behavior.

## Development commands

Run commands from the repository root.

- `make check`: golangci-lint v2, `go test -race ./...`, and `make check-deploy`;
  run before each commit. Deployment checks are shell syntax and offline fixtures.
  Tests use fake AnkiConnect and grading CLIs, so they need no live services.
- `make build`: build the local static binary with the embedded page.
- `make build-linux`: build the Linux amd64 binary for the deploy kit.
- `make run`: run locally, loading optional settings from the ignored `.env`.

## Setup, deployment, and data safety

Use `$setup-anki-review`, defined in
`.agents/skills/setup-anki-review/SKILL.md`, for setup and deployment requests.
Its modes are `local` (default), `check`, `server`, and `server-update`. Follow its
server reference for auto-deploy, self-hosted sync, and updates; use its local
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

Setup diagnostics must not submit API writes or press **Save to Anki**. Use the
skill's helpers for Anki checks and secret transfer. A restarted server is locked;
unlocking it can resume a saved pass, including grading or confirmed-grade writes.
Follow the skill's saved-pass checks before restarting or asking the owner to
unlock, and preserve its backup and sync-direction checks when moving collections.

## Native agent configuration

Codex uses this standalone `AGENTS.md`, project skills in `.agents/skills`, and
optional native settings in `.codex`. Claude Code keeps its own `.claude`
configuration. Maintain them independently: do not symlink instruction files,
rewrite Claude sources as part of a Codex migration, or duplicate existing imports.
Run Codex setup helpers from `.agents/skills/setup-anki-review`; preserve their
real paths and application-specific commands. Skill text does not grant tool
permissions; use the active runtime's permissions and available process tools.
