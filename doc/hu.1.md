hu 1 "hu" "User Commands"
==========================

# NAME

hu - a minimal coding agent (plain CLI)

# SYNOPSIS

**hu** [**-p** *task*] [**--model** *name*] [**--base-url** *url*] [**--max-steps** *n*] [**--load-messages** *file*] [**--event-log** *file*] [**--trajectory** *file*] [**-v**|**--verbose**] [**--version**]

# DESCRIPTION

**hu** is a minimal coding agent that queries LLMs and executes bash commands in an iterative loop. It supports both Anthropic and OpenAI-compatible APIs.

In default mode, **hu** starts an interactive REPL. With **-p**, it runs a single task and exits.

**hu** is the plain CLI variant of the hew project, with no external dependencies beyond the Go standard library. A TUI variant is available as **hew**(1).

Project-specific instructions are loaded from an **AGENTS.md** file in the current working directory, if present.

# OPTIONS

**-p**, **--prompt** *task*
: Run the given task and exit. Without this flag, hu starts in conversational REPL mode.

**--model** *name*
: LLM model identifier (default: claude-sonnet-4-20250514).

**--base-url** *url*
: LLM API endpoint (default: https://api.anthropic.com). If the URL contains "anthropic.com", the Anthropic adapter is used; otherwise, the OpenAI-compatible adapter is used.

**--max-steps** *n*
: Maximum agent iterations. 0 uses the default of 100.

**--load-messages** *file*
: Read a JSON array of messages from *file* and prepend them to the conversation before starting. The format matches **--trajectory** output, so one agent's trajectory can seed another agent's context.

**--event-log** *file*
: Write every agent event as a JSONL line to *file*. Each line is a JSON object with "type" and "payload" fields. Uses O_APPEND for atomic writes, safe to tail from another process.

**--trajectory** *file*
: After the task finishes, write the full message history as pretty-printed JSON to *file*. Written before exit, even on error. Only applies to single-task mode (**-p**), not the REPL.

**-v**, **--verbose**
: Show internal decisions (queries, parsing, working directory).

**--disable-planning-workflow**
: Omit the planning workflow instructions from the system prompt.

**--version**
: Print version and exit.

**--continue**
: Resume the most recent session for the current project directory.

**--list-sessions**
: List all saved sessions for the current project directory.

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

# SEE ALSO

**hew**(1)

# AUTHOR

Brian Cosgrove <cosgroveb@gmail.com>
