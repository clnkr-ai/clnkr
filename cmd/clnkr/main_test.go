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
	"github.com/clnkr-ai/clnkr/cmd/internal/clnkrapp"
	"github.com/clnkr-ai/clnkr/cmd/internal/session"
)

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
	for _, want := range []string{"provider-api", "--effort", "thinking-budget-tokens", "max-output-tokens", "Limit executed commands", "before summary (default: 100)", "infers provider when provider is unset", "anthropic base URL  https://api.anthropic.com"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("usage output missing %q, got %q", want, stdout.String())
		}
	}
}

func TestHelpSeparatesProviderOverrides(t *testing.T) {
	stdout, stderr, err := runMainHelper(t, "--help")
	if err != nil {
		t.Fatalf("help command: %v\nstderr: %s", err, stderr.String())
	}
	usage := stdout.String()
	generalStart := strings.Index(usage, "Options:\n")
	overridesStart := strings.Index(usage, "Provider overrides:\n")
	sessionsStart := strings.Index(usage, "Sessions:\n")
	if generalStart == -1 || overridesStart == -1 || sessionsStart == -1 {
		t.Fatalf("usage missing expected sections:\n%s", usage)
	}
	if generalStart >= overridesStart || overridesStart >= sessionsStart {
		t.Fatalf("section order wrong:\n%s", usage)
	}

	generalOptions := usage[generalStart:overridesStart]
	for _, commonProviderFlag := range []string{"--provider", "--effort", "--max-output-tokens"} {
		if !strings.Contains(generalOptions, commonProviderFlag) {
			t.Fatalf("general options missing %q:\n%s", commonProviderFlag, usage)
		}
	}
	for _, overrideFlag := range []string{"--provider-api", "--thinking-budget-tokens"} {
		if strings.Contains(generalOptions, overrideFlag) {
			t.Fatalf("general options include provider override %q:\n%s", overrideFlag, usage)
		}
	}

	providerOverrides := usage[overridesStart:sessionsStart]
	for _, overrideFlag := range []string{"--provider-api", "--thinking-budget-tokens"} {
		if !strings.Contains(providerOverrides, overrideFlag) {
			t.Fatalf("provider overrides missing %q:\n%s", overrideFlag, usage)
		}
	}
	for _, commonProviderFlag := range []string{"--provider string", "--effort", "--max-output-tokens"} {
		if strings.Contains(providerOverrides, commonProviderFlag) {
			t.Fatalf("provider overrides include common option %q:\n%s", commonProviderFlag, usage)
		}
	}
	for _, want := range []string{"OpenAI", "Anthropic", "legacy/debug"} {
		if !strings.Contains(providerOverrides, want) {
			t.Fatalf("provider overrides missing %q label:\n%s", want, usage)
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

func TestTurnProtocolFlagIsRemoved(t *testing.T) {
	stdout, stderr, err := runMainHelper(t, "--turn-protocol", "structured-json")
	if err == nil {
		t.Fatal("removed legacy protocol flag succeeded, want failure")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined: -turn-protocol") {
		t.Fatalf("stderr = %q, want removed flag error", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Run 'clnkr --help'") {
		t.Fatalf("stderr = %q, want help hint", stderr.String())
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

func TestDumpToolCallsSystemPromptDoesNotRequireProviderConfig(t *testing.T) {
	stdout, stderr, err := runMainHelper(t, "--act-protocol", "tool-calls", "--dump-system-prompt")
	if err != nil {
		t.Fatalf("dump tool-calls system prompt: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "call the bash tool") {
		t.Fatalf("stdout missing tool-calls prompt: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "exactly once") {
		t.Fatalf("stdout imposes one tool call: %q", stdout.String())
	}
	if strings.Contains(stderr.String(), "provider is required") {
		t.Fatalf("stderr = %q, provider validation ran first", stderr.String())
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

func TestSingleTaskFullSendClarificationWritesQuestionToStderr(t *testing.T) {
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
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if stderr.String() != "Which repo?\nClarification needed.\n" {
		t.Fatalf("stderr = %q, want clarification question and status", stderr.String())
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

func TestSingleTaskPromptImpliesFullSend(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": openAIWrappedDone("done")}},
			},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer server.Close()

	stdout, stderr, err := runMainHelperWithEnv(t, []string{
		"CLNKR_API_KEY=test-key",
	}, "--provider", "openai",
		"--provider-api", "openai-chat-completions",
		"--base-url", server.URL,
		"--model", "gpt-test",
		"-p", "hi",
	)
	if err != nil {
		t.Fatalf("run main: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if stdout.String() != "done\n" {
		t.Fatalf("stdout = %q, want summary", stdout.String())
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if calls != 1 {
		t.Fatalf("model calls = %d, want 1", calls)
	}
}

func TestOpenAIResponsesHarnessFlagsReachRequestAndMetadata(t *testing.T) {
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
						{"type": "output_text", "text": `{"turn":{"type":"done","summary":"done","reasoning":null}}`},
					},
				},
			},
			"usage": map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer server.Close()

	eventLogPath := filepath.Join(t.TempDir(), "events.jsonl")
	stdout, stderr, err := runMainHelperWithEnv(t, []string{"CLNKR_API_KEY=test-key"},
		"--provider", "openai",
		"--provider-api", "openai-responses",
		"--base-url", server.URL,
		"--model", "gpt-5.1",
		"--effort", "high",
		"--max-output-tokens", "8000",
		"--event-log", eventLogPath,
		"-p", "hi",
	)
	if err != nil {
		t.Fatalf("run main: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if stdout.String() != "done\n" {
		t.Fatalf("stdout = %q, want summary", stdout.String())
	}
	reasoning, ok := gotBody["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("reasoning = %#v, want high", gotBody["reasoning"])
	}
	if got := gotBody["max_output_tokens"]; got != float64(8000) {
		t.Fatalf("max_output_tokens = %#v, want 8000", got)
	}

	data, err := os.ReadFile(eventLogPath)
	if err != nil {
		t.Fatalf("ReadFile event log: %v", err)
	}
	firstLine, _, _ := strings.Cut(string(data), "\n")
	var event struct {
		Type    string `json:"type"`
		Payload struct {
			Message string `json:"message"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(firstLine), &event); err != nil {
		t.Fatalf("unmarshal first event: %v\n%s", err, firstLine)
	}
	if event.Type != "debug" {
		t.Fatalf("first event type = %q, want debug", event.Type)
	}
	var metadata clnkrapp.RunMetadata
	if err := json.Unmarshal([]byte(event.Payload.Message), &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if metadata.Effective.Effort.Level == nil || *metadata.Effective.Effort.Level != "high" {
		t.Fatalf("metadata effort = %#v, want high", metadata.Effective.Effort.Level)
	}
	if metadata.Effective.Output.MaxOutputTokens == nil || *metadata.Effective.Output.MaxOutputTokens != 8000 {
		t.Fatalf("metadata max output = %#v, want 8000", metadata.Effective.Output.MaxOutputTokens)
	}
}

func TestMaxOutputTokensZeroIsRejectedWhenFlagSet(t *testing.T) {
	stdout, stderr, err := runMainHelperWithEnv(t, []string{"CLNKR_API_KEY=test-key"},
		"--provider", "openai",
		"--model", "gpt-5",
		"--max-output-tokens", "0",
		"-p", "hi",
	)
	if err == nil {
		t.Fatalf("run main succeeded; stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "max-output-tokens must be at least 1") {
		t.Fatalf("stderr = %q, want max output validation", stderr.String())
	}
}

func TestSingleTaskRejectsExplicitFullSendFalseBeforeProviderConfig(t *testing.T) {
	stdout, stderr, err := runMainHelperWithEnv(t, []string{
		"CLNKR_API_KEY=test-key",
	}, "-p", "hi", "--full-send=false")
	if err == nil {
		t.Fatalf("run main succeeded; stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--full-send=false conflicts with -p") {
		t.Fatalf("stderr = %q, want full-send conflict", stderr.String())
	}
	if strings.Contains(stderr.String(), "provider is required") || strings.Contains(stderr.String(), "No API key found") {
		t.Fatalf("stderr = %q, provider config ran before conflict validation", stderr.String())
	}
}

func TestSingleTaskRejectsRepeatedExplicitFullSendFalse(t *testing.T) {
	stdout, stderr, err := runMainHelperWithEnv(t, []string{
		"CLNKR_API_KEY=test-key",
	}, "-p", "hi", "--full-send=false", "--full-send=true")
	if err == nil {
		t.Fatalf("run main succeeded; stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--full-send=false conflicts with -p") {
		t.Fatalf("stderr = %q, want full-send conflict", stderr.String())
	}
}

func TestSingleTaskRejectsNumericFullSendFalse(t *testing.T) {
	stdout, stderr, err := runMainHelperWithEnv(t, []string{
		"CLNKR_API_KEY=test-key",
	}, "-p", "hi", "--full-send=0")
	if err == nil {
		t.Fatalf("run main succeeded; stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--full-send=false conflicts with -p") {
		t.Fatalf("stderr = %q, want full-send conflict", stderr.String())
	}
}

func TestToolCallsActProtocolDoesNotRequireFullSendBeforeProviderConfig(t *testing.T) {
	stdout, stderr, err := runMainHelperWithEnv(t, []string{
		"CLNKR_API_KEY=test-key",
	}, "--act-protocol", "tool-calls")
	if err == nil {
		t.Fatalf("run main succeeded; stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if strings.Contains(stderr.String(), "--act-protocol tool-calls requires --full-send") {
		t.Fatalf("stderr = %q, tool-calls mode should not require full-send", stderr.String())
	}
	if !strings.Contains(stderr.String(), "provider") {
		t.Fatalf("stderr = %q, want provider config error after act protocol parse", stderr.String())
	}
}

func TestConversationalApprovalRejectsNonTTYStdin(t *testing.T) {
	stdout, stderr, err := runMainHelperWithEnv(t, []string{
		"CLNKR_API_KEY=test-key",
		"CLNKR_PROVIDER=openai",
		"CLNKR_MODEL=gpt-test",
		"TERM=xterm",
	})
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
		reply, err := p.ActReply(context.Background(), clnkrapp.FormatActProposal([]clnkr.BashAction{{Command: "rm important.txt", Workdir: "subdir"}}))
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
