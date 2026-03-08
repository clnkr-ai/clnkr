# AGENTS.md

## Overview

hew is a coding agent CLI that queries LLMs and executes bash commands in a loop. Single binary, stdlib only. Talks to the Anthropic Messages API and any OpenAI-compatible endpoint.

## Commands

```bash
make build          # Compile binary (version from git describe)
make test           # Run all tests (go test ./... -v)
make check          # Run lint + tests
make lint           # Run golangci-lint
make setup          # Install git hooks
make fmt            # Format source
make man            # Generate man page from doc/hew.1.md
make check-man      # Verify committed man page matches source
make help           # List all targets

# Run a single test
go test . -v -run TestExtractCommand

# Build with explicit version
make build VERSION=0.2.0
```

<rules>
- Before committing, run `make fmt` then `make check`. Do not commit if either fails.
- A PostToolUse hook runs gofmt on Go files after Edit/Write automatically. You do not need to format manually, but `make fmt` and `make check` are still required before commits.
- Do not add external dependencies. This project is stdlib only.
- Do not move packages to `internal/`. The core is a public library.
- Do not add `io.Writer` parameters for output. Output goes through typed events.
- Do not put policy logic in `Step()`. Keep it in `Run()`. New shared logic goes in `Step()`.
- Do not add provider-detection logic outside `main.go`.
- Wrap errors with `fmt.Errorf("context: %w", err)`. Do not use bare error returns or third-party error packages.
- Adapter tests use external test packages (`package anthropic_test`). Core tests use internal packages (`package hew`). Match the existing pattern in each directory.
- The `exhaustive` linter is enabled. Switch statements on sealed types (like `Event`) must cover all cases. A `default` branch counts as exhaustive (`default-signifies-exhaustive: true` in `.golangci.yml`).
- When adding event types: define the struct, add the unexported `event()` method, and the exhaustive linter will catch missing switch cases.
</rules>

## Architecture

The core is an importable library at module root (`github.com/cosgroveb/hew`). `cmd/hew/main.go` is the first consumer — a thin CLI that wires components and handles output formatting via the `Notify` callback.

**Package structure:**
```
hew/                    # core: types, interfaces, Agent, events
├── anthropic/          # Anthropic Messages API adapter
├── openai/             # OpenAI-compatible adapter
└── cmd/hew/            # CLI consumer
```

**Two-tier agent API:**
- `Step(ctx) (StepResult, error)` — one query-parse-execute cycle, no policy. `StepResult` carries the response, parsed action, command output, and execution error.
- `Run(ctx, task) error` — policy loop over `Step()`. Counts format errors (exits on 2 consecutive) and enforces step limits.

**Events:** `Notify func(Event)` callback on Agent. Sealed interface (unexported marker method) with five types: `EventResponse`, `EventCommandStart`, `EventCommandDone`, `EventFormatError`, `EventDebug`. Nil Notify = silent.

**Interfaces:**
- `Model` — `Query(ctx, []Message) (Response, error)` — implemented by `anthropic.Model` and `openai.Model`
- `Executor` — `Execute(ctx, command, dir) (string, error)` — implemented by `CommandExecutor`

**Provider selection:** `main.go` checks if `--base-url` contains `anthropic.com` → Anthropic adapter, everything else → OpenAI adapter. Both use raw `net/http` with `httptest` servers in tests.

**Parser** (`parser.go`): `ExtractCommand` pulls the first `` ```bash `` fenced block via regex. Returns `ErrNoCommand` if none found.

**System prompt** (`prompt.go`): Hardcoded base prompt + optional `AGENTS.md` content from the working directory.

## Testing patterns

- Provider adapters use `httptest.NewServer` in external test packages (`package anthropic_test`) — black-box, no real API calls
- Agent loop uses `fakeModel` and `fakeExecutor` (in `agent_test.go`) — deterministic inputs for state machine testing
- Executor tests shell out to bash (real integration tests)
- macOS `/private/tmp` symlink is handled in executor tests

## Design decisions

- `Step()` is the loop primitive; `Run()` adds policy
- `Messages()` returns a defensive copy; `AddMessages()` prepends seed messages (errors after first `Step()`)
- Version injected at build time via `-ldflags '-X main.version=...'` (defaults to `"dev"`)
- `max_tokens` is a field on `anthropic.Model`, not hardcoded
- `HEW_API_KEY` falls back to `ANTHROPIC_API_KEY` when base URL is Anthropic's
- Man page source is `doc/hew.1.md` (markdown); `doc/hew.1` (troff) is generated via `go-md2man` and committed. Debian packaging references `doc/hew.1` instead of maintaining a separate `debian/hew.1`
- `--event-log` uses `O_APPEND` for atomic JSONL writes; serialization lives in `main.go`, not the library
- `--trajectory` writes before checking `runErr` so failed runs still produce output
- `--trajectory` output and `--load-messages` input use the same JSON format (array of `{role, content}` objects)

## Worktrees

Worktree directory: `~/.config/superpowers/worktrees/hew/`

## CI

GitHub Actions runs on push to `main` and PRs targeting `main`. Single job: lint, test (with `-race`), build.

**Local setup:** Run `make setup` to install the pre-commit hook (enforces gofmt). golangci-lint is required for `make lint` and `make check` — install with `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest` or `brew install golangci-lint`.
