package hew

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnthropicModel(t *testing.T) {
	t.Run("sends correct request", func(t *testing.T) {
		var gotBody map[string]interface{}
		var gotHeaders http.Header

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotHeaders = r.Header
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &gotBody)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"content": []map[string]string{{"type": "text", "text": "response"}},
				"usage":   map[string]int{"input_tokens": 10, "output_tokens": 20},
			})
		}))
		defer server.Close()

		m := NewAnthropicModel(server.URL, "test-key", "claude-test", "sys prompt")
		_, err := m.Query(context.Background(), []Message{{Role: "user", Content: "hello"}})
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
	})

	t.Run("parses response with usage", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"content": []map[string]string{{"type": "text", "text": "hello back"}},
				"usage":   map[string]int{"input_tokens": 50, "output_tokens": 30},
			})
		}))
		defer server.Close()

		m := NewAnthropicModel(server.URL, "test-key", "claude-test", "sys")
		resp, err := m.Query(context.Background(), []Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Message.Role != "assistant" {
			t.Errorf("got role %q, want assistant", resp.Message.Role)
		}
		if resp.Message.Content != "hello back" {
			t.Errorf("got content %q, want %q", resp.Message.Content, "hello back")
		}
		if resp.Usage.InputTokens != 50 || resp.Usage.OutputTokens != 30 {
			t.Errorf("got usage %+v, want 50/30", resp.Usage)
		}
	})

	t.Run("returns error on non-200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid key"}`))
		}))
		defer server.Close()

		m := NewAnthropicModel(server.URL, "bad-key", "claude-test", "sys")
		_, err := m.Query(context.Background(), []Message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Error("expected error on 401")
		}
	})
}
