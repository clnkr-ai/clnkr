package clnkr

import (
	"context"
	"strings"
	"testing"
)

func TestRunBoundsCommandObservationButKeepsRawCommandEvent(t *testing.T) {
	hugeStdout := "stdout-head-" + strings.Repeat("o", 128*1024) + "-stdout-tail"
	hugeStderr := "stderr-head-" + strings.Repeat("e", 128*1024) + "-stderr-tail"
	model := &fakeModel{responses: []Response{
		{Turn: &ActTurn{Bash: BashBatch{Commands: []BashAction{{Command: "generate-noise"}}}}},
		{Turn: verifiedDone("finished")},
	}}
	executor := &fakeExecutor{results: []CommandResult{{Stdout: hugeStdout, Stderr: hugeStderr, ExitCode: 0}}}
	notify, events := collectEvents()
	agent := NewAgent(model, executor, "/tmp")
	agent.Notify = notify

	if err := agent.Run(context.Background(), "handle noisy command"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	payload := messagesText(model.got[1])
	if strings.Contains(payload, hugeStdout) || strings.Contains(payload, hugeStderr) || !strings.Contains(payload, "compressed") {
		t.Fatalf("model query received unbounded command output")
	}
	done, ok := firstEvent[EventCommandDone](*events)
	if !ok || done.Stdout != hugeStdout || done.Stderr != hugeStderr {
		t.Fatalf("command done event = %#v", done)
	}
}
