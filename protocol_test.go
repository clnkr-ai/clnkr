package hew

import (
	"errors"
	"testing"
)

func TestParseTurn(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{
			name:  "valid act turn",
			input: `{"type":"act","command":"ls -la"}`,
		},
		{
			name:  "valid clarify turn",
			input: `{"type":"clarify","question":"Which directory?"}`,
		},
		{
			name:  "valid done turn",
			input: `{"type":"done","summary":"Created the file and verified it works."}`,
		},
		{
			name:  "act with reasoning preserved",
			input: `{"type":"act","command":"echo hi","reasoning":"testing output"}`,
		},
		{
			name:    "invalid json",
			input:   `not json at all`,
			wantErr: ErrInvalidJSON,
		},
		{
			name:    "missing type field",
			input:   `{"command":"ls"}`,
			wantErr: ErrUnknownTurnType,
		},
		{
			name:    "unknown type",
			input:   `{"type":"explode","data":"boom"}`,
			wantErr: ErrUnknownTurnType,
		},
		{
			name:    "act missing command",
			input:   `{"type":"act"}`,
			wantErr: ErrMissingCommand,
		},
		{
			name:    "act empty command",
			input:   `{"type":"act","command":""}`,
			wantErr: ErrMissingCommand,
		},
		{
			name:    "clarify missing question",
			input:   `{"type":"clarify"}`,
			wantErr: ErrEmptyClarify,
		},
		{
			name:    "clarify empty question",
			input:   `{"type":"clarify","question":""}`,
			wantErr: ErrEmptyClarify,
		},
		{
			name:    "done missing summary",
			input:   `{"type":"done"}`,
			wantErr: ErrEmptySummary,
		},
		{
			name:    "done empty summary",
			input:   `{"type":"done","summary":""}`,
			wantErr: ErrEmptySummary,
		},
		{
			name:  "json wrapped in prose with json fence",
			input: "Here is my response:\n\n```json\n{\"type\":\"act\",\"command\":\"ls\"}\n```\n\nLet me know.",
		},
		{
			name:  "json wrapped in plain code fence",
			input: "```\n{\"type\":\"done\",\"summary\":\"All finished.\"}\n```",
		},
		{
			name:  "bare json with surrounding prose",
			input: "I'll run this command:\n{\"type\":\"act\",\"command\":\"echo hello\"}\nThat should work.",
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: ErrInvalidJSON,
		},
		{
			name:    "whitespace only",
			input:   "   \n\n  ",
			wantErr: ErrInvalidJSON,
		},
		{
			name:    "no json at all",
			input:   "Here is my analysis of the problem.",
			wantErr: ErrInvalidJSON,
		},
		{
			name:    "truncated json",
			input:   `{"type":"act"`,
			wantErr: ErrInvalidJSON,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			turn, err := ParseTurn(tt.input)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("want error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Verify type is correct by type assertion
			switch tt.input {
			default:
				if turn == nil {
					t.Fatal("expected non-nil turn")
				}
			}
		})
	}
}

func TestParseTurnTypeAssertions(t *testing.T) {
	t.Run("act turn fields", func(t *testing.T) {
		turn, err := ParseTurn(`{"type":"act","command":"ls -la"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		act, ok := turn.(*ActTurn)
		if !ok {
			t.Fatalf("expected *ActTurn, got %T", turn)
		}
		if act.Command != "ls -la" {
			t.Errorf("got command %q, want %q", act.Command, "ls -la")
		}
	})

	t.Run("clarify turn fields", func(t *testing.T) {
		turn, err := ParseTurn(`{"type":"clarify","question":"Which directory?"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cl, ok := turn.(*ClarifyTurn)
		if !ok {
			t.Fatalf("expected *ClarifyTurn, got %T", turn)
		}
		if cl.Question != "Which directory?" {
			t.Errorf("got question %q", cl.Question)
		}
	})

	t.Run("done turn fields", func(t *testing.T) {
		turn, err := ParseTurn(`{"type":"done","summary":"All done."}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		d, ok := turn.(*DoneTurn)
		if !ok {
			t.Fatalf("expected *DoneTurn, got %T", turn)
		}
		if d.Summary != "All done." {
			t.Errorf("got summary %q", d.Summary)
		}
	})
}

func TestParseTurnReasoningPreserved(t *testing.T) {
	t.Run("act reasoning", func(t *testing.T) {
		turn, err := ParseTurn(`{"type":"act","command":"ls","reasoning":"checking files"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		act := turn.(*ActTurn)
		if act.Reasoning != "checking files" {
			t.Errorf("got reasoning %q, want %q", act.Reasoning, "checking files")
		}
	})

	t.Run("clarify reasoning", func(t *testing.T) {
		turn, err := ParseTurn(`{"type":"clarify","question":"Which?","reasoning":"need to know"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cl := turn.(*ClarifyTurn)
		if cl.Reasoning != "need to know" {
			t.Errorf("got reasoning %q, want %q", cl.Reasoning, "need to know")
		}
	})

	t.Run("done reasoning", func(t *testing.T) {
		turn, err := ParseTurn(`{"type":"done","summary":"Finished.","reasoning":"all tests pass"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		d := turn.(*DoneTurn)
		if d.Reasoning != "all tests pass" {
			t.Errorf("got reasoning %q, want %q", d.Reasoning, "all tests pass")
		}
	})

	t.Run("empty reasoning is omitted", func(t *testing.T) {
		turn, err := ParseTurn(`{"type":"act","command":"ls"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		act := turn.(*ActTurn)
		if act.Reasoning != "" {
			t.Errorf("expected empty reasoning, got %q", act.Reasoning)
		}
	})
}

func TestParseTurnTypes(t *testing.T) {
	t.Run("ActTurn implements Turn via value", func(t *testing.T) {
		var turn Turn = ActTurn{Command: "ls"}
		turn.turn() // compile-time check
	})
	t.Run("ActTurn implements Turn via pointer", func(t *testing.T) {
		var turn Turn = &ActTurn{Command: "ls"}
		turn.turn()
	})
	t.Run("ClarifyTurn implements Turn", func(t *testing.T) {
		var turn Turn = ClarifyTurn{Question: "which dir?"}
		turn.turn()
	})
	t.Run("DoneTurn implements Turn", func(t *testing.T) {
		var turn Turn = DoneTurn{Summary: "done"}
		turn.turn()
	})
}

func TestExtractJSON_NestedBraces(t *testing.T) {
	// Verify that braces inside JSON string values are handled correctly.
	input := `{"type":"act","command":"echo '{}'"}`
	turn, err := ParseTurn(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	act, ok := turn.(*ActTurn)
	if !ok {
		t.Fatalf("expected *ActTurn, got %T", turn)
	}
	if act.Command != "echo '{}'" {
		t.Errorf("got command %q, want %q", act.Command, "echo '{}'")
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"raw object", `{"type":"act"}`, `{"type":"act"}`, false},
		{"prose before", "Let me check.\n" + `{"type":"act","command":"ls"}`, `{"type":"act","command":"ls"}`, false},
		{"code fenced json", "```json\n{\"type\":\"done\",\"summary\":\"ok\"}\n```", `{"type":"done","summary":"ok"}`, false},
		{"code fenced plain", "```\n{\"type\":\"done\",\"summary\":\"ok\"}\n```", `{"type":"done","summary":"ok"}`, false},
		{"nested braces", `{"type":"act","command":"echo '{}'"}`, `{"type":"act","command":"echo '{}'"}`, false},
		{"empty input", "", "", true},
		{"no json", "just plain text", "", true},
		{"unbalanced", `{"type":"act"`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractJSON(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				if !errors.Is(err, ErrInvalidJSON) {
					t.Fatalf("expected ErrInvalidJSON, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("want %q, got %q", tt.want, got)
			}
		})
	}
}

func TestErrorToReason(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{ErrInvalidJSON, "invalid_json"},
		{ErrMissingCommand, "missing_command"},
		{ErrEmptyClarify, "empty_clarify"},
		{ErrEmptySummary, "empty_summary"},
		{ErrUnknownTurnType, "unknown_turn_type"},
		{errors.New("something else"), "unknown"},
	}
	for _, tt := range tests {
		got := errorToReason(tt.err)
		if got != tt.want {
			t.Errorf("errorToReason(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}
