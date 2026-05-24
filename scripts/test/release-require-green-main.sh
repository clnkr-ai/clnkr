#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="$repo_root/scripts/release-require-green-main.sh"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

mkdir -p "$tmpdir/bin"
cat > "$tmpdir/bin/gh" <<'SH'
#!/usr/bin/env bash
case "$*" in
  *site.yml*) workflow=site ;;
  *) workflow=ci ;;
esac
if [[ "${GREEN_MAIN_FAIL:-}" == "$workflow" ]]; then
  echo '{"workflow_runs":[{"status":"completed","conclusion":"failure","head_sha":"bad","html_url":"https://example.test"}]}'
else
  echo '{"workflow_runs":[{"status":"completed","conclusion":"success","head_sha":"ok","html_url":"https://example.test"}]}'
fi
SH
chmod +x "$tmpdir/bin/gh"

PATH="$tmpdir/bin:$PATH" GITHUB_REPOSITORY=clnkr-ai/clnkr "$script" >/dev/null

if PATH="$tmpdir/bin:$PATH" GITHUB_REPOSITORY=clnkr-ai/clnkr GREEN_MAIN_FAIL=site "$script" >/dev/null 2>&1; then
  echo "expected failed site run to block release" >&2
  exit 1
fi

echo "release green-main preflight script tests passed"
