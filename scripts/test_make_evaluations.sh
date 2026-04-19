#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MAKE_BIN="$(command -v make)"
AWK_BIN="$(command -v awk)"
MIN_VERSION="$(sed -n 's/^CLANKERVAL_MIN_VERSION := //p' "$ROOT/Makefile")"

if [ -z "$MIN_VERSION" ]; then
    echo "error: failed to read CLANKERVAL_MIN_VERSION from Makefile" >&2
    exit 1
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT
bindir="$tmpdir/bin"
mkdir -p "$bindir"
ln -s "$AWK_BIN" "$bindir/awk"

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

run_make() {
    PATH="$bindir" "$MAKE_BIN" -C "$ROOT" "$@"
}

assert_contains() {
    local haystack="$1"
    local needle="$2"
    if [[ "$haystack" != *"$needle"* ]]; then
        fail "expected output to contain: $needle"$'\n'"got:"$'\n'"$haystack"
    fi
}

assert_not_contains() {
    local haystack="$1"
    local needle="$2"
    if [[ "$haystack" == *"$needle"* ]]; then
        fail "expected output to omit: $needle"$'\n'"got:"$'\n'"$haystack"
    fi
}

write_runner() {
    local version="$1"
    local version_status="${2:-0}"
    cat >"$bindir/clankerval" <<EOF
#!/bin/sh
set -eu
if [ "\${1-}" = "--version" ]; then
    printf 'clankerval version %s\n' "$version"
    exit "$version_status"
fi
printf '%s\n' "\$*" >"$tmpdir/args"
printf '%s\n' "\${CLNKR_EVALUATION_MODE-}" >"$tmpdir/mode"
exit 0
EOF
    chmod 755 "$bindir/clankerval"
}

rm -f "$bindir/clankerval"
if output="$(run_make evaluations 2>&1)"; then
    fail "make evaluations unexpectedly succeeded without clankerval"
fi
assert_contains "$output" "clankerval >= $MIN_VERSION is required."
assert_not_contains "$output" "install-clankerval.sh"
assert_not_contains "$output" "require-clankerval.py"

write_runner "0.0.0"
if output="$(run_make evaluations 2>&1)"; then
    fail "make evaluations unexpectedly succeeded with an old clankerval"
fi
assert_contains "$output" "clankerval >= $MIN_VERSION is required."
assert_contains "$output" "found clankerval 0.0.0"

write_runner "$MIN_VERSION" 1
rm -f "$tmpdir/args"
if output="$(run_make evaluations 2>&1)"; then
    fail "make evaluations unexpectedly succeeded when clankerval --version failed"
fi
assert_contains "$output" "clankerval --version failed:"
if [ -f "$tmpdir/args" ]; then
    fail "make evaluations ran clankerval even though the version probe failed"
fi

write_runner "$MIN_VERSION"
run_make evaluations >/dev/null
assert_contains "$(cat "$tmpdir/args")" "run --suite default"

run_make evaluations-live >/dev/null
assert_contains "$(cat "$tmpdir/args")" "run --suite default"
if [ "$(cat "$tmpdir/mode")" != "live-provider" ]; then
    fail "expected evaluations-live to export CLNKR_EVALUATION_MODE=live-provider"
fi

printf 'ok\n'
