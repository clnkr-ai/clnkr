% clnku(1) User Commands

# NAME

clnku - a minimal coding agent (plain CLI)

# SYNOPSIS

**clnku** [**-p**|**--prompt** *task*] [**-m**|**--model** *name*] [**-u**|**--base-url** *url*] [**--provider** *mode*] [**--provider-api** *surface*] [**--max-steps** *n*] [**--full-send**] [**-c**|**--continue**] [**-l**|**--list-sessions**] [**-S**|**--no-system-prompt**] [**--system-prompt-append** *text*] [**--dump-system-prompt**] [**--load-messages** *file*] [**--event-log** *file*] [**--trajectory** *file*] [**-v**|**--verbose**] [**-V**|**--version**]

# DESCRIPTION

**clnku** is a minimal coding agent that queries LLMs and executes bash commands using a structured JSON turn protocol. It supports the Anthropic Messages API and OpenAI-compatible APIs that implement structured outputs on the selected model path.

In default mode, **clnku** starts an interactive REPL. With **-p**, it runs a single task and exits.

At the main idle conversational prompt, **/compact** summarizes older transcript history while keeping recent context intact for the current working thread.

**clnku** is the plain CLI variant of the clnkr project, with no external dependencies beyond the Go standard library. A TUI variant is available as **clnkr**(1).

The agent communicates through JSON turns: **act** (execute a command through a nested `bash.command` plus optional `bash.workdir`), **clarify** (ask the user), and **done** (signal completion).

By default, **clnku** asks for approval before each **act** turn. Approval prompts show the requested command and any explicit workdir override. Pass **--full-send** to execute commands immediately without approval.

For Anthropic runs, **clnku** requests Anthropic's native structured output format on every turn. Keep **--model** on a model Anthropic documents as supporting structured output.

On OpenAI-compatible backends, the selected model path must support structured outputs. If a backend rejects the resolved OpenAI API surface, **clnku** returns the provider error. When a proxy or gateway expects a different OpenAI surface, override with **--provider-api** or **CLNKR_PROVIDER_API**.

`gpt-5.2-pro`, `gpt-5.4-pro`, and their dated snapshots still fail outright for the main structured loop in this pass, even if you force **openai-chat-completions**.

Project-specific instructions are loaded from an **AGENTS.md** file in the current working directory, if present.

# OPTIONS

**-p**, **--prompt** *task*
: Run the given task and exit. Without this flag, clnku starts in conversational REPL mode.

**-m**, **--model** *name*
: LLM model identifier. Required unless **CLNKR_MODEL** is set.

**-u**, **--base-url** *url*
: LLM endpoint transport URL. If omitted, clnku uses the provider default: **https://api.anthropic.com** for **anthropic** or **https://api.openai.com/v1** for **openai**. **CLNKR_BASE_URL** overrides the default when set.

**--provider** *mode*
: Provider adapter semantics: **anthropic** or **openai**. Required in normal use unless **CLNKR_PROVIDER** is set. Compatibility fallback: if provider is unset but **--base-url** or **CLNKR_BASE_URL** is explicitly set, clnku infers the provider from that URL.

**--provider-api** *surface*
: OpenAI-only API surface override: **auto**, **openai-chat-completions**, or **openai-responses**. With **provider=openai**, **auto** prefers **openai-responses** for known supported names and other OpenAI-looking model names such as **gpt-***, **o** followed by a digit, and **codex***. Names that do not look OpenAI-ish, such as **llama3**, **gemini-2.0-flash**, **orca-***, **olmo-***, **openhermes-***, and **chatgpt-***, stay on **openai-chat-completions**. This flag is rejected for **provider=anthropic**.

**--max-steps** *n*
: Maximum agent iterations. 0 uses the default of 100.

**--full-send**
: Execute every **act** turn immediately. Without this flag, clnku asks for approval before each command.

**-c**, **--continue**
: Resume the most recent session for the current project directory. Saved JSON **[state]** messages restore the last persisted working directory. Mutually exclusive with **--trajectory**.

**-l**, **--list-sessions**
: List all saved sessions for the current project directory and exit.

**-S**, **--no-system-prompt**
: Omit the built-in system prompt entirely.

**--system-prompt-append** *text*
: Append *text* to the built-in system prompt after any loaded **AGENTS.md** content.

**--dump-system-prompt**
: Print the composed system prompt and exit.

**--load-messages** *file*
: Read a JSON array of messages from *file* and prepend them to the conversation before starting. The format matches **--trajectory** output, so one agent's trajectory can seed another agent's context. Host-generated JSON **[state]** messages in that transcript restore the current working directory.

**--event-log** *file*
: Write every agent event as a JSONL line to *file*. Each line is a JSON object with "type" and "payload" fields. Uses O_APPEND for atomic writes, safe to tail from another process.

**--trajectory** *file*
: After the task finishes, write the full message history as pretty-printed JSON to *file*. Written before exit, even on error. Only applies to single-task mode (**-p**), not the REPL. Mutually exclusive with **--continue**.

**-v**, **--verbose**
: Show internal decisions (queries, parsing, working directory).

**-V**, **--version**
: Print version and exit.

# INTERACTIVE COMMANDS

**/compact** [*instructions*]
: At the idle conversational prompt, summarize older transcript history while keeping the recent working thread intact. Optional trailing text is passed to the compaction summarizer as extra instructions.

This command is only available at the top-level conversational prompt. In single-task mode, approval replies, and clarification replies, the literal text is treated as normal input or rejected with an error rather than triggering compaction.

# ENVIRONMENT

**CLNKR_API_KEY**
: API key for the LLM provider (required).

**CLNKR_PROVIDER**
: Provider adapter semantics.

**CLNKR_PROVIDER_API**
: OpenAI-only API surface override.

**CLNKR_MODEL**
: Default model identifier when **--model** is not provided.

**CLNKR_BASE_URL**
: Default LLM endpoint when **--base-url** is not provided.

# FILES

**AGENTS.md**
: If present in the current directory, its contents are appended to the system prompt as project-specific instructions.

# EXIT STATUS

**0**
: Success.

**1**
: Error (no API key, step limit reached, invalid flags, session load failure, etc.).

**2**
: In single-task mode with **--full-send**, the run stopped because the agent asked for clarification.

# SEE ALSO

**clnkr**(1)

# AUTHOR

Brian Cosgrove <cosgroveb@gmail.com>
