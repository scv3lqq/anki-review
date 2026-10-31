# Repository Codex migration plan

## Goal

Provide independent native Codex instructions and setup workflow while preserving
every original Claude file and all application code. Repair the existing import
in place; keep its valid helper scripts unchanged.

## Evidence and boundaries

- Base: `8aff7f5`. Tracked files were clean; `.agents/` was already untracked.
- No root `CLAUDE.md`, `AGENTS.md`, `.mcp.json`, `.claude.json`, project settings,
  rules, commands, agents, or plugins were present. One setup skill was present.
- The imported skill has invalid `.Codex` paths, renamed application commands,
  and Claude-only metadata and tool vocabulary. Its four helper scripts and
  server reference match the Claude originals byte for byte.
- `.claude/scheduled_tasks.lock` is process/session lock state, not task definitions.
- The migrator's scan, plan, doctor, and dry run completed. Its merge mode replaces
  colliding skills and omits shell helpers stored at the skill root. Therefore a
  full real migration ran only in `.git/codex-migration-audit/stage/.codex`.
  Selective reconciliation into the existing import is required.
- Baseline hashes and modes are saved in `.git/codex-migration-audit/baseline.json`.
  They cover tracked files, Claude sources, the existing import, and a hash of
  global Codex config. No secret values were copied.
- `make check` passed before edits: golangci-lint reported zero issues and the
  race-enabled Go suite passed. No application behavior is being changed.
- Installed CLI: `codex-cli 0.166.0`. Strict app-server startup config validation
  passed. The migrator validator accepts the existing skill but does not detect
  its broken commands. Missing optional project config is a warning, not a failure.

## Current official format evidence

Checked on 2026-10-31, independently of the migrator's April reference:

- [Project instructions](https://developers.openai.com/codex/guides/agents-md/):
  ordinary repository `AGENTS.md`, no alias or custom fallback required.
- [Skills](https://developers.openai.com/codex/skills/): repository
  `.agents/skills/<name>/SKILL.md`, with `name` and `description` frontmatter.
- [Configuration](https://developers.openai.com/codex/config-reference/) and
  [MCP](https://developers.openai.com/codex/mcp/): project `.codex/config.toml`
  is optional; there are no project server definitions to translate.
- [Subagents](https://developers.openai.com/codex/subagents/): standalone project
  TOML definitions are supported, but this repository has none to migrate.
- [Hooks](https://developers.openai.com/codex/hooks/): project hooks are supported;
  `hooks` is the current feature key and `codex_hooks` is deprecated. None are
  configured here, so no hooks or feature flags should be invented.

## Shared definitions

- Codex is the assistant operating the setup workflow. Claude Code remains the
  application's grader. Keep `claude`, `claude setup-token`, `claude -p`,
  `CLAUDE_BIN`, `CLAUDE_MODEL`, Anthropic token terminology, and deployment
  semantics intact. This is a configuration migration, not a grader replacement.
- All Codex workflow helpers are the existing regular files under
  `.agents/skills/setup-anki-review/`. Preserve their bytes and modes; never call
  helpers through `.claude/` from the adapted Codex Markdown files.
  The three entrypoint scripts are executable; `lib.sh` is sourced, not executable.
- Preserve local, check, server, server-update, auto-deploy, self-hosted sync,
  autostart, owner lock, saved-pass, and secret-handling behavior.
- Skill mode comes from the user's request (`local` by default), not an expanded
  `$ARGUMENTS` variable. Use available question tools or a single concise question.
  Do not assume Claude tool names or metadata create permissions.
- Use native process sessions for long operations: start with `exec_command`,
  retain its returned `session_id`, poll with `write_stdin`, and interrupt only
  the process session created by this workflow. Use a PTY when Ctrl-C cleanup is
  needed, and short polling intervals so progress and user input are handled.
  A yielded shell process is not a detached Claude background task. Do not end
  the turn merely because a process is pending; ask for required human input and
  continue independent work. Waiting for mandatory human input may require ending
  a turn when no asynchronous question tool is available: retain ownership of the
  session IDs or close and later reopen only the workflow's own tunnel. Never
  leave an untracked process. If process sessions are unavailable, explain the
  actual limitation rather than inventing a background-task API.
- Perform steps authorized by the setup request without redundant confirmations.
  Preserve checks for missing choices, unknown hosts, existing services, active
  passes, sync direction, and secrets entered directly by the user.
- No application, original Claude, global config, credentials, or live deployment
  changes. No installations, paid grading calls, collection writes, or SSH probes
  as migration validation. Tests and isolated harmless script probes are allowed.
- Public artifacts contain English prose, relative repository paths, no personal
  identities, private hosts, credentials, transcript excerpts, or home paths.

## Tasks

### T1: Project instructions

Ownership: `AGENTS.md` only. No dependencies.

Create a regular standalone file headed `# anki-review project instructions`.
Summarize project facts from README.md, deploy/README.md, Makefile and go.mod:
Go with embedded HTML/CSS/JS; inner domain/usecase dependencies point inward;
AnkiConnect and Claude grading adapters stay in repositories; HTTP in handlers;
zerolog, wrapped errors, meaningful tests, and `make check` before commits.
Name `make build`, `make build-linux`, `make run`, and mocked test dependencies.
Point setup/deploy requests to the native project skill and its four modes.
Explain Codex configuration ownership and the unchanged Claude application
dependency. Preserve secret, Anki write, and locked/saved-pass cautions from the
setup workflow. Keep the file concise and below 32 KiB. Do not copy global
personal instructions or create a CLAUDE.md, symlink, optional config, or agent.

Task gates: verify the file is regular/nonempty and under 32 KiB; find literal
`make check`, `$setup-anki-review`, `CLAUDE_BIN`, and `internal/domain`; run
`git diff --check`. Baseline: this file was absent.

### T2: Repair setup skill

Ownership: `.agents/skills/setup-anki-review/SKILL.md` only. No dependencies.

Repair the existing import using the original as semantic evidence, preserving
the complete six-step workflow and linked server modes. Keep name and description
with existing triggers. Remove `argument-hint` and `allowed-tools`; incorporate
their useful intent using Shared definitions, not an invented enforcement layer.
Fix every invalid `.Codex` helper path and application command/token rename.
Use Codex for the acting assistant, Claude for the application runtime. Replace
`$ARGUMENTS`, `AskUserQuestion`, `run_in_background`, and obsolete permission
claims with the supported behaviors above. Do not change the shell helpers or
use broad provider-name substitution across the tree.

Make the read-only `check` promise explicit and higher priority than local repair
steps: diagnostics, the announced grading smoke test, and report only. Missing
`.env` or prerequisites must be reported without creating files, installing,
repairing, building, or starting the app. Server checks remain status/kit-diff only.

Task gates: frontmatter contains only name and description; no `.Codex/`,
`.claude/skills/`, `$ARGUMENTS`, `AskUserQuestion`, `run_in_background`, or
`Codex token`; literal `claude setup-token` and `claude -p --safe-mode` remain;
all helper references resolve; `git diff --check`. Baseline: incompatible
metadata and commands are present. No source-code behavior changes or new tests.

### T3: Adapt server workflow

Ownership: `.agents/skills/setup-anki-review/references/server.md` only.
No dependencies; consumes the same Shared definitions as T2.

Retain the server, update, sync migration, status, and auto-deploy workflows.
Change helper references to `.agents/skills/setup-anki-review/`. Replace Claude
question/background/task-stop vocabulary using Shared definitions. Preserve
application Claude command/token references and every operational safeguard.
Explain process-session tracking once, then reference it consistently. Never
end a turn merely because a process is pending; preserve the human-input exception
and process ownership rules in Shared definitions.

Task gates: no `.claude/skills/`, `AskUserQuestion`, `run_in_background`, or
`TaskStop`; `claude setup-token`, `DEPLOY_AUTO`, `wait-install`, and `wait-setup`
remain; all helper references resolve; `git diff --check`. Baseline: Claude paths
and task APIs are present. Review against the original for accidental omissions.

### T4: Durable migration audit

Ownership: `.codex/migration-report.md` only. Depends on T1, T2, T3 and their review.

Write the complete inventory, selected changes, preserved import files, unsupported
semantics and non-applicable surfaces, exact created/modified/preserved file list,
official references/date, executed validation results and limits. Distinguish
Claude metadata tool restrictions from Codex session permissions. Explain the
staging run, why a later dry run still offers overwrites, and why rerunning the
migrator directly would regress the reviewed adaptation. Document the ignored
lock state and the unchanged helper's cosmetic Claude window hint. State that
the app still requires Claude Code. Do not claim live deploy/login/Anki checks.
Mention absence of a built-in TODO tool; the plan records execution design,
not a fabricated task-tool result. Do not include private raw tool output.

Task gates: every recorded check has evidence; all changes are listed; no secret
or personal path literals; official source links are present; `git diff --check`.

## Seams

| Producer | Consumer | Shared definition |
| --- | --- | --- |
| T1 | T2 | Setup skill name, four modes, app remains Claude-based |
| T2 | T3 | Native process sessions, helper paths, permissions and secret handling |
| T1, T2, T3 | T4 | File inventory, preserved helpers, scope and validation limits |

## Gates and acceptance criteria

The orchestrator and final validator run full gates serially. Implementers run
only their scoped gates. Reviewers are read-only. The existing `.agents` files
are part of this authorized migration and will be committed with explicit paths.

1. `make check`: zero lint issues, race-enabled tests pass.
2. `git diff --check` and, after explicit staging, `git diff --cached --check`:
   exit zero (unstaged checks do not cover untracked files).
   `git diff 8aff7f5 -- .claude` is empty and
   no tracked application file changes. Baseline hashes also prove ignored
   Claude files, global config, and all four imported helpers unchanged.
3. All six native skill files are regular files, with one SKILL.md, executable
   entrypoints, and the non-executable sourced library. `bash -n` succeeds for each
   helper. No extra skill/agent/MCP/hook.
4. `python3 -B "$MIGRATOR" --validate-target ./.codex/`: no errors; absence of
   optional config is documented. Repeat scan/doctor/dry-run after reconciliation
   without applying overwrite proposals to the repaired import.
5. `codex --strict-config app-server --print-startup-config`: exit zero; inspect
   only status, never publish configuration values. Use the installed native
   discovery interface to prove project instructions and the unique enabled
   skill load; document any runtime limitations accurately.
6. Independent skill dry run before implementation and on final instructions,
   scoped plan review, then full acceptance validation. No live setup execution.
7. Commit scoped verified files with configured signing and push the feature
   branch under the user's standing authorization. Never merge into main; check
   CI when a run exists (the workflow runs on main pushes and pull requests).

## Not doing

No Claude source edits, symlinks, duplicate skills, application changes, global
configuration changes, secret migration, speculative MCP/hooks/agents, deployment,
or unrelated cleanup. Native TODO tooling is unavailable in this runtime.

## Plan review

The independent pre-implementation dry run verified helper integrity and native
PTY interruption. Its findings were incorporated: make check mode strictly
diagnostic, allow required human-input waits with tracked process ownership, and
check staged whitespace because the imported files begin untracked. The requested
native `skill-dry-run` role was unavailable, so a separate default agent performed
the same read-only review without changing runtime defaults.

## Implementation review corrections

The post-implementation dry run confirmed diagnostic check mode on an isolated
fixture (missing environment/prerequisites, no files created). T3 must distinguish
finite command completion from long-lived tunnel readiness. It must also inspect
the install window's printed exit code: `wait-install` can itself exit zero after
reporting a failed window. Apply the same rule to sudo-restart without changing
the preserved helper scripts. The native implementer role was also unavailable;
separate default agents owned T1, T2, and T3 with the same scoped contracts.

## Concurrent global state

The global config hash changed during the audit, after earlier preservation checks
passed. No task command wrote global settings. The original baseline is retained;
its global identity assertion must not be relabeled as passing. Repository-only
immutability checks remain required. Native startup/discovery probes are also
checked against the current global hash before and after execution. Do not restore
or overwrite concurrent global changes. Report attribution separately when known.
The user confirmed another Codex agent is updating global settings. Coordination
was requested with that task; repository and global ownership remain separate.
