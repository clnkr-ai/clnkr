package hew

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
		if strings.TrimSpace(out) != "hello" {
			t.Errorf("got %q, want %q", out, "hello")
		}
	})

	t.Run("respects working directory", func(t *testing.T) {
		out, err := exec.Execute(ctx, "pwd", "/tmp")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		trimmed := strings.TrimSpace(out)
		if trimmed != "/tmp" && trimmed != "/private/tmp" {
			t.Errorf("got %q, want /tmp or /private/tmp", trimmed)
		}
	})

	t.Run("captures stderr", func(t *testing.T) {
		out, err := exec.Execute(ctx, "echo error >&2", "/tmp")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.TrimSpace(out) != "error" {
			t.Errorf("got %q, want %q", out, "error")
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
			t.Fatalf("unexpected error: %v", err)
		}
		childPgid, err := strconv.Atoi(strings.TrimSpace(out))
		if err != nil {
			t.Fatalf("failed to parse child pgid %q: %v", strings.TrimSpace(out), err)
		}
		if childPgid == parentPgid {
			t.Errorf("child pgid %d should differ from parent pgid %d", childPgid, parentPgid)
		}
	})
}
