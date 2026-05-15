package clnkr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func envListToMap(list []string) map[string]string {
	env := make(map[string]string, len(list))
	for _, item := range list {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			env[key] = value
		}
	}
	return env
}

func TestCommandExecutor(t *testing.T) {
	exec := &CommandExecutor{}
	ctx := context.Background()

	for _, field := range []string{"Timeout", "ProcessGroup", "ExtraEnv"} {
		t.Run("does not expose "+field, func(t *testing.T) {
			if _, ok := reflect.TypeOf(CommandExecutor{}).FieldByName(field); ok {
				t.Fatalf("CommandExecutor should not expose %s", field)
			}
		})
	}

	t.Run("captures stdout stderr cwd and exit", func(t *testing.T) {
		out, err := exec.Execute(ctx, `pwd && echo error >&2`, "/tmp")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		trimmed := strings.TrimSpace(out.Stdout)
		if trimmed != "/tmp" && trimmed != "/private/tmp" {
			t.Errorf("stdout = %q, want /tmp or /private/tmp", out.Stdout)
		}
		if strings.TrimSpace(out.Stderr) != "error" {
			t.Errorf("stderr = %q, want %q", out.Stderr, "error")
		}
		if out.ExitCode != 0 {
			t.Errorf("exit code = %d, want 0", out.ExitCode)
		}
		assertExitOutcome(t, out.Outcome, 0)
	})

	t.Run("returns exit code and stderr on failure", func(t *testing.T) {
		out, err := exec.Execute(ctx, "echo nope >&2; exit 7", "/tmp")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if out.ExitCode != 7 {
			t.Errorf("exit code = %d, want 7", out.ExitCode)
		}
		if strings.TrimSpace(out.Stderr) != "nope" {
			t.Errorf("stderr = %q, want %q", out.Stderr, "nope")
		}
		assertExitOutcome(t, out.Outcome, 7)
	})

	t.Run("records timeout outcome", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		out, err := exec.Execute(ctx, "sleep 1", "/tmp")
		if err == nil {
			t.Fatal("expected error from expired context, got nil")
		}
		if ctx.Err() != context.DeadlineExceeded {
			t.Fatalf("caller context = %v, want deadline exceeded (err=%v)", ctx.Err(), err)
		}
		if out.Outcome.Type != CommandOutcomeTimeout {
			t.Fatalf("outcome = %#v, want timeout", out.Outcome)
		}
	})

	t.Run("records cancelled outcome", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		out, err := exec.Execute(ctx, "echo hello", "/tmp")
		if err == nil {
			t.Fatal("expected error from cancelled context, got nil")
		}
		if out.Outcome.Type != CommandOutcomeCancelled {
			t.Fatalf("outcome = %#v, want cancelled", out.Outcome)
		}
	})

	t.Run("records host execution error outcome", func(t *testing.T) {
		out, err := exec.Execute(ctx, "pwd", filepath.Join(t.TempDir(), "missing"))
		if err == nil {
			t.Fatal("expected host execution error, got nil")
		}
		if out.Outcome.Type != CommandOutcomeError {
			t.Fatalf("outcome = %#v, want error", out.Outcome)
		}
		if out.Outcome.Message == "" {
			t.Fatal("outcome message is empty, want host error detail")
		}
	})

	t.Run("context cancellation kills child processes", func(t *testing.T) {
		dir := t.TempDir()
		marker := filepath.Join(dir, "child-survived")
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, err := exec.Execute(ctx, "sh -c 'sleep 1; touch child-survived' & wait", dir)
		if err == nil {
			t.Fatal("expected error from expired context, got nil")
		}

		time.Sleep(1500 * time.Millisecond)
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("child process survived cancellation and wrote %s", marker)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat marker: %v", err)
		}
	})

	t.Run("process group assigns new pgid", func(t *testing.T) {
		parentPgid, err := syscall.Getpgid(os.Getpid())
		if err != nil {
			t.Fatalf("failed to get parent pgid: %v", err)
		}

		out, err := exec.Execute(ctx, "ps -o pgid= -p $$", "/tmp")
		if err != nil {
			if strings.Contains(out.Stderr, "Operation not permitted") {
				t.Skip("ps is not permitted in this sandbox")
			}
			t.Fatalf("unexpected error: %v", err)
		}
		childPgid, err := strconv.Atoi(strings.TrimSpace(out.Stdout))
		if err != nil {
			t.Fatalf("parse child pgid %q: %v", strings.TrimSpace(out.Stdout), err)
		}
		if childPgid == parentPgid {
			t.Errorf("child pgid %d should differ from parent pgid %d", childPgid, parentPgid)
		}
	})

	t.Run("base environment controls command environment", func(t *testing.T) {
		t.Setenv("CLNKR_PARENT_ONLY", "parent")
		envExec := &CommandExecutor{BaseEnv: map[string]string{
			"CLNKR_ONLY": "yes",
			"PATH":       os.Getenv("PATH"),
		}}

		out, err := envExec.Execute(ctx, `printf "%s,%s" "$CLNKR_ONLY" "$CLNKR_PARENT_ONLY"`, "/tmp")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Stdout != "yes," {
			t.Fatalf("stdout = %q, want %q", out.Stdout, "yes,")
		}
	})

	t.Run("SetEnv uses a copied environment snapshot", func(t *testing.T) {
		envExec := &CommandExecutor{}
		base := envListToMap(os.Environ())
		base["CLNKR_SETENV_COPY"] = "before"
		envExec.SetEnv(base)
		base["CLNKR_SETENV_COPY"] = "after"

		out, err := envExec.Execute(ctx, `printf %s "$CLNKR_SETENV_COPY"`, "/tmp")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Stdout != "before" {
			t.Fatalf("stdout = %q, want %q", out.Stdout, "before")
		}
	})

	t.Run("captures post-command shell state", func(t *testing.T) {
		out, err := exec.Execute(ctx, `export CLNKR_TEST_VAR=ok && cd /tmp && printf done`, "/")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Stdout != "done" {
			t.Fatalf("stdout = %q, want %q", out.Stdout, "done")
		}
		if got := out.PostEnv["CLNKR_TEST_VAR"]; got != "ok" {
			t.Fatalf("PostEnv[CLNKR_TEST_VAR] = %q, want ok", got)
		}
		assertSamePath(t, out.PostCwd, "/tmp")
	})

	t.Run("state file does not leak into environment snapshots", func(t *testing.T) {
		envExec := &CommandExecutor{}
		base := envListToMap(os.Environ())
		base["BASE"] = "ok"
		delete(base, "CLNKR_STATE_FILE")
		envExec.SetEnv(base)

		out, err := envExec.Execute(ctx, `export CLNKR_TEST_VAR=ok && printf done`, "/tmp")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := out.PostEnv["CLNKR_STATE_FILE"]; ok {
			t.Fatalf("CLNKR_STATE_FILE leaked into PostEnv: %+v", out.PostEnv)
		}
		if got := envExec.BaseEnv["CLNKR_STATE_FILE"]; got != "" {
			t.Fatalf("CLNKR_STATE_FILE leaked into BaseEnv: %q", got)
		}
	})

	t.Run("captures full env snapshots across stateful commands", func(t *testing.T) {
		envExec := &CommandExecutor{}
		out1 := executeAndUseEnv(t, envExec, `export CLNKR_CHAIN_ONE=one && cd /tmp && printf done`, "/tmp")
		if out1.PostEnv["CLNKR_CHAIN_ONE"] != "one" {
			t.Fatalf("missing chain var in snapshot: %+v", out1.PostEnv)
		}

		out2 := executeAndUseEnv(t, envExec, `export CLNKR_CHAIN_TWO=two && cd /tmp && printf done`, "/tmp")
		if out2.PostEnv["CLNKR_CHAIN_ONE"] != "one" || out2.PostEnv["CLNKR_CHAIN_TWO"] != "two" {
			t.Fatalf("snapshot should include earlier exports: %+v", out2.PostEnv)
		}

		out3 := executeAndUseEnv(t, envExec, `unset CLNKR_CHAIN_ONE && cd /tmp && printf done`, "/tmp")
		if _, ok := out3.PostEnv["CLNKR_CHAIN_ONE"]; ok {
			t.Fatalf("unset variable should be removed from snapshot: %+v", out3.PostEnv)
		}
		if out3.PostEnv["CLNKR_CHAIN_TWO"] != "two" {
			t.Fatalf("expected other vars to persist: %+v", out3.PostEnv)
		}
		if _, ok := out3.PostEnv["CLNKR_STATE_FILE"]; ok {
			t.Fatalf("state file leaked into PostEnv: %+v", out3.PostEnv)
		}

		out4, err := envExec.Execute(ctx, `printf "%s,%s" "$CLNKR_CHAIN_ONE" "$CLNKR_CHAIN_TWO"`, "/tmp")
		if err != nil {
			t.Fatalf("step 4: %v", err)
		}
		if out4.Stdout != ",two" {
			t.Fatalf("stdout = %q, want %q", out4.Stdout, ",two")
		}
	})

	t.Run("collects feedback from a clean git baseline", func(t *testing.T) {
		repo := initGitRepo(t)
		writeFile(t, filepath.Join(repo, "note.txt"), "before\n")
		runGit(t, repo, "add", "note.txt")
		runGit(t, repo, "commit", "-qm", "add note")

		out, err := exec.Execute(ctx, "printf 'next\\n' > note.txt", repo)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := out.Feedback.ChangedFiles; !reflect.DeepEqual(got, []string{"note.txt"}) {
			t.Fatalf("changed files = %#v, want %#v", got, []string{"note.txt"})
		}
		if !strings.Contains(out.Feedback.Diff, "note.txt") || !strings.Contains(out.Feedback.Diff, "+next") {
			t.Fatalf("diff = %q, want note.txt and +next", out.Feedback.Diff)
		}
	})

	t.Run("omits feedback when repo was dirty before command", func(t *testing.T) {
		repo := initGitRepo(t)
		writeFile(t, filepath.Join(repo, "dirty.txt"), "dirty\n")

		out, err := exec.Execute(ctx, "printf 'next\\n' > note.txt", repo)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out.Feedback.ChangedFiles) != 0 || out.Feedback.Diff != "" {
			t.Fatalf("feedback = %#v, want empty", out.Feedback)
		}
	})

	t.Run("tracks both sides of a rename", func(t *testing.T) {
		repo := initGitRepo(t)
		writeFile(t, filepath.Join(repo, "old.txt"), "before\n")
		runGit(t, repo, "add", "old.txt")
		runGit(t, repo, "commit", "-qm", "add old")

		out, err := exec.Execute(ctx, "git mv old.txt new.txt", repo)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := out.Feedback.ChangedFiles; !reflect.DeepEqual(got, []string{"new.txt", "old.txt"}) {
			t.Fatalf("changed files = %#v, want %#v", got, []string{"new.txt", "old.txt"})
		}
	})

	t.Run("normalizes feedback paths relative to final cwd", func(t *testing.T) {
		repo := initGitRepo(t)
		subdir := filepath.Join(repo, "dir", "subdir")

		out, err := exec.Execute(ctx, "mkdir -p dir/subdir && cd dir/subdir && printf 'nested\\n' > local.txt", repo)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertSamePath(t, out.PostCwd, subdir)
		if got := out.Feedback.ChangedFiles; !reflect.DeepEqual(got, []string{"local.txt"}) {
			t.Fatalf("changed files = %#v, want %#v", got, []string{"local.txt"})
		}
	})
}

func assertExitOutcome(t *testing.T, got CommandOutcome, want int) {
	t.Helper()

	if got.Type != CommandOutcomeExit || got.ExitCode == nil || *got.ExitCode != want {
		t.Fatalf("outcome = %#v, want exit %d", got, want)
	}
}

func assertSamePath(t *testing.T, got, want string) {
	t.Helper()

	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("resolve got path %q: %v", got, err)
	}
	wantResolved, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatalf("resolve want path %q: %v", want, err)
	}
	if gotResolved != wantResolved {
		t.Fatalf("path = %q (resolved %q), want %q (resolved %q)", got, gotResolved, want, wantResolved)
	}
}

func executeAndUseEnv(t *testing.T, exec *CommandExecutor, command, dir string) CommandResult {
	t.Helper()

	out, err := exec.Execute(context.Background(), command, dir)
	if err != nil {
		t.Fatalf("execute %q: %v", command, err)
	}
	exec.SetEnv(out.PostEnv)
	return out
}

func initGitRepo(t *testing.T) string {
	t.Helper()

	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.name", "clnkr test")
	runGit(t, repo, "config", "user.email", "clnkr@example.com")
	writeFile(t, filepath.Join(repo, ".gitignore"), "")
	runGit(t, repo, "add", ".gitignore")
	runGit(t, repo, "commit", "-qm", "init")
	return repo
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
