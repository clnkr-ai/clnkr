package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/clnkrapp"
)

func TestHelpWritesRichUsageToStdout(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runMain([]string{"--help"}, strings.NewReader(""), &out, &errOut, func(string) string { return "" }, func() []string { return nil })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr: %s", code, errOut.String())
	}
	for _, want := range []string{
		"clnkrd - stdio JSONL adapter for clnkr",
		"May be launched by clnkr through bash for bounded child work.",
		"Usage:",
		"JSONL commands:",
		"JSONL events:",
		"Options:",
		"Environment:",
		"Examples:",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, out.String())
		}
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if len(line) > 79 {
			t.Fatalf("help line length = %d, want <= 79: %q", len(line), line)
		}
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestRunJSONLPromptWritesResponseAndDone(t *testing.T) {
	var out, errOut bytes.Buffer
	model := &fakeModel{responses: []clnkr.Response{mustResponse(`{"type":"done","summary":"finished","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`)}}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	agent.Notify = func(event clnkr.Event) {
		if err := clnkrapp.WriteJSONL(&out, event); err != nil {
			t.Fatalf("WriteJSONL: %v", err)
		}
	}
	driver := clnkrapp.NewDriver(agent, nil)

	err := runJSONL(context.Background(), strings.NewReader(`{"type":"prompt","text":"inspect","mode":"full_send"}`+"\n"), &out, &errOut, driver)
	if err != nil {
		t.Fatalf("runJSONL: %v", err)
	}

	gotTypes := jsonlTypes(t, out.String())
	assertTypesContainInOrder(t, gotTypes, []string{"response", "done"}, out.String())
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestRunJSONLReplyApprovesPendingCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	reader, writer := io.Pipe()
	defer writer.Close() //nolint:errcheck
	model := &fakeModel{responses: []clnkr.Response{
		mustResponse(`{"type":"act","bash":{"commands":[{"command":"echo hi","workdir":null}]}}`),
		mustResponse(`{"type":"done","summary":"done","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`),
	}}
	executor := &fakeExecutor{results: []clnkr.CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
	agent := clnkr.NewAgent(model, executor, "/tmp")
	agent.Notify = func(event clnkr.Event) {
		if err := clnkrapp.WriteJSONL(&out, event); err != nil {
			t.Fatalf("WriteJSONL: %v", err)
		}
	}
	driver := clnkrapp.NewDriver(agent, nil)
	errCh := make(chan error, 1)
	go func() {
		errCh <- runJSONL(context.Background(), reader, &out, &errOut, driver)
	}()

	if _, err := writer.Write([]byte(`{"type":"prompt","text":"say hi","mode":"approval"}` + "\n")); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	waitForPending(t, driver, clnkrapp.PendingApproval)
	if _, err := writer.Write([]byte(`{"type":"reply","text":"y"}` + "\n")); err != nil {
		t.Fatalf("write reply: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("runJSONL: %v", err)
	}

	gotTypes := jsonlTypes(t, out.String())
	assertTypesContainInOrder(t, gotTypes, []string{"response", "approval_request", "command_start", "command_done", "response", "done"}, out.String())
	if fmt.Sprint(executor.gotCmds) != "[echo hi]" {
		t.Fatalf("commands = %v, want [echo hi]", executor.gotCmds)
	}
}

func TestRunJSONLShutdownReturnsWithoutClosingInput(t *testing.T) {
	var out, errOut bytes.Buffer
	reader, writer := io.Pipe()
	defer writer.Close() //nolint:errcheck

	model := blockingModel{}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	driver := clnkrapp.NewDriver(agent, nil)
	errCh := make(chan error, 1)
	go func() {
		errCh <- runJSONL(context.Background(), reader, &out, &errOut, driver)
	}()

	if _, err := writer.Write([]byte(`{"type":"prompt","text":"wait","mode":"full_send"}` + "\n")); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	if _, err := writer.Write([]byte(`{"type":"shutdown"}` + "\n")); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runJSONL: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runJSONL did not return after shutdown with open input")
	}
}

func TestRunJSONLReplyWithoutPendingRequestDoesNotCarryAcrossRuns(t *testing.T) {
	var out, errOut bytes.Buffer
	model := &fakeModel{responses: []clnkr.Response{mustResponse(`{"type":"done","summary":"finished","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`)}}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	agent.Notify = func(event clnkr.Event) {
		if err := clnkrapp.WriteJSONL(&out, event); err != nil {
			t.Fatalf("WriteJSONL: %v", err)
		}
	}
	driver := clnkrapp.NewDriver(agent, nil)
	input := strings.Join([]string{
		`{"type":"prompt","text":"inspect","mode":"full_send"}`,
		`{"type":"reply","text":"stale"}`,
		`{"type":"prompt","text":"run command","mode":"approval"}`,
		"",
	}, "\n")

	err := runJSONL(context.Background(), strings.NewReader(input), &out, &errOut, driver)
	if err == nil {
		t.Fatal("runJSONL returned nil, want reply error")
	}
	if !strings.Contains(err.Error(), "reply: no pending request") {
		t.Fatalf("error = %v, want no pending request", err)
	}
	if strings.Contains(out.String(), "approval_request") {
		t.Fatalf("stale reply carried into next run:\n%s", out.String())
	}
}

func TestRunJSONLAcceptsLargePromptLines(t *testing.T) {
	var out, errOut bytes.Buffer
	model := &fakeModel{responses: []clnkr.Response{mustResponse(`{"type":"done","summary":"finished","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`)}}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	agent.Notify = func(event clnkr.Event) {
		if err := clnkrapp.WriteJSONL(&out, event); err != nil {
			t.Fatalf("WriteJSONL: %v", err)
		}
	}
	driver := clnkrapp.NewDriver(agent, nil)
	prompt := strings.Repeat("x", 70*1024)
	input := fmt.Sprintf("%s\n", mustMarshalJSONLCommand(t, clnkrapp.JSONLCommand{
		Type: "prompt",
		Text: prompt,
		Mode: string(clnkrapp.PromptModeFullSend),
	}))

	err := runJSONL(context.Background(), strings.NewReader(input), &out, &errOut, driver)
	if err != nil {
		t.Fatalf("runJSONL: %v\nstderr:\n%s", err, errOut.String())
	}

	gotTypes := jsonlTypes(t, out.String())
	assertTypesContainInOrder(t, gotTypes, []string{"response", "done"}, out.String())
}

func TestRunJSONLCompactRoutesInstructionsThroughDriver(t *testing.T) {
	var out, errOut bytes.Buffer
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	compactor := &fakeCompactor{summary: "Older work summarized."}
	driver := clnkrapp.NewDriver(agent, func(instructions string) clnkr.Compactor {
		if instructions != "focus tests" {
			t.Fatalf("instructions = %q, want focus tests", instructions)
		}
		return compactor
	})

	err := runJSONL(context.Background(), strings.NewReader(`{"type":"compact","instructions":"focus tests"}`+"\n"), &out, &errOut, driver)
	if err != nil {
		t.Fatalf("runJSONL: %v", err)
	}

	gotTypes := jsonlTypes(t, out.String())
	wantTypes := []string{"compacted"}
	if !slices.Equal(gotTypes, wantTypes) {
		t.Fatalf("event types = %v, want %v\nstdout:\n%s", gotTypes, wantTypes, out.String())
	}
}

func TestRunJSONLInvalidCommandWritesDiagnostic(t *testing.T) {
	var out, errOut bytes.Buffer
	driver := clnkrapp.NewDriver(clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp"), nil)

	err := runJSONL(context.Background(), strings.NewReader(`{"type":"bogus"}`+"\n"), &out, &errOut, driver)
	if err == nil {
		t.Fatal("runJSONL returned nil, want error")
	}
	if !strings.Contains(errOut.String(), `unknown JSONL command type "bogus"`) {
		t.Fatalf("stderr = %q, want invalid command diagnostic", errOut.String())
	}
}

func TestRunJSONLCommandErrorWaitsForActivePrompt(t *testing.T) {
	var out, errOut bytes.Buffer
	reader, writer := io.Pipe()
	defer writer.Close() //nolint:errcheck

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := newCancellableBlockingModel()
	t.Cleanup(model.releaseQuery)
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	driver := clnkrapp.NewDriver(agent, nil)
	errCh := make(chan error, 1)
	go func() {
		errCh <- runJSONL(ctx, reader, &out, &errOut, driver)
	}()

	if _, err := writer.Write([]byte(`{"type":"prompt","text":"wait","mode":"full_send"}` + "\n")); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	waitForSignal(t, model.started, "model query")

	if _, err := writer.Write([]byte(`{"type":"prompt","text":"second","mode":"full_send"}` + "\n")); err != nil {
		t.Fatalf("write second prompt: %v", err)
	}
	waitForSignal(t, model.cancelled, "prompt cancellation")

	select {
	case err := <-errCh:
		t.Fatalf("runJSONL returned before active prompt exited: %v", err)
	default:
	}

	model.releaseQuery()
	err := waitForRunJSONL(t, errCh)
	if err == nil {
		t.Fatal("runJSONL returned nil, want prompt-in-progress error")
	}
	if !strings.Contains(err.Error(), "prompt: driver run already in progress") {
		t.Fatalf("error = %v, want prompt-in-progress error", err)
	}
	select {
	case <-model.exited:
	default:
		t.Fatal("runJSONL returned before active prompt exited")
	}
	if !strings.Contains(errOut.String(), "prompt: driver run already in progress") {
		t.Fatalf("stderr = %q, want prompt-in-progress diagnostic", errOut.String())
	}
}

func jsonlTypes(t *testing.T, text string) []string {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	types := make([]string, 0, len(lines))
	for _, line := range lines {
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		types = append(types, event.Type)
	}
	return types
}

func assertTypesContainInOrder(t *testing.T, got []string, want []string, stdout string) {
	t.Helper()

	next := 0
	for _, typ := range got {
		if next < len(want) && typ == want[next] {
			next++
		}
	}
	if next != len(want) {
		t.Fatalf("event types = %v, want subsequence %v\nstdout:\n%s", got, want, stdout)
	}
}

func waitForPending(t *testing.T, driver *clnkrapp.Driver, want string) {
	t.Helper()

	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if got := driver.Pending(); got == want {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatalf("driver.Pending() never became %q", want)
		}
	}
}

func waitForSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitForRunJSONL(t *testing.T, errCh <-chan error) error {
	t.Helper()

	select {
	case err := <-errCh:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("runJSONL did not return")
		return nil
	}
}

func mustResponse(raw string) clnkr.Response {
	turn, err := clnkr.ParseTurn(raw)
	if err == nil {
		return clnkr.Response{Turn: turn, Raw: raw}
	}
	var env struct {
		Type    string `json:"type"`
		Summary string `json:"summary"`
	}
	if json.Unmarshal([]byte(raw), &env) == nil && env.Type == "done" {
		return clnkr.Response{Turn: verifiedDone(env.Summary), Raw: raw}
	}
	panic(err)
}

func verifiedDone(summary string) *clnkr.DoneTurn {
	return &clnkr.DoneTurn{
		Summary: summary,
		Verification: clnkr.CompletionVerification{
			Status: clnkr.VerificationVerified,
			Checks: []clnkr.VerificationCheck{{
				Command:  "test -d .",
				Outcome:  "passed",
				Evidence: "current working directory was available for completion",
			}},
		},
		KnownRisks: []string{},
	}
}

func mustMarshalJSONLCommand(t *testing.T, command clnkrapp.JSONLCommand) string {
	t.Helper()

	data, err := json.Marshal(command)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(data)
}

type blockingModel struct{}

func (blockingModel) Query(ctx context.Context, _ []clnkr.Message) (clnkr.Response, error) {
	<-ctx.Done()
	return clnkr.Response{}, ctx.Err()
}

type cancellableBlockingModel struct {
	started     chan struct{}
	cancelled   chan struct{}
	release     chan struct{}
	exited      chan struct{}
	releaseOnce sync.Once
}

func newCancellableBlockingModel() *cancellableBlockingModel {
	return &cancellableBlockingModel{
		started:   make(chan struct{}),
		cancelled: make(chan struct{}),
		release:   make(chan struct{}),
		exited:    make(chan struct{}),
	}
}

func (m *cancellableBlockingModel) Query(ctx context.Context, _ []clnkr.Message) (clnkr.Response, error) {
	defer close(m.exited)
	close(m.started)
	<-ctx.Done()
	close(m.cancelled)
	<-m.release
	return clnkr.Response{}, ctx.Err()
}

func (m *cancellableBlockingModel) releaseQuery() {
	m.releaseOnce.Do(func() {
		close(m.release)
	})
}

type fakeModel struct {
	responses []clnkr.Response
	calls     int
}

func (m *fakeModel) Query(context.Context, []clnkr.Message) (clnkr.Response, error) {
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
	gotCmds []string
}

func (e *fakeExecutor) Execute(_ context.Context, command, _ string) (clnkr.CommandResult, error) {
	e.gotCmds = append(e.gotCmds, command)
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
	messages []clnkr.Message
}

func (c *fakeCompactor) Summarize(_ context.Context, messages []clnkr.Message) (string, error) {
	c.messages = append([]clnkr.Message(nil), messages...)
	if c.err != nil {
		return "", c.err
	}
	return c.summary, nil
}

func compactableMessages() []clnkr.Message {
	return []clnkr.Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done again","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`},
		{Role: "user", Content: "third task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done third","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`},
	}
}
