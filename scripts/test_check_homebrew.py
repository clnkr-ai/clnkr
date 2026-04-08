from __future__ import annotations

import os
import pathlib
import stat
import subprocess
import tempfile
import textwrap
import unittest


ROOT = pathlib.Path(__file__).resolve().parent.parent
SCRIPT = ROOT / "scripts" / "check-homebrew.sh"
WORKFLOW = ROOT / ".github" / "workflows" / "check-homebrew.yml"


class CheckHomebrewScriptTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.workspace = pathlib.Path(self.tempdir.name)
        self.bin_dir = self.workspace / "bin"
        self.bin_dir.mkdir()
        self.calls_file = self.workspace / "calls.txt"
        self.brew_bin_dir = self.workspace / "brew-prefix" / "opt" / "clnkr" / "bin"
        self.brew_bin_dir.mkdir(parents=True)

    def make_executable(self, path: pathlib.Path, body: str) -> pathlib.Path:
        path.write_text(body, encoding="utf-8")
        path.chmod(path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
        return path

    def run_script(self, *args: str, extra_env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
        env = os.environ.copy()
        env["PATH"] = os.pathsep.join([str(self.bin_dir), env.get("PATH", "")])
        if extra_env:
            env.update(extra_env)
        return subprocess.run(
            [str(SCRIPT), *args],
            cwd=ROOT,
            check=False,
            capture_output=True,
            text=True,
            env=env,
        )

    def test_verify_uses_formula_prefix_binaries_instead_of_path(self) -> None:
        self.make_executable(
            self.bin_dir / "brew",
            textwrap.dedent(
                f"""\
                #!/bin/sh
                if [ "$1" = "--prefix" ] && [ "$2" = "clnkr" ]; then
                  printf '%s\\n' "{self.workspace / 'brew-prefix' / 'opt' / 'clnkr'}"
                  exit 0
                fi
                printf 'unexpected brew args: %s\\n' "$*" >&2
                exit 1
                """
            ),
        )
        self.make_executable(
            self.bin_dir / "clnkr",
            "#!/bin/sh\nprintf 'path clnkr invoked\\n' >> \"$CALLS_FILE\"\nprintf 'clnkr version 0.0.0\\n'\n",
        )
        self.make_executable(
            self.bin_dir / "clnku",
            "#!/bin/sh\nprintf 'path clnku invoked\\n' >> \"$CALLS_FILE\"\nprintf 'clnku version 0.0.0\\n'\n",
        )
        self.make_executable(
            self.brew_bin_dir / "clnkr",
            "#!/bin/sh\nprintf 'brew clnkr invoked\\n' >> \"$CALLS_FILE\"\nprintf 'clnkr version 1.2.3\\n'\n",
        )
        self.make_executable(
            self.brew_bin_dir / "clnku",
            "#!/bin/sh\nprintf 'brew clnku invoked\\n' >> \"$CALLS_FILE\"\nprintf 'clnku version 1.2.3\\n'\n",
        )

        proc = self.run_script(
            "verify",
            extra_env={
                "CALLS_FILE": str(self.calls_file),
                "CLNKR_HOMEBREW_EXPECTED_VERSION": "v1.2.3",
            },
        )

        self.assertEqual(proc.returncode, 0, proc.stderr)
        calls = self.calls_file.read_text(encoding="utf-8")
        self.assertIn("brew clnkr invoked", calls)
        self.assertIn("brew clnku invoked", calls)
        self.assertNotIn("path clnkr invoked", calls)
        self.assertNotIn("path clnku invoked", calls)

    def test_install_reinstalls_formula(self) -> None:
        self.make_executable(
            self.bin_dir / "brew",
            textwrap.dedent(
                f"""\
                #!/bin/sh
                printf '%s\\n' "$*" >> "{self.calls_file}"
                exit 0
                """
            ),
        )

        proc = self.run_script("install")

        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(
            self.calls_file.read_text(encoding="utf-8").splitlines(),
            [
                "tap clnkr-ai/tap",
                "reinstall clnkr-ai/tap/clnkr",
            ],
        )

    def test_latest_version_uses_jq_tag_name_parser(self) -> None:
        self.make_executable(
            self.bin_dir / "curl",
            "#!/bin/sh\nprintf '{\"tag_name\":\"v9.8.7\"}'\n",
        )
        self.make_executable(
            self.bin_dir / "jq",
            textwrap.dedent(
                f"""\
                #!/bin/sh
                printf '%s\\n' "$*" >> "{self.calls_file}"
                cat >/dev/null
                printf 'v9.8.7\\n'
                """
            ),
        )
        self.make_executable(
            self.bin_dir / "brew",
            textwrap.dedent(
                f"""\
                #!/bin/sh
                if [ "$1" = "--prefix" ] && [ "$2" = "clnkr" ]; then
                  printf '%s\\n' "{self.workspace / 'brew-prefix' / 'opt' / 'clnkr'}"
                  exit 0
                fi
                exit 1
                """
            ),
        )
        self.make_executable(
            self.brew_bin_dir / "clnkr",
            "#!/bin/sh\nprintf 'clnkr version 9.8.7\\n'\n",
        )
        self.make_executable(
            self.brew_bin_dir / "clnku",
            "#!/bin/sh\nprintf 'clnku version 9.8.7\\n'\n",
        )

        proc = self.run_script("verify")

        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("-r .tag_name", self.calls_file.read_text(encoding="utf-8"))


class CheckHomebrewWorkflowTest(unittest.TestCase):
    def test_workflow_requires_input_for_non_tag_dispatch(self) -> None:
        content = WORKFLOW.read_text(encoding="utf-8")

        self.assertIn("inputs:", content)
        self.assertIn("expected_version:", content)
        self.assertIn("CLNKR_HOMEBREW_EXPECTED_VERSION:", content)
        self.assertIn("github.event.inputs.expected_version", content)
        self.assertIn("github.ref_type == 'tag'", content)


if __name__ == "__main__":
    unittest.main()
