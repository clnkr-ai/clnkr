package clnkr_test

import (
	"errors"
	"strings"
	"testing"

	clnkr "github.com/clnkr-ai/clnkr"
)

func TestParseTurn(t *testing.T) {
	t.Run("accepts canonical turns", func(t *testing.T) {
		tests := []struct {
			name  string
			input string
		}{
			{name: "act", input: `{"type":"act","bash":{"commands":[{"command":"ls -la","workdir":null}]},"reasoning":"inspect files"}`},
			{name: "clarify", input: `{"type":"clarify","question":"Which directory?"}`},
			{name: "done", input: `{"type":"done","summary":"Finished the task."}`},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				turn, err := clnkr.ParseTurn(tt.input)
				if err != nil {
					t.Fatalf("ParseTurn(%q) error = %v", tt.input, err)
				}
				if turn == nil {
					t.Fatalf("ParseTurn(%q) returned nil turn", tt.input)
				}
			})
		}
	})

	t.Run("rejects prose wrapped json", func(t *testing.T) {
		raw := "Here is the turn:\n{\"type\":\"act\",\"bash\":{\"commands\":[{\"command\":\"pwd\",\"workdir\":null}]}}\nThanks."
		_, err := clnkr.ParseTurn(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseTurn(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
	})

	t.Run("rejects wrapped provider turns so the wrapper stays provider-owned", func(t *testing.T) {
		raw := `{"turn":{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`
		_, err := clnkr.ParseTurn(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseTurn(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
	})

	t.Run("rejects unknown fields", func(t *testing.T) {
		raw := `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]},"extra":"nope"}`
		_, err := clnkr.ParseTurn(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseTurn(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
		if !strings.Contains(err.Error(), `unknown field "extra"`) {
			t.Fatalf("ParseTurn(%q) error = %v, want unknown field detail", raw, err)
		}
	})

	t.Run("rejects wrong turn specific fields", func(t *testing.T) {
		raw := `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]},"question":""}`
		_, err := clnkr.ParseTurn(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseTurn(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
	})

	t.Run("rejects provider-shaped act siblings", func(t *testing.T) {
		raw := `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]},"question":null,"summary":null,"reasoning":null}`
		_, err := clnkr.ParseTurn(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseTurn(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
		if !strings.Contains(err.Error(), "act turn only allows question when it is omitted") {
			t.Fatalf("ParseTurn(%q) error = %v, want canonical-only detail", raw, err)
		}
	})

	t.Run("rejects missing command workdir in canonical act", func(t *testing.T) {
		raw := `{"type":"act","bash":{"commands":[{"command":"pwd"}]}}`
		_, err := clnkr.ParseTurn(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseTurn(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
	})

	t.Run("normalizes escaped clarify text", func(t *testing.T) {
		raw := `{"type":"clarify","question":"Here are the skills I found:\\n- **citations-agent**: Verify claims.\\\\- **humanizer**: Sound more natural.\\nWhat would you like to work on?","reasoning":"Need to list the available skills.\\\\nThen I can ask a follow-up."}`
		turn, err := clnkr.ParseTurn(raw)
		if err != nil {
			t.Fatalf("ParseTurn(%q) error = %v", raw, err)
		}
		cl, ok := turn.(*clnkr.ClarifyTurn)
		if !ok {
			t.Fatalf("expected *ClarifyTurn, got %T", turn)
		}
		wantQuestion := "Here are the skills I found:\n- **citations-agent**: Verify claims.\n- **humanizer**: Sound more natural.\nWhat would you like to work on?"
		if cl.Question != wantQuestion {
			t.Fatalf("question = %q, want %q", cl.Question, wantQuestion)
		}
		wantReasoning := "Need to list the available skills.\nThen I can ask a follow-up."
		if cl.Reasoning != wantReasoning {
			t.Fatalf("reasoning = %q, want %q", cl.Reasoning, wantReasoning)
		}
	})

	t.Run("rejects whitespace only clarify question", func(t *testing.T) {
		raw := `{"type":"clarify","question":"   "}`
		_, err := clnkr.ParseTurn(raw)
		if !errors.Is(err, clnkr.ErrEmptyClarify) {
			t.Fatalf("ParseTurn(%q) error = %v, want ErrEmptyClarify", raw, err)
		}
	})

	t.Run("rejects whitespace only done summary", func(t *testing.T) {
		raw := `{"type":"done","summary":"\n\t"}`
		_, err := clnkr.ParseTurn(raw)
		if !errors.Is(err, clnkr.ErrEmptySummary) {
			t.Fatalf("ParseTurn(%q) error = %v, want ErrEmptySummary", raw, err)
		}
	})
}

func TestCanonicalRoundTrip(t *testing.T) {
	turns := []clnkr.Turn{
		&clnkr.ActTurn{Bash: clnkr.BashBatch{Commands: []clnkr.BashAction{{Command: "ls -la"}}}, Reasoning: "inspect files"},
		&clnkr.ClarifyTurn{Question: "Which directory?"},
		&clnkr.DoneTurn{Summary: "Finished the task."},
	}

	for _, original := range turns {
		raw, err := clnkr.CanonicalTurnJSON(original)
		if err != nil {
			t.Fatalf("CanonicalTurnJSON(%T) error = %v", original, err)
		}

		parsed, err := clnkr.ParseTurn(raw)
		if err != nil {
			t.Fatalf("ParseTurn(%q) error = %v", raw, err)
		}
		roundTrip, err := clnkr.CanonicalTurnJSON(parsed)
		if err != nil {
			t.Fatalf("CanonicalTurnJSON(parsed) error = %v", err)
		}
		if roundTrip != raw {
			t.Fatalf("round trip = %q, want %q", roundTrip, raw)
		}
	}
}
