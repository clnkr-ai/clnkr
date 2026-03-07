# hew

A minimal coding agent. Query an LLM, execute bash commands, repeat. Supports the Anthropic Messages API and any OpenAI-compatible endpoint (vLLM, Ollama, LiteLLM, etc.).

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
hew --base-url http://gpu-host:8000 --model my-model

# Ollama
hew --base-url http://localhost:11434 --model llama3

# LiteLLM
hew --base-url http://proxy:4000 --model gpt-4o
```

### Flags

```
-p, --prompt string    Task to run (exits after completion)
--model string         Model identifier (default: claude-sonnet-4-20250514)
--base-url string      LLM API endpoint (default: https://api.anthropic.com)
--max-steps int        Maximum agent steps (default: 100)
-v, --verbose          Show internal decisions on stderr
--version              Print version and exit
```

### Environment variables

| Variable | Description |
|----------|-------------|
| `HEW_API_KEY` | API key for the LLM provider (required) |
| `ANTHROPIC_API_KEY` | Fallback when using the default Anthropic endpoint |

### Project-specific instructions

Place an `AGENTS.md` file in your working directory. Its contents are appended to the system prompt, giving the LLM project-specific context.

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
make check      # Vet + tests
make test       # Tests only
make fmt        # Format source
```
