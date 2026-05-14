#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture="$(mktemp -d)"
cleanup() {
  rm -rf "$fixture"
}
trap cleanup EXIT

cp "$repo_root/scripts/check-architecture-imports.sh" "$fixture/check-architecture-imports.sh"
chmod +x "$fixture/check-architecture-imports.sh"

cd "$fixture"
cat > go.mod <<'MOD'
module example.test/clnkr

go 1.24
MOD

mkdir -p cmd/internal/clnkrapp internal/providers/openai
cat > internal/providers/openai/openai.go <<'GO'
package openai

type Model struct{}
GO
cat > cmd/internal/clnkrapp/clnkrapp.go <<'GO'
package clnkrapp

import _ "example.test/clnkr/internal/providers/openai"
GO

set +e
output="$(./check-architecture-imports.sh 2>&1)"
status=$?
set -e

if [[ $status -ne 1 ]]; then
  echo "expected invalid fixture to fail with status 1, got $status" >&2
  echo "$output" >&2
  exit 1
fi

for expected in \
  "error: architecture import boundary violation" \
  "rule: ARCH010 frontend-provider-construction" \
  "importer: example.test/clnkr/cmd/internal/clnkrapp" \
  "target: example.test/clnkr/internal/providers/openai" \
  "import_source: imports" \
  "trusted_rule: frontend coordinator must use internal/providerfactory instead of concrete provider adapters." \
  "source_fact: go list reported importer imports target." \
  "guidance: move provider construction behind internal/providerfactory; do not import concrete provider adapters from frontend packages."
do
  if [[ "$output" != *"$expected"* ]]; then
    echo "missing expected output: $expected" >&2
    echo "$output" >&2
    exit 1
  fi
done

mkdir -p cmd/internal/providerconfig internal/providerfactory internal/providers/providerconfig
cat > internal/providers/providerconfig/providerconfig.go <<'GO'
package providerconfig
GO
cat > cmd/internal/providerconfig/providerconfig.go <<'GO'
package providerconfig

import _ "example.test/clnkr/internal/providers/providerconfig"
GO
cat > internal/providerfactory/providerfactory.go <<'GO'
package providerfactory

import _ "example.test/clnkr/internal/providers/openai"
GO
cat > cmd/internal/clnkrapp/clnkrapp.go <<'GO'
package clnkrapp

import (
	_ "example.test/clnkr/internal/providerfactory"
	_ "example.test/clnkr/internal/session"
)
GO

mkdir -p cmd/internal/other
cat > cmd/internal/other/other.go <<'GO'
package other

import _ "example.test/clnkr/internal/providers/openai"
GO

set +e
output="$(./check-architecture-imports.sh 2>&1)"
status=$?
set -e

if [[ $status -ne 1 ]]; then
  echo "expected generic cmd concrete-provider import to fail with status 1, got $status" >&2
  echo "$output" >&2
  exit 1
fi

expected="cmd/... may import only root clnkr, cmd/internal/..., internal/providers/providerconfig, internal/session from cmd/internal/clnkrapp, or internal/providerfactory from clnkrapp"
if [[ "$output" != *"$expected"* ]]; then
  echo "missing expected output: $expected" >&2
  echo "$output" >&2
  exit 1
fi

rm -rf cmd/internal/other

mkdir -p cmd/clnkr internal/session
cat > internal/session/session.go <<'GO'
package session
GO
cat > cmd/clnkr/main.go <<'GO'
package main

import _ "example.test/clnkr/internal/session"
GO

set +e
output="$(./check-architecture-imports.sh 2>&1)"
status=$?
set -e

if [[ $status -ne 1 ]]; then
  echo "expected frontend session import to fail with status 1, got $status" >&2
  echo "$output" >&2
  exit 1
fi

for expected in \
  "error: architecture import boundary violation" \
  "rule: ARCH011 frontend-session-boundary" \
  "importer: example.test/clnkr/cmd/clnkr" \
  "target: example.test/clnkr/internal/session" \
  "import_source: imports" \
  "trusted_rule: frontend adapters must use cmd/internal/clnkrapp instead of importing internal/session directly." \
  "source_fact: go list reported importer imports target." \
  "guidance: move session persistence calls behind cmd/internal/clnkrapp; do not import internal/session from cmd/... outside cmd/internal/clnkrapp."
do
  if [[ "$output" != *"$expected"* ]]; then
    echo "missing expected output: $expected" >&2
    echo "$output" >&2
    exit 1
  fi
done

rm -rf cmd/clnkr

mkdir -p cmd/clnkr cmd/internal/clnkrapp
cat > cmd/internal/clnkrapp/clnkrapp.go <<'GO'
package clnkrapp
GO
cat > cmd/clnkr/cli_options.go <<'GO'
package main

import _ "example.test/clnkr/cmd/internal/clnkrapp"
GO

set +e
output="$(./check-architecture-imports.sh 2>&1)"
status=$?
set -e

if [[ $status -ne 1 ]]; then
  echo "expected CLI parser app import to fail with status 1, got $status" >&2
  echo "$output" >&2
  exit 1
fi

for expected in \
  "error: architecture import boundary violation" \
  "rule: ARCH012 cli-parser-boundary" \
  "importer: cmd/clnkr/cli_options.go" \
  "target: example.test/clnkr/cmd/internal/clnkrapp" \
  "import_source: file_imports" \
  "trusted_rule: CLI option parsing must stay local and stdlib-only." \
  "source_fact: file import scan reported importer imports target." \
  "guidance: keep cmd/clnkr/cli_*.go parser-only; move app-service and config-resolution calls out of the parser."
do
  if [[ "$output" != *"$expected"* ]]; then
    echo "missing expected output: $expected" >&2
    echo "$output" >&2
    exit 1
  fi
done

cat > cmd/clnkr/cli_options.go <<'GO'
package main

import "flag"

func valueFlag(arg string) bool {
	switch arg {
	case "-p":
		return true
	default:
		return false
	}
}

var _ = flag.ContinueOnError
GO

cat >> go.mod <<'MOD'

require foo v0.0.0

replace foo => ./foo
MOD
mkdir -p foo/pkg
cat > foo/go.mod <<'MOD'
module foo

go 1.24
MOD
cat > foo/pkg/pkg.go <<'GO'
package pkg
GO
cat > cmd/clnkr/cli_options.go <<'GO'
package main

import _ "foo/pkg"
GO

set +e
output="$(./check-architecture-imports.sh 2>&1)"
status=$?
set -e

if [[ $status -ne 1 ]]; then
  echo "expected CLI parser no-dot non-stdlib import to fail with status 1, got $status" >&2
  echo "$output" >&2
  exit 1
fi

for expected in \
  "error: architecture import boundary violation" \
  "rule: ARCH012 cli-parser-boundary" \
  "importer: cmd/clnkr/cli_options.go" \
  "target: foo/pkg" \
  "import_source: file_imports"
do
  if [[ "$output" != *"$expected"* ]]; then
    echo "missing expected output: $expected" >&2
    echo "$output" >&2
    exit 1
  fi
done

cat > cmd/clnkr/cli_options.go <<'GO'
package main

import "flag"

var _ = flag.ContinueOnError
GO

cat >> go.mod <<'MOD'

require example.com/notstdlib v0.0.0

replace example.com/notstdlib => ./notstdlib
MOD
mkdir -p notstdlib/pkg
cat > notstdlib/go.mod <<'MOD'
module example.com/notstdlib

go 1.24
MOD
cat > notstdlib/pkg/pkg.go <<'GO'
package pkg
GO
cat > cmd/clnkr/cli_options.go <<'GO'
package main

import _ "example.com/notstdlib/pkg"
GO

set +e
output="$(./check-architecture-imports.sh 2>&1)"
status=$?
set -e

if [[ $status -ne 1 ]]; then
  echo "expected CLI parser non-stdlib import to fail with status 1, got $status" >&2
  echo "$output" >&2
  exit 1
fi

for expected in \
  "error: architecture import boundary violation" \
  "rule: ARCH012 cli-parser-boundary" \
  "importer: cmd/clnkr/cli_options.go" \
  "target: example.com/notstdlib/pkg" \
  "import_source: file_imports"
do
  if [[ "$output" != *"$expected"* ]]; then
    echo "missing expected output: $expected" >&2
    echo "$output" >&2
    exit 1
  fi
done

cat > cmd/clnkr/cli_options.go <<'GO'
package main

import "flag"

var _ = flag.ContinueOnError
GO

cat > cmd/clnkr/cli_helper.go <<'GO'
package main

import _ "example.com/notstdlib/pkg"
GO

set +e
output="$(./check-architecture-imports.sh 2>&1)"
status=$?
set -e

if [[ $status -ne 1 ]]; then
  echo "expected CLI parser helper non-stdlib import to fail with status 1, got $status" >&2
  echo "$output" >&2
  exit 1
fi

for expected in \
  "error: architecture import boundary violation" \
  "rule: ARCH012 cli-parser-boundary" \
  "importer: cmd/clnkr/cli_helper.go" \
  "target: example.com/notstdlib/pkg" \
  "import_source: file_imports"
do
  if [[ "$output" != *"$expected"* ]]; then
    echo "missing expected output: $expected" >&2
    echo "$output" >&2
    exit 1
  fi
done

rm -f cmd/clnkr/cli_helper.go

output="$(./check-architecture-imports.sh 2>&1)"
if [[ "$output" != *"target architecture import checks passed"* ]]; then
  echo "missing success output" >&2
  echo "$output" >&2
  exit 1
fi
