package openairesponses_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/providers/openairesponses"
)

const (
	checkEvidence = "go test ./... passed and ls output showed current directory entries for completion"
	canonicalDone = `{"type":"done","summary":"canonical transcript","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"` + checkEvidence + `"}]},"known_risks":[]}`
	providerDone  = `{"turn":{"type":"done","bash":null,"question":null,"summary":"canonical transcript","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"` + checkEvidence + `"}]},"known_risks":[],"reasoning":null}}`
)

func TestModelQueryUsesResponsesStructuredRequest(t *testing.T) {
	var gotBody map[string]any
	var gotPath string
	server := captureServer(t, &gotBody, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		writeJSON(t, w, responseWithText(11, 7, `{"turn":{"type":"done","summary":"hello`, ` back","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"`+checkEvidence+`"}]},"known_risks":[],"reasoning":null}}`))
	})
	defer server.Close()

	model := openairesponses.NewModel(server.URL+"/v1", "test-key", "gpt-5", "sys prompt")
	resp, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if gotPath != "/v1/responses" {
		t.Fatalf("path = %q, want %q", gotPath, "/v1/responses")
	}
	assertNoDefaultOptions(t, gotBody)
	if got, want := gotBody["instructions"], "sys prompt"; got != want {
		t.Fatalf("instructions = %#v, want %q", got, want)
	}
	assertUserInput(t, inputAt(t, gotBody, 0), "hello")
	assertStructuredFormat(t, gotBody)
	if got, want := mustCanonicalTurn(t, resp.Turn), canonicalDoneWithSummary("hello back"); got != want {
		t.Fatalf("canonical turn = %q, want %q", got, want)
	}
	if resp.ProtocolErr != nil {
		t.Fatalf("ProtocolErr = %v, want nil", resp.ProtocolErr)
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v, want 11/7", resp.Usage)
	}
}

func TestModelQueryRetriesTransientServerError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusBadGateway)
			writeJSON(t, w, map[string]any{"error": map[string]any{"message": "context deadline exceeded"}})
			return
		}
		writeJSON(t, w, responseWithText(3, 4, doneTurn("ok")))
	}))
	defer server.Close()

	model := openairesponses.NewModel(server.URL, "test-key", "gpt-test", "system")
	resp, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "finish"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if got, want := mustCanonicalTurn(t, resp.Turn), canonicalDoneWithSummary("ok"); got != want {
		t.Fatalf("canonical turn = %q, want %q", got, want)
	}
}

func TestModelQueryDoesNotRetryBadRequest(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(t, w, map[string]any{"error": map[string]any{"message": "bad request"}})
	}))
	defer server.Close()

	model := openairesponses.NewModel(server.URL, "test-key", "gpt-test", "system")
	_, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "finish"}})
	if err == nil {
		t.Fatal("Query succeeded, want bad request error")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if got, want := err.Error(), "api error (status 400): bad request"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestModelQueryJoinsBaseURLPath(t *testing.T) {
	tests := []struct {
		name            string
		baseURLSuffix   string
		wantPath        string
		wantRawQuery    string
		wantEscapedPath string
	}{
		{name: "trailing slash", baseURLSuffix: "/v1/", wantPath: "/v1/responses"},
		{name: "preserves internal repeated slashes", baseURLSuffix: "/proxy//v1/", wantPath: "/proxy//v1/responses"},
		{name: "keeps query on base URL", baseURLSuffix: "/v1/?token=abc", wantPath: "/v1/responses", wantRawQuery: "token=abc"},
		{name: "preserves escaped slash", baseURLSuffix: "/proxy%2Fv1/", wantPath: "/proxy/v1/responses", wantEscapedPath: "/proxy%2Fv1/responses"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath, gotRawQuery, gotEscapedPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotRawQuery = r.URL.RawQuery
				gotEscapedPath = r.URL.EscapedPath()
				writeJSON(t, w, responseWithText(0, 0, doneTurn("ok")))
			}))
			defer server.Close()

			model := openairesponses.NewModel(server.URL+tt.baseURLSuffix, "test-key", "gpt-5", "sys prompt")
			if _, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}}); err != nil {
				t.Fatalf("Query: %v", err)
			}
			if gotPath != tt.wantPath {
				t.Fatalf("path = %q, want %q", gotPath, tt.wantPath)
			}
			if gotRawQuery != tt.wantRawQuery {
				t.Fatalf("raw query = %q, want %q", gotRawQuery, tt.wantRawQuery)
			}
			if tt.wantEscapedPath != "" && gotEscapedPath != tt.wantEscapedPath {
				t.Fatalf("escaped path = %q, want %q", gotEscapedPath, tt.wantEscapedPath)
			}
		})
	}
}

func TestModelQuerySerializesProviderRequestOptions(t *testing.T) {
	var gotBody map[string]any
	server := captureServer(t, &gotBody, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, responseWithText(0, 0, doneTurn("ok")))
	})
	defer server.Close()

	model := openairesponses.NewModelWithOptions(server.URL, "test-key", "gpt-5.1", "sys", openairesponses.Options{
		ReasoningEffort:    "high",
		MaxOutputTokens:    8000,
		HasMaxOutputTokens: true,
	})
	if _, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("Query: %v", err)
	}

	reasoning, ok := gotBody["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning = %#v, want object", gotBody["reasoning"])
	}
	if got, want := reasoning["effort"], "high"; got != want {
		t.Fatalf("reasoning.effort = %#v, want %q", got, want)
	}
	if got, want := gotBody["max_output_tokens"], float64(8000); got != want {
		t.Fatalf("max_output_tokens = %#v, want %v", got, want)
	}
}

func TestModelQueryRejectsContradictoryStructuredTurn(t *testing.T) {
	content := `{"turn":{"type":"done","bash":{"commands":[{"command":"rm -rf tmp","workdir":null}]},"question":null,"summary":"done","reasoning":null}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, responseWithText(11, 7, content))
	}))
	defer server.Close()

	model := openairesponses.NewModel(server.URL, "test-key", "gpt-test", "sys")
	resp, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if resp.Raw != content {
		t.Fatalf("Raw = %q, want %q", resp.Raw, content)
	}
	if !errors.Is(resp.ProtocolErr, clnkr.ErrInvalidJSON) {
		t.Fatalf("ProtocolErr = %v, want ErrInvalidJSON", resp.ProtocolErr)
	}
}

func TestModelQueriesNormalizeAssistantHistory(t *testing.T) {
	history := []clnkr.Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: canonicalDone},
	}
	tests := []struct {
		name  string
		query func(*openairesponses.Model) error
		want  string
	}{
		{
			name: "structured",
			query: func(model *openairesponses.Model) error {
				_, err := model.Query(context.Background(), history)
				return err
			},
			want: doneTurn("ok"),
		},
		{
			name: "text",
			query: func(model *openairesponses.Model) error {
				summary, err := model.QueryText(context.Background(), history)
				if summary != "Older work summarized." {
					t.Fatalf("summary = %q, want %q", summary, "Older work summarized.")
				}
				return err
			},
			want: "Older work summarized.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBody map[string]any
			server := captureServer(t, &gotBody, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, responseWithText(9, 4, tt.want))
			})
			defer server.Close()

			model := openairesponses.NewModel(server.URL, "test-key", "gpt-5", "sys prompt")
			if err := tt.query(model); err != nil {
				t.Fatalf("query: %v", err)
			}
			if tt.name == "text" {
				if _, ok := gotBody["text"]; ok {
					t.Fatalf("text should be omitted for QueryText, got %#v", gotBody["text"])
				}
				assertNoDefaultOptions(t, gotBody)
			}
			assertUserInput(t, inputAt(t, gotBody, 0), "first task")
			assertAssistantInput(t, inputAt(t, gotBody, 1), "msg_prev_1")
		})
	}
}

func TestModelQueriesReturnRefusalError(t *testing.T) {
	tests := []struct {
		name      string
		query     func(*openairesponses.Model) error
		wantError string
	}{
		{
			name: "structured",
			query: func(model *openairesponses.Model) error {
				_, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}})
				return err
			},
			wantError: "structured output refusal: refused",
		},
		{
			name: "text",
			query: func(model *openairesponses.Model) error {
				_, err := model.QueryText(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}})
				return err
			},
			wantError: "free-form refusal: refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, responseWithRefusal("refused"))
			}))
			defer server.Close()

			model := openairesponses.NewModel(server.URL, "test-key", "gpt-5", "sys prompt")
			err := tt.query(model)
			if err == nil {
				t.Fatal("expected refusal error")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %q, want %q", err.Error(), tt.wantError)
			}
		})
	}
}

func TestModelQueryErrorsOnMissingOutputText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"output": []map[string]any{{"type": "message", "role": "assistant", "content": []map[string]any{}}}})
	}))
	defer server.Close()

	model := openairesponses.NewModel(server.URL, "test-key", "gpt-5", "sys prompt")
	_, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "hello"}})
	if err == nil {
		t.Fatal("expected missing output_text error")
	}
	if err.Error() != "no usable output_text in response" {
		t.Fatalf("error = %q, want %q", err.Error(), "no usable output_text in response")
	}
}

func TestModelQueryToolCalls(t *testing.T) {
	var gotBody map[string]any
	server := captureServer(t, &gotBody, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"output": []map[string]any{
				{"type": "reasoning", "id": "rs_1", "summary": []any{}},
				{"type": "function_call", "call_id": "call_1", "name": "bash", "arguments": `{"command":"pwd","workdir":null}`, "status": "completed"},
				{"type": "function_call", "call_id": "call_2", "name": "bash", "arguments": `{"command":"git status","workdir":null}`, "status": "completed"},
			},
			"usage": map[string]any{"input_tokens": 3, "output_tokens": 4},
		})
	})
	defer server.Close()

	model := openairesponses.NewModelWithOptions(server.URL, "test-key", "gpt-5", "sys prompt", openairesponses.Options{UseBashToolCalls: true})
	resp, err := model.Query(context.Background(), []clnkr.Message{{Role: "user", Content: "where"}})
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
	if len(resp.BashToolCalls) != 2 || resp.BashToolCalls[0].ID != "call_1" || resp.BashToolCalls[1].ID != "call_2" {
		t.Fatalf("BashToolCalls = %#v", resp.BashToolCalls)
	}
	if len(resp.ProviderReplay) != 1 || resp.ProviderReplay[0].Type != "reasoning" {
		t.Fatalf("ProviderReplay = %#v", resp.ProviderReplay)
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
			name:         "keeps encrypted reasoning",
			replay:       json.RawMessage(`{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque"}`),
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

			model := openairesponses.NewModelWithOptions(server.URL, "test-key", "gpt-5", "sys prompt", openairesponses.Options{UseBashToolCalls: true})
			_, err := model.Query(context.Background(), []clnkr.Message{
				{Role: "assistant", Content: `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]}}`, BashToolCalls: []clnkr.BashToolCall{{ID: "call_1", Command: "pwd"}}, ProviderReplay: []clnkr.ProviderReplayItem{{
					Provider: "openai", ProviderAPI: "openai-responses", Type: "reasoning", JSON: tt.replay,
				}}},
				{Role: "user", Content: "payload", BashToolResult: &clnkr.BashToolResult{ID: "call_1", Content: "payload"}},
			})
			if err != nil {
				t.Fatalf("Query: %v", err)
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
			output := lastTwo[1].(map[string]any)
			if output["call_id"] != "call_1" || output["output"] != "payload" {
				t.Fatalf("function_call_output = %#v, want payload for call_1", output)
			}
			if tt.wantReasonID != "" {
				reasoning := gotInput[0].(map[string]any)
				if reasoning["id"] != tt.wantReasonID || reasoning["encrypted_content"] != "opaque" {
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

	model := openairesponses.NewModelWithOptions(server.URL, "test-key", "gpt-5", "sys prompt", openairesponses.Options{UseBashToolCalls: true})
	if _, err := model.QueryFinal(context.Background(), []clnkr.Message{{Role: "user", Content: "summarize"}}); err != nil {
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

func captureServer(t *testing.T, gotBody *map[string]any, handler func(http.ResponseWriter, *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if err := json.Unmarshal(body, gotBody); err != nil {
			t.Fatalf("Unmarshal request: %v", err)
		}
		handler(w, r)
	}))
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("Encode response: %v", err)
	}
}

func responseWithText(inputTokens, outputTokens int, texts ...string) map[string]any {
	content := make([]map[string]any, 0, len(texts))
	for _, text := range texts {
		content = append(content, map[string]any{"type": "output_text", "text": text})
	}
	return map[string]any{
		"output": []map[string]any{{"type": "message", "role": "assistant", "content": content}},
		"usage":  map[string]any{"input_tokens": inputTokens, "output_tokens": outputTokens},
	}
}

func responseWithRefusal(refusal string) map[string]any {
	return map[string]any{"output": []map[string]any{{
		"type": "message", "role": "assistant",
		"content": []map[string]any{{"type": "refusal", "refusal": refusal}},
	}}}
}

func doneTurn(summary string) string {
	return `{"turn":{"type":"done","summary":"` + summary + `","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"` + checkEvidence + `"}]},"known_risks":[],"reasoning":null}}`
}

func canonicalDoneWithSummary(summary string) string {
	return `{"type":"done","summary":"` + summary + `","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"` + checkEvidence + `"}]},"known_risks":[]}`
}

func input(t *testing.T, body map[string]any) []any {
	t.Helper()
	got, ok := body["input"].([]any)
	if !ok {
		t.Fatalf("input = %#v, want array", body["input"])
	}
	return got
}

func inputAt(t *testing.T, body map[string]any, i int) map[string]any {
	t.Helper()
	messages := input(t, body)
	if len(messages) <= i {
		t.Fatalf("input = %#v, want index %d", messages, i)
	}
	msg, ok := messages[i].(map[string]any)
	if !ok {
		t.Fatalf("input[%d] = %#v, want map", i, messages[i])
	}
	return msg
}

func contentAt(t *testing.T, msg map[string]any, i int) map[string]any {
	t.Helper()
	content, ok := msg["content"].([]any)
	if !ok || len(content) <= i {
		t.Fatalf("content = %#v, want index %d", msg["content"], i)
	}
	item, ok := content[i].(map[string]any)
	if !ok {
		t.Fatalf("content[%d] = %#v, want map", i, content[i])
	}
	return item
}

func assertUserInput(t *testing.T, msg map[string]any, text string) {
	t.Helper()
	if got, want := msg["role"], "user"; got != want {
		t.Fatalf("user role = %#v, want %q", got, want)
	}
	if got, want := msg["type"], "message"; got != want {
		t.Fatalf("user type = %#v, want %q", got, want)
	}
	item := contentAt(t, msg, 0)
	if got, want := item["type"], "input_text"; got != want {
		t.Fatalf("user content type = %#v, want %q", got, want)
	}
	if got := item["text"]; got != text {
		t.Fatalf("user text = %#v, want %q", got, text)
	}
	if _, ok := item["annotations"]; ok {
		t.Fatalf("user annotations = %#v, want omitted", item["annotations"])
	}
}

func assertAssistantInput(t *testing.T, msg map[string]any, id string) {
	t.Helper()
	if got, want := msg["type"], "message"; got != want {
		t.Fatalf("assistant type = %#v, want %q", got, want)
	}
	if got := msg["id"]; got != id {
		t.Fatalf("assistant id = %#v, want %q", got, id)
	}
	if got, want := msg["role"], "assistant"; got != want {
		t.Fatalf("assistant role = %#v, want %q", got, want)
	}
	if got, want := msg["status"], "completed"; got != want {
		t.Fatalf("assistant status = %#v, want %q", got, want)
	}
	item := contentAt(t, msg, 0)
	if got, want := item["type"], "output_text"; got != want {
		t.Fatalf("assistant content type = %#v, want %q", got, want)
	}
	if got := item["text"]; got != providerDone {
		t.Fatalf("assistant text = %#v, want %q", got, providerDone)
	}
	annotations, ok := item["annotations"].([]any)
	if !ok || len(annotations) != 0 {
		t.Fatalf("assistant annotations = %#v, want empty array", item["annotations"])
	}
}

func assertNoDefaultOptions(t *testing.T, body map[string]any) {
	t.Helper()
	if _, ok := body["reasoning"]; ok {
		t.Fatalf("reasoning = %#v, want omitted by default", body["reasoning"])
	}
	if _, ok := body["max_output_tokens"]; ok {
		t.Fatalf("max_output_tokens = %#v, want omitted by default", body["max_output_tokens"])
	}
}

func assertStructuredFormat(t *testing.T, body map[string]any) {
	t.Helper()
	text, ok := body["text"].(map[string]any)
	if !ok {
		t.Fatalf("text = %#v, want map", body["text"])
	}
	format, ok := text["format"].(map[string]any)
	if !ok {
		t.Fatalf("text.format = %#v, want map", text["format"])
	}
	for key, want := range map[string]any{"type": "json_schema", "name": "agent_turn", "strict": true} {
		if got := format[key]; got != want {
			t.Fatalf("text.format.%s = %#v, want %#v", key, got, want)
		}
	}
	if _, ok := format["schema"].(map[string]any); !ok {
		t.Fatalf("text.format.schema = %#v, want schema object", format["schema"])
	}
}

func assertBashTool(t *testing.T, raw any) {
	t.Helper()
	tool, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("tool = %#v, want object", raw)
	}
	if tool["type"] != "function" || tool["name"] != "bash" || tool["strict"] != true {
		t.Fatalf("tool = %#v, want strict bash function", tool)
	}
	params := tool["parameters"].(map[string]any)
	if params["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v, want false", params["additionalProperties"])
	}
	required := params["required"].([]any)
	if len(required) != 2 || required[0] != "command" || required[1] != "workdir" {
		t.Fatalf("required = %#v, want command/workdir", required)
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
