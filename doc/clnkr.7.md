% clnkr(7) Miscellaneous Information Manual

# NAME

clnkr - architecture and agent design notes

# DESCRIPTION

**clnkr** is a terminal coding agent with a small loop: send the transcript to
a model, parse one structured turn, run host commands when the turn asks for
them, append the result, and repeat.

The user-facing command is documented in **clnkr**(1). This page documents
clnkr's act protocol, transcript structure, provider boundary, command
execution model, run metadata, and domain vocabulary.

# ARCHITECTURE

**Agent**
: The owner of the transcript, current working directory, model, executor, and
loop policy for one run.

**Model**
: The provider connection used to send transcript messages to an LLM provider
and return a response.

**Provider adapter**
: The Anthropic, OpenAI Responses, or OpenAI-compatible Chat Completions path
between a provider API and clnkr's canonical protocol.

**Provider structured-output schema**
: The provider-facing schema for APIs that require different JSON wrappers,
tool-call records, or null-field shapes. Provider adapters project those
provider-specific responses into clnkr's canonical **act**, **clarify**, or
**done** turn. The schema includes verified-completion fields for **done**
turns.

**CLI config resolver**
: The frontend-owned resolver that applies flag and **CLNKR_** environment
precedence, parses and normalizes base URLs, finds the API key, selects
provider semantics, and returns a **Resolved provider config**.

**Provider request semantics**
: The provider-domain vocabulary and validation for provider, provider API,
request options, model capability checks, and request-option validation.

**Executor**
: The host component that runs one bash action and returns a structured command
result.

**Child process**
: A **clnkrd** process launched through bash for bounded work such as
research, codebase exploration, log analysis, test investigation, or review.
It is an ordinary Unix process, not a host-owned agent subtype.

**Transcript**
: The ordered message history sent to the model.

**Transcript formatter**
: The **internal/core/transcript** boundary that formats host-authored
transcript blocks, including state messages, compact blocks, and bounded
command observations. Command-done events keep raw
stdout/stderr; the transcript receives bounded command result JSON.

**Session persistence**
: The **internal/session** boundary that stores and loads local saved-session
envelopes for **cmd/clnkr** and **cmd/clnkrd**. A saved-session envelope
contains transcript messages and run metadata.

**Driver**
: The frontend coordinator around **RunWithPolicy**. It starts prompts,
routes compaction requests, chooses approval or full-send policy for a prompt,
turns policy hooks into frontend events, and passes replies back to the run.

**cmd/clnkr**
: The terminal adapter. It resolves CLI inputs, creates the agent and driver,
renders terminal output, reads human replies, and maps terminal outcomes to
exit status.

**cmd/clnkrd**
: The stdio JSONL adapter. It resolves non-interactive inputs, creates the
agent and driver, reads JSONL commands from stdin, writes JSONL events to
stdout, and writes diagnostics to stderr.

The main boundary is **Step** versus **RunWithPolicy**. **Step** performs one
model query and protocol parse. **RunWithPolicy** owns the control loop:
appending resource-state messages before queries, retrying after protocol
failures, dispatching turn policy hooks, executing approved act turns, stopping
on done, asking for a final summary at the step limit, and enforcing step
limits. In full-send runs, **RunWithPolicy** also applies an automated
completion gate before accepting **done** turns: it emits a completion-gate
event, appends guidance after rejected or challenged completions, and exits
after repeated invalid completions. **Run** is the full-send entry point; it
delegates to **RunWithPolicy** with a policy that approves act turns and leaves
clarify turns unanswered.

The frontend boundary is **Driver** versus command adapters. **Driver**
coordinates frontend interaction around **RunWithPolicy** by turning approval
and clarify policy hooks into frontend events and accepting replies. It does
not query the Model, parse Turns, or execute commands directly. **cmd/clnkr**
and **cmd/clnkrd** adapt terminal and stdio JSONL surfaces to the driver.
**internal/session** owns saved-session file mechanics for both frontends.

The configuration ownership boundary is **CLI config resolver** versus
**Provider request semantics**. The CLI resolver owns app inputs and user-facing
errors. Provider request semantics own the provider vocabulary and reject
unsupported provider/model/request combinations before adapters serialize a
request. Provider structured-output schemas and adapters own provider-specific
response shapes and projection into canonical turns.

Child-agent orchestration is deliberately outside host policy. Bash is the
model's only tool; the built-in prompt teaches the model when to launch
**clnkrd** as another ordinary process and how to read stdout, stderr, and
event-log artifacts. **/delegate** is ordinary user prompt text that instructs
the model to run **clnkrd** for the bounded child task. **Driver**,
**CommandExecutor**, provider adapters, provider request semantics, canonical
turns, and **Agent.Step** are unchanged by child-process orchestration.

# ACT PROTOCOL

Every accepted model instruction is a **turn**. There are three turn types:

**act**
: Ask the host to run one or more bash actions.

**clarify**
: Ask the user a non-empty question. Policies that can answer clarification
append the reply and continue the run; policies that cannot answer stop.

**done**
: Summarize completion with structured verification evidence and stop the run
successfully.

An **act** turn contains a **bash batch**. Each **bash action** contains a shell
command and an optional working directory. **Max steps** is the execution cap;
if a batch exceeds the remaining command budget, clnkr runs only the commands
that fit and then asks for a final summary.

A **canonical turn** is the internal JSON shape clnkr writes to the transcript
after a provider adapter projects provider output into clnkr's turn space:
**act**, **clarify**, or **done**. A **provider turn** is the provider-specific
structured-output shape before that projection.

The default act protocol is **clnkr-inline**. In that mode all accepted
act turns arrive as provider-portable clnkr act JSON in assistant text.
**tool-calls** is an explicit fail-closed protocol for Anthropic
Messages and OpenAI Responses. In that mode provider-native **bash** tool calls
become an **act** turn, while **clarify** and **done** remain text JSON.
Single-task unattended runs use a schema that omits **clarify**.

Provider request options such as effort and output-token limits change provider
wire fields. They do not add turn types or change the canonical turn shape.

A **done** turn contains a non-empty summary, a **verification** object, and a
**known_risks** array. Verification status is **verified**,
**partially_verified**, or **not_verified**. **verified** completions include at
least one concrete check with command, outcome, and evidence.
**partially_verified** completions list remaining risks. This makes completion a
claim backed by transcript evidence instead of a summary-only stop signal.

All run policies require parser/schema validation before a **done** turn can be
accepted. In full-send unattended runs, **RunWithPolicy** also applies an
automated completion-quality gate before accepting structurally valid
**done** turns. The gate rejects or challenges weak completions, challenges thin
verification once, appends guidance as a normal user message, and emits a
machine-readable completion-gate event. Approval and interactive policies leave
completion quality to the frontend or user after parser/schema validation.

A **protocol failure** is a model response that cannot be accepted as exactly
one valid turn. When that happens, clnkr appends a **protocol correction** to
the transcript. The correction tells the model why clnkr ignored the previous
response and what shape to emit next.
Final-summary protocol failures record the invalid final response and return an
error instead of appending a protocol correction.

# TRANSCRIPT

The transcript includes human-authored messages and host-authored blocks.
Host-authored blocks use normal transcript roles because they are part of the
conversation sent to the model, but they are not user-authored text.

**User-authored message**
: A transcript message whose content came from the human user.

**Assistant message**
: A transcript message whose content came from the model. Accepted model turns
are stored as canonical assistant JSON. When a normal model query fails
protocol validation, clnkr stores the raw assistant response before appending a
protocol correction.

**Host block**
: A clnkr-authored transcript message.

**Command result block**
: A host block containing a JSON object with optional **command**, **stdout**,
**stderr**, **outcome**, and optional **feedback**. Exit outcomes include
**exit_code**. Other outcomes include **timeout**, **cancelled**, **denied**,
**skipped**, and **error**. Large command observations are compressed before
they enter the model transcript. The executor and command-done events keep raw
stdout/stderr, while the transcript receives bounded **stdout** and **stderr**
strings with deterministic markers and optional **observation** metadata that
records original, shown, and omitted byte counts.

**Bash tool metadata**
: Optional transcript metadata beside message content. It records provider tool
calls, matching bash tool results, and provider/API-scoped replay items. Message
content is canonical clnkr transcript text; adapters use the metadata to replay
tool-call history without duplicating it as plain text.

**State message**
: A host message containing strict JSON current working directory state, for
example **{"type":"state","source":"clnkr","cwd":"/repo"}**.

**Resource-state message**
: A host message containing strict JSON command and model-turn budget state. It
records **commands_used** and **model_turns_used**. When a command budget is
configured, it also records **commands_remaining** and **max_commands**.

**Compact block**
: A host block containing a summary of older transcript history.

During compaction, clnkr replaces an older transcript prefix with one compact
block. It keeps a recent tail of user-authored turns and any host state the
next model call still needs.

Run metadata is not a transcript message. It is emitted as a debug event and
persisted alongside session files so a run can be inspected without adding
configuration values to the model transcript.

# COMMAND EXECUTION

The executor runs bash through the host shell. It captures stdout, stderr, exit
code, the post-command working directory, the post-command environment, and
optional command feedback.

Provider request options affect model calls only. Once a model emits an
accepted **act** turn, command execution sees the same bash batch shape.

The executor always starts each command in its own process group so cancellation
can kill the shell and its children. A **base environment snapshot**, when set,
is the complete environment used for the next command; it is not an additive
overlay on top of the parent process environment.

**Command result**
: The structured outcome of one bash action.

**Command outcome**
: The normalized completion state of a command: exit, timeout, cancelled,
denied, skipped, or error.

**Post-command state**
: The current working directory and environment snapshot captured after a
command runs.

**Base environment snapshot**
: The complete environment snapshot used as the base for command execution.

**Command feedback**
: Git-backed host feedback attached to a command result.

**Git baseline**
: The clean-worktree snapshot taken before a command runs.

Command feedback is emitted only when the command started from a clean git
baseline. The feedback contains changed file paths relative to the final
working directory and a combined unstaged plus staged diff.

On cancellation, clnkr kills the command process group. Child processes should
not survive the shell command that started them.

# PROVIDER BOUNDARY

clnkr treats provider APIs as adapters around one internal protocol.

Provider resolution is split. **cmd/internal/providerconfig** owns CLI
inputs: flag/env precedence, **CLNKR_** names, API key lookup, base URL parsing,
provider inference from an explicit base URL, and user-facing configuration
errors. **internal/providers/providerconfig** owns provider-domain semantics:
provider/API constants, provider request options, model capability predicates,
and request-option validation.

For OpenAI, clnkr can use the Responses API or an OpenAI-compatible Chat
Completions API. **provider-api=auto** selects Responses for known supported
OpenAI model names and model names matching OpenAI model-name patterns, and
keeps known non-OpenAI compatible names on Chat Completions. OpenAI-compatible
endpoints, including vLLM's compatible server, must support structured model
outputs.

For Anthropic, clnkr uses the Messages API and asks for structured JSON output
through the provider adapter.

**tool-calls** requires a provider API that can accept tools, return
tool calls with IDs, accept tool results, and let the adapter map calls/results
without prompt parsing. Anthropic Messages uses a custom **bash** tool. OpenAI
Responses uses a strict function tool named **bash**. OpenAI Chat Completions
is rejected for tool-call mode.

Provider adapters serialize validated provider request options. They do not
resolve **CLNKR_** environment variables, choose API keys, or parse CLI base
URLs. They still join provider endpoint paths against the configured base URL so
direct adapter construction and CLI config resolution follow the same request
path rules.

**Response**
: One provider reply plus usage and any protocol failure.

**Raw response**
: Provider output preserved for debugging when protocol parsing fails. Depending
on the provider path, this may be assistant text or a raw provider payload.

**Usage**
: Token accounting for one model call.

**Refusal**
: A provider-declared refusal instead of usable model text.

**Structured output**
: Provider-enforced JSON output intended to contain exactly one provider turn.

# RUN METADATA

**Run metadata** records run and provider request configuration. It is emitted
once as an **EventDebug** payload and persisted with saved sessions.

**Requested request metadata**
: The provider request options as the user requested them, including explicit
**--effort auto**.

**Effective request metadata**
: The provider request options clnkr will put on provider calls after
validation and defaults.

Run metadata includes the selected **act_protocol**. Requested and effective
request metadata use the same JSON fields: **effort**, **output**, and
**anthropic_manual**. For effort, omitted and
explicit **auto** are distinct in requested metadata. Effective metadata omits
**auto** because clnkr sends no provider effort field for it. For Anthropic,
effective metadata also records the Anthropic thinking mode and effective
**max_tokens** when known.

# GLOSSARY

**Act proposal**
: The approval-mode prompt text shown before an accepted act turn executes.

**Act turn**
: A turn that asks the host to run one or more bash actions.

**Approval mode**
: A **RunWithPolicy** policy that asks before executing accepted act turns.

**Bash action**
: One shell command plus an optional working directory.

**Bash batch**
: The ordered list of bash actions in a single act turn.

**Bash tool call**
: A provider-native request to run the bash tool, projected into one bash
action.

**Bash tool call ID**
: The opaque provider ID attached to a bash tool call and its matching
tool result.

**Bash tool result**
: The provider-native result paired with a bash tool call ID after execution,
denial, or skipping.

**Base environment snapshot**
: The complete environment map used as the base for command execution.

**Canonical turn**
: The internal JSON shape used by core code and persisted assistant transcript
messages.

**CLI config resolver**
: The frontend-owned resolver that turns CLI inputs and **CLNKR_** environment
variables into a **Resolved provider config**.

**Command outcome**
: The normalized completion state of a command result: exit, timeout,
cancelled, denied, skipped, or error.

**Completion gate**
: The full-send policy check that rejects or challenges structurally valid
**done** turns before accepting completion.

**Clarify turn**
: A turn that asks the user a non-empty question. Policies may answer and
continue, or stop when no answer is available.

**Done turn**
: A turn that summarizes completion and stops the run successfully.

**Driver**
: The frontend coordinator that turns policy hooks and top-level frontend
requests into terminal or JSONL interaction.

**Child process**
: A **clnkrd** process launched by bash for bounded child work. Its stdout,
stderr, and event logs are ordinary process artifacts that the model must read
and synthesize.

**Effective request metadata**
: The provider request state after validation, defaults, and provider-specific
mapping.

**Effort level**
: A provider-neutral effort request: **auto**, **low**, **medium**, **high**,
**xhigh**, or **max**.

**Manual thinking budget**
: The Anthropic-only legacy request for **thinking.type=enabled** with an
explicit token budget.

**Max output tokens**
: A provider request for the maximum response output token count.

**Final summary query**
: The done-only model query clnkr sends after the step limit is reached.

**Provider API**
: The OpenAI API surface selected for an OpenAI run: **auto**,
**openai-responses**, or **openai-chat-completions**.

**Provider request options**
: The provider-neutral request settings clnkr validates before constructing a
provider adapter request.

**Act protocol**
: How the model proposes command execution: **clnkr-inline** or
**tool-calls**.

**Provider base URL**
: The configured provider API root that provider adapters join with their API
endpoint paths.

**Provider request semantics**
: Provider-domain rules for provider/API names, model capability checks, and
request-option validation.

**Provider turn**
: The provider-specific structured-output wrapper that a provider adapter
projects into a canonical turn.

**Provider replay item**
: Provider/API-scoped data that must be sent back with later tool-call
history, such as an OpenAI Responses reasoning item.

**Recent tail**
: The un-compacted suffix of the transcript preserved during compaction.

**Resolved provider config**
: The app-facing result of config resolution: provider, provider API, model,
base URL, API key, and validated provider request options.

**Requested request metadata**
: The provider request state exactly as requested by CLI inputs after
normalization.

**Resource-state message**
: A host-authored transcript message containing strict JSON command and
model-turn budget state.

**Run metadata**
: The version, provider selection, prompt hash, requested and effective request
metadata for one run.

**RunWithPolicy**
: The run loop with policy hooks for approval and clarification interaction.

**Step**
: One model query plus protocol parsing cycle.

**Step limit**
: The maximum number of commands an agent run may execute before asking for a
final summary.

**Remaining step budget**
: The number of commands left before the step limit is reached.

**Turn**
: One structured model instruction in the agent protocol.

# SEE ALSO

**clnkr**(1), **clnkr**(3)
