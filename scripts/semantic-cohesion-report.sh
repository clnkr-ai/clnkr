#!/usr/bin/env bash

set -euo pipefail

target="${1:-.}"
bin="${THOTH_BIN:-thoth}"

if [[ ! -e "$target" ]]; then
  echo "error: target not found: $target" >&2
  exit 1
fi
if [[ ! -d "$target" ]]; then
  echo "error: target must be a directory: $target" >&2
  exit 1
fi
if ! command -v "$bin" >/dev/null 2>&1; then
  echo "error: thoth binary not found: $bin; set THOTH_BIN or install thoth" >&2
  exit 1
fi

report="$(mktemp)"
prod_tree="$(mktemp -d)"
trap 'rm -f "$report"; rm -rf "$prod_tree"' EXIT

analysis_target="$(python3 - "$target" "$prod_tree" <<'PY'
import shutil
import sys
from pathlib import Path

source = Path(sys.argv[1])
dest = Path(sys.argv[2]) / "clnkr-production"

def ignored(path, names):
    current = Path(path)
    rejected = set()
    for name in names:
        child = current / name
        rel_text = child.relative_to(source).as_posix() if source in child.parents or child == source else child.as_posix()
        if name in {".git", ".worktrees", "build"} or rel_text == "site/public":
            rejected.add(name)
        elif name.endswith("_test.go"):
            rejected.add(name)
    return rejected

shutil.copytree(source, dest, ignore=ignored)
print(dest)
PY
)"

if [[ "${THOTH_DEBUG_INPUT:-}" == "1" ]]; then
  echo "debug: semantic cohesion input copied to $analysis_target" >&2
fi

"$bin" "$analysis_target" --report > "$report"

python3 - "$report" <<'PY'
import json
import re
import sys
from collections import Counter, defaultdict
from json import JSONDecodeError
from pathlib import Path

try:
    data = json.loads(Path(sys.argv[1]).read_text())
except JSONDecodeError as exc:
    print(f"error: thoth --report did not return valid JSON: {exc}", file=sys.stderr)
    sys.exit(1)

missing = [key for key in ("module_health", "functions") if key not in data]
if missing:
    print(f"error: thoth --report missing expected keys: {', '.join(missing)}", file=sys.stderr)
    sys.exit(1)

modules = data["module_health"]
functions = data["functions"]

stop = {
    "init", "test", "main", "run", "write", "read", "build", "get", "set",
    "put", "new", "old", "json", "text", "file", "files", "path", "node",
    "nodes", "list", "map", "array", "entry", "items", "item", "ctx", "self",
    "data", "root", "from", "into", "with", "count", "name", "type",
    "function", "string", "error", "context", "options", "config", "request",
    "response", "result", "results", "message", "messages", "command",
    "commands", "event", "events",
}

def identifier_terms(text):
    normalized = re.sub(r"[^0-9A-Za-z]+", " ", text)
    pieces = []
    for raw in normalized.split():
        pieces.extend(re.findall(
            r"[A-Z]+(?=[A-Z][a-z]|[0-9]|$)|[A-Z]?[a-z]+|[0-9]+",
            raw,
        ))
    out = []
    for piece in pieces:
        lowered = piece.lower()
        if len(lowered) >= 4 and lowered not in stop:
            out.append(lowered)
    return out

by_file = defaultdict(Counter)
term_files = defaultdict(set)
file_sloc = {}
file_funcs = {}

for module in modules:
    path = module.get("path", "")
    file_sloc[path] = int(module.get("sloc", 0))
    file_funcs[path] = int(module.get("funcs", 0))

for fn in functions:
    path = fn.get("file", "")
    for term in identifier_terms(fn.get("name", "")):
        by_file[path][term] += 2
        term_files[term].add(path)

print("semantic cohesion report")
print("mode: manual-report-only")

print("file_terms:")
for path in sorted(by_file):
    common = ", ".join(f"{term}:{count}" for term, count in by_file[path].most_common(8))
    print(f"- {path}: {common if common else 'none'}")

scattered = [
    (term, sorted(paths))
    for term, paths in term_files.items()
    if len(paths) >= 3
]
scattered.sort(key=lambda item: (-len(item[1]), item[0]))

print("scattered_terms:")
if scattered:
    for term, paths in scattered[:20]:
        print(f"- {term}: files={len(paths)} paths={', '.join(paths[:8])}")
else:
    print("- none")

mixed = []
for path, counts in by_file.items():
    if not counts:
        continue
    if sum(counts.values()) < 20 and file_sloc.get(path, 0) < 100 and file_funcs.get(path, 0) < 5:
        continue
    top = counts.most_common(5)
    total = sum(counts.values())
    dominant = top[0][1] / total if total else 0
    if len(top) >= 5 and dominant < 0.35:
        mixed.append((path, dominant, top))

print("mixed_concept_files:")
if mixed:
    for path, dominant, top in sorted(mixed, key=lambda item: (item[1], item[0]))[:20]:
        rendered = ", ".join(f"{term}:{count}" for term, count in top)
        print(f"- {path}: dominant_share={dominant:.2f} terms={rendered}")
else:
    print("- none")
PY
