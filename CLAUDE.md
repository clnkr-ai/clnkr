# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

hew is a minimal coding agent CLI that queries LLMs and executes bash commands in a loop. Single Go binary, zero external dependencies beyond stdlib. Dual-provider support: Anthropic Messages API and OpenAI-compatible chat completions.

## Commands

```bash
make build          # Compile binary (version from git describe)
make test           # Run all tests (go test ./... -v)
make check          # Run lint + tests
make lint           # Run golangci-lint
make setup          # Install git hooks
make fmt            # Format source
make help           # List all targets

# Run a single test
go test . -v -run TestExtractCommand

# Build with explicit version
make build VERSION=0.2.0
```

**Before committing:** Run `make fmt` then `make check`. Do not commit if either fails.

## Architecture

The core is an importable library at module root (`github.com/cosgroveb/hew`). `cmd/hew/main.go` is the first consumer — a thin CLI entry point that wires components and provides output formatting via the `Notify` callback.

**Package structure:**
```
hew/                    # core: types, interfaces, Agent, events
├── anthropic/          # Anthropic Messages API adapter
├── openai/             # OpenAI-compatible adapter
└── cmd/hew/            # CLI consumer
```

**Two-tier Agent API:**
- `Step(ctx) (StepResult, error)` — single query-parse-execute cycle, policy-free. For advanced consumers (ToT, research harnesses).
- `Run(ctx, task) error` — thin policy loop over `Step()`. Handles format error counting (exit on 2 consecutive) and step limits.

**Event system:** `Notify func(Event)` callback on Agent. Sealed interface (unexported marker method) with five event types: `EventResponse`, `EventCommandStart`, `EventCommandDone`, `EventFormatError`, `EventDebug`. Nil Notify = silent. `cmd/hew/main.go` sets it to print to stdout/stderr.

**Core interfaces:**
- `Model` — `Query(ctx, []Message) (Response, error)` — implemented by `anthropic.Model` and `openai.Model`
- `Executor` — `Execute(ctx, command, dir) (string, error)` — implemented by `CommandExecutor`

**Provider selection**: `main.go` checks if `--base-url` contains `anthropic.com` → Anthropic adapter, everything else → OpenAI-compatible adapter. Both use raw `net/http` with `httptest` servers in tests.

**Parser** (`parser.go`): `ExtractCommand` extracts the first `` ```bash `` fenced block via regex. Returns `ErrNoCommand` if none found.

**System prompt** (`prompt.go`): Hardcoded base prompt + optional `AGENTS.md` file content from the working directory.

## Testing Patterns

- Provider adapters tested with `httptest.NewServer` in external test packages (`package anthropic_test`) — black-box, no real API calls
- Agent loop tested with `fakeModel` and `fakeExecutor` (defined in `agent_test.go`) — deterministic inputs for state machine testing
- Executor tests are real integration tests that shell out to bash
- macOS `/private/tmp` symlink is handled in executor tests

## Key Design Decisions

- Core is a public library, not `internal/` — extensions (TUI, alternative loop strategies) compose onto it
- `Step()` exposes the loop primitive; `Run()` adds policy. Shared logic lives in `Step()`.
- Output flows through typed events, not `io.Writer` — consumers control rendering
- `Messages()` returns a defensive copy of conversation history
- Version injected at build time: `-ldflags '-X main.version=...'` (defaults to `"dev"`)
- `max_tokens` is a configurable field on `anthropic.Model`, not hardcoded
- REPL creates a fresh `signal.NotifyContext` per `Run` call so Ctrl-C cancels the current operation without killing the session
- `HEW_API_KEY` falls back to `ANTHROPIC_API_KEY` when base URL is Anthropic's

## CI

GitHub Actions runs on push to `main` and PRs targeting `main`. Single job: lint (golangci-lint with exhaustive, gofmt, govet, errcheck, staticcheck, unused), test (with `-race`), build.

**Local setup:** Run `make setup` to install the pre-commit hook (enforces gofmt). golangci-lint is required for `make lint` and `make check` — install with `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest` or `brew install golangci-lint`.

**Format enforcement:** Three layers — Claude Code PostToolUse hook formats `.go` files on edit, git pre-commit hook rejects unformatted files, CI golangci-lint catches anything remaining.
