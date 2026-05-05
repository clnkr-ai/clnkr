package clnkrapp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr"
)

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
			gotInstructions, gotOK := ParseCompactCommand(tt.input)
			if gotInstructions != tt.wantInstructions || gotOK != tt.wantOK {
				t.Fatalf("ParseCompactCommand(%q) = (%q, %v), want (%q, %v)", tt.input, gotInstructions, gotOK, tt.wantInstructions, tt.wantOK)
			}
		})
	}
}

func TestHandleCompactCommandDoesNotAppendLiteralUserMessage(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}

	compactor := &fakeCompactor{summary: "Older work summarized."}
	var gotInstructions string
	factory := func(instructions string) clnkr.Compactor {
		gotInstructions = instructions
		return compactor
	}

	stats, ran, err := HandleCompactCommand(context.Background(), agent, "/compact focus on failing tests", factory)
	if err != nil {
		t.Fatalf("HandleCompactCommand: %v", err)
	}
	if !ran {
		t.Fatal("HandleCompactCommand did not run compact command")
	}
	if stats.CompactedMessages != 2 || stats.KeptMessages != 4 {
		t.Fatalf("stats = %#v, want 2 compacted and 4 kept", stats)
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
}

func TestHandleCompactCommandUpdatesWorkingMemoryAfterSuccessfulCompaction(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	updater := &fakeWorkingMemoryUpdater{}
	agent.SetWorkingMemoryUpdater(updater)

	compactor := &fakeCompactor{summary: "Older work summarized."}
	factory := func(string) clnkr.Compactor { return compactor }

	_, ran, err := HandleCompactCommand(context.Background(), agent, "/compact", factory)
	if err != nil {
		t.Fatalf("HandleCompactCommand: %v", err)
	}
	if !ran {
		t.Fatal("HandleCompactCommand did not run compact command")
	}
	if updater.calls != 1 {
		t.Fatalf("working memory updates = %d, want 1", updater.calls)
	}
	if updater.reasons[0] != clnkr.WorkingMemoryUpdateReasonCompact {
		t.Fatalf("working memory update reason = %q, want compact", updater.reasons[0])
	}
}

func TestHandleCompactCommandFailureLeavesMessagesUnchanged(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	before := agent.Messages()

	compactor := &fakeCompactor{err: errors.New("boom")}
	factory := func(string) clnkr.Compactor { return compactor }

	_, _, err := HandleCompactCommand(context.Background(), agent, "/compact", factory)
	if err == nil || !strings.Contains(err.Error(), "compact transcript: summarize prefix: boom") {
		t.Fatalf("got %v, want summarize prefix error", err)
	}
	if !reflect.DeepEqual(agent.Messages(), before) {
		t.Fatalf("messages changed on compaction failure: got %#v want %#v", agent.Messages(), before)
	}
}

func TestHandleCompactCommandRejectsMissingCompactor(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")

	_, _, err := HandleCompactCommand(context.Background(), agent, "/compact", nil)
	if err == nil || !strings.Contains(err.Error(), "no compactor factory configured") {
		t.Fatalf("got %v, want missing factory error", err)
	}

	_, _, err = HandleCompactCommand(context.Background(), agent, "/compact", func(string) clnkr.Compactor { return nil })
	if err == nil || !strings.Contains(err.Error(), "no compactor configured") {
		t.Fatalf("got %v, want missing compactor error", err)
	}
}

func TestRejectCompactCommandOutsideConversation(t *testing.T) {
	err := RejectCompactCommand("/compact focus on tests")
	if err == nil || !strings.Contains(err.Error(), "/compact is only available at the conversational prompt") {
		t.Fatalf("got %v, want compact rejection", err)
	}
}
