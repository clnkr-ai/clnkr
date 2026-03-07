# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

hew is a minimal coding agent CLI that queries LLMs and executes bash commands in a loop. Single Go binary, zero external dependencies beyond stdlib. Dual-provider support: Anthropic Messages API and OpenAI-compatible chat completions.

## Commands

```bash
make build          # Compile binary (version from git describe)
make test           # Run all tests (go test ./... -v)
make check          # Run vet + tests
make fmt            # Format source
make help           # List all targets

# Run a single test
go test ./internal/hew/ -v -run TestExtractCommand

# Build with explicit version
make build VERSION=0.2.0
```

## Architecture

The agent loop lives entirely in `internal/hew/`. `cmd/hew/main.go` is a thin CLI entry point that wires components together.

**Core loop** (`agent.go`): `Query model → ExtractCommand → Execute → append output → repeat`. Exits on `exit` action or step limit. Tracks working directory across `cd` commands (standalone only — compound commands like `cd /tmp && ls` are skipped).

**Two interfaces drive testability:**
- `Model` — `Query(ctx, []Message) (Response, error)` — implemented by `AnthropicModel` and `OpenAIModel`
- `Executor` — `Execute(ctx, command, dir) (string, error)` — implemented by `CommandExecutor`

**Provider selection**: `main.go` checks if `--base-url` contains `anthropic.com` → Anthropic adapter, everything else → OpenAI-compatible adapter. Both use raw `net/http` with `httptest` servers in tests.

**Parser** (`parser.go`): `ExtractCommand` extracts the first `` ```bash `` fenced block via regex. Returns `ErrNoCommand` if none found. The agent tolerates one format error (sends a reminder), exits on two consecutive.

**System prompt** (`prompt.go`): Hardcoded base prompt + optional `AGENTS.md` file content from the working directory.

**Debug output** (`--verbose` / `-v`): `[hew]` prefixed lines go to stderr via `DebugOut` writer, keeping stdout clean for piping. Shows token usage, parsed actions, cwd changes.

## Testing Patterns

- Provider adapters tested with `httptest.NewServer` — no real API calls
- Agent loop tested with `fakeModel` and `fakeExecutor` (defined in `agent_test.go`)
- Executor tests are real integration tests that shell out to bash
- macOS `/private/tmp` symlink is handled in executor tests

## Key Design Decisions

- Version injected at build time: `-ldflags '-X main.version=...'` (defaults to `"dev"`)
- `max_tokens` is a configurable field on `AnthropicModel`, not hardcoded
- REPL creates a fresh `signal.NotifyContext` per `Run` call so Ctrl-C cancels the current operation without killing the session
- `HEW_API_KEY` falls back to `ANTHROPIC_API_KEY` when base URL is Anthropic's
