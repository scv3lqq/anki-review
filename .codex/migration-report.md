# Codex migration audit

Audit date: 2026-10-31. Installed runtime: `codex-cli 0.166.0`.
Baseline commit: `8aff7f5`. Implementation commit: `a1cba8c`.

The repository now has standalone Codex project instructions and one repaired
native setup skill. The original Claude configuration and application files are
unchanged. Independent local acceptance passed for T1-T4 with no blockers or
actionable migration work remaining. Publication and remote CI are completion
steps after committing this audit; their results are recorded in the final
completion response.

## Inventory and decisions

| Surface at the start | Finding and disposition |
| --- | --- |
| Project instruction files | No Claude or Codex project instruction file was present. Created a regular `AGENTS.md` from project facts, without a symlink or a new Claude instruction file. |
| `.claude/skills/` | One `setup-anki-review` skill with four shell helpers and one server reference. All original files remain unchanged. |
| `.agents/skills/` | An existing untracked import of the same six files. Repaired its two Markdown files in place and preserved the four valid helpers. No second skill was created. |
| `.codex/` | Absent initially. Added the migration plan and this audit; no runtime overrides were needed. |
| Commands and agents | No project `.claude/commands/` or `.claude/agents/` definitions to convert. No speculative Codex skills or agents were added. |
| MCP | No project `.mcp.json`, `.claude.json`, or server definitions to migrate. No project MCP entry was added or duplicated. |
| Settings, rules, and hooks | No project Claude settings, scoped rules, or hooks were found. No optional `config.toml`, hooks file, or feature flag was invented. |
| Plugins and marketplaces | No project plugin or marketplace configuration to migrate. Global installations remain outside this migration's scope. |
| `.claude/scheduled_tasks.lock` | Ignored process/session lock state, not scheduled-task definitions. Preserved unchanged; it was not converted into a Codex automation. |

The repaired workflow keeps `local`, `check`, `server`, and `server-update`,
including autostart, auto-deploy, self-hosted sync, saved-pass checks, and secret
handling. `check` explicitly diagnoses and reports without changing setup state.
The assistant uses Codex's process sessions and tracks tunnel readiness separately
from finite command completion. Install checks inspect the window's printed exit
status as well as the wait helper's own exit status.

Claude Code remains the application's grader. Its commands, `CLAUDE_BIN`,
`CLAUDE_MODEL`, Anthropic credentials, and deployment behavior were preserved.
This migration does not replace that application dependency with an OpenAI model.

## Complete file list

The six skill files existed before this work and are now tracked. “Repaired” and
“preserved” below describe changes relative to that existing import, not merely
the files' new Git tracking status.

| File | Result |
| --- | --- |
| `AGENTS.md` | Created independent project instructions. |
| `.codex/migration-plan.md` | Created the scoped plan, gates, review corrections, and concurrent-state audit notes. |
| `.codex/migration-report.md` | Created this audit and validation record. |
| `.agents/skills/setup-anki-review/SKILL.md` | Repaired paths, application/runtime terminology, mode selection, permission guidance, and native process handling. |
| `.agents/skills/setup-anki-review/references/server.md` | Repaired helper paths and question/process handling while retaining deployment behavior and safeguards. |
| `.agents/skills/setup-anki-review/check.sh` | Preserved bytes and executable mode; newly tracked. |
| `.agents/skills/setup-anki-review/server.sh` | Preserved bytes and executable mode; newly tracked. |
| `.agents/skills/setup-anki-review/install-ankiconnect.sh` | Preserved bytes and executable mode; newly tracked. |
| `.agents/skills/setup-anki-review/lib.sh` | Preserved bytes and non-executable mode; it is sourced by the entrypoints. Newly tracked. |

No application source, original Claude configuration, or credentials were changed
or copied into these artifacts. Temporary baselines, probes, schemas, and staged
converter output are task-local under `.git/codex-migration-audit`, outside the
committed file list.

## Migration execution and repeat-run caution

The migrator's `--scan-only`, `--plan`, `--doctor`, and `--dry-run` were run before
editing. Inspection showed that `--merge` still replaces colliding skill files
and that the converter does not copy shell helpers stored at a skill's root.
To protect the existing import, the full real migration ran in
`.git/codex-migration-audit/stage/.codex`. Its generated report was inspected,
then the two existing Codex Markdown files were selectively reconciled.

After reconciliation, validation and diagnostic dry runs were repeated. A later
source-based dry run still proposes replacing the skill and flags the untouched
Claude metadata for review. Those proposals describe a fresh conversion of the
original source; they are not unresolved defects in the reviewed Codex target.
Do not apply that overwrite directly to the repaired import. Stage any future
conversion and review its differences first.

## Compatibility limits

- Claude's `allowed-tools` pre-approval semantics were not recreated as a Codex
  permission boundary. The unsupported metadata was removed; Codex uses the
  active session's native permission policy. Workflow safeguards remain explicit
  instructions and do not grant tool access.
- Claude's argument hint and `$ARGUMENTS` expansion were replaced with explicit
  mode selection from the user's request. Question and process handling use
  available runtime capabilities instead of assuming Claude-specific APIs.
- The unchanged server helper still has a cosmetic window hint naming Claude
  Code. It does not select the assistant or affect execution.
- No built-in TODO tool was available. The plan records the actual design and
  gates, without claiming a task-tool invocation.
- The requested native `implementer`, `plan-reviewer`, `skill-dry-run`, and
  `acceptance-validator` roles
  could not be spawned in this existing session. Separate default agents carried
  out the same scoped implementation, independent reviews, and final validation. No model defaults
  or global configuration were changed to work around that session limitation;
  the roles were reported discoverable in new sessions.

## Validation evidence

| Check | Observed result |
| --- | --- |
| `make check` | PASS: zero golangci-lint issues; `go test -race ./...` passed. |
| Migrator `--validate-target ./.codex/` | PASS: no errors. It reports the absent optional `config.toml` as a warning; there are no project settings or MCP entries requiring one. |
| `verify-preservation.py --repository-only` | PASS: all 82 protected original/imported files retain their hashes and modes, including Claude sources and the four imported helpers. Source inventory is unchanged. |
| Native `skills/list` probe | PASS: exactly one enabled project skill, `setup-anki-review`; the name is unique across discovered scopes; zero project discovery errors. |
| Native instruction probe | PASS: the complete `AGENTS.md` body is injected into the native prompt. No model call was made. |
| `codex --strict-config app-server --print-startup-config` | PASS: exit zero. Configuration values were not published. |
| `bash -n` for all four helpers | PASS. Three entrypoints remain executable; `lib.sh` retains its sourced-library mode. |
| Whitespace checks | PASS for the implementation, including the staged files. The report is checked separately before staging. |
| `gitleaks git --log-opts '8aff7f5..HEAD' --redact --no-banner --no-color` | PASS through implementation commit `a1cba8c`: exit zero, no leaks. A separate scan of the current `.codex` files was also clean. The final-range scan runs after the audit commit. |
| Independent plan review of T1-T3 | PASS with no blockers. |
| Independent skill dry runs | PASS after the check-mode, tunnel-readiness, and install-status corrections. An isolated missing-prerequisites fixture created no files; native PTY interruption was verified. |
| Commit signatures | PASS: `git verify-commit 7f07808` and `git verify-commit a1cba8c` both exited zero. Configured signing was preserved. |
| Independent local acceptance of T1-T4 | PASS: no blockers. The validator confirmed the application checks, native discovery, preservation, coordinated global hash, nine-file inventory, privacy, whitespace, regular-file and helper-syntax gates, secret scans, and existing commit signatures. |
| Publication and remote CI | Completion steps after the audit commit: push the feature branch and dispatch its supported CI workflow. Results are recorded in the completion response; this checkpoint does not claim either step has already completed. |

The local probe files named above are in `.git/codex-migration-audit`. Native
startup and discovery checks were also bracketed by global-config hash checks.
The original global hash changed during the audit because a separate concurrent
task updated global marketplace/plugin entries, as explicitly confirmed by the
user and that task. This migration made no global configuration writes. The
confirmed post-change hash stayed stable across the native checks; whole-session
global byte identity is therefore not claimed, and the original baseline remains
available. Concurrent changes were neither reverted nor overwritten.

No live deployment, SSH connection, GUI login, paid grading call, installation,
or Anki collection write was executed as migration validation. The checks prove
configuration discovery, instruction consistency, preservation, and the existing
application test suite, not a fresh live setup on an external machine.

## Official format verification

Current official documentation was checked on 2026-10-31, independently of the
migration skill's older bundled reference:

- [Project instructions](https://developers.openai.com/codex/guides/agents-md/)
  support the standalone repository `AGENTS.md` used here.
- [Skills](https://developers.openai.com/codex/skills/) document native repository
  discovery under `.agents/skills` and the skill metadata used here.
- [Configuration reference](https://developers.openai.com/codex/config-reference/)
  and [MCP](https://developers.openai.com/codex/mcp/) document optional project
  configuration and native server entries. No source entries required conversion.
- [Subagents](https://developers.openai.com/codex/subagents/) document standalone
  project agent definitions; none were present to migrate.
- [Hooks](https://developers.openai.com/codex/hooks/) document native project hooks
  and the current `hooks` feature key. The old `codex_hooks` alias is deprecated;
  neither key was added because this repository defines no hooks.
