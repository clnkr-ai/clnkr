package clnkr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr/transcript"
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

type fakeCompactor struct {
	summary string
	err     error
	got     [][]Message
}

func (c *fakeCompactor) Summarize(_ context.Context, messages []Message) (string, error) {
	cp := append([]Message{}, messages...)
	c.got = append(c.got, cp)
	if c.err != nil {
		return "", c.err
	}
	return c.summary, nil
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
	return transcript.FormatCommandResult(transcript.CommandResult{
		Command:  result.Command,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
		Feedback: result.Feedback,
	})
}

func mustStepAct(t *testing.T, agent *Agent) *ActTurn {
	t.Helper()
	result, err := agent.Step(context.Background())
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	act, ok := result.Turn.(*ActTurn)
	if !ok {
		t.Fatalf("expected *ActTurn, got %T", result.Turn)
	}
	return act
}

func runActStep(t *testing.T, agent *Agent) StepResult {
	t.Helper()
	act := mustStepAct(t, agent)
	result, err := agent.ExecuteTurn(context.Background(), act)
	if err != nil {
		t.Fatalf("ExecuteTurn: %v", err)
	}
	return result
}

func bashBatch(commands ...BashAction) BashBatch {
	return BashBatch{Commands: commands}
}

func actJSON(commands ...string) string {
	items := make([]string, 0, len(commands))
	for _, command := range commands {
		items = append(items, fmt.Sprintf(`{"command":%q,"workdir":null}`, command))
	}
	return fmt.Sprintf(`{"type":"act","bash":{"commands":[%s]}}`, strings.Join(items, ","))
}

func actJSONWithReasoning(reasoning string, commands ...string) string {
	items := make([]string, 0, len(commands))
	for _, command := range commands {
		items = append(items, fmt.Sprintf(`{"command":%q,"workdir":null}`, command))
	}
	return fmt.Sprintf(`{"type":"act","bash":{"commands":[%s]},"reasoning":%q}`, strings.Join(items, ","), reasoning)
}

// collectEvents returns a Notify function and a pointer to the collected events slice.
func collectEvents() (func(Event), *[]Event) {
	var events []Event
	return func(e Event) { events = append(events, e) }, &events
}

func TestStep(t *testing.T) {
	t.Run("returns act turn without executing command", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: actJSON("ls")}},
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
		if got := len(act.Bash.Commands); got != 1 {
			t.Fatalf("len(commands) = %d, want 1", got)
		}
		if act.Bash.Commands[0].Command != "ls" {
			t.Errorf("got command %q, want %q", act.Bash.Commands[0].Command, "ls")
		}
		if executor.calls != 0 {
			t.Fatalf("Step should not execute commands, got %d calls", executor.calls)
		}
		if result.Output != "" {
			t.Errorf("expected empty output for Step act turn, got %q", result.Output)
		}
		if result.ExecErr != nil {
			t.Errorf("expected empty exec error for Step act turn, got %v", result.ExecErr)
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
			{Message: Message{Role: "assistant", Content: `{"type":"act","bash":{"commands":[{"workdir":null}]}}`}},
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

	t.Run("protocol failure on response protocol error", func(t *testing.T) {
		raw := `{"type":"done","summary":"ignored schema"}`
		model := &fakeModel{responses: []Response{
			{
				Message:     Message{Role: "assistant", Content: raw},
				ProtocolErr: fmt.Errorf("%w: missing required structured output field %q", ErrInvalidJSON, "turn"),
			},
		}}
		notify, events := collectEvents()

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.Notify = notify
		agent.messages = append(agent.messages, Message{Role: "user", Content: "do it"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Turn != nil {
			t.Fatalf("expected nil Turn, got %T", result.Turn)
		}
		if !errors.Is(result.ParseErr, ErrInvalidJSON) {
			t.Fatalf("expected ErrInvalidJSON, got %v", result.ParseErr)
		}
		if got := agent.messages[len(agent.messages)-2]; got != (Message{Role: "assistant", Content: raw}) {
			t.Fatalf("assistant message = %#v, want raw payload", got)
		}
		if last := agent.messages[len(agent.messages)-1]; !strings.Contains(last.Content, "[protocol_error]") {
			t.Fatalf("last message = %q, want protocol correction", last.Content)
		}

		var sawResponse, sawProtocolFailure bool
		for _, e := range *events {
			switch e.(type) {
			case EventResponse:
				sawResponse = true
			case EventProtocolFailure:
				sawProtocolFailure = true
			}
		}
		if !sawProtocolFailure {
			t.Fatal("missing EventProtocolFailure")
		}
		if sawResponse {
			t.Fatal("unexpected EventResponse for response protocol error")
		}
	})

	t.Run("emits response events via Notify without command events", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: actJSON("echo hi")}, Usage: Usage{InputTokens: 10, OutputTokens: 5}},
		}}
		notify, events := collectEvents()

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
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

		// Must have response, but no command events yet.
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
		if hasCmdStart {
			t.Error("Step should not emit EventCommandStart")
		}
		if hasCmdDone {
			t.Error("Step should not emit EventCommandDone")
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

	t.Run("act with reasoning preserved", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: actJSONWithReasoning("checking files", "ls")}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "list"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		act, ok := result.Turn.(*ActTurn)
		if !ok {
			t.Fatalf("expected *ActTurn, got %T", result.Turn)
		}
		if got := len(act.Bash.Commands); got != 1 {
			t.Fatalf("len(commands) = %d, want 1", got)
		}
		if act.Bash.Commands[0].Command != "ls" {
			t.Errorf("got command %q, want %q", act.Bash.Commands[0].Command, "ls")
		}
		if act.Reasoning != "checking files" {
			t.Errorf("got reasoning %q, want %q", act.Reasoning, "checking files")
		}
	})
}

func TestExecuteTurn(t *testing.T) {
	t.Run("appends command output as user message", func(t *testing.T) {
		executor := &fakeExecutor{results: []CommandResult{{ExitCode: 0}}}
		agent := NewAgent(&fakeModel{}, executor, "/tmp")

		result, err := agent.ExecuteTurn(context.Background(), &ActTurn{Bash: bashBatch(BashAction{Command: "touch /tmp/test"})})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ExecErr != nil {
			t.Fatalf("unexpected exec error: %v", result.ExecErr)
		}

		if len(agent.messages) != 2 {
			t.Fatalf("expected command payload plus state message, got %#v", agent.messages)
		}
		payloadMsg := agent.messages[len(agent.messages)-2]
		if payloadMsg.Role != "user" {
			t.Errorf("payload message role should be user, got %q", payloadMsg.Role)
		}
		if payloadMsg.Content == "" {
			t.Error("message content must not be empty (violates Anthropic API)")
		}
		want := mustCommandPayload(t, CommandResult{Command: "touch /tmp/test", ExitCode: 0})
		if payloadMsg.Content != want {
			t.Errorf("output should use command payload, got %q", payloadMsg.Content)
		}
		stateMsg := agent.messages[len(agent.messages)-1]
		if stateMsg != (Message{Role: "user", Content: transcript.FormatStateMessage("/tmp")}) {
			t.Errorf("unexpected trailing state message: %#v", stateMsg)
		}
	})

	t.Run("emits command events via Notify", func(t *testing.T) {
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
		notify, events := collectEvents()

		agent := NewAgent(&fakeModel{}, executor, "/tmp")
		agent.Notify = notify

		_, err := agent.ExecuteTurn(context.Background(), &ActTurn{Bash: bashBatch(BashAction{Command: "echo hi"})})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var hasCmdStart, hasCmdDone bool
		for _, e := range *events {
			switch e.(type) {
			case EventCommandStart:
				hasCmdStart = true
			case EventCommandDone:
				hasCmdDone = true
			}
		}
		if !hasCmdStart {
			t.Error("missing EventCommandStart")
		}
		if !hasCmdDone {
			t.Error("missing EventCommandDone")
		}
	})

	t.Run("resolves relative workdir against current cwd", func(t *testing.T) {
		executor := &fakeExecutor{results: []CommandResult{{ExitCode: 0}}}
		agent := NewAgent(&fakeModel{}, executor, "/repo")

		result, err := agent.ExecuteTurn(context.Background(), &ActTurn{
			Bash: bashBatch(BashAction{Command: "pwd", Workdir: "subdir"}),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := executor.gotDirs[0]; got != filepath.Join("/repo", "subdir") {
			t.Fatalf("executor dir = %q, want %q", got, filepath.Join("/repo", "subdir"))
		}
		if !strings.Contains(result.Output, "[command]\npwd\n[/command]") {
			t.Fatalf("output = %q, want command payload for pwd", result.Output)
		}
	})

	t.Run("uses absolute workdir directly", func(t *testing.T) {
		executor := &fakeExecutor{results: []CommandResult{{ExitCode: 0}}}
		agent := NewAgent(&fakeModel{}, executor, "/repo")
		absDir := filepath.Join(t.TempDir(), "elsewhere")

		_, err := agent.ExecuteTurn(context.Background(), &ActTurn{
			Bash: bashBatch(BashAction{Command: "pwd", Workdir: absDir}),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := executor.gotDirs[0]; got != absDir {
			t.Fatalf("executor dir = %q, want %q", got, absDir)
		}
	})

	t.Run("executes two commands and appends two payload messages", func(t *testing.T) {
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: "one\n", ExitCode: 0},
			{Stdout: "two\n", ExitCode: 0},
		}}
		agent := NewAgent(&fakeModel{}, executor, "/repo")

		result, err := agent.ExecuteTurn(context.Background(), &ActTurn{
			Bash: bashBatch(
				BashAction{Command: "echo one"},
				BashAction{Command: "echo two"},
			),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ExecErr != nil {
			t.Fatalf("unexpected exec error: %v", result.ExecErr)
		}
		if result.ExecCount != 2 {
			t.Fatalf("ExecCount = %d, want 2", result.ExecCount)
		}
		if executor.calls != 2 {
			t.Fatalf("executor calls = %d, want 2", executor.calls)
		}

		wantFirst := mustCommandPayload(t, CommandResult{Command: "echo one", Stdout: "one\n", ExitCode: 0})
		wantSecond := mustCommandPayload(t, CommandResult{Command: "echo two", Stdout: "two\n", ExitCode: 0})
		if result.Output != wantFirst+"\n\n"+wantSecond {
			t.Fatalf("Output = %q, want joined payloads", result.Output)
		}
		if len(agent.messages) != 3 {
			t.Fatalf("expected two payload messages plus state message, got %#v", agent.messages)
		}
		if agent.messages[0] != (Message{Role: "user", Content: wantFirst}) {
			t.Fatalf("first payload message = %#v, want %#v", agent.messages[0], Message{Role: "user", Content: wantFirst})
		}
		if agent.messages[1] != (Message{Role: "user", Content: wantSecond}) {
			t.Fatalf("second payload message = %#v, want %#v", agent.messages[1], Message{Role: "user", Content: wantSecond})
		}
		if agent.messages[2] != (Message{Role: "user", Content: transcript.FormatStateMessage("/repo")}) {
			t.Fatalf("final state message = %#v", agent.messages[2])
		}
	})

	t.Run("stops after first failing command", func(t *testing.T) {
		execErr := fmt.Errorf("run command: exit status 1")
		executor := &fakeExecutor{
			results: []CommandResult{
				{Stdout: "one\n", ExitCode: 0},
				{Stderr: "boom\n", ExitCode: 1},
				{Stdout: "three\n", ExitCode: 0},
			},
			errs: []error{nil, execErr},
		}
		agent := NewAgent(&fakeModel{}, executor, "/repo")

		result, err := agent.ExecuteTurn(context.Background(), &ActTurn{
			Bash: bashBatch(
				BashAction{Command: "echo one"},
				BashAction{Command: "false"},
				BashAction{Command: "echo three"},
			),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !errors.Is(result.ExecErr, execErr) {
			t.Fatalf("ExecErr = %v, want %v", result.ExecErr, execErr)
		}
		if result.ExecCount != 2 {
			t.Fatalf("ExecCount = %d, want 2", result.ExecCount)
		}
		if executor.calls != 2 {
			t.Fatalf("executor calls = %d, want 2", executor.calls)
		}

		wantFirst := mustCommandPayload(t, CommandResult{Command: "echo one", Stdout: "one\n", ExitCode: 0})
		wantSecond := mustCommandPayload(t, CommandResult{Command: "false", Stderr: "boom\n", ExitCode: 1})
		if result.Output != wantFirst+"\n\n"+wantSecond {
			t.Fatalf("Output = %q, want payloads through failing command", result.Output)
		}
		if len(agent.messages) != 3 {
			t.Fatalf("expected two payload messages plus state message, got %#v", agent.messages)
		}
		if agent.messages[0] != (Message{Role: "user", Content: wantFirst}) {
			t.Fatalf("first payload message = %#v, want %#v", agent.messages[0], Message{Role: "user", Content: wantFirst})
		}
		if agent.messages[1] != (Message{Role: "user", Content: wantSecond}) {
			t.Fatalf("second payload message = %#v, want %#v", agent.messages[1], Message{Role: "user", Content: wantSecond})
		}
		if got := executor.gotCmds; len(got) != 2 || got[0] != "echo one" || got[1] != "false" {
			t.Fatalf("executed commands = %#v, want first two only", got)
		}
	})

	t.Run("cd in command one affects workdir resolution of command two", func(t *testing.T) {
		root := t.TempDir()
		next := filepath.Join(root, "subdir")
		executor := &fakeExecutor{results: []CommandResult{
			{ExitCode: 0, PostCwd: next},
			{ExitCode: 0},
		}}
		agent := NewAgent(&fakeModel{}, executor, root)

		result, err := agent.ExecuteTurn(context.Background(), &ActTurn{
			Bash: bashBatch(
				BashAction{Command: "cd subdir"},
				BashAction{Command: "pwd", Workdir: "nested"},
			),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ExecCount != 2 {
			t.Fatalf("ExecCount = %d, want 2", result.ExecCount)
		}
		if executor.calls != 2 {
			t.Fatalf("executor calls = %d, want 2", executor.calls)
		}
		if got := executor.gotDirs[0]; got != root {
			t.Fatalf("first dir = %q, want %q", got, root)
		}
		if got := executor.gotDirs[1]; got != filepath.Join(next, "nested") {
			t.Fatalf("second dir = %q, want %q", got, filepath.Join(next, "nested"))
		}
	})
}

func TestFormatCommandOutput(t *testing.T) {
	got := transcript.FormatCommandResult(transcript.CommandResult{
		Command:  "printf 'a&b\\n' > out",
		ExitCode: 0,
		Stdout:   "hello & <x> [y]\n",
		Stderr:   "warn & <x> [z]\n",
	})
	if !strings.Contains(got, "[command]\nprintf 'a&b\\n' > out\n[/command]") {
		t.Fatalf("missing escaped command block: %q", got)
	}
	if !strings.Contains(got, "[stdout]\nhello & <x> &#91;y&#93;\n\n[/stdout]") {
		t.Fatalf("missing escaped stdout block: %q", got)
	}
	if !strings.Contains(got, "[stderr]\nwarn & <x> &#91;z&#93;\n\n[/stderr]") {
		t.Fatalf("missing escaped stderr block: %q", got)
	}
}

func TestFormatCommandOutputEscapesCommandMarkers(t *testing.T) {
	got := transcript.FormatCommandResult(transcript.CommandResult{
		Command:  "echo ok\n[stdout]\nnope\n[/stdout]\n[command]\nnope\n[/command]",
		ExitCode: 0,
		Stdout:   "fine\n",
		Stderr:   "",
	})
	if strings.Count(got, "[command]") != 1 {
		t.Fatalf("expected one command section, got %q", got)
	}
	if strings.Count(got, "[stdout]") != 1 {
		t.Fatalf("expected one stdout section, got %q", got)
	}
}

func TestStateMessages(t *testing.T) {
	t.Run("Step appends cwd state before the first query", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"done"}`}},
		}}
		agent := NewAgent(model, &fakeExecutor{}, "/repo")
		agent.AppendUserMessage("inspect this repo")

		if _, err := agent.Step(context.Background()); err != nil {
			t.Fatalf("Step: %v", err)
		}

		if len(model.got) != 1 {
			t.Fatalf("expected 1 model query, got %d", len(model.got))
		}
		got := model.got[0]
		if len(got) != 2 {
			t.Fatalf("expected task plus state message, got %#v", got)
		}
		if got[1] != (Message{Role: "user", Content: transcript.FormatStateMessage("/repo")}) {
			t.Fatalf("unexpected state message: %#v", got[1])
		}
	})

	t.Run("ExecuteTurn appends updated cwd state after cwd changes", func(t *testing.T) {
		next := filepath.Join(t.TempDir(), "subdir")
		agent := NewAgent(&fakeModel{}, &fakeExecutor{results: []CommandResult{{
			Command:  "cd subdir",
			ExitCode: 0,
			PostCwd:  next,
		}}}, "/repo")
		agent.messages = append(agent.messages,
			Message{Role: "user", Content: "go to subdir"},
			Message{Role: "user", Content: transcript.FormatStateMessage("/repo")},
		)

		if _, err := agent.ExecuteTurn(context.Background(), &ActTurn{Bash: bashBatch(BashAction{Command: "cd subdir"})}); err != nil {
			t.Fatalf("ExecuteTurn: %v", err)
		}

		msgs := agent.Messages()
		if len(msgs) < 4 {
			t.Fatalf("expected command result and updated state message, got %#v", msgs)
		}
		if msgs[len(msgs)-1] != (Message{Role: "user", Content: transcript.FormatStateMessage(next)}) {
			t.Fatalf("unexpected final state message: %#v", msgs[len(msgs)-1])
		}
	})
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

func TestAgentPersistsShellStateBetweenCommands(t *testing.T) {
	t.Run("persists export to future commands", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: actJSON(`export NANOCHAT_BASE_DIR=/tmp/runtime && echo ok`)}},
			{Message: Message{Role: "assistant", Content: actJSON(`printf %s "$NANOCHAT_BASE_DIR"`)}},
		}}
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: "ok\n", ExitCode: 0, PostEnv: map[string]string{"NANOCHAT_BASE_DIR": "/tmp/runtime"}},
			{Stdout: "/tmp/runtime", ExitCode: 0},
		}}

		agent := NewAgent(model, executor, "/tmp")
		agent.AppendUserMessage("set env then use it")

		runActStep(t, agent)
		runActStep(t, agent)
		if len(executor.gotEnv) < 2 {
			t.Fatalf("expected env snapshots for both commands, got %d", len(executor.gotEnv))
		}
		if got := executor.gotEnv[1]["NANOCHAT_BASE_DIR"]; got != "/tmp/runtime" {
			t.Fatalf("got env %q, want %q", got, "/tmp/runtime")
		}
	})

	t.Run("preserves env snapshots across stateful commands and unsets", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: actJSON("export CLNKR_CHAIN_ONE=one")}},
			{Message: Message{Role: "assistant", Content: actJSON("export CLNKR_CHAIN_TWO=two")}},
			{Message: Message{Role: "assistant", Content: actJSON("unset CLNKR_CHAIN_ONE")}},
			{Message: Message{Role: "assistant", Content: actJSON(`printf %s "$CLNKR_CHAIN_TWO"`)}},
		}}
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: "ok\n", ExitCode: 0, PostEnv: map[string]string{"CLNKR_CHAIN_ONE": "one"}},
			{Stdout: "ok\n", ExitCode: 0, PostEnv: map[string]string{"CLNKR_CHAIN_ONE": "one", "CLNKR_CHAIN_TWO": "two"}},
			{Stdout: "ok\n", ExitCode: 0, PostEnv: map[string]string{"CLNKR_CHAIN_TWO": "two"}},
			{Stdout: "two", ExitCode: 0},
		}}

		agent := NewAgent(model, executor, "/tmp")
		agent.AppendUserMessage("persist env across commands")

		for i := 0; i < 4; i++ {
			runActStep(t, agent)
		}

		if len(executor.gotEnv) < 4 {
			t.Fatalf("expected env snapshots for each command, got %d", len(executor.gotEnv))
		}
		if got := executor.gotEnv[1]["CLNKR_CHAIN_ONE"]; got != "one" {
			t.Fatalf("step 2 env missing chain one: %q", got)
		}
		if executor.gotEnv[2]["CLNKR_CHAIN_ONE"] != "one" || executor.gotEnv[2]["CLNKR_CHAIN_TWO"] != "two" {
			t.Fatalf("step 3 env missing expected vars: %+v", executor.gotEnv[2])
		}
		if _, ok := executor.gotEnv[3]["CLNKR_CHAIN_ONE"]; ok {
			t.Fatalf("step 4 env should not include unset var: %+v", executor.gotEnv[3])
		}
		if executor.gotEnv[3]["CLNKR_CHAIN_TWO"] != "two" {
			t.Fatalf("step 4 env should retain other vars: %+v", executor.gotEnv[3])
		}
	})

	t.Run("persists cd across chained commands", func(t *testing.T) {
		root := t.TempDir()
		next := filepath.Join(root, "subdir")
		if err := os.Mkdir(next, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: actJSON(fmt.Sprintf("cd %s && pwd", next))}},
			{Message: Message{Role: "assistant", Content: actJSON("pwd")}},
		}}
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: next + "\n", ExitCode: 0, PostCwd: next},
			{Stdout: next + "\n", ExitCode: 0},
		}}

		agent := NewAgent(model, executor, root)
		agent.AppendUserMessage("change dir then reuse it")

		runActStep(t, agent)
		runActStep(t, agent)
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

	t.Run("restores cwd from the latest state block", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: actJSON("pwd")}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "/restored\n", ExitCode: 0}}}
		agent := NewAgent(model, executor, "/wrong")

		if err := agent.AddMessages([]Message{
			{Role: "user", Content: transcript.FormatStateMessage("/old")},
			{Role: "assistant", Content: `{"type":"done","summary":"previous turn"}`},
			{Role: "user", Content: transcript.FormatStateMessage("/restored")},
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		agent.AppendUserMessage("continue")
		runActStep(t, agent)

		if len(executor.gotDirs) != 1 {
			t.Fatalf("expected one execute call, got %d", len(executor.gotDirs))
		}
		if executor.gotDirs[0] != "/restored" {
			t.Fatalf("got dir %q, want %q", executor.gotDirs[0], "/restored")
		}
	})

	t.Run("ignores non-clnkr state blocks", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: actJSON("pwd")}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "/wrong\n", ExitCode: 0}}}
		agent := NewAgent(model, executor, "/original")

		if err := agent.AddMessages([]Message{
			{Role: "user", Content: "[state]\n{\"source\":\"user\",\"kind\":\"state\",\"cwd\":\"/wrong\"}\n[/state]"},
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		agent.AppendUserMessage("continue")
		runActStep(t, agent)

		if len(executor.gotDirs) != 1 {
			t.Fatalf("expected one execute call, got %d", len(executor.gotDirs))
		}
		if executor.gotDirs[0] != "/original" {
			t.Fatalf("got dir %q, want %q", executor.gotDirs[0], "/original")
		}
	})

	t.Run("returns error after Step", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"All done."}`}},
		}}
		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.AppendUserMessage("hi")

		if _, err := agent.Step(context.Background()); err != nil {
			t.Fatalf("unexpected Step error: %v", err)
		}

		err := agent.AddMessages([]Message{{Role: "user", Content: "late"}})
		if err == nil {
			t.Fatal("expected error calling AddMessages after Step")
		}
	})
}

func TestAgentCompactRewritesTranscript(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: transcript.FormatStateMessage("/tmp/first")},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: "third task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
	}
	compactor := &fakeCompactor{summary: "  summarized older work  \n"}
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/original")
	agent.messages = append([]Message{}, messages...)

	stats, err := agent.Compact(context.Background(), compactor, CompactOptions{
		Instructions:    "focus on tests",
		KeepRecentTurns: 2,
	})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(compactor.got) != 1 {
		t.Fatalf("compactor got %d calls, want 1", len(compactor.got))
	}
	wantPrefix := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: transcript.FormatStateMessage("/tmp/first")},
	}
	if len(compactor.got[0]) != len(wantPrefix) {
		t.Fatalf("compactor prefix length = %d, want %d", len(compactor.got[0]), len(wantPrefix))
	}
	for i := range wantPrefix {
		if compactor.got[0][i] != wantPrefix[i] {
			t.Fatalf("compactor prefix message %d = %#v, want %#v", i, compactor.got[0][i], wantPrefix[i])
		}
	}
	wantMessages := []Message{
		{Role: "user", Content: transcript.FormatCompactMessage("summarized older work", "focus on tests")},
		{Role: "user", Content: transcript.FormatStateMessage("/tmp/first")},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: "third task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
	}
	got := agent.Messages()
	if len(got) != len(wantMessages) {
		t.Fatalf("got %d messages, want %d", len(got), len(wantMessages))
	}
	for i := range wantMessages {
		if got[i] != wantMessages[i] {
			t.Fatalf("message %d = %#v, want %#v", i, got[i], wantMessages[i])
		}
	}
	if stats != (CompactStats{CompactedMessages: 3, KeptMessages: 5}) {
		t.Fatalf("stats = %#v, want %#v", stats, CompactStats{CompactedMessages: 3, KeptMessages: 5})
	}
}

func TestAgentCompactPreservesLatestState(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: transcript.FormatStateMessage("/tmp/old")},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: transcript.FormatStateMessage("/tmp/latest")},
		{Role: "user", Content: "third task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
	}
	compactor := &fakeCompactor{summary: "summary"}
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/original")
	agent.messages = append([]Message{}, messages...)
	agent.env = map[string]string{"KEEP": "me"}
	agent.started = true

	stats, err := agent.Compact(context.Background(), compactor, CompactOptions{KeepRecentTurns: 1})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(compactor.got) != 1 {
		t.Fatalf("compactor got %d calls, want 1", len(compactor.got))
	}
	wantPrefix := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: transcript.FormatStateMessage("/tmp/old")},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: transcript.FormatStateMessage("/tmp/latest")},
	}
	if len(compactor.got[0]) != len(wantPrefix) {
		t.Fatalf("compactor prefix length = %d, want %d", len(compactor.got[0]), len(wantPrefix))
	}
	for i := range wantPrefix {
		if compactor.got[0][i] != wantPrefix[i] {
			t.Fatalf("compactor prefix message %d = %#v, want %#v", i, compactor.got[0][i], wantPrefix[i])
		}
	}
	if stats != (CompactStats{CompactedMessages: 6, KeptMessages: 3}) {
		t.Fatalf("stats = %#v, want %#v", stats, CompactStats{CompactedMessages: 6, KeptMessages: 3})
	}
	if agent.cwd != "/tmp/latest" {
		t.Fatalf("cwd = %q, want %q", agent.cwd, "/tmp/latest")
	}
	if got := agent.env["KEEP"]; got != "me" {
		t.Fatalf("env changed: got %q", got)
	}
	if !agent.started {
		t.Fatal("started changed")
	}
}

func TestAgentCompactLeavesMessagesUntouchedOnSummarizerError(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: "third task"},
	}
	compactor := &fakeCompactor{err: errors.New("summarizer failed")}
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/original")
	agent.messages = append([]Message{}, messages...)

	stats, err := agent.Compact(context.Background(), compactor, CompactOptions{KeepRecentTurns: 2})
	if err == nil {
		t.Fatal("expected Compact error")
	}
	if !strings.Contains(err.Error(), "summarizer failed") {
		t.Fatalf("error = %v, want summarizer error", err)
	}
	if stats != (CompactStats{}) {
		t.Fatalf("stats = %#v, want zero value", stats)
	}
	got := agent.Messages()
	if len(got) != len(messages) {
		t.Fatalf("messages changed on error: got %d, want %d", len(got), len(messages))
	}
	for i := range messages {
		if got[i] != messages[i] {
			t.Fatalf("message %d = %#v, want %#v", i, got[i], messages[i])
		}
	}
}

func TestAgentCompactLeavesMessagesUntouchedOnEmptySummary(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: "third task"},
	}
	compactor := &fakeCompactor{summary: "  \n\t "}
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/original")
	agent.messages = append([]Message{}, messages...)

	stats, err := agent.Compact(context.Background(), compactor, CompactOptions{KeepRecentTurns: 2})
	if err == nil {
		t.Fatal("expected Compact error")
	}
	if !strings.Contains(err.Error(), "empty summary") {
		t.Fatalf("error = %v, want empty summary error", err)
	}
	if stats != (CompactStats{}) {
		t.Fatalf("stats = %#v, want zero value", stats)
	}
	got := agent.Messages()
	if len(got) != len(messages) {
		t.Fatalf("messages changed on empty summary: got %d, want %d", len(got), len(messages))
	}
	for i := range messages {
		if got[i] != messages[i] {
			t.Fatalf("message %d = %#v, want %#v", i, got[i], messages[i])
		}
	}
}

func TestAgentCompactDefaultsKeepRecentTurnsToTwo(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: "third task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
	}
	compactor := &fakeCompactor{summary: "summary"}
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/original")
	agent.messages = append([]Message{}, messages...)

	stats, err := agent.Compact(context.Background(), compactor, CompactOptions{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(compactor.got) != 1 {
		t.Fatalf("compactor got %d calls, want 1", len(compactor.got))
	}
	wantPrefix := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
	}
	if len(compactor.got[0]) != len(wantPrefix) {
		t.Fatalf("compactor prefix length = %d, want %d", len(compactor.got[0]), len(wantPrefix))
	}
	for i := range wantPrefix {
		if compactor.got[0][i] != wantPrefix[i] {
			t.Fatalf("compactor prefix message %d = %#v, want %#v", i, compactor.got[0][i], wantPrefix[i])
		}
	}
	if stats != (CompactStats{CompactedMessages: 2, KeptMessages: 4}) {
		t.Fatalf("stats = %#v, want %#v", stats, CompactStats{CompactedMessages: 2, KeptMessages: 4})
	}
}

func TestAgentCompactRejectsNegativeKeepRecentTurns(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: "third task"},
	}
	compactor := &fakeCompactor{summary: "summary"}
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/original")
	agent.messages = append([]Message{}, messages...)

	stats, err := agent.Compact(context.Background(), compactor, CompactOptions{KeepRecentTurns: -1})
	if err == nil {
		t.Fatal("expected Compact error")
	}
	if !strings.Contains(err.Error(), "invalid keep recent turns") {
		t.Fatalf("error = %v, want invalid-input error", err)
	}
	if len(compactor.got) != 0 {
		t.Fatalf("compactor should not be called on invalid input, got %d calls", len(compactor.got))
	}
	if stats != (CompactStats{}) {
		t.Fatalf("stats = %#v, want zero value", stats)
	}
	got := agent.Messages()
	if len(got) != len(messages) {
		t.Fatalf("messages changed on invalid input: got %d, want %d", len(got), len(messages))
	}
	for i := range messages {
		if got[i] != messages[i] {
			t.Fatalf("message %d = %#v, want %#v", i, got[i], messages[i])
		}
	}
}

func TestAgentCompactRejectsNilCompactor(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: "third task"},
	}
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/original")
	agent.messages = append([]Message{}, messages...)

	stats, err := agent.Compact(context.Background(), nil, CompactOptions{KeepRecentTurns: 2})
	if err == nil {
		t.Fatal("expected Compact error")
	}
	if !strings.Contains(err.Error(), "no compactor configured") {
		t.Fatalf("error = %v, want nil-compactor error", err)
	}
	if stats != (CompactStats{}) {
		t.Fatalf("stats = %#v, want zero value", stats)
	}
	got := agent.Messages()
	if len(got) != len(messages) {
		t.Fatalf("messages changed on nil compactor: got %d, want %d", len(got), len(messages))
	}
	for i := range messages {
		if got[i] != messages[i] {
			t.Fatalf("message %d = %#v, want %#v", i, got[i], messages[i])
		}
	}
}

func TestAppendUserMessage(t *testing.T) {
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")

	agent.AppendUserMessage("hello")

	msgs := agent.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0] != (Message{Role: "user", Content: "hello"}) {
		t.Fatalf("got %#v", msgs[0])
	}
}

func TestRequestStepLimitSummary(t *testing.T) {
	t.Run("treats response protocol errors as invalid final turns", func(t *testing.T) {
		raw := `{"type":"done","summary":"ignored schema"}`
		model := &fakeModel{responses: []Response{
			{
				Message:     Message{Role: "assistant", Content: raw},
				ProtocolErr: fmt.Errorf("%w: missing required structured output field %q", ErrInvalidJSON, "turn"),
			},
		}}
		notify, events := collectEvents()

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.Notify = notify
		agent.messages = append(agent.messages, Message{Role: "user", Content: "do it"})

		if err := agent.RequestStepLimitSummary(context.Background()); err != nil {
			t.Fatalf("RequestStepLimitSummary: %v", err)
		}
		if got := agent.messages[len(agent.messages)-1]; got != (Message{Role: "assistant", Content: raw}) {
			t.Fatalf("assistant message = %#v, want raw payload", got)
		}

		found := false
		sawResponse := false
		sawProtocolFailure := false
		for _, e := range *events {
			switch ev := e.(type) {
			case EventDebug:
				if strings.Contains(ev.Message, "final response not a valid turn") {
					found = true
				}
			case EventResponse:
				sawResponse = true
			case EventProtocolFailure:
				sawProtocolFailure = true
			}
		}
		if !found {
			t.Fatal("expected final invalid-turn debug message")
		}
		if !sawProtocolFailure {
			t.Fatal("expected EventProtocolFailure")
		}
		if sawResponse {
			t.Fatal("unexpected EventResponse for final response protocol error")
		}
	})
}

func TestAgent(t *testing.T) {
	t.Run("single step then exit", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: actJSON("ls")}},
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
		if len(msgs) != 3 {
			t.Fatalf("expected task, state, and assistant messages, got %d", len(msgs))
		}
		if msgs[1] != (Message{Role: "user", Content: transcript.FormatStateMessage("/tmp")}) {
			t.Fatalf("unexpected state message: %#v", msgs[1])
		}
		if msgs[2].Role != "assistant" {
			t.Fatalf("unexpected message role: %q", msgs[2].Role)
		}
	})

	t.Run("respects max steps", func(t *testing.T) {
		responses := make([]Response, 12)
		for i := 0; i < 10; i++ {
			responses[i] = Response{Message: Message{Role: "assistant", Content: actJSON("echo step")}}
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
			{Message: Message{Role: "assistant", Content: actJSON(fmt.Sprintf("cd %s", subDir))}},
			{Message: Message{Role: "assistant", Content: actJSON("ls")}},
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"Done."}`}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{ExitCode: 0, PostCwd: subDir}, {Stdout: "files", ExitCode: 0}}}

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
			{Message: Message{Role: "assistant", Content: actJSON(fmt.Sprintf("cd %s", nonexistent))}},
			{Message: Message{Role: "assistant", Content: actJSON("ls")}},
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

	t.Run("response protocol error then recovery", func(t *testing.T) {
		raw := `{"type":"done","summary":"ignored schema"}`
		model := &fakeModel{responses: []Response{
			{
				Message:     Message{Role: "assistant", Content: raw},
				ProtocolErr: fmt.Errorf("%w: missing required structured output field %q", ErrInvalidJSON, "turn"),
			},
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"Done."}`}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		err := agent.Run(context.Background(), "do something")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		lastMsgs := model.got[len(model.got)-1]
		found := false
		for _, msg := range lastMsgs {
			if strings.Contains(msg.Content, "[protocol_error]") {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("protocol correction message should be in conversation")
		}
	})

	t.Run("wrapped multi-object response protocol error then recovery", func(t *testing.T) {
		raw := `{"turn":{"type":"act","bash":{"command":"pwd","workdir":null},"question":null,"summary":null,"reasoning":null}}` +
			`{"turn":{"type":"done","bash":null,"question":null,"summary":"Done.","reasoning":null}}`
		model := &fakeModel{responses: []Response{
			{
				Message:     Message{Role: "assistant", Content: raw},
				ProtocolErr: fmt.Errorf("%w: unexpected trailing JSON value", ErrInvalidJSON),
			},
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"Done."}`}},
		}}
		executor := &fakeExecutor{}

		agent := NewAgent(model, executor, "/tmp")
		err := agent.Run(context.Background(), "do something")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.calls != 0 {
			t.Fatalf("executor calls = %d, want 0", executor.calls)
		}
		if len(model.got) != 2 {
			t.Fatalf("model calls = %d, want 2", len(model.got))
		}
		secondQuery := model.got[1]
		foundRaw := false
		foundCorrection := false
		for _, msg := range secondQuery {
			if msg.Role == "assistant" && msg.Content == raw {
				foundRaw = true
			}
			if msg.Role == "user" && strings.Contains(msg.Content, "[protocol_error]") {
				foundCorrection = true
				if !strings.Contains(msg.Content, "Do not emit multiple JSON objects in one response.") {
					t.Fatalf("protocol correction message = %q, want multiple-object guidance", msg.Content)
				}
			}
		}
		if !foundRaw {
			t.Fatal("second query should include the rejected wrapped multi-object assistant reply")
		}
		if !foundCorrection {
			t.Fatal("second query should include a protocol_error correction message")
		}
		if got := agent.messages[len(agent.messages)-1].Content; got != `{"type":"done","summary":"Done."}` {
			t.Fatalf("final assistant message = %q, want canonical done turn", got)
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

	t.Run("exits on consecutive response protocol errors", func(t *testing.T) {
		raw := `{"type":"done","summary":"ignored schema"}`
		model := &fakeModel{responses: []Response{
			{
				Message:     Message{Role: "assistant", Content: raw},
				ProtocolErr: fmt.Errorf("%w: missing required structured output field %q", ErrInvalidJSON, "turn"),
			},
			{
				Message:     Message{Role: "assistant", Content: raw},
				ProtocolErr: fmt.Errorf("%w: missing required structured output field %q", ErrInvalidJSON, "turn"),
			},
			{
				Message:     Message{Role: "assistant", Content: raw},
				ProtocolErr: fmt.Errorf("%w: missing required structured output field %q", ErrInvalidJSON, "turn"),
			},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		err := agent.Run(context.Background(), "do something")
		if err == nil {
			t.Fatal("expected error on consecutive protocol failures")
		}
		if !strings.Contains(err.Error(), "protocol") {
			t.Fatalf("error should mention protocol, got: %v", err)
		}
	})

	t.Run("emits events for output", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: actJSON("echo hi")}, Usage: Usage{InputTokens: 10, OutputTokens: 5}},
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
			{Message: Message{Role: "assistant", Content: actJSON("cd /tmp && ls")}},
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
			{Message: Message{Role: "assistant", Content: actJSON("cd ~")}},
			{Message: Message{Role: "assistant", Content: actJSON("ls")}},
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"Done."}`}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{ExitCode: 0, PostCwd: home}, {Stdout: "files", ExitCode: 0}}}

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
			{Message: Message{Role: "assistant", Content: actJSON("false")}},
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

func TestRun(t *testing.T) {
	t.Run("counts executed commands toward max steps", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: actJSON("echo one", "echo two")}},
			{Message: Message{Role: "assistant", Content: `{"type":"done","summary":"Step limit reached."}`}},
		}}
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: "one\n", ExitCode: 0},
			{Stdout: "two\n", ExitCode: 0},
		}}

		agent := NewAgent(model, executor, "/tmp")
		agent.MaxSteps = 2
		err := agent.Run(context.Background(), "do stuff")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.calls != 2 {
			t.Fatalf("executor calls = %d, want 2", executor.calls)
		}
		if model.calls != 2 {
			t.Fatalf("model calls = %d, want 2", model.calls)
		}
	})
}
