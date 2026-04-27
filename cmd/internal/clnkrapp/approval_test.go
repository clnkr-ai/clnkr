package clnkrapp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr"
)

var errApprovalPending = errors.New("approval pending")

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
		return "", errors.New("no more act replies")
	}
	reply := p.actReplies[p.actReplyCalls]
	p.actReplyCalls++
	return reply.text, reply.err
}

func (p *scriptPrompter) Clarify(context.Context, string) (string, error) {
	if p.clarifyCalls >= len(p.clarifications) {
		return "", errors.New("no more clarification replies")
	}
	reply := p.clarifications[p.clarifyCalls]
	p.clarifyCalls++
	return reply.text, reply.err
}

func TestRunApprovalTaskRejectsCompactApprovalReply(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(actJSON("echo hi")),
		mustResponse(`{"type":"done","summary":"done"}`),
	}}
	executor := &fakeExecutor{results: []clnkr.CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	prompter := &scriptPrompter{
		actReplies: []clarifyReply{
			{text: "/compact focus on tests"},
			{text: "y"},
		},
	}
	var reported []error

	err := RunApprovalTask(context.Background(), agent, "say hi", prompter, func(err error) {
		reported = append(reported, err)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executor.calls != 1 {
		t.Fatalf("expected command execution after valid retry, got %d", executor.calls)
	}
	if hasUserMessage(agent.Messages(), "/compact focus on tests") {
		t.Fatalf("compact reply leaked into transcript: %#v", agent.Messages())
	}
	if prompter.actReplyCalls != 2 {
		t.Fatalf("expected reprompt after rejected compact reply, got %d prompts", prompter.actReplyCalls)
	}
	if len(reported) != 1 || !strings.Contains(reported[0].Error(), "/compact is only available at the conversational prompt") {
		t.Fatalf("reported = %#v, want compact rejection", reported)
	}
}

func TestRunApprovalTaskRejectsCompactClarificationReply(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(`{"type":"clarify","question":"Which repo?"}`),
		mustResponse(`{"type":"done","summary":"done"}`),
	}}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	prompter := &scriptPrompter{
		clarifications: []clarifyReply{
			{text: "/compact"},
			{text: "/tmp/repo"},
		},
	}
	var reported []error

	err := RunApprovalTask(context.Background(), agent, "inspect", prompter, func(err error) {
		reported = append(reported, err)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hasUserMessage(agent.Messages(), "/compact") {
		t.Fatalf("compact clarification leaked into transcript: %#v", agent.Messages())
	}
	if !hasUserMessage(agent.Messages(), "/tmp/repo") {
		t.Fatalf("expected valid clarification reply in transcript: %#v", agent.Messages())
	}
	if prompter.clarifyCalls != 2 {
		t.Fatalf("expected reprompt after rejected compact clarification, got %d prompts", prompter.clarifyCalls)
	}
	if len(reported) != 1 || !strings.Contains(reported[0].Error(), "/compact is only available at the conversational prompt") {
		t.Fatalf("reported = %#v, want compact rejection", reported)
	}
}

func TestRunApprovalTaskApproveExecutesCommand(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(actJSON("echo hi")),
		mustResponse(`{"type":"done","summary":"done"}`),
	}}
	executor := &fakeExecutor{results: []clnkr.CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	prompter := &scriptPrompter{actReplies: []clarifyReply{{text: "y"}}}

	err := RunApprovalTask(context.Background(), agent, "say hi", prompter, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executor.calls != 1 {
		t.Fatalf("expected 1 execute call, got %d", executor.calls)
	}
}

func TestRunApprovalTaskNonApprovalReplyBecomesGuidance(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(actJSON("rm important.txt")),
		mustResponse(`{"type":"done","summary":"okay"}`),
	}}
	executor := &fakeExecutor{}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	prompter := &scriptPrompter{
		actReplies: []clarifyReply{{text: "list files instead"}},
	}

	err := RunApprovalTask(context.Background(), agent, "do it", prompter, nil)
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

func TestRunApprovalTaskCountsBatchCommandsTowardStepLimit(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(`{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null},{"command":"go test ./...","workdir":"subdir"}]}}`),
		mustResponse(`{"type":"done","summary":"step limit summary"}`),
	}}
	executor := &fakeExecutor{results: []clnkr.CommandResult{
		{Stdout: "/tmp\n", ExitCode: 0},
		{Stdout: "ok\n", ExitCode: 0},
	}}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	agent.MaxSteps = 2
	prompter := &scriptPrompter{actReplies: []clarifyReply{{text: "y"}}}

	err := RunApprovalTask(context.Background(), agent, "do it", prompter, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executor.calls != 2 {
		t.Fatalf("expected 2 command executions, got %d", executor.calls)
	}
	msgs := agent.Messages()
	found := false
	for _, msg := range msgs {
		if msg.Role == "user" && strings.HasPrefix(msg.Content, "Step limit reached.") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected step-limit prompt in transcript, got %#v", msgs)
	}
}

func TestRunApprovalTaskClarifyTurnAppendsReply(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(`{"type":"clarify","question":"Which repo?"}`),
		mustResponse(`{"type":"done","summary":"okay"}`),
	}}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	prompter := &scriptPrompter{
		clarifications: []clarifyReply{{text: "/tmp/repo"}},
	}

	err := RunApprovalTask(context.Background(), agent, "inspect", prompter, nil)
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
		mustResponse(actJSON("rm important.txt")),
	}}
	executor := &fakeExecutor{}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	prompter := &scriptPrompter{
		actReplies: []clarifyReply{
			{text: ""},
			{err: errApprovalPending},
		},
	}

	err := RunApprovalTask(context.Background(), agent, "do it", prompter, nil)
	if !errors.Is(err, errApprovalPending) {
		t.Fatalf("got %v, want errApprovalPending", err)
	}
	if executor.calls != 0 {
		t.Fatalf("empty act reply should not execute the command")
	}
}

func TestFormatActProposal(t *testing.T) {
	got := FormatActProposal([]clnkr.BashAction{
		{Command: "pwd"},
		{Command: "go test ./...", Workdir: "subdir"},
	})

	want := "1. pwd\n2. go test ./... in subdir"
	if got != want {
		t.Fatalf("FormatActProposal() = %q, want %q", got, want)
	}
}
