package openairesponses_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/providers/openairesponses"
)

func TestModelQueryUsesResponsesStructuredRequest(t *testing.T) {
	var gotBody map[string]any
	var gotPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path

		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{
				{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{
						{"type": "output_text", "text": `{"turn":{"type":"done","summary":"hello`},
						{"type": "output_text", "text": ` back","reasoning":null}}`},
					},
				},
			},
			"usage": map[string]any{
				"input_tokens":  11,
				"output_tokens": 7,
			},
		})
	}))
	defer server.Close()

	model := openairesponses.NewModel(server.URL+"/v1", "test-key", "gpt-5", "sys prompt")
	resp, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if gotPath != "/v1/responses" {
		t.Fatalf("path = %q, want %q", gotPath, "/v1/responses")
	}
	if got, want := gotBody["instructions"], "sys prompt"; got != want {
		t.Fatalf("instructions = %#v, want %q", got, want)
	}

	input, ok := gotBody["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("input = %#v, want single message", gotBody["input"])
	}
	msg, ok := input[0].(map[string]any)
	if !ok {
		t.Fatalf("input[0] = %#v, want map", input[0])
	}
	if got, want := msg["role"], "user"; got != want {
		t.Fatalf("input[0].role = %#v, want %q", got, want)
	}

	content, ok := msg["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("input[0].content = %#v, want one text item", msg["content"])
	}
	textItem, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("input[0].content[0] = %#v, want map", content[0])
	}
	if got, want := textItem["type"], "input_text"; got != want {
		t.Fatalf("input text type = %#v, want %q", got, want)
	}
	if got, want := textItem["text"], "hello"; got != want {
		t.Fatalf("input text = %#v, want %q", got, want)
	}

	text, ok := gotBody["text"].(map[string]any)
	if !ok {
		t.Fatalf("text = %#v, want map", gotBody["text"])
	}
	format, ok := text["format"].(map[string]any)
	if !ok {
		t.Fatalf("text.format = %#v, want map", text["format"])
	}
	if got, want := format["type"], "json_schema"; got != want {
		t.Fatalf("text.format.type = %#v, want %q", got, want)
	}
	if got, want := format["name"], "agent_turn"; got != want {
		t.Fatalf("text.format.name = %#v, want %q", got, want)
	}
	if got, want := format["strict"], true; got != want {
		t.Fatalf("text.format.strict = %#v, want %v", got, want)
	}
	if _, ok := format["schema"].(map[string]any); !ok {
		t.Fatalf("text.format.schema = %#v, want schema object", format["schema"])
	}

	if got := mustCanonicalTurn(t, resp.Turn); got != `{"type":"done","summary":"hello back"}` {
		t.Fatalf("canonical turn = %q, want %q", got, `{"type":"done","summary":"hello back"}`)
	}
	if resp.ProtocolErr != nil {
		t.Fatalf("ProtocolErr = %v, want nil", resp.ProtocolErr)
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v, want 11/7", resp.Usage)
	}
}

func TestModelQueryTextOmitsStructuredOutputConfigAndNormalizesAssistantHistory(t *testing.T) {
	var gotBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{
				{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{
						{"type": "output_text", "text": "Older work summarized."},
					},
				},
			},
			"usage": map[string]any{
				"input_tokens":  9,
				"output_tokens": 4,
			},
		})
	}))
	defer server.Close()

	history := []clnkr.Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"canonical transcript"}`},
	}

	model := openairesponses.NewModel(server.URL, "test-key", "gpt-5", "sys prompt")
	summary, err := model.QueryText(context.Background(), history)
	if err != nil {
		t.Fatalf("QueryText: %v", err)
	}
	if summary != "Older work summarized." {
		t.Fatalf("summary = %q, want %q", summary, "Older work summarized.")
	}
	if _, ok := gotBody["text"]; ok {
		t.Fatalf("text should be omitted for QueryText, got %#v", gotBody["text"])
	}

	input, ok := gotBody["input"].([]any)
	if !ok || len(input) != 2 {
		t.Fatalf("input = %#v, want 2 messages", gotBody["input"])
	}
	last, ok := input[1].(map[string]any)
	if !ok {
		t.Fatalf("input[1] = %#v, want map", input[1])
	}
	if got, want := last["role"], "assistant"; got != want {
		t.Fatalf("input[1].role = %#v, want %q", got, want)
	}
	content, ok := last["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("input[1].content = %#v, want one text item", last["content"])
	}
	item, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("input[1].content[0] = %#v, want map", content[0])
	}
	if got, want := item["type"], "output_text"; got != want {
		t.Fatalf("assistant content type = %#v, want %q", got, want)
	}
	if got, want := item["text"], `{"turn":{"type":"done","bash":null,"question":null,"summary":"canonical transcript","reasoning":null}}`; got != want {
		t.Fatalf("assistant text = %#v, want %q", got, want)
	}
}

func TestModelQueryReturnsRefusalError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{
				{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{
						{"type": "refusal", "refusal": "refused"},
					},
				},
			},
		})
	}))
	defer server.Close()

	model := openairesponses.NewModel(server.URL, "test-key", "gpt-5", "sys prompt")
	_, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}})
	if err == nil {
		t.Fatal("expected refusal error")
	}
	if !strings.Contains(err.Error(), "structured output refusal: refused") {
		t.Fatalf("error = %q, want structured refusal", err.Error())
	}
}

func TestModelQueryTextReturnsRefusalError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{
				{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{
						{"type": "refusal", "refusal": "refused"},
					},
				},
			},
		})
	}))
	defer server.Close()

	model := openairesponses.NewModel(server.URL, "test-key", "gpt-5", "sys prompt")
	_, err := model.QueryText(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}})
	if err == nil {
		t.Fatal("expected refusal error")
	}
	if !strings.Contains(err.Error(), "free-form refusal: refused") {
		t.Fatalf("error = %q, want free-form refusal", err.Error())
	}
}

func TestModelQueryErrorsOnMissingOutputText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{
				{
					"type":    "message",
					"role":    "assistant",
					"content": []map[string]any{},
				},
			},
		})
	}))
	defer server.Close()

	model := openairesponses.NewModel(server.URL, "test-key", "gpt-5", "sys prompt")
	_, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}})
	if err == nil {
		t.Fatal("expected missing output_text error")
	}
	if err.Error() != "no usable output_text in response" {
		t.Fatalf("error = %q, want %q", err.Error(), "no usable output_text in response")
	}
}

func mustCanonicalTurn(t *testing.T, turn clnkr.Turn) string {
	t.Helper()

	raw, err := clnkr.CanonicalTurnJSON(turn)
	if err != nil {
		t.Fatalf("CanonicalTurnJSON: %v", err)
	}
	return raw
}
