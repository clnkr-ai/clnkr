#!/usr/bin/env bash

set -euo pipefail

python3 - <<'PY'
from pathlib import Path
import re

root = Path(".")
test_impl_files = sorted(
    path
    for path in root.rglob("*_test.go")
    if path.is_file()
    and ".git" not in path.parts
    and ".worktrees" not in path.parts
    and "build" not in path.parts
)
fixture_files = sorted(
    path
    for path in list(root.glob("testdata/**/*")) + list(root.glob("**/testdata/**/*")) + list(root.glob("evaluations/suites/**/*"))
    if path.is_file()
    and ".git" not in path.parts
    and ".worktrees" not in path.parts
    and "build" not in path.parts
)

def read(path):
    return path.read_text(errors="ignore")

mock_terms = re.compile(r"(?<![A-Za-z0-9_-])(mock|stub|fake)(?![A-Za-z0-9_-])", re.IGNORECASE)
helper_terms = re.compile(r"^\s*func\s+[A-Za-z_][A-Za-z0-9_]*\s*\(", re.MULTILINE)
assert_terms = re.compile(r"\b(t\.Fatalf|t\.Fatal|t\.Errorf|t\.Error|cmp\.Diff|reflect\.DeepEqual|strings\.Contains)\b")
external_signals = {
    "httptest_server": re.compile(r"\bhttptest\.NewServer\b"),
    "real_shell_exec": re.compile(r"\bexec\.Command\b|/bin/sh|bash"),
    "temp_filesystem": re.compile(r"\bt\.TempDir\b|os\.CreateTemp|os\.WriteFile|os\.Mkdir"),
    "golden_or_fixture": re.compile(r"\btestdata\b|fixture|golden|snapshot", re.IGNORECASE),
    "real_cli_process": re.compile(r"\bos\.Args\b|runMainHelper|runClnkrdHelper"),
}

print("test fidelity report")
print("mode: manual-report-only")
print(f"test_impl_files={len(test_impl_files)} fixtures={len(set(fixture_files))}")

print("test_impl:")
for path in test_impl_files:
    text = read(path)
    line_count = text.count("\n") + (1 if text else 0)
    helpers = len(helper_terms.findall(text))
    assertions = len(assert_terms.findall(text))
    mocks = len(mock_terms.findall(text))
    signal_hits = [name for name, pattern in external_signals.items() if pattern.search(text)]
    print(
        f"- {path}: lines={line_count} helper_defs={helpers} "
        f"assertions={assertions} mock_words={mocks} "
        f"integration_signals={','.join(signal_hits) if signal_hits else 'none'}"
    )

print("fixture_summary:")
by_suffix = {}
for path in set(fixture_files):
    by_suffix[path.suffix or "<none>"] = by_suffix.get(path.suffix or "<none>", 0) + 1
if by_suffix:
    for suffix, count in sorted(by_suffix.items()):
        print(f"- {suffix}: {count}")
else:
    print("- none")

print("mock_or_fake_mentions:")
found = False
for path in test_impl_files:
    for lineno, line in enumerate(read(path).splitlines(), start=1):
        if mock_terms.search(line):
            print(f"- {path}:{lineno}: {line.strip()[:160]}")
            found = True
if not found:
    print("- none")

print("integration_fidelity_signals:")
all_text = "\n".join(read(path) for path in test_impl_files)
for name, pattern in external_signals.items():
    print(f"- {name}: {'yes' if pattern.search(all_text) else 'no'}")

print("fail_before_pass_evidence:")
print("- not detected automatically; record sabotage or mutation command in review notes when changing behavior")
PY
