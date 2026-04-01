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
	"github.com/clnkr-ai/clnkr/openai"
)

func TestModel(t *testing.T) {
	t.Run("prepends system message", func(t *testing.T) {
		var gotBody map[string]interface{}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "content": "resp"}},
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
	})

	t.Run("parses response with usage", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "content": "hello back"}},
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
		if resp.Message.Content != "hello back" {
			t.Errorf("got %q, want %q", resp.Message.Content, "hello back")
		}
		if resp.Usage.InputTokens != 15 || resp.Usage.OutputTokens != 25 {
			t.Errorf("got usage %+v, want 15/25", resp.Usage)
		}
	})

	t.Run("posts to base URL plus chat/completions", func(t *testing.T) {
		var gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "content": "ok"}},
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
					{"message": map[string]string{"role": "assistant", "content": "ok after retry"}},
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
		if resp.Message.Content != "ok after retry" {
			t.Fatalf("content = %q, want %q", resp.Message.Content, "ok after retry")
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
}
