package hew

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIModel(t *testing.T) {
	t.Run("prepends system message", func(t *testing.T) {
		var gotBody map[string]interface{}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &gotBody)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "content": "resp"}},
				},
				"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 20},
			})
		}))
		defer server.Close()

		m := NewOpenAIModel(server.URL, "test-key", "gpt-test", "sys prompt")
		_, err := m.Query(context.Background(), []Message{{Role: "user", Content: "hello"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		msgs := gotBody["messages"].([]interface{})
		first := msgs[0].(map[string]interface{})
		if first["role"] != "system" || first["content"] != "sys prompt" {
			t.Errorf("first message should be system prompt, got %v", first)
		}
	})

	t.Run("parses response with usage", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "content": "hello back"}},
				},
				"usage": map[string]int{"prompt_tokens": 15, "completion_tokens": 25},
			})
		}))
		defer server.Close()

		m := NewOpenAIModel(server.URL, "test-key", "gpt-test", "sys")
		resp, err := m.Query(context.Background(), []Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Message.Content != "hello back" {
			t.Errorf("got %q, want %q", resp.Message.Content, "hello back")
		}
		if resp.Usage.InputTokens != 15 || resp.Usage.OutputTokens != 25 {
			t.Errorf("got usage %+v, want 15/25", resp.Usage)
		}
	})

	t.Run("returns error on empty choices", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []interface{}{},
				"usage":   map[string]int{"prompt_tokens": 0, "completion_tokens": 0},
			})
		}))
		defer server.Close()

		m := NewOpenAIModel(server.URL, "test-key", "gpt-test", "sys")
		_, err := m.Query(context.Background(), []Message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Error("expected error on empty choices")
		}
	})
}
