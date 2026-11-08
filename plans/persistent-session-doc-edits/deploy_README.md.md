# Prose edits for `deploy/README.md`

Base: `2a6e326`. Generated with canonical `plan_edits.py stub`; only new fences changed.

**E1.** (lines 41-41)

````md E1-old
   `scp -r deploy/anki deploy/systemd deploy/install.sh deploy/smoke.sh deploy/provider.sh deploy/test-provider.sh deploy/autodeploy.sh <user>@<server>:DIR/`,
````

````md E1-new
   `scp -r deploy/anki deploy/systemd deploy/install.sh deploy/smoke.sh deploy/provider.sh deploy/test-provider.sh deploy/session-key.py deploy/test-session-key.py deploy/autodeploy.sh <user>@<server>:DIR/`,
````

**E2.** (lines 230-231)

````md E2-old
A restart always locks the app until owner login; unlocking may resume a saved pass. Review the
saved-pass warning before unlocking. During Linux grading, credentials exist only in a private
````

````md E2-new
A configured recovery key lets valid browser sessions recover after restart; otherwise password
login is needed. Saved passes load passively and require explicit **Resume** for interrupted work. During Linux grading, credentials exist only in a private
````

**E3.** (lines 236-236)

````md E3-old
## Sync server: AnkiWeb or self-hosted
````

````md E3-new
## Persistent browser sessions

The installers enable recovery on first installation or upgrade only when the existing environment
has no `SESSION_KEY_FILE` entry. They generate an independent 32-byte key on the host at
`/etc/anki-review/session.key`, owned by `root:anki-review` mode `0640` in a `0750` directory.
They validate existing configured key metadata before changing the installation and never overwrite,
rotate or regenerate a missing key. Only the path is stored in `/etc/anki-review/env`.
A custom existing absolute path is retained. Keep keys outside the repository and application data;
a regular non-symlink file, exactly 32 bytes, mode `0600` or `0640`, and protected ancestors are required.
Do not display key bytes. `session-key.py` reports validation only, without key contents.

Each existing account needs one successful password login after enabling recovery. Later valid
browser cookies restore only their own account until the original 30-day expiry; restart never
extends it. Logout durably revokes the current session. Password changes revoke other sessions
while preserving the initiating session's original expiry. Every account's saved review loads
passively: status, login, recovery and polling never resume grading or confirmed writes. The owner
must click **Resume checking** or **Resume saving to Anki**; diagnostics never press those buttons.

The recovery envelope wraps the password-derived vault key and hashed session metadata.
Application data plus the server key decrypts provider credentials without the password. A data-only
backup without the server key retains password protection. Store key backups separately with
restricted access; encrypt and protect any full host backup containing both. Include the configured
key in a deliberate full recovery set, retain ownership/modes, and test restore on disposable copies.

An empty `SESSION_KEY_FILE` preserves legacy password-only operation only before any recovery
envelope exists. If recovery already exists, an empty setting fails closed too. A configured missing,
unsafe or wrong key prevents startup; never rerun an installer expecting a replacement key.

For a lost key, restore the exact key from a protected backup first. If that is impossible, an
administrator may deliberately migrate offline, revoking every browser session:

1. Stop HTTP and preserve protected backups of the entire application data, environment and any
   remaining key. In multi-account mode inspect every saved pass privately and follow the consistent
   broker/collection backup procedure below. Preserve account registry, preferences and all passes.
2. In each existing account's `vault.json`, atomically remove only the optional top-level `recovery`
   field, preserving version, login, KDF and password ciphertext byte values and private file metadata.
   Do not delete vaults, reset secrets, change providers or modify review state. No password is read
   by the administrator or tool. With HTTP stopped, removing all recovery envelopes revokes sessions.
3. To disable recovery, explicitly clear `SESSION_KEY_FILE`. To rotate or re-enable it, move the old
   key to protected backup storage, deliberately provision a fresh key with the helper's
   `--allow-create --uid 0 --gid <anki-review-group-id>` arguments and the chosen protected directory,
   and configure its absolute path. This is an intentional administrator action, never installer repair.
4. Start the matching HTTP binary and verify public status without cookies. Accounts remain
   inaccessible until their owners enter their existing passwords on the normal login page. That
   login recreates recovery when enabled. Do not claim restored access before it succeeds.
   Saved passes remain passive and need explicit **Resume**.

Use the same offline procedure for deliberate key rotation, even when the old key is available;
removing every recovery envelope revokes old browser sessions. Rotation alone does not replace
the password-derived vault key. Old data plus its old server key can expose that derived key and
may decrypt later credential reseals that keep the same KDF. If exposure is suspected, after
password login the owner must deliberately change the password through Settings to reseal with
a fresh KDF. Keep old backups protected because their old data and old key remain decryptable.

## Sync server: AnkiWeb or self-hosted
````

**E4.** (lines 292-293)

````md E4-old
- A restart locks the service: the tokens leave memory with the process. Log in on the page to unlock
  grading again. A pass in progress resumes after the login.
````

````md E4-new
- A restart clears process memory. With recovery configured, valid browser sessions recover lazily
  until their original expiry; otherwise log in on the page. Interrupted passes require **Resume**.
````

**E5.** (lines 322-323)

````md E5-old
and do not unlock any account automatically. Unlocking can resume grading or
confirmed writes. Detect multi-account mode before uploading or running setup;
````

````md E5-new
and do not unlock any account automatically. Login and session recovery load saved passes
passively; interrupted grading or confirmed writes require the owner's explicit **Resume**. Detect multi-account mode before uploading or running setup;
````

**E6.** (lines 340-340)

````md E6-old
then start the matching HTTP binary and verify public status while locked.
````

````md E6-new
then start the matching HTTP binary and verify public status without an authenticated cookie.
````

**E7.** (lines 449-450)

````md E7-old
   Stop `anki-review` for the maintenance window; every account will be locked after
   restart. Inspect all saved passes with the administrator-only helper described
````

````md E7-new
   Stop `anki-review` for the maintenance window; valid browser sessions can recover after
   restart when configured. Inspect all saved passes with the administrator-only helper described
````

**E8.** (lines 499-502)

````md E8-old
explicit decision before any account is asked to unlock: login can resume grading
or confirmed writes. Never move/delete saved passes or log users in as a diagnostic.
Restart leaves every account locked; restarting the broker alone does not authorize
review writes. Broker storage and keys must remain root-only and survive restarts.
````

````md E8-new
explicit decision before **Resume checking** or **Resume saving to Anki**. Login and session
recovery load passes passively. Never move/delete saved passes or log users in as a diagnostic.
Restart can preserve valid browser sessions; restarting the broker alone does not authorize
review writes. Broker storage and keys must remain root-only and survive restarts.
````

**E9.** (lines 540-540)

````md E9-old
   and preferences. All accounts remain locked; no diagnostic should unlock them.
````

````md E9-new
   and preferences. Accounts load passively; no diagnostic should unlock them or press **Resume**.
````

**E10.** (lines 579-581)

````md E10-old
broker and HTTP. Do not regenerate keys against restored vaults. All accounts start
locked; verify only public status before the owners decide whether to unlock saved
passes. Test recovery on disposable copies. Deleting an application vault destroys
````

````md E10-new
broker and HTTP. Do not regenerate keys against restored vaults. Verify only public status
without cookies; valid sessions may recover, while interrupted saved passes await **Resume**. Test recovery on disposable copies. Deleting an application vault destroys
````

**E11.** (lines 366-366)

````md E11-old
Each deploy locks the service until you log in on the page, like any restart.
````

````md E11-new
After deployment, valid browser sessions can recover with the configured server key until their
original expiry. Password-only mode requires login. Interrupted checking or saving waits for **Resume**.
````

**E12.** (lines 412-421)

````md E12-old
- Grading credentials and the AnkiConnect key are sealed in `/var/lib/anki-review/data/vault.json` (0600),
  under a key from Argon2id of the owner's password, with XChaCha20-Poly1305. The env file holds no
  secret. Credentials are decrypted after owner login. Codex auth snapshots also exist in temporary
  private tmpfs files during calls, and are removed afterward.
- What this does not protect: root on the host, and the `anki-review` user, can read the memory of the
  running service and so the tokens. A copy of the disk, a backup or a snapshot is protected only by
  the password. Because the AnkiConnect key is also in `DIR/anki/secrets/`, the vault mainly guards the
  grading credentials.
- No recovery for a forgotten password: `sudo rm /var/lib/anki-review/data/vault.json`, then
  `systemctl restart anki-review`, then set up again on the page (grading credentials and AnkiConnect key again).
````

````md E12-new
- Grading credentials and the AnkiConnect key are sealed in `/var/lib/anki-review/data/vault.json` (0600),
  under a key from Argon2id of the owner's password, with XChaCha20-Poly1305. With recovery enabled,
  an independent server key also wraps the password-derived key and hashed session metadata.
  Valid browser sessions can recover credentials after restart without entering the password;
  anonymous requests cannot recover them. The env file holds only the key path, never key bytes.
  Codex auth snapshots exist in private temporary tmpfs files during calls and are removed afterward.
- What this protects: a data-only backup without the independent server key retains password
  protection. Application data plus the server key decrypts credentials without the password;
  encrypt and protect full host backups that contain both. Root and the `anki-review` user can read
  live credentials. Because the AnkiConnect key also exists in `DIR/anki/secrets/`, the vault mainly
  guards grading credentials.
- There is no password-reset flow. A missing configured server key prevents startup; restore its
  protected backup or follow the explicit [offline recovery migration](#persistent-browser-sessions).
  Migration removes only recovery envelopes, preserving password ciphertext and all saved account
  data, then the owner enters their existing password on the normal page. Never delete a vault to
  repair a lost key. Login and recovery are passive; interrupted work still requires **Resume**.
````
