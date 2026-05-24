package openairesponses_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/providers/openairesponses"
)

func TestModelQueryToolCalls(t *testing.T) {
	var gotBody map[string]any
	server := captureServer(t, &gotBody, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"output": []map[string]any{
				{"type": "reasoning", "id": "rs_1", "summary": []any{}},
				{
					"type":      "function_call",
					"call_id":   "call_1",
					"name":      "bash",
					"arguments": `{"command":"pwd","workdir":null}`,
					"status":    "completed",
				},
				{
					"type":      "function_call",
					"call_id":   "call_2",
					"name":      "bash",
					"arguments": `{"command":"git status","workdir":null}`,
					"status":    "completed",
				},
			},
			"usage": map[string]any{"input_tokens": 3, "output_tokens": 4},
		})
	})
	defer server.Close()

	model := openairesponses.NewModelWithOptions(
		server.URL,
		"test-key",
		"gpt-5",
		"sys prompt",
		openairesponses.Options{UseBashToolCalls: true},
	)
	resp, err := model.Query(
		context.Background(),
		[]clnkr.Message{{Role: "user", Content: "where"}},
	)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if _, ok := gotBody["parallel_tool_calls"]; ok {
		t.Fatalf("parallel_tool_calls = %#v, want omitted", gotBody["parallel_tool_calls"])
	}
	include, ok := gotBody["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v, want reasoning.encrypted_content", gotBody["include"])
	}
	tools, ok := gotBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one bash tool", gotBody["tools"])
	}
	assertBashTool(t, tools[0])

	act, ok := resp.Turn.(*clnkr.ActTurn)
	if !ok {
		t.Fatalf("Turn = %T, want *ActTurn", resp.Turn)
	}
	if got := len(act.Bash.Commands); got != 2 {
		t.Fatalf("commands = %d, want 2", got)
	}
	if got := act.Bash.Commands[0]; got.ID != "call_1" || got.Command != "pwd" {
		t.Fatalf("first command = %#v, want provider ID and command", got)
	}
	if got := act.Bash.Commands[1]; got.ID != "call_2" || got.Command != "git status" {
		t.Fatalf("second command = %#v, want provider ID and command", got)
	}
	if len(resp.BashToolCalls) != 2 || resp.BashToolCalls[0].ID != "call_1" ||
		resp.BashToolCalls[1].ID != "call_2" {
		t.Fatalf("BashToolCalls = %#v", resp.BashToolCalls)
	}
	if len(resp.ProviderReplay) != 1 || resp.ProviderReplay[0].Type != "reasoning" {
		t.Fatalf("ProviderReplay = %#v", resp.ProviderReplay)
	}
}

func TestModelQueryToolCallsPreservesWorkdir(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"output": []map[string]any{
				{
					"type":      "function_call",
					"call_id":   "call_1",
					"name":      "bash",
					"arguments": `{"command":"pwd","workdir":"/tmp/project"}`,
					"status":    "completed",
				},
			},
			"usage": map[string]any{"input_tokens": 3, "output_tokens": 4},
		})
	}))
	defer server.Close()

	model := openairesponses.NewModelWithOptions(
		server.URL,
		"test-key",
		"gpt-5",
		"sys prompt",
		openairesponses.Options{UseBashToolCalls: true},
	)
	resp, err := model.Query(
		context.Background(),
		[]clnkr.Message{{Role: "user", Content: "where"}},
	)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	act, ok := resp.Turn.(*clnkr.ActTurn)
	if !ok {
		t.Fatalf("Turn = %T, want *ActTurn", resp.Turn)
	}
	if got := act.Bash.Commands[0]; got.Workdir != "/tmp/project" {
		t.Fatalf("command workdir = %q, want /tmp/project", got.Workdir)
	}
	if got := resp.BashToolCalls[0]; got.Workdir != "/tmp/project" {
		t.Fatalf("tool call workdir = %q, want /tmp/project", got.Workdir)
	}
}

func TestModelQueryToolCallsRejectsInvalidFunctionCalls(t *testing.T) {
	tests := []struct {
		name      string
		item      map[string]any
		wantErr   error
		wantError string
	}{
		{
			name: "missing call id",
			item: map[string]any{
				"type":      "function_call",
				"name":      "bash",
				"arguments": `{"command":"pwd","workdir":null}`,
				"status":    "completed",
			},
			wantErr:   clnkr.ErrInvalidJSON,
			wantError: "bash tool call missing call_id",
		},
		{
			name: "unsupported tool",
			item: map[string]any{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "python",
				"arguments": `{"command":"pwd","workdir":null}`,
				"status":    "completed",
			},
			wantErr:   clnkr.ErrInvalidJSON,
			wantError: `unsupported tool "python"`,
		},
		{
			name: "malformed arguments",
			item: map[string]any{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "bash",
				"arguments": `{`,
				"status":    "completed",
			},
			wantErr:   clnkr.ErrInvalidJSON,
			wantError: "malformed bash tool arguments",
		},
		{
			name: "unknown argument field",
			item: map[string]any{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "bash",
				"arguments": `{"command":"pwd","workdir":null,"extra":true}`,
				"status":    "completed",
			},
			wantErr:   clnkr.ErrInvalidJSON,
			wantError: "malformed bash tool arguments",
		},
		{
			name: "missing workdir",
			item: map[string]any{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "bash",
				"arguments": `{"command":"pwd"}`,
				"status":    "completed",
			},
			wantErr:   clnkr.ErrInvalidJSON,
			wantError: "bash tool arguments missing workdir",
		},
		{
			name: "empty command",
			item: map[string]any{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "bash",
				"arguments": `{"command":"  ","workdir":null}`,
				"status":    "completed",
			},
			wantErr:   clnkr.ErrMissingCommand,
			wantError: clnkr.ErrMissingCommand.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					writeJSON(t, w, map[string]any{
						"output": []map[string]any{tt.item},
						"usage":  map[string]any{"input_tokens": 3, "output_tokens": 4},
					})
				}),
			)
			defer server.Close()

			model := openairesponses.NewModelWithOptions(
				server.URL,
				"test-key",
				"gpt-5",
				"sys prompt",
				openairesponses.Options{UseBashToolCalls: true},
			)
			resp, err := model.Query(
				context.Background(),
				[]clnkr.Message{{Role: "user", Content: "where"}},
			)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if !errors.Is(resp.ProtocolErr, tt.wantErr) {
				t.Fatalf("ProtocolErr = %v, want %v", resp.ProtocolErr, tt.wantErr)
			}
			if !strings.Contains(resp.ProtocolErr.Error(), tt.wantError) {
				t.Fatalf(
					"ProtocolErr = %q, want containing %q",
					resp.ProtocolErr.Error(),
					tt.wantError,
				)
			}
			if resp.Usage.InputTokens != 3 || resp.Usage.OutputTokens != 4 {
				t.Fatalf("usage = %+v, want 3/4", resp.Usage)
			}
		})
	}
}

func TestModelQueryToolCallsRejectsInvalidOutputShape(t *testing.T) {
	tests := []struct {
		name             string
		output           []map[string]any
		wantQueryError   string
		wantProtocolErr  error
		wantProtocolText string
	}{
		{
			name: "mixed function call and text",
			output: []map[string]any{
				{
					"type":      "function_call",
					"call_id":   "call_1",
					"name":      "bash",
					"arguments": `{"command":"pwd","workdir":null}`,
					"status":    "completed",
				},
				{
					"type":    "message",
					"role":    "assistant",
					"content": []map[string]any{{"type": "output_text", "text": doneTurn("done")}},
				},
			},
			wantProtocolErr:  clnkr.ErrInvalidJSON,
			wantProtocolText: "mixed bash tool call and structured text",
		},
		{
			name: "missing output text and tool call",
			output: []map[string]any{
				{"type": "message", "role": "assistant", "content": []map[string]any{}},
			},
			wantQueryError: "tool-call response: no usable output_text or bash tool call",
		},
		{
			name: "text act turn",
			output: []map[string]any{
				{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{
						{
							"type": "output_text",
							"text": `{"turn":{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`,
						},
					},
				},
			},
			wantProtocolErr:  clnkr.ErrInvalidJSON,
			wantProtocolText: "tool-call mode does not accept text act turns",
		},
		{
			name: "refusal",
			output: []map[string]any{
				{
					"type":    "message",
					"role":    "assistant",
					"content": []map[string]any{{"type": "refusal", "refusal": "refused"}},
				},
			},
			wantQueryError: "tool-call refusal: refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					writeJSON(t, w, map[string]any{
						"output": tt.output,
						"usage":  map[string]any{"input_tokens": 3, "output_tokens": 4},
					})
				}),
			)
			defer server.Close()

			model := openairesponses.NewModelWithOptions(
				server.URL,
				"test-key",
				"gpt-5",
				"sys prompt",
				openairesponses.Options{UseBashToolCalls: true},
			)
			resp, err := model.Query(
				context.Background(),
				[]clnkr.Message{{Role: "user", Content: "where"}},
			)
			if tt.wantQueryError != "" {
				if err == nil || err.Error() != tt.wantQueryError {
					t.Fatalf("Query error = %v, want %q", err, tt.wantQueryError)
				}
				return
			}
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if !errors.Is(resp.ProtocolErr, tt.wantProtocolErr) {
				t.Fatalf("ProtocolErr = %v, want %v", resp.ProtocolErr, tt.wantProtocolErr)
			}
			if !strings.Contains(resp.ProtocolErr.Error(), tt.wantProtocolText) {
				t.Fatalf(
					"ProtocolErr = %q, want containing %q",
					resp.ProtocolErr.Error(),
					tt.wantProtocolText,
				)
			}
			if resp.Usage.InputTokens != 3 || resp.Usage.OutputTokens != 4 {
				t.Fatalf("usage = %+v, want 3/4", resp.Usage)
			}
		})
	}
}

func TestModelQueryToolCallsReplayToolMessagesWithoutDuplicateText(t *testing.T) {
	tests := []struct {
		name         string
		replay       json.RawMessage
		wantTypes    []string
		wantReasonID string
	}{
		{
			name: "keeps encrypted reasoning",
			replay: json.RawMessage(
				`{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque"}`,
			),
			wantTypes:    []string{"reasoning", "function_call", "function_call_output"},
			wantReasonID: "rs_1",
		},
		{
			name:      "skips reasoning without encrypted content",
			replay:    json.RawMessage(`{"type":"reasoning","id":"rs_1","summary":[]}`),
			wantTypes: []string{"function_call", "function_call_output"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBody map[string]any
			server := captureServer(t, &gotBody, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, responseWithText(0, 0, doneTurn("ok")))
			})
			defer server.Close()

			model := openairesponses.NewModelWithOptions(
				server.URL,
				"test-key",
				"gpt-5",
				"sys prompt",
				openairesponses.Options{UseBashToolCalls: true},
			)
			resp, err := model.Query(context.Background(), []clnkr.Message{
				{
					Role:    "assistant",
					Content: `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":"/tmp/project"}]}}`,
					BashToolCalls: []clnkr.BashToolCall{
						{ID: "call_1", Command: "pwd", Workdir: "/tmp/project"},
					},
					ProviderReplay: []clnkr.ProviderReplayItem{
						{
							Provider:    "openai",
							ProviderAPI: "openai-responses",
							Type:        "reasoning",
							JSON:        tt.replay,
						},
					},
				},
				{
					Role:           "user",
					Content:        "payload",
					BashToolResult: &clnkr.BashToolResult{ID: "call_1", Content: "payload"},
				},
			})
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if got, want := mustCanonicalTurn(
				t,
				resp.Turn,
			), canonicalDoneWithSummary(
				"ok",
			); got != want {
				t.Fatalf("canonical turn = %q, want %q", got, want)
			}
			if resp.ProtocolErr != nil {
				t.Fatalf("ProtocolErr = %v, want nil", resp.ProtocolErr)
			}
			gotInput := input(t, gotBody)
			if len(gotInput) != len(tt.wantTypes) {
				t.Fatalf("input = %#v, want %d items", gotInput, len(tt.wantTypes))
			}
			for i, wantType := range tt.wantTypes {
				item := gotInput[i].(map[string]any)
				if item["type"] != wantType {
					t.Fatalf("input[%d] = %#v, want type %q", i, item, wantType)
				}
			}
			lastTwo := gotInput[len(gotInput)-2:]
			functionCall := lastTwo[0].(map[string]any)
			if functionCall["call_id"] != "call_1" {
				t.Fatalf("function_call = %#v, want call_1", functionCall)
			}
			var arguments struct {
				Command string  `json:"command"`
				Workdir *string `json:"workdir"`
			}
			if err := json.Unmarshal(
				[]byte(functionCall["arguments"].(string)),
				&arguments,
			); err != nil {
				t.Fatalf("unmarshal function_call arguments: %v", err)
			}
			if arguments.Command != "pwd" || arguments.Workdir == nil ||
				*arguments.Workdir != "/tmp/project" {
				t.Fatalf("function_call arguments = %#v, want command and workdir", arguments)
			}
			output := lastTwo[1].(map[string]any)
			if output["call_id"] != "call_1" || output["output"] != "payload" {
				t.Fatalf("function_call_output = %#v, want payload for call_1", output)
			}
			if tt.wantReasonID != "" {
				reasoning := gotInput[0].(map[string]any)
				if reasoning["id"] != tt.wantReasonID ||
					reasoning["encrypted_content"] != "opaque" {
					t.Fatalf("reasoning = %#v, want opaque replay", reasoning)
				}
			}
		})
	}
}

func TestModelQueryFinalOmitsBashTool(t *testing.T) {
	var gotBody map[string]any
	server := captureServer(t, &gotBody, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, responseWithText(0, 0, doneTurn("final")))
	})
	defer server.Close()

	model := openairesponses.NewModelWithOptions(
		server.URL,
		"test-key",
		"gpt-5",
		"sys prompt",
		openairesponses.Options{UseBashToolCalls: true},
	)
	if _, err := model.QueryFinal(
		context.Background(),
		[]clnkr.Message{{Role: "user", Content: "summarize"}},
	); err != nil {
		t.Fatalf("QueryFinal: %v", err)
	}
	if _, ok := gotBody["tools"]; ok {
		t.Fatalf("tools = %#v, want omitted", gotBody["tools"])
	}
	schema := gotBody["text"].(map[string]any)["format"].(map[string]any)["schema"].(map[string]any)
	choices := schema["properties"].(map[string]any)["turn"].(map[string]any)["anyOf"].([]any)
	if len(choices) != 1 {
		t.Fatalf("final schema choices = %#v, want done only", choices)
	}
}
