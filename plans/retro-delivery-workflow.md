# Tighten deployment planning and reporting

## Goal and scope
Apply four small, evidence-based procedure improvements to the project setup skill.
Base: `05e8ed8`. Branch: `feat/retro-delivery-workflow`.
No runtime code, grading behavior, credentials, permissions or live services change.
The independent global instruction checkout has unrelated edits and is outside scope.

## Evidence, exact change and expected effect
1. Custom privileged/controller scripts written outside task ownership required repeated
   corrections for backup encoding, preflight ordering, marker precedence and child cleanup.
   Classify these scripts as implementation, with ownership, phase contracts and offline
   failure-path review before handoff. This moves the checks ahead of operational execution.
2. A feature plan initially mixed implementation acceptance with later merge/CI/deployment
   proof. Separate those stages and audit actual delivery afterward, reusing unchanged
   implementation evidence. This avoids circular pre-release criteria and duplicate gates.
3. Deployment-only scoped fixtures missed a Go test that extracts the installer; the full
   gate then failed after unrelated preflight entered its extracted block. Require discovery
   of Go consumers and checked extraction boundaries in scoped deployment gates.
4. The release report did not make completed merge/deployment sufficiently explicit alongside
   account enrollment. Lead with actual delivery status and keep enrollment as a separate
   owner action, reducing follow-up ambiguity without expanding the report.

## Constraints
Follow AGENTS.md, keep prose in English and responses in Russian. Preserve the skill's modes,
provider choice, authorization boundaries, password handling, multi-account maintenance and
runtime tool guidance. Do not edit global instructions, shared skills or Claude sources.
The canonical prose edits are committed separately from their application. Their old fences
come directly from the base; implementers apply rather than retype them.

## Shared definitions
- Canonical edit helper: the `plan_edits.py` beside the canonical feature skill, provided by
  the orchestrator in dispatch. Keep its existing real location.
- Base argument for all prose apply/check commands: `05e8ed8`.
- T1 edit artifact: `plans/retro-delivery-edits/skill.md`.
- T2 edit artifact: `plans/retro-delivery-edits/server.md`.
- Validation does not execute live setup, install, upload, login, Resume or Anki writes.
- Reports stay in the existing work-report format; the new delivery guidance adds content
  within it and does not add a new template or permission step.

## Acceptance criteria
1. The canonical helper's `check` succeeds for both artifact/file pairs against the base.
   On the base these checks must fail because the specified additions are absent.
2. Skill creator `quick_validate.py .agents/skills/setup-anki-review` succeeds with PyYAML
   in an isolated uv tool environment. Base also passes; no metadata changes are intended.
3. An independent skill dry run verifies all four behaviors against realistic scenarios:
   custom scratch maintenance is planned/tested, delivery evidence is staged correctly,
   Go installer tests are discovered, and a completed deployment with pending enrollment
   is described unambiguously. Check-only mode still performs no mutations.
4. `make check` passes before each commit. Changed Go/runtime inputs are absent; no build
   or live deployment is required for this procedure-only update.
5. Commit/push/merge and CI complete; local main fast-forwards to origin/main, unrelated
   work remains untouched. Audit this completion after implementation acceptance.

## Gates
Full: `make check`.
Scoped prose: canonical helper `apply` then `check` for the owned pair.
Scoped skill metadata: skill creator quick validator via `uv run --no-project --with pyyaml`.
Save outputs and exit codes privately, associate pre-commit evidence with commits.
Implementation acceptance covers 1-4; criterion 5 is the subsequent release audit.

## Tasks
### T1: deployment consumer gates
Own only `.agents/skills/setup-anki-review/SKILL.md`.
Apply the T1 artifact with the canonical helper; check its output byte for byte against
base plus edits. Preserve all other text and frontmatter. This adds the Go consumer/
extraction boundary guidance at Test and build, without changing ordinary setup commands.
Task gates: canonical apply/check and the scoped skill metadata validation above.
Definition of done: exact prose application and passing task gates. Do not run full Gates.

### T2: maintenance contracts, release stages and reporting
Own only `.agents/skills/setup-anki-review/references/server.md`.
Apply the T2 artifact with the canonical helper; check its output byte for byte against
base plus edits. Preserve recovery safeguards and all commands. The new paragraphs precede
custom maintenance recovery checks, and the report clarifies delivery versus enrollment.
Task gates: canonical apply/check. Definition of done: exact prose application and passing
check. Do not run full Gates or any live operations.

## Seams
| Producer | Consumer | Shared definition |
|---|---|---|
| T1 | T2 | Existing skill routes server work to its server reference |
| Edit artifacts | T1,T2,validator | Exact canonical helper, base and artifact/file pairs |
| T2 | Final report | Existing report contract, explicit delivery/enrollment distinction |

## Not doing
No general instruction rewrite, native model changes, permission relaxation, new deployment
controller, server restart, credential inspection or changes to the grading provider.

## Baseline
Quick validation succeeds with isolated PyYAML. Bare system Python lacks PyYAML, so the
isolated uv invocation is the documented gate for this task. New prose checks are expected
RED on the base; full make check evidence is archived before the plan commit.
