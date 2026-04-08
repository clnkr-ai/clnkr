+++
title = "clankerval"
description = "Evaluation runner manual page"
slug = "clankerval"
weight = 20
+++

> Generated from [clankerval.1.md](https://github.com/clnkr-ai/clankerval/blob/main/doc/clankerval.1.md).

# NAME

clankerval - evaluation runner for checked-in agent suites

# SYNOPSIS

**clankerval** [**--help**] [**--version**] *command* [*flags*]

# DESCRIPTION

**clankerval** loads checked-in evaluation suites, runs trial workspaces against an agent CLI, grades the result, and writes per-trial bundles plus run-level reports.

Today the runner supports two agent adapters: **clnku** and Claude Code.

The two top-level commands are **run** and **init**.

# COMMANDS

**run**
: Run an evaluation suite against the current directory.

**init**
: Scaffold an **evaluations/** directory with the default live-provider example suite.

# RUN OPTIONS

**--suite** *id*
: Suite identifier to run. Defaults to **default**.

**--agent** *id*
: Default agent under test. Must be **clnku** or **claude**. This is only the lowest-precedence selector. Effective agent resolution is **task.agent**, then **suite.agent**, then **--agent**. When no level sets an agent, the default is **clnku**.

**--binary** *path*
: Path to the **clnku** binary under test. When omitted, **clankerval** builds **./cmd/clnku** from the current source tree when present; otherwise it resolves **clnku** from **PATH**. Claude runs ignore this flag and resolve **claude** from **PATH** instead.

**--evals-dir** *path*
: Evaluations directory. Defaults to **./evaluations** relative to the current working directory.

**--output-dir** *path*
: Output directory for trial bundles and reports. Defaults to the evaluations directory.

# EVALUATION FILES

`clankerval init` creates this shape:

```text
evaluations/
  suites/
    default/
      suite.json
      tasks/
        001-example/
          task.json
          input/
            instruction.txt
```

**suite.json**
: Declares **id**, **description**, **mode**, optional **agent**, **trials_per_task**, **failure_policy**, and the ordered task list.

**task.json**
: Declares **instruction_file**, **working_directory**, **step_limit**, **full_send**, optional **seed_transcript_file**, optional **mode**, optional **agent**, optional **scripted_turns_file**, and grader configuration.

**scripted_turns_file**
: Required only for **mock-provider** tasks.

**input/project/AGENTS.md**
: Project-local prompt file staged into the workspace for **clnku** tasks.

**input/project/CLAUDE.md**
: Project-local prompt file staged into the workspace for Claude tasks.

# ENVIRONMENT

**CLNKR_EVALUATION_MODE**
: Run mode. Must be **mock-provider** or **live-provider**. Defaults to **mock-provider** when unset.

**CLNKR_EVALUATION_API_KEY**
: Required in **live-provider** mode by the shared run-config contract.

**CLNKR_EVALUATION_BASE_URL**
: Required in **live-provider** mode. Recorded in bundle metadata as the configured provider endpoint.

**CLNKR_EVALUATION_MODEL**
: Optional in **live-provider** mode. Defaults to **gpt-5.4-nano** when unset. Recorded in bundle metadata as the configured provider model.

**ANTHROPIC_API_KEY**
: Forwarded into Claude runs when present. In practice, Claude live runs need this set and need the **claude** CLI on **PATH**.

# OUTPUT

Each trial writes a bundle under the selected output directory. Important artifacts include:

**bundle.json**
: Canonical bundle metadata, including suite, task, trial, mode, resolved agent, provider metadata, and artifact paths.

**raw/agent/**
: Native agent artifacts, for example **trajectory.json** and **events.jsonl** for **clnku**, or **transcript.jsonl** and **result.json** for Claude.

**raw/commands.jsonl**
: Agent-neutral command trace used by graders such as **transcript_command_trace**.

**normalized/transcript.jsonl**
: Canonical visible transcript events.

**normalized/outcome.json**
: Canonical outcome record.

**normalized/graders.jsonl**
: Normalized grader results.

# EXAMPLES

Run this repo's checked-in dummy self-test suite:

```bash
make evaluations
```

Scaffold the default suite:

```bash
clankerval init
```

Run a consuming project's suite against **clnku**:

```bash
export CLNKR_EVALUATION_MODE=live-provider
export CLNKR_EVALUATION_API_KEY=your-api-key
export CLNKR_EVALUATION_BASE_URL=https://api.openai.com/v1
export CLNKR_EVALUATION_MODEL=gpt-5.4-nano

clankerval run --suite default --binary /path/to/clnku
```

Run a consuming project's suite against Claude Code:

```bash
export CLNKR_EVALUATION_MODE=live-provider
export CLNKR_EVALUATION_API_KEY=placeholder
export CLNKR_EVALUATION_BASE_URL=https://api.anthropic.com
export CLNKR_EVALUATION_MODEL=claude-code-default
export ANTHROPIC_API_KEY=your-anthropic-key

clankerval run --suite default --agent claude
```

Run the checked-in manual Claude smoke suite:

```bash
CLANKERVAL_CLAUDE_LIVE_SMOKE=1 \
go test ./internal/evaluations -run TestClaudeLiveSmokeSuite -count=1
```

# EXIT STATUS

**0**
: Success.

**1**
: Error.

# AUTHOR

Brian Cosgrove <cosgroveb@gmail.com>
