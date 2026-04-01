# clnkr

[![CI](https://github.com/clnkr-ai/clnkr/actions/workflows/ci.yml/badge.svg)](https://github.com/clnkr-ai/clnkr/actions/workflows/ci.yml)

Warning: `clnkr` executes bash directly and currently has no permissions or sandboxing system; use it only in environments you are willing to trust and break.

Platform note: today `clnkr` is Unix-only. The executor assumes `bash`, process groups, and tools like `/usr/bin/env`, so Linux and macOS are supported targets. Windows is not supported.

A minimal coding agent. Query an LLM, execute bash commands, repeat. Supports the Anthropic Messages API and any OpenAI-compatible endpoint (vLLM, Ollama, LiteLLM, etc.).

Ships two binaries: **clnkr** (TUI) and **clnku** (plain CLI). A **clnk** symlink points to clnkr for convenience.

<img width="512" height="512" alt="Isildur cut the Ring (the ring here is bash -jokeexplainer)from his hand with the hilt-shard of his father's sword, and took it for his own." src="https://github.com/user-attachments/assets/7c9d648c-f5b9-4610-a311-04f5af37b364" />


## Install

```bash
# Plain CLI only (stdlib, no external deps)
go install github.com/clnkr-ai/clnkr/cmd/clnku@latest

# TUI requires building from source (due to replace directive)
make build
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
clnkr

# Single task
clnkr -p "find all TODO comments in this project"

# Skip per-command approval
clnkr --full-send -p "fix the failing test"
```

### OpenAI-compatible providers

Point `--base-url` at any OpenAI-compatible endpoint:

```bash
# vLLM
clnkr --base-url http://gpu-host:8000/v1 --model my-model

# Ollama
clnkr --base-url http://localhost:11434/v1 --model llama3

# LiteLLM
clnkr --base-url http://proxy:4000/v1 --model gpt-4o

# Gemini (free tier)
clnkr --base-url https://generativelanguage.googleapis.com/v1beta/openai --model gemini-2.0-flash
```

### Flags

```
-p, --prompt string      Task to run (exits after completion)
--model string           Model identifier (default: claude-sonnet-4-20250514)
--base-url string        LLM API endpoint (default: https://api.anthropic.com)
--max-steps int          Maximum agent steps (default: 100)
--full-send              Execute every Act turn without approval
--load-messages string   Seed conversation from JSON file (e.g. from --trajectory)
--event-log string       Write JSONL events to file (streams in real time)
--trajectory string      Write message history as JSON on exit (single-task mode only)
-v, --verbose            Show internal decisions on stderr
--version                Print version and exit
```

### Environment variables

| Variable | Description |
|----------|-------------|
| `CLNKR_API_KEY` | API key for the LLM provider (required) |
| `ANTHROPIC_API_KEY` | Fallback when using the default Anthropic endpoint |
| `CLNKR_MODEL` | Model identifier (overridden by `--model`) |
| `CLNKR_BASE_URL` | LLM endpoint (overridden by `--base-url`) |

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

### Project-specific instructions

Place an `AGENTS.md` file in your working directory. Its contents are appended to the system prompt, giving the LLM project-specific context.

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

### Manual Compaction

At the main idle conversational prompt, run `/compact` to summarize older
transcript history while keeping the recent working thread intact. You can add
extra instructions, for example `/compact focus on failing tests and edited files`.

## How it works

clnkr runs a loop using a structured JSON turn protocol:

1. Send conversation history to the LLM
2. Parse the model's JSON response into a typed turn (`clarify`, `act`, or `done`)
3. If `clarify`: return control to the frontend for more input
4. If `act`: either ask the user for approval or execute the bash command immediately, then append structured output to the conversation
5. If `done`: exit with the model's summary
6. Repeat until done or step limit

The LLM is the agent. clnkr is the scaffold.

## Development

```bash
make help       # Show all targets
make build      # Build both binaries
make check      # Full quality suite
make test       # Tests only
make evaluations      # Run the mock-provider evaluation suite
make evaluations-live # Run the live-provider evaluation suite
make docs       # Build documentation site
```

Evaluation suite layout, bundle contents, and maintenance workflows are documented in `evaluations/README.md`.
