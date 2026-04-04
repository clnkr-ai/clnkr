---
name: clnkr-tui-workflow-features
description: |
  Adds rich TUI-only workflow commands to clnkr without expanding the shared core
  or forcing clnku feature parity. Use when: (1) adding slash/workflow commands
  to cmd/clnkr, (2) a feature needs child orchestration or transcript artifacts,
  (3) the plain CLI should remain minimal.
author: Claude Code
version: 1.0.0
date: 2026-04-01
---

# clnkr TUI-only Workflow Features

## When to Use

- Adding a new workflow command to `cmd/clnkr` such as `/delegate`
- The feature is UI/policy heavy and should not change core agent semantics
- `clnkr` should gain a capability that `clnku` should not necessarily share
- The result should persist as a transcript artifact and replay cleanly on resume

## When NOT to Use

- The behavior belongs in shared agent semantics (`Step`, `ExecuteTurn`, `Run`)
- Both `clnkr` and `clnku` need the same feature and surface area
- The feature is just another shell command proposal rather than frontend policy

## Problem

It is easy to over-generalize rich frontend workflows into the shared core or to assume `clnkr` and `clnku` need parity. That increases API surface, weakens the architecture split, and makes TUI-specific features harder to test. The non-obvious part is separating orchestration from shared agent behavior while still preserving transcript state and replay behavior.

## Solution

### Step 1: Keep orchestration out of the core

Treat slash/workflow commands as TUI policy first.

- Do **not** add behavior to `Agent`, `Run`, `Step`, or `ExecuteTurn` unless the feature truly changes shared semantics.
- Put reusable child-process or helper logic in an optional top-level package.
- Let `cmd/clnkr` own parsing, status handling, transcript policy, and rendering.

For `/delegate`, the reusable child orchestration lived in a new `delegate/` package, while `cmd/clnkr` handled `/delegate` parsing and async flow.

### Step 2: Do not assume `clnkr` / `clnku` feature parity

Use the repo’s binary split intentionally:

- `cmd/clnkr` is the rich UI
- `cmd/clnku` is the minimal plain CLI

If a feature is primarily workflow/UI policy, put it in `cmd/clnkr` only unless the task explicitly calls for both.

### Step 3: Persist user-visible results as transcript artifacts

If the feature produces a durable result, store it as a compact artifact message rather than replaying raw internal state.

Example artifact shape:

```text
[delegate]
{"source":"clnkr","kind":"delegate","task":"inspect compaction tests","summary":"Found test patterns."}
[/delegate]
```

This keeps the transcript durable and inspectable without dumping full child-session history into the parent transcript.

### Step 4: Render artifacts cleanly on hydration

If the artifact is user-visible, do not hide it on resume just because it is structured.

- Compact transcript blocks are hidden because they represent host compaction mechanics.
- Delegate transcript blocks are rendered as a friendly summary because they represent user-visible task output.

For hydration logic, parse the artifact and render a readable host note instead of showing raw tagged JSON.

### Step 5: Store injectable helpers behind narrow local interfaces

In `cmd/clnkr`, store helpers in model/shared state behind the smallest interface needed by the TUI.

Good:

```go
type delegateRunner interface {
    Run(context.Context, delegate.Request) (delegate.Result, error)
}
```

Bad:

```go
// Harder to stub in tests.
delegateRunner delegate.Runner
```

This keeps production wiring simple while allowing tests to inject stub runners.

### Step 6: Mirror the existing `/compact` shape

When adding another workflow command, reuse the proven TUI pattern:

- parse command in idle input handling only
- start an async `tea.Cmd`
- emit a dedicated completion message type
- append host feedback on completion/failure
- ensure literal slash input does not leak into transcript when intercepted
- ensure the command is **not** intercepted during approval or clarification mode

## Verification

1. Add focused TUI tests mirroring `/compact` coverage:
   - idle interception
   - async completion
   - host feedback + artifact append
   - non-interception during approval/clarification
2. Add hydration tests proving artifacts replay as friendly summaries, not raw JSON.
3. Run root-package tests for any new helper package and `go -C cmd/clnkr test ./...` for the TUI module.

## References

- `cmd/clnkr/ui.go`
- `cmd/clnkr/chat.go`
- `delegate/delegate.go`
- `cmd/clnkr/ui_test.go`
