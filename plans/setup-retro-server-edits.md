**E1.** (lines 256-260)

````md E1-old

## Report

Each step as done, skipped (already there) or failed; what the user still has to do (the setup
page, the login after a restart); the URL.
````

````md E1-new

## Reliable interactive handoffs

Before a device-login retry, check the previous operation's metadata and cancel only that
owned login. After enabling device authentication, start a fresh login and have the owner
use the new code. Never capture the terminal while a code or credential may be visible.
Track the root pane, tunnels, helper process IDs and staging paths separately. Observed
completion advances the workflow; a late answer to an earlier question does not restart it.

Keep status metadata separate from credential files: only state, exit code and owned paths.
Write retry status atomically to a newly created 0600 file, set its final owner, then use
`os.replace`. Do not truncate a user-owned file in sticky `/tmp` from a root process:
Linux protected-regular rules can reject this even for root. For multiline remote Python,
prefer a named static script and compile it before transfer, or build argv with
`shlex.join`; do not nest shell quotes or turn literal `\n` into executable Python.

## Sync login and VNC clipboard

`Saved to Anki` followed by `sync: auth not configured` means the grades are already written
and the Anki profile lacks sync authentication. Do not press Save to Anki again. Use the
existing pass and backup checks before repairing login; never submit API writes as a probe.
The self-hosted account is `anki`, not the owner's app login or email. The internal URL
`http://sync:8766/` belongs in remote Anki Preferences, Syncing; it is not a browser URL on
the owner's computer. Check the endpoint before changing it. Report credential comparisons
only as booleans, never values, hashes, process environments or auth snapshots.

Try `S sync-password` and Screen Sharing's Edit, Use Shared Clipboard, then Ctrl+V inside
Linux Anki. If sharing fails, place the password directly into the container's X11 clipboard
with a temporary Qt process; the owner still focuses the password field and pastes it.
Anki's standalone Python does not automatically include its bundled Qt packages: the pinned
image needs `/opt/anki/app_packages` on `sys.path`. Run this static script through an owned
process session in `anki-desktop`, using `/opt/anki/python/bin/python3 -I`. Send the existing
sync password file through stdin on the VPS; its value must never enter argv or output.

```python
import os
import signal
import sys
from types import FrameType


def main() -> None:
    # The bundled GUI packages are separate from the standalone Python runtime.
    sys.path.insert(0, "/opt/anki/app_packages")
    from PyQt6.QtCore import QTimer
    from PyQt6.QtGui import QClipboard
    from PyQt6.QtWidgets import QApplication

    app = QApplication([])
    clipboard = app.clipboard()
    password = sys.stdin.buffer.read(256).strip()
    if len(password) != 24 or not password.isalnum():
        raise SystemExit("Invalid sync password input")
    clipboard.setText(password.decode("ascii"), QClipboard.Mode.Clipboard)

    def cleanup() -> None:
        if clipboard.ownsClipboard():
            clipboard.clear(QClipboard.Mode.Clipboard)

    def stop(_signal: int, _frame: FrameType | None) -> None:
        app.quit()

    def heartbeat() -> None:
        pass

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    timer = QTimer()
    timer.timeout.connect(heartbeat)
    timer.start(200)
    app.aboutToQuit.connect(cleanup)
    QTimer.singleShot(300_000, app.quit)
    print(f"clipboard_ready pid={os.getpid()}", flush=True)
    app.exec()


main()
```

Pass only the static script as `-c` or as a named script file; the secret is the redirected
stdin of `docker exec -i`, not the script text. Execute the redirection on the VPS, where
`DIR/anki/secrets/sync-password` exists. Wait for `clipboard_ready` before asking the owner
to paste. Tell them to use login `anki`, clear the password field, then use Ctrl+V or its
right-click Paste menu. Copying a URL afterward can overwrite the password through shared
clipboard. On success stop only the reported helper PID and run `S clip-clear`. The process
also exits after five minutes and clears the clipboard only if it still owns that selection.

## Consistent backups before sync repair

Back up both collections before login can cause a full sync. Use SQLite's backup API for
an open client collection and preferences; bound busy retries with a progress callback and
a deadline rather than waiting indefinitely. A running sync server may hold an exclusive
lock. If that prevents an online backup, briefly pause only `anki-sync`, copy its database
and any existing `-wal` and rollback `-journal` sidecars while frozen, and unpause in a
guaranteed cleanup path. Resume the server before inspecting the copied database. Never
copy a changing live database without its journals. Recover and create canonical snapshots
through SQLite from the writable private copy, not from live files.

Container tmpfs may be visible to `docker exec` while `docker cp` cannot archive it. Stream
only the task-owned backup directory with `docker exec ... tar -cf -` into a root-only host
backup directory, with pipe failure handling. Remove only that named temporary directory.
Anki uses the custom SQLite collation `unicase`: generic Python may reject a whole-database
`quick_check` without it. A table-scoped `quick_check(revlog)` and comparison of review IDs
can verify the review transfer without changing a live database or rebuilding indexes.

A forced Upload/Download prompt must first be cancelled. Compare client-only and server-only
review IDs in the snapshots and check for other collection changes before choosing a
direction. New client reviews must not be overwritten by Download; Upload can overwrite
changes from other devices. After ordinary sync, a read-only snapshot comparison can prove
that the server contains the pending client reviews. Print only counts and safe booleans.

## Report

Each step as done, skipped (already there) or failed; what the user still has to do (the setup
page, the login after a restart); the URL.
````
