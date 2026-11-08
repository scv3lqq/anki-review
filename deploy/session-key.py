"""Provision the server session key without replacing existing key material."""

from __future__ import annotations

import argparse
import os
import stat
import sys
import tempfile
from pathlib import Path


def protected_directory(path: Path, uid: int, *, key_directory: bool = False) -> None:
    metadata = path.lstat()
    if not stat.S_ISDIR(metadata.st_mode) or metadata.st_uid not in (0, uid):
        raise ValueError("unsafe session key directory")
    writable = metadata.st_mode & 0o022
    trusted_temporary = metadata.st_uid == 0 and bool(metadata.st_mode & stat.S_ISVTX)
    if writable and (key_directory or not trusted_temporary):
        raise ValueError("unsafe session key directory permissions")
    if key_directory and (
        metadata.st_uid != uid or stat.S_IMODE(metadata.st_mode) not in (0o700, 0o750)
    ):
        raise ValueError("unexpected session key directory owner or permissions")


def canonical_directory(directory: Path, uid: int) -> Path:
    if not directory.is_absolute():
        raise ValueError("session key directory must be absolute")
    # macOS exposes /tmp and /var as trusted root-owned aliases. Other links fail closed.
    current = Path(directory.anchor)
    for component in directory.parts[1:]:
        current /= component
        if current.is_symlink() and current not in (Path("/tmp"), Path("/var")):
            raise ValueError("symlink session key directory")
    canonical = directory.resolve(strict=False)
    for parent in reversed(canonical.parents):
        protected_directory(parent, uid)
    return canonical


def validate_key(path: Path, uid: int, gid: int) -> None:
    metadata = path.lstat()
    if not stat.S_ISREG(metadata.st_mode):
        raise ValueError("session key must be a regular non-symlink file")
    if (metadata.st_uid, metadata.st_gid) != (uid, gid):
        raise ValueError("unexpected session key owner or group")
    if stat.S_IMODE(metadata.st_mode) not in (0o600, 0o640) or metadata.st_nlink != 1:
        raise ValueError("unsafe session key permissions or links")
    if metadata.st_size != 32:
        raise ValueError("session key must contain exactly 32 bytes")
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        opened = os.fstat(descriptor)
        if opened != metadata:
            raise ValueError("session key changed during validation")
    finally:
        os.close(descriptor)


def ensure_key(directory: Path, uid: int, gid: int, allow_create: bool) -> Path:
    """Create once, or validate unchanged; configured missing keys are never recreated."""
    directory = canonical_directory(directory, uid)
    if not directory.exists():
        if not allow_create:
            raise ValueError("configured session key is missing")
        directory.mkdir(mode=0o750)
        os.chown(directory, uid, gid)
        directory.chmod(0o750)
    protected_directory(directory, uid, key_directory=True)
    if directory.stat().st_gid != gid:
        raise ValueError("unexpected session key directory group")
    path = directory / "session.key"
    if path.exists() or path.is_symlink():
        validate_key(path, uid, gid)
        return path
    if not allow_create:
        raise ValueError("configured session key is missing")
    descriptor, temporary_name = tempfile.mkstemp(prefix=".session-key.", dir=directory)
    temporary = Path(temporary_name)
    try:
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(os.urandom(32))
            stream.flush()
            os.fchown(stream.fileno(), uid, gid)
            os.fchmod(stream.fileno(), 0o640)
            os.fsync(stream.fileno())
        try:
            os.link(temporary, path, follow_symlinks=False)
        except FileExistsError:
            # A concurrent publisher wins. Validate it without ever replacing it.
            pass
    finally:
        temporary.unlink()
    validate_key(path, uid, gid)
    directory_descriptor = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(directory_descriptor)
    finally:
        os.close(directory_descriptor)
    return path


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument("--uid", type=int, required=True)
    parser.add_argument("--gid", type=int, required=True)
    parser.add_argument("--allow-create", action="store_true")
    parser.add_argument(
        "--file", type=Path, help="validate an existing custom key path"
    )
    args = parser.parse_args()
    try:
        if args.file is not None:
            directory = canonical_directory(args.file.parent, args.uid)
            protected_directory(directory, args.uid, key_directory=True)
            if directory.stat().st_gid != args.gid:
                raise ValueError("unexpected session key directory group")
            validate_key(directory / args.file.name, args.uid, args.gid)
        else:
            ensure_key(args.directory, args.uid, args.gid, args.allow_create)
    except (OSError, ValueError):
        print(
            "session key unavailable or unsafe; restore the protected key backup",
            file=sys.stderr,
        )
        return 1
    print("session key metadata valid")
    return 0


if __name__ == "__main__":
    sys.exit(main())
