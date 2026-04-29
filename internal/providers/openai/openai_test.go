package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/providers/openai"
)

func TestModel(t *testing.T) {
	t.Run("uses structured output request body and prepends system message", func(t *testing.T) {
		var gotBody map[string]interface{}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "content": openAIWrappedDone("ok")}},
				},
				"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 20},
			})
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys prompt")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		msgs := gotBody["messages"].([]interface{})
		first := msgs[0].(map[string]interface{})
		if first["role"] != "system" || first["content"] != "sys prompt" {
			t.Errorf("first message should be system prompt, got %v", first)
		}
		responseFormat, ok := gotBody["response_format"].(map[string]interface{})
		if !ok {
			t.Fatalf("response_format = %T, want map[string]interface{}", gotBody["response_format"])
		}
		if responseFormat["type"] != "json_schema" {
			t.Fatalf("response_format.type = %v, want json_schema", responseFormat["type"])
		}
		jsonSchema, ok := responseFormat["json_schema"].(map[string]interface{})
		if !ok {
			t.Fatalf("response_format.json_schema = %T, want map[string]interface{}", responseFormat["json_schema"])
		}
		if jsonSchema["strict"] != true {
			t.Fatalf("response_format.json_schema.strict = %v, want true", jsonSchema["strict"])
		}
		schema, ok := jsonSchema["schema"].(map[string]interface{})
		if !ok {
			t.Fatalf("response_format.json_schema.schema = %T, want map[string]interface{}", jsonSchema["schema"])
		}
		if schemaContainsKey(schema, "maxItems") {
			t.Fatal("response_format.json_schema.schema unexpectedly includes maxItems")
		}
		if !schemaContainsKey(schema, "minItems") {
			t.Fatal("response_format.json_schema.schema unexpectedly omits minItems")
		}
		assertSchemaShape(t, schema)
	})

	t.Run("QueryText returns plain text without response format", func(t *testing.T) {
		var gotBody map[string]any
		history := []clnkr.Message{
			{Role: "user", Content: "first task"},
			{Role: "assistant", Content: `{"type":"done","summary":"canonical transcript"}`},
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "content": "Older work summarized."}},
				},
				"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 20},
			})
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys prompt")
		summary, err := m.QueryText(context.Background(), history)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if summary != "Older work summarized." {
			t.Fatalf("summary = %q, want %q", summary, "Older work summarized.")
		}
		if _, ok := gotBody["response_format"]; ok {
			t.Fatalf("response_format should be omitted for QueryText, got %#v", gotBody["response_format"])
		}
		msgs, ok := gotBody["messages"].([]any)
		if !ok || len(msgs) != len(history)+1 {
			t.Fatalf("messages = %#v, want system plus %d transcript messages", gotBody["messages"], len(history))
		}
		last, ok := msgs[len(msgs)-1].(map[string]any)
		if !ok {
			t.Fatalf("last message = %#v, want map", msgs[len(msgs)-1])
		}
		if got, want := last["content"], `{"type":"done","summary":"canonical transcript"}`; got != want {
			t.Fatalf("last assistant content = %#v, want %q", got, want)
		}
	})

	t.Run("returns canonical json text", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{
						"role":    "assistant",
						"content": openAIWrappedDone("hello back"),
					}},
				},
				"usage": map[string]int{"prompt_tokens": 15, "completion_tokens": 25},
			})
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
		resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := mustCanonicalTurn(t, resp.Turn); got != `{"type":"done","summary":"hello back"}` {
			t.Errorf("got %q, want %q", got, `{"type":"done","summary":"hello back"}`)
		}
		if resp.ProtocolErr != nil {
			t.Fatalf("got protocol error %v, want nil", resp.ProtocolErr)
		}
		if resp.Usage.InputTokens != 15 || resp.Usage.OutputTokens != 25 {
			t.Errorf("got usage %+v, want 15/25", resp.Usage)
		}
	})

	t.Run("normalizes assistant history to wrapped provider json", func(t *testing.T) {
		var gotBody map[string]interface{}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{
						"role":    "assistant",
						"content": openAIWrappedDone("ok"),
					}},
				},
				"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1},
			})
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: `{"type":"done","summary":"hello back"}`},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		msgs := gotBody["messages"].([]interface{})
		last := msgs[len(msgs)-1].(map[string]interface{})
		if got, want := last["content"], openAIWrappedDone("hello back"); got != want {
			t.Fatalf("assistant history content = %q, want %q", got, want)
		}
	})

	t.Run("posts to base URL plus chat/completions", func(t *testing.T) {
		var gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "content": openAIWrappedDone("ok")}},
				},
				"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1},
			})
		}))
		defer server.Close()

		m := openai.NewModel(server.URL+"/v1beta/openai", "test-key", "gemini-2.0-flash", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotPath != "/v1beta/openai/chat/completions" {
			t.Errorf("got path %q, want %q", gotPath, "/v1beta/openai/chat/completions")
		}
	})

	t.Run("joins trailing slash base URL", func(t *testing.T) {
		var gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "content": openAIWrappedDone("ok")}},
				},
				"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1},
			})
		}))
		defer server.Close()

		m := openai.NewModel(server.URL+"/v1/", "test-key", "gpt-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotPath != "/v1/chat/completions" {
			t.Errorf("got path %q, want %q", gotPath, "/v1/chat/completions")
		}
	})

	t.Run("extracts error message from JSON error response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]interface{}{
					"code":    429,
					"message": "Rate limit exceeded",
				},
			})
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Fatal("expected error on 429")
		}
		want := "api error (status 429): Rate limit exceeded"
		if err.Error() != want {
			t.Errorf("got %q, want error containing %q", err.Error(), want)
		}
	})

	t.Run("retries rate limit response and succeeds", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			if attempts == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]interface{}{
						"code":    429,
						"message": "Rate limit exceeded",
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{
						"role":    "assistant",
						"content": openAIWrappedDone("ok after retry"),
					}},
				},
				"usage": map[string]int{"prompt_tokens": 2, "completion_tokens": 3},
			})
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
		resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if attempts != 2 {
			t.Fatalf("attempt count = %d, want 2", attempts)
		}
		if got := mustCanonicalTurn(t, resp.Turn); got != `{"type":"done","summary":"ok after retry"}` {
			t.Fatalf("content = %q, want %q", got, `{"type":"done","summary":"ok after retry"}`)
		}
	})

	t.Run("fails closed on unsupported structured output backend", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]interface{}{
					"message": "response_format json_schema is not supported",
				},
			})
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Fatal("expected unsupported feature error")
		}
		if attempts != 1 {
			t.Fatalf("attempt count = %d, want 1", attempts)
		}
	})

	t.Run("extracts error message from array-wrapped response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`[{"error":{"code":429,"message":"Quota exceeded"}}]`))
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Fatal("expected error on 429")
		}
		want := "api error (status 429): Quota exceeded"
		if err.Error() != want {
			t.Errorf("got %q, want %q", err.Error(), want)
		}
	})

	t.Run("stops after max attempts on repeated rate limits", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]interface{}{
					"code":    429,
					"message": "Rate limit exceeded",
				},
			})
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Fatal("expected error on repeated 429")
		}
		if attempts != 5 {
			t.Fatalf("attempt count = %d, want 5", attempts)
		}
		want := "api error (status 429): Rate limit exceeded"
		if err.Error() != want {
			t.Fatalf("got %q, want %q", err.Error(), want)
		}
	})

	t.Run("falls back to raw body for non-JSON errors", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("Bad Gateway"))
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Fatal("expected error on 502")
		}
		want := "api error (status 502): Bad Gateway"
		if err.Error() != want {
			t.Errorf("got %q, want error containing %q", err.Error(), want)
		}
		if attempts != 1 {
			t.Fatalf("attempt count = %d, want 1", attempts)
		}
	})

	t.Run("cancellation stops rate limit backoff", func(t *testing.T) {
		attempts := 0
		firstAttempt := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			if attempts == 1 {
				close(firstAttempt)
				w.Header().Set("Retry-After", "10")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]interface{}{
						"code":    429,
						"message": "Rate limit exceeded",
					},
				})
				return
			}
			t.Fatalf("unexpected retry after cancellation")
		}))
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			<-firstAttempt
			cancel()
		}()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
		_, err := m.Query(ctx, []clnkr.Message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Fatal("expected cancellation error")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
		if attempts != 1 {
			t.Fatalf("attempt count = %d, want 1", attempts)
		}
	})

	t.Run("returns error on empty choices", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []interface{}{},
				"usage":   map[string]int{"prompt_tokens": 0, "completion_tokens": 0},
			})
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Error("expected error on empty choices")
		}
	})

	t.Run("fails closed on empty choice content", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "content": ""}},
				},
				"usage": map[string]int{"prompt_tokens": 0, "completion_tokens": 0},
			})
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Fatal("expected empty content error")
		}
	})

	t.Run("returns raw payload plus protocol error on invalid structured payload", func(t *testing.T) {
		tests := []struct {
			name    string
			content string
			wantErr error
		}{
			{name: "single-command wrapped act turn", content: `{"turn":{"type":"act","bash":{"command":"pwd","workdir":null},"question":null,"summary":null,"reasoning":null}}`, wantErr: clnkr.ErrInvalidJSON},
			{name: "semantic invalid act turn", content: `{"turn":{"type":"act","bash":{"commands":[{"command":"","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`, wantErr: clnkr.ErrMissingCommand},
			{name: "done turn with command sibling", content: `{"turn":{"type":"done","bash":{"commands":[{"command":"rm -rf tmp","workdir":null}]},"question":null,"summary":"done","reasoning":null}}`, wantErr: clnkr.ErrInvalidJSON},
			{name: "clarify turn with summary sibling", content: `{"turn":{"type":"clarify","bash":null,"question":"Which repo?","summary":"done","reasoning":null}}`, wantErr: clnkr.ErrInvalidJSON},
			{name: "multiple wrapped objects", content: `{"turn":{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]},"question":null,"summary":null,"reasoning":null}}{"turn":{"type":"done","bash":null,"question":null,"summary":"done","reasoning":null}}`, wantErr: clnkr.ErrInvalidJSON},
			{name: "prose wrapped json", content: "Here is the result:\n{\"turn\":{\"type\":\"done\",\"summary\":\"wrapped\"}}", wantErr: clnkr.ErrInvalidJSON},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"choices": []map[string]interface{}{
							{"message": map[string]string{"role": "assistant", "content": tt.content}},
						},
						"usage": map[string]int{"prompt_tokens": 0, "completion_tokens": 0},
					})
				}))
				defer server.Close()

				m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
				resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if resp.Raw != tt.content {
					t.Fatalf("raw = %q, want %q", resp.Raw, tt.content)
				}
				if !errors.Is(resp.ProtocolErr, tt.wantErr) {
					t.Fatalf("protocol error = %v, want %v", resp.ProtocolErr, tt.wantErr)
				}
			})
		}
	})

	t.Run("canonicalizes wrapped clarify and done payloads without null siblings", func(t *testing.T) {
		tests := []struct {
			name    string
			content string
			want    string
		}{
			{name: "clarify", content: `{"turn":{"type":"clarify","question":"Which directory?"}}`, want: `{"type":"clarify","question":"Which directory?"}`},
			{name: "done", content: `{"turn":{"type":"done","summary":"ignored schema"}}`, want: `{"type":"done","summary":"ignored schema"}`},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"choices": []map[string]interface{}{
							{"message": map[string]string{"role": "assistant", "content": tt.content}},
						},
						"usage": map[string]int{"prompt_tokens": 0, "completion_tokens": 0},
					})
				}))
				defer server.Close()

				m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
				resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got := mustCanonicalTurn(t, resp.Turn); got != tt.want {
					t.Fatalf("content = %q, want %q", got, tt.want)
				}
				if resp.ProtocolErr != nil {
					t.Fatalf("protocol error = %v, want nil", resp.ProtocolErr)
				}
			})
		}
	})

	t.Run("returns raw payload plus protocol error when response format is ignored", func(t *testing.T) {
		raw := `{"type":"done","summary":"ignored"}`
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "content": raw}},
				},
				"usage": map[string]int{"prompt_tokens": 0, "completion_tokens": 0},
			})
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
		resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Raw != raw {
			t.Fatalf("raw = %q, want %q", resp.Raw, raw)
		}
		if !errors.Is(resp.ProtocolErr, clnkr.ErrInvalidJSON) {
			t.Fatalf("protocol error = %v, want ErrInvalidJSON", resp.ProtocolErr)
		}
	})

	t.Run("returns refusal as a distinct error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "refusal": "refused"}},
				},
				"usage": map[string]int{"prompt_tokens": 0, "completion_tokens": 0},
			})
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Fatal("expected refusal error")
		}
		if err.Error() == "empty choice content" {
			t.Fatal("expected refusal-specific error, got empty choice content")
		}
	})
}

func openAIWrappedDone(summary string) string {
	return `{"turn":{"type":"done","bash":null,"question":null,"summary":"` + summary + `","reasoning":null}}`
}

func schemaContainsKey(node any, key string) bool {
	switch v := node.(type) {
	case map[string]interface{}:
		if _, ok := v[key]; ok {
			return true
		}
		for _, child := range v {
			if schemaContainsKey(child, key) {
				return true
			}
		}
	case []interface{}:
		for _, child := range v {
			if schemaContainsKey(child, key) {
				return true
			}
		}
	}

	return false
}

func assertSchemaShape(t *testing.T, schema map[string]interface{}) {
	t.Helper()

	if got := schema["type"]; got != "object" {
		t.Fatalf("schema type = %v, want object", got)
	}
	if got := schema["additionalProperties"]; got != false {
		t.Fatalf("schema additionalProperties = %v, want false", got)
	}
	if got, want := schema["required"], []string{"turn"}; !sameStringSlice(got, want) {
		t.Fatalf("schema required = %#v, want %#v", got, want)
	}

	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("schema properties = %T, want map[string]interface{}", schema["properties"])
	}
	turnProp, ok := properties["turn"].(map[string]interface{})
	if !ok {
		t.Fatalf("schema properties[turn] = %T, want map[string]interface{}", properties["turn"])
	}
	branches, ok := turnProp["anyOf"].([]interface{})
	if !ok {
		t.Fatalf("schema properties[turn].anyOf = %T, want []interface{}", turnProp["anyOf"])
	}
	if len(branches) != 3 {
		t.Fatalf("len(schema properties[turn].anyOf) = %d, want 3", len(branches))
	}

	for _, turnType := range []string{"act", "clarify", "done"} {
		branch := schemaBranchForType(t, branches, turnType)
		if got := branch["additionalProperties"]; got != false {
			t.Fatalf("%s branch additionalProperties = %v, want false", turnType, got)
		}
		if got, want := branch["required"], []string{"type", "bash", "question", "summary", "reasoning"}; !sameStringSlice(got, want) {
			t.Fatalf("%s branch required = %#v, want %#v", turnType, got, want)
		}
		branchProperties, ok := branch["properties"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s branch properties = %T, want map[string]interface{}", turnType, branch["properties"])
		}
		typeProp, ok := branchProperties["type"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s branch properties[type] = %T, want map[string]interface{}", turnType, branchProperties["type"])
		}
		if got := typeProp["type"]; got != "string" {
			t.Fatalf("%s branch properties[type].type = %v, want string", turnType, got)
		}
		if got := typeProp["const"]; got != turnType {
			t.Fatalf("%s branch properties[type].const = %v, want %q", turnType, got, turnType)
		}
		assertNullableStringUnion(t, branchProperties["reasoning"])
	}
}

func schemaBranchForType(t *testing.T, branches []interface{}, turnType string) map[string]interface{} {
	t.Helper()

	for _, branch := range branches {
		branchMap, ok := branch.(map[string]interface{})
		if !ok {
			continue
		}
		properties, ok := branchMap["properties"].(map[string]interface{})
		if !ok {
			continue
		}
		typeProp, ok := properties["type"].(map[string]interface{})
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

func assertNullableStringUnion(t *testing.T, raw any) {
	t.Helper()

	prop, ok := raw.(map[string]interface{})
	if !ok {
		t.Fatalf("nullable property = %T, want map[string]interface{}", raw)
	}
	branches, ok := prop["anyOf"].([]interface{})
	if !ok {
		t.Fatalf("nullable property anyOf = %T, want []interface{}", prop["anyOf"])
	}
	if len(branches) != 2 {
		t.Fatalf("len(nullable property anyOf) = %d, want 2", len(branches))
	}

	assertSchemaTypeBranch(t, branches, "string")
	assertSchemaTypeBranch(t, branches, "null")
}

func assertSchemaTypeBranch(t *testing.T, branches []interface{}, wantType string) {
	t.Helper()

	for _, branch := range branches {
		branchMap, ok := branch.(map[string]interface{})
		if !ok {
			continue
		}
		if branchMap["type"] == wantType {
			return
		}
	}

	t.Fatalf("missing anyOf branch with type %q", wantType)
}

func sameStringSlice(got any, want []string) bool {
	gotSlice, ok := got.([]interface{})
	if !ok || len(gotSlice) != len(want) {
		return false
	}
	for i := range gotSlice {
		gotString, ok := gotSlice[i].(string)
		if !ok || gotString != want[i] {
			return false
		}
	}
	return true
}

func mustCanonicalTurn(t *testing.T, turn clnkr.Turn) string {
	t.Helper()
	raw, err := clnkr.CanonicalTurnJSON(turn)
	if err != nil {
		t.Fatalf("CanonicalTurnJSON: %v", err)
	}
	return raw
}
