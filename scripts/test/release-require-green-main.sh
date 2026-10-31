#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="$repo_root/scripts/release-require-green-main.sh"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

mkdir -p "$tmpdir/bin" "$tmpdir/repo"
git -C "$tmpdir/repo" init -q
git -C "$tmpdir/repo" config user.name "Release test"
git -C "$tmpdir/repo" config user.email "release@example.test"
# Fixture commits must not prompt for the maintainer's signing key.
git -C "$tmpdir/repo" config commit.gpgsign false
cat >"$tmpdir/repo/CHANGELOG.md" <<'EOF'
# Changelog

## 0.3.20 (2026-11-01)

- Prepare changelogs before tagging releases.

## 0.3.19 (2026-10-31)

- Previous release.
EOF
git -C "$tmpdir/repo" add CHANGELOG.md
git -C "$tmpdir/repo" commit -qm "Prepare release fixture"
release_sha="$(git -C "$tmpdir/repo" rev-parse HEAD)"

cat >"$tmpdir/bin/gh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail

echo "$*" >>"$GH_CALLS"

case "$*" in
  *'/git/ref/heads/main')
    if [[ "${SCENARIO:-}" == "empty-main" ]]; then
      echo '{"object":{}}'
    else
      printf '{"object":{"sha":"%s"}}\n' "$TEST_MAIN_SHA"
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
    echo '{"workflow_runs":[{"status":"queued","conclusion":null,"head_sha":"'"$TEST_MAIN_SHA"'","html_url":"https://example.test/ci"}]}'
    ;;
  failed:ci|site-failed:site)
    echo "{\"workflow_runs\":[{\"status\":\"completed\",\"conclusion\":\"failure\",\"head_sha\":\"${TEST_MAIN_SHA}\",\"html_url\":\"https://example.test/$workflow\"}]}"
    ;;
  *)
    echo "{\"workflow_runs\":[{\"status\":\"completed\",\"conclusion\":\"success\",\"head_sha\":\"${TEST_MAIN_SHA}\",\"html_url\":\"https://example.test/$workflow\"}]}"
    ;;
esac
SH
chmod +x "$tmpdir/bin/gh"

run_preflight() (
  cd "$tmpdir/repo"
  : >"$tmpdir/calls"
  PATH="$tmpdir/bin:$PATH" \
    GH_CALLS="$tmpdir/calls" \
    GITHUB_REPOSITORY=clnkr-ai/clnkr \
    GITHUB_REF_NAME="${RELEASE_TAG:-0.3.20}" \
    GITHUB_SHA="${RELEASE_SHA:-$release_sha}" \
    TEST_MAIN_SHA="${MAIN_SHA:-$release_sha}" \
    SCENARIO="${1:-valid}" \
    "$script" >"$tmpdir/stdout" 2>"$tmpdir/stderr"
)

expect_failure() {
  local scenario="$1"
  if run_preflight "$scenario"; then
    echo "expected $scenario to block release" >&2
    exit 1
  fi
}

run_preflight valid
cat >"$tmpdir/want-calls" <<EOF
api /repos/clnkr-ai/clnkr/git/ref/heads/main
api /repos/clnkr-ai/clnkr/actions/workflows/ci.yml/runs?branch=main&event=push&head_sha=${release_sha}&per_page=1
api /repos/clnkr-ai/clnkr/actions/workflows/site.yml/runs?branch=main&event=push&head_sha=${release_sha}&per_page=1
EOF
diff -u "$tmpdir/want-calls" "$tmpdir/calls"

expect_failure empty-main
expect_failure no-runs
expect_failure missing-sha
expect_failure mismatched-sha
expect_failure queued
expect_failure failed
expect_failure site-failed

# Only the tagged commit may satisfy preparation, never a working-tree edit.
RELEASE_TAG=0.3.21 expect_failure stale-changelog
grep -qF "CHANGELOG.md" "$tmpdir/stderr"
[[ ! -s "$tmpdir/calls" ]]
sed 's/0.3.20/0.3.21/' "$tmpdir/repo/CHANGELOG.md" >"$tmpdir/new-changelog"
cp "$tmpdir/new-changelog" "$tmpdir/repo/CHANGELOG.md"
RELEASE_TAG=0.3.21 expect_failure uncommitted-changelog
MAIN_SHA=other-sha expect_failure tag-not-main
grep -qF "tagged commit" "$tmpdir/stderr"

# A matching heading without release notes must not borrow an older entry's notes.
cat >"$tmpdir/repo/CHANGELOG.md" <<'EOF'
# Changelog

## 0.3.20 (2026-11-01)

## 0.3.19 (2026-10-31)

- Previous release.
EOF
git -C "$tmpdir/repo" commit -qam "Empty release fixture"
empty_sha="$(git -C "$tmpdir/repo" rev-parse HEAD)"
RELEASE_SHA="$empty_sha" MAIN_SHA="$empty_sha" expect_failure empty-entry
grep -qF "release notes" "$tmpdir/stderr"

if GITHUB_REPOSITORY=clnkr-ai/clnkr "$script" unexpected >/dev/null 2>&1; then
  echo "expected arguments to be rejected" >&2
  exit 1
fi

if env -u GITHUB_REPOSITORY "$script" >/dev/null 2>&1; then
  echo "expected missing GITHUB_REPOSITORY to be rejected" >&2
  exit 1
fi

echo "release green-main preflight script tests passed"
