"""Offline session-key provisioning contracts, using only synthetic key material."""

from __future__ import annotations

import importlib.util
import os
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Protocol, cast
from unittest import mock

HELPER = Path(__file__).with_name("session-key.py")


class KeyHelper(Protocol):
    def ensure_key(
        self, directory: Path, uid: int, gid: int, allow_create: bool
    ) -> Path: ...

    def validate_key(self, path: Path, uid: int, gid: int) -> None: ...


class SessionKeyTests(unittest.TestCase):
    def setUp(self) -> None:
        self.scratch = tempfile.TemporaryDirectory()
        self.addCleanup(self.scratch.cleanup)
        self.directory = Path(self.scratch.name) / "keys"
        spec = importlib.util.spec_from_file_location("session_key", HELPER)
        assert spec is not None and spec.loader is not None
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        self.helper = cast(KeyHelper, module)

    def ensure(self, create: bool = True) -> Path:
        result: Path = self.helper.ensure_key(
            self.directory, os.getuid(), os.getgid(), create
        )
        return result

    def test_create_private_key_and_never_overwrite(self) -> None:
        path = self.ensure()
        original = path.read_bytes()
        self.assertEqual(len(original), 32)
        self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o640)
        self.assertEqual(stat.S_IMODE(self.directory.stat().st_mode), 0o750)
        self.assertEqual(path.stat().st_uid, os.getuid())
        self.assertEqual(path.stat().st_gid, os.getgid())
        self.assertEqual(self.ensure(False).read_bytes(), original)
        self.assertEqual(self.ensure().read_bytes(), original)
        self.assertEqual(list(self.directory.resolve().iterdir()), [path])

    def test_missing_configured_key_does_not_create_directory(self) -> None:
        with self.assertRaises((OSError, ValueError)):
            self.ensure(False)
        self.assertFalse(self.directory.exists())

    def test_unsafe_existing_metadata_and_symlink_are_preserved(self) -> None:
        path = self.ensure()
        for mode in (0o644, 0o660, 0o700):
            path.chmod(mode)
            with self.assertRaises(ValueError):
                self.ensure()
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), mode)
        path.chmod(0o600)
        self.ensure(False)
        path.write_bytes(b"short")
        with self.assertRaises(ValueError):
            self.ensure()
        path.unlink()
        outside = self.directory.parent / "outside"
        outside.write_bytes(b"x" * 32)
        path.symlink_to(outside)
        with self.assertRaises(ValueError):
            self.ensure()
        self.assertTrue(path.is_symlink())

    def test_unsafe_parent_and_symlink_directory_are_rejected(self) -> None:
        self.directory.mkdir(mode=0o777)
        for mode in (0o777, 0o770, 0o755):
            self.directory.chmod(mode)
            with self.assertRaises(ValueError):
                self.ensure()
        self.directory.rmdir()
        outside = self.directory.parent / "outside"
        outside.mkdir(mode=0o700)
        self.directory.symlink_to(outside, target_is_directory=True)
        with self.assertRaises(ValueError):
            self.ensure()

    def test_publication_race_preserves_winner(self) -> None:
        real_link = os.link
        winner = b"w" * 32

        def racing_link(
            source: str | Path, destination: str | Path, **kwargs: object
        ) -> None:
            target = Path(destination)
            target.write_bytes(winner)
            target.chmod(0o640)
            real_link(source, destination)

        with mock.patch.object(os, "link", side_effect=racing_link):
            path = self.ensure()
        self.assertEqual(path.read_bytes(), winner)
        self.assertEqual(list(self.directory.resolve().iterdir()), [path])

    def test_key_owner_group_and_untrusted_ancestor_fail(self) -> None:
        path = self.ensure()
        with self.assertRaises(ValueError):
            self.helper.validate_key(path, os.getuid() + 1, os.getgid())
        with self.assertRaises(ValueError):
            self.helper.validate_key(path, os.getuid(), os.getgid() + 1)
        path.parent.parent.chmod(0o777)
        with self.assertRaises(ValueError):
            self.ensure()
        path.parent.parent.chmod(0o700)

    def test_configured_key_preflight_fails_before_mutations_and_preserves_env(
        self,
    ) -> None:
        root = HELPER.parent.parent
        environment = Path(self.scratch.name) / "env"
        sentinel = Path(self.scratch.name) / "mutation"
        path = self.ensure()
        for installer, start, end in (
            ("install.sh", "# Validate a configured", "# Sets NAME=VALUE"),
            ("install-multi-account.sh", "# Preflight the HTTP", "for tool in"),
        ):
            source = (HELPER.parent / installer).read_text()
            mutation = (
                'echo "== user $svc"'
                if installer == "install.sh"
                else "getent group anki-review >/dev/null || groupadd"
            )
            self.assertLess(source.index(start), source.index(mutation))
            body = source[source.index(start) : source.index(end)]
            body = body.replace("env_file=/etc/anki-review/env", "")
            body = body.replace("--uid 0", '--uid "$(id -u)"')
            for setting, success in (
                (f"SESSION_KEY_FILE={path}", True),
                (f"SESSION_KEY_FILE={path.parent / 'missing'}", False),
                ("SESSION_KEY_FILE=", True),
                ("SESSION_KEY_FILE=/missing\nSESSION_KEY_FILE=", False),
                (" SESSION_KEY_FILE=/missing", False),
            ):
                with self.subTest(installer=installer, setting=setting):
                    original = (
                        "ACCOUNT_SOCKET=/fixture/broker\nGRADER_PROVIDER=codex\n"
                        + setting
                        + "\n"
                    )
                    environment.write_text(original)
                    if sentinel.exists():
                        sentinel.unlink()
                    script = (
                        "set -euo pipefail\nfail() { exit 1; }\n"
                        'getent() { printf "anki-review:x:%s:\\n" "$(id -g)"; }\n'
                        'source "$TASK_ROOT/deploy/provider.sh"\n'
                        + body
                        + '\nprintf mutation >"$SENTINEL"\n'
                    )
                    completed = subprocess.run(
                        ["bash", "-c", script],
                        capture_output=True,
                        text=True,
                        check=False,
                        env={
                            **os.environ,
                            "TASK_ROOT": str(root),
                            "env_file": str(environment),
                            "deploy_dir": str(HELPER.parent),
                            "script_dir": str(HELPER.parent),
                            "svc": "anki-review",
                            "SENTINEL": str(sentinel),
                        },
                    )
                    self.assertEqual(
                        completed.returncode == 0, success, completed.stderr
                    )
                    self.assertEqual(sentinel.exists(), success)
                    self.assertEqual(environment.read_text(), original)

    def test_broker_before_http_preserves_first_install_defaults_and_key(self) -> None:
        source = HELPER.with_name("install-multi-account.sh").read_text()
        start = source.index("if [[ $session_key_configured == false ]]; then")
        block = source[start : source.index('usermod -a -G anki-review "$http_user"')]
        block = block.replace("/etc/anki-review", str(self.directory))
        block = block.replace("--uid 0", '--uid "$(id -u)"')
        environment = self.directory / "env"
        command = (
            "set -euo pipefail\nfail() { exit 1; }\n"
            'getent() { printf "anki-review:x:%s:\\n" "$(id -g)"; }\n'
            "chown() { :; }\n" + block
        )
        values = {
            **os.environ,
            "env_file": str(environment),
            "script_dir": str(HELPER.parent),
            "session_key_configured": "false",
            "session_key_file": str(self.directory / "session.key"),
        }
        completed = subprocess.run(
            ["bash", "-c", command],
            env=values,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertFalse(
            environment.exists(), "broker created incomplete HTTP environment"
        )
        original_key = (self.directory / "session.key").read_bytes()
        self.assertEqual(self.ensure().read_bytes(), original_key)
        original_env = (
            "PORT=8767\nACCOUNT_SOCKET=/fixture/broker\nGRADER_PROVIDER=codex\n"
        )
        environment.write_text(original_env)
        completed = subprocess.run(
            ["bash", "-c", command],
            env=values,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertEqual(
            environment.read_text(),
            original_env
            + "\nSESSION_KEY_FILE="
            + str(self.directory / "session.key")
            + "\n",
        )
        self.assertEqual((self.directory / "session.key").read_bytes(), original_key)

    def test_cli_outputs_metadata_only_and_refuses_missing_key(self) -> None:
        command = [
            sys.executable,
            "-I",
            str(HELPER),
            str(self.directory),
            "--uid",
            str(os.getuid()),
            "--gid",
            str(os.getgid()),
        ]
        missing = subprocess.run(command, capture_output=True, text=True, check=False)
        self.assertNotEqual(missing.returncode, 0)
        made = subprocess.run(
            command + ["--allow-create"], capture_output=True, text=True, check=False
        )
        self.assertEqual(made.returncode, 0, made.stderr)
        self.assertEqual(made.stdout, "session key metadata valid\n")
        self.assertEqual(made.stderr, "")
        self.assertNotIn(
            (self.directory / "session.key").read_bytes().hex(), made.stdout
        )


if __name__ == "__main__":
    unittest.main()
