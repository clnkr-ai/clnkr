package hew

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
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
	outputs []string
	calls   int
	gotCmds []string
	gotDirs []string
}

func (e *fakeExecutor) Execute(_ context.Context, command string, dir string) (string, error) {
	e.gotCmds = append(e.gotCmds, command)
	e.gotDirs = append(e.gotDirs, dir)
	if e.calls >= len(e.outputs) {
		return "", fmt.Errorf("no more outputs")
	}
	out := e.outputs[e.calls]
	e.calls++
	return out, nil
}

func TestAgent(t *testing.T) {
	t.Run("single step then exit", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "Let me check\n\n```bash\nls\n```"}},
			{Message: Message{Role: "assistant", Content: "Done!\n\n```bash\nexit\n```"}},
		}}
		executor := &fakeExecutor{outputs: []string{"file1.go\n"}}
		var buf bytes.Buffer

		agent := NewAgent(model, executor, "/tmp", &buf)
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

	t.Run("respects max steps", func(t *testing.T) {
		responses := make([]Response, 12)
		outputs := make([]string, 10)
		for i := 0; i < 10; i++ {
			responses[i] = Response{Message: Message{Role: "assistant", Content: "```bash\necho step\n```"}}
			outputs[i] = "step"
		}
		responses[10] = Response{Message: Message{Role: "assistant", Content: "summary"}}

		model := &fakeModel{responses: responses}
		executor := &fakeExecutor{outputs: outputs}
		var buf bytes.Buffer

		agent := NewAgent(model, executor, "/tmp", &buf)
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
			{Message: Message{Role: "assistant", Content: "```bash\nexit\n```"}},
		}}
		var buf bytes.Buffer

		agent := NewAgent(model, &fakeExecutor{}, "/tmp", &buf)
		if agent.MaxSteps != 100 {
			t.Errorf("default MaxSteps should be 100, got %d", agent.MaxSteps)
		}
	})

	t.Run("tracks cwd on cd", func(t *testing.T) {
		// Use a real directory that exists
		realDir := t.TempDir()
		subDir := filepath.Join(realDir, "sub")
		if err := exec.Command("mkdir", "-p", subDir).Run(); err != nil {
			t.Fatalf("failed to create subdir: %v", err)
		}

		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "```bash\ncd " + subDir + "\n```"}},
			{Message: Message{Role: "assistant", Content: "```bash\nls\n```"}},
			{Message: Message{Role: "assistant", Content: "```bash\nexit\n```"}},
		}}
		executor := &fakeExecutor{outputs: []string{"", "files"}}
		var buf bytes.Buffer

		agent := NewAgent(model, executor, realDir, &buf)
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
			{Message: Message{Role: "assistant", Content: "```bash\nexit\n```"}},
		}}
		executor := &fakeExecutor{outputs: []string{"", "files"}}
		var buf bytes.Buffer

		agent := NewAgent(model, executor, startDir, &buf)
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
			{Message: Message{Role: "assistant", Content: "no code block"}},
			{Message: Message{Role: "assistant", Content: "```bash\nexit\n```"}},
		}}
		var buf bytes.Buffer

		agent := NewAgent(model, &fakeExecutor{}, "/tmp", &buf)
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
			{Message: Message{Role: "assistant", Content: "no block"}},
			{Message: Message{Role: "assistant", Content: "still no block"}},
		}}
		var buf bytes.Buffer

		agent := NewAgent(model, &fakeExecutor{}, "/tmp", &buf)
		err := agent.Run(context.Background(), "do something")
		if err == nil {
			t.Error("expected error on consecutive format failures")
		}
	})

	t.Run("writes output to configured writer", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Message: Message{Role: "assistant", Content: "```bash\nexit\n```"}},
		}}
		var buf bytes.Buffer

		agent := NewAgent(model, &fakeExecutor{}, "/tmp", &buf)
		agent.Run(context.Background(), "do something")
		if buf.Len() == 0 {
			t.Error("expected output written to buffer")
		}
	})

	t.Run("returns error on context cancellation", func(t *testing.T) {
		model := &fakeModel{responses: []Response{}}
		var buf bytes.Buffer
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		agent := NewAgent(model, &fakeExecutor{}, "/tmp", &buf)
		err := agent.Run(ctx, "do something")
		if err == nil {
			t.Error("expected error on cancelled context")
		}
	})
}
