+++
title = "clnku"
description = "Plain CLI manual page"
slug = "clnku"
weight = 10
+++

> Generated from [clnku.1.md](https://github.com/clnkr-ai/clnkr/blob/main/doc/clnku.1.md).

# NAME

clnku - a minimal coding agent (plain CLI)

# SYNOPSIS

**clnku** [**-p** *task*] [**--model** *name*] [**--base-url** *url*] [**--max-steps** *n*] [**--full-send**] [**--load-messages** *file*] [**--event-log** *file*] [**--trajectory** *file*] [**-v**|**--verbose**] [**--version**]

# DESCRIPTION

**clnku** is a minimal coding agent that queries LLMs and executes bash commands using a structured JSON turn protocol. It supports both Anthropic and OpenAI-compatible APIs.

In default mode, **clnku** starts an interactive REPL. With **-p**, it runs a single task and exits.

**clnku** is the plain CLI variant of the clnkr project, with no external dependencies beyond the Go standard library. A TUI variant is available as **clnkr**(1).

The agent communicates through JSON turns: **act** (execute a command), **clarify** (ask the user), and **done** (signal completion).

By default, **clnku** asks for approval before each **act** turn. Pass **--full-send** to execute commands immediately without approval.

Project-specific instructions are loaded from an **AGENTS.md** file in the current working directory, if present.

# OPTIONS

**-p**, **--prompt** *task*
: Run the given task and exit. Without this flag, clnku starts in conversational REPL mode.

**--model** *name*
: LLM model identifier (default: claude-sonnet-4-20250514).

**--base-url** *url*
: LLM API endpoint (default: https://api.anthropic.com). If the URL contains "anthropic.com", the Anthropic adapter is used; otherwise, the OpenAI-compatible adapter is used.

**--max-steps** *n*
: Maximum agent iterations. 0 uses the default of 100.

**--full-send**
: Execute every **act** turn immediately. Without this flag, clnku asks for approval before each command.

**--load-messages** *file*
: Read a JSON array of messages from *file* and prepend them to the conversation before starting. The format matches **--trajectory** output, so one agent's trajectory can seed another agent's context.

**--event-log** *file*
: Write every agent event as a JSONL line to *file*. Each line is a JSON object with "type" and "payload" fields. Uses O_APPEND for atomic writes, safe to tail from another process.

**--trajectory** *file*
: After the task finishes, write the full message history as pretty-printed JSON to *file*. Written before exit, even on error. Only applies to single-task mode (**-p**), not the REPL.

**-v**, **--verbose**
: Show internal decisions (queries, parsing, working directory).

**--version**
: Print version and exit.

**--continue**
: Resume the most recent session for the current project directory.

**--list-sessions**
: List all saved sessions for the current project directory.

# ENVIRONMENT

**CLNKR_API_KEY**
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

**clnkr**(1)

# AUTHOR

Brian Cosgrove <cosgroveb@gmail.com>
