package delegation

import (
	"context"
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr"
)

func TestReadOnlyExecutorDeniesRepresentativeWrites(t *testing.T) {
	tests := []string{
		"touch note.txt",
		"rm note.txt",
		"mv old new",
		"cp a b",
		"mkdir out",
		"sed -i 's/a/b/' file.txt",
		"printf 'x' > file.txt",
		"echo x >> file.txt",
		"echo x | tee file.txt",
	}
	for _, command := range tests {
		t.Run(command, func(t *testing.T) {
			result, err := NewReadOnlyExecutor(&fakeExecutor{}).Execute(context.Background(), command, "/tmp")
			if err == nil || !strings.Contains(err.Error(), "read-only child probe denied command") {
				t.Fatalf("Execute error = %v, want read-only denial", err)
			}
			if result.Outcome.Type != clnkr.CommandOutcomeDenied {
				t.Fatalf("outcome = %#v, want denied", result.Outcome)
			}
		})
	}
}

func TestReadOnlyExecutorAllowsReadCommands(t *testing.T) {
	inner := &fakeExecutor{result: clnkr.CommandResult{Stdout: "ok\n", ExitCode: 0}}
	result, err := NewReadOnlyExecutor(inner).Execute(context.Background(), "sed -n '1,20p' README.md", "/tmp")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Stdout != "ok\n" {
		t.Fatalf("stdout = %q, want ok", result.Stdout)
	}
	if inner.calls != 1 {
		t.Fatalf("inner calls = %d, want 1", inner.calls)
	}
}
