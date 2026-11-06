# Deployment handoff retrospective

## Goal and evidence

Make the existing setup skill avoid three demonstrated deployment problems:
manual sudo stalled despite a subsequently authorized stored credential; a task
window opened in a detached tmux session; an independent review found incomplete
rollback and asynchronous guest recovery checks in a temporary maintenance script.
Do not change server permissions, app code, credentials or the deployed service.

Base: `9c7c596067d5dd402c6d89bef9780e630ee53a00`.
Branch: `feat/retro-deployment-handoff`.
Project AGENTS.md applies; no path-scoped rules are configured.

## Shared definitions

The exact prose is defined by the generated old/new blocks in
`plans/retro-deployment-edits/skill.md` and `plans/retro-deployment-edits/server.md`.
`PLAN_EDITS` is the installed feature workflow's verbatim prose-edit checker.
Its existing `stub`, `apply` and `check` operations are used without modification.
No personal credential identifiers, hosts, URLs, home paths or session transcript
excerpts belong in these public artifacts. Preserve the separate Claude skill.

## Tasks

### T1: Update setup entry point

Own only `.agents/skills/setup-anki-review/SKILL.md`.
Apply the skill.md edit file against the base with PLAN_EDITS; no hand transcription.
Scoped gate: PLAN_EDITS check with the same edit file, target and base.
Done means exact generated prose and no other changes.

### T2: Update deployment procedures

Own only `.agents/skills/setup-anki-review/references/server.md`.
Apply the server.md edit file against the base with PLAN_EDITS; no hand transcription.
Scoped gate: PLAN_EDITS check with the same edit file, target and base.
Done means exact generated prose and no other changes.
T1 and T2 can run together; commit their green output as one wave.

## Seams

T1's credential route link points at T2's existing Settings and access heading.
T2's installation and restart steps follow the same explicitly authorized credential
route and retain mode, saved-pass and operation-result checks.

## Acceptance criteria

1. Both PLAN_EDITS check commands pass; before applying changes they must fail.
2. Independent skill-dry-run checks the proposed workflow before implementation,
   then checks exact resulting content: authorized and unauthorized credential
   sources, detached/ambiguous tmux clients, check mode, rollback ExecStartPre,
   asynchronous recovery and private/public marker permissions. No live actions.
3. `make check` and `git diff --check` pass; only this plan, its two edit files and
   the two Codex skill documents differ from base. Claude sources are unchanged.

## Gates and baseline

Full gates: `make check`, `git diff --check`.
Baseline check results and exact commands/exit codes are saved in private scratch.
Save each pre-commit gate log and associate it with the resulting commit. Reuse
unchanged-code baseline evidence for the plan-only commit. Final validator runs
full gates once after the implementation wave; heavy suites remain serial.
Verbatim prose tasks need no additional code reviewer: exact edit checks and the
independent skill dry run plus final acceptance validation cover them.

## Not doing

No live deployment or credential retrieval, no sudo policy changes, no new secret
permissions, no password-store identifiers, no helper-code changes, no edits to
shared dotfiles or their existing dirty files, and no broad workflow rewrite.
