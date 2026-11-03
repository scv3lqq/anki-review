# Setup workflow session retrospective

## Goal and evidence

Improve the Codex server procedure without changing application behavior or permissions.
Four demonstrated improvements: define runtime evidence before long experiments; protect
SSH stdin and prove completion; verify durable sync authentication; route unavailable
shared clipboard controls to the existing X11 helper instead of local-note transcription.

## Acceptance and Gates

- The reference equals base plus the five verbatim edits below: run the task check.
- Independent dry run reproduces the stdin failure/success offline, verifies the runtime
  contract and follows persisted/memory-only/ambiguous-profile and disabled-clipboard paths.
- `make check` and `git diff 853eba7 --check` pass; archive output in private Git scratch.

## Shared definitions and constraints

Base: `853eba7c3696` (resolve with Git). Source: `.agents/skills/setup-anki-review/references/server.md`.
`PLAN_EDITS` is the canonical prose editor supplied by the orchestrator outside this repository.
No SSH, Docker, GUI, credentials, live sync, collection or permission changes for this task.
Preserve the separate Claude skill and all concurrent dotfiles edits. No model or role changes.

## T1: Apply the verbatim procedure edits

Owner: implementer. Files: the source reference only. Dependencies: plan dry run passes.
Apply `python3 "$PLAN_EDITS" apply plans/setup-session-retro.md "$SOURCE" 853eba7`.
Task gates: the same command with `check`, `make check-deploy`, `git diff --check`.
Definition of done: all exact edits and task gates pass; no extra edits or commit.
The orchestrator owns this plan. Verbatim output needs no separate code-review task;
the independent validator checks the full diff and workflow behavior.

## Seams, edge cases and non-goals

One task, no producer/consumer seams. Preserve intentional stdin streams, `check` mode,
saved-pass restart restrictions, backup and sync-direction rules, and owner secret entry.
Handle persisted versus memory-only auth, ambiguous profiles, unavailable shared clipboard
and denied GUI access without weakening permissions. Do not add autologin or diagnostics APIs.

## Verbatim edits

**E1.** (lines 12-17)

````md E1-old
Run helpers through Codex's available shell tools under the current session's permissions.
For a long operation, use `exec_command` with `tty: true` and a short `yield_time_ms`
(for example, 1000). Record the returned `session_id` together with its command and purpose.
Poll that session with `write_stdin`, empty `chars`, and waits of at most 30 seconds; handle
progress and user input between polls. For finite commands, a pending session is not a successful
exit: check the final exit code and the helper's reported result before dependent steps.
````

````md E1-new
Run helpers through Codex's available shell tools under the current session's permissions.
For a long operation, use `exec_command` with `tty: true` and a short `yield_time_ms`
(for example, 1000). Record the returned `session_id` together with its command and purpose.
Poll that session with `write_stdin`, empty `chars`, and waits of at most 30 seconds; handle
progress and user input between polls. For finite commands, a pending session is not a successful
exit: check the final exit code and the helper's reported result before dependent steps.

If a local script later streams input to SSH, redirect preliminary non-streaming SSH or
helper probes with `</dev/null`; they can otherwise consume the pending script. Preserve
stdin for intended script, archive and secret streams. For a multi-step remote backup or
repair, require exit zero, expected artifacts and a unique terminal completion marker
emitted after all postconditions pass. EOF alone can exit zero without doing the work.
````

**E2.** (lines 34-34)

````md E2-old
## Grading provider
````

````md E2-new
## Runtime investigations

Outside `check` mode, before an authorized isolated runtime experiment, record a compact
contract in its brief: identical synthetic workload, image and resource limits, exact flags,
completion/failure signals, memory metric, required API/VNC checks, isolation and cleanup.
For WebEngine include renderer-termination signals, not just process exit or OOM status.
Compare short runs first; extend only the selected candidate. Freeze the contract before
expensive runs, revising it only when new evidence changes the question. Identify diagnostic
flags and validate the exact shipping flags separately.

## Grading provider
````

**E3.** (lines 99-99)

````md E3-old
5. Sync login, only when N is 0 or 1 (an empty profile):
````

````md E3-new
5. Sync login, only when N is 0 or 1 (an empty profile):
   Before closing either login tunnel, complete the persisted-auth check in
   [Sync login and VNC clipboard](#sync-login-and-vnc-clipboard).
````

**E4.** (lines 118-121)

````md E4-old
     - Then `S sync-password` and tell the user: "In Screen Sharing turn on Edit, Use Shared Clipboard.
       Log in as `anki` and paste the password with Ctrl+V (inside the session it is Ctrl, not Cmd; if
       it still won't paste, paste it into a local note and type it). Choose **Download**: the server
       has your collection. I watch for the decks."
````

````md E4-new
     - `S sync-password`, then follow [Sync login and VNC clipboard](#sync-login-and-vnc-clipboard).
       If shared-clipboard controls are unavailable, use its temporary X11 helper before the handoff.
       Tell the user: "Log in as `anki` and paste with Ctrl+V or the password field's Paste menu.
       Choose **Download**: the server has your collection. I watch for the decks."
````

**E5.** (lines 274-280)

````md E5-old
`Saved to Anki` followed by `sync: auth not configured` means the grades are already written
and the Anki profile lacks sync authentication. Do not press Save to Anki again. Use the
existing pass and backup checks before repairing login; never submit API writes as a probe.
The self-hosted account is `anki`, not the owner's app login or email. The internal URL
`http://sync:8766/` belongs in remote Anki Preferences, Syncing; it is not a browser URL on
the owner's computer. Check the endpoint before changing it. Report credential comparisons
only as booleans, never values, hashes, process environments or auth snapshots.
````

````md E5-new
`Saved to Anki` followed by `sync: auth not configured` means the grades are already written
and the Anki profile lacks sync authentication. Do not press Save to Anki again. Use the
existing pass and backup checks before repairing login; never submit API writes as a probe.
The self-hosted account is `anki`, not the owner's app login or email. The internal URL
`http://sync:8766/` belongs in remote Anki Preferences, Syncing; it is not a browser URL on
the owner's computer. Check the endpoint before changing it. Report credential comparisons
only as booleans, never values, hashes, process environments or auth snapshots.

After GUI sync login, verify the active profile's saved `syncKey` presence in `prefs21.db`
using read-only SQLite and restricted pickle decoding that rejects globals; output only a
boolean. Do not guess the active profile when it is ambiguous. A successful sync may leave
authentication only in memory. If unsaved, use the pinned version's native profile-save path,
such as accepting Preferences or a clean shutdown, then verify again. Any restart still
follows the saved-pass and backup rules. Prefer native persistence before adding autologin
code; one missing stored key does not establish a need for repeated manual login.
````
