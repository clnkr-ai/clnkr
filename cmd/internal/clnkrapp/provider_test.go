package clnkrapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/providerconfig"
	providerdomain "github.com/clnkr-ai/clnkr/internal/providers/providerconfig"
)

func anthropicWrappedDone(summary string) string {
	return fmt.Sprintf(`{"turn":{"type":"done","bash":null,"question":null,"summary":%q,"verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":null}}`, summary)
}

func TestNewModelForConfigUsesOpenAIResponsesWhenConfigured(t *testing.T) {
	var gotPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
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
			"usage": map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer server.Close()

	model := NewModelForConfig(providerconfig.ResolvedProviderConfig{
		Provider:    "openai",
		ProviderAPI: "openai-responses",
		Model:       "gpt-5.4",
		BaseURL:     server.URL,
		APIKey:      "test-key",
	}, "sys")
	resp, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if gotPath != "/responses" {
		t.Fatalf("path = %q, want %q", gotPath, "/responses")
	}
	if got := mustCanonicalTurn(t, resp.Turn); got != `{"type":"done","summary":"ok","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}` {
		t.Fatalf("canonical turn = %q, want %q", got, `{"type":"done","summary":"ok","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`)
	}
}

func TestNewModelForConfigPassesOpenAIResponsesRequestOptions(t *testing.T) {
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

	model := NewModelForConfig(providerconfig.ResolvedProviderConfig{
		Provider:    providerdomain.ProviderOpenAI,
		ProviderAPI: providerdomain.ProviderAPIOpenAIResponses,
		Model:       "gpt-5.1",
		BaseURL:     server.URL,
		APIKey:      "test-key",
		RequestOptions: providerdomain.ProviderRequestOptions{
			Effort: providerdomain.ProviderEffortOptions{Level: "high", Set: true},
			Output: providerdomain.ProviderOutputOptions{
				MaxOutputTokens: providerdomain.OptionalInt{Value: 8000, Set: true},
			},
		},
	}, "sys")
	if _, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("Query: %v", err)
	}
	reasoning, ok := gotBody["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("reasoning = %#v, want high effort", gotBody["reasoning"])
	}
	if got := gotBody["max_output_tokens"]; got != float64(8000) {
		t.Fatalf("max_output_tokens = %#v, want 8000", got)
	}
}

func TestNewModelForConfigPassesAnthropicRequestOptions(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": anthropicWrappedDone("ok")}},
			"usage":   map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer server.Close()

	model := NewModelForConfig(providerconfig.ResolvedProviderConfig{
		Provider: providerdomain.ProviderAnthropic,
		Model:    "claude-sonnet-4-20250514",
		BaseURL:  server.URL,
		APIKey:   "test-key",
		RequestOptions: providerdomain.ProviderRequestOptions{
			AnthropicManual: providerdomain.AnthropicManualThinkingOptions{
				ThinkingBudgetTokens: providerdomain.OptionalInt{Value: 2048, Set: true},
			},
			Output: providerdomain.ProviderOutputOptions{
				MaxOutputTokens: providerdomain.OptionalInt{Value: 8000, Set: true},
			},
		},
	}, "sys")
	if _, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if got := gotBody["max_tokens"]; got != float64(8000) {
		t.Fatalf("max_tokens = %#v, want 8000", got)
	}
	thinking, ok := gotBody["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" || thinking["budget_tokens"] != float64(2048) {
		t.Fatalf("thinking = %#v, want enabled budget 2048", gotBody["thinking"])
	}
}

func TestNewModelForConfigPassesBashToolCallOption(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{
				"type":  "tool_use",
				"id":    "toolu_1",
				"name":  "bash",
				"input": map[string]any{"command": "pwd", "workdir": nil},
			}},
		})
	}))
	defer server.Close()

	model := NewModelForConfig(providerconfig.ResolvedProviderConfig{
		Provider:    providerdomain.ProviderAnthropic,
		Model:       "claude-sonnet-4-20250514",
		BaseURL:     server.URL,
		APIKey:      "test-key",
		ActProtocol: clnkr.ActProtocolToolCalls,
	}, "sys")
	resp, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if tools, ok := gotBody["tools"].([]any); !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one bash tool", gotBody["tools"])
	}
	if _, ok := resp.Turn.(*clnkr.ActTurn); !ok {
		t.Fatalf("Turn = %T, want *ActTurn", resp.Turn)
	}
}

func TestNewModelForConfigPassesAnthropicEffortWithAdaptiveThinking(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": anthropicWrappedDone("ok")}},
			"usage":   map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer server.Close()

	model := NewModelForConfig(providerconfig.ResolvedProviderConfig{
		Provider: providerdomain.ProviderAnthropic,
		Model:    "claude-sonnet-4-20250514",
		BaseURL:  server.URL,
		APIKey:   "test-key",
		RequestOptions: providerdomain.ProviderRequestOptions{
			Effort: providerdomain.ProviderEffortOptions{Level: "medium", Set: true},
			Output: providerdomain.ProviderOutputOptions{
				MaxOutputTokens: providerdomain.OptionalInt{Value: 4096, Set: true},
			},
		},
	}, "sys")
	if _, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("Query: %v", err)
	}

	outputConfig, ok := gotBody["output_config"].(map[string]any)
	if !ok {
		t.Fatalf("output_config = %#v, want object", gotBody["output_config"])
	}
	if outputConfig["effort"] != "medium" {
		t.Fatalf("output_config.effort = %#v, want medium", gotBody["output_config"])
	}

	thinking, ok := gotBody["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("thinking = %#v, want object", gotBody["thinking"])
	}
	if thinking["type"] != "adaptive" {
		t.Fatalf("thinking.type = %#v, want adaptive", gotBody["thinking"])
	}
}

func TestMakeCompactorFactoryUsesOpenAIWhenProviderSelected(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": "Older work summarized."}},
			},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer server.Close()

	compactor := MakeCompactorFactory(providerconfig.ResolvedProviderConfig{
		Provider:    "openai",
		ProviderAPI: "openai-chat-completions",
		BaseURL:     server.URL,
		APIKey:      "test-key",
		Model:       "gpt-test",
	})("")
	summary, err := compactor.Summarize(context.Background(), []clnkr.Message{{Role: "user", Content: "first task"}})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if summary != "Older work summarized." {
		t.Fatalf("summary = %q, want %q", summary, "Older work summarized.")
	}
	if _, ok := gotBody["response_format"]; ok {
		t.Fatalf("response_format should be omitted for compaction, got %#v", gotBody["response_format"])
	}
}

func TestMakeCompactorFactoryUsesOpenAIResponsesWhenConfigured(t *testing.T) {
	var requestPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
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
			"usage": map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer server.Close()

	compactor := MakeCompactorFactory(providerconfig.ResolvedProviderConfig{
		Provider:    "openai",
		ProviderAPI: "openai-responses",
		BaseURL:     server.URL,
		APIKey:      "test-key",
		Model:       "gpt-test",
	})("")
	summary, err := compactor.Summarize(context.Background(), []clnkr.Message{{Role: "user", Content: "first task"}})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if summary != "Older work summarized." {
		t.Fatalf("summary = %q, want %q", summary, "Older work summarized.")
	}
	if requestPath != "/responses" {
		t.Fatalf("request path = %q, want %q", requestPath, "/responses")
	}
	if _, ok := gotBody["text"]; ok {
		t.Fatalf("text should be omitted for compaction, got %#v", gotBody["text"])
	}
}

func TestMakeCompactorFactoryUsesAnthropicWhenProviderSelected(t *testing.T) {
	var requestPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{
				{"type": "text", "text": "Older work summarized."},
			},
			"usage": map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer server.Close()

	compactor := MakeCompactorFactory(providerconfig.ResolvedProviderConfig{
		Provider: "anthropic",
		BaseURL:  server.URL,
		APIKey:   "test-key",
		Model:    "claude-test",
	})("")
	summary, err := compactor.Summarize(context.Background(), []clnkr.Message{{Role: "user", Content: "first task"}})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if summary != "Older work summarized." {
		t.Fatalf("summary = %q, want %q", summary, "Older work summarized.")
	}
	if requestPath != "/v1/messages" {
		t.Fatalf("request path = %q, want %q", requestPath, "/v1/messages")
	}
	if _, ok := gotBody["response_format"]; ok {
		t.Fatalf("response_format should be omitted for compaction, got %#v", gotBody["response_format"])
	}
	if _, ok := gotBody["max_tokens"]; !ok {
		t.Fatalf("anthropic request missing max_tokens: %#v", gotBody)
	}
}
