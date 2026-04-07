#!/usr/bin/env python3
"""
Check GitHub Actions workflows for `make <target>` calls that do not exist in this repo's Makefile.
This keeps workflow `run:` steps from silently drifting away from the targets developers can actually invoke.
"""

from __future__ import annotations

import pathlib
import re
import subprocess
import sys


ROOT = pathlib.Path(__file__).resolve().parent.parent
WORKFLOW_DIR = ROOT / ".github" / "workflows"

TARGET_RE = re.compile(r"^([A-Za-z0-9][A-Za-z0-9_-]*):(?!=)")
SINGLE_LINE_RUN_RE = re.compile(r"^\s*run:\s+make\s+([A-Za-z0-9][A-Za-z0-9_-]*)\b")
BLOCK_RUN_RE = re.compile(r"^(\s*)run:\s*[|>]\s*$")
MAKE_CMD_RE = re.compile(r"^\s*make\s+([A-Za-z0-9][A-Za-z0-9_-]*)\b")


def make_targets() -> set[str]:
    proc = subprocess.run(
        ["make", "-qp"],
        cwd=ROOT,
        check=False,
        capture_output=True,
        text=True,
    )
    targets: set[str] = set()
    for line in proc.stdout.splitlines():
        if line.startswith((".", "#")):
            continue
        match = TARGET_RE.match(line)
        if match:
            targets.add(match.group(1))
    return targets


def workflow_make_calls(path: pathlib.Path) -> list[tuple[int, str]]:
    calls: list[tuple[int, str]] = []
    lines = path.read_text().splitlines()
    i = 0
    while i < len(lines):
        line = lines[i]
        single = SINGLE_LINE_RUN_RE.match(line)
        if single:
            calls.append((i + 1, single.group(1)))
            i += 1
            continue

        block = BLOCK_RUN_RE.match(line)
        if not block:
            i += 1
            continue

        block_indent = len(block.group(1))
        i += 1
        while i < len(lines):
            body = lines[i]
            stripped = body.strip()
            if stripped and len(body) - len(body.lstrip()) <= block_indent:
                break
            if stripped and not stripped.startswith("#"):
                match = MAKE_CMD_RE.match(body)
                if match:
                    calls.append((i + 1, match.group(1)))
            i += 1

    return calls


def main() -> int:
    targets = make_targets()
    failures: list[str] = []

    for workflow in sorted(WORKFLOW_DIR.glob("*.y*ml")):
        for lineno, target in workflow_make_calls(workflow):
            if target not in targets:
                rel = workflow.relative_to(ROOT)
                failures.append(f"{rel}:{lineno}: unknown make target '{target}'")

    if failures:
        print("workflow make target check failed:", file=sys.stderr)
        for failure in failures:
            print(f"  {failure}", file=sys.stderr)
        return 1

    print("workflow make target check passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
