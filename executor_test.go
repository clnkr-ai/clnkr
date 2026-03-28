package clnkr

import (
	"context"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCommandExecutor(t *testing.T) {
	exec := &CommandExecutor{Timeout: 5 * time.Second}
	ctx := context.Background()

	t.Run("simple command", func(t *testing.T) {
		out, err := exec.Execute(ctx, "echo hello", "/tmp")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.TrimSpace(out.Stdout) != "hello" {
			t.Errorf("got %q, want %q", out.Stdout, "hello")
		}
		if out.Stderr != "" {
			t.Errorf("expected empty stderr, got %q", out.Stderr)
		}
		if out.ExitCode != 0 {
			t.Errorf("expected exit code 0, got %d", out.ExitCode)
		}
	})

	t.Run("respects working directory", func(t *testing.T) {
		out, err := exec.Execute(ctx, "pwd", "/tmp")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		trimmed := strings.TrimSpace(out.Stdout)
		if trimmed != "/tmp" && trimmed != "/private/tmp" {
			t.Errorf("got %q, want /tmp or /private/tmp", trimmed)
		}
	})

	t.Run("captures stderr", func(t *testing.T) {
		out, err := exec.Execute(ctx, "echo error >&2", "/tmp")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Stdout != "" {
			t.Errorf("expected empty stdout, got %q", out.Stdout)
		}
		if strings.TrimSpace(out.Stderr) != "error" {
			t.Errorf("got %q, want %q", out.Stderr, "error")
		}
	})

	t.Run("returns error on timeout", func(t *testing.T) {
		shortExec := &CommandExecutor{Timeout: 100 * time.Millisecond}
		_, err := shortExec.Execute(ctx, "sleep 10", "/tmp")
		if err == nil {
			t.Error("expected timeout error, got nil")
		}
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := exec.Execute(ctx, "echo hello", "/tmp")
		if err == nil {
			t.Error("expected error from cancelled context, got nil")
		}
	})

	t.Run("process group assigns new pgid", func(t *testing.T) {
		pgExec := &CommandExecutor{Timeout: 5 * time.Second, ProcessGroup: true}

		parentPgid, err := syscall.Getpgid(os.Getpid())
		if err != nil {
			t.Fatalf("failed to get parent pgid: %v", err)
		}

		out, err := pgExec.Execute(ctx, "ps -o pgid= -p $$", "/tmp")
		if err != nil {
			if strings.Contains(out.Stderr, "Operation not permitted") {
				t.Skip("ps is not permitted in this sandbox")
			}
			t.Fatalf("unexpected error: %v", err)
		}
		childPgid, err := strconv.Atoi(strings.TrimSpace(out.Stdout))
		if err != nil {
			t.Fatalf("failed to parse child pgid %q: %v", strings.TrimSpace(out.Stdout), err)
		}
		if childPgid == parentPgid {
			t.Errorf("child pgid %d should differ from parent pgid %d", childPgid, parentPgid)
		}
	})

	t.Run("returns exit code on failure", func(t *testing.T) {
		out, err := exec.Execute(ctx, "echo nope >&2; exit 7", "/tmp")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if out.ExitCode != 7 {
			t.Errorf("expected exit code 7, got %d", out.ExitCode)
		}
		if strings.TrimSpace(out.Stderr) != "nope" {
			t.Errorf("expected stderr %q, got %q", "nope", out.Stderr)
		}
	})

	t.Run("injects persisted env", func(t *testing.T) {
		envExec := &CommandExecutor{Timeout: 5 * time.Second}
		envExec.SetEnv(map[string]string{"NANOCHAT_BASE_DIR": "/tmp/runtime"})
		out, err := envExec.Execute(ctx, `printf %s "$NANOCHAT_BASE_DIR"`, "/tmp")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Stdout != "/tmp/runtime" {
			t.Fatalf("got %q, want %q", out.Stdout, "/tmp/runtime")
		}
	})
}
