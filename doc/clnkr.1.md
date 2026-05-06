% clnkr(1) User Commands

# NAME

clnkr - a minimal coding agent (plain CLI)

# SYNOPSIS

**clnkr** [**-p**|**--prompt**|**--prompt-mode-unattended** *task*] [**-m**|**--model** *name*] [**-u**|**--base-url** *url*] [**--provider** *mode*] [**--provider-api** *surface*] [**--act-protocol** *protocol*] [**--effort** *level*] [**--thinking-budget-tokens** *n*] [**--max-output-tokens** *n*] [**--max-steps** *n*] [**--full-send**] [**-c**|**--continue**] [**-l**|**--list-sessions**] [**-S**|**--no-system-prompt**] [**--system-prompt-append** *text*] [**--dump-system-prompt**] [**--load-messages** *file*] [**--event-log** *file*] [**--trajectory** *file*] [**-v**|**--verbose**] [**-V**|**--version**]

# DESCRIPTION

**clnkr** is a minimal coding agent that queries LLMs and executes bash commands using an act protocol. The default protocol is **auto**, which uses provider-native **bash** tool calls when the resolved provider/API supports them and falls back to provider-portable clnkr act JSON otherwise.

In default mode, **clnkr** starts an interactive REPL. With **-p**, it runs a single unattended task and exits.

At the main idle conversational prompt, **/compact** summarizes older transcript history while keeping recent context intact for the current working thread.

The built-in prompt teaches the model when to launch **clnkrd** through bash for
bounded child work. A child is an ordinary process with stdout, stderr, and
optional **--event-log** artifacts. **/delegate** *task* is ordinary prompt text;
the host does not intercept it.

**clnkr** has no external dependencies beyond the Go standard library.

The agent communicates through JSON turns: **act** (execute one or more `bash.commands[]` entries with `command` and nullable `workdir`), **clarify** (ask the user), and **done** (signal completion).

In **tool-calls** mode, command execution uses provider-native **bash** tool calls. **clarify** and **done** remain text JSON. Explicit **tool-calls** is rejected for OpenAI Chat Completions and OpenAI-compatible endpoints.

By default, **clnkr** asks for approval before each **act** turn in conversational mode. One approval accepts the whole command batch. Approval prompts show each requested command and any explicit workdir override. Pass **--full-send** to execute commands immediately without approval. Single-task mode (**-p**) implies **--full-send** and uses an unattended turn contract that omits **clarify**.

For Anthropic runs, **clnkr** requests Anthropic's native structured output format on every turn. Use **--model** with a model Anthropic documents as supporting structured output.

On OpenAI-compatible backends, the selected model path must support structured outputs. If a backend rejects the resolved OpenAI API surface, **clnkr** returns the provider error. When a proxy or gateway expects a different OpenAI surface, override with **--provider-api** or **CLNKR_PROVIDER_API**.

**clnkr** rejects `gpt-5.2-pro`, `gpt-5.4-pro`, and their dated snapshots even if you force **openai-chat-completions**, because agent turns require structured outputs.

Project-specific instructions are loaded from an **AGENTS.md** file in the current working directory, if present.

# OPTIONS

**-p**, **--prompt** *task*
: Run the given task unattended and exit. Without this flag, clnkr starts in conversational REPL mode. This implies **--full-send** and removes **clarify** from the model turn contract; passing **--full-send=false** with **-p** is rejected.

**--prompt-mode-unattended** *task*
: Long alias for **-p** and **--prompt**. When preceded by **--dump-system-prompt** with no *task*, selects the unattended prompt contract and exits after printing it.

**-m**, **--model** *name*
: LLM model identifier. Required unless **CLNKR_MODEL** is set.

**-u**, **--base-url** *url*
: LLM endpoint transport URL. If omitted, clnkr uses the provider default: **https://api.anthropic.com** for **anthropic** or **https://api.openai.com/v1** for **openai**. **CLNKR_BASE_URL** overrides the default when set.

**--provider** *mode*
: Provider adapter semantics: **anthropic** or **openai**. Required in normal use unless **CLNKR_PROVIDER** is set. Compatibility fallback: if provider is unset but **--base-url** or **CLNKR_BASE_URL** is explicitly set, clnkr infers the provider from that URL.

**--provider-api** *surface*
: OpenAI-only API surface override: **auto**, **openai-chat-completions**, or **openai-responses**. With **provider=openai**, **auto** prefers **openai-responses** for known supported names and other OpenAI-looking model names such as **gpt-***, **o** followed by a digit, **codex**, **codex-***, names ending in **-codex**, and names containing **-codex-**. Names that do not look OpenAI-ish, such as **llama3**, **gemini-2.0-flash**, **orca-***, **olmo-***, **openhermes-***, and **chatgpt-***, stay on **openai-chat-completions**. This flag is rejected for **provider=anthropic**.

**--act-protocol** *protocol*
: Command proposal mode: **auto**, **clnkr-inline**, or **tool-calls**. The default is **auto**, which uses provider-native **bash** tool calls when the resolved provider/API supports them and falls back to **clnkr-inline** otherwise. **clnkr-inline** accepts provider-portable clnkr act JSON in assistant text. **tool-calls** forces provider-native tools and is rejected when unsupported. Most users do not need this flag.

**--effort** *level*
: Provider effort level. Accepted values are **auto**, **low**, **medium**, **high**, **xhigh**, and **max**. Provider/model validation rejects levels that are not supported. For OpenAI Responses, maps to reasoning effort; **gpt-5-pro** accepts only **high**, and **xhigh** is accepted only for known codex-max-or-newer model families. For Anthropic, **low**, **medium**, and **high** send both `output_config.effort` and `thinking.type=adaptive` to the API. **auto** omits both fields. **max** is accepted only where supported and is otherwise rejected.

**--thinking-budget-tokens** *n*
: Anthropic manual thinking budget (legacy/debug flag). Requires **provider=anthropic** and a Claude model that supports extended thinking. Uses `thinking.type=enabled` with `budget_tokens=n`. Cannot be used with non-auto **--effort** or with Opus 4.7+ models. The value must be at least 1024 and less than the effective Anthropic **max_tokens** value.

**--max-output-tokens** *n*
: Provider output-token limit. Supported for Anthropic Messages and OpenAI Responses. For Anthropic it overrides the **max_tokens** request field; the default is 4096 when unset. Anthropic values above 21333 are rejected. OpenAI Chat Completions rejects this flag.

**--max-steps** *n*
: Maximum executed commands before requesting a final summary. 0 uses the default of 100. If an **act** batch exceeds the remaining budget, clnkr executes only the commands that fit and then requests a final summary.

**--full-send**
: Execute every **act** turn immediately. Without this flag, clnkr asks for approval before each command batch in conversational mode. Implied by **-p**.

**-c**, **--continue**
: Resume the most recent session for the current project directory. Saved JSON state messages restore the last persisted working directory. Mutually exclusive with **--trajectory**.

**-l**, **--list-sessions**
: List all saved sessions for the current project directory and exit.

**-S**, **--no-system-prompt**
: Omit the built-in system prompt entirely.

**--system-prompt-append** *text*
: Append *text* to the built-in system prompt after any loaded **AGENTS.md** content.

**--dump-system-prompt**
: Print the composed system prompt and exit. By default this prints the conversational prompt. Use **--dump-system-prompt -p** or **--dump-system-prompt --prompt-mode-unattended** to print the unattended prompt.

**--load-messages** *file*
: Read a JSON message array, or a compatible envelope with a **messages** field, from *file* and prepend the messages to the conversation before starting. Host-generated JSON state messages in that transcript restore the current working directory.

**--event-log** *file*
: Write every agent event as a JSONL line to *file*. Each line is a JSON object with "type" and "payload" fields. Uses O_APPEND for atomic writes, safe to tail from another process.

**--trajectory** *file*
: After the task finishes, write the full message history as a pretty-printed JSON array to *file*. Written before exit, even on error. Only applies to single-task mode (**-p**), not the REPL. Mutually exclusive with **--continue**.

**-v**, **--verbose**
: Show internal decisions (queries, parsing, working directory).

**-V**, **--version**
: Print version and exit.

# INTERACTIVE COMMANDS

**/compact** [*instructions*]
: At the idle conversational prompt, summarize older transcript history while keeping the recent working thread intact. Optional trailing text is passed to the compaction summarizer as extra instructions.

This command is only available at the top-level conversational prompt. In single-task mode, approval replies, and clarification replies, the literal text is treated as normal input or rejected with an error rather than triggering compaction.

**/delegate** *task*
: Ask the model to launch **clnkrd** through bash for a bounded child task.
The text reaches the model like any other prompt. The model is expected to use
ordinary shell redirection, temporary directories, **--event-log**, and **wait**
when it runs child processes. Treat child output as evidence, not authority;
verify important child claims before using them for edits or final answers.

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
: Error (no API key, invalid flags, session load failure, failed final step-limit summary, etc.).

**2**
: In single-task mode or non-interactive **--full-send** mode, the run stopped because the agent asked for clarification. The question is printed to stderr.

# AUTHOR

Brian Cosgrove <cosgroveb@gmail.com>

# SEE ALSO

**clnkr**(3), **clnkr**(7)
