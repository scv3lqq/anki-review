#!/usr/bin/env python3
"""Print private offline stage counts without exposing saved pass contents."""

from __future__ import annotations

import json
import os
import re
import stat
import sys
from pathlib import Path
from typing import cast

STAGES = ("idle", "input", "checking", "results", "confirmed", "written")
MAX_STATE_BYTES = 64 * 1024 * 1024
ACCOUNT_ID = re.compile(r"[0-9a-f]{32}\Z")


def object_mapping(value: object) -> dict[str, object]:
    if not isinstance(value, dict) or any(not isinstance(key, str) for key in value):
        raise ValueError("invalid object")
    return cast("dict[str, object]", value)


def open_directory(path: str, parent: int | None = None) -> int:
    descriptor = os.open(
        path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=parent
    )
    metadata = os.fstat(descriptor)
    if metadata.st_mode & 0o022 or (
        os.geteuid() != 0 and metadata.st_uid != os.geteuid()
    ):
        os.close(descriptor)
        raise ValueError("unsafe directory")
    return descriptor


def read_json(directory: int, filename: str, limit: int) -> object:
    descriptor = os.open(
        filename, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory
    )
    with os.fdopen(descriptor, "rb") as stream:
        metadata = os.fstat(stream.fileno())
        if (
            not stat.S_ISREG(metadata.st_mode)
            or metadata.st_nlink != 1
            or metadata.st_size > limit
        ):
            raise ValueError("unsafe file")
        raw = stream.read(limit + 1)
        if len(raw) > limit:
            raise ValueError("oversized file")
    value: object = json.loads(raw)
    return value


def registry_accounts(directory: int) -> set[str]:
    try:
        registry = object_mapping(read_json(directory, "accounts.json", 64 * 1024))
    except FileNotFoundError:
        return set()
    if type(registry.get("version")) is not int or registry["version"] != 1:
        raise ValueError("unsupported registry")
    accounts = registry.get("accounts")
    if not isinstance(accounts, list):
        raise TypeError("invalid accounts")
    identifiers: set[str] = set()
    for account in accounts:
        identifier = object_mapping(account).get("id")
        if (
            not isinstance(identifier, str)
            or (identifier != "owner" and not ACCOUNT_ID.fullmatch(identifier))
            or identifier in identifiers
        ):
            raise ValueError("invalid account")
        identifiers.add(identifier)
    return identifiers - {"owner"}


def stage(directory: int) -> str:
    try:
        state = object_mapping(read_json(directory, "state.json", MAX_STATE_BYTES))
    except FileNotFoundError:
        return "idle"
    if (
        type(state.get("version")) is not int
        or state["version"] != 1
        or "pass" not in state
    ):
        raise ValueError("invalid state")
    if state["pass"] is None:
        return "idle"
    current = object_mapping(state["pass"])
    for field in ("items", "answers", "results", "confirmed"):
        if current.get(field) is not None and not isinstance(current[field], list):
            raise ValueError("invalid pass")
    status = current.get("status")
    if status is not None:
        status_mapping = object_mapping(status)
        written = status_mapping.get("written", False)
        if not isinstance(written, bool):
            raise ValueError("invalid write status")
        if written:
            return "written"
    for field, result in (
        ("confirmed", "confirmed"),
        ("results", "results"),
        ("answers", "checking"),
    ):
        if current.get(field) is not None:
            return result
    return "input"


def summarize(path: str) -> dict[str, int]:
    directory = open_directory(path)
    try:
        counts = dict.fromkeys(STAGES, 0)
        counts[stage(directory)] += 1
        registered = registry_accounts(directory)
        try:
            guest_root = open_directory("accounts", directory)
        except FileNotFoundError:
            counts["idle"] += len(registered)
            return counts
        try:
            entries = os.listdir(guest_root)
            if len(entries) > 4096:
                raise ValueError("too many account directories")
            found: set[str] = set()
            for identifier in entries:
                if not ACCOUNT_ID.fullmatch(identifier):
                    raise ValueError("unsafe account directory")
                account = open_directory(identifier, guest_root)
                try:
                    counts[stage(account)] += 1
                finally:
                    os.close(account)
                found.add(identifier)
            counts["idle"] += len(registered - found)
        finally:
            os.close(guest_root)
        return counts
    finally:
        os.close(directory)


def main(arguments: list[str]) -> int:
    try:
        if len(arguments) != 1:
            raise ValueError("expected data directory")
        counts = summarize(str(Path(arguments[0])))
    except (OSError, ValueError, TypeError, RecursionError):
        print("unsafe data")
        return 1
    print(" ".join(f"{name}={counts[name]}" for name in STAGES))
    return (
        2
        if any(counts[name] for name in ("input", "checking", "results", "confirmed"))
        else 0
    )


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
