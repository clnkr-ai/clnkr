# Ubiquitous Language

## Agent loop

| Term | Definition | Aliases to avoid |
| ---- | ---------- | ---------------- |
| **Agent** | The runtime object that owns the transcript, current working directory, model, executor, and loop policy. [1] [35] [36] [38] | Runner, loop, driver |
| **Run** | A policy loop that repeatedly requests turns, executes act turns, and stops on done, clarify, or step limit. [2] [35] [36] [38] | Full send, main loop |
| **Step** | One model query plus protocol parsing cycle that does not execute commands. [3] [36] | Turn cycle, query step |
| **ExecuteTurn** | The command-execution phase for one act turn. [4] [35] [36] [38] | Execute step, command loop |
| **Step limit** | The maximum number of commands an agent run may execute before asking for a final summary. [2] | Max steps, command cap |
| **Step-limit summary** | A done turn requested after the step limit is reached. [5] | Final response, limit summary |

## Turn protocol

| Term | Definition | Aliases to avoid |
| ---- | ---------- | ---------------- |
| **Turn** | One structured model instruction in the agent protocol. [6] | Response, message, payload |
| **Act turn** | A turn that asks the host to run one or more bash actions. [6] | Command turn, bash turn |
| **Clarify turn** | A turn that asks the user a non-empty question and stops the run. [6] [7] | Question turn, clarification |
| **Done turn** | A turn that summarizes completion and stops the run successfully. [6] [7] | Final turn, summary response |
| **Bash action** | One shell command plus an optional working directory. [6] [35] [36] [38] | Command, action |
| **Bash batch** | The ordered list of bash actions in a single act turn. [6] | Command batch, commands |
| **Reasoning** | Optional model-authored context attached to a turn. [6] | Explanation, notes |
| **Canonical turn** | The exact internal JSON shape accepted by core code and persisted for assistant transcript messages. [8] [9] | Internal turn, parsed JSON |
| **Provider turn** | The provider-specific structured-output shape translated into a canonical turn. [10] [11] [27] [31] | Wire turn, structured payload |
| **Protocol failure** | A model response that cannot be accepted as exactly one valid turn. [7] [12] [20] | Parse error, protocol error |
| **Protocol correction** | A host-authored transcript message that tells the model why the previous response was ignored. [13] | Error hint, correction message |

## Transcript

| Term | Definition | Aliases to avoid |
| ---- | ---------- | ---------------- |
| **Transcript** | The ordered message history sent to the model. [1] [20] [35] [36] | Conversation, history |
| **Message** | One role-tagged transcript entry. [14] | Chat message, entry |
| **User-authored message** | A transcript message whose content came from the human user rather than a clnkr host block. [15] | User message, authored turn |
| **Host block** | A host-authored tagged transcript message that is not user-authored content. [15] | System block, synthetic message |
| **Command result block** | A host block containing command, exit code, stdout, stderr, and optional command feedback. [16] | Command output, command message |
| **State block** | A host block containing the current working directory. [17] | Cwd message, state message |
| **Compact block** | A host block containing a summary of older transcript history. [18] | Compaction, summary block |
| **Recent tail** | The un-compacted suffix of the transcript preserved during compaction. [18] [19] | Kept messages, tail |
| **Compaction boundary** | The transcript index where the recent tail begins. [18] | Boundary, rewrite point |

## Command execution

| Term | Definition | Aliases to avoid |
| ---- | ---------- | ---------------- |
| **Executor** | The host component that runs one bash action and returns a structured command result. [20] [35] [36] [38] | Shell, runner |
| **Command result** | The structured outcome of one bash action, including streams, exit code, post-command state, and command feedback. [20] [21] | Command output, execution result |
| **Exit code** | The process status code returned by the shell command. [20] [21] | Status, return code |
| **Post-command state** | The current working directory and environment snapshot captured after a command runs. [21] [22] | Shell state, post state |
| **Process group** | The Unix process group used to cancel a command and its children together. [23] | Process tree, child cleanup |
| **Command feedback** | Git-backed host feedback attached to a command result. [14] [22] [24] | Git feedback, change summary |
| **Git baseline** | The clean-worktree snapshot taken before a command runs. [24] | Baseline, pre-state |
| **Changed files** | The paths changed by a command relative to the final working directory. [24] [26] | File list, touched files |
| **Combined diff** | The unstaged plus staged diff collected after a command runs. [25] | Diff, patch |

## Provider boundary

| Term | Definition | Aliases to avoid |
| ---- | ---------- | ---------------- |
| **Model** | The interface that sends transcript messages to an LLM provider and returns a response. [20] [28] [30] | Provider, client |
| **Provider adapter** | Code that translates between a provider API and clnkr's canonical protocol. [10] [11] [29] [30] | Model, backend |
| **OpenAI-compatible endpoint** | A non-OpenAI HTTP API that follows OpenAI Chat Completions-style request and response shapes closely enough for the OpenAI adapter. [29] [32] | OpenAI backend, OpenAI-style API |
| **Response** | One provider reply plus usage and any protocol failure. [20] [28] [30] | Turn, output |
| **Raw response** | The original assistant text preserved when protocol parsing fails. [20] | Raw payload, raw text |
| **Usage** | Token accounting for one model call. [20] [28] [30] | Cost, tokens |
| **Refusal** | A provider-declared refusal instead of usable model text. [27] [33] [34] | Safety block, refusal error |
| **Structured output** | Provider-enforced JSON output intended to contain exactly one provider turn. [27] [31] | JSON mode, schema output |

## Workflow context

| Term | Definition | Aliases to avoid |
| ---- | ---------- | ---------------- |
| **Agent skill** | A portable capability package that gives an agent specialized instructions, workflows, scripts, references, or assets. [37] | Prompt snippet, recipe |
| **Progressive disclosure** | A skill-loading pattern where an agent starts with metadata, reads full instructions only when relevant, and loads supporting files only as needed. [37] | Lazy loading, context trick |

## Relationships

- An **Agent** owns exactly one **Transcript** during a run. [1] [35] [36] [38]
- A **Run** contains zero or more **Step** cycles. [2] [3] [36]
- A **Step** returns exactly one accepted **Turn** or one **Protocol failure**. [3] [20]
- An **Act turn** contains one **Bash batch** with one or more **Bash actions**. [6] [8]
- **ExecuteTurn** produces one **Command result block** for each executed **Bash action**. [4] [16] [35] [38]
- A **Command result** may include one **Command feedback** value. [20] [22]
- **Command feedback** is collected only when a valid clean **Git baseline** exists. [24]
- A **Compact block** replaces older transcript messages while preserving the **Recent tail**. [18] [19]
- A **Provider adapter** translates one **Provider turn** into one **Canonical turn**. [10] [11] [8]
- A **Response** may contain either an accepted **Turn** or a **Protocol failure**, but not both as successful state. [20]
- An **Agent skill** is workflow context for the agent harness, not a **Turn** or **Command feedback** value in the clnkr runtime. [37]

## Example dialogue

> **Dev:** "When the model returns JSON, do we call it a **Response** or a **Turn**?" [20]
> **Domain expert:** "The provider gives us a **Response**. After protocol validation, the instruction inside it is a **Turn**." [6] [11] [20]
> **Dev:** "So a **Done turn** with a non-null bash sibling is not a partial success?" [12]
> **Domain expert:** "Right. That is a **Protocol failure**. The **Provider adapter** must reject it instead of dropping the bash field." [11] [12]
> **Dev:** "And after an **Act turn**, the shell output becomes a **Command result block**, not a **User-authored message**?" [4] [15] [16]
> **Domain expert:** "Exactly. It is a **Host block**. If it includes **Command feedback**, compaction still treats it as host output." [14] [15] [16]

## Flagged ambiguities

- "response" was used casually for both **Response** and **Turn**. Use **Response** for provider reply metadata and **Turn** for the validated agent instruction. [6] [20]
- "command output" can mean stdout/stderr only or the whole **Command result**. Use **Command result** for the full structured outcome and **stdout** or **stderr** for streams. [20] [21]
- "git feedback" and "command feedback" describe the same model-facing concept. Use **Command feedback** in agent and transcript language; reserve Git details for implementation. [14] [22] [24]
- "protocol error", "parse error", and "protocol failure" overlap. Use **Protocol failure** for the domain event; use parse error only for local error variables. [7] [20]
- "user message" is ambiguous because host blocks use the transcript role `user`. Use **User-authored message** for human text and **Host block** for clnkr-authored transcript content. [15] [17] [18]
- "step" can mean a model query or an executed command. Use **Step** only for query-plus-parse; use **Bash action** or **executed command** for command count. [3] [4]
- "skill" can mean an agent capability package or ordinary developer expertise. Use **Agent skill** only for the portable package format and workflow context. [37]

## 12-Factor Agents

clnkr is strongly 12-factor in the parts that matter for a terminal coding agent: it owns prompts, context construction, structured turn parsing, deterministic command execution, explicit control flow, transcript persistence, and protocol error recovery. It is intentionally weaker on product-agent concerns like omnichannel triggers, external human contact tools, and durable business state because clnkr's domain is a local CLI session over a working tree, not a workflow service. [39]

| Factor | What it means | clnkr fit |
| ------ | ------------- | --------- |
| **1. Natural language to tool calls** | Convert a user request into structured tool-call data before deterministic code acts. [39] | Strong fit. clnkr turns natural-language tasks into `act`, `clarify`, or `done` JSON; `act` carries bash actions that deterministic Go code executes. [6] [8] [39] |
| **2. Own your prompts** | Keep prompt text and prompt assembly under application control instead of hiding them in a framework. [39] | Strong fit. clnkr owns the base prompt, protocol examples, AGENTS.md layering, prompt omission, and prompt append behavior in repo code. [40] [41] |
| **3. Own your context window** | Treat the model input as an engineered context object, not an opaque chat history. [39] | Strong fit. clnkr explicitly appends state blocks, command result blocks, protocol corrections, canonical assistant turns, and compact blocks into the transcript. [9] [13] [16] [17] [18] |
| **4. Tools are just structured outputs** | Tool use is model-emitted structured data plus deterministic host execution. [39] | Strong fit. clnkr's `Act turn` is structured output; `ExecuteTurn` interprets it and calls the executor. [4] [6] [8] |
| **5. Unify execution state and business state** | Store enough state in one timeline to resume and reason about the workflow. [39] | Partial fit. clnkr keeps execution state in transcript messages and session files, but it has no separate business object model beyond the working tree, cwd, environment, and git feedback. [17] [20] [42] |
| **6. Launch/Pause/Resume with simple APIs** | Start, stop, and resume agents without coupling callers to internal orchestration. [39] | Partial fit. clnkr has CLI launch, `--continue`, `--load-messages`, saved sessions, and single-task mode, but no HTTP/webhook API or durable pause primitive outside local session files. [42] [43] |
| **7. Contact humans with tool calls** | Model-initiated human contact should be represented as an explicit tool-like action. [39] | Partial fit. clnkr has `clarify` turns and approval mode, but human contact is local stdin interaction, not a generalized contact-human tool with Slack/email/SMS delivery. [6] [44] |
| **8. Own your control flow** | Deterministic code should own loop policy instead of letting the LLM or framework run the whole graph. [39] | Strong fit. `Run` and approval mode switch on accepted turn types, count protocol failures, enforce step limits, and decide when commands execute. [2] [44] |
| **9. Compact Errors into Context Window** | Feed failures back into context so the model can recover on the next step. [39] | Strong fit. Protocol failures become protocol correction blocks, command stderr and exit codes become command result blocks, and compaction preserves recent context while summarizing older history. [13] [16] [18] [19] |
| **10. Small, Focused Agents** | Keep agents narrow enough that prompts, tools, and control flow stay understandable. [39] | Mixed fit. clnkr is a focused coding-agent CLI with one shipped binary and a small core API, but the available bash surface is intentionally broad. [1] [20] |
| **11. Trigger from anywhere, meet users where they are** | Allow agents to start from the channels and systems where work arrives. [39] | Weak fit. clnkr starts from CLI flags, stdin, and an interactive REPL; it does not expose Slack, email, webhook, cron, or service integrations. [43] |
| **12. Make your agent a stateless reducer** | Model the agent as state plus event input producing next state and effects. [39] | Partial fit. clnkr's transcript and sessions make state explicit, but `Agent` is a mutable in-process object and `Step`/`ExecuteTurn` mutate messages, cwd, and environment directly. [1] [3] [4] [42] |

Overall: clnkr is closest to a 12-factor terminal agent core, not a 12-factor production workflow platform. It does the high-leverage engineering pieces directly: structured output instead of magical tool dispatch, prompt ownership, context ownership, deterministic control flow, transcript persistence, and explicit recovery after invalid model output. It deliberately does less on distributed state, external triggers, and human-contact channels because those would expand the product boundary beyond a local coding CLI. [1] [2] [39]

## References

[1] [clnkr `agent.go`: Agent fields](https://github.com/clnkr-ai/clnkr/blob/main/agent.go#L17-L30)

[2] [clnkr `agent.go`: Run policy loop](https://github.com/clnkr-ai/clnkr/blob/main/agent.go#L265-L307)

[3] [clnkr `agent.go`: Step query-parse cycle](https://github.com/clnkr-ai/clnkr/blob/main/agent.go#L152-L172)

[4] [clnkr `agent.go`: ExecuteTurn command execution](https://github.com/clnkr-ai/clnkr/blob/main/agent.go#L175-L237)

[5] [clnkr `agent.go`: RequestStepLimitSummary](https://github.com/clnkr-ai/clnkr/blob/main/agent.go#L239-L262)

[6] [clnkr `protocol.go`: turn and turn variants](https://github.com/clnkr-ai/clnkr/blob/main/protocol.go#L9-L38)

[7] [clnkr `protocol.go`: protocol errors and turn examples](https://github.com/clnkr-ai/clnkr/blob/main/protocol.go#L70-L80)

[8] [clnkr `turnschema.go`: canonical turn parser](https://github.com/clnkr-ai/clnkr/blob/main/turnschema.go#L29-L53)

[9] [clnkr `agent.go`: assistant turns persisted as canonical JSON](https://github.com/clnkr-ai/clnkr/blob/main/agent.go#L83-L94)

[10] [clnkr `openaiwire/protocol.go`: provider wire envelope](https://github.com/clnkr-ai/clnkr/blob/main/internal/providers/openaiwire/protocol.go#L15-L35)

[11] [clnkr `openaiwire/protocol.go`: provider turn parsing](https://github.com/clnkr-ai/clnkr/blob/main/internal/providers/openaiwire/protocol.go#L166-L188)

[12] [clnkr `openaiwire/protocol.go`: provider turn shape validation](https://github.com/clnkr-ai/clnkr/blob/main/internal/providers/openaiwire/protocol.go#L235-L329)

[13] [clnkr `protocol.go`: protocol correction message](https://github.com/clnkr-ai/clnkr/blob/main/protocol.go#L101-L116)

[14] [clnkr `internal/core/transcript/types.go`: transcript message and feedback types](https://github.com/clnkr-ai/clnkr/blob/main/internal/core/transcript/types.go#L3-L22)

[15] [clnkr `internal/core/transcript/compact.go`: transcript message classification](https://github.com/clnkr-ai/clnkr/blob/main/internal/core/transcript/compact.go#L16-L43)

[16] [clnkr `internal/core/transcript/command.go`: command result block format](https://github.com/clnkr-ai/clnkr/blob/main/internal/core/transcript/command.go#L13-L27)

[17] [clnkr `internal/core/transcript/state.go`: state block format](https://github.com/clnkr-ai/clnkr/blob/main/internal/core/transcript/state.go#L14-L50)

[18] [clnkr `internal/core/transcript/compact.go`: compact block and boundary](https://github.com/clnkr-ai/clnkr/blob/main/internal/core/transcript/compact.go#L45-L80)

[19] [clnkr `internal/core/transcript/compact.go`: compaction rewrite and kept tail](https://github.com/clnkr-ai/clnkr/blob/main/internal/core/transcript/compact.go#L83-L129)

[20] [clnkr `types.go`: response, usage, model, executor, and command result](https://github.com/clnkr-ai/clnkr/blob/main/types.go#L12-L50)

[21] [clnkr `executor.go`: command execution result fields](https://github.com/clnkr-ai/clnkr/blob/main/executor.go#L34-L99)

[22] [clnkr `executor.go`: post-command state and command feedback attachment](https://github.com/clnkr-ai/clnkr/blob/main/executor.go#L101-L126)

[23] [clnkr `executor.go`: process group cancellation](https://github.com/clnkr-ai/clnkr/blob/main/executor.go#L63-L64)

[24] [clnkr `internal/core/gitfeedback/git.go`: git baseline and feedback collection](https://github.com/clnkr-ai/clnkr/blob/main/internal/core/gitfeedback/git.go#L17-L64)

[25] [clnkr `internal/core/gitfeedback/git.go`: status and combined diff collection](https://github.com/clnkr-ai/clnkr/blob/main/internal/core/gitfeedback/git.go#L74-L95)

[26] [clnkr `internal/core/gitfeedback/git.go`: changed file path normalization](https://github.com/clnkr-ai/clnkr/blob/main/internal/core/gitfeedback/git.go#L103-L140)

[27] [OpenAI docs: Structured Outputs](https://platform.openai.com/docs/guides/structured-outputs)

[28] [OpenAI API reference: Responses](https://platform.openai.com/docs/api-reference/responses)

[29] [OpenAI API reference: Chat Completions](https://platform.openai.com/docs/api-reference/chat)

[30] [Anthropic API reference: Messages](https://docs.anthropic.com/en/api/messages)

[31] [Anthropic docs: JSON output with tool schemas](https://docs.anthropic.com/en/docs/agents-and-tools/tool-use/implement-tool-use#json-output)

[32] [vLLM docs: OpenAI-compatible server](https://docs.vllm.ai/en/latest/serving/openai_compatible_server/)

[33] [clnkr `internal/providers/openai/openai.go`: OpenAI-compatible adapter and refusal handling](https://github.com/clnkr-ai/clnkr/blob/main/internal/providers/openai/openai.go#L19-L29)

[34] [clnkr `internal/providers/openairesponses/openairesponses.go`: Responses refusal handling](https://github.com/clnkr-ai/clnkr/blob/main/internal/providers/openairesponses/openairesponses.go#L254-L285)

[35] [Simon Willison: How coding agents work](https://simonwillison.net/guides/agentic-engineering-patterns/how-coding-agents-work/)

[36] [Sebastian Raschka: Components of A Coding Agent](https://magazine.sebastianraschka.com/p/components-of-a-coding-agent)

[37] [Agent Skills: Agent Skills Overview](https://agentskills.io/home)

[38] [Anthropic Engineering: Building Effective Agents](https://www.anthropic.com/engineering/building-effective-agents)

[39] [HumanLayer: 12-Factor Agents](https://github.com/humanlayer/12-factor-agents)

[40] [clnkr `prompt.go`: base prompt and protocol instructions](https://github.com/clnkr-ai/clnkr/blob/main/prompt.go#L9-L53)

[41] [clnkr `prompt.go`: prompt options and AGENTS.md layering](https://github.com/clnkr-ai/clnkr/blob/main/prompt.go#L55-L96)

[42] [clnkr `cmd/internal/session/session.go`: session persistence and resume data](https://github.com/clnkr-ai/clnkr/blob/main/cmd/internal/session/session.go#L18-L170)

[43] [clnkr `cmd/clnkr/main.go`: CLI launch, prompt, session, and single-task flow](https://github.com/clnkr-ai/clnkr/blob/main/cmd/clnkr/main.go#L185-L464)

[44] [clnkr `cmd/internal/clnkrapp/clnkrapp.go`: approval-mode human loop](https://github.com/clnkr-ai/clnkr/blob/main/cmd/internal/clnkrapp/clnkrapp.go#L126-L170)
