package clnkr

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestProtocolCorrectionMessage(t *testing.T) {
	t.Run("states prior response was ignored", func(t *testing.T) {
		msg := protocolCorrectionMessage(fmt.Errorf("%w: unexpected trailing JSON value", ErrInvalidJSON))
		if !strings.Contains(msg, "Your previous response was ignored and no command ran.") {
			t.Fatalf("expected ignored-response guidance, got %q", msg)
		}
		if !strings.Contains(msg, "If you intended to run commands, resend only that act turn.") {
			t.Fatalf("expected resend-act guidance, got %q", msg)
		}
		if !strings.Contains(msg, "Do not jump to done unless prior command results in this conversation already prove the task is complete.") {
			t.Fatalf("expected done-guard guidance, got %q", msg)
		}
		if !strings.Contains(msg, protocolActExample) {
			t.Fatalf("expected canonical act guidance, got %q", msg)
		}
		if !strings.Contains(msg, "Do not emit multiple JSON objects in one response.") {
			t.Fatalf("expected multiple-object warning, got %q", msg)
		}
		if !strings.Contains(msg, "Do not emit an act turn and a done turn together.") {
			t.Fatalf("expected act-plus-done warning, got %q", msg)
		}
		if !strings.Contains(msg, "Include reasoning in every response; use null if you have nothing to add.") {
			t.Fatalf("expected explicit reasoning-field guidance, got %q", msg)
		}
		if strings.Contains(msg, `top-level "turn" field`) {
			t.Fatalf("expected provider-specific top-level guidance to be absent, got %q", msg)
		}
		if strings.Contains(msg, `{"type":"clarify"`) {
			t.Fatalf("expected separate clarify example to be absent, got %q", msg)
		}
		if strings.Contains(msg, `{"type":"done"`) {
			t.Fatalf("expected separate done example to be absent, got %q", msg)
		}
	})

	t.Run("mentions invalid pipe escape", func(t *testing.T) {
		msg := protocolCorrectionMessage(fmt.Errorf("%w: invalid character '|' in string escape code", ErrInvalidJSON))
		if !strings.Contains(msg, `\|`) || !strings.Contains(msg, `\\|`) {
			t.Fatalf("expected targeted pipe escape hint, got %q", msg)
		}
	})

	t.Run("mentions invalid backtick escape", func(t *testing.T) {
		msg := protocolCorrectionMessage(fmt.Errorf("%w: invalid character '`' in string escape code", ErrInvalidJSON))
		if !strings.Contains(msg, "\\`") || !strings.Contains(msg, "\\\\") {
			t.Fatalf("expected targeted backtick escape hint, got %q", msg)
		}
	})
}

func TestTurnTypes(t *testing.T) {
	t.Run("ActTurn implements Turn via value", func(t *testing.T) {
		var turn Turn = ActTurn{Bash: BashBatch{Commands: []BashAction{{Command: "ls"}}}}
		turn.turn()
	})
	t.Run("ActTurn implements Turn via pointer", func(t *testing.T) {
		var turn Turn = &ActTurn{Bash: BashBatch{Commands: []BashAction{{Command: "ls"}}}}
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
