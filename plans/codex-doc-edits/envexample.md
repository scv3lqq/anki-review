# Mechanical edits: .env.example

Base: 3422c81bf8c0a9a57d99dd3a2f43bb0c24a862bc. Generated with the canonical plan_edits helper.

**E1.** (lines 11-16)

````md E1-old
ANKI_CONNECT_URL=http://127.0.0.1:8765
# Sync Anki with its sync server (AnkiWeb or self-hosted) before a pass and after writing grades.
ANKI_SYNC=true
# Claude Code binary.
CLAUDE_BIN=claude
# Model for grading, for example sonnet. Default: the CLI's default.
````

````md E1-new
ANKI_CONNECT_URL=http://127.0.0.1:8765
# Sync Anki with its sync server (AnkiWeb or self-hosted) before a pass and after writing grades.
ANKI_SYNC=true
# Initial setup default only; encrypted saved provider wins. Either claude or codex.
GRADER_PROVIDER=claude
# Codex CLI 0.166.0 binary; auth.json is imported on the page, never configured here.
CODEX_BIN=codex
# CODEX_MODEL=
# Claude Code binary.
CLAUDE_BIN=claude
# Model for grading, for example sonnet. Default: the CLI's default.
````
