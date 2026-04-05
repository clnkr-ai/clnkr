package turnschema_test

import (
	"errors"
	"strings"
	"testing"

	clnkr "github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/turnschema"
)

func TestSchema(t *testing.T) {
	schema := turnschema.Schema()

	if got := schema["type"]; got != "object" {
		t.Fatalf("schema type = %v, want object", got)
	}
	if got := schema["additionalProperties"]; got != false {
		t.Fatalf("schema additionalProperties = %v, want false", got)
	}
	if got, want := schema["required"], []any{"type", "command", "question", "summary", "reasoning"}; !sameStringSlice(got, want) {
		t.Fatalf("schema required = %#v, want %#v", got, want)
	}

	branches, ok := schema["anyOf"].([]any)
	if !ok {
		t.Fatalf("schema anyOf = %T, want []any", schema["anyOf"])
	}
	if len(branches) != 3 {
		t.Fatalf("len(schema anyOf) = %d, want 3", len(branches))
	}

	for _, turnType := range []string{"act", "clarify", "done"} {
		branch := schemaBranchForType(t, branches, turnType)
		if got := branch["additionalProperties"]; got != false {
			t.Fatalf("%s branch additionalProperties = %v, want false", turnType, got)
		}
		if got, want := branch["required"], []any{"type", "command", "question", "summary", "reasoning"}; !sameStringSlice(got, want) {
			t.Fatalf("%s branch required = %#v, want %#v", turnType, got, want)
		}
	}
}

func TestParse(t *testing.T) {
	t.Run("accepts canonical and provider-shaped turns", func(t *testing.T) {
		tests := []struct {
			name  string
			input string
		}{
			{name: "act", input: `{"type":"act","command":"ls -la","reasoning":"inspect files"}`},
			{name: "clarify", input: `{"type":"clarify","question":"Which directory?"}`},
			{name: "done", input: `{"type":"done","summary":"Finished the task."}`},
			{name: "provider shaped act", input: `{"type":"act","command":"ls -la","question":null,"summary":null,"reasoning":null}`},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				turn, err := turnschema.Parse(tt.input)
				if err != nil {
					t.Fatalf("Parse(%q) error = %v", tt.input, err)
				}
				if turn == nil {
					t.Fatalf("Parse(%q) returned nil turn", tt.input)
				}
			})
		}
	})

	t.Run("rejects prose wrapped json that legacy parser accepts", func(t *testing.T) {
		raw := "Here is the turn:\n{\"type\":\"act\",\"command\":\"pwd\"}\nThanks."

		if _, err := clnkr.ParseTurn(raw); err != nil {
			t.Fatalf("ParseTurn(%q) error = %v, want nil", raw, err)
		}

		_, err := turnschema.Parse(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("Parse(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
	})

	t.Run("rejects unknown fields", func(t *testing.T) {
		raw := `{"type":"act","command":"pwd","extra":"nope"}`

		_, err := turnschema.Parse(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("Parse(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
		if !strings.Contains(err.Error(), `unknown field "extra"`) {
			t.Fatalf("Parse(%q) error = %v, want unknown field detail", raw, err)
		}
	})

	t.Run("rejects wrong turn specific fields", func(t *testing.T) {
		raw := `{"type":"act","command":"pwd","question":""}`

		_, err := turnschema.Parse(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("Parse(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
	})
}

func TestParseProvider(t *testing.T) {
	t.Run("accepts provider-shaped turns", func(t *testing.T) {
		turn, err := turnschema.ParseProvider(`{"type":"act","command":"ls -la","question":null,"summary":null,"reasoning":null}`)
		if err != nil {
			t.Fatalf("ParseProvider error = %v", err)
		}
		if turn == nil {
			t.Fatal("ParseProvider returned nil turn")
		}
	})

	t.Run("rejects missing required provider fields", func(t *testing.T) {
		_, err := turnschema.ParseProvider(`{"type":"done","summary":"ignored schema"}`)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseProvider error = %v, want ErrInvalidJSON", err)
		}
		if !strings.Contains(err.Error(), `missing required structured output field "command"`) {
			t.Fatalf("ParseProvider error = %v, want missing-field detail", err)
		}
	})
}

func TestCanonicalJSON(t *testing.T) {
	t.Run("emits canonical json for each turn type", func(t *testing.T) {
		tests := []struct {
			name string
			turn clnkr.Turn
			want string
		}{
			{
				name: "act",
				turn: &clnkr.ActTurn{Command: "ls -la", Reasoning: "inspect files"},
				want: `{"type":"act","command":"ls -la","reasoning":"inspect files"}`,
			},
			{
				name: "clarify",
				turn: &clnkr.ClarifyTurn{Question: "Which directory?"},
				want: `{"type":"clarify","question":"Which directory?"}`,
			},
			{
				name: "done",
				turn: &clnkr.DoneTurn{Summary: "Finished the task."},
				want: `{"type":"done","summary":"Finished the task."}`,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got, err := turnschema.CanonicalJSON(tt.turn)
				if err != nil {
					t.Fatalf("CanonicalJSON(%T) error = %v", tt.turn, err)
				}
				if got != tt.want {
					t.Fatalf("CanonicalJSON(%T) = %q, want %q", tt.turn, got, tt.want)
				}
			})
		}
	})

	t.Run("round trips through strict and legacy parsers", func(t *testing.T) {
		turns := []clnkr.Turn{
			&clnkr.ActTurn{Command: "ls -la", Reasoning: "inspect files"},
			&clnkr.ClarifyTurn{Question: "Which directory?"},
			&clnkr.DoneTurn{Summary: "Finished the task."},
		}

		for _, original := range turns {
			raw, err := turnschema.CanonicalJSON(original)
			if err != nil {
				t.Fatalf("CanonicalJSON(%T) error = %v", original, err)
			}

			strictTurn, err := turnschema.Parse(raw)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", raw, err)
			}
			strictRaw, err := turnschema.CanonicalJSON(strictTurn)
			if err != nil {
				t.Fatalf("CanonicalJSON(strictTurn) error = %v", err)
			}
			if strictRaw != raw {
				t.Fatalf("strict round trip = %q, want %q", strictRaw, raw)
			}

			legacyTurn, err := clnkr.ParseTurn(raw)
			if err != nil {
				t.Fatalf("ParseTurn(%q) error = %v", raw, err)
			}
			legacyRaw, err := turnschema.CanonicalJSON(legacyTurn)
			if err != nil {
				t.Fatalf("CanonicalJSON(legacyTurn) error = %v", err)
			}
			if legacyRaw != raw {
				t.Fatalf("legacy round trip = %q, want %q", legacyRaw, raw)
			}
		}
	})

	t.Run("wraps unsupported turn errors", func(t *testing.T) {
		_, err := turnschema.CanonicalJSON(nil)
		if !errors.Is(err, turnschema.ErrUnsupportedTurn) {
			t.Fatalf("CanonicalJSON(nil) error = %v, want ErrUnsupportedTurn", err)
		}
	})
}

func schemaBranchForType(t *testing.T, branches []any, turnType string) map[string]any {
	t.Helper()

	for _, branch := range branches {
		branchMap, ok := branch.(map[string]any)
		if !ok {
			continue
		}
		properties, ok := branchMap["properties"].(map[string]any)
		if !ok {
			continue
		}
		typeProp, ok := properties["type"].(map[string]any)
		if !ok {
			continue
		}
		if typeProp["const"] == turnType {
			return branchMap
		}
	}

	t.Fatalf("no schema branch found for type %q", turnType)
	return nil
}

func sameStringSlice(got any, want []any) bool {
	gotSlice, ok := got.([]string)
	if !ok || len(gotSlice) != len(want) {
		return false
	}
	for i := range gotSlice {
		if gotSlice[i] != want[i] {
			return false
		}
	}
	return true
}
