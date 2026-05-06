% clnkrd(1) User Commands

# NAME

clnkrd - stdio JSONL adapter for clnkr

# SYNOPSIS

**clnkrd** [**--model** *name*] [**--base-url** *url*] [**--provider** *mode*] [**--provider-api** *surface*] [**--act-protocol** *protocol*] [**--effort** *level*] [**--thinking-budget-tokens** *n*] [**--max-output-tokens** *n*] [**--max-steps** *n*] [**--continue**] [**--load-messages** *file*] [**--event-log** *file*] [**--no-system-prompt**] [**--system-prompt-append** *text*] [**--dump-system-prompt**] [**--version**]

# DESCRIPTION

**clnkrd** runs clnkr behind a newline-delimited JSON protocol on standard
input and standard output. A frontend writes one JSON command per line to
stdin. clnkrd writes one JSON event per line to stdout.

Normal diagnostics, flag errors, configuration errors, JSONL decode errors,
and run errors are written to stderr. In JSONL mode, stdout is reserved for
protocol events.

clnkrd is a stdio adapter. It does not open sockets, authenticate clients,
serve HTTP, host plugins, or manage a background service lifecycle.

clnkr can launch clnkrd through bash for bounded child work. In that use,
clnkrd is still just a stdio JSONL process; stdout, stderr, and **--event-log**
are the child artifact surfaces.

# JSONL COMMANDS

Every stdin line is one JSON object with a **type** field.

**prompt**
: Start a run.
Requires **text** and **mode**. **mode** is **approval** or **full_send**.
Only one prompt can run at a time.

```
{"type":"prompt","text":"inspect this repo","mode":"approval"}
```

**reply**
: Answer a pending **approval_request** or **clarify** event.
For approval, **text** equal to **y** approves the command batch. Any other
non-empty text rejects the batch and is sent back to the model as guidance.

```
{"type":"reply","text":"y"}
```

**compact**
: Compact older transcript history. Optional **instructions** are passed to
the compaction summarizer.

```
{"type":"compact","instructions":"keep test failures and decisions"}
```

**shutdown**
: Cancel an active run, drain pending events, and exit successfully.

```
{"type":"shutdown"}
```

# JSONL EVENTS

Every stdout event is a JSON object with **type** and **payload** fields.

**debug**
: Diagnostic metadata and internal progress. Payload: **message**.

**response**
: Accepted model turn. Payload: **turn**, **usage**, and optional **raw**.
**usage** contains **input_tokens** and **output_tokens**.

**protocol_failure**
: Rejected model response. Payload: **reason** and **raw**.

**approval_request**
: Command batch waiting for a **reply**. Payload: **prompt** and **commands**.
Each command has **command** and optional **workdir**.

**clarify**
: User question waiting for a **reply**. Payload: **question**.

**command_start**
: Command execution started. Payload: **command** and **dir**.

**command_done**
: Command execution finished. Payload: **command**, **stdout**, **stderr**,
**exit_code**, **feedback**, and optional **err**.

**compacted**
: Transcript compaction completed. Payload: **compacted_messages** and
**kept_messages**.

**done**
: Prompt run completed. Payload: **summary**.

**error**
: Driver-visible run error. Payload: **message**.

# OPTIONS

clnkrd accepts the same provider, prompt, session, and run-control options as
the clnkr CLI where they apply to non-interactive operation:
**--model**, **--base-url**, **--provider**, **--provider-api**,
**--act-protocol**, **--effort**, **--thinking-budget-tokens**,
**--max-output-tokens**, **--max-steps**, **--continue**,
**--load-messages**, **--event-log**, **--no-system-prompt**,
**--system-prompt-append**, **--dump-system-prompt**, and **--version**.

**--event-log** writes the same JSONL event stream to a file while also writing
events to stdout.

# CHILD ORCHESTRATION EXAMPLE

Run one bounded child and inspect its JSONL events:

```bash
children=$(mktemp -d /tmp/clnkr-children.$$.XXXXXX)
mkdir -p "$children/readme"
printf '%s\n' '{"type":"prompt","text":"Inspect README.md. Do not edit files. Return evidence and uncertainty.","mode":"full_send"}' |
  clnkrd --event-log "$children/readme/events.jsonl" \
    > "$children/readme/out.jsonl" \
    2> "$children/readme/err"
sed -n '1,200p' "$children/readme/out.jsonl"
```

Run two independent children in parallel, then synthesize their output:

```bash
children=$(mktemp -d /tmp/clnkr-children.$$.XXXXXX)
mkdir -p "$children/code" "$children/docs"
printf '%s\n' '{"type":"prompt","text":"Review prompt.go for clnkrd child orchestration risks. Do not edit.","mode":"full_send"}' |
  clnkrd --event-log "$children/code/events.jsonl" \
    > "$children/code/out.jsonl" 2> "$children/code/err" & pid1=$!
printf '%s\n' '{"type":"prompt","text":"Review doc/*.1.md for child orchestration docs. Do not edit.","mode":"full_send"}' |
  clnkrd --event-log "$children/docs/events.jsonl" \
    > "$children/docs/out.jsonl" 2> "$children/docs/err" & pid2=$!
wait "$pid1" "$pid2"
sed -n '1,200p' "$children/code/out.jsonl"
sed -n '1,200p' "$children/docs/out.jsonl"
```

# EXIT STATUS

**0**
: Clean EOF on stdin, **shutdown**, **--help**, **--version**, or
**--dump-system-prompt**.

**1**
: Invalid flags, missing provider configuration, bad JSONL input, invalid
command sequencing, session load failure, provider error, command/run error,
or event write failure.

**130**
: Interrupted by SIGINT.

# ENVIRONMENT

**CLNKR_API_KEY**
: API key for the LLM provider.

**CLNKR_PROVIDER**
: Provider adapter semantics.

**CLNKR_PROVIDER_API**
: OpenAI-only API surface override.

**CLNKR_MODEL**
: Default model identifier when **--model** is not provided.

**CLNKR_BASE_URL**
: Default LLM endpoint when **--base-url** is not provided.

# SEE ALSO

**clnkr**(1), **clnkr**(3), **clnkr**(7)
