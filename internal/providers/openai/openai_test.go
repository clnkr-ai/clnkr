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
		var gotBody map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotBody = readRequestBody(t, r)
			writeChatResponse(t, w, openAIWrappedDone("ok"), 10, 20)
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys prompt")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		msgs := gotBody["messages"].([]any)
		first := msgs[0].(map[string]any)
		if first["role"] != "system" || first["content"] != "sys prompt" {
			t.Errorf("first message should be system prompt, got %v", first)
		}
		responseFormat, ok := gotBody["response_format"].(map[string]any)
		if !ok {
			t.Fatalf("response_format = %T, want map[string]any", gotBody["response_format"])
		}
		if responseFormat["type"] != "json_schema" {
			t.Fatalf("response_format.type = %v, want json_schema", responseFormat["type"])
		}
		jsonSchema, ok := responseFormat["json_schema"].(map[string]any)
		if !ok {
			t.Fatalf("response_format.json_schema = %T, want map[string]any", responseFormat["json_schema"])
		}
		if jsonSchema["strict"] != true {
			t.Fatalf("response_format.json_schema.strict = %v, want true", jsonSchema["strict"])
		}
		schema, ok := jsonSchema["schema"].(map[string]any)
		if !ok {
			t.Fatalf("response_format.json_schema.schema = %T, want map[string]any", jsonSchema["schema"])
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
			{Role: "assistant", Content: doneTurn("canonical transcript")},
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotBody = readRequestBody(t, r)
			writeChatResponse(t, w, "Older work summarized.", 10, 20)
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
		if got, want := last["content"], doneTurn("canonical transcript"); got != want {
			t.Fatalf("last assistant content = %#v, want %q", got, want)
		}
	})

	t.Run("QueryText retries transient server error and succeeds", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			if attempts == 1 {
				writeAPIError(t, w, http.StatusBadGateway, "context deadline exceeded")
				return
			}
			writeChatResponse(t, w, "ok after retry", 2, 3)
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
		summary, err := m.QueryText(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if attempts != 2 {
			t.Fatalf("attempt count = %d, want 2", attempts)
		}
		if summary != "ok after retry" {
			t.Fatalf("summary = %q, want ok after retry", summary)
		}
	})

	t.Run("returns canonical json text", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeChatResponse(t, w, openAIWrappedDone("hello back"), 15, 25)
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
		resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := mustCanonicalTurn(t, resp.Turn), doneTurn("hello back"); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
		if resp.ProtocolErr != nil {
			t.Fatalf("got protocol error %v, want nil", resp.ProtocolErr)
		}
		if resp.Usage.InputTokens != 15 || resp.Usage.OutputTokens != 25 {
			t.Errorf("got usage %+v, want 15/25", resp.Usage)
		}
	})

	t.Run("normalizes assistant history to wrapped provider json", func(t *testing.T) {
		var gotBody map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotBody = readRequestBody(t, r)
			writeChatResponse(t, w, openAIWrappedDone("ok"), 1, 1)
		}))
		defer server.Close()

		m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: doneTurn("hello back")},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		msgs := gotBody["messages"].([]any)
		last := msgs[len(msgs)-1].(map[string]any)
		if got, want := last["content"], openAIWrappedDone("hello back"); got != want {
			t.Fatalf("assistant history content = %q, want %q", got, want)
		}
	})

	t.Run("posts to joined chat completions endpoint", func(t *testing.T) {
		tests := []struct {
			name     string
			base     string
			wantPath string
		}{
			{name: "nested base URL", base: "/v1beta/openai", wantPath: "/v1beta/openai/chat/completions"},
			{name: "trailing slash base URL", base: "/v1/", wantPath: "/v1/chat/completions"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var gotPath string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotPath = r.URL.Path
					writeChatResponse(t, w, openAIWrappedDone("ok"), 1, 1)
				}))
				defer server.Close()

				m := openai.NewModel(server.URL+tt.base, "test-key", "gpt-test", "sys")
				_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if gotPath != tt.wantPath {
					t.Errorf("got path %q, want %q", gotPath, tt.wantPath)
				}
			})
		}
	})

	t.Run("extracts API error messages", func(t *testing.T) {
		tests := []struct {
			name string
			body string
			want string
		}{
			{name: "JSON error response", body: `{"error":{"code":429,"message":"Rate limit exceeded"}}`, want: "api error (status 429): Rate limit exceeded"},
			{name: "array-wrapped response", body: `[{"error":{"code":429,"message":"Quota exceeded"}}]`, want: "api error (status 429): Quota exceeded"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = w.Write([]byte(tt.body))
				}))
				defer server.Close()

				m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
				_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
				if err == nil {
					t.Fatal("expected error on 429")
				}
				if err.Error() != tt.want {
					t.Errorf("got %q, want %q", err.Error(), tt.want)
				}
			})
		}
	})

	t.Run("retries Query errors and succeeds", func(t *testing.T) {
		tests := []struct {
			name    string
			status  int
			message string
		}{
			{name: "rate limit response", status: http.StatusTooManyRequests, message: "Rate limit exceeded"},
			{name: "transient server error", status: http.StatusBadGateway, message: "context deadline exceeded"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				attempts := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					attempts++
					if attempts == 1 {
						writeAPIError(t, w, tt.status, tt.message)
						return
					}
					writeChatResponse(t, w, openAIWrappedDone("ok after retry"), 2, 3)
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
				if got, want := mustCanonicalTurn(t, resp.Turn), doneTurn("ok after retry"); got != want {
					t.Fatalf("content = %q, want %q", got, want)
				}
			})
		}
	})

	t.Run("fails closed on unsupported structured output backend", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			writeAPIError(t, w, http.StatusBadRequest, "response_format json_schema is not supported")
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

	t.Run("stops after max attempts on repeated retryable errors", func(t *testing.T) {
		tests := []struct {
			name   string
			status int
			body   string
			want   string
		}{
			{name: "rate limits", status: http.StatusTooManyRequests, body: `{"error":{"code":429,"message":"Rate limit exceeded"}}`, want: "api error (status 429): Rate limit exceeded"},
			{name: "non-JSON server errors", status: http.StatusBadGateway, body: "Bad Gateway", want: "api error (status 502): Bad Gateway"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				attempts := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					attempts++
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte(tt.body))
				}))
				defer server.Close()

				m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
				_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
				if err == nil {
					t.Fatal("expected retryable error")
				}
				if err.Error() != tt.want {
					t.Fatalf("got %q, want %q", err.Error(), tt.want)
				}
				if attempts != 5 {
					t.Fatalf("attempt count = %d, want 5", attempts)
				}
			})
		}
	})

	t.Run("cancellation stops rate limit backoff", func(t *testing.T) {
		attempts := 0
		firstAttempt := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			if attempts != 1 {
				t.Fatalf("unexpected retry after cancellation")
			}
			close(firstAttempt)
			w.Header().Set("Retry-After", "10")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"code": 429, "message": "Rate limit exceeded"},
			})
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

	t.Run("fails on unusable successful responses", func(t *testing.T) {
		tests := []struct {
			name      string
			response  map[string]any
			assertErr func(*testing.T, error)
		}{
			{
				name:     "empty choices",
				response: map[string]any{"choices": []any{}, "usage": map[string]int{"prompt_tokens": 0, "completion_tokens": 0}},
				assertErr: func(t *testing.T, err error) {
					if err == nil {
						t.Error("expected error on empty choices")
					}
				},
			},
			{
				name: "empty choice content",
				response: map[string]any{
					"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": ""}}},
					"usage":   map[string]int{"prompt_tokens": 0, "completion_tokens": 0},
				},
				assertErr: func(t *testing.T, err error) {
					if err == nil {
						t.Fatal("expected empty content error")
					}
				},
			},
			{
				name: "refusal",
				response: map[string]any{
					"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "refusal": "refused"}}},
					"usage":   map[string]int{"prompt_tokens": 0, "completion_tokens": 0},
				},
				assertErr: func(t *testing.T, err error) {
					if err == nil {
						t.Fatal("expected refusal error")
					}
					if err.Error() == "empty choice content" {
						t.Fatal("expected refusal-specific error, got empty choice content")
					}
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if err := json.NewEncoder(w).Encode(tt.response); err != nil {
						t.Fatalf("encode response: %v", err)
					}
				}))
				defer server.Close()

				m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
				_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
				tt.assertErr(t, err)
			})
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
				resp := queryContent(t, tt.content)
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
			{name: "done", content: `{"turn":{"type":"done","summary":"ignored schema","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}}`, want: doneTurn("ignored schema")},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				resp := queryContent(t, tt.content)
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
		raw := doneTurn("ignored")
		resp := queryContent(t, raw)
		if resp.Raw != raw {
			t.Fatalf("raw = %q, want %q", resp.Raw, raw)
		}
		if !errors.Is(resp.ProtocolErr, clnkr.ErrInvalidJSON) {
			t.Fatalf("protocol error = %v, want ErrInvalidJSON", resp.ProtocolErr)
		}
	})
}

func readRequestBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return got
}

func writeChatResponse(t *testing.T, w http.ResponseWriter, content string, inputTokens, outputTokens int) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": content}}},
		"usage":   map[string]int{"prompt_tokens": inputTokens, "completion_tokens": outputTokens},
	}); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func writeAPIError(t *testing.T, w http.ResponseWriter, status int, message string) {
	t.Helper()
	w.Header().Set("Retry-After", "0")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": status, "message": message},
	}); err != nil {
		t.Fatalf("encode error response: %v", err)
	}
}

func queryContent(t *testing.T, content string) clnkr.Response {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeChatResponse(t, w, content, 0, 0)
	}))
	defer server.Close()

	m := openai.NewModel(server.URL, "test-key", "gpt-test", "sys")
	resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return resp
}

func doneTurn(summary string) string {
	return `{"type":"done","summary":"` + summary + `","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`
}

func openAIWrappedDone(summary string) string {
	return `{"turn":{"type":"done","bash":null,"question":null,"summary":"` + summary + `","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":null}}`
}

func schemaContainsKey(node any, key string) bool {
	switch v := node.(type) {
	case map[string]any:
		if _, ok := v[key]; ok {
			return true
		}
		for _, child := range v {
			if schemaContainsKey(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if schemaContainsKey(child, key) {
				return true
			}
		}
	}

	return false
}

func assertSchemaShape(t *testing.T, schema map[string]any) {
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

	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties = %T, want map[string]any", schema["properties"])
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
		wantRequired := []string{"type", "bash", "question", "summary", "reasoning"}
		if turnType == "done" {
			wantRequired = []string{"type", "bash", "question", "summary", "verification", "known_risks", "reasoning"}
		}
		if got, want := branch["required"], wantRequired; !sameStringSlice(got, want) {
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
		assertNullableStringUnion(t, branchProperties["reasoning"])
	}
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

func assertNullableStringUnion(t *testing.T, raw any) {
	t.Helper()

	prop, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("nullable property = %T, want map[string]any", raw)
	}
	branches, ok := prop["anyOf"].([]any)
	if !ok {
		t.Fatalf("nullable property anyOf = %T, want []any", prop["anyOf"])
	}
	if len(branches) != 2 {
		t.Fatalf("len(nullable property anyOf) = %d, want 2", len(branches))
	}

	assertSchemaTypeBranch(t, branches, "string")
	assertSchemaTypeBranch(t, branches, "null")
}

func assertSchemaTypeBranch(t *testing.T, branches []any, wantType string) {
	t.Helper()

	for _, branch := range branches {
		branchMap, ok := branch.(map[string]any)
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
	gotSlice, ok := got.([]any)
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
