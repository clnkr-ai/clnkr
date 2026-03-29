package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	clnkr "github.com/clnkr-ai/clnkr"
)

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

func (e *fakeExecutor) Execute(_ context.Context, command, _ string) (clnkr.CommandResult, error) {
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

func (e *fakeExecutor) SetEnv(map[string]string) {}

type clarifyReply struct {
	text string
	err  error
}

type scriptPrompter struct {
	actReplies     []clarifyReply
	clarifications []clarifyReply
	actReplyCalls  int
	clarifyCalls   int
}

func (p *scriptPrompter) ActReply(context.Context, string) (string, error) {
	if p.actReplyCalls >= len(p.actReplies) {
		return "", fmt.Errorf("no more act replies")
	}
	reply := p.actReplies[p.actReplyCalls]
	p.actReplyCalls++
	return reply.text, reply.err
}

func (p *scriptPrompter) Clarify(context.Context, string) (string, error) {
	if p.clarifyCalls >= len(p.clarifications) {
		return "", fmt.Errorf("no more clarification replies")
	}
	reply := p.clarifications[p.clarifyCalls]
	p.clarifyCalls++
	return reply.text, reply.err
}

func TestIsApprovalReply(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"y", true},
		{" y ", true},
		{"yes", false},
		{"Y", false},
		{"n", false},
		{"list files", false},
	}
	for _, tt := range tests {
		if got := isApprovalReply(tt.in); got != tt.want {
			t.Fatalf("isApprovalReply(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestRunTaskFullSendUsesRun(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"act","command":"echo hi"}`}},
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"done","summary":"done"}`}},
	}}
	executor := &fakeExecutor{results: []clnkr.CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
	agent := clnkr.NewAgent(model, executor, "/tmp")

	err := runTask(context.Background(), agent, "say hi", true, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executor.calls != 1 {
		t.Fatalf("expected 1 execute call, got %d", executor.calls)
	}
}

func TestRunApprovalTaskApproveExecutesCommand(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"act","command":"echo hi"}`}},
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"done","summary":"done"}`}},
	}}
	executor := &fakeExecutor{results: []clnkr.CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	prompter := &scriptPrompter{actReplies: []clarifyReply{{text: "y"}}}

	err := runApprovalTask(context.Background(), agent, "say hi", prompter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executor.calls != 1 {
		t.Fatalf("expected 1 execute call, got %d", executor.calls)
	}
}

func TestRunApprovalTaskNonApprovalReplyBecomesGuidance(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"act","command":"rm important.txt"}`}},
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"done","summary":"okay"}`}},
	}}
	executor := &fakeExecutor{}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	prompter := &scriptPrompter{
		actReplies: []clarifyReply{{text: "list files instead"}},
	}

	err := runApprovalTask(context.Background(), agent, "do it", prompter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executor.calls != 0 {
		t.Fatalf("non-approval reply should not execute the command")
	}
	msgs := agent.Messages()
	if len(msgs) == 0 || msgs[len(msgs)-2].Content != "list files instead" {
		t.Fatalf("guidance was not appended: %#v", msgs)
	}
}

func TestRunApprovalTaskClarifyTurnAppendsReply(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"clarify","question":"Which repo?"}`}},
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"done","summary":"okay"}`}},
	}}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	prompter := &scriptPrompter{
		clarifications: []clarifyReply{{text: "/tmp/repo"}},
	}

	err := runApprovalTask(context.Background(), agent, "inspect", prompter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msgs := agent.Messages()
	if len(msgs) == 0 || msgs[len(msgs)-2].Content != "/tmp/repo" {
		t.Fatalf("clarification was not appended: %#v", msgs)
	}
}

func TestRunApprovalTaskEmptyActReplyIsNoOp(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"act","command":"rm important.txt"}`}},
	}}
	executor := &fakeExecutor{}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	prompter := &scriptPrompter{
		actReplies: []clarifyReply{
			{text: ""},
			{err: errApprovalPending},
		},
	}

	err := runApprovalTask(context.Background(), agent, "do it", prompter)
	if !errors.Is(err, errApprovalPending) {
		t.Fatalf("got %v, want errApprovalPending", err)
	}
	if executor.calls != 0 {
		t.Fatalf("empty act reply should not execute the command")
	}
}

func TestRequireApprovalInput(t *testing.T) {
	if !approvalInputAllowed(os.ModeCharDevice, "xterm-256color") {
		t.Fatal("expected char-device mode to be allowed")
	}
	if approvalInputAllowed(os.ModeCharDevice, "") {
		t.Fatal("expected empty TERM to be rejected")
	}
	if approvalInputAllowed(0, "xterm-256color") {
		t.Fatal("expected regular file mode to be rejected")
	}
}

func TestStdinPrompterConfirmWritesPromptToStderr(t *testing.T) {
	stderr := captureStderr(t, func() {
		p := &stdinPrompter{reader: newLineReader(strings.NewReader("y\n"))}
		reply, err := p.ActReply(context.Background(), "rm important.txt")
		if err != nil {
			t.Fatalf("ActReply: %v", err)
		}
		if reply != "y" {
			t.Fatalf("reply = %q, want y", reply)
		}
	})

	if !strings.Contains(stderr, "rm important.txt") {
		t.Fatalf("stderr should contain command, got %q", stderr)
	}
	if !strings.Contains(stderr, "Send 'y' to approve, or type what the agent should do instead: ") {
		t.Fatalf("stderr should contain approval prompt, got %q", stderr)
	}
}

func TestStdinPrompterActReplyCanBeCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	captureStderr(t, func() {
		p := &stdinPrompter{reader: &lineReader{lines: make(chan lineResult)}}
		_, err := p.ActReply(ctx, "rm important.txt")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	})
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() {
		os.Stderr = old
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close write pipe: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close read pipe: %v", err)
	}
	return string(data)
}
