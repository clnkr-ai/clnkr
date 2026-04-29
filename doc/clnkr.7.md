% clnkr(7) Miscellaneous Information Manual

# NAME

clnkr - architecture and agent design notes

# DESCRIPTION

**clnkr** is a terminal coding agent with a small loop: send the transcript to
a model, parse one structured turn, run host commands when the turn asks for
them, append the result, and repeat.

The user-facing command is documented in **clnkr**(1). This page documents
clnkr's act protocol, transcript structure, provider boundary, command
execution model, run metadata, and 12-factor-agent mapping.

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

**Transcript**
: The ordered message history sent to the model.

The main boundary is **Step** versus **Run**. **Step** performs one model query
and protocol parse. **Run** owns policy: retrying after protocol failures,
executing act turns, stopping on clarify or done, and enforcing step limits.

The configuration ownership boundary is **CLI config resolver** versus
**Provider request semantics**. The CLI resolver owns app inputs and user-facing
errors. Provider request semantics own the provider vocabulary and reject
unsupported provider/model/request combinations before adapters serialize a
request.

# ACT PROTOCOL

Every accepted model instruction is a **turn**. There are three turn types:

**act**
: Ask the host to run one or more bash actions.

**clarify**
: Ask the user a non-empty question and stop the run.

**done**
: Summarize completion and stop the run successfully.

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

Provider request options such as effort and output-token limits change provider
wire fields. They do not add turn types or change the canonical turn shape.

A **protocol failure** is a model response that cannot be accepted as exactly
one valid turn. When that happens, clnkr appends a **protocol correction** to
the transcript. The correction tells the model why clnkr ignored the previous
response and what shape to emit next.

# TRANSCRIPT

The transcript includes human-authored messages and host-authored blocks.
Host-authored blocks use normal transcript roles because they are part of the
conversation sent to the model, but they are not user-authored text.

**User-authored message**
: A transcript message whose content came from the human user.

**Host block**
: A clnkr-authored transcript message.

**Command result block**
: A host block containing a JSON object with optional **command**, **stdout**,
**stderr**, **outcome**, and optional **feedback**. Exit outcomes include
**exit_code**. Other outcomes include **timeout**, **cancelled**, **denied**,
**skipped**, and **error**.

**Bash tool metadata**
: Optional transcript metadata that records provider tool calls,
matching bash tool results, and provider/API-scoped replay items. The text
content remains canonical clnkr transcript text; adapters use the metadata to
serialize provider tool-call history without duplicating the same exchange as
plain text.

**State block**
: A host block containing the current working directory.

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
: The original assistant text preserved when protocol parsing fails.

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
: The local human-gated loop that asks before executing accepted act turns.

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

**Clarify turn**
: A turn that asks the user a non-empty question and stops the run.

**Done turn**
: A turn that summarizes completion and stops the run successfully.

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

**Run metadata**
: The version, provider selection, prompt hash, requested and effective request
metadata, and compaction policy for one run.

**Step**
: One model query plus protocol parsing cycle.

**Step limit**
: The maximum number of commands an agent run may execute before asking for a
final summary.

**Remaining step budget**
: The number of commands left before the step limit is reached.

**Turn**
: One structured model instruction in the agent protocol.

# 12-FACTOR AGENTS

clnkr matches the local-agent parts of the 12-factor model: prompt
construction, transcript state, the act protocol, the control loop, session
persistence, provider request configuration, and recovery after invalid model
output. Distributed state, external triggers, and Slack/email/SMS delivery are
outside clnkr's local coding CLI boundary.

**1. Natural language to tool calls**
: clnkr turns natural-language tasks into **act**, **clarify**, or **done**.
In **clnkr-inline** mode, **act** carries bash actions in assistant text. In
**tool-calls** mode, provider tool calls are projected into **act**. The
executor runs the resulting bash actions in both modes.

**2. Own your prompts**
: clnkr defines the base prompt, protocol examples, AGENTS.md layering, prompt
omission, and prompt append behavior. Run metadata records the prompt hash for
the run.

**3. Own your context window**
: clnkr appends state blocks, command result blocks, protocol corrections,
canonical assistant turns, and compact blocks into the transcript. Run metadata
is stored beside the transcript, not inside the context window.

**4. Tools are just structured outputs**
: clnkr's core sees a typed **act** turn regardless of provider wire shape.
**clnkr-inline** act turns and provider bash tool calls both become the
same executor input.

**5. Unify execution state and business state**
: clnkr keeps execution state in transcript messages and session files. The
working tree, cwd, base environment snapshot, post-command environment, and git
feedback are the state the agent acts on. Run metadata records the provider
request options used to construct model calls.

**6. Launch/Pause/Resume with simple APIs**
: clnkr starts from a prompt, stdin, or an interactive session. **--continue**,
**--load-messages**, saved sessions, and single-task mode cover local resume
flows. Saved sessions include run metadata for inspection. clnkr has no HTTP
API, webhook entry point, or pause state outside local session files.

**7. Contact humans with tool calls**
: clnkr has **clarify** turns and approval mode. Human contact is local stdin
interaction, not a general contact-human tool with Slack, email, or SMS
delivery.

**8. Own your control flow**
: **Run** and approval mode switch on accepted turn types, count protocol
failures, enforce step limits, and decide when commands execute. Approval mode
truncates an act batch to the remaining step budget before showing the proposal
or executing commands. Provider request validation happens before the control
loop constructs the model.

**9. Compact errors into context window**
: Protocol failures become protocol correction blocks. Command stderr and exit
codes become command result blocks. Compaction preserves recent context while
summarizing older history.

**10. Small, focused agents**
: clnkr is a focused coding-agent CLI with one shipped binary and a small core
API. Bash is still a broad tool surface.

**11. External triggers**
: clnkr starts from CLI flags, stdin, and an interactive REPL. It has no Slack,
email, webhook, cron, or service integrations.

**12. Make your agent a stateless reducer**
: clnkr's transcript and sessions make state explicit. **Agent** still holds
mutable state while **Step** and **ExecuteTurn** process a turn: messages, cwd,
and base environment snapshot. Provider request options live outside **Agent**
and are passed to the model adapter before the run starts.

# REFERENCES

HumanLayer 12-Factor Agents:
<https://github.com/humanlayer/12-factor-agents>

Simon Willison, "How coding agents work":
<https://simonwillison.net/guides/agentic-engineering-patterns/how-coding-agents-work/>

Sebastian Raschka, "Components of A Coding Agent":
<https://magazine.sebastianraschka.com/p/components-of-a-coding-agent>

Anthropic Engineering, "Building Effective Agents":
<https://www.anthropic.com/engineering/building-effective-agents>

Agent Skills:
<https://agentskills.io/home>

OpenAI Structured Outputs:
<https://platform.openai.com/docs/guides/structured-outputs>

OpenAI Responses API:
<https://platform.openai.com/docs/api-reference/responses>

OpenAI Chat Completions API:
<https://platform.openai.com/docs/api-reference/chat>

Anthropic Messages API:
<https://docs.anthropic.com/en/api/messages>

Anthropic JSON output with tool schemas:
<https://docs.anthropic.com/en/docs/agents-and-tools/tool-use/implement-tool-use#json-output>

vLLM OpenAI-compatible server:
<https://docs.vllm.ai/en/latest/serving/openai_compatible_server/>

# SEE ALSO

**clnkr**(1), **clnkr**(3)
