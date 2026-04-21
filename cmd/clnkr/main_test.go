package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	clnkr "github.com/clnkr-ai/clnkr"
)

func actJSON(command string) string {
	return fmt.Sprintf(`{"type":"act","bash":{"commands":[{"command":%q,"workdir":null}]}}`, command)
}

func mustTurn(raw string) clnkr.Turn {
	turn, err := clnkr.ParseTurn(raw)
	if err != nil {
		panic(err)
	}
	return turn
}

func mustResponse(raw string) clnkr.Response {
	return clnkr.Response{Turn: mustTurn(raw), Raw: raw}
}

type fakeModel struct {
	responses []clnkr.Response
	calls     int
	mu        sync.Mutex
}

func (m *fakeModel) Query(_ context.Context, _ []clnkr.Message) (clnkr.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.calls >= len(m.responses) {
		return clnkr.Response{}, fmt.Errorf("no more responses")
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

func (m *fakeModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

type fakeExecutor struct {
	results []clnkr.CommandResult
	errs    []error
	calls   int
	mu      sync.Mutex
}

func (e *fakeExecutor) Execute(_ context.Context, command, _ string) (clnkr.CommandResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.calls >= len(e.results) {
		return clnkr.CommandResult{}, fmt.Errorf("no more results")
	}
	result := e.results[e.calls]
	if result.Command == "" {
		result.Command = command
	}
	var err error
	if e.calls < len(e.errs) {
		err = e.errs[e.calls]
	}
	e.calls++
	return result, err
}

func (e *fakeExecutor) SetEnv(map[string]string) {}

func (e *fakeExecutor) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type clarifyReply struct {
	text string
	err  error
}

type scriptPrompter struct {
	actReplies     []clarifyReply
	clarifications []clarifyReply
	actReplyCalls  int
	clarifyCalls   int
}

func (p *scriptPrompter) ActReply(context.Context, string) (string, error) {
	if p.actReplyCalls >= len(p.actReplies) {
		return "", fmt.Errorf("no more act replies")
	}
	reply := p.actReplies[p.actReplyCalls]
	p.actReplyCalls++
	return reply.text, reply.err
}

func (p *scriptPrompter) Clarify(context.Context, string) (string, error) {
	if p.clarifyCalls >= len(p.clarifications) {
		return "", fmt.Errorf("no more clarification replies")
	}
	reply := p.clarifications[p.clarifyCalls]
	p.clarifyCalls++
	return reply.text, reply.err
}

func TestRunPlainApprovalNonApprovalReplyBecomesGuidance(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(actJSON("rm important.txt")),
		mustResponse(`{"type":"done","summary":"okay"}`),
	}}
	executor := &fakeExecutor{}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	prompter := &scriptPrompter{
		actReplies: []clarifyReply{{text: "list files instead"}},
	}

	err := runPlainApproval(context.Background(), agent, "do it", prompter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executor.calls != 0 {
		t.Fatalf("non-approval reply should not execute")
	}
	msgs := agent.Messages()
	if len(msgs) == 0 || msgs[len(msgs)-2].Content != "list files instead" {
		t.Fatalf("guidance was not appended: %#v", msgs)
	}
}

func TestDefaultAnthropicModel(t *testing.T) {
	if defaultAnthropicModel != "claude-sonnet-4-6" {
		t.Fatalf("defaultAnthropicModel = %q, want %q", defaultAnthropicModel, "claude-sonnet-4-6")
	}
}

func TestUsageTextIncludesDefaultAnthropicModel(t *testing.T) {
	if !strings.Contains(usageText(), defaultAnthropicModel) {
		t.Fatalf("usageText() does not mention defaultAnthropicModel %q", defaultAnthropicModel)
	}
}

func TestResolveModelValue(t *testing.T) {
	tests := []struct {
		name     string
		flag     string
		env      string
		expected string
	}{
		{name: "flag wins", flag: "flag-model", env: "env-model", expected: "flag-model"},
		{name: "env used when flag empty", env: "env-model", expected: "env-model"},
		{name: "default used when flag and env empty", expected: defaultAnthropicModel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveModelValue(tt.flag, tt.env); got != tt.expected {
				t.Fatalf("resolveModelValue(%q, %q) = %q, want %q", tt.flag, tt.env, got, tt.expected)
			}
		})
	}
}

func TestParseProviderMode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    providerMode
		wantErr string
	}{
		{name: "default auto", input: "", want: providerAuto},
		{name: "auto", input: "auto", want: providerAuto},
		{name: "anthropic", input: "anthropic", want: providerAnthropic},
		{name: "openai", input: "openai", want: providerOpenAI},
		{name: "invalid", input: "other", wantErr: `invalid provider "other" (allowed: auto, anthropic, openai)`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProviderMode(tt.input)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseProviderMode(%q) err = %v, want %q", tt.input, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseProviderMode(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("parseProviderMode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveProviderSelection(t *testing.T) {
	tests := []struct {
		name          string
		mode          providerMode
		baseURL       string
		baseURLSource string
		want          providerMode
		wantErr       string
	}{
		{
			name:          "auto uses parsed host not raw substring",
			mode:          providerAuto,
			baseURL:       "https://proxy.example.com/anthropic.com/messages",
			baseURLSource: "--base-url flag",
			want:          providerOpenAI,
		},
		{
			name:          "auto picks anthropic host",
			mode:          providerAuto,
			baseURL:       "https://api.anthropic.com/v1/messages",
			baseURLSource: "--base-url flag",
			want:          providerAnthropic,
		},
		{
			name:          "explicit anthropic overrides host",
			mode:          providerAnthropic,
			baseURL:       "https://proxy.example.com/v1",
			baseURLSource: "--base-url flag",
			want:          providerAnthropic,
		},
		{
			name:          "explicit openai overrides anthropic host",
			mode:          providerOpenAI,
			baseURL:       "https://api.anthropic.com/v1/messages",
			baseURLSource: "--base-url flag",
			want:          providerOpenAI,
		},
		{
			name:          "explicit openai rejects built in anthropic default",
			mode:          providerOpenAI,
			baseURL:       defaultBaseURL,
			baseURLSource: "default",
			wantErr:       `provider "openai" requires --base-url or CLNKR_BASE_URL; refusing built-in default https://api.anthropic.com`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveProviderSelection(tt.mode, tt.baseURL, tt.baseURLSource)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("resolveProviderSelection(%q, %q, %q) err = %v, want %q", tt.mode, tt.baseURL, tt.baseURLSource, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveProviderSelection(%q, %q, %q): %v", tt.mode, tt.baseURL, tt.baseURLSource, err)
			}
			if got != tt.want {
				t.Fatalf("resolveProviderSelection(%q, %q, %q) = %q, want %q", tt.mode, tt.baseURL, tt.baseURLSource, got, tt.want)
			}
		})
	}
}

func TestResolveAPIKeyByProvider(t *testing.T) {
	tests := []struct {
		name       string
		mode       providerMode
		clnkrKey   string
		anthroKey  string
		wantAPIKey string
	}{
		{name: "clnkr key wins", mode: providerAnthropic, clnkrKey: "clnkr-key", anthroKey: "anthropic-key", wantAPIKey: "clnkr-key"},
		{name: "anthropic fallback allowed", mode: providerAnthropic, anthroKey: "anthropic-key", wantAPIKey: "anthropic-key"},
		{name: "openai ignores anthropic fallback", mode: providerOpenAI, anthroKey: "anthropic-key", wantAPIKey: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveAPIKey(tt.mode, tt.clnkrKey, tt.anthroKey); got != tt.wantAPIKey {
				t.Fatalf("resolveAPIKey(%q, %q, %q) = %q, want %q", tt.mode, tt.clnkrKey, tt.anthroKey, got, tt.wantAPIKey)
			}
		})
	}
}

func TestMissingAPIKeyMessageByProvider(t *testing.T) {
	anthropicMessage := missingAPIKeyMessage(providerAnthropic)
	if !strings.Contains(anthropicMessage, "ANTHROPIC_API_KEY") {
		t.Fatalf("anthropic missing key message should mention ANTHROPIC_API_KEY, got %q", anthropicMessage)
	}

	openAIMessage := missingAPIKeyMessage(providerOpenAI)
	if strings.Contains(openAIMessage, "ANTHROPIC_API_KEY") {
		t.Fatalf("openai missing key message should not mention ANTHROPIC_API_KEY, got %q", openAIMessage)
	}
}

func TestRunPlainApprovalEmptyActReplyIsNoOp(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(actJSON("rm important.txt")),
	}}
	executor := &fakeExecutor{}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	prompter := &scriptPrompter{
		actReplies: []clarifyReply{
			{text: ""},
			{err: errApprovalPending},
		},
	}

	err := runPlainApproval(context.Background(), agent, "do it", prompter)
	if !errors.Is(err, errApprovalPending) {
		t.Fatalf("got %v, want errApprovalPending", err)
	}
	if executor.calls != 0 {
		t.Fatalf("empty act reply should not execute")
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

	compactor := makeCompactorFactory(providerOpenAI, server.URL, "test-key", "gpt-test")("")
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

	compactor := makeCompactorFactory(providerAnthropic, server.URL, "test-key", "claude-test")("")
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

func TestStdinPrompterActReplyWritesPromptToStderr(t *testing.T) {
	stderr := captureStderr(t, func() {
		p := &stdinPrompter{reader: newLineReader(strings.NewReader("y\n"))}
		reply, err := p.ActReply(context.Background(), "rm important.txt")
		if err != nil {
			t.Fatalf("ActReply: %v", err)
		}
		if reply != "y" {
			t.Fatalf("reply = %q, want y", reply)
		}
	})

	if !strings.Contains(stderr, "rm important.txt") {
		t.Fatalf("stderr should contain command, got %q", stderr)
	}
	if !strings.Contains(stderr, "Send 'y' to approve, or type what the agent should do instead: ") {
		t.Fatalf("stderr should contain approval prompt, got %q", stderr)
	}
}

func TestStdinPrompterActReplyCanBeCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	captureStderr(t, func() {
		p := &stdinPrompter{reader: &lineReader{lines: make(chan lineResult)}}
		_, err := p.ActReply(ctx, "rm important.txt")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	})
}

func TestStdinPrompterActReplyShowsWorkdir(t *testing.T) {
	stderr := captureStderr(t, func() {
		p := &stdinPrompter{reader: newLineReader(strings.NewReader("y\n"))}
		reply, err := p.ActReply(context.Background(), formatActProposal([]clnkr.BashAction{{Command: "rm important.txt", Workdir: "subdir"}}))
		if err != nil {
			t.Fatalf("ActReply: %v", err)
		}
		if reply != "y" {
			t.Fatalf("reply = %q, want y", reply)
		}
	})

	if !strings.Contains(stderr, "rm important.txt in subdir") {
		t.Fatalf("stderr should contain workdir note, got %q", stderr)
	}
}

func TestFormatActProposal(t *testing.T) {
	got := formatActProposal([]clnkr.BashAction{
		{Command: "pwd"},
		{Command: "go test ./...", Workdir: "subdir"},
	})

	want := "1. pwd\n2. go test ./... in subdir"
	if got != want {
		t.Fatalf("formatActProposal() = %q, want %q", got, want)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() {
		os.Stderr = old
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close write pipe: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close read pipe: %v", err)
	}
	return string(data)
}
