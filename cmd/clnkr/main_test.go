package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	clnkr "github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/clnkrapp"
)

func openAIWrappedDone(summary string) string {
	return fmt.Sprintf(
		`{"turn":{"type":"done","bash":null,"question":null,"summary":%q,"verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":null}}`,
		summary,
	)
}

func mustTurn(raw string) clnkr.Turn {
	turn, err := clnkr.ParseTurn(raw)
	if err == nil {
		return turn
	}
	var env struct {
		Type    string `json:"type"`
		Summary string `json:"summary"`
	}
	if json.Unmarshal([]byte(raw), &env) == nil && env.Type == "done" {
		return verifiedDone(env.Summary)
	}
	panic(err)
}

func mustResponse(raw string) clnkr.Response {
	return clnkr.Response{Turn: mustTurn(raw), Raw: raw}
}

func verifiedDone(summary string) *clnkr.DoneTurn {
	return &clnkr.DoneTurn{
		Summary: summary,
		Verification: clnkr.CompletionVerification{
			Status: clnkr.VerificationVerified,
			Checks: []clnkr.VerificationCheck{
				{
					Command:  "go test ./...",
					Outcome:  "passed",
					Evidence: "go test ./... passed and ls output showed current directory entries for completion",
				},
			},
		},
		KnownRisks: []string{},
	}
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

func runMainHelperWithEnv(
	t *testing.T,
	env []string,
	args ...string,
) (bytes.Buffer, bytes.Buffer, error) {
	t.Helper()

	return runMainHelperWithEnvAndInput(t, env, nil, args...)
}

func runMainHelperWithEnvAndInput(
	t *testing.T,
	env []string,
	stdin io.Reader,
	args ...string,
) (bytes.Buffer, bytes.Buffer, error) {
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

func TestHelpWritesRichUsageToStdout(t *testing.T) {
	stdout, stderr, err := runMainHelper(t, "--help")
	if err != nil {
		t.Fatalf("help command: %v\nstderr: %s", err, stderr.String())
	}
	for _, want := range []string{
		"clnkr - a minimal coding agent",
		"Usage:",
		"Options:",
		"Provider options:",
		"Sessions:",
		"System prompt:",
		"Debugging:",
		"Environment:",
		"Defaults:",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
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

func TestFlagParseErrorsKeepUsageOffStdout(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown flag", args: []string{"--bogus"}, want: "flag provided but not defined"},
		{
			name: "removed turn protocol flag",
			args: []string{"--turn-protocol", "structured-json"},
			want: "flag provided but not defined: -turn-protocol",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := runMainHelper(t, tt.args...)
			if err == nil {
				t.Fatal("invalid flag succeeded, want failure")
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.want)
			}
			if !strings.Contains(stderr.String(), "See clnkr(1)") {
				t.Fatalf("stderr = %q, want manpage hint", stderr.String())
			}
		})
	}
}

type dumpSystemPromptWant struct {
	err                  bool
	stdout               string
	stdoutContains       []string
	stdoutSuffix         string
	rejectStdoutContains []string
	stderrContains       []string
	rejectStderrContains []string
}

func assertDumpSystemPrompt(t *testing.T, args []string, want dumpSystemPromptWant) {
	t.Helper()

	stdout, stderr, err := runMainHelper(t, args...)
	if want.err && err == nil {
		t.Fatalf("run main succeeded; stdout: %s stderr: %s", stdout.String(), stderr.String())
	}
	if !want.err && err != nil {
		t.Fatalf("dump system prompt: %v\nstderr: %s", err, stderr.String())
	}
	if want.stdout != "" || want.err {
		if stdout.String() != want.stdout {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want.stdout)
		}
	}
	if want.stdoutSuffix != "" && !strings.HasSuffix(stdout.String(), want.stdoutSuffix) {
		t.Fatalf("stdout suffix = %q, want suffix %q", stdout.String(), want.stdoutSuffix)
	}
	for _, text := range want.stdoutContains {
		if !strings.Contains(stdout.String(), text) {
			t.Fatalf("stdout missing %q: %q", text, stdout.String())
		}
	}
	for _, text := range want.rejectStdoutContains {
		if strings.Contains(stdout.String(), text) {
			t.Fatalf("stdout contains %q: %q", text, stdout.String())
		}
	}
	for _, text := range want.stderrContains {
		if !strings.Contains(stderr.String(), text) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), text)
		}
	}
	for _, text := range want.rejectStderrContains {
		if strings.Contains(stderr.String(), text) {
			t.Fatalf("stderr contains %q: %q", text, stderr.String())
		}
	}
	if len(want.stderrContains) == 0 && len(want.rejectStderrContains) == 0 && stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestDumpCustomSystemPromptDoesNotRequireProviderConfig(t *testing.T) {
	assertDumpSystemPrompt(
		t,
		[]string{
			"--no-system-prompt",
			"--system-prompt-append",
			"custom prompt",
			"--dump-system-prompt",
		},
		dumpSystemPromptWant{stdout: "custom prompt"},
	)
}

func TestDumpSystemPromptAllowsPromptFlagTextInAppend(t *testing.T) {
	assertDumpSystemPrompt(
		t,
		[]string{
			"--act-protocol",
			"clnkr-inline",
			"--system-prompt-append",
			"-p",
			"--dump-system-prompt",
		},
		dumpSystemPromptWant{stdoutSuffix: "\n\n-p"},
	)
}

func TestDumpSystemPromptAppendMarkerDoesNotSelectUnattendedPrompt(t *testing.T) {
	assertDumpSystemPrompt(t,
		[]string{"--system-prompt-append", "--dump-system-prompt", "-p"},
		dumpSystemPromptWant{
			err:            true,
			stderrContains: []string{"flag needs an argument: -p"},
		},
	)
}

func TestDumpAutoSystemPromptResolvesWithoutAPIKey(t *testing.T) {
	assertDumpSystemPrompt(t,
		[]string{
			"--provider", "openai",
			"--provider-api", "openai-responses",
			"--model", "gpt-5",
			"--dump-system-prompt",
		},
		dumpSystemPromptWant{
			stdoutContains:       []string{"call the bash tool"},
			rejectStderrContains: []string{"No API key found", "api key is required"},
		},
	)
}

func TestDumpAutoSystemPromptReportsMissingProviderContext(t *testing.T) {
	assertDumpSystemPrompt(t,
		[]string{"--dump-system-prompt"},
		dumpSystemPromptWant{
			err:            true,
			stderrContains: []string{"--act-protocol clnkr-inline"},
		},
	)
}

func TestPromptFlagDumpsUnattendedSystemPrompt(t *testing.T) {
	assertDumpSystemPrompt(t,
		[]string{"--act-protocol", "clnkr-inline", "-p", "fix it", "--dump-system-prompt"},
		dumpSystemPromptWant{
			stdoutContains:       []string{`Set type to exactly one of "act" or "done".`},
			rejectStdoutContains: []string{"clarify"},
			rejectStderrContains: []string{"provider is required"},
		},
	)
}

func TestPromptFlagBeforeDumpSystemPromptErrors(t *testing.T) {
	assertDumpSystemPrompt(t,
		[]string{"-p", "--dump-system-prompt"},
		dumpSystemPromptWant{
			err:            true,
			stderrContains: []string{"-p requires a task", "clnkr --dump-system-prompt -p"},
		},
	)
}

func newOpenAIChatServer(t *testing.T, content func(call int) string) (*httptest.Server, *int) {
	t.Helper()

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": content(calls)}},
			},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	return server, &calls
}

func TestPromptRunsSingleTask(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server, _ := newOpenAIChatServer(t, func(int) string { return openAIWrappedDone("ok") })
	defer server.Close()

	stdout, stderr, err := runMainHelperWithEnv(t, []string{"CLNKR_API_KEY=test-key"},
		"--provider", "openai",
		"--provider-api", "openai-chat-completions",
		"--base-url", server.URL,
		"--model", "gpt-test",
		"--prompt", "hi",
	)
	if err != nil {
		t.Fatalf("run main: %v\nstderr: %s", err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "ok" {
		t.Fatalf("stdout = %q, want ok", stdout.String())
	}
	if strings.Contains(stderr.String(), "Clarification needed") {
		t.Fatalf("stderr = %q, want no clarification", stderr.String())
	}
}

func TestTrajectoryRequiresPromptBeforeProviderConfig(t *testing.T) {
	stdout, stderr, err := runMainHelper(
		t,
		"--trajectory",
		filepath.Join(t.TempDir(), "trajectory.json"),
	)
	if err == nil {
		t.Fatalf(
			"trajectory without prompt succeeded; stdout: %s stderr: %s",
			stdout.String(),
			stderr.String(),
		)
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

func TestCommandProgressWritesToStderr(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server, calls := newOpenAIChatServer(t, func(call int) string {
		if call == 1 {
			return `{"turn":{"type":"act","bash":{"commands":[{"command":"printf %s \"$COMMAND_OUTPUT_SENTINEL\"; printf err-no-newline >&2","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`
		}
		return openAIWrappedDone("done")
	})
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
		t.Fatalf(
			"stdout = %q, want command output and final summary %q",
			stdout.String(),
			wantStdout,
		)
	}
	if strings.Contains(stderr.String(), sentinel) {
		t.Fatalf("stderr contains command output: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), `{"type":"act"`) {
		t.Fatalf("stderr contains non-verbose model response: %q", stderr.String())
	}
	if strings.Contains(
		stderr.String(),
		`{"type":"done","summary":"done","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`,
	) {
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
	if *calls != 2 {
		t.Fatalf("model calls = %d, want 2", *calls)
	}
}

func TestStepLimitInvalidSummaryExitsNonzero(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server, _ := newOpenAIChatServer(t, func(call int) string {
		if call > 1 {
			return `not-json`
		}
		return `{"turn":{"type":"act","bash":{"commands":[{"command":"printf reached-limit","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`
	})
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

func TestSingleTaskFullSendClarificationCrashesWithoutQuestion(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server, calls := newOpenAIChatServer(t, func(int) string {
		return `{"turn":{"type":"clarify","bash":null,"question":"Which repo?","summary":null,"reasoning":null}}`
	})
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
	if stderr.String() != "clarify not allowed in unattended mode\n" {
		t.Fatalf("stderr = %q, want unattended clarify error", stderr.String())
	}
	if *calls != 1 {
		t.Fatalf("model calls = %d, want 1", *calls)
	}
}

func TestFullSendPipeClarificationExitsNonzeroWithoutStdout(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server, _ := newOpenAIChatServer(t, func(int) string {
		return `{"turn":{"type":"clarify","bash":null,"question":"Which repo?","summary":null,"reasoning":null}}`
	})
	defer server.Close()

	stdout, stderr, err := runMainHelperWithEnvAndInput(
		t,
		[]string{"CLNKR_API_KEY=test-key"},
		strings.NewReader("inspect\n"),
		"--provider",
		"openai",
		"--provider-api",
		"openai-chat-completions",
		"--base-url",
		server.URL,
		"--model",
		"gpt-test",
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
	if !strings.HasPrefix(stderr.String(), "Which repo?\n[Session saved to ") ||
		!strings.HasSuffix(stderr.String(), "]\nClarification needed.\n") {
		t.Fatalf(
			"stderr = %q, want question, session save, and clarification status",
			stderr.String(),
		)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := clnkrapp.ListSessions(cwd)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %#v, want one saved session", sessions)
	}
}

func TestFullSendPipeRunErrorExitsNonzero(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server, _ := newOpenAIChatServer(t, func(call int) string {
		if call > 1 {
			return `not-json`
		}
		return `{"turn":{"type":"act","bash":{"commands":[{"command":"printf reached-limit","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`
	})
	defer server.Close()

	stdout, stderr, err := runMainHelperWithEnvAndInput(
		t,
		[]string{"CLNKR_API_KEY=test-key"},
		strings.NewReader("hit the limit\n"),
		"--provider",
		"openai",
		"--provider-api",
		"openai-chat-completions",
		"--base-url",
		server.URL,
		"--model",
		"gpt-test",
		"--max-steps",
		"1",
		"--full-send",
	)
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("run main err = %v, want exit 1", err)
	}
	if stdout.String() != "reached-limit\n" {
		t.Fatalf("stdout = %q, want command output", stdout.String())
	}
	if !strings.Contains(stderr.String(), "[Session saved to ") ||
		!strings.Contains(stderr.String(), "Error: query model (final):") {
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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server, calls := newOpenAIChatServer(t, func(int) string { return openAIWrappedDone("done") })
	defer server.Close()

	stdout, stderr, err := runMainHelperWithEnv(t, []string{"CLNKR_API_KEY=test-key"},
		"--provider", "openai",
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
	if *calls != 1 {
		t.Fatalf("model calls = %d, want 1", *calls)
	}
	requireOneSavedSession(t)
}

func requireOneSavedSession(t *testing.T) {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := clnkrapp.ListSessions(cwd)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %#v, want one saved session", sessions)
	}
}

func TestSingleTaskListSessionsShapeStaysGeneric(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server, _ := newOpenAIChatServer(t, func(int) string { return openAIWrappedDone("done") })
	defer server.Close()

	stdout, stderr, err := runMainHelperWithEnv(t, []string{"CLNKR_API_KEY=test-key"},
		"--provider", "openai",
		"--provider-api", "openai-chat-completions",
		"--base-url", server.URL,
		"--model", "gpt-test",
		"-p", "hi",
	)
	if err != nil {
		t.Fatalf("run main: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	listStdout, listStderr, err := runMainHelperWithEnv(
		t,
		[]string{"XDG_STATE_HOME=" + os.Getenv("XDG_STATE_HOME")},
		"--list-sessions",
	)
	if err != nil {
		t.Fatalf(
			"list sessions: %v\nstdout: %s\nstderr: %s",
			err,
			listStdout.String(),
			listStderr.String(),
		)
	}
	if listStderr.String() != "" {
		t.Fatalf("list stderr = %q, want empty", listStderr.String())
	}
	if !strings.HasPrefix(listStdout.String(), "Saved sessions:\n  1. ") ||
		!strings.Contains(listStdout.String(), " messages) - ") {
		t.Fatalf("list stdout = %q, want existing generic session shape", listStdout.String())
	}
	if strings.Contains(listStdout.String(), "single-task") ||
		strings.Contains(listStdout.String(), "prompt") {
		t.Fatalf("list stdout = %q, want no one-shot session label", listStdout.String())
	}
}

func TestSingleTaskRunErrorSavesSessionBeforeExit(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server, _ := newOpenAIChatServer(t, func(call int) string {
		if call > 1 {
			return `not-json`
		}
		return `{"turn":{"type":"act","bash":{"commands":[{"command":"printf reached-limit","workdir":null}]},"question":null,"summary":null,"reasoning":null}}`
	})
	defer server.Close()

	stdout, stderr, err := runMainHelperWithEnv(t, []string{"CLNKR_API_KEY=test-key"},
		"--provider", "openai",
		"--provider-api", "openai-chat-completions",
		"--base-url", server.URL,
		"--model", "gpt-test",
		"--max-steps", "1",
		"-p", "hit the limit",
	)
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("run main err = %v, want exit 1", err)
	}
	if stdout.String() != "reached-limit\n" {
		t.Fatalf("stdout = %q, want command output", stdout.String())
	}
	if strings.Contains(stderr.String(), "[Session saved to ") {
		t.Fatalf("stderr = %q, want no saved-session success notice", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Error: query model (final):") {
		t.Fatalf("stderr = %q, want final query error", stderr.String())
	}
	requireOneSavedSession(t)
}

func TestSingleTaskClarificationSavesSessionBeforeExit(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server, calls := newOpenAIChatServer(t, func(int) string {
		return `{"turn":{"type":"clarify","bash":null,"question":"Which repo?","summary":null,"reasoning":null}}`
	})
	defer server.Close()

	stdout, stderr, err := runMainHelperWithEnv(t, []string{"CLNKR_API_KEY=test-key"},
		"--provider", "openai",
		"--provider-api", "openai-chat-completions",
		"--base-url", server.URL,
		"--model", "gpt-test",
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
	if stderr.String() != "clarify not allowed in unattended mode\n" {
		t.Fatalf("stderr = %q, want unattended clarify error", stderr.String())
	}
	if *calls != 1 {
		t.Fatalf("model calls = %d, want 1", *calls)
	}
	requireOneSavedSession(t)
}

func TestSingleTaskTrajectoryWriteFailureStillSavesSession(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server, _ := newOpenAIChatServer(t, func(int) string { return openAIWrappedDone("done") })
	defer server.Close()
	trajectoryPath := filepath.Join(t.TempDir(), "missing", "trajectory.json")

	stdout, stderr, err := runMainHelperWithEnv(t, []string{"CLNKR_API_KEY=test-key"},
		"--provider", "openai",
		"--provider-api", "openai-chat-completions",
		"--base-url", server.URL,
		"--model", "gpt-test",
		"--trajectory", trajectoryPath,
		"-p", "hi",
	)
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("run main err = %v, want exit 1", err)
	}
	if stdout.String() != "done\n" {
		t.Fatalf("stdout = %q, want summary before trajectory write failure", stdout.String())
	}
	if strings.Contains(stderr.String(), "[Session saved to ") {
		t.Fatalf("stderr = %q, want no saved-session success notice", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Error: cannot write trajectory") {
		t.Fatalf("stderr = %q, want trajectory write error", stderr.String())
	}
	requireOneSavedSession(t)
}

func TestSingleTaskTrajectoryAlsoSavesSession(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server, _ := newOpenAIChatServer(t, func(int) string { return openAIWrappedDone("done") })
	defer server.Close()
	trajectoryPath := filepath.Join(t.TempDir(), "trajectory.json")

	stdout, stderr, err := runMainHelperWithEnv(t, []string{"CLNKR_API_KEY=test-key"},
		"--provider", "openai",
		"--provider-api", "openai-chat-completions",
		"--base-url", server.URL,
		"--model", "gpt-test",
		"--trajectory", trajectoryPath,
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
	data, err := os.ReadFile(trajectoryPath)
	if err != nil {
		t.Fatalf("ReadFile trajectory: %v", err)
	}
	if !strings.Contains(string(data), `"role": "user"`) ||
		!strings.Contains(string(data), `"content": "hi"`) {
		t.Fatalf("trajectory = %s, want saved prompt message", data)
	}
	requireOneSavedSession(t)
}

func TestSingleTaskSeedsCommandEnvFromResolvedProviderFlags(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	server, _ := newOpenAIChatServer(t, func(call int) string {
		if call == 1 {
			return `{"turn":{"type":"act","bash":{"commands":[{"command":"printf '%s|%s|%s|%s' \"$CLNKR_PROVIDER\" \"$CLNKR_PROVIDER_API\" \"$CLNKR_MODEL\" \"$CLNKR_BASE_URL\"","workdir":null}]},"question":null,"summary":null,"reasoning":"inspect child env"}}`
		}
		return openAIWrappedDone("Printed resolved provider config.")
	})
	defer server.Close()

	stdout, stderr, err := runMainHelperWithEnv(t, []string{"CLNKR_API_KEY=test-key"},
		"--provider", "openai",
		"--provider-api", "openai-chat-completions",
		"--base-url", server.URL,
		"--model", "gpt-test",
		"-p", "print provider env",
	)
	if err != nil {
		t.Fatalf("run main: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	want := "openai|openai-chat-completions|gpt-test|" + server.URL
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want command output containing %q", stdout.String(), want)
	}
}

func TestOpenAIResponsesHarnessFlagsReachRequestAndMetadata(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
						{
							"type": "output_text",
							"text": `{"turn":{"type":"done","summary":"done","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[],"reasoning":null}}`,
						},
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
	if metadata.Effective.Output.MaxOutputTokens == nil ||
		*metadata.Effective.Output.MaxOutputTokens != 8000 {
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

func TestSingleTaskRejectsExplicitFullSendFalse(t *testing.T) {
	stdout, stderr, err := runMainHelperWithEnv(
		t,
		[]string{"CLNKR_API_KEY=test-key"},
		"-p",
		"hi",
		"--full-send=false",
	)
	if err == nil {
		t.Fatalf("run main succeeded; stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--full-send=false conflicts with -p") {
		t.Fatalf("stderr = %q, want full-send conflict", stderr.String())
	}
	if strings.Contains(stderr.String(), "provider is required") ||
		strings.Contains(stderr.String(), "No API key found") {
		t.Fatalf("stderr = %q, provider config ran before conflict validation", stderr.String())
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
		t.Fatalf(
			"stderr = %q, want provider config error after act protocol parse",
			stderr.String(),
		)
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

func TestRunSingleTaskRejectsCompactCommand(t *testing.T) {
	stdout, stderr, err := runMainHelperWithEnv(t, []string{
		"CLNKR_API_KEY=test-key",
		"CLNKR_PROVIDER=openai",
		"CLNKR_MODEL=gpt-test",
		"TERM=xterm",
	}, "-p", "/compact")
	if err == nil {
		t.Fatalf("run main succeeded; stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if !strings.Contains(
		stderr.String(),
		"/compact is only available at the conversational prompt",
	) {
		t.Fatalf("stderr = %q, want compact rejection", stderr.String())
	}
}

func TestRunPromptLoopTreatsQueuedPasteLinesAsOnePrompt(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(
			`{"type":"done","summary":"done","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`,
		),
	}}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	driver := clnkrapp.NewDriver(agent, nil)
	reader := newLineReader(strings.NewReader("first line\nsecond line\nthird line\n"))

	err := runPromptLoop(driver, reader, nil, false, clnkrapp.PromptModeApproval, nil)
	if err != nil {
		t.Fatalf("runPromptLoop: %v", err)
	}

	userMessages := make([]string, 0)
	for _, msg := range agent.Messages() {
		if msg.Role == "user" && !strings.Contains(msg.Content, `"source":"clnkr"`) {
			userMessages = append(userMessages, msg.Content)
		}
	}
	if len(userMessages) != 1 {
		t.Fatalf("user messages = %#v, want one pasted prompt", userMessages)
	}
	if userMessages[0] != "first line\nsecond line\nthird line" {
		t.Fatalf("prompt = %q, want pasted lines joined", userMessages[0])
	}
}
