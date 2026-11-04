# Exact prose edits: .claude/skills/setup-anki-review/references/server.md

Base: `e576a83`. Apply/check with canonical plan_edits helper.

**E1.** (lines 219-222)

````md E1-old
## Report

Each step as done, skipped (already there) or failed; what the user still has to do (the setup
page, the login after a restart); the URL.
````

````md E1-new
## Multi-account maintenance

When `ACCOUNT_SOCKET` is enabled, follow the multi-account section of deploy/README.md.
The ordinary wait-setup/restart/status assumptions do not apply: unauthenticated
status intentionally omits every account's unlocked/stage. Helpers must detect and
report multi-account mode, refuse automated restart and stop waiting for public unlock.
Auto-deploy remains held. Build and transfer both Linux binaries, the broker unit,
installer and check-passes.py; administrator installation updates the broker only.

Do not change the owner's collection, endpoint, credentials or pass during migration.
Guests start empty and no-sync, and import compatible packages through their own page.
Use explicit dedicated credentials per account; never reuse host Claude login or a
Codex device snapshot. No setup/verification call should register a real account or
import into the existing collection. Maintenance first stops HTTP, then inspects every
saved account pass privately with check-passes.py. Keep backups and obtain the owner's
decision about checking/confirmed states before any account is asked to unlock.
Root-only broker metadata, keys, quota backing filesystems and all guest app data
are part of backup/restore; do not run broad resource cleanup.

## Report

Each step as done, skipped (already there) or failed; what the user still has to do (the setup
page, the login after a restart); the URL.
````
