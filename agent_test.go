package clnkr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeModel struct {
	responses []Response
	calls     int
	got       [][]Message
}

func (m *fakeModel) Query(_ context.Context, messages []Message) (Response, error) {
	m.got = append(m.got, append([]Message{}, messages...))
	if m.calls >= len(m.responses) {
		return Response{}, fmt.Errorf("no more responses")
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

type fakeExecutor struct {
	results []CommandResult
	errs    []error
	calls   int
	gotCmds []string
	gotDirs []string
	gotEnv  []map[string]string
}

func (e *fakeExecutor) Execute(_ context.Context, command string, dir string) (CommandResult, error) {
	e.gotCmds = append(e.gotCmds, command)
	e.gotDirs = append(e.gotDirs, dir)
	if e.calls >= len(e.results) {
		return CommandResult{}, fmt.Errorf("no more outputs")
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

func (e *fakeExecutor) SetEnv(env map[string]string) {
	cp := make(map[string]string, len(env))
	for k, v := range env {
		cp[k] = v
	}
	e.gotEnv = append(e.gotEnv, cp)
}

func mustCommandPayload(t *testing.T, result CommandResult) string {
	t.Helper()
	return formatCommandOutput(result)
}

// collectEvents returns a Notify function and a pointer to the collected events slice.
func collectEvents() (func(Event), *[]Event) {
	var events []Event
	return func(e Event) { events = append(events, e) }, &events
}

func TestStep(t *testing.T) {
	t.Run("returns command result", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"act","command":"ls"}`}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "file1.go\n", ExitCode: 0}}}

		agent := NewAgent(model, executor, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "list files"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		act, ok := result.Turn.(*ActTurn)
		if !ok {
			t.Fatalf("expected *ActTurn, got %T", result.Turn)
		}
		if act.Command != "ls" {
			t.Errorf("got command %q, want %q", act.Command, "ls")
		}
		wantOutput := mustCommandPayload(t, CommandResult{Command: "ls", Stdout: "file1.go\n", ExitCode: 0})
		if result.Output != wantOutput {
			t.Errorf("got output %q, want %q", result.Output, wantOutput)
		}
		if result.ExecErr != nil {
			t.Errorf("unexpected exec error: %v", result.ExecErr)
		}
	})

	t.Run("returns done turn", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"Listed all files."}`}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "done"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		done, ok := result.Turn.(*DoneTurn)
		if !ok {
			t.Fatalf("expected *DoneTurn, got %T", result.Turn)
		}
		if done.Summary != "Listed all files." {
			t.Errorf("got summary %q, want %q", done.Summary, "Listed all files.")
		}
		if result.Output != "" {
			t.Errorf("expected empty output for done, got %q", result.Output)
		}
	})

	t.Run("returns clarify turn", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"clarify","question":"Which directory should I inspect?"}`}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "do something"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cl, ok := result.Turn.(*ClarifyTurn)
		if !ok {
			t.Fatalf("expected *ClarifyTurn, got %T", result.Turn)
		}
		if cl.Question != "Which directory should I inspect?" {
			t.Errorf("got question %q", cl.Question)
		}
	})

	t.Run("protocol failure on non-JSON response", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "I'm not sure what to do here."}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "do something"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Turn != nil {
			t.Errorf("expected nil Turn on protocol failure, got %T", result.Turn)
		}
		if result.ParseErr == nil {
			t.Error("expected ParseErr to be set")
		}
		// Should have appended a protocol_error correction message
		last := agent.messages[len(agent.messages)-1]
		if !strings.Contains(last.Content, "[protocol_error]") {
			t.Error("expected protocol_error correction message")
		}
	})

	t.Run("protocol failure on missing command", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"act"}`}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "do it"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Turn != nil {
			t.Errorf("expected nil Turn, got %T", result.Turn)
		}
		if result.ParseErr == nil {
			t.Error("expected ParseErr for missing command")
		}
		if !errors.Is(result.ParseErr, ErrMissingCommand) {
			t.Errorf("expected ErrMissingCommand, got %v", result.ParseErr)
		}
	})

	t.Run("emits events via Notify", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"act","command":"echo hi"}`}, Usage: Usage{InputTokens: 10, OutputTokens: 5}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
		notify, events := collectEvents()

		agent := NewAgent(model, executor, "/tmp")
		agent.Notify = notify
		agent.messages = append(agent.messages, Message{Role: "user", Content: "say hi"})

		_, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Check we got the expected event types in order
		var types []string
		for _, e := range *events {
			switch e.(type) {
			case EventDebug:
				types = append(types, "debug")
			case EventResponse:
				types = append(types, "response")
			case EventCommandStart:
				types = append(types, "cmd_start")
			case EventCommandDone:
				types = append(types, "cmd_done")
			case EventProtocolFailure:
				types = append(types, "proto_fail")
			}
		}

		// Must have response, cmd_start, cmd_done (debug events may vary)
		hasResponse := false
		hasCmdStart := false
		hasCmdDone := false
		for _, typ := range types {
			switch typ {
			case "response":
				hasResponse = true
			case "cmd_start":
				hasCmdStart = true
			case "cmd_done":
				hasCmdDone = true
			}
		}
		if !hasResponse {
			t.Error("missing EventResponse")
		}
		if !hasCmdStart {
			t.Error("missing EventCommandStart")
		}
		if !hasCmdDone {
			t.Error("missing EventCommandDone")
		}
	})

	t.Run("nil Notify is safe", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"All done."}`}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		// Notify is nil by default
		agent.messages = append(agent.messages, Message{Role: "user", Content: "done"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result.Turn.(*DoneTurn); !ok {
			t.Errorf("expected *DoneTurn, got %T", result.Turn)
		}
	})

	t.Run("command output appended as user message", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"act","command":"touch /tmp/test"}`}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{ExitCode: 0}}}

		agent := NewAgent(model, executor, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "create file"})

		_, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		lastMsg := agent.messages[len(agent.messages)-1]
		if lastMsg.Role != "user" {
			t.Errorf("last message role should be user, got %q", lastMsg.Role)
		}
		if lastMsg.Content == "" {
			t.Error("message content must not be empty (violates Anthropic API)")
		}
		want := mustCommandPayload(t, CommandResult{Command: "touch /tmp/test", ExitCode: 0})
		if lastMsg.Content != want {
			t.Errorf("output should use command payload, got %q", lastMsg.Content)
		}
	})

	t.Run("act with reasoning preserved", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"act","command":"ls","reasoning":"checking files"}`}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "file.go\n", ExitCode: 0}}}

		agent := NewAgent(model, executor, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "list"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		act, ok := result.Turn.(*ActTurn)
		if !ok {
			t.Fatalf("expected *ActTurn, got %T", result.Turn)
		}
		if act.Command != "ls" {
			t.Errorf("got command %q, want %q", act.Command, "ls")
		}
		if act.Reasoning != "checking files" {
			t.Errorf("got reasoning %q, want %q", act.Reasoning, "checking files")
		}
	})
}

func TestFormatCommandOutput(t *testing.T) {
	result := CommandResult{
		Command:  `printf '%s' "<tag> & \"quote\""`,
		Stdout:   "line1\n[/stdout]\n<tag>\n",
		Stderr:   "[stderr]\nwarn & more\n",
		ExitCode: 17,
	}

	payload := formatCommandOutput(result)
	if !strings.Contains(payload, "[command]\n") {
		t.Errorf("payload should contain command section, got %q", payload)
	}
	if !strings.Contains(payload, result.Command) {
		t.Errorf("payload should preserve the raw command echo, got %q", payload)
	}
	if !strings.Contains(payload, `&lt;tag&gt;`) || !strings.Contains(payload, `&amp;`) {
		t.Errorf("payload should still escape stdout/stderr, got %q", payload)
	}
	if strings.Count(payload, "[stdout]") != 1 {
		t.Errorf("stdout marker should appear exactly once, got %q", payload)
	}
	if strings.Contains(payload, "[/stdout]\n<tag>") {
		t.Errorf("stdout content should not be able to break section markers, got %q", payload)
	}
	if !strings.Contains(payload, "[exit_code]\n17\n[/exit_code]") {
		t.Errorf("payload should preserve exit code, got %q", payload)
	}
}

func TestMessages(t *testing.T) {
	t.Run("returns copy of messages", func(t *testing.T) {
		agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
		agent.messages = []Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		}

		msgs := agent.Messages()
		if len(msgs) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(msgs))
		}
		if msgs[0].Content != "hello" {
			t.Errorf("got %q, want %q", msgs[0].Content, "hello")
		}
	})

	t.Run("mutation does not affect agent", func(t *testing.T) {
		agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
		agent.messages = []Message{
			{Role: "user", Content: "hello"},
		}

		msgs := agent.Messages()
		msgs[0].Content = "mutated"

		if agent.messages[0].Content != "hello" {
			t.Error("mutation of returned slice should not affect agent")
		}
	})
}

func TestAgentAppliesLeadingShellState(t *testing.T) {
	t.Run("persists export to future commands", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"act","command":"export NANOCHAT_BASE_DIR=/tmp/runtime && echo ok"}`}},
			{Message: Message{Role: "assistant", Content: `{"type":"act","command":"printf %s \"$NANOCHAT_BASE_DIR\""}`}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "ok\n", ExitCode: 0}, {Stdout: "/tmp/runtime", ExitCode: 0}}}

		agent := NewAgent(model, executor, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "set env then use it"})

		if _, err := agent.Step(context.Background()); err != nil {
			t.Fatalf("step 1: %v", err)
		}
		if _, err := agent.Step(context.Background()); err != nil {
			t.Fatalf("step 2: %v", err)
		}
		if len(executor.gotEnv) < 2 {
			t.Fatalf("expected env snapshots for both commands, got %d", len(executor.gotEnv))
		}
		if got := executor.gotEnv[1]["NANOCHAT_BASE_DIR"]; got != "/tmp/runtime" {
			t.Fatalf("got env %q, want %q", got, "/tmp/runtime")
		}
	})

	t.Run("persists cd across chained commands", func(t *testing.T) {
		root := t.TempDir()
		next := filepath.Join(root, "subdir")
		if err := os.Mkdir(next, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: fmt.Sprintf(`{"type":"act","command":"cd %s && pwd"}`, next)}},
			{Message: Message{Role: "assistant", Content: `{"type":"act","command":"pwd"}`}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: next + "\n", ExitCode: 0}, {Stdout: next + "\n", ExitCode: 0}}}

		agent := NewAgent(model, executor, root)
		agent.messages = append(agent.messages, Message{Role: "user", Content: "change dir then reuse it"})

		if _, err := agent.Step(context.Background()); err != nil {
			t.Fatalf("step 1: %v", err)
		}
		if _, err := agent.Step(context.Background()); err != nil {
			t.Fatalf("step 2: %v", err)
		}
		if len(executor.gotDirs) < 2 {
			t.Fatalf("expected two execute calls, got %d", len(executor.gotDirs))
		}
		if executor.gotDirs[1] != next {
			t.Fatalf("got dir %q, want %q", executor.gotDirs[1], next)
		}
	})
}

func TestAddMessages(t *testing.T) {
	t.Run("prepends to existing history", func(t *testing.T) {
		agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
		agent.messages = []Message{{Role: "user", Content: "existing"}}

		if err := agent.AddMessages([]Message{
			{Role: "user", Content: "seed1"},
			{Role: "assistant", Content: "seed2"},
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		msgs := agent.Messages()
		if len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(msgs))
		}
		if msgs[0].Content != "seed1" {
			t.Errorf("first message should be seed1, got %q", msgs[0].Content)
		}
		if msgs[2].Content != "existing" {
			t.Errorf("last message should be existing, got %q", msgs[2].Content)
		}
	})

	t.Run("works on empty history", func(t *testing.T) {
		agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")

		if err := agent.AddMessages([]Message{
			{Role: "user", Content: "hello"},
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		msgs := agent.Messages()
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
		if msgs[0].Content != "hello" {
			t.Errorf("got %q, want %q", msgs[0].Content, "hello")
		}
	})

	t.Run("nil slice is no-op", func(t *testing.T) {
		agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
		agent.messages = []Message{{Role: "user", Content: "existing"}}

		if err := agent.AddMessages(nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		msgs := agent.Messages()
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
	})

	t.Run("does not alias caller slice", func(t *testing.T) {
		agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")

		// Create a slice with spare capacity so naive append would reuse it.
		caller := make([]Message, 1, 10)
		caller[0] = Message{Role: "system", Content: "original"}

		if err := agent.AddMessages(caller); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Mutate the caller's slice — must not affect the agent.
		caller[0].Content = "corrupted"

		msgs := agent.Messages()
		if msgs[0].Content != "original" {
			t.Errorf("AddMessages aliased caller slice: got %q, want %q",
				msgs[0].Content, "original")
		}
	})

	t.Run("returns error after Step", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"All done."}`}},
		}}
		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "hi"})

		if _, err := agent.Step(context.Background()); err != nil {
			t.Fatalf("unexpected Step error: %v", err)
		}

		err := agent.AddMessages([]Message{{Role: "user", Content: "late"}})
		if err == nil {
			t.Fatal("expected error calling AddMessages after Step")
		}
	})
}

func TestAgent(t *testing.T) {
	t.Run("single step then exit", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"act","command":"ls"}`}},
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"Listed files."}`}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "file1.go\n", ExitCode: 0}}}

		agent := NewAgent(model, executor, "/tmp")
		err := agent.Run(context.Background(), "list files")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.calls != 1 {
			t.Errorf("expected 1 command, got %d", executor.calls)
		}
		if executor.gotCmds[0] != "ls" {
			t.Errorf("got command %q, want %q", executor.gotCmds[0], "ls")
		}
	})

	t.Run("returns after clarification request", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"clarify","question":"Which repo should I inspect?"}`}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		err := agent.Run(context.Background(), "debug this")
		if !errors.Is(err, ErrClarificationNeeded) {
			t.Fatalf("expected ErrClarificationNeeded, got %v", err)
		}
		if model.calls != 1 {
			t.Fatalf("expected one model call, got %d", model.calls)
		}
		msgs := agent.Messages()
		if len(msgs) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(msgs))
		}
		if msgs[1].Role != "assistant" {
			t.Fatalf("unexpected message role: %q", msgs[1].Role)
		}
	})

	t.Run("respects max steps", func(t *testing.T) {
		responses := make([]Response, 12)
		for i := 0; i < 10; i++ {
			responses[i] = Response{Message: Message{Role: "assistant", Content: `{"type":"act","command":"echo step"}`}}
		}
		responses[10] = Response{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"Step limit reached."}`}}

		model := &fakeModel{responses: responses}
		results := make([]CommandResult, 10)
		for i := 0; i < 10; i++ {
			results[i] = CommandResult{Stdout: "step", ExitCode: 0}
		}
		executor := &fakeExecutor{results: results}

		agent := NewAgent(model, executor, "/tmp")
		agent.MaxSteps = 3
		err := agent.Run(context.Background(), "do stuff")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.calls > 3 {
			t.Errorf("expected at most 3 commands, got %d", executor.calls)
		}
	})

	t.Run("default max steps is 100", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"Done."}`}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		if agent.MaxSteps != 100 {
			t.Errorf("default MaxSteps should be 100, got %d", agent.MaxSteps)
		}
	})

	t.Run("tracks cwd on cd", func(t *testing.T) {
		realDir := t.TempDir()
		subDir := filepath.Join(realDir, "sub")
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			t.Fatalf("failed to create subdir: %v", err)
		}

		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: fmt.Sprintf(`{"type":"act","command":"cd %s"}`, subDir)}},
			{Message: Message{Role: "assistant", Content: `{"type":"act","command":"ls"}`}},
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"Done."}`}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{ExitCode: 0}, {Stdout: "files", ExitCode: 0}}}

		agent := NewAgent(model, executor, realDir)
		err := agent.Run(context.Background(), "go to sub and list")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.gotDirs[1] != subDir {
			t.Errorf("second command dir %q, want %q", executor.gotDirs[1], subDir)
		}
	})

	t.Run("cd to nonexistent dir keeps previous cwd", func(t *testing.T) {
		startDir := t.TempDir()
		nonexistent := filepath.Join(t.TempDir(), "does-not-exist")
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: fmt.Sprintf(`{"type":"act","command":"cd %s"}`, nonexistent)}},
			{Message: Message{Role: "assistant", Content: `{"type":"act","command":"ls"}`}},
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"Done."}`}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{ExitCode: 0}, {Stdout: "files", ExitCode: 0}}}

		agent := NewAgent(model, executor, startDir)
		err := agent.Run(context.Background(), "try bad cd")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.gotDirs[1] != startDir {
			t.Errorf("cwd after bad cd should stay %q, got %q", startDir, executor.gotDirs[1])
		}
	})

	t.Run("protocol error then recovery", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "I'll just explain what to do..."}},
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"Done."}`}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		err := agent.Run(context.Background(), "do something")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Verify correction message was sent
		lastMsgs := model.got[len(model.got)-1]
		found := false
		for _, msg := range lastMsgs {
			if strings.Contains(msg.Content, "[protocol_error]") {
				found = true
				break
			}
		}
		if !found {
			t.Error("protocol error correction message should be in conversation")
		}
	})

	t.Run("exits on consecutive protocol errors", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "no json 1"}},
			{Message: Message{Role: "assistant", Content: "no json 2"}},
			{Message: Message{Role: "assistant", Content: "no json 3"}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		err := agent.Run(context.Background(), "do something")
		if err == nil {
			t.Error("expected error on consecutive protocol failures")
		}
		if !strings.Contains(err.Error(), "protocol") {
			t.Errorf("error should mention protocol, got: %v", err)
		}
	})

	t.Run("emits events for output", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"act","command":"echo hi"}`}, Usage: Usage{InputTokens: 10, OutputTokens: 5}},
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"Done."}`}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
		notify, events := collectEvents()

		agent := NewAgent(model, executor, "/tmp")
		agent.Notify = notify
		err := agent.Run(context.Background(), "test events")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var gotResponse, gotCmdStart, gotCmdDone bool
		for _, e := range *events {
			switch ev := e.(type) {
			case EventResponse:
				if !gotResponse {
					gotResponse = true
					if ev.Usage.InputTokens != 10 {
						t.Errorf("expected 10 input tokens, got %d", ev.Usage.InputTokens)
					}
				}
			case EventCommandStart:
				gotCmdStart = true
				if ev.Command != "echo hi" {
					t.Errorf("expected command %q, got %q", "echo hi", ev.Command)
				}
			case EventCommandDone:
				gotCmdDone = true
				if ev.Stdout != "hi\n" {
					t.Errorf("expected stdout %q, got %q", "hi\n", ev.Stdout)
				}
				if ev.ExitCode != 0 {
					t.Errorf("expected exit code 0, got %d", ev.ExitCode)
				}
			default:
			}
		}
		if !gotResponse {
			t.Error("missing EventResponse")
		}
		if !gotCmdStart {
			t.Error("missing EventCommandStart")
		}
		if !gotCmdDone {
			t.Error("missing EventCommandDone")
		}
	})

	t.Run("compound cd does not update cwd", func(t *testing.T) {
		startDir := t.TempDir()
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"act","command":"cd /tmp && ls"}`}},
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"Done."}`}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "stuff", ExitCode: 0}}}

		agent := NewAgent(model, executor, startDir)
		err := agent.Run(context.Background(), "compound cd")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.gotDirs[0] != startDir {
			t.Errorf("compound cd should not change cwd, got dir %q", executor.gotDirs[0])
		}
	})

	t.Run("cd with tilde prefix expands home", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("cannot get home dir")
		}
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"act","command":"cd ~"}`}},
			{Message: Message{Role: "assistant", Content: `{"type":"act","command":"ls"}`}},
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"Done."}`}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{ExitCode: 0}, {Stdout: "files", ExitCode: 0}}}

		agent := NewAgent(model, executor, "/tmp")
		err = agent.Run(context.Background(), "go home")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.gotDirs[1] != home {
			t.Errorf("expected cwd %q after cd ~, got %q", home, executor.gotDirs[1])
		}
	})

	t.Run("execution error is preserved in command payload", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"act","command":"false"}`}},
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"Done."}`}},
		}}
		failExecutor := &fakeExecutor{
			results: []CommandResult{{Stderr: "nope\n", ExitCode: 1}},
			errs:    []error{fmt.Errorf("run command: exit status 1")},
		}

		agent := NewAgent(model, failExecutor, "/tmp")
		err := agent.Run(context.Background(), "run failing command")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(model.got) < 2 {
			t.Fatal("expected at least 2 model queries")
		}
		lastMsgs := model.got[1]
		lastMsg := lastMsgs[len(lastMsgs)-1]
		if lastMsg.Role != "user" {
			t.Errorf("expected user message with output, got role %q", lastMsg.Role)
		}
		if !strings.Contains(lastMsg.Content, "[exit_code]\n1\n[/exit_code]") {
			t.Errorf("expected command payload to preserve exit code, got %q", lastMsg.Content)
		}
		if !strings.Contains(lastMsg.Content, "nope") {
			t.Errorf("expected stderr in command payload, got %q", lastMsg.Content)
		}
	})

	t.Run("returns error on context cancellation", func(t *testing.T) {
		model := &fakeModel{responses: []Response{}}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		err := agent.Run(ctx, "do something")
		if err == nil {
			t.Error("expected error on cancelled context")
		}
	})
}
