package actwire

import (
	"errors"
	"reflect"
	"testing"

	"github.com/clnkr-ai/clnkr"
)

type schemaAssert func(*testing.T, map[string]any)

func TestSchemasExposeAllowedTurnShapes(t *testing.T) {
	tests := []struct {
		name    string
		schema  map[string]any
		asserts []schemaAssert
	}{
		{
			name:    "request",
			schema:  RequestSchema(),
			asserts: []schemaAssert{assertActTurnSchema, assertClarifyTurnSchema, assertDoneTurnSchema},
		},
		{
			name:    "unattended request",
			schema:  UnattendedRequestSchema(),
			asserts: []schemaAssert{assertActTurnSchema, assertDoneTurnSchema},
		},
		{
			name:    "final turn",
			schema:  FinalTurnSchema(),
			asserts: []schemaAssert{assertClarifyTurnSchema, assertDoneTurnSchema},
		},
		{
			name:    "done only",
			schema:  DoneOnlySchema(),
			asserts: []schemaAssert{assertDoneTurnSchema},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			choices := turnChoices(t, tt.schema)
			if len(choices) != len(tt.asserts) {
				t.Fatalf("turn choices = %d, want %d", len(choices), len(tt.asserts))
			}
			for i, assert := range tt.asserts {
				assert(t, choices[i])
			}
		})
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
	want := []clnkr.Message{
		messages[0],
		{Role: "assistant", Content: `{"turn":{"type":"done","bash":null,"question":null,"summary":"canonical","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":"ok"}}`},
		messages[2],
		messages[3],
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("messages = %#v, want %#v", got, want)
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

func turnChoices(t *testing.T, schema map[string]any) []map[string]any {
	t.Helper()

	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("schema envelope = %#v", schema)
	}
	if got := schema["required"]; !reflect.DeepEqual(got, []string{"turn"}) {
		t.Fatalf("schema required = %#v, want turn", got)
	}

	choices := schema["properties"].(map[string]any)["turn"].(map[string]any)["anyOf"].([]any)
	branches := make([]map[string]any, 0, len(choices))
	for _, choice := range choices {
		branches = append(branches, choice.(map[string]any))
	}
	return branches
}

func assertActTurnSchema(t *testing.T, schema map[string]any) {
	t.Helper()

	properties := assertTurnSchema(t, schema, "act", []string{"type", "bash", "question", "summary", "reasoning"})
	assertNullSchema(t, properties, "question")
	assertNullSchema(t, properties, "summary")
	assertNullableStringSchema(t, properties["reasoning"])

	bash := properties["bash"].(map[string]any)
	if bash["type"] != "object" || bash["additionalProperties"] != false {
		t.Fatalf("act bash schema = %#v", bash)
	}
	if got := bash["required"]; !reflect.DeepEqual(got, []string{"commands"}) {
		t.Fatalf("act bash required = %#v, want commands", got)
	}

	commands := bash["properties"].(map[string]any)["commands"].(map[string]any)
	if commands["type"] != "array" || commands["minItems"] != 1 {
		t.Fatalf("act commands schema = %#v", commands)
	}
	command := commands["items"].(map[string]any)
	if command["type"] != "object" || command["additionalProperties"] != false {
		t.Fatalf("act command item schema = %#v", command)
	}
	if got := command["required"]; !reflect.DeepEqual(got, []string{"command", "workdir"}) {
		t.Fatalf("act command required = %#v, want command and workdir", got)
	}
	commandProperties := command["properties"].(map[string]any)
	if got := commandProperties["command"]; !reflect.DeepEqual(got, map[string]any{"type": "string"}) {
		t.Fatalf("act command property = %#v", got)
	}
	assertNullableStringSchema(t, commandProperties["workdir"])
}

func assertClarifyTurnSchema(t *testing.T, schema map[string]any) {
	t.Helper()

	properties := assertTurnSchema(t, schema, "clarify", []string{"type", "bash", "question", "summary", "reasoning"})
	assertNullSchema(t, properties, "bash")
	if got := properties["question"]; !reflect.DeepEqual(got, map[string]any{"type": "string"}) {
		t.Fatalf("clarify question schema = %#v", got)
	}
	assertNullSchema(t, properties, "summary")
	assertNullableStringSchema(t, properties["reasoning"])
}

func assertDoneTurnSchema(t *testing.T, schema map[string]any) {
	t.Helper()

	properties := assertTurnSchema(t, schema, "done", []string{"type", "bash", "question", "summary", "verification", "known_risks", "reasoning"})
	assertNullSchema(t, properties, "bash")
	assertNullSchema(t, properties, "question")
	if got := properties["summary"]; !reflect.DeepEqual(got, map[string]any{"type": "string"}) {
		t.Fatalf("done summary schema = %#v", got)
	}
	assertNullableStringSchema(t, properties["reasoning"])

	verification := properties["verification"].(map[string]any)
	if verification["type"] != "object" || verification["additionalProperties"] != false {
		t.Fatalf("verification schema = %#v", verification)
	}
	if got := verification["required"]; !reflect.DeepEqual(got, []string{"status", "checks"}) {
		t.Fatalf("verification required = %#v, want status and checks", got)
	}
	verificationProperties := verification["properties"].(map[string]any)
	if got := verificationProperties["status"]; !reflect.DeepEqual(got, map[string]any{"type": "string", "enum": []string{"verified", "partially_verified", "not_verified"}}) {
		t.Fatalf("verification status schema = %#v", got)
	}
	checks := verificationProperties["checks"].(map[string]any)
	if checks["type"] != "array" {
		t.Fatalf("verification checks schema = %#v", checks)
	}
	check := checks["items"].(map[string]any)
	if check["type"] != "object" || check["additionalProperties"] != false {
		t.Fatalf("verification check schema = %#v", check)
	}
	if got := check["required"]; !reflect.DeepEqual(got, []string{"command", "outcome", "evidence"}) {
		t.Fatalf("verification check required = %#v, want command, outcome, evidence", got)
	}
	checkProperties := check["properties"].(map[string]any)
	for _, name := range []string{"command", "outcome", "evidence"} {
		if got := checkProperties[name]; !reflect.DeepEqual(got, map[string]any{"type": "string"}) {
			t.Fatalf("verification check %s schema = %#v", name, got)
		}
	}

	risks := properties["known_risks"].(map[string]any)
	if got := risks; !reflect.DeepEqual(got, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}) {
		t.Fatalf("known risks schema = %#v", got)
	}
}

func assertTurnSchema(t *testing.T, schema map[string]any, typ string, required []string) map[string]any {
	t.Helper()

	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("%s schema = %#v", typ, schema)
	}
	if got := schema["required"]; !reflect.DeepEqual(got, required) {
		t.Fatalf("%s required = %#v, want %#v", typ, got, required)
	}
	properties := schema["properties"].(map[string]any)
	if got := properties["type"]; !reflect.DeepEqual(got, map[string]any{"type": "string", "const": typ}) {
		t.Fatalf("%s type schema = %#v", typ, got)
	}
	return properties
}

func assertNullSchema(t *testing.T, properties map[string]any, name string) {
	t.Helper()

	if got := properties[name]; !reflect.DeepEqual(got, map[string]any{"type": "null"}) {
		t.Fatalf("%s schema = %#v, want null", name, got)
	}
}

func assertNullableStringSchema(t *testing.T, got any) {
	t.Helper()

	want := map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "null"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("nullable string schema = %#v, want %#v", got, want)
	}
}
