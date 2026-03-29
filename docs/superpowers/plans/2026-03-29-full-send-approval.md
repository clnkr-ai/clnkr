# Full-Send Approval Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add host-enforced per-command approval to `clnku` and `clnkr`, default it on, and preserve `Run()` as the full-send path behind `--full-send=true`.

**Architecture:** Break `Agent.Step()` into a proposal boundary and move command execution into a separate core method. Keep `Run()` as the full-send policy loop, then layer approval policy in `cmd/clnku` and `cmd/clnkr` using the split API. Reuse existing events, session behavior, and prompt composition where they already fit.

**Tech Stack:** Go stdlib in the core and `cmd/clnku`; Bubble Tea and bubbles in `cmd/clnkr`.

---

## File Map

- Modify: `agent.go`
  Current ground truth: `Step()` queries, parses, executes, appends command output, and emits command events in one method at lines 79-125. `Run()` assumes `Step()` already executed the command at lines 128-179.
- Modify: `events.go`
  Current ground truth: `StepResult` still carries `Output` and `ExecErr`, which only make sense because `Step()` currently executes at lines 39-45.
- Modify: `prompt.go`
  Current ground truth: the base prompt teaches only immediate `act` execution and has no approval-mode guidance at lines 8-54.
- Modify: `agent_test.go`
  Current ground truth: the tests assume `Step()` executes commands, appends command payloads, updates `cwd` and `env`, and emits command events, especially in `TestStep` and `TestAgentPersistsShellStateBetweenCommands`.
- Modify: `cmd/clnku/main.go`
  Current ground truth: both single-task mode and REPL call `agent.Run(...)` directly at lines 286-314 and 320-339. There is no approval loop.
- Add: `cmd/clnku/main_test.go`
  Reason: `cmd/clnku` has no tests today. Approval mode will need unit coverage around prompt parsing and the manual loop.
- Modify: `cmd/clnkr/main.go`
  Current ground truth: single-task mode launches `agent.Run(...)` in a goroutine at lines 266-314, and `runPlain(...)` also calls `Run()` directly at lines 358-405.
- Modify: `cmd/clnkr/ui.go`
  Current ground truth: `startTask()` starts a full-send run with one async `agent.Run(...)` command at lines 328-355, and input handling rejects enter while `m.running` at lines 275-279.
- Modify: `cmd/clnkr/chat.go`
  Current ground truth: pending commands only exist after `EventCommandStart` at lines 71-98. Approval mode needs a host-side pending command before execution.
- Modify: `cmd/clnkr/ui_test.go`
  Current ground truth: tests only cover the full-send task loop and key handling.
- Modify: `cmd/clnkr/chat_test.go`
  Current ground truth: tests only cover event-driven pending command rendering.

### Task 1: Split Proposal from Execution in the Core

**Files:**
- Modify: `agent.go`
- Modify: `events.go`
- Modify: `agent_test.go`

- [ ] **Step 1: Write failing tests for the new split**

Add or rewrite tests in `agent_test.go` so they express the new contract:

```go
t.Run("act turn does not execute in Step", func(t *testing.T) {
	model := &fakeModel{responses: []Response{
		{Message: Message{Role: "assistant", Content: `{"type":"act","command":"ls"}`}},
	}}
	executor := &fakeExecutor{results: []CommandResult{{Stdout: "file1.go\n", ExitCode: 0}}}

	agent := NewAgent(model, executor, "/tmp")
	agent.messages = append(agent.messages, Message{Role: "user", Content: "list files"})

	result, err := agent.Step(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := result.Turn.(*ActTurn); !ok {
		t.Fatalf("expected *ActTurn, got %T", result.Turn)
	}
	if executor.calls != 0 {
		t.Fatalf("Step should not execute, got %d execute calls", executor.calls)
	}
	last := agent.messages[len(agent.messages)-1]
	if last.Role != "assistant" {
		t.Fatalf("last message role = %q, want assistant", last.Role)
	}
})

t.Run("ExecuteTurn executes act and appends payload", func(t *testing.T) {
	executor := &fakeExecutor{results: []CommandResult{{Stdout: "file1.go\n", ExitCode: 0}}}
	agent := NewAgent(&fakeModel{}, executor, "/tmp")

	act := &ActTurn{Command: "ls"}
	result, err := agent.ExecuteTurn(context.Background(), act)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExecErr != nil {
		t.Fatalf("unexpected command error: %v", result.ExecErr)
	}
	if executor.calls != 1 {
		t.Fatalf("got %d execute calls, want 1", executor.calls)
	}
	last := agent.messages[len(agent.messages)-1]
	if !strings.Contains(last.Content, "[command]\nls\n[/command]") {
		t.Fatalf("missing command payload: %q", last.Content)
	}
})
```

- [ ] **Step 2: Run the focused core tests and verify RED**

Run: `go test . -run 'TestStep|TestAgentPersistsShellStateBetweenCommands' -v`

Expected: failures where old tests still expect `Step()` to execute and where `ExecuteTurn` does not exist yet.

- [ ] **Step 3: Implement the split in `agent.go` and `events.go`**

Make these code changes:

```go
// Step runs one query-parse cycle. Policy lives in Run.
func (a *Agent) Step(ctx context.Context) (StepResult, error) {
	a.started = true
	a.notify(EventDebug{Message: "querying model..."})
	resp, err := a.model.Query(ctx, a.messages)
	if err != nil {
		return StepResult{}, fmt.Errorf("query model: %w", err)
	}
	a.notify(EventDebug{Message: fmt.Sprintf("usage: %d input, %d output tokens", resp.Usage.InputTokens, resp.Usage.OutputTokens)})
	a.messages = append(a.messages, resp.Message)
	a.notify(EventResponse{Message: resp.Message, Usage: resp.Usage})

	turn, parseErr := ParseTurn(resp.Message.Content)
	if parseErr != nil {
		a.notify(EventProtocolFailure{Reason: errorToReason(parseErr), Raw: resp.Message.Content})
		a.messages = append(a.messages, Message{Role: "user", Content: protocolCorrectionMessage(parseErr)})
		return StepResult{Response: resp, ParseErr: parseErr}, nil
	}

	a.notify(EventDebug{Message: fmt.Sprintf("parsed turn: %T", turn)})
	return StepResult{Response: resp, Turn: turn}, nil
}

func (a *Agent) ExecuteTurn(ctx context.Context, act *ActTurn) (StepResult, error) {
	if setter, ok := a.executor.(ExecutorStateSetter); ok {
		setter.SetEnv(a.env)
	}
	a.notify(EventCommandStart{Command: act.Command, Dir: a.cwd})

	execResult, execErr := a.executor.Execute(ctx, act.Command, a.cwd)
	if execResult.PostCwd != "" {
		a.cwd = execResult.PostCwd
	}
	if execResult.PostEnv != nil {
		a.env = execResult.PostEnv
	}
	a.notify(EventDebug{Message: fmt.Sprintf("cwd: %s", a.cwd)})

	payload := formatCommandOutput(execResult)
	if execErr != nil {
		a.notify(EventDebug{Message: fmt.Sprintf("command error: %v", execErr)})
	}
	a.notify(EventCommandDone{
		Command: act.Command, Stdout: execResult.Stdout, Stderr: execResult.Stderr,
		ExitCode: execResult.ExitCode, Err: execErr,
	})
	a.messages = append(a.messages, Message{Role: "user", Content: payload})
	return StepResult{Turn: act, Output: payload, ExecErr: execErr}, nil
}
```

Update `Run()` so the `*ActTurn` case explicitly calls `ExecuteTurn(...)` before it increments the executed-step count. Keep protocol failure handling and the final summary path intact.

Add one small core helper for the frontends:

```go
func (a *Agent) AppendUserMessage(text string) {
	a.started = true
	a.messages = append(a.messages, Message{Role: "user", Content: text})
}
```

Use that helper everywhere a frontend needs to append a task or clarification after the initial construction path. Do not mutate `Agent` internals from `main`.

- [ ] **Step 4: Run the focused core tests and verify GREEN**

Run: `go test . -run 'TestStep|TestAgentPersistsShellStateBetweenCommands|TestRun|TestFormatCommandOutput|TestMessages' -v`

Expected: PASS

- [ ] **Step 5: Refactor only what the new tests justify**

Keep cleanup small:

```go
// StepResult is the outcome of one agent operation.
type StepResult struct {
	Response Response
	Turn     Turn
	ParseErr error
	Output   string
	ExecErr  error
}
```

If the existing `StepResult` fields still read naturally with both `Step()` and `ExecuteTurn()`, keep them. Do not add a second result type unless the current type becomes confusing in tests.

### Task 2: Add Approval Mode to `clnku`

**Files:**
- Modify: `agent.go`
- Modify: `cmd/clnku/main.go`
- Add: `cmd/clnku/main_test.go`

- [ ] **Step 1: Write failing tests for approval parsing and the denial loop**

Create `cmd/clnku/main_test.go` with local test doubles in `package main`. Do not rely on root-package `fakeModel` or `fakeExecutor`; those are private to `agent_test.go`.

```go
type fakeModel struct {
	responses []clnkr.Response
	calls     int
}

func (m *fakeModel) Query(_ context.Context, _ []clnkr.Message) (clnkr.Response, error) {
	if m.calls >= len(m.responses) {
		return clnkr.Response{}, fmt.Errorf("no more responses")
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

type fakeExecutor struct {
	results []clnkr.CommandResult
	errs    []error
	calls   int
}

func (e *fakeExecutor) Execute(_ context.Context, command, dir string) (clnkr.CommandResult, error) {
	if e.calls >= len(e.results) {
		return clnkr.CommandResult{}, fmt.Errorf("no more results")
	}
	result := e.results[e.calls]
	if result.Command == "" {
		result.Command = command
	}
	var err error
	if e.calls < len(e.errs) {
		err = e.errs[e.calls]
	}
	e.calls++
	return result, err
}

func TestParseApprovalInput(t *testing.T) {
	tests := []struct {
		in   string
		want bool
		ok   bool
	}{
		{"y", true, true},
		{"yes", true, true},
		{"n", false, true},
		{"no", false, true},
		{"maybe", false, false},
	}
	for _, tt := range tests {
		got, ok := parseApprovalInput(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("parseApprovalInput(%q) = (%v, %v), want (%v, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestRunApprovalLoopRejectEmptyClarificationIsNoOp(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"act","command":"rm important.txt"}`}},
	}}
	executor := &fakeExecutor{}
	agent := clnkr.NewAgent(model, executor, "/tmp")

	in := strings.NewReader("no\n\n")
	var out bytes.Buffer

	err := runApprovalLoop(context.Background(), agent, "do it", bufio.NewScanner(in), &out)
	if !errors.Is(err, errApprovalPending) {
		t.Fatalf("got %v, want errApprovalPending", err)
	}
	if executor.calls != 0 {
		t.Fatalf("rejected command should not execute")
	}
}
```

- [ ] **Step 2: Run the new plain-CLI tests and verify RED**

Run: `go test ./cmd/clnku -run 'TestParseApprovalInput|TestRunApprovalLoop' -v`

Expected: FAIL because the helpers and approval loop do not exist yet.

- [ ] **Step 3: Add `--full-send` and a testable approval runner**

Modify `cmd/clnku/main.go` with a small helper layer:

```go
fullSend := flags.Bool("full-send", false, "")

func parseApprovalInput(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes":
		return true, true
	case "n", "no":
		return false, true
	default:
		return false, false
	}
}
```

Real implementation requirements:

- in single-task mode:
  - `--full-send=true`: keep `agent.Run(...)`
  - `--full-send=false`: drive `Step()` and `ExecuteTurn()` manually
- in REPL mode:
  - on approval denial, prompt for clarification and keep waiting on empty input
  - on non-empty clarification, append a normal user message and continue stepping
- if approval mode needs interactive input and stdin is not usable, fail fast with a clear error that tells the user to pass `--full-send=true`

Add `AppendUserMessage(text string)` to the core and use it from both frontends for tasks, clarifications, and denial follow-up. Do not reach into `agent.messages` from `main`.

- [ ] **Step 4: Run the focused `clnku` tests and verify GREEN**

Run: `go test ./cmd/clnku -v`

Expected: PASS

- [ ] **Step 5: Run a targeted integration check for the core plus plain CLI**

Run: `make test`

Expected: root module tests still pass, including the new `cmd/clnku` tests.

### Task 3: Add Approval Mode to `clnkr`

**Files:**
- Modify: `cmd/clnkr/main.go`
- Add: `cmd/clnkr/main_test.go`
- Modify: `cmd/clnkr/ui.go`
- Modify: `cmd/clnkr/chat.go`
- Modify: `cmd/clnkr/ui_test.go`
- Modify: `cmd/clnkr/chat_test.go`

- [ ] **Step 1: Write failing TUI tests for a pending pre-execution command**

Add tests that describe the new host-side pending state, plus one focused non-TTY plain-run test in `cmd/clnkr/main_test.go` so the fallback path is covered too:

```go
func TestChatShowsProposedCommandBeforeExecution(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	c.setProposedCommand("rm important.txt")
	if c.pendingCmd == "" {
		t.Fatal("pendingCmd should be set for a proposed command")
	}
	if strings.Contains(c.content.String(), "rm important.txt") {
		t.Fatal("proposed command should not be committed yet")
	}
}

func TestModelRejectProposalEntersClarificationMode(t *testing.T) {
	m := setupModel()
	m.pendingAct = &clnkr.ActTurn{Command: "rm important.txt"}
	m.awaitingApproval = true

	m = updateModel(t, m, tea.KeyPressMsg{Code: 'n'})
	if !m.awaitingClarification {
		t.Fatal("reject should switch to clarification mode")
	}
}

func TestRunPlainApprovalRejectsBeforeExecution(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"act","command":"rm important.txt"}`}},
	}}
	executor := &fakeExecutor{}
	agent := clnkr.NewAgent(model, executor, "/tmp")

	in := strings.NewReader("no\n\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runPlainApproval(context.Background(), agent, "do it", bufio.NewScanner(in), &stdout, &stderr, false)
	if !errors.Is(err, errApprovalPending) {
		t.Fatalf("got %v, want errApprovalPending", err)
	}
	if executor.calls != 0 {
		t.Fatalf("rejected command should not execute")
	}
}
```

- [ ] **Step 2: Run the focused TUI tests and verify RED**

Run: `cd cmd/clnkr && go test . -run 'TestChatShowsProposedCommandBeforeExecution|TestModelRejectProposalEntersClarificationMode' -v`

Expected: FAIL because the model has no approval state yet.

- [ ] **Step 3: Add approval state and async step commands to the TUI**

Implement the smallest state machine that matches current `ui.go` structure:

```go
type model struct {
	// existing fields...
	pendingAct            *clnkr.ActTurn
	awaitingApproval      bool
	awaitingClarification bool
	fullSend              bool
}

type stepDoneMsg struct {
	result clnkr.StepResult
	err    error
}

func stepCmd(agent *clnkr.Agent, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		result, err := agent.Step(ctx)
		return stepDoneMsg{result: result, err: err}
	}
}
```

Real behavior requirements:

- `--full-send=true`: keep the existing `agent.Run(...)` path
- `--full-send=false`:
  - `runPlain(...)` must also use the approval loop, not `Run()`, so `clnkr` keeps the same safety default when stdout is not a terminal
  - `startTask()` appends the user message, starts a context, and launches `stepCmd(...)`
  - `stepDoneMsg` handles `DoneTurn`, `ClarifyTurn`, `ActTurn`, and protocol failures
  - `ActTurn` sets `pendingAct`, renders the raw command, and stops automatic progress
  - approve key runs `ExecuteTurn(...)` in a `tea.Cmd`, then launches the next `stepCmd(...)`
  - reject key switches to clarification mode
  - empty clarification input is a no-op
  - non-empty clarification input appends a user message and restarts `stepCmd(...)`

Use the same keyboard surface everywhere in approval mode:

- `y`: approve
- `n`: reject and ask for clarification
- `Enter`: submit clarification when in clarification mode

- [ ] **Step 4: Run the focused TUI tests and verify GREEN**

Run: `cd cmd/clnkr && go test . -run 'TestChat|TestModel' -v`

Expected: PASS

- [ ] **Step 5: Run the full TUI test suite**

Run: `cd cmd/clnkr && go test ./...`

Expected: PASS

### Task 4: Prompt, Help Text, and Final Verification

**Files:**
- Modify: `prompt.go`
- Modify: `prompt_test.go`
- Modify: `doc/clnku.1.md`
- Modify: `doc/clnkr.1.md`
- Modify: `README.md`

- [ ] **Step 1: Write failing prompt tests for approval-mode guidance**

Add a prompt test that looks for explicit approval text:

```go
func TestBasePromptMentionsApprovalMode(t *testing.T) {
	if !strings.Contains(basePrompt, "The host may require approval before running commands.") {
		t.Fatal("base prompt should mention host approval mode")
	}
}
```

- [ ] **Step 2: Run the focused prompt tests and verify RED**

Run: `go test . -run 'TestBasePromptMentionsApprovalMode|TestLoadPromptWithOptions' -v`

Expected: FAIL because the prompt text does not mention approval mode yet.

- [ ] **Step 3: Add prompt guidance and any necessary help text updates**

Update `prompt.go` with short approval guidance inside `<rules>` or a new section:

```go
- The host may require approval before running commands.
- A denied command is not the same as a command failure.
- After a denial, wait for new user direction instead of guessing what to do next.
```

Also update `--help` text in both binaries so `--full-send` is visible and its default is clear.

Update the manpage sources in `doc/clnku.1.md` and `doc/clnkr.1.md` because this repo treats `doc/*.1.md` as the source of truth for CLI docs. If the new flag changes reference docs, run the normal generators after editing them.

- [ ] **Step 4: Run repo verification**

Run:

```bash
make fmt
make man
make check
make check-man
cd cmd/clnkr && go test ./...
```

Expected:

- formatting succeeds
- lint and root-module tests pass
- TUI module tests pass

- [ ] **Step 5: Commit the feature**

Run:

```bash
git add agent.go events.go prompt.go prompt_test.go agent_test.go cmd/clnku/main.go cmd/clnku/main_test.go cmd/clnkr/main.go cmd/clnkr/ui.go cmd/clnkr/chat.go cmd/clnkr/ui_test.go cmd/clnkr/chat_test.go docs/superpowers/plans/2026-03-29-full-send-approval.md
git commit -m "Add per-command approval mode"
```

## Self-Review

Spec coverage:

- `--full-send` on both binaries: Tasks 2 and 3
- `Step()` split from execution: Task 1
- denial becomes host-side clarification: Tasks 2 and 3
- empty clarification input is a no-op: Tasks 2 and 3
- prompt support only, not enforcement: Task 4
- no durable pending-command resume: intentionally preserved by omitting session changes from all tasks

Placeholder scan:

- No `TODO`, `TBD`, or "handle edge cases later" placeholders remain

Type consistency:

- The plan uses `ActTurn`, `StepResult`, and `ExecuteTurn(...)` consistently
- If implementation needs an `AppendUserMessage(...)` helper, define it once in the core and use that exact name everywhere
