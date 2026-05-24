package clnkr

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestProtocolCorrectionMessage(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		protocol    ActProtocol
		contains    []string
		notContains []string
	}{
		{
			name:     "states prior response was ignored",
			err:      fmt.Errorf("%w: unexpected trailing JSON value", ErrInvalidJSON),
			protocol: ActProtocolClnkrInline,
			contains: []string{
				"Your previous response was ignored and no command ran.",
				"If you intended to run commands, resend only that act turn.",
				"Do not jump to done unless prior command results in this conversation already prove the task is complete.",
				protocolActExample,
				"Do not emit multiple JSON objects in one response.",
				"Do not emit an act turn and a done turn together.",
				"Include reasoning in every response; use null if you have nothing to add.",
				`"verification"`,
				`"known_risks"`,
			},
			notContains: []string{
				`top-level "turn" field`,
				`{"type":"clarify"`,
				`{"type":"done"`,
			},
		},
		{
			name:     "mentions invalid pipe escape",
			err:      fmt.Errorf("%w: invalid character '|' in string escape code", ErrInvalidJSON),
			protocol: ActProtocolClnkrInline,
			contains: []string{`\|`, `\\|`},
		},
		{
			name:     "mentions invalid backtick escape",
			err:      fmt.Errorf("%w: invalid character '`' in string escape code", ErrInvalidJSON),
			protocol: ActProtocolClnkrInline,
			contains: []string{"\\`", "\\\\"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := protocolCorrectionMessageFor(tt.err, tt.protocol)
			for _, want := range tt.contains {
				if !strings.Contains(msg, want) {
					t.Fatalf("message missing %q: %q", want, msg)
				}
			}
			for _, unwanted := range tt.notContains {
				if strings.Contains(msg, unwanted) {
					t.Fatalf("message contains %q: %q", unwanted, msg)
				}
			}
		})
	}
}

func TestTurnTypes(t *testing.T) {
	for _, turn := range []Turn{
		ActTurn{Bash: BashBatch{Commands: []BashAction{{Command: "ls"}}}},
		&ActTurn{Bash: BashBatch{Commands: []BashAction{{Command: "ls"}}}},
		ClarifyTurn{Question: "which dir?"},
		DoneTurn{Summary: "done"},
	} {
		turn.turn()
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
		if got := errorToReason(tt.err); got != tt.want {
			t.Errorf("errorToReason(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}
