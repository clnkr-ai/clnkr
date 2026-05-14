#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: scripts/sabotage-check.sh --patch <patch-file> --test-cmd <command>

Applies a deliberate sabotage patch, requires the test command to fail, reverts
the patch, then requires the same test command to pass. The working tree must be
clean before running.
EOF
}

patch_file=""
test_cmd=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --patch)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      patch_file="$2"
      shift 2
      ;;
    --test-cmd)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      test_cmd="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      exit 2
      ;;
  esac
done

[[ -n "$patch_file" && -n "$test_cmd" ]] || { usage; exit 2; }
[[ -f "$patch_file" ]] || { echo "error: patch file not found: $patch_file" >&2; exit 2; }

if [[ -n "$(git status --short)" ]]; then
  echo "error: working tree must be clean before sabotage check" >&2
  exit 1
fi

cleanup() {
  if git apply --reverse --check "$patch_file" >/dev/null 2>&1; then
    git apply --reverse "$patch_file"
  fi
}
trap cleanup EXIT

git apply --check "$patch_file"
git apply "$patch_file"

set +e
bash -lc "$test_cmd"
status=$?
set -e

if [[ "$status" -eq 0 ]]; then
  echo "error: sabotage patch did not make the test command fail" >&2
  exit 1
fi

cleanup
trap - EXIT

bash -lc "$test_cmd"
echo "sabotage check passed: sabotage failed as expected, restored test passed"
