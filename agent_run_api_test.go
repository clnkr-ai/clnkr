package clnkr

import (
	"context"
	"errors"
	"testing"
)

func TestRunPublicAPIGuards(t *testing.T) {
	t.Run("default max steps", func(t *testing.T) {
		if got := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp").MaxSteps; got != DefaultMaxSteps {
			t.Fatalf("MaxSteps = %d, want %d", got, DefaultMaxSteps)
		}
	})

	t.Run("full-send clarify returns clarification needed", func(t *testing.T) {
		agent := NewAgent(&fakeModel{responses: []Response{{Turn: &ClarifyTurn{Question: "Which repo?"}}}}, &fakeExecutor{}, "/tmp")
		if err := agent.Run(context.Background(), "inspect"); !errors.Is(err, ErrClarificationNeeded) {
			t.Fatalf("Run error = %v, want ErrClarificationNeeded", err)
		}
	})

	t.Run("value turns from model are accepted", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Turn: ActTurn{Bash: BashBatch{Commands: []BashAction{{Command: "pwd"}}}}},
			{Turn: *verifiedDone("done")},
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "/tmp\n", ExitCode: 0}}}
		if err := NewAgent(model, executor, "/tmp").Run(context.Background(), "pwd"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if executor.calls != 1 {
			t.Fatalf("executor calls = %d, want 1", executor.calls)
		}
	})

	t.Run("cancelled context stops run", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		model := &fakeModel{}
		err := NewAgent(model, &fakeExecutor{}, "/tmp").Run(ctx, "do it")
		if !errors.Is(err, context.Canceled) || model.calls != 0 {
			t.Fatalf("Run error=%v model calls=%d", err, model.calls)
		}
	})
}
