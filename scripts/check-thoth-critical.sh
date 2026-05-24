#!/usr/bin/env bash

set -euo pipefail

target="${1:-.}"
bin="${THOTH_BIN:-thoth}"
critical_threshold="${THOTH_CRITICAL_THRESHOLD:-0.65}"

if [[ $# -gt 1 ]]; then
  echo "usage: $0 [target]" >&2
  exit 2
fi
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
trap 'rm -f "$report"' EXIT

"$bin" "$target" --json > "$report"

python3 - "$report" "$critical_threshold" <<'PY'
import json
import sys
from pathlib import Path

report_path = Path(sys.argv[1])
threshold_text = sys.argv[2]

try:
    threshold = float(threshold_text)
except ValueError:
    print(f"error: invalid THOTH_CRITICAL_THRESHOLD: {threshold_text}", file=sys.stderr)
    sys.exit(2)

try:
    data = json.loads(report_path.read_text())
except Exception as exc:
    print(f"error: thoth --json did not return valid JSON: {exc}", file=sys.stderr)
    sys.exit(1)

files = data.get("files")
if not isinstance(files, list):
    print("error: thoth --json missing files array", file=sys.stderr)
    sys.exit(1)

critical = []
for entry in files:
    try:
        risk = float(entry.get("risk", 0.0))
    except (TypeError, ValueError):
        print(f"error: invalid risk value in thoth output: {entry!r}", file=sys.stderr)
        sys.exit(1)
    if risk >= threshold:
        critical.append((risk, entry))

critical.sort(reverse=True, key=lambda item: item[0])

if critical:
    print(
        f"thoth critical check failed: {len(critical)} file(s) at or above risk {threshold:.3f}",
        file=sys.stderr,
    )
    for risk, entry in critical:
        print(
            f"- {entry.get('path', '<unknown>')}: "
            f"risk={risk:.3f} sloc={entry.get('sloc', 0)} "
            f"funcs={entry.get('funcs', 0)} worst={entry.get('worst', '')}",
            file=sys.stderr,
        )
    sys.exit(1)

print(f"thoth critical check passed: 0 files at or above risk {threshold:.3f}")
PY
