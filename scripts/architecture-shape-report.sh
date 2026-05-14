#!/usr/bin/env bash

set -euo pipefail

target="."
target_set=0
baseline="${THOTH_ARCH_BASELINE:-}"
write_report=""
bin="${THOTH_BIN:-thoth}"

usage() {
  echo "usage: $0 [--target .] [--baseline previous-report.json] [--write-report current-report.json]" >&2
}

set_target() {
  if [[ "$target_set" -eq 1 ]]; then
    echo "error: target specified more than once" >&2
    exit 2
  fi
  target="$1"
  target_set=1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --baseline)
      [[ $# -ge 2 ]] || { echo "error: --baseline requires a report path" >&2; exit 2; }
      baseline="$2"
      shift 2
      ;;
    --target)
      [[ $# -ge 2 ]] || { echo "error: --target requires a path" >&2; exit 2; }
      set_target "$2"
      shift 2
      ;;
    --write-report)
      [[ $# -ge 2 ]] || { echo "error: --write-report requires a path" >&2; exit 2; }
      write_report="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      echo "error: unknown option: $1" >&2
      usage
      exit 2
      ;;
    *)
      set_target "$1"
      shift
      ;;
  esac
done

if [[ ! -e "$target" ]]; then
  echo "error: target not found: $target" >&2
  exit 1
fi
if [[ ! -d "$target" ]]; then
  echo "error: target must be a directory: $target" >&2
  exit 1
fi
if [[ -n "$baseline" ]]; then
  if [[ ! -f "$baseline" ]]; then
    echo "error: baseline not found: $baseline" >&2
    exit 1
  fi
  python3 - "$baseline" <<'PY'
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
try:
    json.loads(path.read_text())
except Exception as exc:
    print(f"error: invalid baseline JSON: {path}: {exc}", file=sys.stderr)
    sys.exit(1)
PY
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

ignored_dirs = {".git", ".worktrees", "build", "site/public"}

def ignored(path, names):
    current = Path(path)
    rejected = set()
    for name in names:
        child = current / name
        rel = child.relative_to(source) if source in child.parents or child == source else child
        rel_text = rel.as_posix()
        if name in {".git", ".worktrees", "build"} or rel_text == "site/public":
            rejected.add(name)
        elif name.endswith("_test.go"):
            rejected.add(name)
    return rejected

shutil.copytree(source, dest, ignore=ignored)
print(dest)
PY
)"

"$bin" "$analysis_target" --report > "$report"

output="$(python3 - "$report" "$baseline" "$target" <<'PY'
import json
import math
import statistics
import sys
from pathlib import Path

report_path = Path(sys.argv[1])
baseline_path = Path(sys.argv[2]) if len(sys.argv) > 2 and sys.argv[2] else None
target = sys.argv[3]
data = json.loads(report_path.read_text())
summary = data.get("summary", {})
architecture = data.get("architecture", [])
modules = data.get("module_health", [])
cycles = data.get("dependency_cycles", [])

print("architecture shape report")
print(f"target: {target} production Go files")
print(
    "summary: "
    f"files={summary.get('files', 0)} "
    f"sloc={summary.get('total_sloc', 0)} "
    f"functions={summary.get('functions', 0)} "
    f"dep_edges={summary.get('dep_edges', 0)} "
    f"cycles={summary.get('cycles', 0)}"
)

ca_values = [int(entry.get("ca", 0)) for entry in architecture]
mean_ca = statistics.mean(ca_values) if ca_values else 0.0
std_ca = statistics.pstdev(ca_values) if len(ca_values) > 1 else 0.0
hub_threshold = mean_ca + (2 * std_ca)
hubs = [
    entry
    for entry in architecture
    if int(entry.get("ca", 0)) >= 5
    and (int(entry.get("ca", 0)) > hub_threshold or entry.get("role") == "hub")
]

print(f"ca: mean={mean_ca:.2f} stddev={std_ca:.2f} hub_threshold={hub_threshold:.2f}")
if hubs:
    print("hubs:")
    for entry in hubs:
        print(
            f"- {entry.get('path')}: "
            f"ca={entry.get('ca')} ce={entry.get('ce')} "
            f"instability={entry.get('instability')} role={entry.get('role')}"
        )
else:
    print("hubs: none")

if cycles:
    print("dependency_cycles:")
    for cycle in cycles[:10]:
        print("- " + " -> ".join(cycle))
    if len(cycles) > 10:
        print(f"- ... {len(cycles) - 10} more")
else:
    print("dependency_cycles: none")

slocs = sorted(int(module.get("sloc", 0)) for module in modules)
if slocs:
    buckets = {
        "<50": sum(1 for value in slocs if value < 50),
        "50-100": sum(1 for value in slocs if 50 <= value < 100),
        "100-200": sum(1 for value in slocs if 100 <= value < 200),
        "200-400": sum(1 for value in slocs if 200 <= value < 400),
        "400+": sum(1 for value in slocs if value >= 400),
    }
    mean_sloc = statistics.mean(slocs)
    median_sloc = statistics.median(slocs)
    numerator = sum((2 * idx - len(slocs) - 1) * value for idx, value in enumerate(slocs, start=1))
    denominator = len(slocs) * sum(slocs)
    gini = numerator / denominator if denominator else 0.0
    print(
        "sloc_distribution: "
        + " ".join(f"{name}={value}" for name, value in buckets.items())
    )
    print(
        f"sloc_shape: gini={gini:.3f} "
        f"median_mean_ratio={(median_sloc / mean_sloc if mean_sloc else math.nan):.3f}"
    )
else:
    print("sloc_distribution: no modules")

print("mode: manual-report-only")

if baseline_path:
    baseline = json.loads(baseline_path.read_text())
    base_summary = baseline.get("summary", {})
    baseline_arch_entries = baseline.get("architecture", [])
    base_architecture = {entry.get("path"): entry for entry in baseline_arch_entries}
    current_architecture = {entry.get("path"): entry for entry in architecture}
    base_ca_values = [int(entry.get("ca", 0)) for entry in baseline_arch_entries]
    base_mean_ca = statistics.mean(base_ca_values) if base_ca_values else 0.0
    base_std_ca = statistics.pstdev(base_ca_values) if len(base_ca_values) > 1 else 0.0
    base_hub_threshold = base_mean_ca + (2 * base_std_ca)
    base_hubs = {
        entry.get("path")
        for entry in baseline_arch_entries
        if int(entry.get("ca", 0)) >= 5
        and (int(entry.get("ca", 0)) > base_hub_threshold or entry.get("role") == "hub")
    }
    current_hubs = {entry.get("path") for entry in hubs}

    print("budget_deltas:")
    for key in ("files", "total_sloc", "functions", "dep_edges", "cycles"):
        current = int(summary.get(key, 0))
        previous = int(base_summary.get(key, 0))
        print(f"- {key}: current={current} baseline={previous} delta={current - previous}")

    new_hubs = sorted(path for path in current_hubs if path not in base_hubs)
    print(f"- new_hubs: {', '.join(new_hubs) if new_hubs else 'none'}")

    ca_growth = []
    for path, current in current_architecture.items():
        previous = base_architecture.get(path)
        if not previous:
            continue
        delta = int(current.get("ca", 0)) - int(previous.get("ca", 0))
        if delta > 0:
            ca_growth.append((path, delta, current.get("ca"), previous.get("ca")))
    if ca_growth:
        print("- ca_growth:")
        for path, delta, current, previous in sorted(ca_growth, key=lambda item: (-item[1], item[0]))[:20]:
            print(f"  - {path}: current={current} baseline={previous} delta={delta}")
    else:
        print("- ca_growth: none")
else:
    print("budget_deltas: no baseline supplied")
PY
)"

echo "$output"

if [[ -n "$write_report" ]]; then
  tmp_write="$(mktemp)"
  cp "$report" "$tmp_write"
  mv "$tmp_write" "$write_report"
fi
