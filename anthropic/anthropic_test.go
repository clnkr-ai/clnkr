package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
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
				"content": []map[string]string{{"type": "text", "text": anthropicWrappedDone("ok")}},
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
					"text": anthropicWrappedDone("hello back"),
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
		if resp.ProtocolErr != nil {
			t.Fatalf("got protocol error %v, want nil", resp.ProtocolErr)
		}
		if resp.Usage.InputTokens != 50 || resp.Usage.OutputTokens != 30 {
			t.Errorf("got usage %+v, want 50/30", resp.Usage)
		}
	})

	t.Run("normalizes assistant history to wrapped provider json", func(t *testing.T) {
		var gotBody map[string]interface{}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"content": []map[string]interface{}{{
					"type": "text",
					"text": anthropicWrappedDone("ok"),
				}},
				"usage": map[string]int{"input_tokens": 1, "output_tokens": 1},
			})
		}))
		defer server.Close()

		m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: `{"type":"done","summary":"hello back"}`},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		msgs := gotBody["messages"].([]interface{})
		last := msgs[len(msgs)-1].(map[string]interface{})
		if got, want := last["content"], anthropicWrappedDone("hello back"); got != want {
			t.Fatalf("assistant history content = %q, want %q", got, want)
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

	t.Run("returns raw payload plus protocol error on invalid structured payload", func(t *testing.T) {
		tests := []struct {
			name    string
			text    string
			wantErr error
		}{
			{name: "single-command wrapped act turn", text: `{"turn":{"type":"act","bash":{"command":"pwd","workdir":null},"question":null,"summary":null,"reasoning":null}}`, wantErr: clnkr.ErrInvalidJSON},
			{name: "semantic invalid act turn", text: `{"turn":{"type":"act","bash":{"commands":[{"command":"","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`, wantErr: clnkr.ErrMissingCommand},
			{name: "multiple wrapped objects", text: `{"turn":{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]},"question":null,"summary":null,"reasoning":null}}{"turn":{"type":"done","bash":null,"question":null,"summary":"done","reasoning":null}}`, wantErr: clnkr.ErrInvalidJSON},
			{name: "prose wrapped json", text: "Here is the result:\n{\"turn\":{\"type\":\"done\",\"summary\":\"wrapped\"}}", wantErr: clnkr.ErrInvalidJSON},
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
				resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if resp.Message.Content != tt.text {
					t.Fatalf("content = %q, want %q", resp.Message.Content, tt.text)
				}
				if !errors.Is(resp.ProtocolErr, tt.wantErr) {
					t.Fatalf("protocol error = %v, want %v", resp.ProtocolErr, tt.wantErr)
				}
			})
		}
	})

	t.Run("canonicalizes wrapped clarify and done payloads without null siblings", func(t *testing.T) {
		tests := []struct {
			name string
			text string
			want string
		}{
			{name: "clarify", text: `{"turn":{"type":"clarify","question":"Which directory?"}}`, want: `{"type":"clarify","question":"Which directory?"}`},
			{name: "done", text: `{"turn":{"type":"done","summary":"ignored schema"}}`, want: `{"type":"done","summary":"ignored schema"}`},
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
				resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if resp.Message.Content != tt.want {
					t.Fatalf("content = %q, want %q", resp.Message.Content, tt.want)
				}
				if resp.ProtocolErr != nil {
					t.Fatalf("protocol error = %v, want nil", resp.ProtocolErr)
				}
			})
		}
	})

	t.Run("returns raw payload plus protocol error when output config is ignored", func(t *testing.T) {
		raw := `{"type":"done","summary":"ignored schema"}`
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"content": []map[string]string{{"type": "text", "text": raw}},
				"usage":   map[string]int{"input_tokens": 1, "output_tokens": 1},
			})
		}))
		defer server.Close()

		m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys")
		resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Message.Content != raw {
			t.Fatalf("content = %q, want %q", resp.Message.Content, raw)
		}
		if !errors.Is(resp.ProtocolErr, clnkr.ErrInvalidJSON) {
			t.Fatalf("protocol error = %v, want ErrInvalidJSON", resp.ProtocolErr)
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

func anthropicWrappedDone(summary string) string {
	return `{"turn":{"type":"done","bash":null,"question":null,"summary":"` + summary + `","reasoning":null}}`
}
