**E1.** (lines 88-89)

````md E1-old
- Outside tmux, `S window` drives Terminal through AppleScript; the first time macOS may ask
  whether the current terminal app may control Terminal: the user clicks OK.
````

````md E1-new
- Before a sudo handoff, check whether current instructions explicitly authorize an
  existing credential source such as a named `gopass` entry. That authorization permits
  using that source for the authorized operation only; it does not expand maintenance
  permissions, enable writes in `check` mode, or authorize owner/grader credential access.
  Discover only relevant entry names, never display their values. Prefer an already
  authorized source to asking for manual entry again.
- For authorized stored sudo credentials, finish non-streaming SSH probes first with
  stdin redirected from `/dev/null`. Stage the static maintenance script before piping
  the password directly from the store to `sudo -S -p ''` over non-PTY SSH. Keep shell
  tracing off and never put the value in argv, logs, chat, `.env`, a temporary file or
  the clipboard. Preserve the configured SSH target/port, quoting and saved-pass/mode
  checks when adapting the helper transport; a sudo route never bypasses those guards.
- Without an authorized stored source, use a visible terminal for manual sudo. In tmux,
  inspect the current session and `tmux list-clients -F '#{client_session}'` before
  prompting. `S window` can create its task window in the agent's detached session.
  Move only the newly created task window into the intended attached session and select
  it before telling the owner to type. If the intended client is ambiguous, establish
  the intended terminal first; do not guess or wait on an invisible prompt.
- Outside tmux, `S window` drives Terminal through AppleScript; the first time macOS may ask
  whether the current terminal app may control Terminal: the user clicks OK.
````

**E2.** (lines 151-154)

````md E2-old
   `S install` opens a new window with
   `ssh -t ... sudo env KNOWLEDGE_DECKS=... DIR/install.sh DIR` (absolute paths, quoted for the
   host) and prints an exit file. Tell the user to type their sudo password there, run
   and poll `S wait-install <file>` in a process session. Inspect its printed
````

````md E2-new
   Choose the credential route above before opening an interactive window. With an
   explicitly authorized stored sudo source, run the same checked installer operation
   through a tracked non-PTY SSH session and require its actual exit code. Otherwise,
   `S install` opens a window with
   `ssh -t ... sudo env KNOWLEDGE_DECKS=... DIR/install.sh DIR` (absolute paths, quoted for
   the host) and prints an exit file. Verify that window is visible before asking for
   manual sudo; run and poll `S wait-install <file>` in a process session. Inspect its printed
````

**E3.** (lines 231-235)

````md E3-old
4. `S upload-bin`, then `S restart`. Refused (an older deploy without the rule, or no polkit): `S
   sudo-restart` opens a window for the sudo password; run and poll `S wait-install <file>` in a
   process session. Inspect the printed `window ended, exit code N`; the helper's own exit code
   0 only means the wait completed. A nonzero printed `N` means the restart failed: follow the
   install-window diagnostic flow in mode `server`, step 7, before retrying within scope.
````

````md E3-new
4. `S upload-bin`, then `S restart`. Refused (an older deploy without the rule, or no
   polkit): follow the credential route above. For manual sudo, `S sudo-restart` opens
   a window; verify its visibility, then run and poll `S wait-install <file>` in a
   process session. Inspect the printed `window ended, exit code N`; the helper's own
   exit code 0 only means the wait completed. With an authorized stored source, require
   the actual tracked SSH/sudo exit code instead. A nonzero operation result means the
   restart failed: follow the install-window diagnostic flow in mode `server`, step 7,
   before retrying within scope.
````

**E4.** (lines 443-444)

````md E4-old
Root-only broker metadata, keys, quota backing filesystems and all guest app data
are part of backup/restore; do not run broad resource cleanup.
````

````md E4-new
Root-only broker metadata, keys, quota backing filesystems and all guest app data
are part of backup/restore; do not run broad resource cleanup.

Before running a custom maintenance script, independently check these recovery points:

- Rollback restores both installed binaries and their deploy-kit staging copies.
  The HTTP unit's `ExecStartPre` installs `DIR/bin/anki-review` on every start; restoring
  only `/usr/local/bin/anki-review` is undone by the next start. Keep the matching broker
  configuration and unit, restore them on failure, and verify both restored binaries.
- An active broker unit is not proof that asynchronous guest reconciliation succeeded.
  Before reporting recovery, wait within a finite bound for every saved reservation's
  expected ext4 UUID, exact loop backing file, matching container labels and running
  container. These are read-only checks; zero reservations is a valid empty set.
- Require the real process exit code and the final postcondition marker. Any non-secret
  stage/result file consumed by the deploy user must be readable by that user, while
  backup keys, vaults and auth remain administrator-only.
````
