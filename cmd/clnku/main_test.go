package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	clnkr "github.com/clnkr-ai/clnkr"
)

func actJSON(command string) string {
	return fmt.Sprintf(`{"type":"act","bash":{"commands":[{"command":%q,"workdir":null}]}}`, command)
}

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

type fakeCompactor struct {
	summary  string
	err      error
	calls    int
	messages []clnkr.Message
}

func (c *fakeCompactor) Summarize(_ context.Context, messages []clnkr.Message) (string, error) {
	c.calls++
	c.messages = append([]clnkr.Message{}, messages...)
	if c.err != nil {
		return "", c.err
	}
	return c.summary, nil
}

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

func TestDefaultAnthropicModel(t *testing.T) {
	if defaultAnthropicModel != "claude-sonnet-4-6" {
		t.Fatalf("defaultAnthropicModel = %q, want %q", defaultAnthropicModel, "claude-sonnet-4-6")
	}
}

func TestUsageTextIncludesDefaultAnthropicModel(t *testing.T) {
	if !strings.Contains(usageText(), defaultAnthropicModel) {
		t.Fatalf("usageText() does not mention defaultAnthropicModel %q", defaultAnthropicModel)
	}
}

func TestResolveModelValue(t *testing.T) {
	tests := []struct {
		name     string
		flag     string
		env      string
		expected string
	}{
		{name: "flag wins", flag: "flag-model", env: "env-model", expected: "flag-model"},
		{name: "env used when flag empty", env: "env-model", expected: "env-model"},
		{name: "default used when flag and env empty", expected: defaultAnthropicModel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveModelValue(tt.flag, tt.env); got != tt.expected {
				t.Fatalf("resolveModelValue(%q, %q) = %q, want %q", tt.flag, tt.env, got, tt.expected)
			}
		})
	}
}

func TestParseCompactCommand(t *testing.T) {
	tests := []struct {
		name             string
		input            string
		wantInstructions string
		wantOK           bool
	}{
		{name: "bare command", input: "/compact", wantOK: true},
		{name: "trimmed command", input: "  /compact  ", wantOK: true},
		{name: "with instructions", input: "/compact focus on tests", wantInstructions: "focus on tests", wantOK: true},
		{name: "with tab separator", input: "/compact\tfocus on tests", wantInstructions: "focus on tests", wantOK: true},
		{name: "not compact", input: "compact", wantOK: false},
		{name: "prefixed word", input: "/compaction", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotInstructions, gotOK := parseCompactCommand(tt.input)
			if gotInstructions != tt.wantInstructions || gotOK != tt.wantOK {
				t.Fatalf("parseCompactCommand(%q) = (%q, %v), want (%q, %v)", tt.input, gotInstructions, gotOK, tt.wantInstructions, tt.wantOK)
			}
		})
	}
}

func TestCompactCommandDoesNotAppendLiteralUserMessage(t *testing.T) {
	model := &fakeModel{}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}

	compactor := &fakeCompactor{summary: "Older work summarized."}
	var gotInstructions string
	factory := func(instructions string) clnkr.Compactor {
		gotInstructions = instructions
		return compactor
	}

	var stderr bytes.Buffer
	if err := handleConversationalInput(context.Background(), &stderr, agent, "/compact focus on failing tests", true, nil, factory); err != nil {
		t.Fatalf("handleConversationalInput: %v", err)
	}

	if compactor.calls != 1 {
		t.Fatalf("expected compactor to be called once, got %d", compactor.calls)
	}
	if gotInstructions != "focus on failing tests" {
		t.Fatalf("factory instructions = %q, want %q", gotInstructions, "focus on failing tests")
	}
	if len(compactor.messages) != 2 {
		t.Fatalf("compactor saw %d messages, want 2", len(compactor.messages))
	}

	msgs := agent.Messages()
	for _, msg := range msgs {
		if msg.Role == "user" && msg.Content == "/compact focus on failing tests" {
			t.Fatalf("literal compact command was appended: %#v", msgs)
		}
	}
	if len(msgs) == 0 || !strings.HasPrefix(msgs[0].Content, "[compact]\n") {
		t.Fatalf("expected compact block at start, got %#v", msgs)
	}
	if !strings.Contains(msgs[0].Content, `"instructions":"focus on failing tests"`) {
		t.Fatalf("compact block missing instructions: %q", msgs[0].Content)
	}
	if got := stderr.String(); !strings.Contains(got, "[Session compacted: 2 messages summarized, 4 kept]") {
		t.Fatalf("stderr = %q, want compact summary", got)
	}
}

func TestCompactCommandFailureLeavesMessagesUnchanged(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	before := agent.Messages()

	compactor := &fakeCompactor{err: errors.New("boom")}
	factory := func(string) clnkr.Compactor { return compactor }

	var stderr bytes.Buffer
	err := handleConversationalInput(context.Background(), &stderr, agent, "/compact", true, nil, factory)
	if err == nil || !strings.Contains(err.Error(), "compact transcript: summarize prefix: boom") {
		t.Fatalf("got %v, want summarize prefix error", err)
	}
	if !reflect.DeepEqual(agent.Messages(), before) {
		t.Fatalf("messages changed on compaction failure: got %#v want %#v", agent.Messages(), before)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty on failure", stderr.String())
	}
}

func TestCompactCommandKeepsSessionUsable(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"done","summary":"done"}`}},
	}}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}

	compactor := &fakeCompactor{summary: "Older work summarized."}
	factory := func(string) clnkr.Compactor { return compactor }

	var stderr bytes.Buffer
	if err := handleConversationalInput(context.Background(), &stderr, agent, "/compact", true, nil, factory); err != nil {
		t.Fatalf("compact command: %v", err)
	}
	if err := handleConversationalInput(context.Background(), &stderr, agent, "next task", true, nil, factory); err != nil {
		t.Fatalf("follow-up task: %v", err)
	}

	msgs := agent.Messages()
	if len(msgs) < 3 {
		t.Fatalf("expected compacted transcript plus follow-up, got %#v", msgs)
	}
	if !strings.HasPrefix(msgs[0].Content, "[compact]\n") {
		t.Fatalf("expected compact block to remain at start, got %#v", msgs)
	}
	if msgs[len(msgs)-1].Content != `{"type":"done","summary":"done"}` {
		t.Fatalf("expected follow-up completion at end, got %#v", msgs)
	}
	if !hasUserMessage(msgs, "next task") {
		t.Fatalf("follow-up task not appended after compaction: %#v", msgs)
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want 1", model.calls)
	}
}

func TestRunSingleTaskRejectsCompactCommand(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")

	err := runSingleTask(context.Background(), agent, "/compact focus on tests", true, nil)
	if err == nil || !strings.Contains(err.Error(), "/compact is only available at the conversational prompt") {
		t.Fatalf("got %v, want conversational prompt error", err)
	}
	if msgs := agent.Messages(); len(msgs) != 0 {
		t.Fatalf("single-task compact should not touch transcript: %#v", msgs)
	}
}

func TestRunSingleTaskRejectsCompactCommandBeforeApprovalCheck(t *testing.T) {
	called := false
	err := prepareSingleTask("/compact focus on tests", false, func() error {
		called = true
		return errors.New("approval mode requires interactive stdin")
	})
	if err == nil || !strings.Contains(err.Error(), "/compact is only available at the conversational prompt") {
		t.Fatalf("got %v, want conversational prompt error", err)
	}
	if called {
		t.Fatal("approval preflight should not run for /compact")
	}
}

func TestRunApprovalTaskRejectsCompactApprovalReply(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		{Message: clnkr.Message{Role: "assistant", Content: actJSON("echo hi")}},
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"done","summary":"done"}`}},
	}}
	executor := &fakeExecutor{results: []clnkr.CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	prompter := &scriptPrompter{
		actReplies: []clarifyReply{
			{text: "/compact focus on tests"},
			{text: "y"},
		},
	}

	stderr := captureStderr(t, func() {
		err := runApprovalTask(context.Background(), agent, "say hi", prompter)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if executor.calls != 1 {
		t.Fatalf("expected command execution after valid retry, got %d", executor.calls)
	}
	if hasUserMessage(agent.Messages(), "/compact focus on tests") {
		t.Fatalf("compact reply leaked into transcript: %#v", agent.Messages())
	}
	if prompter.actReplyCalls != 2 {
		t.Fatalf("expected reprompt after rejected compact reply, got %d prompts", prompter.actReplyCalls)
	}
	if !strings.Contains(stderr, "/compact is only available at the conversational prompt") {
		t.Fatalf("stderr = %q, want compact rejection", stderr)
	}
}

func TestRunApprovalTaskRejectsCompactClarificationReply(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"clarify","question":"Which repo?"}`}},
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"done","summary":"done"}`}},
	}}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	prompter := &scriptPrompter{
		clarifications: []clarifyReply{
			{text: "/compact"},
			{text: "/tmp/repo"},
		},
	}

	stderr := captureStderr(t, func() {
		err := runApprovalTask(context.Background(), agent, "inspect", prompter)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if hasUserMessage(agent.Messages(), "/compact") {
		t.Fatalf("compact clarification leaked into transcript: %#v", agent.Messages())
	}
	if !hasUserMessage(agent.Messages(), "/tmp/repo") {
		t.Fatalf("expected valid clarification reply in transcript: %#v", agent.Messages())
	}
	if prompter.clarifyCalls != 2 {
		t.Fatalf("expected reprompt after rejected compact clarification, got %d prompts", prompter.clarifyCalls)
	}
	if !strings.Contains(stderr, "/compact is only available at the conversational prompt") {
		t.Fatalf("stderr = %q, want compact rejection", stderr)
	}
}

func TestRunTaskFullSendUsesRun(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		{Message: clnkr.Message{Role: "assistant", Content: actJSON("echo hi")}},
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
		{Message: clnkr.Message{Role: "assistant", Content: actJSON("echo hi")}},
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
		{Message: clnkr.Message{Role: "assistant", Content: actJSON("rm important.txt")}},
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

func TestRunApprovalTaskCountsBatchCommandsTowardStepLimit(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null},{"command":"go test ./...","workdir":"subdir"}]}}`}},
		{Message: clnkr.Message{Role: "assistant", Content: `{"type":"done","summary":"step limit summary"}`}},
	}}
	executor := &fakeExecutor{results: []clnkr.CommandResult{
		{Stdout: "/tmp\n", ExitCode: 0},
		{Stdout: "ok\n", ExitCode: 0},
	}}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	agent.MaxSteps = 2
	prompter := &scriptPrompter{actReplies: []clarifyReply{{text: "y"}}}

	err := runApprovalTask(context.Background(), agent, "do it", prompter)
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
		{Message: clnkr.Message{Role: "assistant", Content: actJSON("rm important.txt")}},
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

func TestStdinPrompterConfirmShowsWorkdir(t *testing.T) {
	stderr := captureStderr(t, func() {
		p := &stdinPrompter{reader: newLineReader(strings.NewReader("y\n"))}
		reply, err := p.ActReply(context.Background(), formatActProposal([]clnkr.BashAction{{Command: "rm important.txt", Workdir: "subdir"}}))
		if err != nil {
			t.Fatalf("ActReply: %v", err)
		}
		if reply != "y" {
			t.Fatalf("reply = %q, want y", reply)
		}
	})

	if !strings.Contains(stderr, "rm important.txt in subdir") {
		t.Fatalf("stderr should contain workdir note, got %q", stderr)
	}
}

func TestFormatActProposal(t *testing.T) {
	got := formatActProposal([]clnkr.BashAction{
		{Command: "pwd"},
		{Command: "go test ./...", Workdir: "subdir"},
	})

	want := "1. pwd\n2. go test ./... in subdir"
	if got != want {
		t.Fatalf("formatActProposal() = %q, want %q", got, want)
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

func TestWriteEventLogIncludesFeedback(t *testing.T) {
	f, err := os.CreateTemp("", "clnku-event-log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	defer f.Close()           //nolint:errcheck

	writeEventLog(f, clnkr.EventCommandDone{
		Command:  "touch note.txt",
		ExitCode: 0,
		Feedback: clnkr.CommandFeedback{ChangedFiles: []string{"note.txt"}},
	})

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Type    string `json:"type"`
		Payload struct {
			Feedback clnkr.CommandFeedback `json:"feedback"`
		} `json:"payload"`
	}
	if err := json.NewDecoder(f).Decode(&payload); err != nil {
		t.Fatalf("decode event log: %v", err)
	}
	if payload.Type != "command_done" {
		t.Fatalf("type = %q, want command_done", payload.Type)
	}
	if len(payload.Payload.Feedback.ChangedFiles) != 1 || payload.Payload.Feedback.ChangedFiles[0] != "note.txt" {
		t.Fatalf("feedback = %#v, want note.txt", payload.Payload.Feedback)
	}
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

func compactableMessages() []clnkr.Message {
	return []clnkr.Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done"}`},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done again"}`},
		{Role: "user", Content: "third task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
	}
}

func hasUserMessage(msgs []clnkr.Message, want string) bool {
	for _, msg := range msgs {
		if msg.Role == "user" && msg.Content == want {
			return true
		}
	}
	return false
}
