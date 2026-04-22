% clnkr(1) User Commands

# NAME

clnkr - a minimal coding agent (TUI)

# SYNOPSIS

**clnkr** [**-p**|**--prompt** *task*] [**-m**|**--model** *name*] [**-u**|**--base-url** *url*] [**--provider** *mode*] [**--provider-api** *surface*] [**--max-steps** *n*] [**--full-send**] [**-c**|**--continue**] [**-l**|**--list-sessions**] [**-S**|**--no-system-prompt**] [**--system-prompt-append** *text*] [**--dump-system-prompt**] [**--load-messages** *file*] [**--event-log** *file*] [**--trajectory** *file*] [**-v**|**--verbose**] [**-V**|**--version**]

# DESCRIPTION

**clnkr** is a minimal coding agent with a terminal user interface (TUI), built with bubbletea. It queries LLMs and executes bash commands using a structured JSON turn protocol. It supports the Anthropic Messages API and OpenAI-compatible APIs that implement structured outputs on the selected model path.

In default mode, **clnkr** starts the TUI. With **-p**, it runs a single task and exits. When stdout is not a TTY, **clnkr** falls back to plain-text rendering.

At the main idle conversational prompt, **/compact** summarizes older transcript history while keeping recent context intact for the current working thread.

At the main idle conversational prompt, **/delegate** runs a child **clnku** task seeded with the current transcript and appends a host-generated delegation summary artifact when the child run completes.

The agent communicates through JSON turns: **act** (execute a command through a nested `bash.command` plus optional `bash.workdir`), **clarify** (ask the user), and **done** (signal completion).

By default, **clnkr** asks for approval before each **act** turn. Approval and proposal UI show the requested command and any explicit workdir override. Pass **--full-send** to execute commands immediately without approval.

For Anthropic runs, **clnkr** requests Anthropic's native structured output format on every turn. Keep **--model** on a model Anthropic documents as supporting structured output.

On OpenAI-compatible backends, the selected model path must support structured outputs. If a backend rejects the resolved OpenAI API surface, **clnkr** returns the provider error. When a proxy or gateway expects a different OpenAI surface, override with **--provider-api** or **CLNKR_PROVIDER_API**.

**clnkr** rejects `gpt-5.2-pro`, `gpt-5.4-pro`, and their dated snapshots even if you force **openai-chat-completions**, because agent turns require structured outputs.

A plain CLI variant is available as **clnku**(1).

Project-specific instructions are loaded from an **AGENTS.md** file in the current working directory, if present.

# OPTIONS

**-p**, **--prompt** *task*
: Run the given task and exit. Without this flag, clnkr starts the TUI.

**-m**, **--model** *name*
: LLM model identifier. Required unless **CLNKR_MODEL** is set.

**-u**, **--base-url** *url*
: LLM endpoint transport URL. If omitted, clnkr uses the provider default: **https://api.anthropic.com** for **anthropic** or **https://api.openai.com/v1** for **openai**. **CLNKR_BASE_URL** overrides the default when set.

**--provider** *mode*
: Provider adapter semantics: **anthropic** or **openai**. Required in normal use unless **CLNKR_PROVIDER** is set. Compatibility fallback: if provider is unset but **--base-url** or **CLNKR_BASE_URL** is explicitly set, clnkr infers the provider from that URL.

**--provider-api** *surface*
: OpenAI-only API surface override: **auto**, **openai-chat-completions**, or **openai-responses**. With **provider=openai**, **auto** prefers **openai-responses** for known supported names and other OpenAI-looking model names such as **gpt-***, **o** followed by a digit, and **codex***. Names that do not look OpenAI-ish, such as **llama3**, **gemini-2.0-flash**, **orca-***, **olmo-***, **openhermes-***, and **chatgpt-***, stay on **openai-chat-completions**. This flag is rejected for **provider=anthropic**. Delegated child runs propagate the resolved provider API only for OpenAI children.

**--max-steps** *n*
: Maximum agent iterations. 0 uses the default of 100.

**--full-send**
: Execute every **act** turn immediately. Without this flag, clnkr asks for approval before each command.

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
: After the task finishes, write the full message history as pretty-printed JSON to *file*. Written before exit, even on error. Only applies to single-task mode (**-p**), not the conversational TUI. Mutually exclusive with **--continue**.

**-v**, **--verbose**
: Show internal decisions (queries, parsing, working directory).

In the interactive TUI, parsed assistant turns with non-empty `reasoning` expose a reasoning trace. The chat shows a breadcrumb (`Reasoning trace available (press Ctrl+Y)`), and **Ctrl+Y** opens a modal with the latest cached reasoning trace. This is separate from **--verbose**: verbose mode shows debug/internal event lines, while the reasoning modal shows the model-provided `reasoning` field from the structured turn protocol.

**-V**, **--version**
: Print version and exit.

# INTERACTIVE COMMANDS

**/compact** [*instructions*]
: At the idle conversational prompt, summarize older transcript history while keeping the recent working thread intact. Optional trailing text is passed to the compaction summarizer as extra instructions.

**/delegate** *task*
: At the idle conversational prompt, run *task* in a child **clnku** process seeded with the current transcript. When the child run completes, clnkr appends a host-generated **[delegate]** artifact containing the delegated task and summary. Empty delegated tasks are rejected.

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

# AUTHOR

Brian Cosgrove <cosgroveb@gmail.com>

# SEE ALSO

**clnku**(1)
