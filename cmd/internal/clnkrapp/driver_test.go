package clnkrapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/clnkr-ai/clnkr"
)

func TestDriverApprovalRequestReplyExecutesCommand(t *testing.T) {
	executor := &fakeExecutor{results: []clnkr.CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
	agent := clnkr.NewAgent(&fakeModel{responses: []clnkr.Response{
		mustResponse(actJSON("echo hi")),
		mustResponse(`{"type":"done","summary":"done"}`),
	}}, executor, "/tmp")
	driver := NewDriver(agent, nil)
	errCh := promptAsync(driver, "say hi")

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
	if event := nextDriverEvent(t, driver); event != (EventDone{Summary: "done"}) {
		t.Fatalf("event = %#v, want done", event)
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
	agent := clnkr.NewAgent(&fakeModel{responses: []clnkr.Response{
		mustResponse(`{"type":"clarify","question":"Which repo?"}`),
		mustResponse(`{"type":"done","summary":"done"}`),
	}}, &fakeExecutor{}, "/tmp")
	driver := NewDriver(agent, nil)
	errCh := promptAsync(driver, "inspect")

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
	driver := NewDriver(agent, func() clnkr.Compactor {
		return compactor
	})

	if err := driver.Prompt(
		context.Background(),
		"/compact",
		PromptModeApproval,
	); err != nil {
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
	if hasUserMessage(agent.Messages(), "/compact") {
		t.Fatalf("compact command leaked into transcript: %#v", agent.Messages())
	}
	if got := driver.Pending(); got != PendingNone {
		t.Fatalf("Pending() = %q, want %q", got, PendingNone)
	}
}

func TestDriverCompactLookalikesReachModelAsOrdinaryPrompts(t *testing.T) {
	tests := []string{
		"/compactfoo",
		"/compact/anything",
		"/compaction",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			model := &fakeModel{responses: []clnkr.Response{
				mustResponse(`{"type":"done","summary":"done"}`),
			}}
			agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
			factoryCalls := 0
			driver := NewDriver(agent, func() clnkr.Compactor {
				factoryCalls++
				return &fakeCompactor{summary: "should not compact"}
			})

			if err := driver.Prompt(context.Background(), input, PromptModeApproval); err != nil {
				t.Fatalf("Prompt: %v", err)
			}
			if event := nextDriverEvent(t, driver); event != (EventDone{Summary: "done"}) {
				t.Fatalf("event = %#v, want done", event)
			}
			if factoryCalls != 0 {
				t.Fatalf("compactor factory calls = %d, want 0", factoryCalls)
			}
			if len(model.queries) != 1 || !hasUserMessage(model.queries[0], input) {
				t.Fatalf("model queries = %#v, want user prompt %q", model.queries, input)
			}
		})
	}
}

func TestDriverCompactDispatch(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	compactor := &fakeCompactor{summary: "Older work summarized."}
	driver := NewDriver(agent, func() clnkr.Compactor {
		return compactor
	})

	if err := driver.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	event := nextDriverEvent(t, driver)
	compacted, ok := event.(EventCompacted)
	if !ok {
		t.Fatalf("event = %T, want EventCompacted", event)
	}
	if compacted.Stats.CompactedMessages != 2 || compacted.Stats.KeptMessages != 4 {
		t.Fatalf("stats = %#v, want 2 compacted and 4 kept", compacted.Stats)
	}
	if got := driver.Pending(); got != PendingNone {
		t.Fatalf("Pending() = %q, want %q", got, PendingNone)
	}
}

func TestDriverContextLengthBackstopCompactsAndRetries(t *testing.T) {
	contextErr := fmt.Errorf("%w: provider context too long", clnkr.ErrContextLengthExceeded)
	model := &fakeModel{
		errs: []error{contextErr, nil},
		responses: []clnkr.Response{
			mustResponse(`{"type":"done","summary":"done"}`),
		},
	}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	compactor := &fakeCompactor{summary: "Older work summarized."}
	driver := NewDriver(agent, func() clnkr.Compactor {
		return compactor
	})

	if err := driver.Prompt(context.Background(), "continue", PromptModeFullSend); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	events := drainDriverEvents(t, driver, 2)
	if _, ok := events[0].(EventCompacted); !ok {
		t.Fatalf("first event = %T, want EventCompacted", events[0])
	}
	if done, ok := events[1].(EventDone); !ok || done.Summary != "done" {
		t.Fatalf("second event = %#v, want done", events[1])
	}
	if compactor.calls != 1 {
		t.Fatalf("compactor calls = %d, want 1", compactor.calls)
	}
	if model.calls != 2 {
		t.Fatalf("model calls = %d, want 2", model.calls)
	}
	if countUserMessages(agent.Messages(), "continue") != 1 {
		t.Fatalf("prompt was appended more than once: %#v", agent.Messages())
	}
	if len(agent.Messages()) == 0 ||
		!strings.HasPrefix(agent.Messages()[0].Content, "[compact]\n") {
		t.Fatalf("messages were not compacted: %#v", agent.Messages())
	}
}

func TestDriverContextLengthBackstopCompactionFailureReportsBothErrors(t *testing.T) {
	contextErr := fmt.Errorf("%w: provider context too long", clnkr.ErrContextLengthExceeded)
	compactionErr := errors.New("compact boom")
	agent := clnkr.NewAgent(&fakeModel{errs: []error{contextErr}}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	driver := NewDriver(agent, func() clnkr.Compactor {
		return &fakeCompactor{err: compactionErr}
	})

	err := driver.Prompt(context.Background(), "continue", PromptModeFullSend)
	if err == nil {
		t.Fatal("Prompt error = nil, want failure")
	}
	if !errors.Is(err, clnkr.ErrContextLengthExceeded) {
		t.Fatalf("error = %v, want context failure", err)
	}
	if !errors.Is(err, compactionErr) {
		t.Fatalf("error = %v, want compaction failure", err)
	}
	event := nextDriverEvent(t, driver)
	if eventErr, ok := event.(EventError); !ok || !errors.Is(eventErr.Err, compactionErr) {
		t.Fatalf("event = %#v, want compaction EventError", event)
	}
}

func TestDriverContextLengthBackstopRetryFailureReportsBothErrors(t *testing.T) {
	contextErr := fmt.Errorf("%w: provider context too long", clnkr.ErrContextLengthExceeded)
	retryErr := errors.New("network down")
	model := &fakeModel{errs: []error{contextErr, retryErr}}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	driver := NewDriver(agent, func() clnkr.Compactor {
		return &fakeCompactor{summary: "Older work summarized."}
	})

	err := driver.Prompt(context.Background(), "continue", PromptModeFullSend)
	if err == nil {
		t.Fatal("Prompt error = nil, want failure")
	}
	if !errors.Is(err, clnkr.ErrContextLengthExceeded) {
		t.Fatalf("error = %v, want original context failure", err)
	}
	if !errors.Is(err, retryErr) {
		t.Fatalf("error = %v, want retry failure", err)
	}
	events := drainDriverEvents(t, driver, 2)
	if _, ok := events[0].(EventCompacted); !ok {
		t.Fatalf("first event = %T, want EventCompacted", events[0])
	}
	if eventErr, ok := events[1].(EventError); !ok || !errors.Is(eventErr.Err, retryErr) {
		t.Fatalf("second event = %#v, want retry EventError", events[1])
	}
}

func TestDriverContextLengthBackstopDoesNotRetryTwice(t *testing.T) {
	contextErr := fmt.Errorf("%w: provider context too long", clnkr.ErrContextLengthExceeded)
	model := &fakeModel{errs: []error{contextErr, contextErr}}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	compactor := &fakeCompactor{summary: "Older work summarized."}
	driver := NewDriver(agent, func() clnkr.Compactor {
		return compactor
	})

	err := driver.Prompt(context.Background(), "continue", PromptModeFullSend)
	if err == nil {
		t.Fatal("Prompt error = nil, want failure")
	}
	if compactor.calls != 1 {
		t.Fatalf("compactor calls = %d, want 1", compactor.calls)
	}
	if model.calls != 2 {
		t.Fatalf("model calls = %d, want 2", model.calls)
	}
}

func TestDriverDelegateTextReachesModelAsOrdinaryPrompt(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{responses: []clnkr.Response{
		mustResponse(`{"type":"done","summary":"done"}`),
	}}, &fakeExecutor{}, "/tmp/repo")
	driver := NewDriver(agent, nil)

	if err := driver.Prompt(
		context.Background(),
		"/delegate inspect README",
		PromptModeApproval,
	); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if event := nextDriverEvent(t, driver); event != (EventDone{Summary: "done"}) {
		t.Fatalf("event = %#v, want done", event)
	}
	if !hasUserMessage(agent.Messages(), "/delegate inspect README") {
		t.Fatalf("delegate prompt did not reach transcript: %#v", agent.Messages())
	}
}

func drainDriverEvents(t *testing.T, driver *Driver, count int) []DriverEvent {
	t.Helper()
	events := make([]DriverEvent, 0, count)
	for len(events) < count {
		events = append(events, nextDriverEvent(t, driver))
	}
	return events
}

func promptAsync(driver *Driver, prompt string) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- driver.Prompt(context.Background(), prompt, PromptModeApproval)
	}()
	return errCh
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
