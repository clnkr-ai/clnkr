package main

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	clnkr "github.com/clnkr-ai/clnkr"
)

func TestProgramApprovalFlowExecutesProposedCommand(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"act","command":"printf 'hello from test\\n'","reasoning":"emit test output"}`}},
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"done","summary":"done"}`}},
	}}
	executor := &fakeExecutor{results: []clnkr.CommandResult{
		{Stdout: "hello from test\n", ExitCode: 0},
	}}
	agent := clnkr.NewAgent(model, executor, t.TempDir())

	h := startProgramHarness(t, agent, "say hello")
	defer h.Quit()

	waitForCondition(t, 5*time.Second, "first model step", func() bool {
		return model.callCount() == 1
	})
	h.WaitForApproval()
	h.Send(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	waitForCondition(t, 5*time.Second, "command execution and second model step", func() bool {
		return executor.callCount() == 1 && model.callCount() == 2
	})
	h.WaitFinished()

	if got := executor.callCount(); got != 1 {
		t.Fatalf("executor calls = %d, want 1", got)
	}
	if got := model.callCount(); got != 2 {
		t.Fatalf("model calls = %d, want 2", got)
	}
}

func TestProgramGuidanceReplyBecomesNextUserTurn(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"act","command":"rm important.txt","reasoning":"bad idea"}`}},
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"done","summary":"done"}`}},
	}}
	executor := &fakeExecutor{}
	agent := clnkr.NewAgent(model, executor, t.TempDir())

	h := startProgramHarness(t, agent, "do it")
	defer h.Quit()

	waitForCondition(t, 5*time.Second, "first model step", func() bool {
		return model.callCount() == 1
	})
	h.WaitForApproval()
	h.Type("list files instead")
	h.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitForCondition(t, 5*time.Second, "guidance follow-up model step", func() bool {
		return model.callCount() == 2
	})
	h.WaitFinished()

	if got := executor.callCount(); got != 0 {
		t.Fatalf("executor calls = %d, want 0", got)
	}
	if got := model.callCount(); got != 2 {
		t.Fatalf("model calls = %d, want 2", got)
	}

	msgs := agent.Messages()
	if got, want := lastUserMessage(msgs), "list files instead"; got != want {
		t.Fatalf("last user message = %q, want %q", got, want)
	}
}

type programHarness struct {
	t      *testing.T
	p      *tea.Program
	output *lockedBuffer
	shared *shared
	done   chan tea.Model
}

func startProgramHarness(t *testing.T, agent *clnkr.Agent, task string) *programHarness {
	t.Helper()

	s := defaultStyles(true)
	m := newModel(modelOpts{
		styles:          s,
		modelName:       "test-model",
		maxSteps:        agent.MaxSteps,
		fullSend:        false,
		exitOnRunFinish: true,
	})
	m.shared.agent = agent
	m.shared.cwd = agent.Cwd()
	m.chat.appendUserMessage(task)
	m.chat.updateViewport()
	m.running = true
	m.status.startRun()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.runCtx = ctx
	eventCh := make(chan clnkr.Event, eventChSize)
	m.eventCh = eventCh
	m.closeEventChOnFinish = true
	agent.Notify = makeNotify(eventCh, nil)
	agent.AppendUserMessage(task)
	m.startupCmd = tea.Batch(stepCmd(agent, ctx), tickCmd())

	out := &lockedBuffer{}
	p := tea.NewProgram(
		m,
		tea.WithInput(&bytes.Buffer{}),
		tea.WithOutput(out),
		tea.WithoutSignals(),
	)
	m.shared.program = p

	h := &programHarness{
		t:      t,
		p:      p,
		output: out,
		shared: m.shared,
		done:   make(chan tea.Model, 1),
	}
	go func() {
		finalModel, err := p.Run()
		if err != nil {
			t.Errorf("program run: %v", err)
			close(h.done)
			return
		}
		h.done <- finalModel
		close(h.done)
	}()
	p.Send(tea.WindowSizeMsg{Width: 80, Height: 24})
	return h
}

func (h *programHarness) Send(msg tea.Msg) {
	h.t.Helper()
	h.p.Send(msg)
}

func (h *programHarness) Type(text string) {
	h.t.Helper()
	for _, r := range text {
		h.p.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (h *programHarness) WaitFinished() {
	h.t.Helper()

	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		h.t.Fatalf("timed out waiting for program finish\n\noutput:\n%s", normalizePTYOutput(h.output.String()))
	}
}

func (h *programHarness) WaitForApproval() {
	h.t.Helper()

	waitForCondition(h.t, 5*time.Second, "approval prompt", func() bool {
		return h.shared.getAwaitingApproval()
	})
}

func (h *programHarness) Quit() {
	h.p.Quit()
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func lastUserMessage(messages []clnkr.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

func waitForCondition(t *testing.T, timeout time.Duration, label string, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", label)
}
