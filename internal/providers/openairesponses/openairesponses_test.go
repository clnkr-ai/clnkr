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
						{"type": "output_text", "text": ` back","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":null}}`},
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

	if got := mustCanonicalTurn(t, resp.Turn); got != `{"type":"done","summary":"hello back","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}` {
		t.Fatalf("canonical turn = %q, want %q", got, `{"type":"done","summary":"hello back","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`)
	}
	if resp.ProtocolErr != nil {
		t.Fatalf("ProtocolErr = %v, want nil", resp.ProtocolErr)
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v, want 11/7", resp.Usage)
	}
}

func TestModelQueryRetriesTransientServerError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"message": "context deadline exceeded"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{
				{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{
						{"type": "output_text", "text": `{"turn":{"type":"done","summary":"ok","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":null}}`},
					},
				},
			},
			"usage": map[string]any{"input_tokens": 3, "output_tokens": 4},
		})
	}))
	defer server.Close()

	model := openairesponses.NewModel(server.URL, "test-key", "gpt-test", "system")
	resp, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "finish"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if got := mustCanonicalTurn(t, resp.Turn); got != `{"type":"done","summary":"ok","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}` {
		t.Fatalf("canonical turn = %q, want done", got)
	}
}

func TestModelQueryDoesNotRetryBadRequest(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "bad request"},
		})
	}))
	defer server.Close()

	model := openairesponses.NewModel(server.URL, "test-key", "gpt-test", "system")
	_, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "finish"}})
	if err == nil {
		t.Fatal("Query succeeded, want bad request error")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if got, want := err.Error(), "api error (status 400): bad request"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestModelQueryJoinsBaseURLPath(t *testing.T) {
	tests := []struct {
		name            string
		baseURLSuffix   string
		wantPath        string
		wantRawQuery    string
		wantEscapedPath string
	}{
		{
			name:          "trailing slash",
			baseURLSuffix: "/v1/",
			wantPath:      "/v1/responses",
		},
		{
			name:          "preserves internal repeated slashes",
			baseURLSuffix: "/proxy//v1/",
			wantPath:      "/proxy//v1/responses",
		},
		{
			name:          "keeps query on base URL",
			baseURLSuffix: "/v1/?token=abc",
			wantPath:      "/v1/responses",
			wantRawQuery:  "token=abc",
		},
		{
			name:            "preserves escaped slash",
			baseURLSuffix:   "/proxy%2Fv1/",
			wantPath:        "/proxy/v1/responses",
			wantEscapedPath: "/proxy%2Fv1/responses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			var gotRawQuery string
			var gotEscapedPath string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotRawQuery = r.URL.RawQuery
				gotEscapedPath = r.URL.EscapedPath()

				_ = json.NewEncoder(w).Encode(map[string]any{
					"output": []map[string]any{
						{
							"type": "message",
							"role": "assistant",
							"content": []map[string]any{
								{"type": "output_text", "text": `{"turn":{"type":"done","summary":"ok","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":null}}`},
							},
						},
					},
				})
			}))
			defer server.Close()

			model := openairesponses.NewModel(server.URL+tt.baseURLSuffix, "test-key", "gpt-5", "sys prompt")
			_, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}})
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if gotPath != tt.wantPath {
				t.Fatalf("path = %q, want %q", gotPath, tt.wantPath)
			}
			if gotRawQuery != tt.wantRawQuery {
				t.Fatalf("raw query = %q, want %q", gotRawQuery, tt.wantRawQuery)
			}
			if tt.wantEscapedPath != "" && gotEscapedPath != tt.wantEscapedPath {
				t.Fatalf("escaped path = %q, want %q", gotEscapedPath, tt.wantEscapedPath)
			}
		})
	}
}

func TestModelQuerySerializesProviderRequestOptions(t *testing.T) {
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
						{"type": "output_text", "text": `{"turn":{"type":"done","summary":"ok","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":null}}`},
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
		if got := item["text"]; got != `{"turn":{"type":"done","bash":null,"question":null,"summary":"canonical transcript","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":null}}` {
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
						{"type": "output_text", "text": `{"turn":{"type":"done","summary":"ok","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":null}}`},
					},
				},
			},
		})
	}))
	defer server.Close()

	history := []clnkr.Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"canonical transcript","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`},
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
		{Role: "assistant", Content: `{"type":"done","summary":"canonical transcript","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`},
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
	if got, want := item["text"], `{"turn":{"type":"done","bash":null,"question":null,"summary":"canonical transcript","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":null}}`; got != want {
		t.Fatalf("assistant text = %#v, want %q", got, want)
	}
	annotations, ok := item["annotations"].([]any)
	if !ok || len(annotations) != 0 {
		t.Fatalf("assistant annotations = %#v, want empty array", item["annotations"])
	}
}

func TestModelQueryTextRetriesTransientServerError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"message": "context deadline exceeded"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{
				{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{
						{"type": "output_text", "text": "Older work summarized after retry."},
					},
				},
			},
			"usage": map[string]any{"input_tokens": 2, "output_tokens": 3},
		})
	}))
	defer server.Close()

	model := openairesponses.NewModel(server.URL, "test-key", "gpt-5", "sys prompt")
	summary, err := model.QueryText(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatalf("QueryText: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if summary != "Older work summarized after retry." {
		t.Fatalf("summary = %q, want retry summary", summary)
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

func TestModelQueryToolCalls(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{
				{"type": "reasoning", "id": "rs_1", "summary": []any{}},
				{"type": "function_call", "call_id": "call_1", "name": "bash", "arguments": `{"command":"pwd","workdir":null}`, "status": "completed"},
				{"type": "function_call", "call_id": "call_2", "name": "bash", "arguments": `{"command":"git status","workdir":null}`, "status": "completed"},
			},
			"usage": map[string]any{"input_tokens": 3, "output_tokens": 4},
		})
	}))
	defer server.Close()

	model := openairesponses.NewModelWithOptions(server.URL, "test-key", "gpt-5", "sys prompt", openairesponses.Options{UseBashToolCalls: true})
	resp, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "where"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if _, ok := gotBody["parallel_tool_calls"]; ok {
		t.Fatalf("parallel_tool_calls = %#v, want omitted", gotBody["parallel_tool_calls"])
	}
	include, ok := gotBody["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v, want reasoning.encrypted_content", gotBody["include"])
	}
	tools, ok := gotBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one bash tool", gotBody["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "bash" || tool["strict"] != true {
		t.Fatalf("tool = %#v, want strict bash function", tool)
	}
	params := tool["parameters"].(map[string]any)
	if params["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v, want false", params["additionalProperties"])
	}
	required := params["required"].([]any)
	if len(required) != 2 || required[0] != "command" || required[1] != "workdir" {
		t.Fatalf("required = %#v, want command/workdir", required)
	}

	act, ok := resp.Turn.(*clnkr.ActTurn)
	if !ok {
		t.Fatalf("Turn = %T, want *ActTurn", resp.Turn)
	}
	if got := len(act.Bash.Commands); got != 2 {
		t.Fatalf("commands = %d, want 2", got)
	}
	if got := act.Bash.Commands[0]; got.ID != "call_1" || got.Command != "pwd" {
		t.Fatalf("first command = %#v, want provider ID and command", got)
	}
	if got := act.Bash.Commands[1]; got.ID != "call_2" || got.Command != "git status" {
		t.Fatalf("second command = %#v, want provider ID and command", got)
	}
	if len(resp.BashToolCalls) != 2 || resp.BashToolCalls[0].ID != "call_1" || resp.BashToolCalls[1].ID != "call_2" {
		t.Fatalf("BashToolCalls = %#v", resp.BashToolCalls)
	}
	if len(resp.ProviderReplay) != 1 || resp.ProviderReplay[0].Type != "reasoning" {
		t.Fatalf("ProviderReplay = %#v", resp.ProviderReplay)
	}
}

func TestModelQueryToolCallsReplayToolMessagesWithoutDuplicateText(t *testing.T) {
	var gotInput []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		gotInput = body["input"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{{
				"type": "message", "role": "assistant",
				"content": []map[string]any{{"type": "output_text", "text": `{"turn":{"type":"done","summary":"ok","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":null}}`}},
			}},
		})
	}))
	defer server.Close()

	model := openairesponses.NewModelWithOptions(server.URL, "test-key", "gpt-5", "sys prompt", openairesponses.Options{UseBashToolCalls: true})
	_, err := model.Query(context.Background(), []clnkr.Message{
		{Role: "assistant", Content: `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]}}`, BashToolCalls: []clnkr.BashToolCall{{ID: "call_1", Command: "pwd"}}, ProviderReplay: []clnkr.ProviderReplayItem{{
			Provider: "openai", ProviderAPI: "openai-responses", Type: "reasoning", JSON: json.RawMessage(`{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque"}`),
		}}},
		{Role: "user", Content: "payload", BashToolResult: &clnkr.BashToolResult{ID: "call_1", Content: "payload"}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(gotInput) != 3 {
		t.Fatalf("input = %#v, want reasoning, function call, and output only", gotInput)
	}
	first := gotInput[0].(map[string]any)
	second := gotInput[1].(map[string]any)
	third := gotInput[2].(map[string]any)
	if first["type"] != "reasoning" || first["id"] != "rs_1" {
		t.Fatalf("first input = %#v, want opaque reasoning replay", first)
	}
	if first["encrypted_content"] != "opaque" {
		t.Fatalf("first input encrypted_content = %#v, want opaque", first["encrypted_content"])
	}
	if second["type"] != "function_call" || second["call_id"] != "call_1" {
		t.Fatalf("second input = %#v, want function_call", second)
	}
	if third["type"] != "function_call_output" || third["output"] != "payload" {
		t.Fatalf("third input = %#v, want function_call_output", third)
	}
}

func TestModelQueryToolCallsSkipsReasoningReplayWithoutEncryptedContent(t *testing.T) {
	var gotInput []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		gotInput = body["input"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{{
				"type": "message", "role": "assistant",
				"content": []map[string]any{{"type": "output_text", "text": `{"turn":{"type":"done","summary":"ok","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":null}}`}},
			}},
		})
	}))
	defer server.Close()

	model := openairesponses.NewModelWithOptions(server.URL, "test-key", "gpt-5", "sys prompt", openairesponses.Options{UseBashToolCalls: true})
	_, err := model.Query(context.Background(), []clnkr.Message{
		{Role: "assistant", Content: `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]}}`, BashToolCalls: []clnkr.BashToolCall{{ID: "call_1", Command: "pwd"}}, ProviderReplay: []clnkr.ProviderReplayItem{{
			Provider: "openai", ProviderAPI: "openai-responses", Type: "reasoning", JSON: json.RawMessage(`{"type":"reasoning","id":"rs_1","summary":[]}`),
		}}},
		{Role: "user", Content: "payload", BashToolResult: &clnkr.BashToolResult{ID: "call_1", Content: "payload"}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(gotInput) != 2 {
		t.Fatalf("input = %#v, want function call and output only", gotInput)
	}
	first := gotInput[0].(map[string]any)
	second := gotInput[1].(map[string]any)
	if first["type"] != "function_call" || first["call_id"] != "call_1" {
		t.Fatalf("first input = %#v, want function_call", first)
	}
	if second["type"] != "function_call_output" || second["output"] != "payload" {
		t.Fatalf("second input = %#v, want function_call_output", second)
	}
}

func TestModelQueryFinalOmitsBashTool(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{{
				"type": "message", "role": "assistant",
				"content": []map[string]any{{"type": "output_text", "text": `{"turn":{"type":"done","summary":"final","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":null}}`}},
			}},
		})
	}))
	defer server.Close()

	model := openairesponses.NewModelWithOptions(server.URL, "test-key", "gpt-5", "sys prompt", openairesponses.Options{UseBashToolCalls: true})
	if _, err := model.QueryFinal(context.Background(), []clnkr.Message{{Role: "user", Content: "summarize"}}); err != nil {
		t.Fatalf("QueryFinal: %v", err)
	}
	if _, ok := gotBody["tools"]; ok {
		t.Fatalf("tools = %#v, want omitted", gotBody["tools"])
	}
	schema := gotBody["text"].(map[string]any)["format"].(map[string]any)["schema"].(map[string]any)
	choices := schema["properties"].(map[string]any)["turn"].(map[string]any)["anyOf"].([]any)
	if len(choices) != 1 {
		t.Fatalf("final schema choices = %#v, want done only", choices)
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
