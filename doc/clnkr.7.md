% clnkr(7) Miscellaneous Information Manual

# NAME

clnkr - architecture and agent design notes

# DESCRIPTION

**clnkr** is a terminal coding agent with a small loop: send the transcript to
a model, parse one structured turn, run host commands when the turn asks for
them, append the result, and repeat.

The user-facing command is documented in **clnkr**(1). This page documents
clnkr's turn protocol, transcript structure, provider boundary, command
execution model, and 12-factor-agent mapping.

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

**Executor**
: The host component that runs one bash action and returns a structured command
result.

**Transcript**
: The ordered message history sent to the model.

The main boundary is **Step** versus **Run**. **Step** performs one model query
and protocol parse. **Run** owns policy: retrying after protocol failures,
executing act turns, stopping on clarify or done, and enforcing step limits.

# TURN PROTOCOL

Every accepted model instruction is a **turn**. There are three turn types:

**act**
: Ask the host to run one or more bash actions.

**clarify**
: Ask the user a non-empty question and stop the run.

**done**
: Summarize completion and stop the run successfully.

An **act** turn contains a **bash batch**. Each **bash action** contains a shell
command and an optional working directory. A batch is intentionally small: the
prompt asks for one or two commands, and the parser rejects more than three.

A **canonical turn** is the internal JSON shape clnkr writes to the transcript
after a provider adapter projects provider output into clnkr's turn space:
**act**, **clarify**, or **done**. A **provider turn** is the provider-specific
structured-output shape before that projection.

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
: A clnkr-authored tagged transcript message.

**Command result block**
: A host block containing command, exit code, stdout, stderr, and optional
command feedback.

**State block**
: A host block containing the current working directory.

**Compact block**
: A host block containing a summary of older transcript history.

During compaction, clnkr replaces an older transcript prefix with one compact
block. It keeps a recent tail of user-authored turns and any host state the
next model call still needs.

# COMMAND EXECUTION

The executor runs bash through the host shell. It captures stdout, stderr, exit
code, the post-command working directory, the post-command environment, and
optional command feedback.

**Command result**
: The structured outcome of one bash action.

**Post-command state**
: The current working directory and environment snapshot captured after a
command runs.

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

For OpenAI, clnkr can use the Responses API or an OpenAI-compatible Chat
Completions API. OpenAI-compatible endpoints, including vLLM's compatible
server, must support structured model outputs.

For Anthropic, clnkr uses the Messages API and asks for structured JSON output
through the provider adapter.

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

# GLOSSARY

**Act turn**
: A turn that asks the host to run one or more bash actions.

**Bash action**
: One shell command plus an optional working directory.

**Bash batch**
: The ordered list of bash actions in a single act turn.

**Canonical turn**
: The internal JSON shape used by core code and persisted assistant transcript
messages.

**Clarify turn**
: A turn that asks the user a non-empty question and stops the run.

**Done turn**
: A turn that summarizes completion and stops the run successfully.

**Recent tail**
: The un-compacted suffix of the transcript preserved during compaction.

**Step**
: One model query plus protocol parsing cycle.

**Step limit**
: The maximum number of commands an agent run may execute before asking for a
final summary.

**Turn**
: One structured model instruction in the agent protocol.

# 12-FACTOR AGENTS

clnkr matches the local-agent parts of the 12-factor model: prompt
construction, transcript state, the turn protocol, the control loop, session
persistence, and recovery after invalid model output. Distributed state,
external triggers, and Slack/email/SMS delivery are outside clnkr's local
coding CLI boundary.

**1. Natural language to tool calls**
: clnkr turns natural-language tasks into **act**, **clarify**, or **done**
JSON. **act** carries bash actions; the executor runs them.

**2. Own your prompts**
: clnkr defines the base prompt, protocol examples, AGENTS.md layering, prompt
omission, and prompt append behavior.

**3. Own your context window**
: clnkr appends state blocks, command result blocks, protocol corrections,
canonical assistant turns, and compact blocks into the transcript.

**4. Tools are just structured outputs**
: clnkr's **act** turn is structured output. **ExecuteTurn** interprets it and
calls the executor.

**5. Unify execution state and business state**
: clnkr keeps execution state in transcript messages and session files. The
working tree, cwd, environment, and git feedback are the state the agent acts
on.

**6. Launch/Pause/Resume with simple APIs**
: clnkr starts from a prompt, stdin, or an interactive session. **--continue**,
**--load-messages**, saved sessions, and single-task mode cover local resume
flows. clnkr has no HTTP API, webhook entry point, or pause state outside local
session files.

**7. Contact humans with tool calls**
: clnkr has **clarify** turns and approval mode. Human contact is local stdin
interaction, not a general contact-human tool with Slack, email, or SMS
delivery.

**8. Own your control flow**
: **Run** and approval mode switch on accepted turn types, count protocol
failures, enforce step limits, and decide when commands execute.

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
and environment.

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

**clnkr**(1)
