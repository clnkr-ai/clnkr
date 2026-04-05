#!/usr/bin/env python3

from __future__ import annotations

import dataclasses
import os
import pathlib
import re
import subprocess
import sys
from typing import Iterable


ROOT = pathlib.Path(__file__).resolve().parent.parent
DEFAULT_ENV_FILE = ROOT / "tools" / "clankerval.env"
VERSION_RE = re.compile(r"(?<!\d)v?(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)\b")


@dataclasses.dataclass(frozen=True)
class SemVer:
    major: int
    minor: int
    patch: int
    prerelease: tuple[str | int, ...] = ()

    @classmethod
    def parse(cls, text: str) -> "SemVer":
        version = text.split("+", 1)[0]
        core, dash, prerelease = version.partition("-")
        major, minor, patch = (int(part) for part in core.split("."))
        prerelease_parts: list[str | int] = []
        if dash:
            for part in prerelease.split("."):
                if part.isdigit():
                    prerelease_parts.append(int(part))
                    continue
                prerelease_parts.append(part)
        return cls(major=major, minor=minor, patch=patch, prerelease=tuple(prerelease_parts))

    def __lt__(self, other: "SemVer") -> bool:
        left_core = (self.major, self.minor, self.patch)
        right_core = (other.major, other.minor, other.patch)
        if left_core != right_core:
            return left_core < right_core
        if not self.prerelease and other.prerelease:
            return False
        if self.prerelease and not other.prerelease:
            return True
        if self.prerelease == other.prerelease:
            return False
        for left, right in zip(self.prerelease, other.prerelease):
            if left == right:
                continue
            if isinstance(left, int) and isinstance(right, str):
                return True
            if isinstance(left, str) and isinstance(right, int):
                return False
            return left < right
        return len(self.prerelease) < len(other.prerelease)


@dataclasses.dataclass(frozen=True)
class Candidate:
    name: str
    path: pathlib.Path
    version: str | None
    detail: str


def load_env_file(path: pathlib.Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        key, sep, value = line.partition("=")
        if not sep:
            raise ValueError(f"invalid line in {path}: {raw_line!r}")
        values[key.strip()] = value.strip().strip("'\"")
    return values


def discover_candidates(path_entries: Iterable[str]) -> list[tuple[str, pathlib.Path]]:
    discovered: list[tuple[str, pathlib.Path]] = []
    seen: set[tuple[str, pathlib.Path]] = set()
    for entry in path_entries:
        if not entry:
            continue
        directory = pathlib.Path(entry)
        for name in ("clankerval", "clnkeval"):
            candidate = (directory / name)
            if not candidate.is_file() or not os.access(candidate, os.X_OK):
                continue
            resolved = candidate.resolve()
            key = (name, resolved)
            if key in seen:
                continue
            seen.add(key)
            discovered.append((name, resolved))
    return discovered


def extract_version(output: str) -> str | None:
    matches = VERSION_RE.findall(output)
    if not matches:
        return None
    return matches[-1].removeprefix("v")


def probe_candidate(name: str, path: pathlib.Path) -> Candidate:
    proc = subprocess.run(
        [str(path), "--version"],
        check=False,
        capture_output=True,
        text=True,
    )
    output = "\n".join(part.strip() for part in (proc.stdout, proc.stderr) if part.strip()).strip()
    if proc.returncode != 0:
        detail = f"version check exited {proc.returncode}"
        if output:
            detail = f"{detail}: {output}"
        return Candidate(name=name, path=path, version=None, detail=detail)

    version = extract_version(output)
    if version is None:
        detail = "unparseable version output"
        if output:
            detail = f"{detail}: {output}"
        return Candidate(name=name, path=path, version=None, detail=detail)
    return Candidate(name=name, path=path, version=version, detail="")


def resolve_runner(path_entries: Iterable[str], minimum_version: str) -> tuple[pathlib.Path | None, list[Candidate]]:
    required = SemVer.parse(minimum_version)
    discovered: list[Candidate] = []
    fallback: pathlib.Path | None = None
    for name, path in discover_candidates(path_entries):
        candidate = probe_candidate(name, path)
        discovered.append(candidate)
        if candidate.version is None:
            continue
        if SemVer.parse(candidate.version) < required:
            continue
        if candidate.name == "clankerval":
            return candidate.path, discovered
        if fallback is None:
            fallback = candidate.path
    return fallback, discovered


def failure_message(minimum_version: str, discovered: list[Candidate]) -> str:
    lines = [
        f"error: clankerval >= {minimum_version} is required.",
        "install it with ./scripts/install-clankerval.sh",
    ]
    if discovered:
        lines.append("discovered runners:")
        for candidate in discovered:
            if candidate.version is not None:
                lines.append(f"  - {candidate.name} {candidate.version} at {candidate.path}")
                continue
            lines.append(f"  - {candidate.name} at {candidate.path} ({candidate.detail})")
    return "\n".join(lines)


def main() -> int:
    env_file = pathlib.Path(os.environ.get("CLANKERVAL_ENV_FILE", DEFAULT_ENV_FILE))
    try:
        values = load_env_file(env_file)
    except (OSError, ValueError) as err:
        print(f"error: failed to load {env_file}: {err}", file=sys.stderr)
        return 1

    minimum_version = values.get("CLANKERVAL_MIN_VERSION")
    if not minimum_version:
        print(f"error: {env_file} does not define CLANKERVAL_MIN_VERSION", file=sys.stderr)
        return 1

    runner, discovered = resolve_runner(os.environ.get("PATH", "").split(os.pathsep), minimum_version)
    if runner is None:
        print(failure_message(minimum_version, discovered), file=sys.stderr)
        return 1

    print(runner)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
