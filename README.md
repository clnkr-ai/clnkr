# clnkr

[![CI](https://github.com/clnkr-ai/clnkr/actions/workflows/ci.yml/badge.svg)](https://github.com/clnkr-ai/clnkr/actions/workflows/ci.yml)

Warning: `clnkr` executes bash directly and currently has no permissions or sandboxing system; use it only in environments you are willing to trust and break.

Platform note: today `clnkr` is Unix-only. The executor assumes `bash`, process groups, and tools like `/usr/bin/env`, so Linux and macOS are supported targets. Windows is not supported.

A minimal coding agent. Query an LLM, execute bash commands, repeat. Supports the Anthropic Messages API and OpenAI-compatible endpoints that implement structured outputs on the selected model path.

Ships one binary: **clnkr** (plain CLI). The evaluation runner lives in the separate **clankerval** project and is installed independently.

<img width="512" height="512" alt="Isildur cut the Ring (the ring here is bash -jokeexplainer)from his hand with the hilt-shard of his father's sword, and took it for his own." src="https://github.com/user-attachments/assets/7c9d648c-f5b9-4610-a311-04f5af37b364" />


## Install

```bash
# Plain CLI (stdlib, no external deps)
go install github.com/clnkr-ai/clnkr/cmd/clnkr@latest

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

# Single unattended task
clnkr --provider anthropic --model claude-sonnet-4-6 -p "find all TODO comments in this project"

# Skip act-batch approval in conversational mode
clnkr --provider anthropic --model claude-sonnet-4-6 --full-send
```

### OpenAI-compatible providers

Point `--base-url` at an OpenAI-compatible endpoint that supports structured outputs for the model you select. `--provider` controls adapter semantics; `--base-url` is only the transport endpoint. In normal use, set both `--provider` and `--model`. Compatibility fallback: if `--provider` / `CLNKR_PROVIDER` is unset but `--base-url` / `CLNKR_BASE_URL` is explicitly set, clnkr infers the provider from that URL. For `--provider=openai`, `--provider-api` defaults to `auto`, which prefers `openai-responses` for known supported names and other OpenAI-looking model names such as `gpt-*`, `o` plus a digit, `codex`, `codex-*`, `*-codex`, and `*-codex-*`. Names that do not look OpenAI-ish, such as `llama3`, `gemini-2.0-flash`, `orca-*`, `olmo-*`, `openhermes-*`, and `chatgpt-*`, stay on `openai-chat-completions`.

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

If the backend rejects the resolved OpenAI API surface, clnkr returns the provider error. When a proxy or gateway expects a different OpenAI surface, override with `--provider-api` or `CLNKR_PROVIDER_API`.

For Anthropic runs, clnkr requests Anthropic's native structured output format on every turn. Keep Anthropic runs on a model Anthropic documents as supporting structured output.

Structured outputs are a hard requirement for agent turns. clnkr rejects `gpt-5.2-pro`, `gpt-5.4-pro`, and their dated snapshots even if you force `openai-chat-completions`.

### Common flags

```
-p, --prompt string            Task to run unattended and exit
-m, --model string             Model identifier (required; env: CLNKR_MODEL)
-u, --base-url string          LLM endpoint transport URL (env: CLNKR_BASE_URL)
--provider string              Provider adapter: anthropic|openai
                               (required in normal use; env: CLNKR_PROVIDER)
--provider-api string          OpenAI-only override
                               (auto|openai-chat-completions|openai-responses)
--act-protocol string          Act protocol
                               (clnkr-inline|tool-calls)
--max-steps int                Limit executed commands
                               before summary (default: 100)
--full-send                    Execute every act batch without approval
                               (implied by -p)
-c, --continue                 Resume the most recent session for this project
-l, --list-sessions            List saved sessions for this project
-S, --no-system-prompt         Omit the built-in system prompt
--system-prompt-append string  Append text to the composed system prompt
--dump-system-prompt           Print the composed system prompt and exit
--load-messages string         Seed conversation from JSON file (e.g. from --trajectory)
--event-log string             Write JSONL events to file (streams in real time)
--trajectory string            Save single-task history as JSON on exit
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

A parent process can spawn clnkr as a child agent and monitor or chain runs:

```bash
# Watch events in real time
clnkr -p "fix the build" --event-log /tmp/events.jsonl &
tail -f /tmp/events.jsonl | jq .

# Save a run's conversation, then feed it to a second agent
clnkr -p "investigate the bug" --trajectory /tmp/investigation.json
clnkr -p "write a fix based on the investigation" --load-messages /tmp/investigation.json
```

`--event-log` streams one JSON object per line as events happen (O_APPEND, safe to tail).
`--trajectory` writes the full message array as pretty-printed JSON when the task ends, even if it failed.
`--load-messages` reads that same format and prepends the messages before starting, so one agent's output becomes another's context.
The transcript may include host-generated JSON `[state]` messages. Today they persist the current working directory so `--load-messages` and `--continue` can restore it.
Single-task mode (`-p`) runs unattended and uses the same execution path as `--full-send`. Passing `--full-send=false` with `-p` is rejected. If a single-task run stops to ask for clarification, it exits with status `2` after printing the question to stderr. Non-interactive stdin in `--full-send` mode behaves the same. In the default conversational approval mode, clnkr asks for clarification inline instead.

By default, conversational mode requires explicit approval before each `act` turn. One approval accepts all commands in that turn. Pass `--full-send` to run every command immediately.

### Command result format

clnkr feeds command results back to the model as JSON with separated streams and a structured outcome:

```json
{"stdout":"...","stderr":"...","outcome":{"type":"exit","exit_code":0}}
```

Non-exit outcomes include `timeout`, `cancelled`, `denied`, `skipped`, and `error`. When git feedback is available, clnkr adds a `feedback` object with changed files and diff.

### Prompt customization

clnkr composes its system prompt from the built-in prompt plus any `AGENTS.md` files found in the user home directory, the XDG config directory, and the current working directory.

You can also customize the composed prompt directly:

```bash
# Show the final prompt that will be sent
clnkr --dump-system-prompt

# Append extra one-off instructions
clnkr --system-prompt-append "Prefer targeted tests first"

# Disable the entire composed prompt, including all AGENTS.md layers
clnkr --no-system-prompt
```

## Session Persistence

When using clnkr in conversational mode (no `-p` flag), sessions are
automatically saved to `$XDG_STATE_HOME/clnkr/projects/` on exit.

Resume a session:

```bash
clnkr --continue    # Load most recent session
```

List all sessions for the current project:

```bash
clnkr --list-sessions
```

Sessions are tied to their original working directory. You can only resume a
session from the same project directory where it was created.

Saved message history now restores the agent's current working directory on
resume via transcript-level JSON `[state]` messages. Exported environment variables
still persist only within the live process.

### Conversational commands

At the main idle conversational prompt, run `/compact` to summarize older transcript history while keeping the recent working thread intact. You can add extra instructions, for example `/compact focus on failing tests and edited files`.

## How it works

clnkr runs a loop using an act protocol:

1. Send conversation history to the LLM
2. The selected provider adapter owns the provider-facing structured-output schema, any provider wire wrapper such as `{"turn": ...}`, and the translation from provider response text into a typed turn (`clarify`, `act`, or `done`).
3. If `clarify`: return control to the frontend for more input
4. If `act`: either ask the user to approve the command batch or execute the bash commands sequentially, then append structured command-result JSON to the conversation for each executed command, including optional `feedback` when the host started from a clean git worktree
5. If `done`: exit with the model's summary
6. Successful assistant turns are stored in canonical internal JSON for replay and resume. Invalid provider responses are stored raw alongside protocol error metadata.
7. Repeat until done, or until the executed-command limit triggers a final `done` summary request

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
