package actwire

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/clnkr-ai/clnkr"
)

func TestRequestSchema(t *testing.T) {
	tests := []string{"openai", "anthropic"}

	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			schema := RequestSchema()
			want := map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"turn": map[string]any{
						"anyOf": []any{
							map[string]any{
								"type":                 "object",
								"additionalProperties": false,
								"properties": map[string]any{
									"type": map[string]any{
										"type":  "string",
										"const": "act",
									},
									"bash": map[string]any{
										"type":                 "object",
										"additionalProperties": false,
										"properties": map[string]any{
											"commands": commandArraySchema(),
										},
										"required": []string{"commands"},
									},
									"question":  map[string]any{"type": "null"},
									"summary":   map[string]any{"type": "null"},
									"reasoning": expectedNullableStringSchema(),
								},
								"required": []string{"type", "bash", "question", "summary", "reasoning"},
							},
							map[string]any{
								"type":                 "object",
								"additionalProperties": false,
								"properties": map[string]any{
									"type": map[string]any{
										"type":  "string",
										"const": "clarify",
									},
									"bash":      map[string]any{"type": "null"},
									"question":  map[string]any{"type": "string"},
									"summary":   map[string]any{"type": "null"},
									"reasoning": expectedNullableStringSchema(),
								},
								"required": []string{"type", "bash", "question", "summary", "reasoning"},
							},
							expectedDoneTurnSchema(),
						},
					},
				},
				"required": []string{"turn"},
			}

			if !reflect.DeepEqual(schema, want) {
				gotJSON := mustJSON(t, schema)
				wantJSON := mustJSON(t, want)
				t.Fatalf("schema mismatch\ngot:  %s\nwant: %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestUnattendedRequestSchemaExcludesClarify(t *testing.T) {
	schema := UnattendedRequestSchema()
	choices := schema["properties"].(map[string]any)["turn"].(map[string]any)["anyOf"].([]any)
	if len(choices) != 2 {
		t.Fatalf("UnattendedRequestSchema choices = %d, want act and done", len(choices))
	}
	for _, choice := range choices {
		typ := choice.(map[string]any)["properties"].(map[string]any)["type"].(map[string]any)["const"]
		if typ == "clarify" {
			t.Fatalf("UnattendedRequestSchema includes clarify: %#v", schema)
		}
	}
}

func TestFinalTurnSchemasExcludeAct(t *testing.T) {
	schema := FinalTurnSchema()
	choices := schema["properties"].(map[string]any)["turn"].(map[string]any)["anyOf"].([]any)
	if len(choices) != 2 {
		t.Fatalf("FinalTurnSchema choices = %d, want clarify and done", len(choices))
	}
	for _, choice := range choices {
		typ := choice.(map[string]any)["properties"].(map[string]any)["type"].(map[string]any)["const"]
		if typ == "act" {
			t.Fatalf("FinalTurnSchema includes act: %#v", schema)
		}
	}

	doneOnly := DoneOnlySchema()
	doneChoices := doneOnly["properties"].(map[string]any)["turn"].(map[string]any)["anyOf"].([]any)
	if len(doneChoices) != 1 {
		t.Fatalf("DoneOnlySchema choices = %d, want done only", len(doneChoices))
	}
	typ := doneChoices[0].(map[string]any)["properties"].(map[string]any)["type"].(map[string]any)["const"]
	if typ != "done" {
		t.Fatalf("DoneOnlySchema turn type = %#v, want done", typ)
	}
}

func TestParseProviderTurn(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    clnkr.Turn
		wantErr error
	}{
		{
			name: "act",
			raw:  `{"turn":{"type":"act","bash":{"commands":[{"command":"pwd","workdir":"/tmp"}]},"question":null,"summary":null,"reasoning":"inspect"}}`,
			want: &clnkr.ActTurn{
				Bash:      clnkr.BashBatch{Commands: []clnkr.BashAction{{Command: "pwd", Workdir: "/tmp"}}},
				Reasoning: "inspect",
			},
		},
		{
			name: "act with more than three commands",
			raw:  `{"turn":{"type":"act","bash":{"commands":[{"command":"one","workdir":null},{"command":"two","workdir":null},{"command":"three","workdir":null},{"command":"four","workdir":null}]},"question":null,"summary":null,"reasoning":"batch"}}`,
			want: &clnkr.ActTurn{
				Bash: clnkr.BashBatch{Commands: []clnkr.BashAction{
					{Command: "one"},
					{Command: "two"},
					{Command: "three"},
					{Command: "four"},
				}},
				Reasoning: "batch",
			},
		},
		{
			name: "clarify",
			raw:  `{"turn":{"type":"clarify","bash":null,"question":"Which repo?","summary":null,"reasoning":"need target"}}`,
			want: &clnkr.ClarifyTurn{Question: "Which repo?", Reasoning: "need target"},
		},
		{
			name: "done",
			raw:  `{"turn":{"type":"done","bash":null,"question":null,"summary":"complete","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"test suite passed"}]},"known_risks":[],"reasoning":"tests passed"}}`,
			want: &clnkr.DoneTurn{
				Summary: "complete",
				Verification: clnkr.CompletionVerification{
					Status: clnkr.VerificationVerified,
					Checks: []clnkr.VerificationCheck{{
						Command:  "go test ./...",
						Outcome:  "passed",
						Evidence: "test suite passed",
					}},
				},
				KnownRisks: []string{},
				Reasoning:  "tests passed",
			},
		},
		{
			name:    "done without verification",
			raw:     `{"turn":{"type":"done","bash":null,"question":null,"summary":"complete","reasoning":null}}`,
			wantErr: clnkr.ErrInvalidJSON,
		},
		{
			name:    "done with null known risks",
			raw:     `{"turn":{"type":"done","bash":null,"question":null,"summary":"complete","verification":{"status":"not_verified","checks":[]},"known_risks":null,"reasoning":null}}`,
			wantErr: clnkr.ErrInvalidJSON,
		},
		{
			name:    "done with null verification checks",
			raw:     `{"turn":{"type":"done","bash":null,"question":null,"summary":"complete","verification":{"status":"not_verified","checks":null},"known_risks":[],"reasoning":null}}`,
			wantErr: clnkr.ErrInvalidJSON,
		},
		{
			name:    "malformed wrapped act shape",
			raw:     `{"turn":{"type":"act","bash":{"command":"pwd","workdir":null},"question":null,"summary":null,"reasoning":null}}`,
			wantErr: clnkr.ErrInvalidJSON,
		},
		{
			name:    "semantic invalid act",
			raw:     `{"turn":{"type":"act","bash":{"commands":[{"command":"","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`,
			wantErr: clnkr.ErrMissingCommand,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseProviderTurn(tt.raw)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("turn = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestNormalizeMessagesForProvider(t *testing.T) {
	messages := []clnkr.Message{
		{Role: "user", Content: `{"type":"done","summary":"leave users alone"}`},
		{Role: "assistant", Content: `{"type":"done","summary":"canonical","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":"ok"}`},
		{Role: "assistant", Content: `{"turn":{"type":"clarify","bash":null,"question":"Which repo?","summary":null,"reasoning":null}}`},
		{Role: "assistant", Content: "plain text"},
	}

	got := NormalizeMessagesForProvider(messages)

	if !reflect.DeepEqual(messages[0], got[0]) {
		t.Fatalf("user message changed: %#v", got[0])
	}
	if got[1].Content != `{"turn":{"type":"done","bash":null,"question":null,"summary":"canonical","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":"ok"}}` {
		t.Fatalf("canonical assistant content = %q", got[1].Content)
	}
	if got[2].Content != `{"turn":{"type":"clarify","bash":null,"question":"Which repo?","summary":null,"reasoning":null}}` {
		t.Fatalf("provider assistant content = %q", got[2].Content)
	}
	if got[3].Content != "plain text" {
		t.Fatalf("plain assistant content = %q", got[3].Content)
	}
}

func TestParseProviderTurnNormalizesEscapedHumanText(t *testing.T) {
	clarify, err := ParseProviderTurn(`{"turn":{"type":"clarify","bash":null,"question":"First\\n\\\\- escaped","summary":null,"reasoning":"Because\\n\\\\* escaped"}}`)
	if err != nil {
		t.Fatalf("parse clarify: %v", err)
	}
	if got := clarify.(*clnkr.ClarifyTurn); got.Question != "First\n- escaped" || got.Reasoning != "Because\n* escaped" {
		t.Fatalf("clarify = %#v", got)
	}

	done, err := ParseProviderTurn(`{"turn":{"type":"done","bash":null,"question":null,"summary":"Fixed\\n\\\\- item","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":"Reason\\n\\\\* item"}}`)
	if err != nil {
		t.Fatalf("parse done: %v", err)
	}
	if got := done.(*clnkr.DoneTurn); got.Summary != "Fixed\n- item" || got.Reasoning != "Reason\n* item" {
		t.Fatalf("done = %#v", got)
	}
}

func expectedDoneTurnSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"type": map[string]any{
				"type":  "string",
				"const": "done",
			},
			"bash":     map[string]any{"type": "null"},
			"question": map[string]any{"type": "null"},
			"summary":  map[string]any{"type": "string"},
			"verification": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"status": map[string]any{
						"type": "string",
						"enum": []string{"verified", "partially_verified", "not_verified"},
					},
					"checks": map[string]any{
						"type":  "array",
						"items": verificationCheckSchema(),
					},
				},
				"required": []string{"status", "checks"},
			},
			"known_risks": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"reasoning": expectedNullableStringSchema(),
		},
		"required": []string{"type", "bash", "question", "summary", "verification", "known_risks", "reasoning"},
	}
}

func verificationCheckSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"command":  map[string]any{"type": "string"},
			"outcome":  map[string]any{"type": "string"},
			"evidence": map[string]any{"type": "string"},
		},
		"required": []string{"command", "outcome", "evidence"},
	}
}

func commandArraySchema() map[string]any {
	return map[string]any{
		"type":     "array",
		"minItems": 1,
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"command": map[string]any{"type": "string"},
				"workdir": expectedNullableStringSchema(),
			},
			"required": []string{"command", "workdir"},
		},
	}
}

func expectedNullableStringSchema() map[string]any {
	return map[string]any{
		"anyOf": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "null"},
		},
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()

	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	return string(body)
}
