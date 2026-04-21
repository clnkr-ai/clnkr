#!/usr/bin/env python3
from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parent.parent
CMD_CLNKR_DIR = REPO_ROOT / "cmd" / "clnkr"


def run(cwd: Path, *args: str) -> str:
    return subprocess.check_output(args, cwd=cwd, text=True)


def parse_go_list_json(output: str) -> list[dict[str, object]]:
    decoder = json.JSONDecoder()
    items: list[dict[str, object]] = []
    index = 0
    length = len(output)
    while index < length:
        while index < length and output[index].isspace():
            index += 1
        if index >= length:
            break
        item, index = decoder.raw_decode(output, index)
        items.append(item)
    return items


def load_packages(cwd: Path) -> dict[str, list[str]]:
    packages: dict[str, list[str]] = {}
    for item in parse_go_list_json(run(cwd, "go", "list", "-json", "./...")):
        import_path = item["ImportPath"]
        if not isinstance(import_path, str):
            continue
        imports: set[str] = set()
        for field in ("Imports", "TestImports", "XTestImports"):
            for imp in item.get(field, []):
                if isinstance(imp, str):
                    imports.add(imp)
        packages[import_path] = sorted(imports)
    return packages

def main() -> int:
    allowlist_path = Path(sys.argv[1]) if len(sys.argv) > 1 else REPO_ROOT / "scripts" / "deferred-package-allowlist.txt"
    if not allowlist_path.is_absolute():
        allowlist_path = REPO_ROOT / allowlist_path
    if not allowlist_path.is_file():
        print(f"error: deferred package allowlist not found: {allowlist_path}", file=sys.stderr)
        return 1

    module_path = run(REPO_ROOT, "go", "list", "-m").strip()
    root_pkg = module_path
    core_prefix = f"{module_path}/internal/core/"
    providers_prefix = f"{module_path}/internal/providers/"
    cmd_prefix = f"{module_path}/cmd/"
    cmd_internal_prefix = f"{module_path}/cmd/internal/"
    compaction_pkg = f"{module_path}/cmd/internal/compaction"

    packages = load_packages(REPO_ROOT)
    packages.update(load_packages(CMD_CLNKR_DIR))

    errors: list[str] = []
    edge_count = 0

    def importer_kind(importer: str) -> str:
        if importer == root_pkg:
            return "root"
        if importer.startswith(core_prefix):
            return "core"
        if importer.startswith(providers_prefix):
            return "provider"
        if importer == compaction_pkg:
            return "compaction"
        if importer.startswith(cmd_prefix):
            return "cmd"
        return "other"

    for importer, imports in sorted(packages.items()):
        kind = importer_kind(importer)
        for target in sorted(imp for imp in imports if imp.startswith(module_path)):
            if target == importer:
                continue
            edge_count += 1

            if target.startswith(cmd_internal_prefix) and not importer.startswith(cmd_prefix):
                errors.append(f"{importer} -> {target}: only cmd/... may import cmd/internal/...")
                continue

            if kind == "root":
                if not target.startswith(core_prefix):
                    errors.append(f"{importer} -> {target}: root may import only internal/core/...")
                continue

            if kind == "core":
                if not target.startswith(core_prefix):
                    errors.append(f"{importer} -> {target}: internal/core/... may import only internal/core/...")
                continue

            if kind == "provider":
                if target != root_pkg and not target.startswith(core_prefix):
                    errors.append(f"{importer} -> {target}: internal/providers/... may import only root clnkr or internal/core/...")
                continue

            if kind == "compaction":
                if target != root_pkg:
                    errors.append(f"{importer} -> {target}: cmd/internal/compaction should keep repo-local imports to root clnkr only")
                continue

            if kind == "cmd":
                if target != root_pkg and not target.startswith(cmd_internal_prefix) and not target.startswith(providers_prefix):
                    errors.append(f"{importer} -> {target}: cmd/... may import only root clnkr, cmd/internal/..., or internal/providers/...")
                continue

            errors.append(f"{importer} -> {target}: unclassified repo-local importer")

    if errors:
        for error in errors:
            print(f"error: {error}", file=sys.stderr)
        return 1

    print(f"architecture import checks passed ({len(packages)} packages, {edge_count} repo-local edges)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
