#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="$repo_root/scripts/release-require-green-main.sh"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

mkdir -p "$tmpdir/bin"
cat >"$tmpdir/bin/gh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail

echo "$*" >>"$GH_CALLS"

case "$*" in
  *'/git/ref/heads/main')
    if [[ "${SCENARIO:-}" == "empty-main" ]]; then
      echo '{"object":{}}'
    else
      echo '{"object":{"sha":"snapshot-sha"}}'
    fi
    exit 0
    ;;
  *'/actions/workflows/ci.yml/runs?'*) workflow=ci ;;
  *'/actions/workflows/site.yml/runs?'*) workflow=site ;;
  *)
    echo "unexpected gh call: $*" >&2
    exit 2
    ;;
esac

case "${SCENARIO:-valid}:$workflow" in
  no-runs:ci)
    echo '{"workflow_runs":[]}'
    ;;
  missing-sha:ci)
    echo '{"workflow_runs":[{"status":"completed","conclusion":"success","html_url":"https://example.test/ci"}]}'
    ;;
  mismatched-sha:ci)
    echo '{"workflow_runs":[{"status":"completed","conclusion":"success","head_sha":"other-sha","html_url":"https://example.test/ci"}]}'
    ;;
  queued:ci)
    echo '{"workflow_runs":[{"status":"queued","conclusion":null,"head_sha":"snapshot-sha","html_url":"https://example.test/ci"}]}'
    ;;
  failed:ci|site-failed:site)
    echo "{\"workflow_runs\":[{\"status\":\"completed\",\"conclusion\":\"failure\",\"head_sha\":\"snapshot-sha\",\"html_url\":\"https://example.test/$workflow\"}]}"
    ;;
  *)
    echo "{\"workflow_runs\":[{\"status\":\"completed\",\"conclusion\":\"success\",\"head_sha\":\"snapshot-sha\",\"html_url\":\"https://example.test/$workflow\"}]}"
    ;;
esac
SH
chmod +x "$tmpdir/bin/gh"

run_preflight() {
  : >"$tmpdir/calls"
  PATH="$tmpdir/bin:$PATH" \
    GH_CALLS="$tmpdir/calls" \
    GITHUB_REPOSITORY=clnkr-ai/clnkr \
    SCENARIO="${1:-valid}" \
    "$script" >"$tmpdir/stdout" 2>"$tmpdir/stderr"
}

expect_failure() {
  local scenario="$1"
  if run_preflight "$scenario"; then
    echo "expected $scenario to block release" >&2
    exit 1
  fi
}

run_preflight valid
cat >"$tmpdir/want-calls" <<'EOF'
api /repos/clnkr-ai/clnkr/git/ref/heads/main
api /repos/clnkr-ai/clnkr/actions/workflows/ci.yml/runs?branch=main&event=push&head_sha=snapshot-sha&per_page=1
api /repos/clnkr-ai/clnkr/actions/workflows/site.yml/runs?branch=main&event=push&head_sha=snapshot-sha&per_page=1
EOF
diff -u "$tmpdir/want-calls" "$tmpdir/calls"

expect_failure empty-main
expect_failure no-runs
expect_failure missing-sha
expect_failure mismatched-sha
expect_failure queued
expect_failure failed
expect_failure site-failed

if GITHUB_REPOSITORY=clnkr-ai/clnkr "$script" unexpected >/dev/null 2>&1; then
  echo "expected arguments to be rejected" >&2
  exit 1
fi

if env -u GITHUB_REPOSITORY "$script" >/dev/null 2>&1; then
  echo "expected missing GITHUB_REPOSITORY to be rejected" >&2
  exit 1
fi

echo "release green-main preflight script tests passed"
