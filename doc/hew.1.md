hew 1 "hew" "User Commands"
==========================

# NAME

hew - a minimal coding agent

# SYNOPSIS

**hew** [**-p** *task*] [**--model** *name*] [**--base-url** *url*] [**--max-steps** *n*] [**-v**|**--verbose**] [**--version**]

# DESCRIPTION

**hew** is a minimal coding agent that queries LLMs and executes bash commands in an iterative loop. It supports both Anthropic and OpenAI-compatible APIs.

In default mode, **hew** starts an interactive REPL. With **-p**, it runs a single task and exits.

Project-specific instructions are loaded from an **AGENTS.md** file in the current working directory, if present.

# OPTIONS

**-p**, **--prompt** *task*
: Run the given task and exit. Without this flag, hew starts in conversational REPL mode.

**--model** *name*
: LLM model identifier (default: claude-sonnet-4-20250514).

**--base-url** *url*
: LLM API endpoint (default: https://api.anthropic.com). If the URL contains "anthropic.com", the Anthropic adapter is used; otherwise, the OpenAI-compatible adapter is used.

**--max-steps** *n*
: Maximum agent iterations. 0 uses the default of 100.

**-v**, **--verbose**
: Show internal decisions (queries, parsing, working directory).

**--version**
: Print version and exit.

# ENVIRONMENT

**HEW_API_KEY**
: API key for the LLM provider (required).

**ANTHROPIC_API_KEY**
: Fallback API key when using the Anthropic endpoint.

# FILES

**AGENTS.md**
: If present in the current directory, its contents are appended to the system prompt as project-specific instructions.

# EXIT STATUS

**0**
: Success.

**1**
: Error (no API key, step limit reached, etc.).

# AUTHOR

Brian Cosgrove <cosgroveb@gmail.com>
