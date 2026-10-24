"""Prepares the Anki base folder before Anki starts: first-run prefs, rendering, AnkiConnect config."""

import json
import pickle
import random
import sqlite3
import sys
import time
from pathlib import Path


def seed_prefs(base: Path) -> None:
    prefs = base / "prefs21.db"
    if prefs.exists():
        return
    # Without prefs21.db Anki opens a modal language picker; the meta it would write skips it.
    # updates=False: no update-check dialogs nobody sees on a headless box.
    meta = {
        "ver": 0,
        "updates": False,
        "created": int(time.time()),
        "id": random.randrange(0, 2**63),
        "lastMsg": 0,
        "suppressUpdate": False,
        "firstRun": True,
        "defaultLang": "en_US",
    }
    db = sqlite3.connect(prefs)
    try:
        db.execute(
            "create table if not exists profiles (name text primary key collate nocase, data blob not null)"
        )
        db.execute(
            "insert or replace into profiles values ('_global', ?)",
            (pickle.dumps(meta, protocol=4),),
        )
        db.commit()
    finally:
        db.close()


def write_ankiconnect_config(base: Path, key_file: Path) -> None:
    key = key_file.read_text(encoding="utf-8").strip()
    if not key:
        sys.exit(f"setup-profile: {key_file} is empty")
    config = {
        "apiKey": key,
        "apiLogPath": None,
        # 0.0.0.0 inside the container; the host publishes 8765 on 127.0.0.1 only.
        "webBindAddress": "0.0.0.0",
        "webBindPort": 8765,
        "webCorsOriginList": ["http://localhost"],
        "ignoreOriginList": [],
    }
    path = base / "addons21" / "ankiconnect" / "config.json"
    path.write_text(json.dumps(config, indent=4) + "\n", encoding="utf-8")
    path.chmod(0o600)


def main() -> None:
    if len(sys.argv) != 3:
        sys.exit("usage: setup-profile.py <base> <ankiconnect-key-file>")
    base, key_file = Path(sys.argv[1]), Path(sys.argv[2])
    seed_prefs(base)
    gl = base / "gldriver6"
    if not gl.exists():
        gl.write_text("software\n", encoding="utf-8")  # no GPU in the container
    write_ankiconnect_config(base, key_file)


if __name__ == "__main__":
    main()
