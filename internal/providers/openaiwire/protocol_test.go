package openaiwire

import (
	"encoding/json"
	"testing"

	"github.com/clnkr-ai/clnkr"
)

func TestCanonicalTurnsPreserveLiteralEscapes(t *testing.T) {
	raw := `{"type":"done","summary":"Fixed\\n\\\\- item","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"tests passed"}]},"known_risks":[],"reasoning":"Reason\\n\\\\* item"}`
	wantSummary := `Fixed\n\\- item`
	wantReasoning := `Reason\n\\* item`

	turn, err := ParseChatCompletionTurn(raw)
	if err != nil {
		t.Fatalf("ParseChatCompletionTurn: %v", err)
	}
	done := turn.(*clnkr.DoneTurn)
	if done.Summary != wantSummary || done.Reasoning != wantReasoning {
		t.Fatalf("done = %#v, want literal escaped text", done)
	}

	normalized := NormalizeMessagesForProvider([]clnkr.Message{{
		Role:    "assistant",
		Content: raw,
	}})
	var wrapped struct {
		Turn struct {
			Summary   string `json:"summary"`
			Reasoning string `json:"reasoning"`
		} `json:"turn"`
	}
	if err := json.Unmarshal([]byte(normalized[0].Content), &wrapped); err != nil {
		t.Fatalf("unmarshal normalized assistant history: %v", err)
	}
	if wrapped.Turn.Summary != wantSummary || wrapped.Turn.Reasoning != wantReasoning {
		t.Fatalf("normalized turn = %#v, want literal escaped text", wrapped.Turn)
	}
}
