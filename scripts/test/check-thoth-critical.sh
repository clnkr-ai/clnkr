#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="$repo_root/scripts/check-thoth-critical.sh"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

fake_thoth="$tmpdir/thoth"
cat > "$fake_thoth" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
case "${THOTH_FAKE_CASE:-ok}" in
  ok)
    cat <<'JSON'
{"files":[{"path":"ok.go","risk":0.649,"sloc":100,"funcs":4,"worst":"ok"}]}
JSON
    ;;
  critical)
    cat <<'JSON'
{"files":[{"path":"critical.go","risk":0.650,"sloc":101,"funcs":5,"worst":"critical"}]}
JSON
    ;;
  invalid)
    printf '{not json'
    ;;
esac
SH
chmod +x "$fake_thoth"

output="$(THOTH_BIN="$fake_thoth" THOTH_FAKE_CASE=ok "$script" "$repo_root")"
[[ "$output" == "thoth critical check passed: 0 files at or above risk 0.650" ]]

if THOTH_BIN="$fake_thoth" THOTH_FAKE_CASE=critical "$script" "$repo_root" >"$tmpdir/out" 2>"$tmpdir/err"; then
  echo "expected critical fixture to fail" >&2
  exit 1
fi
grep -F "thoth critical check failed: 1 file(s) at or above risk 0.650" "$tmpdir/err" >/dev/null
grep -F "critical.go: risk=0.650" "$tmpdir/err" >/dev/null

if THOTH_BIN="$fake_thoth" THOTH_FAKE_CASE=invalid "$script" "$repo_root" >"$tmpdir/out" 2>"$tmpdir/err"; then
  echo "expected invalid JSON fixture to fail" >&2
  exit 1
fi
grep -F "error: thoth --json did not return valid JSON" "$tmpdir/err" >/dev/null

echo "thoth critical check script tests passed"
