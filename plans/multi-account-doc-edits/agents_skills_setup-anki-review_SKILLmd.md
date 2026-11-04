# Exact prose edits: .agents/skills/setup-anki-review/SKILL.md

Base: `e576a83`. Apply/check with canonical plan_edits helper.

**E1.** (lines 247-249)

````md E1-old
lines, add-on, autostart file); the URL and how the server runs (this session, autostart, already
running, not running); the owner (set up or not, unlocked or locked); what is left to the user, with
the fix from the table.
````

````md E1-new
lines, add-on, autostart file); the URL and how the server runs (this session, autostart, already
running, not running); the owner (set up or not, unlocked or locked); what is left to the user, with
the fix from the table.

## Multi-account deployments

Use [deploy/README.md](../../../deploy/README.md#multi-account-deployment) for the
opt-in broker, limits, migration, updates and backups. Check mode remains read-only.
A multi-account `/api/session` deliberately hides unlocked/provider/stage from
anonymous requests; report multi-account mode, never infer that all accounts are idle
or wait for public unlock data. Do not use single-owner automated restart/wait-setup
flows. Auto-deploy holds; update both binaries and the separate root broker explicitly.

Before HTTP maintenance, stop HTTP and run the private administrator stage-summary
helper for every account, preserving backups and saved passes. Ask the owner how to
handle checking/confirmed passes before requesting any unlock. Never log in, import
packages, grade or save confirmed grades as a setup diagnostic. The owner and invited
users enter their own explicit grader credentials through the page; do not read them.
Provision guests as empty, isolated, no-sync runtimes, without AnkiWeb/VNC setup or
manual AnkiConnect key transfer. Installation does not grant Docker access to HTTP.
````
