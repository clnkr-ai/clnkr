from __future__ import annotations

import importlib.util
import os
import pathlib
import shutil
import stat
import subprocess
import sys
import types
import tempfile
import textwrap
import unittest


ROOT = pathlib.Path(__file__).resolve().parent.parent
SCRIPT = ROOT / "scripts" / "require-clankerval.py"
MODULE_SPEC = importlib.util.spec_from_file_location("require_clankerval", SCRIPT)
if MODULE_SPEC is None or MODULE_SPEC.loader is None:
    raise RuntimeError(f"failed to load module spec for {SCRIPT}")
REQUIRE_CLANKERVAL = types.ModuleType(MODULE_SPEC.name)
REQUIRE_CLANKERVAL.__file__ = str(SCRIPT)
sys.modules[MODULE_SPEC.name] = REQUIRE_CLANKERVAL
MODULE_SPEC.loader.exec_module(REQUIRE_CLANKERVAL)


class RequireClankervalTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.workspace = pathlib.Path(self.tempdir.name)
        self.env_file = self.workspace / "clankerval.env"
        self.env_file.write_text(
            textwrap.dedent(
                """\
                CLANKERVAL_MIN_VERSION=0.1.3
                CLANKERVAL_PINNED_VERSION=0.1.3
                CLANKERVAL_PINNED_TAG=v0.1.3
                CLANKERVAL_PINNED_DEB_VERSION=0.1.3-1
                """
            ),
            encoding="utf-8",
        )

    def run_resolver(self, *directories: pathlib.Path) -> subprocess.CompletedProcess[str]:
        path_entries = [str(directory) for directory in directories]
        env = os.environ.copy()
        env["CLANKERVAL_ENV_FILE"] = str(self.env_file)
        env["PATH"] = os.pathsep.join(path_entries)
        return subprocess.run(
            [sys.executable, str(SCRIPT)],
            cwd=ROOT,
            check=False,
            capture_output=True,
            text=True,
            env=env,
        )

    def make_binary(self, directory: pathlib.Path, name: str, version_output: str) -> pathlib.Path:
        directory.mkdir(parents=True, exist_ok=True)
        path = directory / name
        path.write_text(
            "\n".join(
                [
                    "#!/bin/sh",
                    'if [ "$1" = "--version" ]; then',
                    f"    printf '%s\\n' '{version_output}'",
                    "    exit 0",
                    "fi",
                    "printf 'unexpected args: %s\\n' \"$*\" >&2",
                    "exit 1",
                    "",
                ]
            ),
            encoding="utf-8",
        )
        path.chmod(path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
        return path.resolve()

    def make_alias(self, directory: pathlib.Path, name: str, target: pathlib.Path) -> pathlib.Path:
        directory.mkdir(parents=True, exist_ok=True)
        alias = directory / name
        alias.symlink_to(target.name)
        return alias

    def test_returns_clankerval_when_present_and_new_enough(self) -> None:
        path = self.make_binary(self.workspace / "bin", "clankerval", "clankerval version 0.1.3")

        proc = self.run_resolver(self.workspace / "bin")

        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(proc.stdout.strip(), str(path))

    def test_falls_back_to_clnkeval_when_clankerval_is_too_old(self) -> None:
        self.make_binary(self.workspace / "early", "clankerval", "clankerval version 0.1.2")
        path = self.make_binary(self.workspace / "late", "clnkeval", "clnkeval version 0.1.3")

        proc = self.run_resolver(self.workspace / "early", self.workspace / "late")

        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(proc.stdout.strip(), str(path))

    def test_falls_back_to_clnkeval_when_clankerval_version_is_malformed(self) -> None:
        self.make_binary(self.workspace / "early", "clankerval", "clankerval version banana")
        path = self.make_binary(self.workspace / "late", "clnkeval", "clnkeval version 0.1.3")

        proc = self.run_resolver(self.workspace / "early", self.workspace / "late")

        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(proc.stdout.strip(), str(path))

    def test_prefers_clankerval_over_compatible_clnkeval_earlier_on_path(self) -> None:
        self.make_binary(self.workspace / "early", "clnkeval", "clnkeval version 0.1.3")
        path = self.make_binary(self.workspace / "late", "clankerval", "clankerval version 0.1.3")

        proc = self.run_resolver(self.workspace / "early", self.workspace / "late")

        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(proc.stdout.strip(), str(path))

    def test_accepts_trailing_clankerval_version_token(self) -> None:
        path = self.make_binary(self.workspace / "bin", "clankerval", "clankerval 0.1.3")

        proc = self.run_resolver(self.workspace / "bin")

        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(proc.stdout.strip(), str(path))

    def test_accepts_trailing_clnkeval_version_token(self) -> None:
        path = self.make_binary(self.workspace / "bin", "clnkeval", "clnkeval 0.1.3")

        proc = self.run_resolver(self.workspace / "bin")

        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(proc.stdout.strip(), str(path))

    def test_exits_non_zero_with_install_message_when_no_runner_is_present(self) -> None:
        proc = self.run_resolver(self.workspace / "empty")

        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("install", proc.stderr.lower())
        self.assertIn("clankerval >= 0.1.3", proc.stderr)

    def test_exits_non_zero_when_all_discovered_runners_are_too_old(self) -> None:
        self.make_binary(self.workspace / "early", "clankerval", "clankerval version 0.1.1")
        self.make_binary(self.workspace / "late", "clnkeval", "clnkeval version 0.1.2")

        proc = self.run_resolver(self.workspace / "early", self.workspace / "late")

        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("clankerval 0.1.1", proc.stderr)
        self.assertIn("clnkeval 0.1.2", proc.stderr)
        self.assertIn("0.1.3", proc.stderr)

    def test_discover_candidates_preserves_both_alias_names_for_same_binary(self) -> None:
        bin_dir = self.workspace / "bin"
        path = self.make_binary(bin_dir, "clankerval", "clankerval version 0.1.2")
        self.make_alias(bin_dir, "clnkeval", path)

        discovered = REQUIRE_CLANKERVAL.discover_candidates([str(bin_dir)])

        self.assertEqual(
            discovered,
            [("clankerval", path), ("clnkeval", path)],
        )

    def test_reports_both_alias_names_when_same_binary_is_too_old(self) -> None:
        bin_dir = self.workspace / "bin"
        path = self.make_binary(bin_dir, "clankerval", "clankerval version 0.1.2")
        self.make_alias(bin_dir, "clnkeval", path)

        proc = self.run_resolver(bin_dir)

        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("clankerval 0.1.2", proc.stderr)
        self.assertIn("clnkeval 0.1.2", proc.stderr)

    def test_make_evaluations_targets_fail_cleanly_when_resolver_exits_non_zero(self) -> None:
        make_path = shutil.which("make")
        self.assertIsNotNone(make_path)
        self.env_file.write_text("CLANKERVAL_MIN_VERSION=9.9.9\n", encoding="utf-8")
        tool_bin = self.workspace / "tool-bin"
        self.make_binary(tool_bin, "clankerval", "clankerval version 0.1.2")
        python_bin = self.workspace / "python-bin"
        python_bin.mkdir()
        (python_bin / "python3").symlink_to(pathlib.Path(sys.executable).resolve())
        env = os.environ.copy()
        env["CLANKERVAL_ENV_FILE"] = str(self.env_file)
        env["PATH"] = os.pathsep.join([str(tool_bin), str(python_bin)])

        for target in ("evaluations", "evaluations-live"):
            with self.subTest(target=target):
                proc = subprocess.run(
                    [make_path, target],
                    cwd=ROOT,
                    check=False,
                    capture_output=True,
                    text=True,
                    env=env,
                )

                combined = f"{proc.stdout}\n{proc.stderr}"
                self.assertNotEqual(proc.returncode, 0)
                self.assertIn("error: clankerval >= 9.9.9 is required.", combined)
                self.assertIn("clankerval 0.1.2", combined)
                self.assertNotRegex(combined, r"(?i)command not found|permission denied")


if __name__ == "__main__":
    unittest.main()
