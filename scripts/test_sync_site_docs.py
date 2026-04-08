from __future__ import annotations

import os
import pathlib
import shutil
import subprocess
import tempfile
import textwrap
import unittest


ROOT = pathlib.Path(__file__).resolve().parent.parent
SCRIPT = ROOT / "scripts" / "sync-site-docs.sh"


class SyncSiteDocsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.workspace = pathlib.Path(self.tempdir.name)
        self.repo_root = self.workspace / "repo"
        self.external_repo = self.workspace / "clankerval-source"
        (self.repo_root / "scripts").mkdir(parents=True)
        (self.repo_root / "doc").mkdir()
        (self.repo_root / "site" / "content" / "docs").mkdir(parents=True)

        shutil.copy2(SCRIPT, self.repo_root / "scripts" / "sync-site-docs.sh")

        (self.repo_root / "doc" / "clnkr.1.md").write_text(
            textwrap.dedent(
                """\
                clnkr 1 "clnkr" "User Commands"
                ==============================

                # NAME

                clnkr - terminal UI
                """
            ),
            encoding="utf-8",
        )
        (self.repo_root / "doc" / "clnku.1.md").write_text(
            textwrap.dedent(
                """\
                clnku 1 "clnku" "User Commands"
                ==============================

                # NAME

                clnku - plain CLI
                """
            ),
            encoding="utf-8",
        )

        (self.external_repo / "doc").mkdir(parents=True)
        (self.external_repo / "doc" / "clankerval.1.md").write_text(
            textwrap.dedent(
                """\
                clankerval 1 "clankerval" "User Commands"
                ========================================

                # NAME

                clankerval - evaluation runner
                """
            ),
            encoding="utf-8",
        )

        subprocess.run(["git", "init", "-b", "main"], cwd=self.external_repo, check=True)
        subprocess.run(["git", "add", "."], cwd=self.external_repo, check=True)
        subprocess.run(
            ["git", "-c", "user.name=Test User", "-c", "user.email=test@example.com", "commit", "-m", "init"],
            cwd=self.external_repo,
            check=True,
        )

    def test_generates_clankerval_page_from_cloned_repo(self) -> None:
        env = os.environ.copy()
        env["CLANKERVAL_DOCS_REPO_URL"] = f"file://{self.external_repo}"

        proc = subprocess.run(
            [str(self.repo_root / "scripts" / "sync-site-docs.sh")],
            cwd=self.repo_root,
            check=False,
            capture_output=True,
            text=True,
            env=env,
        )

        self.assertEqual(proc.returncode, 0, proc.stderr)

        clankerval_page = self.repo_root / "site" / "content" / "docs" / "clankerval.md"
        self.assertTrue(clankerval_page.exists())
        content = clankerval_page.read_text(encoding="utf-8")
        self.assertIn('title = "clankerval"', content)
        self.assertIn("Generated from [clankerval.1.md]", content)
        self.assertIn("clankerval - evaluation runner", content)


if __name__ == "__main__":
    unittest.main()
