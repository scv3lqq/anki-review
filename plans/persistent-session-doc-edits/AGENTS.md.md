# Prose edits for `AGENTS.md`

Base: `e07f3202ca8da8d147206bf2d0d66f05d4a63e3a`. Generated with canonical `plan_edits.py stub`; only new fences changed.

**E1.** (lines 61-64)

````md E1-old
skill's helpers for Anki checks and secret transfer. A restarted server is locked;
unlocking it can resume a saved pass, including grading or confirmed-grade writes.
Follow the skill's saved-pass checks before restarting or asking the owner to
unlock, and preserve its backup and sync-direction checks when moving collections.
````

````md E1-new
skill's helpers for Anki checks and secret transfer. Configured recovery preserves valid
browser sessions after restart until their original expiry; existing accounts need one
password login after enabling it. Login and recovery load saved passes passively;
interrupted grading or confirmed writes require explicit Resume. Diagnostics never
resume them. Follow the skill's saved-pass checks and preserve its key backup and
sync-direction checks when moving collections.
````
