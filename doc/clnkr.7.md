% clnkr(7) Miscellaneous Information Manual

# NAME

clnkr - architecture and agent design notes

# DESCRIPTION

**clnkr** is a terminal coding agent built around a small loop: send a
transcript to a model, parse exactly one structured turn, execute host commands
when requested, append the result, and repeat.

The user-facing command is documented in **clnkr**(1). This page documents the
conceptual model behind the tool: the turn protocol, transcript structure,
provider boundary, command execution model, and the project's orientation
toward 12-factor agent design.

# ARCHITECTURE

**Agent**
: The runtime object that owns the transcript, current working directory,
model, executor, and loop policy.

**Model**
: The provider-facing interface that sends transcript messages to an LLM
provider and returns a response.

**Provider adapter**
: Code that translates between a provider API and clnkr's canonical protocol.
clnkr has Anthropic, OpenAI Responses, and OpenAI-compatible Chat Completions
adapters.

**Executor**
: The host component that runs one bash action and returns a structured command
result.

**Transcript**
: The ordered message history sent to the model.

The core boundary is deliberate: **Step** performs one model query and protocol
parse, while **Run** owns policy such as retrying after protocol failures,
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

A **canonical turn** is the exact internal JSON shape accepted by the root
protocol parser and persisted as assistant transcript content. A **provider
turn** is the provider-specific structured-output shape that a provider adapter
translates into a canonical turn.

A **protocol failure** is a model response that cannot be accepted as exactly
one valid turn. When that happens, clnkr appends a **protocol correction** to
the transcript telling the model why the previous response was ignored and what
shape to emit next.

# TRANSCRIPT

The transcript includes human-authored messages and host-authored blocks.
Host-authored blocks use normal transcript roles because they are part of the
model-visible conversation, but they are not user-authored text.

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
block and preserves a recent tail of user-authored turns plus required host
state.

# COMMAND EXECUTION

The executor runs bash through the host shell and captures stdout, stderr, exit
code, post-command working directory, post-command environment, and optional
command feedback.

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

On cancellation, clnkr kills the command process group so child processes do
not keep running after the shell exits.

# PROVIDER BOUNDARY

clnkr treats provider APIs as adapters around one internal protocol.

For OpenAI, clnkr can use the Responses API or an OpenAI-compatible Chat
Completions API. OpenAI-compatible endpoints, including vLLM's compatible
server, must support the structured-output surface selected for the model path.

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

clnkr is closest to a 12-factor terminal agent core, not a 12-factor production
workflow platform. It does the high-leverage engineering pieces directly:
structured output instead of magical tool dispatch, prompt ownership, context
ownership, deterministic control flow, transcript persistence, and explicit
recovery after invalid model output. It deliberately does less on distributed
state, external triggers, and human-contact channels because those would expand
the product boundary beyond a local coding CLI.

**1. Natural language to tool calls**
: Strong fit. clnkr turns natural-language tasks into **act**, **clarify**, or
**done** JSON; **act** carries bash actions that deterministic Go code
executes.

**2. Own your prompts**
: Strong fit. clnkr owns the base prompt, protocol examples, AGENTS.md
layering, prompt omission, and prompt append behavior in repo code.

**3. Own your context window**
: Strong fit. clnkr explicitly appends state blocks, command result blocks,
protocol corrections, canonical assistant turns, and compact blocks into the
transcript.

**4. Tools are just structured outputs**
: Strong fit. clnkr's **act** turn is structured output; **ExecuteTurn**
interprets it and calls the executor.

**5. Unify execution state and business state**
: Partial fit. clnkr keeps execution state in transcript messages and session
files, but it has no separate business object model beyond the working tree,
cwd, environment, and git feedback.

**6. Launch/Pause/Resume with simple APIs**
: Partial fit. clnkr has CLI launch, **--continue**, **--load-messages**, saved
sessions, and single-task mode, but no HTTP or webhook API and no durable pause
primitive outside local session files.

**7. Contact humans with tool calls**
: Partial fit. clnkr has **clarify** turns and approval mode, but human contact
is local stdin interaction, not a generalized contact-human tool with
Slack/email/SMS delivery.

**8. Own your control flow**
: Strong fit. **Run** and approval mode switch on accepted turn types, count
protocol failures, enforce step limits, and decide when commands execute.

**9. Compact errors into context window**
: Strong fit. Protocol failures become protocol correction blocks, command
stderr and exit codes become command result blocks, and compaction preserves
recent context while summarizing older history.

**10. Small, focused agents**
: Mixed fit. clnkr is a focused coding-agent CLI with one shipped binary and a
small core API, but the available bash surface is intentionally broad.

**11. Trigger from anywhere, meet users where they are**
: Weak fit. clnkr starts from CLI flags, stdin, and an interactive REPL; it does
not expose Slack, email, webhook, cron, or service integrations.

**12. Make your agent a stateless reducer**
: Partial fit. clnkr's transcript and sessions make state explicit, but
**Agent** is a mutable in-process object and **Step**/**ExecuteTurn** mutate
messages, cwd, and environment directly.

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
