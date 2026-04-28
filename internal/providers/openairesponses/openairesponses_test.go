package openairesponses_test

import (
	"context"
	"encoding/json"
	"errors"
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
	if _, ok := gotBody["reasoning"]; ok {
		t.Fatalf("reasoning = %#v, want omitted by default", gotBody["reasoning"])
	}
	if _, ok := gotBody["max_output_tokens"]; ok {
		t.Fatalf("max_output_tokens = %#v, want omitted by default", gotBody["max_output_tokens"])
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
	if got, want := msg["type"], "message"; got != want {
		t.Fatalf("input[0].type = %#v, want %q", got, want)
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
	if _, ok := textItem["annotations"]; ok {
		t.Fatalf("input text annotations = %#v, want omitted", textItem["annotations"])
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

func TestModelQuerySerializesHarnessOptions(t *testing.T) {
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
						{"type": "output_text", "text": `{"turn":{"type":"done","summary":"ok","reasoning":null}}`},
					},
				},
			},
		})
	}))
	defer server.Close()

	model := openairesponses.NewModelWithOptions(server.URL, "test-key", "gpt-5.1", "sys", openairesponses.Options{
		ReasoningEffort:    "high",
		MaxOutputTokens:    8000,
		HasMaxOutputTokens: true,
	})
	_, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	reasoning, ok := gotBody["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning = %#v, want object", gotBody["reasoning"])
	}
	if got, want := reasoning["effort"], "high"; got != want {
		t.Fatalf("reasoning.effort = %#v, want %q", got, want)
	}
	if got, want := gotBody["max_output_tokens"], float64(8000); got != want {
		t.Fatalf("max_output_tokens = %#v, want %v", got, want)
	}
}

func TestModelQueryRejectsContradictoryStructuredTurn(t *testing.T) {
	content := `{"turn":{"type":"done","bash":{"commands":[{"command":"rm -rf tmp","workdir":null}]},"question":null,"summary":"done","reasoning":null}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{
				{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{
						{"type": "output_text", "text": content},
					},
				},
			},
			"usage": map[string]int{"input_tokens": 11, "output_tokens": 7},
		})
	}))
	defer server.Close()

	model := openairesponses.NewModel(server.URL, "test-key", "gpt-test", "sys")
	resp, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if resp.Raw != content {
		t.Fatalf("Raw = %q, want %q", resp.Raw, content)
	}
	if !errors.Is(resp.ProtocolErr, clnkr.ErrInvalidJSON) {
		t.Fatalf("ProtocolErr = %v, want ErrInvalidJSON", resp.ProtocolErr)
	}
}

func TestModelQuerySendsAssistantHistoryAsResponsesOutputItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var gotBody map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)

		input, ok := gotBody["input"].([]any)
		if !ok || len(input) != 2 {
			http.Error(w, "input must have two messages", http.StatusBadRequest)
			return
		}

		user, ok := input[0].(map[string]any)
		if !ok {
			http.Error(w, "user history must be object", http.StatusBadRequest)
			return
		}
		if got := user["type"]; got != "message" {
			http.Error(w, "user history missing type message", http.StatusBadRequest)
			return
		}
		userContent, ok := user["content"].([]any)
		if !ok || len(userContent) != 1 {
			http.Error(w, "user history content must have one item", http.StatusBadRequest)
			return
		}
		userText, ok := userContent[0].(map[string]any)
		if !ok {
			http.Error(w, "user history content must be object", http.StatusBadRequest)
			return
		}
		if got := userText["type"]; got != "input_text" {
			http.Error(w, "user history must use input_text", http.StatusBadRequest)
			return
		}
		if _, ok := userText["annotations"]; ok {
			http.Error(w, "user history must omit annotations", http.StatusBadRequest)
			return
		}

		assistant, ok := input[1].(map[string]any)
		if !ok {
			http.Error(w, "assistant history must be object", http.StatusBadRequest)
			return
		}
		if got := assistant["type"]; got != "message" {
			http.Error(w, "assistant history missing type message", http.StatusBadRequest)
			return
		}
		if got := assistant["id"]; got != "msg_prev_1" {
			http.Error(w, "assistant history missing id", http.StatusBadRequest)
			return
		}
		if got := assistant["role"]; got != "assistant" {
			http.Error(w, "assistant history missing assistant role", http.StatusBadRequest)
			return
		}
		if got := assistant["status"]; got != "completed" {
			http.Error(w, "assistant history missing completed status", http.StatusBadRequest)
			return
		}
		content, ok := assistant["content"].([]any)
		if !ok || len(content) != 1 {
			http.Error(w, "assistant history content must have one item", http.StatusBadRequest)
			return
		}
		item, ok := content[0].(map[string]any)
		if !ok {
			http.Error(w, "assistant history content must be object", http.StatusBadRequest)
			return
		}
		if got := item["type"]; got != "output_text" {
			http.Error(w, "assistant history must use output_text", http.StatusBadRequest)
			return
		}
		if got := item["text"]; got != `{"turn":{"type":"done","bash":null,"question":null,"summary":"canonical transcript","reasoning":null}}` {
			http.Error(w, "assistant history text not normalized", http.StatusBadRequest)
			return
		}
		annotations, ok := item["annotations"].([]any)
		if !ok || len(annotations) != 0 {
			http.Error(w, "assistant history annotations must be empty array", http.StatusBadRequest)
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{
				{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{
						{"type": "output_text", "text": `{"turn":{"type":"done","summary":"ok","reasoning":null}}`},
					},
				},
			},
		})
	}))
	defer server.Close()

	history := []clnkr.Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"canonical transcript"}`},
	}

	model := openairesponses.NewModel(server.URL, "test-key", "gpt-5", "sys prompt")
	_, err := model.Query(context.Background(), history)
	if err != nil {
		t.Fatalf("Query: %v", err)
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
	if _, ok := gotBody["reasoning"]; ok {
		t.Fatalf("reasoning should be omitted by default for QueryText, got %#v", gotBody["reasoning"])
	}
	if _, ok := gotBody["max_output_tokens"]; ok {
		t.Fatalf("max_output_tokens should be omitted by default for QueryText, got %#v", gotBody["max_output_tokens"])
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
	if got, want := last["type"], "message"; got != want {
		t.Fatalf("input[1].type = %#v, want %q", got, want)
	}
	if got, want := last["id"], "msg_prev_1"; got != want {
		t.Fatalf("input[1].id = %#v, want %q", got, want)
	}
	if got, want := last["status"], "completed"; got != want {
		t.Fatalf("input[1].status = %#v, want %q", got, want)
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
	annotations, ok := item["annotations"].([]any)
	if !ok || len(annotations) != 0 {
		t.Fatalf("assistant annotations = %#v, want empty array", item["annotations"])
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
