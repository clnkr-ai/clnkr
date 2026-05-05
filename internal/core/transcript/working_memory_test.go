package transcript

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatWorkingMemoryMessageRoundTripsAsHostBlock(t *testing.T) {
	body := json.RawMessage(`{"source":"clnkr","kind":"working_memory","version":1,"current_state":["green"]}`)
	msg := Message{Role: "user", Content: FormatWorkingMemoryMessage(body)}

	if !IsWorkingMemoryMessage(msg) {
		t.Fatalf("working memory block not recognized: %q", msg.Content)
	}
	if strings.Contains(msg.Content, `\u003c`) {
		t.Fatalf("working memory JSON should remain readable: %q", msg.Content)
	}
	if messageKind(msg) != "working_memory" {
		t.Fatalf("messageKind = %q, want working_memory", messageKind(msg))
	}
}

func TestWorkingMemoryMessageRejectsForeignEnvelope(t *testing.T) {
	msg := Message{Role: "user", Content: FormatWorkingMemoryMessage(json.RawMessage(`{"source":"user","kind":"working_memory","version":1}`))}
	if IsWorkingMemoryMessage(msg) {
		t.Fatalf("foreign working memory should not be recognized: %q", msg.Content)
	}
}

func TestHostStateMessageRecognizesStateAndResourceState(t *testing.T) {
	if !IsTrailingHostStateMessage(Message{Role: "user", Content: FormatStateMessage("/repo")}) {
		t.Fatal("state message should be trailing host state")
	}
	if !IsTrailingHostStateMessage(Message{Role: "user", Content: `{"type":"resource_state","source":"clnkr","commands_used":1,"model_turns_used":2}`}) {
		t.Fatal("resource_state message should be trailing host state")
	}
	if IsTrailingHostStateMessage(Message{Role: "user", Content: "human task"}) {
		t.Fatal("authored user message should not be trailing host state")
	}
}
