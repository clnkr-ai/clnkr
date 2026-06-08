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
		input string
		ok    bool
	}{
		{input: "/compact", ok: true},
		{input: "  /compact  ", ok: true},
		{input: "compact"},
		{input: "/compaction"},
		{input: "/compact focus on tests"},
		{input: "/compact	focus on tests"},
		{input: "/compact\tfocus on tests"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseCompactCommand(tt.input)
			if got != tt.ok {
				t.Fatalf("ParseCompactCommand(%q) = %v, want %v", tt.input, got, tt.ok)
			}
		})
	}
}

func TestMalformedCompactCommand(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "exact", input: "/compact", want: ""},
		{name: "surrounding whitespace", input: "  /compact  ", want: ""},
		{
			name:  "trailing space",
			input: "/compact focus",
			want:  "/compact does not accept arguments",
		},
		{
			name:  "trailing tab",
			input: "/compact\tfocus",
			want:  "/compact does not accept arguments",
		},
		{
			name:  "trailing unicode space",
			input: "/compact\u00a0focus",
			want:  "/compact does not accept arguments",
		},
		{name: "no space suffix", input: "/compactfoo", want: "/compact does not accept arguments"},
		{
			name:  "slash suffix",
			input: "/compact/anything",
			want:  "/compact does not accept arguments",
		},
		{name: "compaction word", input: "/compaction", want: "/compact does not accept arguments"},
		{name: "compact no prefix", input: "compact", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := MalformedCompactCommand(tt.input)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("MalformedCompactCommand(%q) = %v, want nil", tt.input, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("MalformedCompactCommand(%q) = %v, want %q", tt.input, err, tt.want)
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
	stats, ran, err := HandleCompactCommand(
		context.Background(),
		agent,
		"/compact",
		func() clnkr.Compactor {
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
	if compactor.calls != 1 || len(compactor.messages) != 2 {
		t.Fatalf(
			"compactor got %d calls and %d messages, want 1 call and 2 messages",
			compactor.calls,
			len(compactor.messages),
		)
	}

	msgs := agent.Messages()
	if hasUserMessage(msgs, "/compact") {
		t.Fatalf("literal compact command was appended: %#v", msgs)
	}
	if len(msgs) == 0 || !strings.HasPrefix(msgs[0].Content, "[compact]\n") {
		t.Fatalf("expected compact block at start, got %#v", msgs)
	}
}

func TestCompactTranscriptRunsCompaction(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}

	compactor := &fakeCompactor{summary: "Older work summarized."}
	stats, err := CompactTranscript(
		context.Background(),
		agent,
		func() clnkr.Compactor {
			return compactor
		},
	)
	if err != nil {
		t.Fatalf("CompactTranscript: %v", err)
	}
	if stats.CompactedMessages != 2 || stats.KeptMessages != 4 {
		t.Fatalf("stats = %#v, want 2 compacted and 4 kept", stats)
	}
	if len(agent.Messages()) == 0 || !strings.HasPrefix(agent.Messages()[0].Content, "[compact]\n") {
		t.Fatalf("expected compact block at start, got %#v", agent.Messages())
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
		func() clnkr.Compactor {
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
		factory func() clnkr.Compactor
		want    string
	}{
		{name: "factory", want: "no compactor factory configured"},
		{
			name:    "compactor",
			factory: func() clnkr.Compactor { return nil },
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
	tests := []struct {
		input string
		want  string
	}{
		{input: "/compact", want: "/compact is only available at the conversational prompt"},
		{input: "/compact focus on tests", want: "/compact does not accept arguments"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			err := RejectCompactCommand(tt.input)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
		})
	}
}
