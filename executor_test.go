package clnkr

import (
	"context"
	"os"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// envListToMap converts a KEY=VALUE environ slice to a map.
// Only used in tests; production code uses envMapToList for the reverse direction.
func envListToMap(list []string) map[string]string {
	env := make(map[string]string, len(list))
	for _, item := range list {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		env[key] = value
	}
	return env
}

func TestCommandExecutor(t *testing.T) {
	exec := &CommandExecutor{}
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

	t.Run("does not expose timeout field", func(t *testing.T) {
		if _, ok := reflect.TypeOf(CommandExecutor{}).FieldByName("Timeout"); ok {
			t.Fatal("CommandExecutor should not expose a Timeout field")
		}
	})

	t.Run("respects context deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, err := exec.Execute(ctx, "sleep 1", "/tmp")
		if err == nil {
			t.Fatal("expected error from expired context, got nil")
		}
		if ctx.Err() != context.DeadlineExceeded {
			t.Fatalf("expected caller context deadline exceeded, got %v (err=%v)", ctx.Err(), err)
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
		pgExec := &CommandExecutor{ProcessGroup: true}

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
		envExec := &CommandExecutor{}
		base := envListToMap(os.Environ())
		base["NANOCHAT_BASE_DIR"] = "/tmp/runtime"
		envExec.SetEnv(base)
		out, err := envExec.Execute(ctx, `printf %s "$NANOCHAT_BASE_DIR"`, "/tmp")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Stdout != "/tmp/runtime" {
			t.Fatalf("got %q, want %q", out.Stdout, "/tmp/runtime")
		}
	})

	t.Run("captures post-command shell state", func(t *testing.T) {
		stateExec := &CommandExecutor{}
		cmd := `export CLNKR_TEST_VAR=ok && cd /tmp && printf done`
		out, err := stateExec.Execute(ctx, cmd, "/")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Stdout != "done" {
			t.Fatalf("got stdout %q, want %q", out.Stdout, "done")
		}
		if got := out.PostEnv["CLNKR_TEST_VAR"]; got != "ok" {
			t.Fatalf("got env %q, want %q", got, "ok")
		}
		if out.PostCwd != "/tmp" && out.PostCwd != "/private/tmp" {
			t.Fatalf("got cwd %q, want /tmp or /private/tmp", out.PostCwd)
		}
	})

	t.Run("state file does not leak into PostEnv", func(t *testing.T) {
		stateExec := &CommandExecutor{}
		base := envListToMap(os.Environ())
		base["BASE"] = "ok"
		stateExec.SetEnv(base)

		out, err := stateExec.Execute(ctx, `export CLNKR_TEST_VAR=ok && printf done`, "/tmp")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := out.PostEnv["CLNKR_STATE_FILE"]; ok {
			t.Fatalf("CLNKR_STATE_FILE leaked into PostEnv: %+v", out.PostEnv)
		}
		if got := stateExec.ExtraEnv["CLNKR_STATE_FILE"]; got != "" {
			t.Fatalf("CLNKR_STATE_FILE leaked into ExtraEnv: %q", got)
		}
	})

	t.Run("captures full env snapshots across stateful commands", func(t *testing.T) {
		stateExec := &CommandExecutor{}

		cmd1 := `export CLNKR_CHAIN_ONE=one && cd /tmp && printf done`
		out1, err := stateExec.Execute(ctx, cmd1, "/tmp")
		if err != nil {
			t.Fatalf("step 1: %v", err)
		}
		if out1.PostEnv["CLNKR_CHAIN_ONE"] != "one" {
			t.Fatalf("missing chain var in snapshot: %+v", out1.PostEnv)
		}

		stateExec.SetEnv(out1.PostEnv)
		cmd2 := `export CLNKR_CHAIN_TWO=two && cd /tmp && printf done`
		out2, err := stateExec.Execute(ctx, cmd2, "/tmp")
		if err != nil {
			t.Fatalf("step 2: %v", err)
		}
		if out2.PostEnv["CLNKR_CHAIN_ONE"] != "one" || out2.PostEnv["CLNKR_CHAIN_TWO"] != "two" {
			t.Fatalf("snapshot should include earlier exports: %+v", out2.PostEnv)
		}

		stateExec.SetEnv(out2.PostEnv)
		cmd3 := `unset CLNKR_CHAIN_ONE && cd /tmp && printf done`
		out3, err := stateExec.Execute(ctx, cmd3, "/tmp")
		if err != nil {
			t.Fatalf("step 3: %v", err)
		}
		if _, ok := out3.PostEnv["CLNKR_CHAIN_ONE"]; ok {
			t.Fatalf("unset variable should be removed from snapshot: %+v", out3.PostEnv)
		}
		if out3.PostEnv["CLNKR_CHAIN_TWO"] != "two" {
			t.Fatalf("expected other vars to persist: %+v", out3.PostEnv)
		}

		stateExec.SetEnv(out3.PostEnv)
		cmd4 := `printf "%s,%s" "$CLNKR_CHAIN_ONE" "$CLNKR_CHAIN_TWO"`
		out4, err := stateExec.Execute(ctx, cmd4, "/tmp")
		if err != nil {
			t.Fatalf("step 4: %v", err)
		}
		if out4.Stdout != ",two" {
			t.Fatalf("expected unset var to stay cleared, got %q", out4.Stdout)
		}
		if _, ok := out3.PostEnv["CLNKR_STATE_FILE"]; ok {
			t.Fatalf("state file leaked into PostEnv: %+v", out3.PostEnv)
		}
	})
}
