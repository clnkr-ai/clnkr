package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	clnkr "github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/session"
)

func actJSON(command string) string {
	return fmt.Sprintf(`{"type":"act","bash":{"commands":[{"command":%q,"workdir":null}]}}`, command)
}

func openAIWrappedDone(summary string) string {
	return fmt.Sprintf(`{"turn":{"type":"done","bash":null,"question":null,"summary":%q,"reasoning":null}}`, summary)
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

func mustCanonicalDoneText(summary string) string {
	text, err := clnkr.CanonicalTurnJSON(&clnkr.DoneTurn{Summary: summary})
	if err != nil {
		panic(err)
	}
	return text
}

func mustCanonicalTurn(t *testing.T, turn clnkr.Turn) string {
	t.Helper()

	text, err := clnkr.CanonicalTurnJSON(turn)
	if err != nil {
		t.Fatalf("CanonicalTurnJSON: %v", err)
	}
	return text
}

func runDoneTranscript(t *testing.T, summary string) []clnkr.Message {
	t.Helper()

	agent := clnkr.NewAgent(&fakeModel{responses: []clnkr.Response{
		mustResponse(mustCanonicalDoneText(summary)),
	}}, &fakeExecutor{}, "/tmp")
	if err := agent.Run(context.Background(), "finish"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return agent.Messages()
}

type fakeModel struct {
	responses []clnkr.Response
	calls     int
}

func (m *fakeModel) Query(_ context.Context, _ []clnkr.Message) (clnkr.Response, error) {
	if m.calls >= len(m.responses) {
		return clnkr.Response{}, fmt.Errorf("no more responses")
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

type fakeExecutor struct {
	results []clnkr.CommandResult
	errs    []error
	calls   int
}

func (e *fakeExecutor) Execute(_ context.Context, command, _ string) (clnkr.CommandResult, error) {
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

type fakeCompactor struct {
	summary  string
	err      error
	calls    int
	messages []clnkr.Message
}

func (c *fakeCompactor) Summarize(_ context.Context, messages []clnkr.Message) (string, error) {
	c.calls++
	c.messages = append([]clnkr.Message{}, messages...)
	if c.err != nil {
		return "", c.err
	}
	return c.summary, nil
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

func runMainHelper(t *testing.T, args ...string) (bytes.Buffer, bytes.Buffer, error) {
	t.Helper()
	return runMainHelperWithEnv(t, nil, args...)
}

func runMainHelperWithEnv(t *testing.T, env []string, args ...string) (bytes.Buffer, bytes.Buffer, error) {
	t.Helper()

	return runMainHelperWithEnvAndInput(t, env, nil, args...)
}

func runMainHelperWithEnvAndInput(t *testing.T, env []string, stdin io.Reader, args ...string) (bytes.Buffer, bytes.Buffer, error) {
	t.Helper()

	cmdArgs := append([]string{"-test.run=TestMainHelper", "--"}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Env = append(withoutCLNKREnv(os.Environ()), "CLNKR_HELPER_MAIN=1")
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	return stdout, stderr, cmd.Run()
}

func withoutCLNKREnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		if !strings.HasPrefix(entry, "CLNKR_") {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func TestUsageMentionsProviderAPI(t *testing.T) {
	stdout, stderr, err := runMainHelper(t, "--help")
	if err != nil {
		t.Fatalf("help command: %v\nstderr: %s", err, stderr.String())
	}
	for _, want := range []string{"provider-api", "Limit executed commands", "before summary (default: 100)", "infers provider when provider is unset", "anthropic base URL  https://api.anthropic.com"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("usage output missing %q, got %q", want, stdout.String())
		}
	}
}

func TestHelpWritesUsageToStdout(t *testing.T) {
	stdout, stderr, err := runMainHelper(t, "--help")
	if err != nil {
		t.Fatalf("help command: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "provider-api") {
		t.Fatalf("stdout = %q, want usage", stdout.String())
	}
	for _, line := range strings.Split(stdout.String(), "\n") {
		if len(line) > 79 {
			t.Fatalf("help line length = %d, want <= 79: %q", len(line), line)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestInvalidFlagKeepsUsageOffStdout(t *testing.T) {
	stdout, stderr, err := runMainHelper(t, "--bogus")
	if err == nil {
		t.Fatal("invalid flag succeeded, want failure")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Run 'clnkr --help'") {
		t.Fatalf("stderr = %q, want help hint", stderr.String())
	}
	if got := strings.Count(stderr.String(), "flag provided but not defined"); got != 1 {
		t.Fatalf("stderr repeated flag error %d times: %q", got, stderr.String())
	}
}

func TestDumpSystemPromptDoesNotRequireProviderConfig(t *testing.T) {
	stdout, stderr, err := runMainHelper(t,
		"--no-system-prompt",
		"--system-prompt-append", "custom prompt",
		"--dump-system-prompt",
	)
	if err != nil {
		t.Fatalf("dump system prompt: %v\nstderr: %s", err, stderr.String())
	}
	if stdout.String() != "custom prompt" {
		t.Fatalf("stdout = %q, want custom prompt", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestTrajectoryRequiresPromptBeforeProviderConfig(t *testing.T) {
	stdout, stderr, err := runMainHelper(t, "--trajectory", filepath.Join(t.TempDir(), "trajectory.json"))
	if err == nil {
		t.Fatalf("trajectory without prompt succeeded; stdout: %s stderr: %s", stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--trajectory requires -p") {
		t.Fatalf("stderr = %q, want trajectory validation", stderr.String())
	}
	if strings.Contains(stderr.String(), "provider is required") {
		t.Fatalf("stderr = %q, provider validation ran first", stderr.String())
	}
}

func TestMainHelper(t *testing.T) {
	if os.Getenv("CLNKR_HELPER_MAIN") == "" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"clnkr"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	t.Fatal("missing helper arg separator")
}

func TestAliasedStringPrefersExplicitPreferredValue(t *testing.T) {
	if got := aliasedString("long", "short"); got != "long" {
		t.Fatalf("aliasedString preferred = %q, want long", got)
	}
	if got := aliasedString("", "short"); got != "short" {
		t.Fatalf("aliasedString fallback = %q, want short", got)
	}
	if got := aliasedString("  ", "long"); got != "long" {
		t.Fatalf("aliasedString whitespace fallback = %q, want long", got)
	}
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
						{"type": "output_text", "text": `{"turn":{"type":"done","summary":"ok","reasoning":null}}`},
					},
				},
			},
			"usage": map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer server.Close()

	model := newModelForConfig(providerConfig{
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
	if got := mustCanonicalTurn(t, resp.Turn); got != `{"type":"done","summary":"ok"}` {
		t.Fatalf("canonical turn = %q, want %q", got, `{"type":"done","summary":"ok"}`)
	}
}

func TestCommandProgressWritesToStderr(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		content := openAIWrappedDone("done")
		if calls == 1 {
			content = `{"turn":{"type":"act","bash":{"commands":[{"command":"printf %s \"$COMMAND_OUTPUT_SENTINEL\"; printf err-no-newline >&2","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": content}},
			},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer server.Close()

	const sentinel = "host-output"
	stdout, stderr, err := runMainHelperWithEnv(t, []string{
		"CLNKR_API_KEY=test-key",
		"COMMAND_OUTPUT_SENTINEL=" + sentinel,
	},
		"--provider", "openai",
		"--provider-api", "openai-chat-completions",
		"--base-url", server.URL,
		"--model", "gpt-test",
		"--full-send",
		"-p", "say hi",
	)
	if err != nil {
		t.Fatalf("run main: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	wantStdout := sentinel + "\ndone\n"
	if stdout.String() != wantStdout {
		t.Fatalf("stdout = %q, want command output and final summary %q", stdout.String(), wantStdout)
	}
	if strings.Contains(stderr.String(), sentinel) {
		t.Fatalf("stderr contains command output: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), `{"type":"act"`) {
		t.Fatalf("stderr contains non-verbose model response: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), `{"type":"done","summary":"done"}`) {
		t.Fatalf("stderr contains final summary response: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "err-no-newline--- done ---") {
		t.Fatalf("stderr concatenates command stderr with done marker: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "err-no-newline\n--- done ---") {
		t.Fatalf("stderr = %q, want separated command stderr and done marker", stderr.String())
	}
	for _, marker := range []string{"--- running:", "--- done ---"} {
		if strings.Contains(stdout.String(), marker) {
			t.Fatalf("stdout contains progress marker %q: %q", marker, stdout.String())
		}
		if !strings.Contains(stderr.String(), marker) {
			t.Fatalf("stderr = %q, want progress marker %q", stderr.String(), marker)
		}
	}
	if calls != 2 {
		t.Fatalf("model calls = %d, want 2", calls)
	}
}

func TestCommandProgressShowsWorkdir(t *testing.T) {
	workdir := t.TempDir()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		content := fmt.Sprintf(`{"turn":{"type":"act","bash":{"commands":[{"command":"pwd","workdir":%q}]},"question":null,"summary":null,"reasoning":null}}`, workdir)
		if calls > 1 {
			content = `{"turn":{"type":"done","bash":null,"question":null,"summary":"done","reasoning":null}}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": content}},
			},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer server.Close()

	stdout, stderr, err := runMainHelperWithEnv(t, []string{"CLNKR_API_KEY=test-key"},
		"--provider", "openai",
		"--provider-api", "openai-chat-completions",
		"--base-url", server.URL,
		"--model", "gpt-test",
		"--max-steps", "1",
		"--full-send",
		"-p", "pwd elsewhere",
	)
	if err != nil {
		t.Fatalf("run main: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "--- running: pwd in "+workdir+" ---") {
		t.Fatalf("stderr = %q, want progress workdir", stderr.String())
	}
}

func TestStepLimitInvalidSummaryExitsNonzero(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		content := `{"turn":{"type":"act","bash":{"commands":[{"command":"printf reached-limit","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`
		if calls > 1 {
			content = `not-json`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": content}},
			},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer server.Close()

	stdout, stderr, err := runMainHelperWithEnv(t, []string{"CLNKR_API_KEY=test-key"},
		"--provider", "openai",
		"--provider-api", "openai-chat-completions",
		"--base-url", server.URL,
		"--model", "gpt-test",
		"--max-steps", "1",
		"--full-send",
		"-p", "hit the limit",
	)
	if err == nil {
		t.Fatalf("run main succeeded; stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Error: query model (final):") {
		t.Fatalf("stderr = %q, want final query error", stderr.String())
	}
}

func TestSingleTaskFullSendClarificationWritesQuestionToStdout(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": `{"turn":{"type":"clarify","bash":null,"question":"Which repo?","summary":null,"reasoning":null}}`}},
			},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer server.Close()

	stdout, stderr, err := runMainHelperWithEnv(t, []string{"CLNKR_API_KEY=test-key"},
		"--provider", "openai",
		"--provider-api", "openai-chat-completions",
		"--base-url", server.URL,
		"--model", "gpt-test",
		"--full-send",
		"-p", "inspect",
	)
	exitErr, ok := err.(*exec.ExitError)
	const clarificationExit = 2
	if !ok || exitErr.ExitCode() != clarificationExit {
		t.Fatalf("run main err = %v, want exit %d", err, clarificationExit)
	}
	if stdout.String() != "Which repo?\n" {
		t.Fatalf("stdout = %q, want clarification question", stdout.String())
	}
	if stderr.String() != "Clarification needed.\n" {
		t.Fatalf("stderr = %q, want clarification status", stderr.String())
	}
	if calls != 1 {
		t.Fatalf("model calls = %d, want 1", calls)
	}
}

func TestFullSendPipeClarificationExitsNonzeroWithoutStdout(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": `{"turn":{"type":"clarify","bash":null,"question":"Which repo?","summary":null,"reasoning":null}}`}},
			},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer server.Close()

	stdout, stderr, err := runMainHelperWithEnvAndInput(t, []string{"CLNKR_API_KEY=test-key"}, strings.NewReader("inspect\n"),
		"--provider", "openai",
		"--provider-api", "openai-chat-completions",
		"--base-url", server.URL,
		"--model", "gpt-test",
		"--full-send",
	)
	exitErr, ok := err.(*exec.ExitError)
	const clarificationExit = 2
	if !ok || exitErr.ExitCode() != clarificationExit {
		t.Fatalf("run main err = %v, want exit %d", err, clarificationExit)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.HasPrefix(stderr.String(), "Which repo?\n[Session saved to ") || !strings.HasSuffix(stderr.String(), "]\nClarification needed.\n") {
		t.Fatalf("stderr = %q, want question, session save, and clarification status", stderr.String())
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := session.ListSessions(cwd)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %#v, want one saved session", sessions)
	}
}

func TestFullSendPipeRunErrorExitsNonzero(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		content := `{"turn":{"type":"act","bash":{"commands":[{"command":"printf reached-limit","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`
		if calls > 1 {
			content = `not-json`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": content}},
			},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer server.Close()

	stdout, stderr, err := runMainHelperWithEnvAndInput(t, []string{"CLNKR_API_KEY=test-key"}, strings.NewReader("hit the limit\n"),
		"--provider", "openai",
		"--provider-api", "openai-chat-completions",
		"--base-url", server.URL,
		"--model", "gpt-test",
		"--max-steps", "1",
		"--full-send",
	)
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("run main err = %v, want exit 1", err)
	}
	if stdout.String() != "reached-limit\n" {
		t.Fatalf("stdout = %q, want command output", stdout.String())
	}
	if !strings.Contains(stderr.String(), "[Session saved to ") || !strings.Contains(stderr.String(), "Error: query model (final):") {
		t.Fatalf("stderr = %q, want session save and final query error", stderr.String())
	}
}

func TestReplPromptIsSuppressedForNonTTY(t *testing.T) {
	stdout, stderr, err := runMainHelperWithEnv(t, []string{
		"CLNKR_API_KEY=test-key",
		"CLNKR_PROVIDER=openai",
		"CLNKR_MODEL=gpt-test",
		"TERM=xterm",
	}, "--full-send")
	if err != nil {
		t.Fatalf("run main: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestSingleTaskApprovalRejectsNonTTYStdin(t *testing.T) {
	stdout, stderr, err := runMainHelperWithEnv(t, []string{
		"CLNKR_API_KEY=test-key",
		"CLNKR_PROVIDER=openai",
		"CLNKR_MODEL=gpt-test",
		"TERM=xterm",
	}, "-p", "hi")
	if err == nil {
		t.Fatalf("run main succeeded; stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	want := "Error: approval mode requires interactive stdin; pass --full-send=true to bypass approval\n"
	if stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestParseCompactCommand(t *testing.T) {
	tests := []struct {
		name             string
		input            string
		wantInstructions string
		wantOK           bool
	}{
		{name: "bare command", input: "/compact", wantOK: true},
		{name: "trimmed command", input: "  /compact  ", wantOK: true},
		{name: "with instructions", input: "/compact focus on tests", wantInstructions: "focus on tests", wantOK: true},
		{name: "with tab separator", input: "/compact\tfocus on tests", wantInstructions: "focus on tests", wantOK: true},
		{name: "not compact", input: "compact", wantOK: false},
		{name: "prefixed word", input: "/compaction", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotInstructions, gotOK := parseCompactCommand(tt.input)
			if gotInstructions != tt.wantInstructions || gotOK != tt.wantOK {
				t.Fatalf("parseCompactCommand(%q) = (%q, %v), want (%q, %v)", tt.input, gotInstructions, gotOK, tt.wantInstructions, tt.wantOK)
			}
		})
	}
}

func TestCompactCommandDoesNotAppendLiteralUserMessage(t *testing.T) {
	model := &fakeModel{}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}

	compactor := &fakeCompactor{summary: "Older work summarized."}
	var gotInstructions string
	factory := func(instructions string) clnkr.Compactor {
		gotInstructions = instructions
		return compactor
	}

	stderr := captureStderr(t, func() {
		if err := handleConversationalInput(context.Background(), agent, "/compact focus on failing tests", true, nil, factory); err != nil {
			t.Fatalf("handleConversationalInput: %v", err)
		}
	})

	if compactor.calls != 1 {
		t.Fatalf("expected compactor to be called once, got %d", compactor.calls)
	}
	if gotInstructions != "focus on failing tests" {
		t.Fatalf("factory instructions = %q, want %q", gotInstructions, "focus on failing tests")
	}
	if len(compactor.messages) != 2 {
		t.Fatalf("compactor saw %d messages, want 2", len(compactor.messages))
	}

	msgs := agent.Messages()
	for _, msg := range msgs {
		if msg.Role == "user" && msg.Content == "/compact focus on failing tests" {
			t.Fatalf("literal compact command was appended: %#v", msgs)
		}
	}
	if len(msgs) == 0 || !strings.HasPrefix(msgs[0].Content, "[compact]\n") {
		t.Fatalf("expected compact block at start, got %#v", msgs)
	}
	if !strings.Contains(msgs[0].Content, `"instructions":"focus on failing tests"`) {
		t.Fatalf("compact block missing instructions: %q", msgs[0].Content)
	}
	if got := stderr; !strings.Contains(got, "[Session compacted: 2 messages summarized, 4 kept]") {
		t.Fatalf("stderr = %q, want compact summary", got)
	}
}

func TestCompactCommandFailureLeavesMessagesUnchanged(t *testing.T) {
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	before := agent.Messages()

	compactor := &fakeCompactor{err: errors.New("boom")}
	factory := func(string) clnkr.Compactor { return compactor }

	var err error
	stderr := captureStderr(t, func() {
		err = handleConversationalInput(context.Background(), agent, "/compact", true, nil, factory)
	})
	if err == nil || !strings.Contains(err.Error(), "compact transcript: summarize prefix: boom") {
		t.Fatalf("got %v, want summarize prefix error", err)
	}
	if !reflect.DeepEqual(agent.Messages(), before) {
		t.Fatalf("messages changed on compaction failure: got %#v want %#v", agent.Messages(), before)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty on failure", stderr)
	}
}

func TestCompactCommandKeepsSessionUsable(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(`{"type":"done","summary":"done"}`),
	}}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}

	compactor := &fakeCompactor{summary: "Older work summarized."}
	factory := func(string) clnkr.Compactor { return compactor }

	captureStderr(t, func() {
		if err := handleConversationalInput(context.Background(), agent, "/compact", true, nil, factory); err != nil {
			t.Fatalf("compact command: %v", err)
		}
		if err := handleConversationalInput(context.Background(), agent, "next task", true, nil, factory); err != nil {
			t.Fatalf("follow-up task: %v", err)
		}
	})

	msgs := agent.Messages()
	if len(msgs) < 3 {
		t.Fatalf("expected compacted transcript plus follow-up, got %#v", msgs)
	}
	if !strings.HasPrefix(msgs[0].Content, "[compact]\n") {
		t.Fatalf("expected compact block to remain at start, got %#v", msgs)
	}
	if msgs[len(msgs)-1].Content != `{"type":"done","summary":"done"}` {
		t.Fatalf("expected follow-up completion at end, got %#v", msgs)
	}
	if !hasUserMessage(msgs, "next task") {
		t.Fatalf("follow-up task not appended after compaction: %#v", msgs)
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want 1", model.calls)
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

	compactor := makeCompactorFactory(providerConfig{
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

	compactor := makeCompactorFactory(providerConfig{
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

	compactor := makeCompactorFactory(providerConfig{
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

func TestTrajectoryRoundTripCanonicalAssistantTurn(t *testing.T) {
	want := runDoneTranscript(t, "Saved transcript")

	trajectoryPath := filepath.Join(t.TempDir(), "trajectory.json")
	data, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if err := os.WriteFile(trajectoryPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loadedData, err := os.ReadFile(trajectoryPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var loaded []clnkr.Message
	if err := json.Unmarshal(loadedData, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	replayed := clnkr.NewAgent(nil, nil, "/tmp")
	if err := replayed.AddMessages(loaded); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	got := replayed.Messages()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("messages = %#v, want %#v", got, want)
	}

	last := got[len(got)-1].Content
	wantCanonical := mustCanonicalDoneText("Saved transcript")
	if last != wantCanonical {
		t.Fatalf("last assistant message = %q, want %q", last, wantCanonical)
	}
	turn, err := clnkr.ParseTurn(last)
	if err != nil {
		t.Fatalf("ParseTurn(last): %v", err)
	}
	done, ok := turn.(*clnkr.DoneTurn)
	if !ok {
		t.Fatalf("last turn = %T, want *clnkr.DoneTurn", turn)
	}
	if done.Summary != "Saved transcript" {
		t.Fatalf("done summary = %q, want %q", done.Summary, "Saved transcript")
	}
}

func TestLoadMessagesRoundTripCanonicalAssistantTurn(t *testing.T) {
	want := runDoneTranscript(t, "Loaded transcript")

	loadPath := filepath.Join(t.TempDir(), "messages.json")
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(loadPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loadedData, err := os.ReadFile(loadPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var loaded []clnkr.Message
	if err := json.Unmarshal(loadedData, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	agent := clnkr.NewAgent(nil, nil, "/tmp")
	if err := agent.AddMessages(loaded); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	got := agent.Messages()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("messages = %#v, want %#v", got, want)
	}

	last := got[len(got)-1].Content
	wantCanonical := mustCanonicalDoneText("Loaded transcript")
	if last != wantCanonical {
		t.Fatalf("last assistant message = %q, want %q", last, wantCanonical)
	}
	turn, err := clnkr.ParseTurn(last)
	if err != nil {
		t.Fatalf("ParseTurn(last): %v", err)
	}
	done, ok := turn.(*clnkr.DoneTurn)
	if !ok {
		t.Fatalf("last turn = %T, want *clnkr.DoneTurn", turn)
	}
	if done.Summary != "Loaded transcript" {
		t.Fatalf("done summary = %q, want %q", done.Summary, "Loaded transcript")
	}
}

func TestRejectCompactCommandOutsideConversation(t *testing.T) {
	err := rejectCompactCommand("/compact focus on tests")
	if err == nil || !strings.Contains(err.Error(), "/compact is only available at the conversational prompt") {
		t.Fatalf("got %v, want conversational prompt error", err)
	}
}

func TestRunSingleTaskRejectsCompactCommandBeforeApprovalCheck(t *testing.T) {
	stdout, stderr, err := runMainHelperWithEnv(t, []string{
		"CLNKR_API_KEY=test-key",
		"CLNKR_PROVIDER=openai",
		"CLNKR_MODEL=gpt-test",
		"TERM=xterm",
	}, "-p", "/compact focus on tests")
	if err == nil {
		t.Fatalf("run main succeeded; stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "/compact is only available at the conversational prompt") {
		t.Fatalf("stderr = %q, want compact rejection", stderr.String())
	}
}

func TestRunSingleTaskRejectsCompactCommandInFullSend(t *testing.T) {
	stdout, stderr, err := runMainHelperWithEnv(t, []string{
		"CLNKR_API_KEY=test-key",
		"CLNKR_PROVIDER=openai",
		"CLNKR_MODEL=gpt-test",
	}, "--full-send", "-p", "/compact focus on tests")
	if err == nil {
		t.Fatalf("run main succeeded; stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "/compact is only available at the conversational prompt") {
		t.Fatalf("stderr = %q, want compact rejection", stderr.String())
	}
}

func TestRunApprovalTaskRejectsCompactApprovalReply(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(actJSON("echo hi")),
		mustResponse(`{"type":"done","summary":"done"}`),
	}}
	executor := &fakeExecutor{results: []clnkr.CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	prompter := &scriptPrompter{
		actReplies: []clarifyReply{
			{text: "/compact focus on tests"},
			{text: "y"},
		},
	}

	stderr := captureStderr(t, func() {
		err := runApprovalTask(context.Background(), agent, "say hi", prompter)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if executor.calls != 1 {
		t.Fatalf("expected command execution after valid retry, got %d", executor.calls)
	}
	if hasUserMessage(agent.Messages(), "/compact focus on tests") {
		t.Fatalf("compact reply leaked into transcript: %#v", agent.Messages())
	}
	if prompter.actReplyCalls != 2 {
		t.Fatalf("expected reprompt after rejected compact reply, got %d prompts", prompter.actReplyCalls)
	}
	if !strings.Contains(stderr, "/compact is only available at the conversational prompt") {
		t.Fatalf("stderr = %q, want compact rejection", stderr)
	}
}

func TestRunApprovalTaskRejectsCompactClarificationReply(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(`{"type":"clarify","question":"Which repo?"}`),
		mustResponse(`{"type":"done","summary":"done"}`),
	}}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	prompter := &scriptPrompter{
		clarifications: []clarifyReply{
			{text: "/compact"},
			{text: "/tmp/repo"},
		},
	}

	stderr := captureStderr(t, func() {
		err := runApprovalTask(context.Background(), agent, "inspect", prompter)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if hasUserMessage(agent.Messages(), "/compact") {
		t.Fatalf("compact clarification leaked into transcript: %#v", agent.Messages())
	}
	if !hasUserMessage(agent.Messages(), "/tmp/repo") {
		t.Fatalf("expected valid clarification reply in transcript: %#v", agent.Messages())
	}
	if prompter.clarifyCalls != 2 {
		t.Fatalf("expected reprompt after rejected compact clarification, got %d prompts", prompter.clarifyCalls)
	}
	if !strings.Contains(stderr, "/compact is only available at the conversational prompt") {
		t.Fatalf("stderr = %q, want compact rejection", stderr)
	}
}

func TestRunApprovalTaskApproveExecutesCommand(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(actJSON("echo hi")),
		mustResponse(`{"type":"done","summary":"done"}`),
	}}
	executor := &fakeExecutor{results: []clnkr.CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	prompter := &scriptPrompter{actReplies: []clarifyReply{{text: "y"}}}

	err := runApprovalTask(context.Background(), agent, "say hi", prompter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executor.calls != 1 {
		t.Fatalf("expected 1 execute call, got %d", executor.calls)
	}
}

func TestRunApprovalTaskNonApprovalReplyBecomesGuidance(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(actJSON("rm important.txt")),
		mustResponse(`{"type":"done","summary":"okay"}`),
	}}
	executor := &fakeExecutor{}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	prompter := &scriptPrompter{
		actReplies: []clarifyReply{{text: "list files instead"}},
	}

	err := runApprovalTask(context.Background(), agent, "do it", prompter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executor.calls != 0 {
		t.Fatalf("non-approval reply should not execute the command")
	}
	msgs := agent.Messages()
	if len(msgs) == 0 || msgs[len(msgs)-2].Content != "list files instead" {
		t.Fatalf("guidance was not appended: %#v", msgs)
	}
}

func TestRunApprovalTaskCountsBatchCommandsTowardStepLimit(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(`{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null},{"command":"go test ./...","workdir":"subdir"}]}}`),
		mustResponse(`{"type":"done","summary":"step limit summary"}`),
	}}
	executor := &fakeExecutor{results: []clnkr.CommandResult{
		{Stdout: "/tmp\n", ExitCode: 0},
		{Stdout: "ok\n", ExitCode: 0},
	}}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	agent.MaxSteps = 2
	prompter := &scriptPrompter{actReplies: []clarifyReply{{text: "y"}}}

	err := runApprovalTask(context.Background(), agent, "do it", prompter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executor.calls != 2 {
		t.Fatalf("expected 2 command executions, got %d", executor.calls)
	}
	msgs := agent.Messages()
	found := false
	for _, msg := range msgs {
		if msg.Role == "user" && strings.HasPrefix(msg.Content, "Step limit reached.") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected step-limit prompt in transcript, got %#v", msgs)
	}
}

func TestRunApprovalTaskClarifyTurnAppendsReply(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(`{"type":"clarify","question":"Which repo?"}`),
		mustResponse(`{"type":"done","summary":"okay"}`),
	}}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	prompter := &scriptPrompter{
		clarifications: []clarifyReply{{text: "/tmp/repo"}},
	}

	err := runApprovalTask(context.Background(), agent, "inspect", prompter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msgs := agent.Messages()
	if len(msgs) == 0 || msgs[len(msgs)-2].Content != "/tmp/repo" {
		t.Fatalf("clarification was not appended: %#v", msgs)
	}
}

func TestRunApprovalTaskEmptyActReplyIsNoOp(t *testing.T) {
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

	err := runApprovalTask(context.Background(), agent, "do it", prompter)
	if !errors.Is(err, errApprovalPending) {
		t.Fatalf("got %v, want errApprovalPending", err)
	}
	if executor.calls != 0 {
		t.Fatalf("empty act reply should not execute the command")
	}
}

func TestStdinPrompterConfirmWritesPromptToStderr(t *testing.T) {
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

func TestStdinPrompterConfirmShowsWorkdir(t *testing.T) {
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

func TestWriteEventLogResponsePayload(t *testing.T) {
	f, err := os.CreateTemp("", "clnkr-event-log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	defer f.Close()           //nolint:errcheck

	writeEventLog(f, clnkr.EventResponse{
		Turn:  &clnkr.DoneTurn{Summary: "done"},
		Usage: clnkr.Usage{InputTokens: 3, OutputTokens: 4},
		Raw:   "raw response",
	})

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Type    string `json:"type"`
		Payload struct {
			Turn  json.RawMessage            `json:"turn"`
			Usage map[string]json.RawMessage `json:"usage"`
			Raw   string                     `json:"raw"`
		} `json:"payload"`
	}
	if err := json.NewDecoder(f).Decode(&payload); err != nil {
		t.Fatalf("decode event log: %v", err)
	}
	if payload.Type != "response" {
		t.Fatalf("type = %q, want response", payload.Type)
	}
	if string(payload.Payload.Turn) != `{"type":"done","summary":"done"}` {
		t.Fatalf("turn = %s, want canonical done turn", payload.Payload.Turn)
	}
	if _, ok := payload.Payload.Usage["input_tokens"]; !ok {
		t.Fatalf("usage = %#v, want input_tokens", payload.Payload.Usage)
	}
	if _, ok := payload.Payload.Usage["InputTokens"]; ok {
		t.Fatalf("usage has Go field name: %#v", payload.Payload.Usage)
	}
	if payload.Payload.Raw != "raw response" {
		t.Fatalf("payload = %#v, want usage and raw response", payload.Payload)
	}
}

func TestWriteEventLogSnakeCasePayloads(t *testing.T) {
	f, err := os.CreateTemp("", "clnkr-event-log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	defer f.Close()           //nolint:errcheck

	writeEventLog(f, clnkr.EventCommandStart{Command: "pwd", Dir: "/tmp"})
	writeEventLog(f, clnkr.EventDebug{Message: "querying model..."})

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(f)
	var start struct {
		Type    string                     `json:"type"`
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := decoder.Decode(&start); err != nil {
		t.Fatalf("decode command_start event: %v", err)
	}
	if _, ok := start.Payload["command"]; start.Type != "command_start" || !ok {
		t.Fatalf("command_start payload = %#v, want snake-case command", start)
	}
	if _, ok := start.Payload["Command"]; ok {
		t.Fatalf("command_start payload has Go field name: %#v", start.Payload)
	}
	var debug struct {
		Type    string                     `json:"type"`
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := decoder.Decode(&debug); err != nil {
		t.Fatalf("decode debug event: %v", err)
	}
	if _, ok := debug.Payload["message"]; debug.Type != "debug" || !ok {
		t.Fatalf("debug payload = %#v, want snake-case message", debug)
	}
	if _, ok := debug.Payload["Message"]; ok {
		t.Fatalf("debug payload has Go field name: %#v", debug.Payload)
	}
}

func TestWriteEventLogIncludesFeedback(t *testing.T) {
	f, err := os.CreateTemp("", "clnkr-event-log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	defer f.Close()           //nolint:errcheck

	writeEventLog(f, clnkr.EventCommandDone{
		Command:  "touch note.txt",
		ExitCode: 0,
		Feedback: clnkr.CommandFeedback{ChangedFiles: []string{"note.txt"}},
	})

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Type    string `json:"type"`
		Payload struct {
			Feedback clnkr.CommandFeedback `json:"feedback"`
		} `json:"payload"`
	}
	if err := json.NewDecoder(f).Decode(&payload); err != nil {
		t.Fatalf("decode event log: %v", err)
	}
	if payload.Type != "command_done" {
		t.Fatalf("type = %q, want command_done", payload.Type)
	}
	if len(payload.Payload.Feedback.ChangedFiles) != 1 || payload.Payload.Feedback.ChangedFiles[0] != "note.txt" {
		t.Fatalf("feedback = %#v, want note.txt", payload.Payload.Feedback)
	}
}

func TestWriteEventLogCommandDoneErrOmittedWhenNil(t *testing.T) {
	f, err := os.CreateTemp("", "clnkr-event-log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	defer f.Close()           //nolint:errcheck

	writeEventLog(f, clnkr.EventCommandDone{Command: "true", ExitCode: 0})

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(f).Decode(&payload); err != nil {
		t.Fatalf("decode event log: %v", err)
	}
	if _, ok := payload.Payload["err"]; ok {
		t.Fatalf("nil command error should omit err, got %#v", payload.Payload["err"])
	}
}

func TestWriteEventLogCommandDoneErrIncludedWhenPresent(t *testing.T) {
	f, err := os.CreateTemp("", "clnkr-event-log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	defer f.Close()           //nolint:errcheck

	writeEventLog(f, clnkr.EventCommandDone{Command: "false", ExitCode: 1, Err: errors.New("boom")})

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Payload struct {
			Err string `json:"err"`
		} `json:"payload"`
	}
	if err := json.NewDecoder(f).Decode(&payload); err != nil {
		t.Fatalf("decode event log: %v", err)
	}
	if payload.Payload.Err != "boom" {
		t.Fatalf("err = %q, want boom", payload.Payload.Err)
	}
}

func TestWriteEventLogProtocolFailurePayload(t *testing.T) {
	f, err := os.CreateTemp("", "clnkr-event-log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	defer f.Close()           //nolint:errcheck

	writeEventLog(f, clnkr.EventProtocolFailure{Reason: "bad json", Raw: "nope"})

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Type    string `json:"type"`
		Payload struct {
			Reason string `json:"reason"`
			Raw    string `json:"raw"`
		} `json:"payload"`
	}
	if err := json.NewDecoder(f).Decode(&payload); err != nil {
		t.Fatalf("decode event log: %v", err)
	}
	if payload.Type != "protocol_failure" || payload.Payload.Reason != "bad json" || payload.Payload.Raw != "nope" {
		t.Fatalf("payload = %#v, want protocol failure details", payload)
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

func compactableMessages() []clnkr.Message {
	return []clnkr.Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done"}`},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done again"}`},
		{Role: "user", Content: "third task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
	}
}

func hasUserMessage(msgs []clnkr.Message, want string) bool {
	for _, msg := range msgs {
		if msg.Role == "user" && msg.Content == want {
			return true
		}
	}
	return false
}
