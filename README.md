# clnkr

[![CI](https://github.com/clnkr-ai/clnkr/actions/workflows/ci.yml/badge.svg)](https://github.com/clnkr-ai/clnkr/actions/workflows/ci.yml)

Warning: `clnkr` executes bash directly and currently has no permissions or sandboxing system; use it only in environments you are willing to trust and break.

Platform note: today `clnkr` is Unix-only. The executor assumes `bash`, process groups, and tools like `/usr/bin/env`, so Linux and macOS are supported targets. Windows is not supported.

A minimal coding agent. Query an LLM, execute bash commands, repeat. Supports the Anthropic Messages API and OpenAI-compatible endpoints that implement structured outputs on the selected model path.

Ships two binaries: **clnkr** (TUI) and **clnku** (plain CLI). The evaluation runner lives in the separate **clankerval** project and is installed independently. A **clnk** symlink points to clnkr for convenience.

<img width="512" height="512" alt="Isildur cut the Ring (the ring here is bash -jokeexplainer)from his hand with the hilt-shard of his father's sword, and took it for his own." src="https://github.com/user-attachments/assets/7c9d648c-f5b9-4610-a311-04f5af37b364" />


## Install

```bash
# Plain CLI only (stdlib, no external deps)
go install github.com/clnkr-ai/clnkr/cmd/clnku@latest

# TUI requires building from source (due to replace directive)
make build

# Install clankerval separately when you need evals.
# Debian-family example:
#   curl -fsSLO https://github.com/clnkr-ai/clankerval/releases/download/v<VERSION>/clankerval_<VERSION>-1_<ARCH>.deb
#   sudo apt install ./clankerval_<VERSION>-1_<ARCH>.deb
```

Or build from source:

```bash
git clone https://github.com/clnkr-ai/clnkr.git
cd clnkr
make build
```

## Usage

Set your API key and run:

```bash
export CLNKR_API_KEY=your-api-key

# Conversational mode
clnkr --provider anthropic --model claude-sonnet-4-6

# Single task
clnkr --provider anthropic --model claude-sonnet-4-6 -p "find all TODO comments in this project"

# Skip per-command approval
clnkr --provider anthropic --model claude-sonnet-4-6 --full-send -p "fix the failing test"
```

### OpenAI-compatible providers

Point `--base-url` at an OpenAI-compatible endpoint that supports structured outputs for the model you select. `--provider` controls adapter semantics; `--base-url` is only the transport endpoint. In normal use, set both `--provider` and `--model`. Compatibility fallback: if `--provider` / `CLNKR_PROVIDER` is unset but `--base-url` / `CLNKR_BASE_URL` is explicitly set, clnkr infers the provider from that URL. For `--provider=openai`, `--provider-api` defaults to `auto`, which prefers `openai-responses` for a conservative allowlist of approved model names under that provider, including current Codex names such as `gpt-5-codex`, `gpt-5.1-codex`, `gpt-5.1-codex-mini`, `gpt-5.1-codex-max`, `gpt-5.2-codex`, and `gpt-5.3-codex`, and otherwise stays on `openai-chat-completions`.

```bash
# vLLM
clnkr --provider openai --base-url http://gpu-host:8000/v1 --model my-model

# Ollama
clnkr --provider openai --base-url http://localhost:11434/v1 --model llama3

# LiteLLM
clnkr --provider openai --base-url http://proxy:4000/v1 --model gpt-4o

# Gemini (free tier)
clnkr --provider openai --base-url https://generativelanguage.googleapis.com/v1beta/openai --model gemini-2.0-flash

# Anthropic via a proxy or gateway that does not use an anthropic.com host
clnkr --provider anthropic --base-url https://proxy.example.com/anthropic --model claude-sonnet-4-6

# Force the legacy OpenAI chat-completions path against a proxy that rejects Responses
clnkr --provider openai --provider-api openai-chat-completions --base-url https://gateway.example.com/v1 --model gpt-4o
```

If the backend rejects the resolved OpenAI API surface, clnkr returns the provider error. When a proxy or gateway expects a different OpenAI surface, override with `--provider-api` or `CLNKR_PROVIDER_API`. For approved Codex names, forcing `openai-chat-completions` is a manual proxy-compatibility escape hatch, not a claim about first-party OpenAI compatibility on that surface.

For Anthropic runs, clnkr requests Anthropic's native structured output format on every turn. Keep Anthropic runs on a model Anthropic documents as supporting structured output.

Structured outputs are a hard requirement for agent turns. `gpt-5.2-pro` and `gpt-5.4-pro` are still rejected in this pass.

### Common flags

```
-p, --prompt string            Task to run (exits after completion)
-m, --model string             Model identifier (required; env: CLNKR_MODEL)
-u, --base-url string          LLM endpoint transport URL (env: CLNKR_BASE_URL)
--provider string              Provider adapter semantics: anthropic|openai (required in normal use; env: CLNKR_PROVIDER)
--provider-api string          OpenAI-only override: auto|openai-chat-completions|openai-responses
--max-steps int                Maximum agent steps (default: 100)
--full-send                    Execute every Act turn without approval
-c, --continue                 Resume the most recent session for this project
-l, --list-sessions            List saved sessions for this project
-S, --no-system-prompt         Omit the built-in system prompt
--system-prompt-append string  Append text to the composed system prompt
--dump-system-prompt           Print the composed system prompt and exit
--load-messages string         Seed conversation from JSON file (e.g. from --trajectory)
--event-log string             Write JSONL events to file (streams in real time)
--trajectory string            Write message history as JSON on exit (single-task mode only)
-v, --verbose                  Show internal decisions on stderr
-V, --version                  Print version and exit
```

### Environment variables

| Variable | Description |
|----------|-------------|
| `CLNKR_API_KEY` | API key for the LLM provider (required) |
| `CLNKR_PROVIDER` | Provider adapter semantics |
| `CLNKR_PROVIDER_API` | OpenAI-only API surface override |
| `CLNKR_MODEL` | Model identifier (overridden by `--model`) |
| `CLNKR_BASE_URL` | LLM endpoint (overridden by `--base-url`); also drives the temporary provider-inference compatibility fallback when `CLNKR_PROVIDER` is unset |

### Agent orchestration

A parent process can spawn clnku as a child agent and monitor or chain runs:

```bash
# Watch events in real time
clnku -p "fix the build" --event-log /tmp/events.jsonl &
tail -f /tmp/events.jsonl | jq .

# Save a run's conversation, then feed it to a second agent
clnku -p "investigate the bug" --trajectory /tmp/investigation.json
clnku -p "write a fix based on the investigation" --load-messages /tmp/investigation.json
```

`--event-log` streams one JSON object per line as events happen (O_APPEND, safe to tail).
`--trajectory` writes the full message array as pretty-printed JSON when the task ends — even if it failed.
`--load-messages` reads that same format and prepends the messages before starting, so one agent's output becomes another's context.
The transcript may include host-generated JSON `[state]` messages. Today they persist the current working directory so `--load-messages` and `--continue` can restore it.
With `--full-send`, a single-task run that stops to ask for clarification exits with status `2` after printing the question, so callers can distinguish "needs input" from "done". In the default approval mode, the harness asks for clarification inline instead.

By default, both binaries require explicit approval before each `act` turn. Pass `--full-send` to restore the old "run every command immediately" behavior.

### Command result format

clnkr executes commands with structured results in the core: command text, exit code, stdout, and stderr are captured separately. When those results are fed back into the model, clnkr uses a flat tagged text format:

```text
[command]
...
[/command]
[exit_code]
...
[/exit_code]
[stdout]
...
[/stdout]
[stderr]
...
[/stderr]
```

This is intentional. The only downstream machine consumers are clnkr and clnku, so the protocol is optimized for model readability rather than external XML tooling. In practice, weaker models follow explicit flat delimiters more reliably than nested structure, while the frontends still receive fully structured events.

### Reasoning trace in the TUI

In the `clnkr` TUI, model `reasoning` is available as a lightweight trace rather than being printed inline with every assistant message. When a parsed assistant turn includes non-empty `reasoning`, the chat shows a breadcrumb like `Reasoning trace available (press Ctrl+Y)`. Press `Ctrl+Y` to open a modal with the latest reasoning trace.

A few important details:

- Act turns do not render their command text as assistant chat; command proposals and execution are shown through the command UI instead. The reasoning breadcrumb can still appear for those turns.
- Done turns render their summary in chat and can also attach a reasoning breadcrumb.
- Live clarify turns do not print the clarify text directly in the chat stream, but their reasoning can still be cached for the reasoning modal.
- If there is no cached reasoning trace, pressing `Ctrl+Y` shows `No reasoning trace available.`
- This is separate from `--verbose`. Verbose mode shows debug/internal event lines; the reasoning modal shows the model-provided `reasoning` field from the structured turn protocol.

### Prompt customization

Place an `AGENTS.md` file in your working directory. Its contents are appended to the system prompt, giving the LLM project-specific context.

You can also customize the composed prompt directly:

```bash
# Show the final prompt that will be sent
clnkr --dump-system-prompt

# Append extra one-off instructions
clnkr --system-prompt-append "Prefer targeted tests first"

# Disable the built-in prompt entirely
clnkr --no-system-prompt
```

## Session Persistence

When using clnkr or clnku in conversational mode (no `-p` flag), sessions are
automatically saved to `$XDG_STATE_HOME/clnkr/projects/` on exit.

Resume a session:

```bash
clnkr --continue   # Load most recent session (TUI)
clnku --continue    # Load most recent session (plain CLI)
```

List all sessions for the current project:

```bash
clnkr --list-sessions
clnku --list-sessions
```

Sessions are tied to their original working directory. You can only resume a
session from the same project directory where it was created.

Saved message history now restores the agent's current working directory on
resume via transcript-level JSON `[state]` messages. Exported environment variables
still persist only within the live process.

### Conversational commands

At the main idle conversational prompt, run `/compact` to summarize older transcript history while keeping the recent working thread intact. You can add extra instructions, for example `/compact focus on failing tests and edited files`.

In the TUI, `/delegate TASK` runs a child `clnku` session seeded with the current transcript and appends a delegation summary back into the conversation when the child run completes.

## How it works

clnkr runs a loop using a structured JSON turn protocol:

1. Send conversation history to the LLM
2. The selected provider adapter owns the provider-facing structured-output schema, any provider wire wrapper such as `{"turn": ...}`, and the translation from provider response text into a typed turn (`clarify`, `act`, or `done`).
3. If `clarify`: return control to the frontend for more input
4. If `act`: either ask the user for approval or execute the bash commands sequentially, then append structured output to the conversation for each executed command, including optional `[command_feedback]` when the host started from a clean git worktree
5. If `done`: exit with the model's summary
6. Successful assistant turns are stored in canonical internal JSON for replay and resume. Invalid provider responses are stored raw alongside protocol error metadata.
7. Repeat until done or step limit

The LLM is the agent. clnkr is the scaffold.

## Development

```bash
make help       # Show all targets
make build      # Build shipped binaries
make check      # Full quality suite
make test       # Tests only
make evaluations                # Run the mock-provider evaluation suite (requires clankerval)
make evaluations-live-openai    # Run the live-provider suite against OpenAI defaults
make evaluations-live-anthropic # Run the live-provider suite against Anthropic defaults
make docs       # Build documentation site
```

`make man` and `make docs` require `pandoc`.

The provider-specific live-eval targets are deterministic. They ignore generic `CLNKR_EVALUATION_*` shell state and use provider-specific inputs instead:

- `make evaluations-live-openai`: `CLNKR_EVALUATION_OPENAI_API_KEY`, optional `CLNKR_EVALUATION_OPENAI_BASE_URL`, optional `CLNKR_EVALUATION_OPENAI_MODEL`
- `make evaluations-live-anthropic`: `CLNKR_EVALUATION_ANTHROPIC_API_KEY`, optional `CLNKR_EVALUATION_ANTHROPIC_BASE_URL`, optional `CLNKR_EVALUATION_ANTHROPIC_MODEL`

For compatibility, the Make targets still fall back to `OPENAI_API_KEY` and `ANTHROPIC_API_KEY` when the provider-specific `CLNKR_EVALUATION_*_API_KEY` vars are unset.

Install `clankerval` separately from the packages published by the
`clankerval` project. `make evaluations` checks that `clankerval` is on
`PATH` and meets this repo's minimum supported version.

For example:

```bash
clankerval run --suite default
```

Evaluation suite layout, bundle contents, and maintenance workflows are documented in `evaluations/README.md`.
