# Prose edits for `README.md`

Base: `e07f3202ca8da8d147206bf2d0d66f05d4a63e3a`. Generated with canonical `plan_edits.py stub`; only new fences changed.

**E1.** (lines 112-114)

````md E1-old
choice in `DATA_DIR/vault.json` and logs you in. After a
restart the page shows **Log in**: until you log in, the server is locked and grading waits. Once
logged in, **Settings** changes the password, grading provider or credentials; omitted credentials keep their current value.
````

````md E1-new
choice in `DATA_DIR/vault.json` and logs you in. With `SESSION_KEY_FILE` configured, existing
browser sessions survive a restart until their original 30-day expiry. Existing accounts need
one successful password login after enabling this feature. Without recovery configured, log in
after restarting. Interrupted checking or saving always waits for an explicit **Resume** click.
Once logged in, **Settings** changes the password, grading provider or credentials; omitted credentials keep their current value.
````

**E2.** (lines 142-142)

````md E2-old
- The server keeps the current pass in `DATA_DIR`: a restart resumes it, grading or writing included.
````

````md E2-new
- The server keeps the current pass in `DATA_DIR`; a restart loads it passively. Click **Resume checking**
  or **Resume saving to Anki** to continue interrupted work.
````

**E3.** (lines 152-152)

````md E3-old
| `DATA_DIR` | `./data` | State of the current pass (it holds the answer keys) and `vault.json` (the sealed tokens). The directory is `0700`, the files `0600` |
````

````md E3-new
| `DATA_DIR` | `./data` | State of the current pass (it holds the answer keys) and `vault.json` (the sealed tokens). The directory is `0700`, the files `0600` |
| `SESSION_KEY_FILE` | empty | Absolute path to an independent 32-byte server recovery key outside the repository and `DATA_DIR`. Empty supports password-only vaults until recovery is enabled; see [key maintenance](deploy/README.md#persistent-browser-sessions) |
````

**E4.** (lines 184-186)

````md E4-old
- **Sessions.** A random cookie: `HttpOnly`, `SameSite=Strict`, 30 days, kept in memory only, so a
  restart logs everyone out. The cookie is `Secure` when the proxy sends `X-Forwarded-Proto: https`.
  Changing the password logs out the other sessions.
````

````md E4-new
- **Sessions.** A random cookie: `HttpOnly`, `SameSite=Strict`, original 30-day expiry. Only hashes
  are stored server-side. With a configured recovery key, sessions survive restart without renewal.
  Logout revokes the current session durably; changing the password revokes other sessions and
  retains the initiating session's expiry. The cookie is `Secure` when the proxy sends `X-Forwarded-Proto: https`.
````

**E5.** (lines 190-194)

````md E5-old
- **Locked after a restart.** Credentials are decrypted only after owner login; Codex call files
  are temporary, with the filesystem boundary described below. Until then nothing that needs Anki or a grader runs.
- **What it protects.** The tokens stay unreadable on a copy of the disk, in a backup or snapshot, in
  an env file, and for other users of the machine. A stolen `vault.json` costs one Argon2id run per
  password guess.
````

````md E5-new
- **Recovery after a restart.** A valid browser session can lazily recover credentials with the
  server key. Anonymous requests cannot unlock accounts. Saved passes load passively even after
  login or recovery; interrupted grading and confirmed writes require explicit **Resume**.
- **What it protects.** A data-only backup without the independent server key retains password
  protection. A stolen `vault.json` without that key costs one Argon2id run per password guess.
  Application data plus the server key decrypts credentials without the password. Protect full
  host backups that contain both with equivalent access controls and encryption.
````

**E6.** (lines 198-200)

````md E6-old
- **Forgotten password.** There is no recovery. Stop the server, delete `DATA_DIR/vault.json`, start it
  and set up again with grading credentials and the AnkiConnect key. On a server: `sudo rm /var/lib/anki-review/data/vault.json`, then
  `systemctl restart anki-review` (the restart drops the tokens still in memory).
````

````md E6-new
- **Forgotten password or lost server key.** There is no password-reset flow. Never delete a vault
  to repair a missing server key. Restore its protected key backup or follow the explicit offline
  [recovery migration](deploy/README.md#persistent-browser-sessions), then enter the existing password on the page.
````
