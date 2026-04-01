package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	repoRoot := repoRoot(t)
	cleanupGeneratedRunOutput(t, repoRoot)
	t.Cleanup(func() {
		cleanupGeneratedRunOutput(t, repoRoot)
	})

	t.Run("run default suite prints summary", func(t *testing.T) {
		cleanupGeneratedRunOutput(t, repoRoot)
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		exitCode := run([]string{"run", "--suite", "default"}, repoRoot, stdout, stderr, func(string) string { return "" })
		if exitCode != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", exitCode, stderr.String())
		}
		if got, want := stdout.String(), "suite=default tasks=1 trials=1 passed=1 failed=0\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		if stderr.Len() != 0 {
			t.Fatalf("stderr = %q, want empty", stderr.String())
		}
	})

	t.Run("failed trial prints stderr context and exits non-zero", func(t *testing.T) {
		cleanupGeneratedRunOutput(t, repoRoot)
		suiteID := writeTempSuite(t, repoRoot, suiteSpec{
			trialsPerTask: 1,
			stopOnFirst:   true,
			maxFailed:     1,
			tasks:         []suiteTaskSpec{{id: "task-fail", expectedNote: "wrong\n"}},
		})
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		exitCode := run([]string{"run", "--suite", suiteID}, repoRoot, stdout, stderr, func(string) string { return "" })
		if exitCode == 0 {
			t.Fatalf("exit code = 0, want non-zero")
		}
		if !strings.Contains(stdout.String(), "suite="+suiteID) {
			t.Fatalf("stdout = %q, want suite summary", stdout.String())
		}
		if !strings.Contains(stderr.String(), "task=task-fail") || !strings.Contains(stderr.String(), "trial=") || !strings.Contains(stderr.String(), "required graders failed") {
			t.Fatalf("stderr = %q, want task/trial failure context", stderr.String())
		}
	})

	t.Run("unsupported first-wave subcommand fails explicitly", func(t *testing.T) {
		for _, subcommand := range []string{"list-suites", "list-tasks", "validate"} {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			exitCode := run([]string{subcommand}, repoRoot, stdout, stderr, func(string) string { return "" })
			if exitCode == 0 {
				t.Fatalf("%s exit code = 0, want non-zero", subcommand)
			}
			if !strings.Contains(stderr.String(), "not available in the first wave") {
				t.Fatalf("%s stderr = %q, want explicit unsupported-subcommand error", subcommand, stderr.String())
			}
		}
	})

	t.Run("invalid evaluation mode surfaces config error", func(t *testing.T) {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		exitCode := run([]string{"run", "--suite", "default"}, repoRoot, stdout, stderr, func(key string) string {
			if key == "CLNKR_EVALUATION_MODE" {
				return "bogus"
			}
			return ""
		})
		if exitCode == 0 {
			t.Fatal("exit code = 0, want non-zero")
		}
		if !strings.Contains(stderr.String(), "unknown CLNKR_EVALUATION_MODE") {
			t.Fatalf("stderr = %q, want invalid mode error", stderr.String())
		}
	})

	t.Run("missing live-provider configuration surfaces config error", func(t *testing.T) {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		exitCode := run([]string{"run", "--suite", "default"}, repoRoot, stdout, stderr, func(key string) string {
			if key == "CLNKR_EVALUATION_MODE" {
				return "live-provider"
			}
			return ""
		})
		if exitCode == 0 {
			t.Fatal("exit code = 0, want non-zero")
		}
		if !strings.Contains(stderr.String(), "missing API key") {
			t.Fatalf("stderr = %q, want live-provider config error", stderr.String())
		}
	})
}

func repoRoot(t *testing.T) string {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	return filepath.Dir(filepath.Dir(cwd))
}

func cleanupGeneratedRunOutput(t *testing.T, repoRoot string) {
	t.Helper()

	for _, rel := range []string{"evaluations/trials", "evaluations/reports"} {
		_ = os.RemoveAll(filepath.Join(repoRoot, rel))
	}
}

type suiteSpec struct {
	trialsPerTask int
	stopOnFirst   bool
	maxFailed     int
	tasks         []suiteTaskSpec
}

type suiteTaskSpec struct {
	id           string
	expectedNote string
}

func writeTempSuite(t *testing.T, repoRoot string, spec suiteSpec) string {
	t.Helper()

	suitesRoot := filepath.Join(repoRoot, "evaluations", "suites")
	suiteDir, err := os.MkdirTemp(suitesRoot, "clnkeval-*")
	if err != nil {
		t.Fatalf("MkdirTemp(): %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(suiteDir)
	})

	tasks := make([]string, 0, len(spec.tasks))
	for _, task := range spec.tasks {
		tasks = append(tasks, task.id)
		taskDir := filepath.Join(suiteDir, "tasks", task.id)
		mustWrite(t, filepath.Join(taskDir, "input", "instruction.txt"), "Create note.txt in the repo root with the contents hello and then finish.\n")
		mustWrite(t, filepath.Join(taskDir, "input", "model-turns.json"), "[\"{\\\"type\\\":\\\"act\\\",\\\"command\\\":\\\"printf 'hello\\\\n' > note.txt\\\"}\",\"{\\\"type\\\":\\\"done\\\",\\\"summary\\\":\\\"finished\\\"}\"]\n")
		mustWrite(t, filepath.Join(taskDir, "input", "project", "AGENTS.md"), "Keep changes tight. Work in the current directory.\n")
		mustWrite(t, filepath.Join(taskDir, "expected", "workspace", "AGENTS.md"), "Keep changes tight. Work in the current directory.\n")
		mustWrite(t, filepath.Join(taskDir, "expected", "workspace", "note.txt"), task.expectedNote)
		taskJSON := `{
  "id": "` + task.id + `",
  "instruction_file": "input/instruction.txt",
  "scripted_turns_file": "input/model-turns.json",
  "working_directory": "workspace",
  "full_send": true,
  "step_limit": 10,
  "graders": {
    "outcome_workspace_snapshot": {
      "enabled": true,
      "required": true
    },
    "transcript_command_trace": {
      "enabled": false,
      "required": false
    }
  }
}`
		mustWrite(t, filepath.Join(taskDir, "task.json"), taskJSON)
	}

	tasksJSON, err := json.Marshal(tasks)
	if err != nil {
		t.Fatalf("json.Marshal(tasks): %v", err)
	}
	suiteID := filepath.Base(suiteDir)
	suiteJSON := `{
  "id": "` + suiteID + `",
  "description": "clnkeval temp suite",
  "mode": "mock-provider",
  "trials_per_task": ` + strconv.Itoa(spec.trialsPerTask) + `,
  "failure_policy": {
    "stop_on_first_failure": ` + strconv.FormatBool(spec.stopOnFirst) + `,
    "max_failed_tasks": ` + strconv.Itoa(spec.maxFailed) + `
  },
  "tasks": ` + string(tasksJSON) + `
}`
	mustWrite(t, filepath.Join(suiteDir, "suite.json"), suiteJSON)
	return suiteID
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
