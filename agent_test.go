package hew

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
			{Message: Message{Role: "assistant", Content: "```bash\nls\n```"}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "file1.go\n", ExitCode: 0}}}

		agent := NewAgent(model, executor, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "list files"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Action != "ls" {
			t.Errorf("got action %q, want %q", result.Action, "ls")
		}
		wantOutput := mustCommandPayload(t, CommandResult{Command: "ls", Stdout: "file1.go\n", ExitCode: 0})
		if result.Output != wantOutput {
			t.Errorf("got output %q, want %q", result.Output, wantOutput)
		}
		if result.ExecErr != nil {
			t.Errorf("unexpected exec error: %v", result.ExecErr)
		}
	})

	t.Run("returns done action", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "All done.\n\n<done/>"}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "done"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Action != DoneSignal {
			t.Errorf("got action %q, want %q", result.Action, DoneSignal)
		}
		if result.Output != "" {
			t.Errorf("expected empty output for done, got %q", result.Output)
		}
	})

	t.Run("returns clarify action on plain text question", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "What directory should I inspect?"}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "do something"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Action != ClarifySignal {
			t.Errorf("expected clarify action, got %q", result.Action)
		}
		if len(agent.messages) != 2 {
			t.Fatalf("expected no reminder message, got %d messages", len(agent.messages))
		}
	})

	t.Run("returns empty action on malformed bash response", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "I'll act now.\n\n```bash\n```"}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "do something"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Action != "" {
			t.Errorf("expected empty action on format error, got %q", result.Action)
		}
		last := agent.messages[len(agent.messages)-1]
		if !strings.Contains(last.Content, "bash") {
			t.Error("format error reminder should mention bash")
		}
	})

	t.Run("post-command no-op guard triggers after prior command output", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "I need more information before I can continue."}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = append(agent.messages,
			Message{Role: "user", Content: "debug this"},
			Message{Role: "assistant", Content: "```bash\nfalse\n```"},
			Message{Role: "user", Content: "[command]\nfalse\n[/command]\n[exit_code]\n1\n[/exit_code]\n[stdout]\n\n[/stdout]\n[stderr]\nnope\n[/stderr]"},
		)

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Action != "" {
			t.Errorf("expected empty action on post-command no-op guard, got %q", result.Action)
		}
		last := agent.messages[len(agent.messages)-1]
		if !strings.Contains(last.Content, "After commands have run, do not ask the user") {
			t.Errorf("expected post-command reminder, got %q", last.Content)
		}
		if !strings.Contains(last.Content, "[stdout]") {
			t.Errorf("expected reminder to reference prior command results, got %q", last.Content)
		}
	})

	t.Run("post-command no-op guard does not block initial clarification", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "Please paste the exact error message."}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "help"})

		_, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		last := agent.messages[len(agent.messages)-1]
		if strings.Contains(last.Content, "After commands have run, do not ask the user") {
			t.Errorf("did not expect post-command reminder before any command output, got %q", last.Content)
		}
	})

	t.Run("emits events via Notify", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "```bash\necho hi\n```"}, Usage: Usage{InputTokens: 10, OutputTokens: 5}},
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
			case EventFormatError:
				types = append(types, "fmt_error")
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
			{Message: Message{Role: "assistant", Content: "All done.\n\n<done/>"}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		// Notify is nil by default
		agent.messages = append(agent.messages, Message{Role: "user", Content: "done"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Action != DoneSignal {
			t.Errorf("got action %q, want %q", result.Action, DoneSignal)
		}
	})

	t.Run("empty command output never produces empty messages", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "```bash\ntouch /tmp/test\n```"}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{ExitCode: 0}}}

		agent := NewAgent(model, executor, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "create file"})

		_, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify that the message was appended but is not empty
		lastMsg := agent.messages[len(agent.messages)-1]
		if lastMsg.Role != "user" {
			t.Errorf("last message role should be user, got %q", lastMsg.Role)
		}
		if lastMsg.Content == "" {
			t.Error("message content must not be empty (violates Anthropic API)")
		}
		want := mustCommandPayload(t, CommandResult{Command: "touch /tmp/test", ExitCode: 0})
		if lastMsg.Content != want {
			t.Errorf("empty output should still use command payload, got %q", lastMsg.Content)
		}
	})
}

func TestStepMultiBlock(t *testing.T) {
	t.Run("executes all bash blocks sequentially", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "Step 1:\n\n```bash\necho hello\n```\n\nStep 2:\n\n```bash\necho world\n```"}},
		}}
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: "hello\n", ExitCode: 0},
			{Stdout: "world\n", ExitCode: 0},
		}}

		agent := NewAgent(model, executor, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "greet"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.calls != 2 {
			t.Errorf("expected 2 commands, got %d", executor.calls)
		}
		if executor.gotCmds[0] != "echo hello" {
			t.Errorf("first command: got %q, want %q", executor.gotCmds[0], "echo hello")
		}
		if executor.gotCmds[1] != "echo world" {
			t.Errorf("second command: got %q, want %q", executor.gotCmds[1], "echo world")
		}
		if result.Action != "echo hello" {
			t.Errorf("Action should be first command, got %q", result.Action)
		}
		wantOutput := mustCommandPayload(t, CommandResult{Command: "echo hello", Stdout: "hello\n", ExitCode: 0}) + "\n" +
			mustCommandPayload(t, CommandResult{Command: "echo world", Stdout: "world\n", ExitCode: 0})
		if result.Output != wantOutput {
			t.Errorf("combined output: got %q, want %q", result.Output, wantOutput)
		}
	})

	t.Run("continues after command error", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "```bash\nfalse\n```\n\n```bash\necho cleanup\n```"}},
		}}
		executor := &fakeExecutor{
			results: []CommandResult{
				{ExitCode: 1},
				{Stdout: "cleaned\n", ExitCode: 0},
			},
			errs: []error{
				fmt.Errorf("run command: exit status 1"),
				nil,
			},
		}

		agent := NewAgent(model, executor, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "do it"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.calls != 2 {
			t.Errorf("expected 2 commands even with error, got %d", executor.calls)
		}
		// ExecErr should be nil because the second command succeeded (fakeExecutor returns nil error for valid outputs)
		if result.ExecErr != nil {
			t.Errorf("expected nil ExecErr from last command, got %v", result.ExecErr)
		}
	})

	t.Run("emits events for each command", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "```bash\necho a\n```\n\n```bash\necho b\n```"}},
		}}
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: "a\n", ExitCode: 0},
			{Stdout: "b\n", ExitCode: 0},
		}}
		notify, events := collectEvents()

		agent := NewAgent(model, executor, "/tmp")
		agent.Notify = notify
		agent.messages = append(agent.messages, Message{Role: "user", Content: "two commands"})

		_, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		cmdStarts := 0
		cmdDones := 0
		for _, e := range *events {
			switch e.(type) {
			case EventCommandStart:
				cmdStarts++
			case EventCommandDone:
				cmdDones++
			}
		}
		if cmdStarts != 2 {
			t.Errorf("expected 2 EventCommandStart, got %d", cmdStarts)
		}
		if cmdDones != 2 {
			t.Errorf("expected 2 EventCommandDone, got %d", cmdDones)
		}
	})

	t.Run("done signal after commands", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "```bash\necho final\n```\n\nAll done.\n\n<done/>"}},
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "final\n", ExitCode: 0}}}

		agent := NewAgent(model, executor, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "finish"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.calls != 1 {
			t.Errorf("expected 1 command executed before done, got %d", executor.calls)
		}
		if result.Action != DoneSignal {
			t.Errorf("expected DoneSignal action, got %q", result.Action)
		}
		want := mustCommandPayload(t, CommandResult{Command: "echo final", Stdout: "final\n", ExitCode: 0})
		if result.Output != want {
			t.Errorf("expected output %q, got %q", want, result.Output)
		}
	})

	t.Run("combined output appended as single message", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "```bash\necho a\n```\n\n```bash\necho b\n```"}},
		}}
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: "a\n", ExitCode: 0},
			{Stdout: "b\n", ExitCode: 0},
		}}

		agent := NewAgent(model, executor, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "two"})

		_, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Messages: initial user, assistant response, combined output
		if len(agent.messages) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(agent.messages))
		}
		last := agent.messages[2]
		if last.Role != "user" {
			t.Errorf("last message role should be user, got %q", last.Role)
		}
		want := mustCommandPayload(t, CommandResult{Command: "echo a", Stdout: "a\n", ExitCode: 0}) + "\n" +
			mustCommandPayload(t, CommandResult{Command: "echo b", Stdout: "b\n", ExitCode: 0})
		if last.Content != want {
			t.Errorf("last message content: got %q, want %q", last.Content, want)
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
	if !strings.Contains(payload, `&lt;tag&gt;`) || !strings.Contains(payload, `&amp;`) {
		t.Errorf("payload should escape special characters, got %q", payload)
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
			{Message: Message{Role: "assistant", Content: "All done.\n\n<done/>"}},
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
			{Message: Message{Role: "assistant", Content: "Let me check\n\n```bash\nls\n```"}},
			{Message: Message{Role: "assistant", Content: "Done!\n\nAll done.\n\n<done/>"}},
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
			{Message: Message{Role: "assistant", Content: "Which repo should I inspect?"}},
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
		if msgs[1].Role != "assistant" || msgs[1].Content != "Which repo should I inspect?" {
			t.Fatalf("unexpected assistant clarification message: %#v", msgs[1])
		}
	})

	t.Run("respects max steps", func(t *testing.T) {
		responses := make([]Response, 12)
		outputs := make([]string, 10)
		for i := 0; i < 10; i++ {
			responses[i] = Response{Message: Message{Role: "assistant", Content: "```bash\necho step\n```"}}
			outputs[i] = "step"
		}
		responses[10] = Response{Message: Message{Role: "assistant", Content: "summary"}}

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
			{Message: Message{Role: "assistant", Content: "All done.\n\n<done/>"}},
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
			{Message: Message{Role: "assistant", Content: "```bash\ncd " + subDir + "\n```"}},
			{Message: Message{Role: "assistant", Content: "```bash\nls\n```"}},
			{Message: Message{Role: "assistant", Content: "All done.\n\n<done/>"}},
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
			{Message: Message{Role: "assistant", Content: "```bash\ncd " + nonexistent + "\n```"}},
			{Message: Message{Role: "assistant", Content: "```bash\nls\n```"}},
			{Message: Message{Role: "assistant", Content: "All done.\n\n<done/>"}},
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

	t.Run("format error then recovery", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "```bash\n```"}},
			{Message: Message{Role: "assistant", Content: "All done.\n\n<done/>"}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		err := agent.Run(context.Background(), "do something")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		lastMsgs := model.got[len(model.got)-1]
		lastMsg := lastMsgs[len(lastMsgs)-1]
		if !strings.Contains(lastMsg.Content, "bash") {
			t.Error("format error should mention bash code blocks")
		}
	})

	t.Run("exits on consecutive format errors", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "```bash\n```"}},
			{Message: Message{Role: "assistant", Content: "```bash\n```"}},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		err := agent.Run(context.Background(), "do something")
		if err == nil {
			t.Error("expected error on consecutive format failures")
		}
	})

	t.Run("emits events for output", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "```bash\necho hi\n```"}, Usage: Usage{InputTokens: 10, OutputTokens: 5}},
			{Message: Message{Role: "assistant", Content: "All done.\n\n<done/>"}},
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
			{Message: Message{Role: "assistant", Content: "```bash\ncd /tmp && ls\n```"}},
			{Message: Message{Role: "assistant", Content: "All done.\n\n<done/>"}},
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
			{Message: Message{Role: "assistant", Content: "```bash\ncd ~\n```"}},
			{Message: Message{Role: "assistant", Content: "```bash\nls\n```"}},
			{Message: Message{Role: "assistant", Content: "All done.\n\n<done/>"}},
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
			{Message: Message{Role: "assistant", Content: "```bash\nfalse\n```"}},
			{Message: Message{Role: "assistant", Content: "All done.\n\n<done/>"}},
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
