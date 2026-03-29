# Full-Send and Per-Command Approval

Date: 2026-03-29

## Problem

`clnkr` has one real tool: arbitrary bash. Today the host executes every `ActTurn` as soon as the model emits it. That makes the runtime simple, but it also means there is no host-controlled checkpoint between "the model chose a command" and "the command ran."

That is the wrong default for this project. Bash is the dangerous tool. A fake permission lattice would not buy us much because `ActTurn` carries an unconstrained command string. The honest user-facing model is binary:

- `--full-send=true`: run every `ActTurn` immediately
- `--full-send=false`: require explicit human approval for every `ActTurn`

The default should be `false`.

## Goals

- Add `--full-send` to `clnku` and `clnkr`
- Default `--full-send` to `false`
- Require explicit approval for every `ActTurn` when `--full-send=false`
- Keep `Run()` as the full-send path
- Make approval host-enforced, not prompt-cooperative
- Land the first version in both binaries

## Non-Goals

- No command allowlist or partial permission model
- No path whitelist
- No durable pending-command resume in the first version
- No command summarization or risk scoring
- No diff previews
- No new tool types beyond bash

## Decision

Break `Step()`.

Today `Step()` means query, parse, execute. After this change it means query, parse, append the model response, and stop before command execution. `Run()` stays full-send by calling `Step()` and then immediately executing any returned `ActTurn`.

This is the cleanest API for the product we want. We do not need to preserve the old `Step()` contract before `v1.0.0`, and carrying both contracts would add code for no real gain.

## Core Design

### `Step()`

`Step()` becomes the proposal boundary.

Responsibilities:

- query the model
- append the model response to `messages`
- parse the response into a `Turn`
- on protocol failure, append the existing correction message and return a `StepResult` with `ParseErr`
- return `ActTurn`, `ClarifyTurn`, or `DoneTurn`
- do not execute commands
- do not emit command start or command done events
- do not mutate `cwd` or `env`

`StepResult` no longer carries command output or execution error because `Step()` no longer executes commands.

### `ExecuteTurn()`

Add `ExecuteTurn(ctx context.Context, act *ActTurn) (CommandResult, error)` on `Agent`.

Responsibilities:

- set executor env via `ExecutorStateSetter` when available
- emit `EventCommandStart`
- execute the command in the current `cwd`
- update `cwd` from `PostCwd`
- update `env` from `PostEnv`
- emit `EventCommandDone`
- append the existing tagged command result payload to `messages`

This keeps shared mechanics in the core and approval policy in the frontends.

### `Run()`

`Run()` stays the full-send path.

Responsibilities:

- append the initial user task
- call `Step()`
- preserve the current protocol failure loop and max-step behavior
- return `ErrClarificationNeeded` on `ClarifyTurn`
- return on `DoneTurn`
- call `ExecuteTurn()` immediately on `ActTurn`

This preserves the simple non-interactive path for embedders and for `--full-send=true`.

## Denial Semantics

A denied command does not trigger another model turn by itself.

Instead:

1. The frontend shows the raw proposed command.
2. The user rejects it.
3. The frontend asks the user what the agent should do instead.
4. Empty input is a no-op. Keep waiting.
5. Non-empty input is appended as a normal user message.
6. Then the frontend calls `Step()` again.

Reason: a denial is a human boundary. The model should not guess what "no" meant.

## `clnku` UX

When `--full-send=true`:

- keep the current `Run()` flow

When `--full-send=false`:

1. start or resume the session as today
2. append the task or user input
3. call `Step()`
4. if `DoneTurn`, exit or return to the prompt
5. if `ClarifyTurn`, print the question and wait for user input
6. if `ActTurn`, print the raw command exactly and ask for approval
7. `yes`: call `ExecuteTurn()`
8. `no`: prompt for clarification
9. empty clarification input: do nothing and keep waiting
10. non-empty clarification input: append it to the agent and continue

The first version should keep the approval prompt minimal. Plain yes or no. No submodes.

## `clnkr` UX

When `--full-send=true`:

- keep the current behavior

When `--full-send=false`:

- show the raw proposed command in the chat pane as a pending command
- expose simple approve and reject actions
- reject switches focus to clarification input
- empty clarification input keeps the UI idle
- non-empty clarification input appends a user message and resumes stepping
- approve calls `ExecuteTurn()` and resumes the loop

The first version should stay literal. No summarized command cards. No staged previews.

## Session Behavior

The first version does not persist pending approval state.

If the process exits while a command is awaiting approval:

- the session file still contains the message history
- `--continue` reloads that history
- the model will be queried again on the next step

This is a known limitation. It is acceptable for v1 because the safety boundary still exists at execution time.

## Prompt Changes

Update the base prompt to explain approval mode in plain terms:

- the host may require approval before running commands
- a rejected command is not execution failure
- after a rejection, the host may ask the user for more direction

This is prompt support, not enforcement. The host remains the source of truth.

## Testing

Core tests:

- `Step()` returns `ActTurn` without executing it
- `ExecuteTurn()` appends command output and updates `cwd` and `env`
- `Run()` still executes acts immediately
- protocol failure behavior stays the same
- max-step behavior stays the same

Plain CLI tests:

- `--full-send=true` preserves current single-task behavior
- `--full-send=false` asks approval before execution
- denial followed by empty clarification input is a no-op
- denial followed by clarification appends the user message and continues

TUI tests:

- pending command renders before execution
- approve executes the proposed command
- reject enters clarification flow
- empty clarification input leaves the pending state unresolved

## Tradeoffs

Breaking `Step()` is a real API change. I think it is the right one.

The upside: the core API now matches the actual control boundary we care about.

The cost: session resume is still message-only, so approval checkpoints are not durable in v1.

That is acceptable. Durable pending approval is separate work.
