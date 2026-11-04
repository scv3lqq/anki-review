"""Offline deployment safety fixtures; never invoke real remote or service tools."""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
HELPER = ROOT / "deploy/check-passes.py"
ACCOUNT_A = "a" * 32
ACCOUNT_B = "b" * 32
PRIVATE = "fixture-private-answer-and-credential"


def malformed_sessions() -> list[dict[str, object]]:
    sessions: list[dict[str, object]] = []
    value: object
    for field in ("multi_account", "unlocked"):
        for value in (None, "true", 1, {}, []):
            sessions.append({"state": "login", field: value})
    for value in ("unknown", 1):
        sessions.append({"state": value})
    for value in (None, "unknown", 1):
        sessions.append({"state": "login", "unlocked": True, "stage": value})
    return sessions


class StageSummaryTests(unittest.TestCase):
    def setUp(self) -> None:
        self.scratch = tempfile.TemporaryDirectory()
        self.addCleanup(self.scratch.cleanup)
        self.data = Path(self.scratch.name) / "data"
        self.data.mkdir(mode=0o700)

    def state(self, directory: Path, value: object) -> None:
        directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        (directory / "state.json").write_text(json.dumps({"version": 1, "pass": value}))

    def run_summary(self, code: int) -> str:
        completed = subprocess.run(
            [sys.executable, "-I", str(HELPER), str(self.data)],
            capture_output=True,
            text=True,
            check=False,
            timeout=10,
        )
        self.assertEqual(
            completed.returncode, code, completed.stdout + completed.stderr
        )
        self.assertNotIn(PRIVATE, completed.stdout + completed.stderr)
        self.assertNotIn(str(self.data), completed.stdout + completed.stderr)
        self.assertEqual(completed.stderr, "")
        return completed.stdout

    def test_owner_guests_and_orphan_states_are_all_counted(self) -> None:
        (self.data / "accounts.json").write_text(
            json.dumps({"version": 1, "accounts": [{"id": "owner"}, {"id": ACCOUNT_A}]})
        )
        self.state(self.data, None)
        self.state(self.data / "accounts" / ACCOUNT_A, {"status": {"written": True}})
        self.state(self.data / "accounts" / ACCOUNT_B, None)
        self.assertEqual(
            self.run_summary(0),
            "idle=2 input=0 checking=0 results=0 confirmed=0 written=1\n",
        )

    def test_missing_registered_states_are_idle(self) -> None:
        (self.data / "accounts.json").write_text(
            json.dumps({"version": 1, "accounts": [{"id": "owner"}, {"id": ACCOUNT_A}]})
        )
        self.assertIn("idle=2", self.run_summary(0))

    def test_active_and_confirmed_stages_follow_domain_precedence(self) -> None:
        cases: list[tuple[dict[str, object], str]] = [
            ({"deck": PRIVATE}, "input"),
            ({"answers": [], "session": PRIVATE}, "checking"),
            ({"answers": [], "results": []}, "results"),
            ({"answers": [], "results": [], "confirmed": []}, "confirmed"),
        ]
        for value, stage in cases:
            with self.subTest(stage=stage):
                self.state(self.data, value)
                self.assertIn(stage + "=1", self.run_summary(2))

    def test_rejects_corruption_version_types_and_oversize_without_content(
        self,
    ) -> None:
        invalid = [
            PRIVATE,
            '{"version":2}',
            '{"version":1,"pass":"' + PRIVATE + '"}',
            '{"version":1,"pass":{"answers":3}}',
        ]
        for raw in invalid:
            with self.subTest(raw=raw):
                (self.data / "state.json").write_text(raw)
                self.assertEqual(self.run_summary(1), "unsafe data\n")
        with (self.data / "state.json").open("wb") as stream:
            stream.truncate(64 * 1024 * 1024 + 1)
        self.assertEqual(self.run_summary(1), "unsafe data\n")

    def test_rejects_symlinked_state_directory_and_registry(self) -> None:
        outside = Path(self.scratch.name) / "outside"
        outside.write_text(PRIVATE)
        state = self.data / "state.json"
        state.symlink_to(outside)
        self.assertEqual(self.run_summary(1), "unsafe data\n")
        state.unlink()
        (self.data / "accounts.json").symlink_to(outside)
        self.assertEqual(self.run_summary(1), "unsafe data\n")
        (self.data / "accounts.json").unlink()
        (self.data / "accounts").mkdir()
        (self.data / "accounts" / ACCOUNT_A).symlink_to(
            self.data, target_is_directory=True
        )
        self.assertEqual(self.run_summary(1), "unsafe data\n")


class DeploymentGuardTests(unittest.TestCase):
    def setUp(self) -> None:
        self.scratch = tempfile.TemporaryDirectory()
        self.addCleanup(self.scratch.cleanup)
        self.directory = Path(self.scratch.name)
        self.commands = self.directory / "commands"
        self.commands.mkdir()
        self.calls = self.directory / "calls"
        executable_dirs = [str(Path(sys.executable).parent), "/usr/bin", "/bin"]
        jq = shutil.which("jq")
        if jq:
            executable_dirs.append(str(Path(jq).parent))
        self.env = {
            "PATH": os.pathsep.join([str(self.commands), *executable_dirs]),
            "HOME": str(self.directory),
            "TMPDIR": str(self.directory),
            "CALL_LOG": str(self.calls),
            "SESSION": '{"multi_account":true,"state":"login"}',
            "DEPLOY_SSH": "fixture",
            "DEPLOY_DIR": str(self.directory / "remote"),
            "TMUX": "fixture",
        }
        self.command(
            "ssh",
            """case "$*" in
  *curl*) printf '%s' "$SESSION" ;;
  *'sh -s --'*) body=$(cat); case "$body" in *'service='*) printf 'service=active\\nsession=%s\\n' "$SESSION" ;; *) printf '{"result":[],"error":null}' ;; esac ;;
  *'systemctl show'*) printf 'fixture-invocation' ;;
  *) printf 'ssh %s\\n' "$*" >> "$CALL_LOG" ;;
esac""",
        )
        self.command("scp", 'printf "scp %s\\n" "$*" >> "$CALL_LOG"')
        self.command(
            "realpath", 'while [ "$#" -gt 1 ]; do shift; done; printf "%s\\n" "$1"'
        )
        for command in (
            "tmux",
            "osascript",
            "systemctl",
            "install",
            "useradd",
            "usermod",
        ):
            self.command(
                command, 'printf "MUTATION %s\\n" "$*" >> "$CALL_LOG"; exit 97'
            )
        self.command("sleep", 'printf "POLL %s\\n" "$*" >> "$CALL_LOG"; exit 97')

    def command(self, name: str, body: str) -> None:
        path = self.commands / name
        path.write_text("#!/bin/bash\nset -eu\n" + body + "\n")
        path.chmod(0o755)

    def run_script(self, script: Path, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["/bin/bash", str(script), *args],
            env=self.env,
            capture_output=True,
            text=True,
            check=False,
            timeout=10,
        )

    def copy_skill(self, tree: str) -> Path:
        destination = self.directory / tree / "skills/setup-anki-review"
        destination.mkdir(parents=True, exist_ok=True)
        for filename in ("server.sh", "lib.sh"):
            shutil.copyfile(
                ROOT / tree / "skills/setup-anki-review" / filename,
                destination / filename,
            )
        return destination / "server.sh"

    def test_both_restart_paths_refuse_multi_before_mutation(self) -> None:
        for tree in (".agents", ".claude"):
            for operation in ("restart", "sudo-restart"):
                with self.subTest(tree=tree, operation=operation):
                    completed = self.run_script(self.copy_skill(tree), operation)
                    self.assertNotEqual(completed.returncode, 0)
                    self.assertIn("multi-account", completed.stdout + completed.stderr)
                    self.assertFalse(
                        self.calls.exists(), "restart attempted an external mutation"
                    )

    def test_restart_paths_refuse_malformed_session_fields(self) -> None:
        for tree in (".agents", ".claude"):
            script = self.copy_skill(tree)
            for operation in ("restart", "sudo-restart"):
                for session in malformed_sessions():
                    with self.subTest(tree=tree, operation=operation, session=session):
                        self.calls.unlink(missing_ok=True)
                        self.env["SESSION"] = json.dumps(session)
                        completed = self.run_script(script, operation)
                        self.assertNotEqual(completed.returncode, 0)
                        self.assertIn("cannot establish account mode", completed.stderr)
                        self.assertFalse(
                            self.calls.exists(), "malformed session permitted restart"
                        )

    def test_wait_setup_returns_mode_without_polling(self) -> None:
        for tree in (".agents", ".claude"):
            completed = self.run_script(self.copy_skill(tree), "wait-setup")
            self.assertEqual(
                completed.returncode, 0, completed.stdout + completed.stderr
            )
            self.assertIn("multi-account", completed.stdout)
            self.assertNotIn("unlocked", completed.stdout)
            self.assertFalse(self.calls.exists(), "wait-setup continued polling")

    def test_wait_setup_does_not_accept_unlock_from_malformed_session(self) -> None:
        self.env["SESSION"] = '{"state":"login","multi_account":null,"unlocked":true}'
        for tree in (".agents", ".claude"):
            completed = self.run_script(self.copy_skill(tree), "wait-setup")
            self.assertNotEqual(completed.returncode, 0)
            self.assertNotIn("owner: unlocked", completed.stdout)

    def test_single_account_wait_and_restart_remain_available(self) -> None:
        for explicit in ({}, {"multi_account": False}):
            self.env["SESSION"] = json.dumps(
                {"state": "login", "unlocked": True, "stage": "idle", **explicit}
            )
            for tree in (".agents", ".claude"):
                script = self.copy_skill(tree)
                completed = self.run_script(script, "wait-setup")
                self.assertEqual(
                    completed.returncode, 0, completed.stdout + completed.stderr
                )
                self.assertIn("owner: unlocked", completed.stdout)
                completed = self.run_script(script, "restart")
                self.assertEqual(
                    completed.returncode, 0, completed.stdout + completed.stderr
                )
                self.assertIn("restarted anki-review", completed.stdout)

    def test_status_reports_private_mode_without_unlock_or_stage_inference(
        self,
    ) -> None:
        for tree in (".agents", ".claude"):
            completed = self.run_script(self.copy_skill(tree), "status")
            self.assertEqual(
                completed.returncode, 0, completed.stdout + completed.stderr
            )
            self.assertIn("multi-account", completed.stdout)
            self.assertNotIn("owner: locked", completed.stdout)
            self.assertNotIn("api stage: idle", completed.stdout)

    def test_verify_url_reports_private_multi_account_mode(self) -> None:
        self.env["DEPLOY_URL"] = "https://review.example.test"
        self.command("curl", 'printf "%s\\n200" "$SESSION"')
        for tree in (".agents", ".claude"):
            for extra in ({}, {"unlocked": True, "stage": "checking"}):
                with self.subTest(tree=tree, extra=extra):
                    self.env["SESSION"] = json.dumps(
                        {"state": "login", "multi_account": True, **extra}
                    )
                    completed = self.run_script(self.copy_skill(tree), "verify-url")
                    self.assertEqual(
                        completed.returncode, 0, completed.stdout + completed.stderr
                    )
                    self.assertIn(
                        "multi-account (account status private)", completed.stdout
                    )
                    self.assertNotIn("locked", completed.stdout)
                    self.assertNotIn("stage", completed.stdout)
                    self.assertNotIn("state login", completed.stdout)
                    self.assertFalse(self.calls.exists())

    def test_full_upload_transports_both_binaries_and_complete_kit(self) -> None:
        kit = [
            "install.sh",
            "install-multi-account.sh",
            "smoke.sh",
            "provider.sh",
            "test-provider.sh",
            "test-multi-account.py",
            "check-passes.py",
            "autodeploy.sh",
            "systemd/anki-review.service",
            "systemd/anki-review-deploy.service",
            "systemd/anki-review-deploy.timer",
            "systemd/anki-review-provisioner.service",
            "anki/Dockerfile",
            "anki/compose.yaml",
            "anki/entrypoint.sh",
            "anki/setup-profile.py",
            "anki/syncserver.sh",
            "anki/up.sh",
        ]
        for filename in kit:
            source, target = (
                ROOT / "deploy" / filename,
                self.directory / "deploy" / filename,
            )
            target.parent.mkdir(parents=True, exist_ok=True)
            if source.exists():
                shutil.copyfile(source, target)
        (self.directory / "bin").mkdir()
        for name in ("anki-review-linux-amd64", "anki-review-provisioner-linux-amd64"):
            (self.directory / "bin" / name).write_bytes(b"\x7fELFfixture")
        for tree in (".agents", ".claude"):
            if self.calls.exists():
                self.calls.unlink()
            completed = self.run_script(self.copy_skill(tree), "upload")
            self.assertEqual(
                completed.returncode, 0, completed.stdout + completed.stderr
            )
            calls = self.calls.read_text()
            for filename in kit:
                self.assertIn(filename, calls)
            self.assertIn("/bin/anki-review-provisioner-linux-amd64", calls)
            self.assertIn("/bin/anki-review", calls)

    def autodeploy_fixture(self) -> tuple[Path, str]:
        kit = self.directory / "kit"
        (kit / "src/.git").mkdir(parents=True)
        (kit / "allowed_signers").write_text("fixture signing key")
        old, new = "a" * 40, "b" * 40
        self.command(
            "git",
            'case "$*" in *rev-parse*) printf \'' + new + "' ;; *) exit 0 ;; esac",
        )
        self.command(
            "curl",
            """case "$*" in *check-runs*) printf '{"check_runs":[{"name":"test","id":1,"status":"completed","conclusion":"success"},{"name":"secrets","id":2,"status":"completed","conclusion":"success"}]}' ;; *) printf '%s' "$SESSION" ;; esac""",
        )
        self.command(
            "go",
            """printf 'BUILD\\n' >> "$CALL_LOG"
while [ "$#" -gt 0 ]; do if [ "$1" = -o ]; then shift; printf binary > "$1"; break; fi; shift; done""",
        )
        self.command("sha256sum", 'printf "same-digest  %s\\n" "$1"')
        return kit, old

    def test_autodeploy_holds_before_unchanged_binary_or_provider_only_advance(
        self,
    ) -> None:
        kit, old = self.autodeploy_fixture()
        for extra in (
            {},
            {"unlocked": False},
            {"unlocked": True, "stage": "idle"},
            {"unlocked": True, "stage": "checking"},
        ):
            with self.subTest(extra=extra):
                self.env["SESSION"] = json.dumps(
                    {"state": "login", "multi_account": True, **extra}
                )
                (kit / "autodeploy-state").write_text("deployed=" + old + "\n")
                completed = self.run_script(ROOT / "deploy/autodeploy.sh", str(kit))
                self.assertEqual(
                    completed.returncode, 0, completed.stdout + completed.stderr
                )
                self.assertIn("multi-account", completed.stdout)
                self.assertIn("deployed=" + old, (kit / "autodeploy-state").read_text())
                self.assertFalse(
                    self.calls.exists(),
                    "multi-account auto-deploy reached build or restart",
                )

    def test_autodeploy_rejects_malformed_session_before_build_or_deployment(
        self,
    ) -> None:
        kit, old = self.autodeploy_fixture()
        for session in malformed_sessions():
            with self.subTest(session=session):
                self.calls.unlink(missing_ok=True)
                self.env["SESSION"] = json.dumps(session)
                (kit / "autodeploy-state").write_text("deployed=" + old + "\n")
                completed = self.run_script(ROOT / "deploy/autodeploy.sh", str(kit))
                self.assertEqual(
                    completed.returncode, 0, completed.stdout + completed.stderr
                )
                self.assertIn("no valid session status", completed.stdout)
                self.assertIn("deployed=" + old, (kit / "autodeploy-state").read_text())
                self.assertFalse(
                    self.calls.exists(), "malformed session reached build/restart"
                )

    def test_autodeploy_accepts_explicit_false_or_legacy_mode(self) -> None:
        kit, old = self.autodeploy_fixture()
        for explicit in ({}, {"multi_account": False}):
            self.env["SESSION"] = json.dumps(
                {"state": "login", "unlocked": False, **explicit}
            )
            (kit / "autodeploy-state").write_text("deployed=" + old + "\n")
            completed = self.run_script(ROOT / "deploy/autodeploy.sh", str(kit))
            self.assertEqual(
                completed.returncode, 0, completed.stdout + completed.stderr
            )
            self.assertIn("binary is unchanged", completed.stdout)
            self.assertIn(
                "deployed=" + "b" * 40, (kit / "autodeploy-state").read_text()
            )

    def test_autodeploy_revalidates_session_before_restart(self) -> None:
        kit, old = self.autodeploy_fixture()
        self.env["SESSION_SEEN"] = str(self.directory / "session-seen")
        self.env["SESSION"] = '{"state":"login","unlocked":true,"stage":null}'
        self.command(
            "curl",
            """case "$*" in
*check-runs*) printf '{"check_runs":[{"name":"test","id":1,"status":"completed","conclusion":"success"},{"name":"secrets","id":2,"status":"completed","conclusion":"success"}]}' ;;
*) if [ -e "$SESSION_SEEN" ]; then printf '%s' "$SESSION"; else touch "$SESSION_SEEN"; printf '{"state":"login","unlocked":false}'; fi ;;
esac""",
        )
        self.command("sha256sum", 'printf "%s  %s\\n" "$1" "$1"')
        (kit / "autodeploy-state").write_text("deployed=" + old + "\n")
        completed = self.run_script(ROOT / "deploy/autodeploy.sh", str(kit))
        self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)
        self.assertIn("no valid session status", completed.stdout)
        self.assertIn("deployed=" + old, (kit / "autodeploy-state").read_text())
        self.assertIn("BUILD", self.calls.read_text())
        self.assertNotIn("MUTATION", self.calls.read_text())

    def test_ordinary_installer_refuses_explicit_and_saved_multi_before_mutations(
        self,
    ) -> None:
        saved = self.directory / "saved-env"
        installer = self.directory / "install.sh"
        installer.write_text(
            (ROOT / "deploy/install.sh")
            .read_text()
            .replace("/etc/anki-review/env", str(saved))
        )
        shutil.copyfile(ROOT / "deploy/provider.sh", self.directory / "provider.sh")
        for explicit, retained in (
            (False, "ACCOUNT_SOCKET=/run/fixture.sock\n"),
            (False, "  ACCOUNT_SOCKET = '/run/fixture.sock'\n"),
            (True, ""),
        ):
            if explicit:
                self.env["ACCOUNT_SOCKET"] = "/run/fixture.sock"
                saved.write_text("")
            else:
                self.env.pop("ACCOUNT_SOCKET", None)
                saved.write_text(retained)
            completed = self.run_script(installer, str(self.directory))
            self.assertNotEqual(completed.returncode, 0)
            self.assertIn("multi-account", completed.stdout + completed.stderr)
            self.assertFalse(
                self.calls.exists(), "ordinary installer mutated a multi-account host"
            )


if __name__ == "__main__":
    unittest.main()
