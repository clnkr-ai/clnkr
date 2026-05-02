# clnkr

[![CI](https://github.com/clnkr-ai/clnkr/actions/workflows/ci.yml/badge.svg)](https://github.com/clnkr-ai/clnkr/actions/workflows/ci.yml)

A minimal coding agent CLI.

<img width="719" alt="clnkr terminal mascot" src="site/static/readme-terminal.png">

Warning: `clnkr` executes bash directly. There is no permission system and no sandbox. Run it only in environments you are willing to trust and break.

## Quick start

Install with Homebrew:

```bash
brew install clnkr-ai/tap/clnkr
```

Or install the Debian package from the latest release:

```bash
curl -fsSLO https://github.com/clnkr-ai/clnkr/releases/download/v<VERSION>/clnkr_<VERSION>-1_<ARCH>.deb
sudo dpkg -i ./clnkr_<VERSION>-1_<ARCH>.deb
```

`go install` works too:

```bash
go install github.com/clnkr-ai/clnkr/cmd/clnkr@latest
```

Set a provider and run:

```bash
export CLNKR_API_KEY=your-api-key
export CLNKR_PROVIDER=anthropic
export CLNKR_MODEL=claude-sonnet-4-6

clnkr
```

Or sign in with a ChatGPT Codex subscription and use the explicit
`openai-codex` provider:

```bash
clnkr --login-openai-codex
clnkr --provider openai-codex --model gpt-5.2-codex
```

At the prompt, ask for a task. `clnkr` proposes bash commands and asks before running each batch.

Run unattended:

```bash
clnkr -p "find all TODO comments in this project"
```

Skip approvals:

```bash
clnkr --full-send
```

## Usage

clnkr can:

- Run interactively, with approval before each command batch
- Run unattended with `-p`
- Resume project sessions with `--continue`
- Stream JSONL event logs with `--event-log`
- Save and load transcripts with `--trajectory` and `--load-messages`

One-shot task:

```bash
clnkr -p "add tests for config loading"
```

Resume the latest session for the current project:

```bash
clnkr --continue
```

Compact older transcript history:

```text
/compact focus on failing tests and edited files
```

Emit events or reuse transcripts:

```bash
clnkr -p "fix the build" --event-log /tmp/events.jsonl
clnkr -p "investigate the bug" --trajectory /tmp/investigation.json
clnkr -p "write a fix based on the investigation" --load-messages /tmp/investigation.json
```

OpenAI-compatible endpoint:

```bash
clnkr --provider openai --base-url http://gpu-host:8000/v1 --model my-model
clnkr --provider openai --base-url http://localhost:11434/v1 --model llama3
clnkr --provider openai --base-url http://proxy:4000/v1 --model gpt-4o
```

clnkr requires structured outputs.

Full CLI reference: [`doc/clnkr.1.md`](doc/clnkr.1.md).

## Configuration

Set `CLNKR_API_KEY` for API-key providers. For ChatGPT Codex subscription auth,
run `clnkr --login-openai-codex` and use `--provider openai-codex`.

| Variable | Description |
|----------|-------------|
| `CLNKR_API_KEY` | API key for `anthropic` or `openai` |
| `CLNKR_PROVIDER` | Provider: `anthropic`, `openai`, or `openai-codex` |
| `CLNKR_MODEL` | Model name (overridden by `--model`) |
| `CLNKR_BASE_URL` | LLM endpoint (overridden by `--base-url`) |
| `CLNKR_OPENAI_CODEX_AUTH_BASE_URL` | Test/debug override for OpenAI Codex auth endpoints during login and runtime auth |
| `CLNKR_OPENAI_CODEX_AUTH_PATH` | Test/debug override for the OpenAI Codex auth file path during login and runtime auth |

clnkr builds its system prompt from the built-in prompt plus `AGENTS.md` files found in the user home directory, the XDG config directory, and the current working directory.

Prompt controls:

```bash
clnkr --dump-system-prompt
clnkr --system-prompt-append "Prefer targeted tests first"
clnkr --no-system-prompt
```

## How it works

clnkr is a scaffold that loops:

1. Send the conversation to the LLM.
2. Get back a question, command batch, or final answer.
3. Ask the user when the model needs clarification.
4. Ask for approval before running commands, unless `--full-send` or `-p` is set.
5. Send command results back to the model.
6. Print the final answer.

Architecture discussion: [`doc/clnkr.7.md`](doc/clnkr.7.md). Library/API reference: [`doc/clnkr.3.md`](doc/clnkr.3.md).

## Development

```bash
git clone https://github.com/clnkr-ai/clnkr.git
cd clnkr

make help       # Show all targets
make build      # Build shipped binaries
make check      # Full quality suite
make test       # Tests only
```

## License

[Apache-2.0](LICENSE)
