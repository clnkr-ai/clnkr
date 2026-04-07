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
	if got, want := schema["required"], []any{"turn"}; !sameStringSlice(got, want) {
		t.Fatalf("schema required = %#v, want %#v", got, want)
	}
	if got := schema["anyOf"]; got != nil {
		t.Fatalf("schema anyOf = %#v, want nil", got)
	}

	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties = %T, want map[string]any", schema["properties"])
	}
	if got := len(properties); got != 1 {
		t.Fatalf("len(schema properties) = %d, want 1", got)
	}
	turnProp, ok := properties["turn"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties[turn] = %T, want map[string]any", properties["turn"])
	}
	branches, ok := turnProp["anyOf"].([]any)
	if !ok {
		t.Fatalf("schema properties[turn].anyOf = %T, want []any", turnProp["anyOf"])
	}
	if len(branches) != 3 {
		t.Fatalf("len(schema properties[turn].anyOf) = %d, want 3", len(branches))
	}

	for _, turnType := range []string{"act", "clarify", "done"} {
		branch := schemaBranchForType(t, branches, turnType)
		if got := branch["additionalProperties"]; got != false {
			t.Fatalf("%s branch additionalProperties = %v, want false", turnType, got)
		}
		if got, want := branch["required"], []any{"type", "bash", "question", "summary", "reasoning"}; !sameStringSlice(got, want) {
			t.Fatalf("%s branch required = %#v, want %#v", turnType, got, want)
		}
		branchProperties, ok := branch["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s branch properties = %T, want map[string]any", turnType, branch["properties"])
		}
		typeProp, ok := branchProperties["type"].(map[string]any)
		if !ok {
			t.Fatalf("%s branch properties[type] = %T, want map[string]any", turnType, branchProperties["type"])
		}
		if got := typeProp["type"]; got != "string" {
			t.Fatalf("%s branch properties[type].type = %v, want string", turnType, got)
		}
		if got := typeProp["const"]; got != turnType {
			t.Fatalf("%s branch properties[type].const = %v, want %q", turnType, got, turnType)
		}
	}
}

func TestParse(t *testing.T) {
	t.Run("accepts canonical and provider-shaped turns", func(t *testing.T) {
		tests := []struct {
			name  string
			input string
		}{
			{name: "act", input: `{"type":"act","bash":{"commands":[{"command":"ls -la","workdir":null}]},"reasoning":"inspect files"}`},
			{name: "clarify", input: `{"type":"clarify","question":"Which directory?"}`},
			{name: "done", input: `{"type":"done","summary":"Finished the task."}`},
			{name: "provider shaped act", input: `{"type":"act","bash":{"commands":[{"command":"ls -la","workdir":null}]},"question":null,"summary":null,"reasoning":null}`},
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

	t.Run("rejects prose wrapped json that ParseTurn accepts", func(t *testing.T) {
		raw := "Here is the turn:\n{\"type\":\"act\",\"bash\":{\"commands\":[{\"command\":\"pwd\",\"workdir\":null}]}}\nThanks."

		if _, err := clnkr.ParseTurn(raw); err != nil {
			t.Fatalf("ParseTurn(%q) error = %v, want nil", raw, err)
		}

		_, err := turnschema.Parse(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("Parse(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
	})

	t.Run("rejects wrapped provider turns so the wrapper stays provider-only", func(t *testing.T) {
		raw := `{"turn":{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`

		_, err := turnschema.Parse(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("Parse(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
	})

	t.Run("rejects unknown fields", func(t *testing.T) {
		raw := `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]},"extra":"nope"}`

		_, err := turnschema.Parse(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("Parse(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
		if !strings.Contains(err.Error(), `unknown field "extra"`) {
			t.Fatalf("Parse(%q) error = %v, want unknown field detail", raw, err)
		}
	})

	t.Run("rejects wrong turn specific fields", func(t *testing.T) {
		raw := `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]},"question":""}`

		_, err := turnschema.Parse(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("Parse(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
	})

	t.Run("rejects missing command workdir in canonical act", func(t *testing.T) {
		raw := `{"type":"act","bash":{"commands":[{"command":"pwd"}]}}`

		_, err := turnschema.Parse(raw)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("Parse(%q) error = %v, want ErrInvalidJSON", raw, err)
		}
	})
}

func TestParseProvider(t *testing.T) {
	t.Run("accepts wrapped provider-shaped turns", func(t *testing.T) {
		turn, err := turnschema.ParseProvider(`{"turn":{"type":"act","bash":{"commands":[{"command":"ls -la","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`)
		if err != nil {
			t.Fatalf("ParseProvider error = %v", err)
		}
		if turn == nil {
			t.Fatal("ParseProvider returned nil turn")
		}
	})

	t.Run("rejects missing turn wrapper", func(t *testing.T) {
		_, err := turnschema.ParseProvider(`{"type":"done","summary":"ignored schema"}`)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseProvider error = %v, want ErrInvalidJSON", err)
		}
		if !strings.Contains(err.Error(), `missing required structured output field "turn"`) {
			t.Fatalf("ParseProvider error = %v, want missing turn detail", err)
		}
	})

	t.Run("rejects non-object turn wrapper", func(t *testing.T) {
		for _, raw := range []string{`{"turn":"nope"}`, `{"turn":null}`} {
			_, err := turnschema.ParseProvider(raw)
			if !errors.Is(err, clnkr.ErrInvalidJSON) {
				t.Fatalf("ParseProvider(%q) error = %v, want ErrInvalidJSON", raw, err)
			}
			if !strings.Contains(err.Error(), `structured output field "turn" must be object`) {
				t.Fatalf("ParseProvider(%q) error = %v, want object detail", raw, err)
			}
		}
	})

	t.Run("rejects missing required wrapped provider fields", func(t *testing.T) {
		_, err := turnschema.ParseProvider(`{"turn":{"type":"done","summary":"ignored schema"}}`)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseProvider error = %v, want ErrInvalidJSON", err)
		}
		if !strings.Contains(err.Error(), `missing required structured output field "bash"`) {
			t.Fatalf("ParseProvider error = %v, want missing-field detail", err)
		}
	})

	t.Run("rejects malformed bash command missing workdir", func(t *testing.T) {
		_, err := turnschema.ParseProvider(`{"turn":{"type":"act","bash":{"commands":[{"command":"ls -la"}]},"question":null,"summary":null,"reasoning":null}}`)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseProvider error = %v, want ErrInvalidJSON", err)
		}
	})

	t.Run("rejects missing reasoning field", func(t *testing.T) {
		_, err := turnschema.ParseProvider(`{"turn":{"type":"done","bash":null,"question":null,"summary":"ignored schema"}}`)
		if !errors.Is(err, clnkr.ErrInvalidJSON) {
			t.Fatalf("ParseProvider error = %v, want ErrInvalidJSON", err)
		}
		if !strings.Contains(err.Error(), `missing required structured output field "reasoning"`) {
			t.Fatalf("ParseProvider error = %v, want missing reasoning detail", err)
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
				turn: &clnkr.ActTurn{Bash: clnkr.BashBatch{Commands: []clnkr.BashAction{{Command: "ls -la"}}}, Reasoning: "inspect files"},
				want: `{"type":"act","bash":{"commands":[{"command":"ls -la","workdir":null}]},"reasoning":"inspect files"}`,
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

	t.Run("round trips through strict and ParseTurn parsers", func(t *testing.T) {
		turns := []clnkr.Turn{
			&clnkr.ActTurn{Bash: clnkr.BashBatch{Commands: []clnkr.BashAction{{Command: "ls -la"}}}, Reasoning: "inspect files"},
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

			parsedTurn, err := clnkr.ParseTurn(raw)
			if err != nil {
				t.Fatalf("ParseTurn(%q) error = %v", raw, err)
			}
			parsedRaw, err := turnschema.CanonicalJSON(parsedTurn)
			if err != nil {
				t.Fatalf("CanonicalJSON(parsedTurn) error = %v", err)
			}
			if parsedRaw != raw {
				t.Fatalf("ParseTurn round trip = %q, want %q", parsedRaw, raw)
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
