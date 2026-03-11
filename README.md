# hew

[![CI](https://github.com/cosgroveb/hew/actions/workflows/ci.yml/badge.svg)](https://github.com/cosgroveb/hew/actions/workflows/ci.yml)

A minimal coding agent. Query an LLM, execute bash commands, repeat. Supports the Anthropic Messages API and any OpenAI-compatible endpoint (vLLM, Ollama, LiteLLM, etc.).

<img width="512" height="512" alt="Isildur cut the Ring (the ring here is bash -jokeexplainer)from his hand with the hilt-shard of his father's sword, and took it for his own." src="https://github.com/user-attachments/assets/7c9d648c-f5b9-4610-a311-04f5af37b364" />


## Install

```bash
go install github.com/cosgroveb/hew/cmd/hew@latest
```

Or build from source:

```bash
git clone https://github.com/cosgroveb/hew.git
cd hew
make build
```

## Usage

Set your API key and run:

```bash
export HEW_API_KEY=your-api-key

# Conversational mode
hew

# Single task
hew -p "find all TODO comments in this project"
```

### OpenAI-compatible providers

Point `--base-url` at any OpenAI-compatible endpoint:

```bash
# vLLM
hew --base-url http://gpu-host:8000/v1 --model my-model

# Ollama
hew --base-url http://localhost:11434/v1 --model llama3

# LiteLLM
hew --base-url http://proxy:4000/v1 --model gpt-4o

# Gemini (free tier)
hew --base-url https://generativelanguage.googleapis.com/v1beta/openai --model gemini-2.0-flash
```

### Flags

```
-p, --prompt string      Task to run (exits after completion)
--model string           Model identifier (default: claude-sonnet-4-20250514)
--base-url string        LLM API endpoint (default: https://api.anthropic.com)
--max-steps int          Maximum agent steps (default: 100)
--load-messages string   Seed conversation from JSON file (e.g. from --trajectory)
--event-log string       Write JSONL events to file (streams in real time)
--trajectory string      Write message history as JSON on exit (single-task mode only)
-v, --verbose            Show internal decisions on stderr
--version                Print version and exit
```

### Environment variables

| Variable | Description |
|----------|-------------|
| `HEW_API_KEY` | API key for the LLM provider (required) |
| `ANTHROPIC_API_KEY` | Fallback when using the default Anthropic endpoint |
| `HEW_MODEL` | Model identifier (overridden by `--model`) |
| `HEW_BASE_URL` | LLM endpoint (overridden by `--base-url`) |

### Agent orchestration

A parent process can spawn hew as a child agent and monitor or chain runs:

```bash
# Watch events in real time
hew -p "fix the build" --event-log /tmp/events.jsonl &
tail -f /tmp/events.jsonl | jq .

# Save a run's conversation, then feed it to a second agent
hew -p "investigate the bug" --trajectory /tmp/investigation.json
hew -p "write a fix based on the investigation" --load-messages /tmp/investigation.json
```

`--event-log` streams one JSON object per line as events happen (O_APPEND, safe to tail).
`--trajectory` writes the full message array as pretty-printed JSON when the task ends — even if it failed.
`--load-messages` reads that same format and prepends the messages before starting, so one agent's output becomes another's context.

### Project-specific instructions

Place an `AGENTS.md` file in your working directory. Its contents are appended to the system prompt, giving the LLM project-specific context.

## Session Persistence

When using hew or hui in conversational mode (no `-p` flag), sessions are
automatically saved to `$XDG_STATE_HOME/hew/projects/` on exit.

Resume a session:

```bash
hew --continue   # Load most recent session
hui --continue   # Load in TUI
```

List all sessions for the current project:

```bash
hew --list-sessions
hui --list-sessions
```

Sessions are tied to their original working directory. You can only resume a
session from the same project directory where it was created.

## How it works

hew runs a loop:

1. Send conversation history to the LLM
2. Extract a bash command from the response (fenced `` ```bash `` block)
3. Execute it and append the output to the conversation
4. Repeat until the LLM sends `exit`

The LLM is the agent. hew is the scaffold.

## Development

```bash
make help       # Show all targets
make check      # Lint + tests
make test       # Tests only
make fmt        # Format source
```
