# Exact prose edits: deploy/README.md

Base: `e576a83`. Apply/check with canonical plan_edits helper.

**E1.** (lines 305-306)

````md E1-old
- No recovery for a forgotten password: `sudo rm /var/lib/anki-review/data/vault.json`, then
  `systemctl restart anki-review`, then set up again on the page (grading credentials and AnkiConnect key again).
````

````md E1-new
- No recovery for a forgotten password: `sudo rm /var/lib/anki-review/data/vault.json`, then
  `systemctl restart anki-review`, then set up again on the page (grading credentials and AnkiConnect key again).

## Multi-account deployment

This opt-in Linux mode preserves the existing owner's `data/vault.json`,
`data/state.json`, Anki endpoint and collection volume. It adds a private account
registry and `data/accounts/<opaque-id>/` for guest vaults, passes and preferences.
The page and all feedback remain English. Do not copy or rename the owner's volume.

A separate root-owned `anki-review-provisioner` controls Docker through a group-only
Unix socket. The public `anki-review` process keeps its existing system user and
hardening, without Docker membership, socket mounts or sudo. The broker accepts only
generated account IDs. It chooses endpoints, random keys, names and container options
from administrator settings; users never enter AnkiConnect infrastructure details.

Each guest uses the pinned Anki image, a separate network and fixed-size ext4 backing
filesystem, a private key and a fresh empty profile. No VNC is published and no sync
login is required. Limits per runtime: 2 GiB RAM/swap, one CPU, 1024 PIDs and 2 GiB
storage by default, plus bounded tmpfs and 256 MiB shared memory. Reserve at least
2.5 GiB additional RAM and 2 GiB disk per guest, plus host and grader headroom; three
guests require at least 7.5 GiB extra RAM. Sparse backing files still need free host
disk as they grow. Hard filesystem capacity prevents a guest exhausting unbounded
storage. Linux loop devices, ext4, mount/losetup/mkfs tools and root are required;
provisioning fails closed if quota mounting or resource ownership cannot be proved.

### Enable invitations on an existing deployment

1. Follow the setup skill's saved-pass checks before stopping or restarting HTTP.
   Stop `anki-review` for the maintenance window; every account will be locked after
   restart. Inspect all saved passes with the administrator-only helper described
   below. Preserve an application/collection backup before changing configuration.
   No verification step should unlock an account, grade cards or write confirmed grades.
2. Build and transfer `make build-linux build-provisioner-linux`, the deploy kit,
   `install-multi-account.sh`, its systemd unit and both Linux binaries. Keep the
   ordinary owner image installed through `anki/up.sh`; do not run sync setup for guests.
3. As root run `DIR/install-multi-account.sh anki-review`. Set `ANKI_UID` and
   `ANKI_GID` to the user IDs built into the Anki image when they differ from 1000.
   `MAX_GUESTS=3` and `STORAGE_BYTES=2147483648` are the defaults. The installer
   installs/starts only the broker and does not restart or unlock the public service.
4. In `/etc/anki-review/env` add
   `ACCOUNT_SOCKET=/run/anki-review-provisioner/control.sock` and `MAX_ACCOUNTS=4`.
   Keep this total at one plus the broker's guest cap; supported total range is 2 to 8.
   Keep existing owner settings and secrets unchanged. Do not put keys/tokens in env.
5. Stage the new HTTP binary and start `anki-review` using the normal maintenance
   procedure. Verify anonymous `/api/session` reports `multi_account:true` and login
   state without another account's provider/stage. The owner enters their password
   and, if needed, their own explicit Claude token or Codex session on the page.
   Empty Claude credentials no longer use the host login in this mode.
6. The owner creates an invitation in Settings and sends the fragment link privately.
   It expires in 24 hours and is single-use. The friend registers, supplies their own
   grader credential and imports a compatible `.apkg` using **Import cards**. Export
   with **Support older Anki versions** enabled; limits are 8 MiB compressed,
   256 MiB expanded and 4096 entries. Finish/reset any active batch before import.
   No AnkiWeb login, VNC interaction, endpoint or key copy is needed for the friend.

The broker reserves IDs durably and checks its own guest cap independently of HTTP.
Registration failures reuse the reservation; an existing sealed vault is never
replaced on retry. If the response was lost after success, log in with the chosen
credentials. Cleanup failures retain a bounded pending record for retry. Inspect
broker logs by operation/account ID, never by printing key or credential files.

### Maintenance, updates and rollback

Auto-deploy holds in multi-account mode, including commits that change only the
provisioner. Setup helpers refuse automated restarts and do not wait for a publicly
visible unlocked account. Update both binaries explicitly: build, transfer the kit,
read the existing root-only `/etc/anki-review-provisioner.conf` and preserve all four
administrator values (ANKI_UID, ANKI_GID, MAX_GUESTS and STORAGE_BYTES). Repeat them
explicitly with `sudo env ANKI_UID=<saved-uid> ANKI_GID=<saved-gid>
MAX_GUESTS=<saved-cap> STORAGE_BYTES=<saved-size> DIR/install-multi-account.sh anki-review`
on one shell line. The installer retains saved values when overrides are omitted;
changing an existing quota is refused and changing image IDs can prevent startup. This updates
only the privileged daemon. Then perform the HTTP maintenance restart. Preserve
prior binaries and the matching daemon configuration for rollback.

After stopping HTTP, an administrator runs `python3 DIR/check-passes.py
/var/lib/anki-review/data` locally on the server. It reports stages/counts only,
including all guest states. An active checking/confirmed pass needs the owner's
explicit decision before any account is asked to unlock: login can resume grading
or confirmed writes. Never move/delete saved passes or log users in as a diagnostic.
Restart leaves every account locked; restarting the broker alone does not authorize
review writes. Broker storage and keys must remain root-only and survive restarts.

Rollback: stop HTTP and the broker, gracefully stop the explicitly selected guest
containers whose labels match their reservation IDs and
`io.anki-review.managed=provisioner-v1`, and retain their storage. Restore the matching
previous binaries/configuration and remove `ACCOUNT_SOCKET` from HTTP to return
to single-account operation. Keep all guest registry/directories and backing storage
for recovery. The owner remains on the original endpoint/volume. Never restore a
guest collection over the owner's volume, and never use broad Docker prune/volume rm.

### Account-aware backups and restore

For a consistent offline backup, stop HTTP first, then the broker, and gracefully
stop only the relevant labelled Anki containers. The existing owner may have a
separate sync container: preserve its documented backup/sync direction checks.
Archive the entire application DATA_DIR, including `accounts.json` and each account's
vault/state/preferences. For every broker-owned opaque ID, the complete recovery set
is `/var/lib/anki-review-provisioner/<id>/reservation.json`, `filesystem.json`,
`storage.img`, `ankiconnect_key` and `vnc_password`, plus the saved daemon configuration.
The collection and media live inside storage.img, mounted at `<id>/mount`.

Before copying storage.img, keep HTTP/broker and that guest container stopped. Verify
that findmnt reports the expected ext4 UUID from filesystem.json at the exact mount
path, and that losetup reports storage.img as the exact backing file for its source
loop device. Unmount only that verified `<id>/mount`, then detach only its verified
loop device if it remains attached. Abort if either identity check or unmount fails.
Archive the complete per-ID directory only after it is unmounted and detached; this
preserves the filesystem UUID, SQLite journals and media together. Do not call the
broker DELETE operation for backup: it deletes the collection. Leave containers
stopped until the broker verifies/remounts their quota filesystem before starting them.

Use encrypted root-only backup storage; keys remain sensitive even though application
vaults are encrypted. Never copy a changing SQLite database or a mounted backing
image. An alternative online backup needs SQLite's backup API, consistent media and
a separately tested logical restore procedure; it is not interchangeable with this
offline filesystem-image recovery set.

Restore each complete per-ID directory to its original absolute broker root and
restore matching daemon UID/GID/quota settings. Preserve file ownership, private
permissions, filesystem UUID and original storage.img backing path before starting the
broker and HTTP. Do not regenerate keys against restored vaults. All accounts start
locked; verify only public status before the owners decide whether to unlock saved
passes. Test recovery on disposable copies. Deleting an application vault destroys
access to its encrypted grader session; there is no password recovery. Deprovision
only explicitly selected broker-labelled guest resources after backup, never the
reserved owner account or another guest's mount. The broker's Remove operation
is for uncommitted provisioning cleanup and must not bypass a sealed account vault.
````
