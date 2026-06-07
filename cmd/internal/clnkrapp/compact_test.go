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
		input        string
		instructions string
		ok           bool
	}{
		{input: "/compact", ok: true},
		{input: "  /compact  ", ok: true},
		{input: "/compact focus on tests", instructions: "focus on tests", ok: true},
		{input: "/compact	focus on tests", instructions: "focus on tests", ok: true},
		{input: "compact"},
		{input: "/compaction"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			gotInstructions, gotOK := ParseCompactCommand(tt.input)
			if gotInstructions != tt.instructions || gotOK != tt.ok {
				t.Fatalf(
					"ParseCompactCommand(%q) = (%q, %v), want (%q, %v)",
					tt.input,
					gotInstructions,
					gotOK,
					tt.instructions,
					tt.ok,
				)
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
	stats, ran, err := HandleCompactCommand(
		context.Background(),
		agent,
		"/compact focus on failing tests",
		func(instructions string) clnkr.Compactor {
			gotInstructions = instructions
			return compactor
		},
	)
	if err != nil {
		t.Fatalf("HandleCompactCommand: %v", err)
	}
	if !ran {
		t.Fatal("HandleCompactCommand did not run compact command")
	}
	if got := [2]int{stats.CompactedMessages, stats.KeptMessages}; got != [2]int{2, 4} {
		t.Fatalf("stats = %#v, want 2 compacted and 4 kept", stats)
	}
	if gotInstructions != "focus on failing tests" {
		t.Fatalf("factory instructions = %q, want focus on failing tests", gotInstructions)
	}
	if compactor.calls != 1 || len(compactor.messages) != 2 {
		t.Fatalf(
			"compactor got %d calls and %d messages, want 1 call and 2 messages",
			compactor.calls,
			len(compactor.messages),
		)
	}

	msgs := agent.Messages()
	if hasUserMessage(msgs, "/compact focus on failing tests") {
		t.Fatalf("literal compact command was appended: %#v", msgs)
	}
	if len(msgs) == 0 || !strings.HasPrefix(msgs[0].Content, "[compact]\n") {
		t.Fatalf("expected compact block at start, got %#v", msgs)
	}
}

func TestCompactTranscriptUsesStructuredInstructions(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}

	compactor := &fakeCompactor{summary: "Older work summarized."}
	stats, err := compactTranscript(
		context.Background(),
		agent,
		"  focus on failing tests  ",
		func(instructions string) clnkr.Compactor {
			if instructions != "  focus on failing tests  " {
				t.Fatalf("instructions = %q, want exact structured instructions", instructions)
			}
			return compactor
		},
	)
	if err != nil {
		t.Fatalf("compactTranscript: %v", err)
	}
	if stats.CompactedMessages != 2 || stats.KeptMessages != 4 {
		t.Fatalf("stats = %#v, want 2 compacted and 4 kept", stats)
	}
	if hasUserMessage(agent.Messages(), "/compact   focus on failing tests  ") {
		t.Fatalf("structured compact instructions leaked into transcript: %#v", agent.Messages())
	}
}

func TestHandleCompactCommandFailureLeavesMessagesUnchanged(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	before := agent.Messages()

	_, _, err := HandleCompactCommand(
		context.Background(),
		agent,
		"/compact",
		func(string) clnkr.Compactor {
			return &fakeCompactor{err: errors.New("boom")}
		},
	)
	if err == nil || !strings.Contains(err.Error(), "compact transcript: summarize prefix: boom") {
		t.Fatalf("got %v, want summarize prefix error", err)
	}
	if !reflect.DeepEqual(agent.Messages(), before) {
		t.Fatalf(
			"messages changed on compaction failure: got %#v want %#v",
			agent.Messages(),
			before,
		)
	}
}

func TestHandleCompactCommandRejectsMissingCompactor(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	tests := []struct {
		name    string
		factory func(string) clnkr.Compactor
		want    string
	}{
		{name: "factory", want: "no compactor factory configured"},
		{
			name:    "compactor",
			factory: func(string) clnkr.Compactor { return nil },
			want:    "no compactor configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := HandleCompactCommand(context.Background(), agent, "/compact", tt.factory)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRejectCompactCommandOutsideConversation(t *testing.T) {
	err := RejectCompactCommand("/compact focus on tests")
	if err == nil ||
		!strings.Contains(err.Error(), "/compact is only available at the conversational prompt") {
		t.Fatalf("got %v, want compact rejection", err)
	}
}
