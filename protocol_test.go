package clnkr

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestParseTurn(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{
			name:  "valid act turn with one command",
			input: `{"type":"act","bash":{"commands":[{"command":"ls -la","workdir":null}]}}`,
		},
		{
			name:  "valid act turn with two commands",
			input: `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null},{"command":"go test ./...","workdir":null}]}}`,
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
			name:    "wrapped provider turn rejected",
			input:   `{"turn":{"type":"act","bash":{"commands":[{"command":"ls -la","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`,
			wantErr: ErrInvalidJSON,
		},
		{
			name:  "act with reasoning preserved",
			input: `{"type":"act","bash":{"commands":[{"command":"echo hi","workdir":null}]},"reasoning":"testing output"}`,
		},
		{
			name:  "repairs invalid pipe escape in command",
			input: `{"type":"act","bash":{"commands":[{"command":"grep 'A\|B' file.txt","workdir":null}]}}`,
		},
		{
			name:  "repairs invalid backtick escape in command",
			input: "{\"type\":\"act\",\"bash\":{\"commands\":[{\"command\":\"printf \\`hi\\`\",\"workdir\":null}]}}",
		},
		{
			name:  "repairs malformed unicode escape in command",
			input: `{"type":"act","bash":{"commands":[{"command":"printf '\u12XZ'","workdir":null}]}}`,
		},
		{
			name:    "invalid json",
			input:   `not json at all`,
			wantErr: ErrInvalidJSON,
		},
		{
			name:    "missing type field",
			input:   `{}`,
			wantErr: ErrUnknownTurnType,
		},
		{
			name:    "unknown type",
			input:   `{"type":"explode"}`,
			wantErr: ErrUnknownTurnType,
		},
		{
			name:    "prior act shape rejected",
			input:   `{"type":"act","bash":{"command":"ls -la","workdir":null}}`,
			wantErr: ErrInvalidJSON,
		},
		{
			name:    "act missing command",
			input:   `{"type":"act","bash":{"commands":[{"workdir":null}]}}`,
			wantErr: ErrMissingCommand,
		},
		{
			name:    "act empty command",
			input:   `{"type":"act","bash":{"commands":[{"command":"","workdir":null}]}}`,
			wantErr: ErrMissingCommand,
		},
		{
			name:    "act missing commands field",
			input:   `{"type":"act","bash":{}}`,
			wantErr: ErrInvalidJSON,
		},
		{
			name:    "act rejects non-null question field",
			input:   `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]},"question":"extra"}`,
			wantErr: ErrInvalidJSON,
		},
		{
			name:    "rejects too many commands",
			input:   `{"type":"act","bash":{"commands":[{"command":"a","workdir":null},{"command":"b","workdir":null},{"command":"c","workdir":null},{"command":"d","workdir":null}]}}`,
			wantErr: ErrTooManyCommands,
		},
		{
			name:    "act missing workdir field",
			input:   `{"type":"act","bash":{"commands":[{"command":"pwd"}]}}`,
			wantErr: ErrInvalidJSON,
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
			name:    "done rejects non-null bash field",
			input:   `{"type":"done","summary":"ok","bash":{"commands":[{"command":"pwd","workdir":null}]}}`,
			wantErr: ErrInvalidJSON,
		},
		{
			name:  "json wrapped in prose with json fence",
			input: "Here is my response:\n\n```json\n{\"type\":\"act\",\"bash\":{\"commands\":[{\"command\":\"ls\",\"workdir\":null}]}}\n```\n\nLet me know.",
		},
		{
			name:  "json wrapped in plain code fence",
			input: "```\n{\"type\":\"done\",\"summary\":\"All finished.\"}\n```",
		},
		{
			name:  "bare json with surrounding prose",
			input: "I'll run this command:\n{\"type\":\"act\",\"bash\":{\"commands\":[{\"command\":\"echo hello\",\"workdir\":null}]}}\nThat should work.",
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
		turn, err := ParseTurn(`{"type":"act","bash":{"commands":[{"command":"ls -la","workdir":"subdir"},{"command":"pwd","workdir":null}]}}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		act, ok := turn.(*ActTurn)
		if !ok {
			t.Fatalf("expected *ActTurn, got %T", turn)
		}
		if got := len(act.Bash.Commands); got != 2 {
			t.Fatalf("len(commands) = %d, want 2", got)
		}
		if act.Bash.Commands[0].Command != "ls -la" {
			t.Errorf("got command %q, want %q", act.Bash.Commands[0].Command, "ls -la")
		}
		if act.Bash.Commands[0].Workdir != "subdir" {
			t.Errorf("got workdir %q, want %q", act.Bash.Commands[0].Workdir, "subdir")
		}
		if act.Bash.Commands[1].Command != "pwd" {
			t.Errorf("got command %q, want %q", act.Bash.Commands[1].Command, "pwd")
		}
		if act.Bash.Commands[1].Workdir != "" {
			t.Errorf("got workdir %q, want empty string", act.Bash.Commands[1].Workdir)
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
		turn, err := ParseTurn(`{"type":"act","bash":{"commands":[{"command":"ls","workdir":null}]},"reasoning":"checking files"}`)
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
		turn, err := ParseTurn(`{"type":"act","bash":{"commands":[{"command":"ls","workdir":null}]}}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		act := turn.(*ActTurn)
		if act.Reasoning != "" {
			t.Errorf("expected empty reasoning, got %q", act.Reasoning)
		}
	})
}

func TestSanitizeJSONEscapes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"valid: quote", `{"c":"say \"hi\""}`, `{"c":"say \"hi\""}`},
		{"valid: backslash", `{"c":"a\\b"}`, `{"c":"a\\b"}`},
		{"valid: slash", `{"c":"a\/b"}`, `{"c":"a\/b"}`},
		{"valid: backspace", `{"c":"a\bb"}`, `{"c":"a\bb"}`},
		{"valid: formfeed", `{"c":"a\fb"}`, `{"c":"a\fb"}`},
		{"valid: newline", `{"c":"a\nb"}`, `{"c":"a\nb"}`},
		{"valid: carriage return", `{"c":"a\rb"}`, `{"c":"a\rb"}`},
		{"valid: tab", `{"c":"a\tb"}`, `{"c":"a\tb"}`},
		{"valid: unicode", `{"c":"\u0041"}`, `{"c":"\u0041"}`},
		{"invalid: pipe", `{"c":"grep 'A\|B'"}`, `{"c":"grep 'A\\|B'"}`},
		{"invalid: backtick", "{\"c\":\"\\`hi\\`\"}", "{\"c\":\"\\\\`hi\\\\`\"}"},
		{"invalid: malformed unicode", `{"c":"\u12XZ"}`, `{"c":"\\u12XZ"}`},
		{"outside string unchanged", `{"type":"act"}`, `{"type":"act"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeJSONEscapes(tt.input)
			if got != tt.want {
				t.Fatalf("sanitizeJSONEscapes(%q)\n got %q\nwant %q", tt.input, got, tt.want)
			}
		})
	}
}

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
		if !strings.Contains(msg, `{"turn":{"type":"act","bash":{"commands":[{"command":"...","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`) {
			t.Fatalf("expected wrapped act guidance, got %q", msg)
		}
		if !strings.Contains(msg, `top-level "turn" field`) {
			t.Fatalf("expected top-level turn guidance, got %q", msg)
		}
		if !strings.Contains(msg, "Do not emit multiple JSON objects in one response.") {
			t.Fatalf("expected multiple-object warning, got %q", msg)
		}
		if !strings.Contains(msg, "Do not emit an act turn and a done turn together.") {
			t.Fatalf("expected act-plus-done warning, got %q", msg)
		}
		if !strings.Contains(msg, "Include turn.reasoning in every response; use null if you have nothing to add.") {
			t.Fatalf("expected explicit reasoning-field guidance, got %q", msg)
		}
		if strings.Contains(msg, `{"turn":{"type":"clarify"`) {
			t.Fatalf("expected separate wrapped clarify example to be absent, got %q", msg)
		}
		if strings.Contains(msg, `{"turn":{"type":"done"`) {
			t.Fatalf("expected separate wrapped done example to be absent, got %q", msg)
		}
		if strings.Contains(msg, `{"type":"act","bash":{"command":"...","workdir":null}}`) {
			t.Fatalf("expected unwrapped act shape to be absent, got %q", msg)
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

func TestParseTurnTypes(t *testing.T) {
	t.Run("ActTurn implements Turn via value", func(t *testing.T) {
		var turn Turn = ActTurn{Bash: BashBatch{Commands: []BashAction{{Command: "ls"}}}}
		turn.turn() // compile-time check
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

func TestExtractJSON_NestedBraces(t *testing.T) {
	// Verify that braces inside JSON string values are handled correctly.
	input := `{"type":"act","command":"echo '{}'"}`
	input = `{"type":"act","bash":{"commands":[{"command":"echo '{}'","workdir":null}]}}`
	turn, err := ParseTurn(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	act, ok := turn.(*ActTurn)
	if !ok {
		t.Fatalf("expected *ActTurn, got %T", turn)
	}
	if got := len(act.Bash.Commands); got != 1 {
		t.Fatalf("len(commands) = %d, want 1", got)
	}
	if act.Bash.Commands[0].Command != "echo '{}'" {
		t.Errorf("got command %q, want %q", act.Bash.Commands[0].Command, "echo '{}'")
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
		{"prose before", "Let me check.\n" + `{"type":"act","bash":{"commands":[{"command":"ls","workdir":null}]}}`, `{"type":"act","bash":{"commands":[{"command":"ls","workdir":null}]}}`, false},
		{"code fenced json", "```json\n{\"type\":\"done\",\"summary\":\"ok\"}\n```", `{"type":"done","summary":"ok"}`, false},
		{"code fenced plain", "```\n{\"type\":\"done\",\"summary\":\"ok\"}\n```", `{"type":"done","summary":"ok"}`, false},
		{"nested braces", `{"type":"act","bash":{"commands":[{"command":"echo '{}'","workdir":null}]}}`, `{"type":"act","bash":{"commands":[{"command":"echo '{}'","workdir":null}]}}`, false},
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
		{ErrTooManyCommands, "too_many_commands"},
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
