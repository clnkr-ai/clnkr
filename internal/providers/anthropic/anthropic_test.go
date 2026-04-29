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
		if gotBody["max_tokens"] != float64(4096) {
			t.Errorf("got max_tokens %v, want 4096", gotBody["max_tokens"])
		}
		if _, ok := gotBody["thinking"]; ok {
			t.Fatalf("thinking = %#v, want omitted by default", gotBody["thinking"])
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
		schema, ok := format["schema"].(map[string]interface{})
		if !ok {
			t.Fatalf("output_config.format.schema = %T, want map[string]interface{}", format["schema"])
		}
		if schemaContainsKey(schema, "maxItems") {
			t.Fatal("output_config.format.schema unexpectedly contains maxItems")
		}
		if !schemaContainsKey(schema, "minItems") {
			t.Fatal("output_config.format.schema unexpectedly omits minItems")
		}
	})

	t.Run("QueryText returns plain text without output config", func(t *testing.T) {
		var gotBody map[string]any
		history := []clnkr.Message{
			{Role: "user", Content: "first task"},
			{Role: "assistant", Content: `{"type":"done","summary":"canonical transcript"}`},
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"content": []map[string]string{{"type": "text", "text": "Older work summarized."}},
				"usage":   map[string]int{"input_tokens": 10, "output_tokens": 20},
			})
		}))
		defer server.Close()

		m := anthropic.NewModel(server.URL, "test-key", "claude-test", "sys prompt")
		summary, err := m.QueryText(context.Background(), history)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if summary != "Older work summarized." {
			t.Fatalf("summary = %q, want %q", summary, "Older work summarized.")
		}
		if outputCfg, ok := gotBody["output_config"]; ok {
			// output_config should be omitted for QueryText when no effort is set
			if outputCfg != nil {
				t.Fatalf("output_config should be omitted for QueryText by default, got %#v", outputCfg)
			}
		}
		if gotBody["max_tokens"] != float64(4096) {
			t.Fatalf("max_tokens = %#v, want 4096", gotBody["max_tokens"])
		}
		if _, ok := gotBody["thinking"]; ok {
			t.Fatalf("thinking should be omitted by default for QueryText, got %#v", gotBody["thinking"])
		}
		msgs, ok := gotBody["messages"].([]any)
		if !ok || len(msgs) != len(history) {
			t.Fatalf("messages = %#v, want %d transcript messages", gotBody["messages"], len(history))
		}
		last, ok := msgs[len(msgs)-1].(map[string]any)
		if !ok {
			t.Fatalf("last message = %#v, want map", msgs[len(msgs)-1])
		}
		if got, want := last["content"], `{"type":"done","summary":"canonical transcript"}`; got != want {
			t.Fatalf("last assistant content = %#v, want %q", got, want)
		}
	})

	t.Run("serializes harness options", func(t *testing.T) {
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

		m := anthropic.NewModelWithOptions(server.URL, "test-key", "claude-sonnet-4-20250514", "sys", anthropic.Options{
			MaxTokens:            8000,
			ThinkingBudgetTokens: 2048,
		})
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotBody["max_tokens"] != float64(8000) {
			t.Fatalf("max_tokens = %#v, want 8000", gotBody["max_tokens"])
		}
		thinking, ok := gotBody["thinking"].(map[string]any)
		if !ok {
			t.Fatalf("thinking = %#v, want object", gotBody["thinking"])
		}
		if got, want := thinking["type"], "enabled"; got != want {
			t.Fatalf("thinking.type = %#v, want %q", got, want)
		}
		if got, want := thinking["budget_tokens"], float64(2048); got != want {
			t.Fatalf("thinking.budget_tokens = %#v, want %v", got, want)
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
		if got := mustCanonicalTurn(t, resp.Turn); got != `{"type":"done","summary":"hello back"}` {
			t.Errorf("got content %q, want %q", got, `{"type":"done","summary":"hello back"}`)
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

	t.Run("joins trailing slash base URL", func(t *testing.T) {
		var gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"content": []map[string]string{{"type": "text", "text": anthropicWrappedDone("ok")}},
				"usage":   map[string]int{"input_tokens": 1, "output_tokens": 1},
			})
		}))
		defer server.Close()

		m := anthropic.NewModel(server.URL+"/", "test-key", "claude-test", "sys")
		_, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotPath != "/v1/messages" {
			t.Errorf("got path %q, want %q", gotPath, "/v1/messages")
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
			{name: "done turn with command sibling", text: `{"turn":{"type":"done","bash":{"commands":[{"command":"rm -rf tmp","workdir":null}]},"question":null,"summary":"done","reasoning":null}}`, wantErr: clnkr.ErrInvalidJSON},
			{name: "clarify turn with summary sibling", text: `{"turn":{"type":"clarify","bash":null,"question":"Which repo?","summary":"done","reasoning":null}}`, wantErr: clnkr.ErrInvalidJSON},
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
				if resp.Raw != tt.text {
					t.Fatalf("raw = %q, want %q", resp.Raw, tt.text)
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
				if got := mustCanonicalTurn(t, resp.Turn); got != tt.want {
					t.Fatalf("content = %q, want %q", got, tt.want)
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
		if resp.Raw != raw {
			t.Fatalf("raw = %q, want %q", resp.Raw, raw)
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

func TestModelToolCalls(t *testing.T) {
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
			}, {
				"type":  "tool_use",
				"id":    "toolu_2",
				"name":  "bash",
				"input": map[string]any{"command": "git status", "workdir": nil},
			}},
			"usage": map[string]int{"input_tokens": 3, "output_tokens": 4},
		})
	}))
	defer server.Close()

	m := anthropic.NewModelWithOptions(server.URL, "test-key", "claude-test", "sys prompt", anthropic.Options{UseBashToolCalls: true})
	resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "where"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	tools, ok := gotBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one bash tool", gotBody["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "bash" {
		t.Fatalf("tool = %#v, want bash", tool)
	}
	schema := tool["input_schema"].(map[string]any)
	if schema["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v, want false", schema["additionalProperties"])
	}
	turn, ok := resp.Turn.(*clnkr.ActTurn)
	if !ok {
		t.Fatalf("Turn = %T, want *ActTurn", resp.Turn)
	}
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{
				"type": "text",
				"text": "I will inspect the working directory first.",
			}, {
				"type":  "tool_use",
				"id":    "toolu_1",
				"name":  "bash",
				"input": map[string]any{"command": "pwd", "workdir": nil},
			}},
			"usage": map[string]int{"input_tokens": 3, "output_tokens": 4},
		})
	}))
	defer server.Close()

	m := anthropic.NewModelWithOptions(server.URL, "test-key", "claude-test", "sys prompt", anthropic.Options{UseBashToolCalls: true})
	resp, err := m.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "where"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if resp.ProtocolErr != nil {
		t.Fatalf("ProtocolErr = %v, want nil", resp.ProtocolErr)
	}
	turn, ok := resp.Turn.(*clnkr.ActTurn)
	if !ok {
		t.Fatalf("Turn = %T, want *ActTurn", resp.Turn)
	}
	if got := turn.Reasoning; got != "I will inspect the working directory first." {
		t.Fatalf("Reasoning = %q, want text block", got)
	}
	if got := turn.Bash.Commands[0]; got.ID != "toolu_1" || got.Command != "pwd" {
		t.Fatalf("command = %#v, want provider ID and command", got)
	}
}

func TestModelToolCallsReplayToolMessagesWithoutDuplicateText(t *testing.T) {
	var gotMessages []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		gotMessages = body["messages"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": anthropicWrappedDone("ok")}},
		})
	}))
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		gotMessages = body["messages"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": anthropicWrappedDone("ok")}},
		})
	}))
	defer server.Close()

	m := anthropic.NewModelWithOptions(server.URL, "test-key", "claude-test", "sys prompt", anthropic.Options{UseBashToolCalls: true})
	_, err := m.Query(context.Background(), []clnkr.Message{
		{Role: "assistant", Content: `{"type":"act","bash":{"commands":[{"command":"false","workdir":null}]}}`, BashToolCalls: []clnkr.BashToolCall{{ID: "toolu_1", Command: "false"}}},
		{Role: "user", Content: "payload", BashToolResult: &clnkr.BashToolResult{ID: "toolu_1", Content: "payload", IsError: true}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	secondContent := gotMessages[1].(map[string]any)["content"].([]any)
	result := secondContent[0].(map[string]any)
	if result["type"] != "tool_result" || result["is_error"] != true {
		t.Fatalf("tool result = %#v, want is_error", result)
	}
}

func TestModelToolCallsReplayConsecutiveToolResultsTogether(t *testing.T) {
	var gotMessages []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		gotMessages = body["messages"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": anthropicWrappedDone("ok")}},
		})
	}))
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
	secondContent := gotMessages[1].(map[string]any)["content"].([]any)
	if len(secondContent) != 2 {
		t.Fatalf("second content = %#v, want two tool_result blocks", secondContent)
	}
	first := secondContent[0].(map[string]any)
	second := secondContent[1].(map[string]any)
	if first["tool_use_id"] != "toolu_1" || first["content"] != "payload 1" {
		t.Fatalf("first tool result = %#v", first)
	}
	if second["tool_use_id"] != "toolu_2" || second["content"] != "payload 2" || second["is_error"] != true {
		t.Fatalf("second tool result = %#v", second)
	}
}

func TestModelQueryFinalOmitsBashTool(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": anthropicWrappedDone("final")}},
		})
	}))
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
