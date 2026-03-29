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

type approvalReply struct {
	approved bool
	err      error
}

type clarifyReply struct {
	text string
	err  error
}

type scriptPrompter struct {
	approvals      []approvalReply
	clarifications []clarifyReply
	approvalCalls  int
	clarifyCalls   int
}

func (p *scriptPrompter) Confirm(context.Context, string) (bool, error) {
	if p.approvalCalls >= len(p.approvals) {
		return false, fmt.Errorf("no more approval replies")
	}
	reply := p.approvals[p.approvalCalls]
	p.approvalCalls++
	return reply.approved, reply.err
}

func (p *scriptPrompter) Clarify(context.Context, string) (string, error) {
	if p.clarifyCalls >= len(p.clarifications) {
		return "", fmt.Errorf("no more clarification replies")
	}
	reply := p.clarifications[p.clarifyCalls]
	p.clarifyCalls++
	return reply.text, reply.err
}

func TestRunPlainApprovalRejectsBeforeExecution(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"act","command":"rm important.txt"}`}},
	}}
	executor := &fakeExecutor{}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	prompter := &scriptPrompter{
		approvals: []approvalReply{{approved: false}},
		clarifications: []clarifyReply{
			{text: ""},
			{err: errApprovalPending},
		},
	}

	err := runPlainApproval(context.Background(), agent, "do it", prompter)
	if !errors.Is(err, errApprovalPending) {
		t.Fatalf("got %v, want errApprovalPending", err)
	}
	if executor.calls != 0 {
		t.Fatalf("rejected command should not execute")
	}
}

func TestStdinPrompterConfirmWritesPromptToStderr(t *testing.T) {
	stderr := captureStderr(t, func() {
		p := &stdinPrompter{reader: newLineReader(strings.NewReader("y\n"))}
		approved, err := p.Confirm(context.Background(), "rm important.txt")
		if err != nil {
			t.Fatalf("Confirm: %v", err)
		}
		if !approved {
			t.Fatal("expected approval")
		}
	})

	if !strings.Contains(stderr, "rm important.txt") {
		t.Fatalf("stderr should contain command, got %q", stderr)
	}
	if !strings.Contains(stderr, "Approve command? [y/n]: ") {
		t.Fatalf("stderr should contain approval prompt, got %q", stderr)
	}
}

func TestStdinPrompterConfirmCanBeCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	captureStderr(t, func() {
		p := &stdinPrompter{reader: &lineReader{lines: make(chan lineResult)}}
		_, err := p.Confirm(ctx, "rm important.txt")
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
