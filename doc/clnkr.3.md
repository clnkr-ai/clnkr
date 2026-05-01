% clnkr(3) Library Functions Manual

# NAME

clnkr - Go package for embedding the clnkr agent loop

# SYNOPSIS

```go
import "github.com/clnkr-ai/clnkr"

agent := clnkr.NewAgent(model, executor, cwd)
err := agent.Run(ctx, "inspect the repository")
```

# DESCRIPTION

Package **clnkr** exposes the agent loop used by **clnkr**(1). It is the
public Go import surface for callers that want to embed clnkr in another
frontend, test harness, or orchestration process.

The package is a loop kernel, not a full provider SDK. Provider adapters live
under **internal/** and are not importable. External callers provide a
**Model** implementation that returns clnkr turns, and an **Executor**
implementation that runs commands and returns structured command results.

Use **Agent.Run** for unattended execution. Use **Agent.RunWithPolicy** when a
frontend needs approval or clarification hooks while preserving the run loop's
protocol retry, step-limit, command execution, and final-summary behavior. Use
**Agent.Step** with **Agent.ExecuteTurn** or **Agent.RejectTurn** for custom
turn loops.

# API

**NewAgent**(*model*, *executor*, *cwd*)
: Constructs an **Agent** with **DefaultMaxSteps**.

**Model**
: Interface for model adapters. **Query** receives transcript messages and
returns a **Response**.

**Executor**
: Interface for command runners. **Execute** receives a command and working
directory and returns a **CommandResult**.

**CommandExecutor**
: Built-in Unix bash executor.

**Agent.Run**
: Runs the full-send policy loop until **done**, clarification, error, or step
limit. A **clarify** turn returns **ErrClarificationNeeded**.

**Agent.RunWithPolicy**
: Runs the same loop as **Agent.Run** with caller-supplied **RunPolicy** hooks.

**RunPolicy**
: Interface for **DecideAct** and **Clarify** hooks.

**ActProposal**, **ActDecision**, **ActDecisionApprove**, **ActDecisionReject**
: Approval proposal and decision types used by **RunWithPolicy**.

**FullSendPolicy**
: Policy implementation used by **Agent.Run**.

**Agent.Step**
: Queries the model once and parses one turn. It does not execute commands.

**Agent.ExecuteTurn**
: Runs an accepted **ActTurn** and appends command result blocks to the
transcript.

**Agent.ExecuteTurnWithSkipped**
: Runs an accepted **ActTurn** and records skipped bash tool calls.

**Agent.AppendUserMessage**
: Appends caller-authored input to the transcript.

**Agent.RejectTurn**
: Records that an approval-mode **ActTurn** was not executed.

**Agent.RequestStepLimitSummary**
: Asks the model for a final **done** turn after the caller exhausts its step
budget.

**Agent.Messages**
: Returns a cloned transcript.

**Agent.Compact**
: Rewrites older transcript history through a caller-supplied **Compactor**.

**ParseTurn**
: Validates canonical clnkr turn JSON.

**CanonicalTurnJSON**
: Marshals a validated turn into canonical transcript JSON.

**ActProtocol**, **ParseActProtocol**
: Select and validate the model act protocol.

**BashToolCall**, **BashToolResult**, **ProviderReplayItem**
: Provider-neutral transcript fields for native bash tool call replay.

**LoadPromptWithOptions**
: Builds the system prompt with optional **AGENTS.md** layers.

Run **go doc github.com/clnkr-ai/clnkr** for the complete exported API.

# EXAMPLE

```go
package main

import (
	"context"
	"errors"
	"log"

	"github.com/clnkr-ai/clnkr"
)

type model struct{}

func (model) Query(context.Context, []clnkr.Message) (clnkr.Response, error) {
	return clnkr.Response{
		Turn: clnkr.DoneTurn{Summary: "no model wired"},
	}, nil
}

func main() {
	agent := clnkr.NewAgent(model{}, &clnkr.CommandExecutor{}, ".")
	if err := agent.Run(context.Background(), "check status"); err != nil {
		if errors.Is(err, clnkr.ErrClarificationNeeded) {
			log.Print("model asked for clarification")
			return
		}
		log.Fatal(err)
	}
}
```

# NOTES

The root package is the public library surface. Packages below **internal/**
are not importable by external callers.

The built-in **CommandExecutor** assumes Unix process semantics and bash.
Windows is unsupported.

# COPYRIGHT

Copyright 2025-2026 Brian Cosgrove. Licensed under the Apache License, Version
2.0.

# SEE ALSO

**clnkr**(1), **clnkr**(7), **go**(1)
