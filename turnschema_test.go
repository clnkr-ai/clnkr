package clnkr_test

import (
	"errors"
	"strings"
	"testing"

	clnkr "github.com/clnkr-ai/clnkr"
)

func TestParseTurnAcceptsCanonicalTurns(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(*testing.T, clnkr.Turn)
	}{
		{
			name:  "act",
			input: `{"type":"act","bash":{"commands":[{"command":"ls -la","workdir":null}]},"reasoning":"inspect files"}`,
		},
		{
			name:  "act with more than three commands",
			input: `{"type":"act","bash":{"commands":[{"command":"one","workdir":null},{"command":"two","workdir":null},{"command":"three","workdir":null},{"command":"four","workdir":null}]},"reasoning":"batch"}`,
			check: func(t *testing.T, turn clnkr.Turn) {
				act, ok := turn.(*clnkr.ActTurn)
				if !ok {
					t.Fatalf("turn = %T, want *ActTurn", turn)
				}
				if got := len(act.Bash.Commands); got != 4 {
					t.Fatalf("commands = %d, want 4", got)
				}
			},
		},
		{
			name:  "clarify",
			input: `{"type":"clarify","question":"Which directory?"}`,
		},
		{
			name:  "done verified",
			input: `{"type":"done","summary":"Finished the task.","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"test suite passed"}]},"known_risks":[]}`,
		},
		{
			name:  "done partially verified with risks",
			input: `{"type":"done","summary":"Implemented the parser change.","verification":{"status":"partially_verified","checks":[{"command":"go test ./...","outcome":"failed","evidence":"one unrelated fixture failed"}]},"known_risks":["full suite still has an unrelated fixture failure"]}`,
			check: func(t *testing.T, turn clnkr.Turn) {
				done, ok := turn.(*clnkr.DoneTurn)
				if !ok {
					t.Fatalf("turn = %T, want *DoneTurn", turn)
				}
				if done.Verification.Status != clnkr.VerificationPartiallyVerified {
					t.Fatalf(
						"status = %q, want %q",
						done.Verification.Status,
						clnkr.VerificationPartiallyVerified,
					)
				}
				if len(done.KnownRisks) != 1 {
					t.Fatalf("known risks = %#v, want one risk", done.KnownRisks)
				}
			},
		},
		{
			name:  "done not verified with risks and no checks",
			input: `{"type":"done","summary":"Could not verify because the command budget was exhausted.","verification":{"status":"not_verified","checks":[]},"known_risks":["command budget exhausted before verification"]}`,
			check: func(t *testing.T, turn clnkr.Turn) {
				done, ok := turn.(*clnkr.DoneTurn)
				if !ok {
					t.Fatalf("turn = %T, want *DoneTurn", turn)
				}
				if done.Verification.Status != clnkr.VerificationNotVerified {
					t.Fatalf(
						"status = %q, want %q",
						done.Verification.Status,
						clnkr.VerificationNotVerified,
					)
				}
			},
		},
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
			if tt.check != nil {
				tt.check(t, turn)
			}
		})
	}
}

func TestParseTurnRejectsInvalidTurns(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   error
		detail string
	}{
		{
			name:  "prose wrapped json",
			input: "Here is the turn:\n{\"type\":\"act\",\"bash\":{\"commands\":[{\"command\":\"pwd\",\"workdir\":null}]}}\nThanks.",
			want:  clnkr.ErrInvalidJSON,
		},
		{
			name:  "wrapped provider turn so wrapper stays provider-owned",
			input: `{"turn":{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`,
			want:  clnkr.ErrInvalidJSON,
		},
		{
			name:   "unknown fields",
			input:  `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]},"extra":"nope"}`,
			want:   clnkr.ErrInvalidJSON,
			detail: `unknown field "extra"`,
		},
		{
			name:  "wrong turn specific fields",
			input: `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]},"question":""}`,
			want:  clnkr.ErrInvalidJSON,
		},
		{
			name:   "provider-shaped act siblings",
			input:  `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]},"question":null,"summary":null,"reasoning":null}`,
			want:   clnkr.ErrInvalidJSON,
			detail: "act turn only allows question when it is omitted",
		},
		{
			name:  "missing command workdir in canonical act",
			input: `{"type":"act","bash":{"commands":[{"command":"pwd"}]}}`,
			want:  clnkr.ErrInvalidJSON,
		},
		{
			name:  "whitespace only clarify question",
			input: `{"type":"clarify","question":"   "}`,
			want:  clnkr.ErrEmptyClarify,
		},
		{
			name:  "whitespace only done summary",
			input: `{"type":"done","summary":"\n\t"}`,
			want:  clnkr.ErrEmptySummary,
		},
		{
			name:   "summary-only done",
			input:  `{"type":"done","summary":"Finished the task."}`,
			want:   clnkr.ErrInvalidJSON,
			detail: `missing required field "verification"`,
		},
		{
			name:   "done with bad verification status",
			input:  `{"type":"done","summary":"Finished.","verification":{"status":"sure","checks":[{"command":"go test ./...","outcome":"passed","evidence":"tests passed"}]},"known_risks":[]}`,
			want:   clnkr.ErrInvalidJSON,
			detail: `invalid verification status "sure"`,
		},
		{
			name:   "verified done without checks",
			input:  `{"type":"done","summary":"Finished.","verification":{"status":"verified","checks":[]},"known_risks":[]}`,
			want:   clnkr.ErrInvalidJSON,
			detail: "verified done requires at least one verification check",
		},
		{
			name:   "done with null known risks",
			input:  `{"type":"done","summary":"Could not verify.","verification":{"status":"not_verified","checks":[]},"known_risks":null}`,
			want:   clnkr.ErrInvalidJSON,
			detail: `known_risks must be an array`,
		},
		{
			name:   "done with null verification checks",
			input:  `{"type":"done","summary":"Could not verify.","verification":{"status":"not_verified","checks":null},"known_risks":[]}`,
			want:   clnkr.ErrInvalidJSON,
			detail: `verification.checks must be an array`,
		},
		{
			name:   "done without known risks",
			input:  `{"type":"done","summary":"Finished.","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"tests passed"}]}}`,
			want:   clnkr.ErrInvalidJSON,
			detail: `missing required field "known_risks"`,
		},
		{
			name:   "done without verification checks",
			input:  `{"type":"done","summary":"Could not verify.","verification":{"status":"not_verified"},"known_risks":["not run"]}`,
			want:   clnkr.ErrInvalidJSON,
			detail: `missing required field "verification.checks"`,
		},
		{
			name:   "done with non-object verification",
			input:  `{"type":"done","summary":"Could not verify.","verification":[],"known_risks":["not run"]}`,
			want:   clnkr.ErrInvalidJSON,
			detail: `cannot unmarshal array`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := clnkr.ParseTurn(tt.input)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ParseTurn(%q) error = %v, want %v", tt.input, err, tt.want)
			}
			if tt.detail != "" && !strings.Contains(err.Error(), tt.detail) {
				t.Fatalf("ParseTurn(%q) error = %v, want detail %q", tt.input, err, tt.detail)
			}
		})
	}
}

func TestParseTurnNormalizesEscapedClarifyText(t *testing.T) {
	raw := `{"type":"clarify","question":"Here are the skills I found:\\n- **citations-agent**: Verify claims.\\\\- **humanizer**: Sound more natural.\\nWhat would you like to work on?","reasoning":"Need to list the available skills.\\\\nThen I can ask a follow-up."}`
	turn, err := clnkr.ParseTurn(raw)
	if err != nil {
		t.Fatalf("ParseTurn(%q) error = %v", raw, err)
	}
	cl, ok := turn.(*clnkr.ClarifyTurn)
	if !ok {
		t.Fatalf("turn = %T, want *ClarifyTurn", turn)
	}
	wantQuestion := "Here are the skills I found:\n- **citations-agent**: Verify claims.\n- **humanizer**: Sound more natural.\nWhat would you like to work on?"
	if cl.Question != wantQuestion {
		t.Fatalf("question = %q, want %q", cl.Question, wantQuestion)
	}
	wantReasoning := "Need to list the available skills.\nThen I can ask a follow-up."
	if cl.Reasoning != wantReasoning {
		t.Fatalf("reasoning = %q, want %q", cl.Reasoning, wantReasoning)
	}
}

func TestCanonicalRoundTrip(t *testing.T) {
	turns := []clnkr.Turn{
		&clnkr.ActTurn{
			Bash:      clnkr.BashBatch{Commands: []clnkr.BashAction{{Command: "ls -la"}}},
			Reasoning: "inspect files",
		},
		&clnkr.ClarifyTurn{Question: "Which directory?"},
		&clnkr.DoneTurn{
			Summary: "Finished the task.",
			Verification: clnkr.CompletionVerification{
				Status: clnkr.VerificationVerified,
				Checks: []clnkr.VerificationCheck{{
					Command:  "go test ./...",
					Outcome:  "passed",
					Evidence: "test suite passed",
				}},
			},
			KnownRisks: []string{},
		},
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

func TestCanonicalTurnJSONAllowsMoreThanThreeCommands(t *testing.T) {
	raw, err := clnkr.CanonicalTurnJSON(&clnkr.ActTurn{
		Bash: clnkr.BashBatch{Commands: []clnkr.BashAction{
			{Command: "one"},
			{Command: "two"},
			{Command: "three"},
			{Command: "four"},
		}},
		Reasoning: "batch",
	})
	if err != nil {
		t.Fatalf("CanonicalTurnJSON error = %v", err)
	}
	parsed, err := clnkr.ParseTurn(raw)
	if err != nil {
		t.Fatalf("ParseTurn(%q) error = %v", raw, err)
	}
	act := parsed.(*clnkr.ActTurn)
	if got := len(act.Bash.Commands); got != 4 {
		t.Fatalf("commands = %d, want 4", got)
	}
}

func TestCanonicalTurnJSONUsesEmptyArraysForNilDoneSlices(t *testing.T) {
	raw, err := clnkr.CanonicalTurnJSON(&clnkr.DoneTurn{
		Summary: "Could not verify.",
		Verification: clnkr.CompletionVerification{
			Status: clnkr.VerificationNotVerified,
		},
	})
	if err != nil {
		t.Fatalf("CanonicalTurnJSON error = %v", err)
	}
	if !strings.Contains(raw, `"checks":[]`) {
		t.Fatalf("canonical JSON = %q, want empty checks array", raw)
	}
	if !strings.Contains(raw, `"known_risks":[]`) {
		t.Fatalf("canonical JSON = %q, want empty known_risks array", raw)
	}
	if _, err := clnkr.ParseTurn(raw); err != nil {
		t.Fatalf("ParseTurn(%q) error = %v", raw, err)
	}
}
