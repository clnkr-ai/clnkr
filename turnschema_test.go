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
			{name: "done", input: `{"type":"done","summary":"Finished the task.","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"test suite passed"}]},"known_risks":[]}`},
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

	t.Run("accepts more than three commands", func(t *testing.T) {
		raw := `{"type":"act","bash":{"commands":[{"command":"one","workdir":null},{"command":"two","workdir":null},{"command":"three","workdir":null},{"command":"four","workdir":null}]},"reasoning":"batch"}`
		turn, err := clnkr.ParseTurn(raw)
		if err != nil {
			t.Fatalf("ParseTurn(%q) error = %v", raw, err)
		}
		act, ok := turn.(*clnkr.ActTurn)
		if !ok {
			t.Fatalf("turn = %T, want *ActTurn", turn)
		}
		if got := len(act.Bash.Commands); got != 4 {
			t.Fatalf("commands = %d, want 4", got)
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

	t.Run("rejects summary-only done", func(t *testing.T) {
		raw := `{"type":"done","summary":"Finished the task."}`
		_, err := clnkr.ParseTurn(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseTurn(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
		if !strings.Contains(err.Error(), `missing required field "verification"`) {
			t.Fatalf("ParseTurn(%q) error = %v, want missing verification detail", raw, err)
		}
	})

	t.Run("rejects done with bad verification status", func(t *testing.T) {
		raw := `{"type":"done","summary":"Finished.","verification":{"status":"sure","checks":[{"command":"go test ./...","outcome":"passed","evidence":"tests passed"}]},"known_risks":[]}`
		_, err := clnkr.ParseTurn(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseTurn(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
		if !strings.Contains(err.Error(), `invalid verification status "sure"`) {
			t.Fatalf("ParseTurn(%q) error = %v, want invalid status detail", raw, err)
		}
	})

	t.Run("rejects verified done without checks", func(t *testing.T) {
		raw := `{"type":"done","summary":"Finished.","verification":{"status":"verified","checks":[]},"known_risks":[]}`
		_, err := clnkr.ParseTurn(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseTurn(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
		if !strings.Contains(err.Error(), "verified done requires at least one verification check") {
			t.Fatalf("ParseTurn(%q) error = %v, want verified checks detail", raw, err)
		}
	})

	t.Run("rejects done with null known risks", func(t *testing.T) {
		raw := `{"type":"done","summary":"Could not verify.","verification":{"status":"not_verified","checks":[]},"known_risks":null}`
		_, err := clnkr.ParseTurn(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseTurn(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
		if !strings.Contains(err.Error(), `known_risks must be an array`) {
			t.Fatalf("ParseTurn(%q) error = %v, want known_risks array detail", raw, err)
		}
	})

	t.Run("rejects done with null verification checks", func(t *testing.T) {
		raw := `{"type":"done","summary":"Could not verify.","verification":{"status":"not_verified","checks":null},"known_risks":[]}`
		_, err := clnkr.ParseTurn(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseTurn(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
		if !strings.Contains(err.Error(), `verification.checks must be an array`) {
			t.Fatalf("ParseTurn(%q) error = %v, want verification checks array detail", raw, err)
		}
	})

	t.Run("rejects done without known risks", func(t *testing.T) {
		raw := `{"type":"done","summary":"Finished.","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"tests passed"}]}}`
		_, err := clnkr.ParseTurn(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseTurn(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
		if !strings.Contains(err.Error(), `missing required field "known_risks"`) {
			t.Fatalf("ParseTurn(%q) error = %v, want missing known_risks detail", raw, err)
		}
	})

	t.Run("rejects done without verification checks", func(t *testing.T) {
		raw := `{"type":"done","summary":"Could not verify.","verification":{"status":"not_verified"},"known_risks":["not run"]}`
		_, err := clnkr.ParseTurn(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseTurn(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
		if !strings.Contains(err.Error(), `missing required field "verification.checks"`) {
			t.Fatalf("ParseTurn(%q) error = %v, want missing verification checks detail", raw, err)
		}
	})

	t.Run("rejects done with non-object verification", func(t *testing.T) {
		raw := `{"type":"done","summary":"Could not verify.","verification":[],"known_risks":["not run"]}`
		_, err := clnkr.ParseTurn(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseTurn(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
		if !strings.Contains(err.Error(), `cannot unmarshal array`) {
			t.Fatalf("ParseTurn(%q) error = %v, want unmarshal detail", raw, err)
		}
	})

	t.Run("accepts partially verified done with risks", func(t *testing.T) {
		raw := `{"type":"done","summary":"Implemented the parser change.","verification":{"status":"partially_verified","checks":[{"command":"go test ./...","outcome":"failed","evidence":"one unrelated fixture failed"}]},"known_risks":["full suite still has an unrelated fixture failure"]}`
		turn, err := clnkr.ParseTurn(raw)
		if err != nil {
			t.Fatalf("ParseTurn(%q) error = %v", raw, err)
		}
		done, ok := turn.(*clnkr.DoneTurn)
		if !ok {
			t.Fatalf("turn = %T, want *DoneTurn", turn)
		}
		if done.Verification.Status != clnkr.VerificationPartiallyVerified {
			t.Fatalf("status = %q, want %q", done.Verification.Status, clnkr.VerificationPartiallyVerified)
		}
		if len(done.KnownRisks) != 1 {
			t.Fatalf("known risks = %#v, want one risk", done.KnownRisks)
		}
	})

	t.Run("accepts not verified done with risks and no checks", func(t *testing.T) {
		raw := `{"type":"done","summary":"Could not verify because the command budget was exhausted.","verification":{"status":"not_verified","checks":[]},"known_risks":["command budget exhausted before verification"]}`
		turn, err := clnkr.ParseTurn(raw)
		if err != nil {
			t.Fatalf("ParseTurn(%q) error = %v", raw, err)
		}
		done, ok := turn.(*clnkr.DoneTurn)
		if !ok {
			t.Fatalf("turn = %T, want *DoneTurn", turn)
		}
		if done.Verification.Status != clnkr.VerificationNotVerified {
			t.Fatalf("status = %q, want %q", done.Verification.Status, clnkr.VerificationNotVerified)
		}
	})
}

func TestCanonicalRoundTrip(t *testing.T) {
	turns := []clnkr.Turn{
		&clnkr.ActTurn{Bash: clnkr.BashBatch{Commands: []clnkr.BashAction{{Command: "ls -la"}}}, Reasoning: "inspect files"},
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
