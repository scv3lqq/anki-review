**E1.** (lines 476-476)

````md E1-old
Before running a custom maintenance script, independently check these recovery points:
````

````md E1-new
Treat custom maintenance and controller scripts as implementation even when kept in private
scratch. For feature work, put their file ownership, phase contract and scoped gates in the
plan before writing them. Before a live or sudo handoff of mutating/coordinating scripts,
complete synthetic tests and independent review for the applicable paths they implement:
backup round trips with the exact pass-file format, phase ordering, marker precedence,
timeout and descendant cleanup even after the controller's leader exits.
For an existing deployment that separates upload and privileged execution, upload follows
private saved-pass checks and backups. Publish readiness only after helper success, budget upload plus cleanup below
the remaining privileged wait, and stop owned writers before abort/rollback.

For a feature that includes delivery, validate implementation before publication; keep
merge, CI and live deployment as subsequent completion checks. Audit their actual results
afterward without repeating full gates solely for release evidence on unchanged reviewed files.

Before running a custom maintenance script, independently check these recovery points:
````

**E2.** (lines 490-493)

````md E2-old
## Report

Each step as done, skipped (already there) or failed; what the user still has to do (the setup
page, the login after a restart); the URL.
````

````md E2-new
## Report

When merge and deployment are in scope, lead with their explicit completed or pending status,
the source commit and CI result. Report one-time account enrollment separately from completed
deployment. Then summarize steps as done, skipped (already there) or failed, any remaining
owner actions and the URL, using the report format required by AGENTS.md.
````
