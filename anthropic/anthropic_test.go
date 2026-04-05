package anthropic_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/anthropic"
	"github.com/clnkr-ai/clnkr/turnschema"
)

func TestModel(t *testing.T) {
	t.Run("uses structured output request body", func(t *testing.T) {
		var gotBody map[string]interface{}
		var gotHeaders http.Header

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotHeaders = r.Header
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"content": []map[string]string{{"type": "text", "text": anthropicStructuredDone("ok")}},
				"usage":   map[string]int{"input_tokens": 10, "output_tokens": 20},
			})
		}))
		defer server.Close()

		m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys prompt")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotHeaders.Get("x-api-key") != "test-key" {
			t.Errorf("got api key %q, want %q", gotHeaders.Get("x-api-key"), "test-key")
		}
		if gotHeaders.Get("anthropic-version") == "" {
			t.Error("missing anthropic-version header")
		}
		if gotBody["system"] != "sys prompt" {
			t.Errorf("got system %v, want %q", gotBody["system"], "sys prompt")
		}
		outputConfig, ok := gotBody["output_config"].(map[string]interface{})
		if !ok {
			t.Fatalf("output_config = %T, want map[string]interface{}", gotBody["output_config"])
		}
		format, ok := outputConfig["format"].(map[string]interface{})
		if !ok {
			t.Fatalf("output_config.format = %T, want map[string]interface{}", outputConfig["format"])
		}
		if format["type"] != "json_schema" {
			t.Fatalf("output_config.format.type = %v, want json_schema", format["type"])
		}
		gotSchemaJSON, err := json.Marshal(format["schema"])
		if err != nil {
			t.Fatalf("marshal got schema: %v", err)
		}
		wantSchemaJSON, err := json.Marshal(turnschema.Schema())
		if err != nil {
			t.Fatalf("marshal want schema: %v", err)
		}
		if string(gotSchemaJSON) != string(wantSchemaJSON) {
			t.Fatalf("schema mismatch\n got: %s\nwant: %s", gotSchemaJSON, wantSchemaJSON)
		}
	})

	t.Run("returns canonical json text", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"content": []map[string]interface{}{{
					"type": "text",
					"text": `{"type":"done","command":null,"question":null,"summary":"hello back","reasoning":null}`,
				}},
				"usage": map[string]int{"input_tokens": 50, "output_tokens": 30},
			})
		}))
		defer server.Close()

		m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys")
		resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Message.Role != "assistant" {
			t.Errorf("got role %q, want assistant", resp.Message.Role)
		}
		if resp.Message.Content != `{"type":"done","summary":"hello back"}` {
			t.Errorf("got content %q, want %q", resp.Message.Content, `{"type":"done","summary":"hello back"}`)
		}
		if resp.Usage.InputTokens != 50 || resp.Usage.OutputTokens != 30 {
			t.Errorf("got usage %+v, want 50/30", resp.Usage)
		}
	})

	t.Run("fails closed on missing or empty structured text payload", func(t *testing.T) {
		tests := []struct {
			name    string
			content []map[string]string
		}{
			{name: "no text block", content: []map[string]string{{"type": "tool_use", "text": ""}}},
			{name: "empty text block", content: []map[string]string{{"type": "text", "text": ""}}},
			{
				name: "multiple text blocks",
				content: []map[string]string{
					{"type": "text", "text": `{"type":"done","summary":"one"}`},
					{"type": "text", "text": `{"type":"done","summary":"two"}`},
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"content": tt.content,
						"usage":   map[string]int{"input_tokens": 1, "output_tokens": 1},
					})
				}))
				defer server.Close()

				m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys")
				_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
				if err == nil {
					t.Fatal("expected fail-closed error")
				}
			})
		}
	})

	t.Run("fails closed on invalid structured payload", func(t *testing.T) {
		tests := []struct {
			name string
			text string
		}{
			{name: "missing command", text: `{"type":"act"}`},
			{name: "prose wrapped json", text: "Here is the result:\n{\"type\":\"done\",\"summary\":\"wrapped\"}"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"content": []map[string]string{{"type": "text", "text": tt.text}},
						"usage":   map[string]int{"input_tokens": 1, "output_tokens": 1},
					})
				}))
				defer server.Close()

				m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys")
				_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
				if err == nil {
					t.Fatal("expected invalid payload error")
				}
			})
		}
	})

	t.Run("fails closed when output config is ignored", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"content": []map[string]string{{"type": "text", "text": `{"type":"done","summary":"ignored schema"}`}},
				"usage":   map[string]int{"input_tokens": 1, "output_tokens": 1},
			})
		}))
		defer server.Close()

		m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Fatal("expected schema-shape error")
		}
	})

	t.Run("extracts error message from JSON error response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"type": "error",
				"error": map[string]interface{}{
					"type":    "rate_limit_error",
					"message": "Rate limit exceeded",
				},
			})
		}))
		defer server.Close()

		m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Fatal("expected error on 429")
		}
		want := "api error (status 429): Rate limit exceeded"
		if err.Error() != want {
			t.Errorf("got %q, want error containing %q", err.Error(), want)
		}
	})

	t.Run("falls back to raw body for non-JSON errors", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("Bad Gateway"))
		}))
		defer server.Close()

		m := anthropic.NewModel(server.URL, "bad-key", "claude-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Fatal("expected error on 502")
		}
		want := "api error (status 502): Bad Gateway"
		if err.Error() != want {
			t.Errorf("got %q, want %q", err.Error(), want)
		}
	})
}

func anthropicStructuredDone(summary string) string {
	return `{"type":"done","command":null,"question":null,"summary":"` + summary + `","reasoning":null}`
}
