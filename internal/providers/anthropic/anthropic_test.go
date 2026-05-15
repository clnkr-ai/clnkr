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
	"github.com/clnkr-ai/clnkr/internal/providers/anthropic"
)

func TestModelRequestSerialization(t *testing.T) {
	t.Run("uses structured output request body", func(t *testing.T) {
		var gotBody map[string]any
		var gotHeaders http.Header
		server := jsonServer(t, func(r *http.Request) any {
			gotHeaders = r.Header
			gotBody = requestBody(t, r)
			return textResponse(anthropicWrappedDone("ok"))
		})
		defer server.Close()

		m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys prompt")
		if _, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}}); err != nil {
			t.Fatalf("Query: %v", err)
		}

		if gotHeaders.Get("x-api-key") != "test-key" {
			t.Errorf("x-api-key = %q, want test-key", gotHeaders.Get("x-api-key"))
		}
		if gotHeaders.Get("anthropic-version") == "" {
			t.Error("missing anthropic-version header")
		}
		if gotBody["system"] != "sys prompt" {
			t.Errorf("system = %#v, want sys prompt", gotBody["system"])
		}
		if gotBody["max_tokens"] != float64(4096) {
			t.Errorf("max_tokens = %#v, want 4096", gotBody["max_tokens"])
		}
		if _, ok := gotBody["thinking"]; ok {
			t.Fatalf("thinking = %#v, want omitted", gotBody["thinking"])
		}

		format := gotBody["output_config"].(map[string]any)["format"].(map[string]any)
		if format["type"] != "json_schema" {
			t.Fatalf("output_config.format.type = %#v, want json_schema", format["type"])
		}
		schema := format["schema"].(map[string]any)
		if schemaContainsKey(schema, "maxItems") {
			t.Fatal("output_config.format.schema unexpectedly contains maxItems")
		}
		if !schemaContainsKey(schema, "minItems") {
			t.Fatal("output_config.format.schema unexpectedly omits minItems")
		}
	})

	t.Run("QueryText returns plain text without output config", func(t *testing.T) {
		var gotBody map[string]any
		server := jsonServer(t, func(r *http.Request) any {
			gotBody = requestBody(t, r)
			return textResponse("Older work summarized.")
		})
		defer server.Close()

		history := []clnkr.Message{
			{Role: "user", Content: "first task"},
			{Role: "assistant", Content: doneTurn("canonical transcript")},
		}
		m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys prompt")
		summary, err := m.QueryText(context.Background(), history)
		if err != nil {
			t.Fatalf("QueryText: %v", err)
		}
		if summary != "Older work summarized." {
			t.Fatalf("summary = %q, want Older work summarized.", summary)
		}
		if outputCfg, ok := gotBody["output_config"]; ok && outputCfg != nil {
			t.Fatalf("output_config = %#v, want omitted", outputCfg)
		}
		if gotBody["max_tokens"] != float64(4096) {
			t.Fatalf("max_tokens = %#v, want 4096", gotBody["max_tokens"])
		}
		if _, ok := gotBody["thinking"]; ok {
			t.Fatalf("thinking = %#v, want omitted", gotBody["thinking"])
		}
		msgs := gotBody["messages"].([]any)
		if len(msgs) != len(history) {
			t.Fatalf("messages = %#v, want %d transcript messages", msgs, len(history))
		}
		last := msgs[len(msgs)-1].(map[string]any)
		if got := last["content"]; got != doneTurn("canonical transcript") {
			t.Fatalf("last assistant content = %#v, want canonical transcript", got)
		}
	})

	t.Run("serializes harness options", func(t *testing.T) {
		var gotBody map[string]any
		server := jsonServer(t, func(r *http.Request) any {
			gotBody = requestBody(t, r)
			return textResponse(anthropicWrappedDone("ok"))
		})
		defer server.Close()

		m := anthropic.NewModelWithOptions(server.URL, "test-key", "claude-sonnet-4-20250514", "sys", anthropic.Options{
			MaxTokens:            8000,
			ThinkingBudgetTokens: 2048,
		})
		if _, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}}); err != nil {
			t.Fatalf("Query: %v", err)
		}
		if gotBody["max_tokens"] != float64(8000) {
			t.Fatalf("max_tokens = %#v, want 8000", gotBody["max_tokens"])
		}
		thinking := gotBody["thinking"].(map[string]any)
		if got := thinking["type"]; got != "enabled" {
			t.Fatalf("thinking.type = %#v, want enabled", got)
		}
		if got := thinking["budget_tokens"]; got != float64(2048) {
			t.Fatalf("thinking.budget_tokens = %#v, want 2048", got)
		}
	})

	t.Run("joins trailing slash base URL", func(t *testing.T) {
		var gotPath string
		server := jsonServer(t, func(r *http.Request) any {
			gotPath = r.URL.Path
			return textResponse(anthropicWrappedDone("ok"))
		})
		defer server.Close()

		m := anthropic.NewModel(server.URL+"/", "test-key", "claude-test", "sys")
		if _, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}}); err != nil {
			t.Fatalf("Query: %v", err)
		}
		if gotPath != "/v1/messages" {
			t.Errorf("path = %q, want /v1/messages", gotPath)
		}
	})
}

func TestModelStructuredResponses(t *testing.T) {
	t.Run("returns canonical json text", func(t *testing.T) {
		server := jsonServer(t, func(r *http.Request) any {
			return response(50, 30, map[string]any{"type": "text", "text": anthropicWrappedDone("hello back")})
		})
		defer server.Close()

		m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys")
		resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if got, want := mustCanonicalTurn(t, resp.Turn), doneTurn("hello back"); got != want {
			t.Errorf("content = %q, want %q", got, want)
		}
		if resp.ProtocolErr != nil {
			t.Fatalf("ProtocolErr = %v, want nil", resp.ProtocolErr)
		}
		if resp.Usage.InputTokens != 50 || resp.Usage.OutputTokens != 30 {
			t.Errorf("usage = %+v, want 50/30", resp.Usage)
		}
	})

	t.Run("normalizes assistant history to wrapped provider json", func(t *testing.T) {
		var gotBody map[string]any
		server := jsonServer(t, func(r *http.Request) any {
			gotBody = requestBody(t, r)
			return textResponse(anthropicWrappedDone("ok"))
		})
		defer server.Close()

		m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: doneTurn("hello back")},
		})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}

		msgs := gotBody["messages"].([]any)
		last := msgs[len(msgs)-1].(map[string]any)
		if got, want := last["content"], anthropicWrappedDone("hello back"); got != want {
			t.Fatalf("assistant history content = %q, want %q", got, want)
		}
	})

	t.Run("fails closed on missing or empty structured text payload", func(t *testing.T) {
		tests := []struct {
			name    string
			content []map[string]string
		}{
			{name: "no text block", content: []map[string]string{{"type": "tool_use"}}},
			{name: "empty text block", content: []map[string]string{{"type": "text", "text": ""}}},
			{name: "multiple text blocks", content: []map[string]string{{"type": "text", "text": doneTurn("one")}, {"type": "text", "text": doneTurn("two")}}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				server := jsonServer(t, func(r *http.Request) any {
					return map[string]any{"content": tt.content, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}}
				})
				defer server.Close()

				m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys")
				if _, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}}); err == nil {
					t.Fatal("expected fail-closed error")
				}
			})
		}
	})

	t.Run("returns raw payload plus protocol error on invalid structured payload", func(t *testing.T) {
		text := `{"turn":{"type":"done","bash":{"commands":[{"command":"rm -rf tmp","workdir":null}]},"question":null,"summary":"done","reasoning":null}}`
		server := jsonServer(t, func(r *http.Request) any { return textResponse(text) })
		defer server.Close()

		m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys")
		resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if resp.Raw != text {
			t.Fatalf("raw = %q, want %q", resp.Raw, text)
		}
		if !errors.Is(resp.ProtocolErr, clnkr.ErrInvalidJSON) {
			t.Fatalf("ProtocolErr = %v, want ErrInvalidJSON", resp.ProtocolErr)
		}
	})

	t.Run("returns raw payload plus protocol error when output config is ignored", func(t *testing.T) {
		raw := doneTurn("ignored schema")
		server := jsonServer(t, func(r *http.Request) any { return textResponse(raw) })
		defer server.Close()

		m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys")
		resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if resp.Raw != raw {
			t.Fatalf("raw = %q, want %q", resp.Raw, raw)
		}
		if !errors.Is(resp.ProtocolErr, clnkr.ErrInvalidJSON) {
			t.Fatalf("ProtocolErr = %v, want ErrInvalidJSON", resp.ProtocolErr)
		}
	})
}

func TestModelErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   any
		want   string
	}{
		{
			name:   "extracts error message from JSON error response",
			status: http.StatusTooManyRequests,
			body:   map[string]any{"type": "error", "error": map[string]any{"type": "rate_limit_error", "message": "Rate limit exceeded"}},
			want:   "api error (status 429): Rate limit exceeded",
		},
		{name: "falls back to raw body for non-JSON errors", status: http.StatusBadGateway, body: "Bad Gateway", want: "api error (status 502): Bad Gateway"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				switch body := tt.body.(type) {
				case string:
					_, _ = w.Write([]byte(body))
				default:
					_ = json.NewEncoder(w).Encode(body)
				}
			}))
			defer server.Close()

			m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys")
			_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
			if err == nil {
				t.Fatal("expected API error")
			}
			if err.Error() != tt.want {
				t.Fatalf("error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestModelToolCalls(t *testing.T) {
	var gotBody map[string]any
	server := jsonServer(t, func(r *http.Request) any {
		gotBody = requestBody(t, r)
		return response(3, 4,
			toolUse("toolu_1", "pwd", nil),
			toolUse("toolu_2", "git status", nil),
		)
	})
	defer server.Close()

	m := anthropic.NewModelWithOptions(server.URL, "test-key", "claude-test", "sys prompt", anthropic.Options{UseBashToolCalls: true})
	resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "where"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	tools := gotBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %#v, want one bash tool", tools)
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "bash" {
		t.Fatalf("tool = %#v, want bash", tool)
	}
	schema := tool["input_schema"].(map[string]any)
	if schema["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v, want false", schema["additionalProperties"])
	}
	turn := resp.Turn.(*clnkr.ActTurn)
	if got := len(turn.Bash.Commands); got != 2 {
		t.Fatalf("commands = %d, want 2", got)
	}
	if got := turn.Bash.Commands[0]; got.ID != "toolu_1" || got.Command != "pwd" {
		t.Fatalf("first command = %#v, want provider ID and command", got)
	}
	if got := turn.Bash.Commands[1]; got.ID != "toolu_2" || got.Command != "git status" {
		t.Fatalf("second command = %#v, want provider ID and command", got)
	}
	if len(resp.BashToolCalls) != 2 || resp.BashToolCalls[0].ID != "toolu_1" || resp.BashToolCalls[1].ID != "toolu_2" {
		t.Fatalf("BashToolCalls = %#v", resp.BashToolCalls)
	}
}

func TestModelToolCallsAllowsTextAlongsideToolUse(t *testing.T) {
	server := jsonServer(t, func(r *http.Request) any {
		return response(3, 4,
			map[string]any{"type": "text", "text": "I will inspect the working directory first."},
			toolUse("toolu_1", "pwd", nil),
		)
	})
	defer server.Close()

	m := anthropic.NewModelWithOptions(server.URL, "test-key", "claude-test", "sys prompt", anthropic.Options{UseBashToolCalls: true})
	resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "where"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if resp.ProtocolErr != nil {
		t.Fatalf("ProtocolErr = %v, want nil", resp.ProtocolErr)
	}
	turn := resp.Turn.(*clnkr.ActTurn)
	if got := turn.Reasoning; got != "I will inspect the working directory first." {
		t.Fatalf("Reasoning = %q, want text block", got)
	}
	if got := turn.Bash.Commands[0]; got.ID != "toolu_1" || got.Command != "pwd" {
		t.Fatalf("command = %#v, want provider ID and command", got)
	}
}

func TestModelToolCallsReplayToolMessagesWithoutDuplicateText(t *testing.T) {
	var gotMessages []any
	server := jsonServer(t, func(r *http.Request) any {
		gotMessages = requestBody(t, r)["messages"].([]any)
		return textResponse(anthropicWrappedDone("ok"))
	})
	defer server.Close()

	m := anthropic.NewModelWithOptions(server.URL, "test-key", "claude-test", "sys prompt", anthropic.Options{UseBashToolCalls: true})
	_, err := m.Query(context.Background(), []clnkr.Message{
		{Role: "assistant", Content: `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]}}`, BashToolCalls: []clnkr.BashToolCall{{ID: "toolu_1", Command: "pwd"}}},
		{Role: "user", Content: "payload", BashToolResult: &clnkr.BashToolResult{ID: "toolu_1", Content: "payload"}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(gotMessages) != 2 {
		t.Fatalf("messages = %#v, want tool_use and tool_result", gotMessages)
	}
	firstContent := gotMessages[0].(map[string]any)["content"].([]any)
	secondContent := gotMessages[1].(map[string]any)["content"].([]any)
	if firstContent[0].(map[string]any)["type"] != "tool_use" {
		t.Fatalf("first content = %#v, want tool_use", firstContent)
	}
	if secondContent[0].(map[string]any)["type"] != "tool_result" || secondContent[0].(map[string]any)["content"] != "payload" {
		t.Fatalf("second content = %#v, want tool_result payload", secondContent)
	}
}

func TestModelToolCallsReplayErroredToolResult(t *testing.T) {
	var gotMessages []any
	server := jsonServer(t, func(r *http.Request) any {
		gotMessages = requestBody(t, r)["messages"].([]any)
		return textResponse(anthropicWrappedDone("ok"))
	})
	defer server.Close()

	m := anthropic.NewModelWithOptions(server.URL, "test-key", "claude-test", "sys prompt", anthropic.Options{UseBashToolCalls: true})
	_, err := m.Query(context.Background(), []clnkr.Message{
		{Role: "assistant", Content: `{"type":"act","bash":{"commands":[{"command":"false","workdir":null}]}}`, BashToolCalls: []clnkr.BashToolCall{{ID: "toolu_1", Command: "false"}}},
		{Role: "user", Content: "payload", BashToolResult: &clnkr.BashToolResult{ID: "toolu_1", Content: "payload", IsError: true}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	result := gotMessages[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["type"] != "tool_result" || result["is_error"] != true {
		t.Fatalf("tool result = %#v, want is_error", result)
	}
}

func TestModelToolCallsReplayConsecutiveToolResultsTogether(t *testing.T) {
	var gotMessages []any
	server := jsonServer(t, func(r *http.Request) any {
		gotMessages = requestBody(t, r)["messages"].([]any)
		return textResponse(anthropicWrappedDone("ok"))
	})
	defer server.Close()

	m := anthropic.NewModelWithOptions(server.URL, "test-key", "claude-test", "sys prompt", anthropic.Options{UseBashToolCalls: true})
	_, err := m.Query(context.Background(), []clnkr.Message{
		{
			Role:    "assistant",
			Content: `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null},{"command":"false","workdir":null}]}}`,
			BashToolCalls: []clnkr.BashToolCall{
				{ID: "toolu_1", Command: "pwd"},
				{ID: "toolu_2", Command: "false"},
			},
		},
		{Role: "user", Content: "payload 1", BashToolResult: &clnkr.BashToolResult{ID: "toolu_1", Content: "payload 1"}},
		{Role: "user", Content: "payload 2", BashToolResult: &clnkr.BashToolResult{ID: "toolu_2", Content: "payload 2", IsError: true}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(gotMessages) != 2 {
		t.Fatalf("messages = %#v, want assistant tool_use and one user tool_result message", gotMessages)
	}
	results := gotMessages[1].(map[string]any)["content"].([]any)
	if len(results) != 2 {
		t.Fatalf("second content = %#v, want two tool_result blocks", results)
	}
	first := results[0].(map[string]any)
	second := results[1].(map[string]any)
	if first["tool_use_id"] != "toolu_1" || first["content"] != "payload 1" {
		t.Fatalf("first tool result = %#v", first)
	}
	if second["tool_use_id"] != "toolu_2" || second["content"] != "payload 2" || second["is_error"] != true {
		t.Fatalf("second tool result = %#v", second)
	}
}

func TestModelQueryFinalOmitsBashTool(t *testing.T) {
	var gotBody map[string]any
	server := jsonServer(t, func(r *http.Request) any {
		gotBody = requestBody(t, r)
		return textResponse(anthropicWrappedDone("final"))
	})
	defer server.Close()

	m := anthropic.NewModelWithOptions(server.URL, "test-key", "claude-test", "sys prompt", anthropic.Options{UseBashToolCalls: true})
	if _, err := m.QueryFinal(context.Background(), []clnkr.Message{{Role: "user", Content: "summarize"}}); err != nil {
		t.Fatalf("QueryFinal: %v", err)
	}
	if _, ok := gotBody["tools"]; ok {
		t.Fatalf("tools = %#v, want omitted", gotBody["tools"])
	}
	schema := gotBody["output_config"].(map[string]any)["format"].(map[string]any)["schema"].(map[string]any)
	choices := schema["properties"].(map[string]any)["turn"].(map[string]any)["anyOf"].([]any)
	if len(choices) != 1 {
		t.Fatalf("final schema choices = %#v, want done only", choices)
	}
}

func jsonServer(t *testing.T, handle func(*http.Request) any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(handle(r))
	}))
}

func requestBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("ReadAll request body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("Unmarshal request body: %v", err)
	}
	return got
}

func textResponse(text string) map[string]any {
	return response(1, 1, map[string]any{"type": "text", "text": text})
}

func response(inputTokens, outputTokens int, content ...map[string]any) map[string]any {
	return map[string]any{
		"content": content,
		"usage":   map[string]int{"input_tokens": inputTokens, "output_tokens": outputTokens},
	}
}

func toolUse(id, command string, workdir any) map[string]any {
	return map[string]any{
		"type":  "tool_use",
		"id":    id,
		"name":  "bash",
		"input": map[string]any{"command": command, "workdir": workdir},
	}
}

func doneTurn(summary string) string {
	return `{"type":"done","summary":"` + summary + `","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`
}

func anthropicWrappedDone(summary string) string {
	return `{"turn":{"type":"done","bash":null,"question":null,"summary":"` + summary + `","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":null}}`
}

func mustCanonicalTurn(t *testing.T, turn clnkr.Turn) string {
	t.Helper()
	raw, err := clnkr.CanonicalTurnJSON(turn)
	if err != nil {
		t.Fatalf("CanonicalTurnJSON: %v", err)
	}
	return raw
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
