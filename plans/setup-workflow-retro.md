# Setup workflow retrospective

Base: `59d7a7d0c1b340df2c76321158d485addef3ffaf`. `$PLAN_EDITS` is the canonical prose helper supplied in task briefs.

## Goal and evidence
Apply four demonstrated workflow improvements without changing grading, credentials, collections, native model settings or live services. Deployment fixtures were outside the standard check gate; native VNC paste required repeated retries; an open sync database and tmpfs archive access defeated naive backups; retry status files and nested shell quoting failed. Agent follow-up guidance already exists and needs no duplicate rule.

## Shared definitions and constraints
Only Codex setup guidance changes; preserve independent Claude configuration. No private identities, hosts, transcript excerpts, credentials or live data in artifacts. No live VPS operations. Canonical prose edits are in the two companion edit plans and must be applied/checked against the base. Keep unrelated dotfiles changes untouched.

## Acceptance criteria and Gates
- make check passes and now includes offline deployment fixtures and shell syntax.
- make check-deploy passes, including the official Codex package fixture.
- make -n check includes the deployment fixture command; CI invokes make check-deploy.
- Both canonical prose edit checks and git diff --check BASE pass.
- Independent dry-run compiles the embedded clipboard script and exercises it against fake Qt modules with dummy stdin only: ready output omits secret, ownership-aware cleanup, bounded lifecycle and signal cleanup.
- Scratch mutation codex-resources -> resources in deploy/provider.sh fails the new check-deploy target; no live installer or grading calls.

## Tasks
### T1: Standard deployment gate
Own Makefile, .github/workflows/ci.yml, AGENTS.md. No dependencies. Add phony check-deploy; make check depends on lint, test and check-deploy. Its commands: a fail-fast loop over deploy/*.sh, deploy/anki/*.sh and both setup skill script globs, calling bash -n on each quoted file separately; then bash deploy/test-provider.sh. A single bash -n with multiple filenames checks only the first. Use $$script in the Makefile recipe. CI test job invokes make check-deploy after go test -race and before lint. Apply the exact AGENTS prose companion plan. Baseline make check-deploy exits2 because target is absent; preserve that RED log. Scoped gates make check-deploy, make -n check, canonical AGENTS check, diff check. Mutation commands: in isolated scratch change codex-resources to resources in provider helper, then make check-deploy must exit nonzero; restore and append invalid shell syntax to the last .claude setup server.sh, then the same gate must also fail. No full make check or commits by implementer.
### T2: Operational procedures
Own .agents/skills/setup-anki-review/references/server.md only. No dependencies. Apply its exact companion prose plan; this adds reliable login handoff, clipboard fallback and bounded consistent sync backups. Preserve all existing safety/confirmation rules and helper target paths. Scoped gates canonical prose check, extract and compile Python clipboard block (never execute live commands), diff check. No live tools, credentials, installs or commits.

## Seams
T1 standard gate -> existing deploy fixtures: shell-only, offline and no grader quota.
T2 clipboard -> pinned container: DISPLAY from container env and Qt imports from /opt/anki/app_packages.
T2 status handoff -> current setup modes: check remains read-only and quota-free, owner enters secrets.

## Not doing
No changes to native roles/models or global guidance; no unrelated dotfiles edits; no new clipboard daemon, auth import API, Anki writes, collections, VPS restarts or deployment.

## Workflow
Independent plan dry-run before implementation, two disjoint implementers, main full make check before scoped signed commit, independent review/dry-run and final acceptance. Native custom roles were unavailable earlier in this runtime; equivalent separate default agents preserve approved instructions and gates. Keep command evidence privately. At most five fix iterations and stop on a finding repeated three times.

## Exact final gates
```bash
make check
make check-deploy
make -n check
python3 "$PLAN_EDITS" check plans/setup-retro-agents-edits.md AGENTS.md 59d7a7d0c1b340df2c76321158d485addef3ffaf
python3 "$PLAN_EDITS" check plans/setup-retro-server-edits.md .agents/skills/setup-anki-review/references/server.md 59d7a7d0c1b340df2c76321158d485addef3ffaf
git diff --check 59d7a7d0c1b340df2c76321158d485addef3ffaf
```
