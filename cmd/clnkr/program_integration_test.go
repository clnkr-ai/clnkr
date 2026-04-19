package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	clnkr "github.com/clnkr-ai/clnkr"
)

func TestProgramApprovalFlowExecutesProposedCommand(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(`{"type":"act","bash":{"commands":[{"command":"printf 'hello from test\\n'","workdir":null}]},"reasoning":"emit test output"}`),
		mustResponse(`{"type":"done","summary":"done"}`),
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
		mustResponse(`{"type":"act","bash":{"commands":[{"command":"rm important.txt","workdir":null}]},"reasoning":"bad idea"}`),
		mustResponse(`{"type":"done","summary":"done"}`),
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

func TestSeedModelHistoryRendersReasoningBreadcrumbWhenEnabled(t *testing.T) {
	model := &fakeModel{}
	executor := &fakeExecutor{}
	agent := clnkr.NewAgent(model, executor, t.TempDir())
	if err := agent.AddMessages([]clnkr.Message{
		{Role: "user", Content: "inspect this repo"},
		{Role: "assistant", Content: `{"type":"act","bash":{"commands":[{"command":"ls","workdir":null}]},"reasoning":"checked repo root first"}`},
	}); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}

	m := setupModel()
	m.chat.reasoningEnabled = true
	seedModelHistory(&m, agent)
	view := m.View()
	if !strings.Contains(view.Content, "inspect this repo") {
		t.Fatalf("view should contain resumed user message, got: %q", view.Content)
	}
	if !strings.Contains(view.Content, "Reasoning trace available") {
		t.Fatalf("view should contain reasoning breadcrumb when enabled, got: %q", view.Content)
	}
}

func TestSeedModelHistoryRendersResumedHistory(t *testing.T) {
	model := &fakeModel{}
	executor := &fakeExecutor{}
	agent := clnkr.NewAgent(model, executor, t.TempDir())
	if err := agent.AddMessages([]clnkr.Message{
		{Role: "user", Content: "inspect this repo"},
		{Role: "assistant", Content: "I will inspect the repo first."},
		{Role: "user", Content: "[command]\nls\n[/command]\n[exit_code]\n0\n[/exit_code]\n[stdout]\nREADME.md\n[/stdout]\n[stderr]\n\n[/stderr]"},
		{Role: "user", Content: "[state]\n{\"source\":\"clnkr\",\"kind\":\"state\",\"cwd\":\"/tmp/project\"}\n[/state]"},
		{Role: "assistant", Content: "The repo has a README."},
	}); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}

	m := setupModel()
	seedModelHistory(&m, agent)
	view := m.View()
	if !strings.Contains(view.Content, "inspect this repo") {
		t.Fatalf("view should contain resumed user message, got: %q", view.Content)
	}
	if !strings.Contains(view.Content, "I will inspect the repo first.") {
		t.Fatalf("view should contain resumed assistant message, got: %q", view.Content)
	}
	if !strings.Contains(view.Content, "README.md") {
		t.Fatalf("view should contain resumed command output, got: %q", view.Content)
	}
	if !strings.Contains(view.Content, "The repo has a README.") {
		t.Fatalf("view should contain final assistant message, got: %q", view.Content)
	}
}

func TestProgramResponseCachesLatestReasoningWhenEnabled(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(`{"type":"done","summary":"done","reasoning":"checked parser -> protocol -> ui"}`),
	}}
	executor := &fakeExecutor{}
	agent := clnkr.NewAgent(model, executor, t.TempDir())

	h := startProgramHarness(t, agent, "explain it")
	defer h.Quit()
	h.WaitFinished()

	fm := h.FinalModel()
	if fm == nil {
		t.Fatal("final model unavailable")
	}
	if got, want := fm.reasoningInfo.latest, "checked parser -> protocol -> ui"; got != want {
		t.Fatalf("latest reasoning = %q, want %q", got, want)
	}
	if !fm.reasoningInfo.enabled {
		t.Fatal("reasoning cache should be enabled")
	}
}

type programHarness struct {
	t      *testing.T
	p      *tea.Program
	output *lockedBuffer
	shared *shared
	done   chan tea.Model
	final  tea.Model
}

func startProgramHarness(t *testing.T, agent *clnkr.Agent, task string, opts ...modelOpts) *programHarness {
	t.Helper()

	s := defaultStyles(true)
	cfg := modelOpts{
		styles:          s,
		modelName:       "test-model",
		maxSteps:        agent.MaxSteps,
		fullSend:        false,
		exitOnRunFinish: true,
	}
	m := newModel(cfg)
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
		h.final = finalModel
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

func (h *programHarness) FinalModel() *model {
	h.t.Helper()
	if h.final == nil {
		return nil
	}
	m, ok := h.final.(model)
	if !ok {
		return nil
	}
	return &m
}

func (h *programHarness) WaitFinished() {
	h.t.Helper()

	select {
	case fm := <-h.done:
		h.final = fm
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
