"""Apply the native AV adapter only to the exact pinned AnkiConnect services."""

from __future__ import annotations

import hashlib
import os
import pathlib
import stat
import sys
import tempfile

PINNED_SERVICES_SHA256 = (
    "3c1132b842f11c30f3fc20f49ea9ba49c67921d8934aac43fd49c1e058d27fd7"
)
IMPORT = "from .protocol import ApiError, error_record, timestamp\n"
PATCHED_IMPORT = IMPORT + "from .native_audio import render_card\n"
BODY = "    def cards_render(self, id):\n        card = self.card(id)\n        return {'cardId': id, 'questionHtml': card.question(), 'answerHtml': card.answer(), 'css': card.note_type()['css']}\n"
PATCHED_BODY = (
    "    def cards_render(self, id):\n        return render_card(self.card(id))\n"
)


def patch_source(source: str, expected_sha256: str = PINNED_SERVICES_SHA256) -> str:
    original = source
    if source.count(PATCHED_IMPORT) == 1 and source.count(PATCHED_BODY) == 1:
        original = source.replace(PATCHED_IMPORT, IMPORT, 1).replace(
            PATCHED_BODY, BODY, 1
        )
    if hashlib.sha256(original.encode("utf-8")).hexdigest() != expected_sha256:
        raise ValueError(
            "AnkiConnect services source does not match the pinned version"
        )
    if original.count(IMPORT) != 1 or original.count(BODY) != 1:
        raise ValueError("AnkiConnect services patch anchors are not unique")
    patched = original.replace(IMPORT, PATCHED_IMPORT, 1).replace(BODY, PATCHED_BODY, 1)
    if source not in {original, patched}:
        raise ValueError("AnkiConnect services source is not an exact supported result")
    return patched


def regular_file(path: pathlib.Path) -> os.stat_result:
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
        raise ValueError("AnkiConnect patch requires regular files")
    return info


def atomic_write(path: pathlib.Path, raw: bytes, mode: int) -> None:
    descriptor, temporary = tempfile.mkstemp(prefix=".native-audio-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as handle:
            os.fchmod(handle.fileno(), mode)
            handle.write(raw)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    finally:
        pathlib.Path(temporary).unlink(missing_ok=True)


def patch_plugin(plugin: pathlib.Path) -> None:
    if not plugin.is_dir() or plugin.is_symlink():
        raise ValueError("AnkiConnect plugin must be a directory")
    services = plugin / "services.py"
    info = regular_file(services)
    patched = patch_source(services.read_text(encoding="utf-8"))
    owned = pathlib.Path(__file__).resolve().with_name("native_audio.py")
    regular_file(owned)
    native = plugin / "native_audio.py"
    if native.exists() or native.is_symlink():
        regular_file(native)
    # Publish the dependency first, after every version/source/anchor guard succeeded.
    atomic_write(native, owned.read_bytes(), 0o644)
    atomic_write(services, patched.encode("utf-8"), stat.S_IMODE(info.st_mode))


def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit("usage: patch_ankiconnect.py PLUGIN_DIR")
    try:
        patch_plugin(pathlib.Path(sys.argv[1]))
    except (OSError, UnicodeError, ValueError) as error:
        raise SystemExit(
            "AnkiConnect native audio patch refused: " + type(error).__name__
        ) from None


if __name__ == "__main__":
    main()
