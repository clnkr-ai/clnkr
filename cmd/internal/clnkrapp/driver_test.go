package clnkrapp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/clnkr-ai/clnkr"
)

func TestDriverApprovalRequestReplyExecutesCommand(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(actJSON("echo hi")),
		mustResponse(`{"type":"done","summary":"done"}`),
	}}
	executor := &fakeExecutor{results: []clnkr.CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	driver := NewDriver(agent, nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- driver.Prompt(context.Background(), "say hi", PromptModeApproval)
	}()

	event := nextDriverEvent(t, driver)
	request, ok := event.(EventApprovalRequest)
	if !ok {
		t.Fatalf("event = %T, want EventApprovalRequest", event)
	}
	if request.Prompt != "1. echo hi" {
		t.Fatalf("prompt = %q, want approval prompt", request.Prompt)
	}
	if len(request.Commands) != 1 || request.Commands[0].Command != "echo hi" {
		t.Fatalf("commands = %#v, want echo hi", request.Commands)
	}
	if got := driver.Pending(); got != PendingApproval {
		t.Fatalf("Pending() = %q, want %q", got, PendingApproval)
	}

	if err := driver.Reply(context.Background(), "y"); err != nil {
		t.Fatalf("Reply: %v", err)
	}

	done, ok := nextDriverEvent(t, driver).(EventDone)
	if !ok {
		t.Fatalf("next event = %T, want EventDone", done)
	}
	if done.Summary != "done" {
		t.Fatalf("summary = %q, want done", done.Summary)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if executor.calls != 1 {
		t.Fatalf("executor calls = %d, want 1", executor.calls)
	}
	if got := driver.Pending(); got != PendingNone {
		t.Fatalf("Pending() = %q, want %q", got, PendingNone)
	}
}

func TestDriverClarificationReplyContinuesRun(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(`{"type":"clarify","question":"Which repo?"}`),
		mustResponse(`{"type":"done","summary":"done"}`),
	}}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	driver := NewDriver(agent, nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- driver.Prompt(context.Background(), "inspect", PromptModeApproval)
	}()

	event := nextDriverEvent(t, driver)
	request, ok := event.(EventClarificationRequest)
	if !ok {
		t.Fatalf("event = %T, want EventClarificationRequest", event)
	}
	if request.Question != "Which repo?" {
		t.Fatalf("question = %q, want model question", request.Question)
	}
	if got := driver.Pending(); got != PendingClarification {
		t.Fatalf("Pending() = %q, want %q", got, PendingClarification)
	}

	if err := driver.Reply(context.Background(), "/tmp/repo"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if event := nextDriverEvent(t, driver); event != (EventDone{Summary: "done"}) {
		t.Fatalf("event = %#v, want done", event)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !hasUserMessage(agent.Messages(), "/tmp/repo") {
		t.Fatalf("clarification reply not appended: %#v", agent.Messages())
	}
	if got := driver.Pending(); got != PendingNone {
		t.Fatalf("Pending() = %q, want %q", got, PendingNone)
	}
}

func TestDriverTopLevelCompactDispatch(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	compactor := &fakeCompactor{summary: "Older work summarized."}
	driver := NewDriver(agent, func(instructions string) clnkr.Compactor {
		if instructions != "focus on tests" {
			t.Fatalf("instructions = %q, want compact instructions", instructions)
		}
		return compactor
	})

	if err := driver.Prompt(context.Background(), "/compact focus on tests", PromptModeApproval); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	event := nextDriverEvent(t, driver)
	compacted, ok := event.(EventCompacted)
	if !ok {
		t.Fatalf("event = %T, want EventCompacted", event)
	}
	if compacted.Stats.CompactedMessages != 2 || compacted.Stats.KeptMessages != 4 {
		t.Fatalf("stats = %#v, want 2 compacted and 4 kept", compacted.Stats)
	}
	if hasUserMessage(agent.Messages(), "/compact focus on tests") {
		t.Fatalf("compact command leaked into transcript: %#v", agent.Messages())
	}
	if got := driver.Pending(); got != PendingNone {
		t.Fatalf("Pending() = %q, want %q", got, PendingNone)
	}
}

func TestDriverDelegateCommandAppendsChildSummaryOnly(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/repo")
	runner := &fakeChildProbeRunner{result: ChildProbeResult{
		ChildID: "child-001",
		Status:  ChildProbeStatusDone,
		Summary: "README says run make test.",
		Artifacts: ChildProbeArtifacts{
			EventLog:   "/tmp/delegates/child-001/event-log.jsonl",
			Trajectory: "/tmp/delegates/child-001/trajectory.json",
			Result:     "/tmp/delegates/child-001/result.json",
		},
	}}
	driver := NewDriver(agent, nil)
	driver.SetDelegateRunner(runner, DelegateConfig{
		Enabled:     true,
		MaxDepth:    1,
		MaxChildren: 3,
		MaxCommands: 10,
		Timeout:     time.Minute,
		ArtifactDir: "/tmp/delegates",
	})

	if err := driver.Prompt(context.Background(), "/delegate inspect README", PromptModeApproval); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	start, ok := nextDriverEvent(t, driver).(EventChildProbeStart)
	if !ok {
		t.Fatalf("first event = %T, want EventChildProbeStart", start)
	}
	if start.Request.Task != "inspect README" || start.Request.ParentCwd != "/tmp/repo" {
		t.Fatalf("request = %#v, want delegated task and cwd", start.Request)
	}
	done, ok := nextDriverEvent(t, driver).(EventChildProbeDone)
	if !ok {
		t.Fatalf("second event = %T, want EventChildProbeDone", done)
	}
	if done.Result.Summary != "README says run make test." {
		t.Fatalf("summary = %q, want child summary", done.Result.Summary)
	}
	msgs := agent.Messages()
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Fatalf("messages = %#v, want one host user block", msgs)
	}
	if !strings.Contains(msgs[0].Content, `"type":"child_probe_result"`) {
		t.Fatalf("message = %s, want child probe result block", msgs[0].Content)
	}
	if strings.Contains(msgs[0].Content, "inspect README") {
		t.Fatalf("parent transcript should contain result, not original delegate command: %s", msgs[0].Content)
	}
}

func TestDriverDelegateRequiresEnabledRunner(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/repo")
	driver := NewDriver(agent, nil)

	err := driver.Prompt(context.Background(), "/delegate inspect README", PromptModeApproval)
	if err == nil || !strings.Contains(err.Error(), "delegation is not enabled") {
		t.Fatalf("Prompt error = %v, want disabled delegation error", err)
	}
	denied, ok := nextDriverEvent(t, driver).(EventChildProbeDenied)
	if !ok {
		t.Fatalf("event = %T, want EventChildProbeDenied", denied)
	}
	if denied.Reason != "delegation is not enabled" {
		t.Fatalf("reason = %q, want disabled reason", denied.Reason)
	}
	if len(agent.Messages()) != 0 {
		t.Fatalf("messages = %#v, want delegate command not appended on failure", agent.Messages())
	}
}

func TestDriverDelegateEmitsDeniedForInvalidRequest(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/repo")
	driver := NewDriver(agent, nil)
	driver.SetDelegateRunner(&fakeChildProbeRunner{}, DelegateConfig{
		Enabled:     true,
		MaxDepth:    1,
		MaxChildren: 3,
		MaxCommands: 10,
		Timeout:     time.Minute,
	})

	err := driver.Prompt(context.Background(), "/delegate", PromptModeApproval)
	if err == nil || !strings.Contains(err.Error(), "task is required") {
		t.Fatalf("Prompt error = %v, want task required error", err)
	}
	denied, ok := nextDriverEvent(t, driver).(EventChildProbeDenied)
	if !ok {
		t.Fatalf("event = %T, want EventChildProbeDenied", denied)
	}
	if !strings.Contains(denied.Reason, "task is required") {
		t.Fatalf("reason = %q, want task required", denied.Reason)
	}
	if len(agent.Messages()) != 0 {
		t.Fatalf("messages = %#v, want no transcript append", agent.Messages())
	}
}

type fakeChildProbeRunner struct {
	result ChildProbeResult
	err    error
	calls  int
	got    ChildProbeRequest
}

func (r *fakeChildProbeRunner) RunChildProbe(_ context.Context, req ChildProbeRequest) (ChildProbeResult, error) {
	r.calls++
	r.got = req
	if r.err != nil {
		return ChildProbeResult{}, r.err
	}
	result := r.result
	if result.ChildID == "" {
		result.ChildID = req.ChildID
	}
	return result, nil
}

func nextDriverEvent(t *testing.T, driver *Driver) DriverEvent {
	t.Helper()

	select {
	case event := <-driver.Events():
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for driver event")
		return nil
	}
}
