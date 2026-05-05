package delegation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr"
)

func TestExecRunnerWritesArtifacts(t *testing.T) {
	dir := t.TempDir()
	exe := helperChildProbeExecutable(t, "child summary\n")
	runner := ExecRunner{Executable: exe}
	req := Request{
		ChildID:     "child-001",
		ParentCwd:   dir,
		Task:        "inspect README",
		Depth:       1,
		MaxCommands: 5,
		Timeout:     "1m",
		ArtifactDir: filepath.Join(dir, "delegates", "child-001"),
	}

	result, err := runner.RunChildProbe(context.Background(), req)
	if err != nil {
		t.Fatalf("RunChildProbe: %v", err)
	}
	if result.Status != StatusDone || result.Summary != "child summary" {
		t.Fatalf("result = %#v, want done child summary", result)
	}
	for _, path := range []string{result.Artifacts.Input, result.Artifacts.EventLog, result.Artifacts.Trajectory, result.Artifacts.Result, result.Artifacts.Stdout, result.Artifacts.Stderr} {
		if path == "" {
			t.Fatalf("artifact path empty in %#v", result.Artifacts)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("artifact %s missing: %v", path, err)
		}
	}
}

func TestExecRunnerDoesNotUseStdoutAsSummary(t *testing.T) {
	dir := t.TempDir()
	exe := helperChildProbeExecutable(t, "command output that should stay out of summary\nchild summary\n")
	runner := ExecRunner{Executable: exe}
	req := Request{
		ChildID:     "child-001",
		ParentCwd:   dir,
		Task:        "inspect README",
		Depth:       1,
		MaxCommands: 5,
		Timeout:     "1m",
		ArtifactDir: filepath.Join(dir, "delegates", "child-001"),
	}

	result, err := runner.RunChildProbe(context.Background(), req)
	if err != nil {
		t.Fatalf("RunChildProbe: %v", err)
	}
	if result.Summary != "child summary" {
		t.Fatalf("summary = %q, want parsed done summary only", result.Summary)
	}
	data, err := os.ReadFile(result.Artifacts.Stdout)
	if err != nil {
		t.Fatalf("ReadFile stdout artifact: %v", err)
	}
	if !strings.Contains(string(data), "command output that should stay out of summary") {
		t.Fatalf("stdout artifact = %q, want child stdout", data)
	}
}

func helperChildProbeExecutable(t *testing.T, stdout string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "child.sh")
	doneTurn := `{"type":"done","summary":"child summary","verification":{"status":"verified","checks":[{"command":"fake","outcome":"passed","evidence":"fake child"}]},"known_risks":[]}`
	eventLine := `{"type":"response","payload":{"turn":` + doneTurn + `,"usage":{"input_tokens":1,"output_tokens":1}}}`
	script := "#!/usr/bin/env bash\n" +
		"set -eu\n" +
		"event_log=''\n" +
		"trajectory=''\n" +
		"while [ \"$#\" -gt 0 ]; do\n" +
		"  case \"$1\" in\n" +
		"    --event-log) event_log=\"$2\"; shift 2 ;;\n" +
		"    --trajectory) trajectory=\"$2\"; shift 2 ;;\n" +
		"    -p|--prompt) shift 2 ;;\n" +
		"    --max-steps|--delegate-depth|--system-prompt-append) shift 2 ;;\n" +
		"    --full-send|--delegate-child-read-only|--no-system-prompt) shift ;;\n" +
		"    *) shift ;;\n" +
		"  esac\n" +
		"done\n" +
		"printf '%s\\n' " + shellQuote(eventLine) + " > \"$event_log\"\n" +
		"printf '[{\"role\":\"assistant\",\"content\":%s}]\\n' " + shellQuote(doneTurn) + " > \"$trajectory\"\n" +
		"printf '%s' " + shellQuote(stdout) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile helper: %v", err)
	}
	return path
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

type fakeExecutor struct {
	result clnkr.CommandResult
	calls  int
}

func (e *fakeExecutor) Execute(context.Context, string, string) (clnkr.CommandResult, error) {
	e.calls++
	return e.result, nil
}
