package evaluations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRunSuite(t *testing.T) {
	repoRoot := repoRoot(t)
	cleanupGeneratedRunOutput(t, repoRoot)
	t.Cleanup(func() {
		cleanupGeneratedRunOutput(t, repoRoot)
	})

	t.Run("default mock-provider suite writes deterministic outputs", func(t *testing.T) {
		cleanupGeneratedRunOutput(t, repoRoot)

		report, err := RunSuite(context.Background(), repoRoot, "default", RunConfig{Mode: ModeMockProvider})
		if err != nil {
			t.Fatalf("RunSuite(): %v", err)
		}
		if report.SuiteID != "default" {
			t.Fatalf("suite id = %q, want default", report.SuiteID)
		}
		if report.TaskCount != 1 || report.TrialCount != 1 || report.Passed != 1 || report.Failed != 0 {
			t.Fatalf("report counts = %#v, want one passing trial", report)
		}
		if len(report.Tasks) != 1 || report.Tasks[0].TaskID != "001-basic-edit" {
			t.Fatalf("report tasks = %#v, want default task", report.Tasks)
		}
		if len(report.Tasks[0].Trials) != 1 {
			t.Fatalf("trial count = %d, want 1", len(report.Tasks[0].Trials))
		}

		bundlePath := report.Tasks[0].Trials[0].BundlePath
		if bundlePath == "" {
			t.Fatal("bundle path is empty")
		}
		if !strings.HasPrefix(bundlePath, filepath.Join(repoRoot, "evaluations", "trials")+string(os.PathSeparator)) {
			t.Fatalf("bundle path = %q, want under evaluations/trials", bundlePath)
		}
		if _, err := os.Stat(filepath.Join(bundlePath, "bundle.json")); err != nil {
			t.Fatalf("bundle.json missing: %v", err)
		}
		for _, rel := range []string{
			filepath.Join(repoRoot, "evaluations", "reports", "open-test-report.xml"),
			filepath.Join(repoRoot, "evaluations", "reports", "junit.xml"),
		} {
			if data, err := os.ReadFile(rel); err != nil {
				t.Fatalf("ReadFile(%q): %v", rel, err)
			} else if len(data) == 0 {
				t.Fatalf("%s is empty", rel)
			}
		}
	})

	t.Run("suite task order comes from suite.json", func(t *testing.T) {
		cleanupGeneratedRunOutput(t, repoRoot)
		suiteID := writeTempRunSuite(t, repoRoot, runSuiteSpec{
			trialsPerTask: 1,
			failurePolicy: FailurePolicy{
				StopOnFirstFailure: false,
				MaxFailedTasks:     10,
			},
			tasks: []runSuiteTaskSpec{
				{id: "task-b", expectedNote: "hello\n"},
				{id: "task-a", expectedNote: "hello\n"},
			},
		})

		report, err := RunSuite(context.Background(), repoRoot, suiteID, RunConfig{Mode: ModeMockProvider})
		if err != nil {
			t.Fatalf("RunSuite(): %v", err)
		}
		if report.TaskCount != 2 || report.TrialCount != 2 || report.Passed != 2 || report.Failed != 0 {
			t.Fatalf("report counts = %#v, want two passing tasks", report)
		}
		if got := []string{report.Tasks[0].TaskID, report.Tasks[1].TaskID}; strings.Join(got, ",") != "task-b,task-a" {
			t.Fatalf("task order = %#v, want suite order [task-b task-a]", got)
		}
		if report.Tasks[0].SuiteTaskIndex != 0 || report.Tasks[1].SuiteTaskIndex != 1 {
			t.Fatalf("suite task indexes = %d/%d, want 0/1", report.Tasks[0].SuiteTaskIndex, report.Tasks[1].SuiteTaskIndex)
		}
	})

	t.Run("trials_per_task controls trial count", func(t *testing.T) {
		cleanupGeneratedRunOutput(t, repoRoot)
		suiteID := writeTempRunSuite(t, repoRoot, runSuiteSpec{
			trialsPerTask: 2,
			failurePolicy: FailurePolicy{
				StopOnFirstFailure: false,
				MaxFailedTasks:     10,
			},
			tasks: []runSuiteTaskSpec{{id: "task-a", expectedNote: "hello\n"}},
		})

		report, err := RunSuite(context.Background(), repoRoot, suiteID, RunConfig{Mode: ModeMockProvider})
		if err != nil {
			t.Fatalf("RunSuite(): %v", err)
		}
		if report.TrialCount != 2 {
			t.Fatalf("trial count = %d, want 2", report.TrialCount)
		}
		if len(report.Tasks[0].Trials) != 2 {
			t.Fatalf("task trial count = %d, want 2", len(report.Tasks[0].Trials))
		}
		if report.Tasks[0].Trials[0].TrialAttempt != 0 || report.Tasks[0].Trials[1].TrialAttempt != 1 {
			t.Fatalf("trial attempts = %d/%d, want 0/1", report.Tasks[0].Trials[0].TrialAttempt, report.Tasks[0].Trials[1].TrialAttempt)
		}
		entries, err := os.ReadDir(filepath.Join(repoRoot, "evaluations", "trials"))
		if err != nil {
			t.Fatalf("ReadDir(trials): %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("trial bundle count = %d, want 2", len(entries))
		}
	})

	t.Run("stop_on_first_failure stops after the first failed task", func(t *testing.T) {
		cleanupGeneratedRunOutput(t, repoRoot)
		suiteID := writeTempRunSuite(t, repoRoot, runSuiteSpec{
			trialsPerTask: 1,
			failurePolicy: FailurePolicy{
				StopOnFirstFailure: true,
				MaxFailedTasks:     10,
			},
			tasks: []runSuiteTaskSpec{
				{id: "task-fail", expectedNote: "wrong\n"},
				{id: "task-pass", expectedNote: "hello\n"},
			},
		})

		report, err := RunSuite(context.Background(), repoRoot, suiteID, RunConfig{Mode: ModeMockProvider})
		if err != nil {
			t.Fatalf("RunSuite(): %v", err)
		}
		if report.TaskCount != 1 || report.TrialCount != 1 || report.Passed != 0 || report.Failed != 1 {
			t.Fatalf("report counts = %#v, want only the first failed task", report)
		}
		if len(report.Tasks) != 1 || report.Tasks[0].TaskID != "task-fail" {
			t.Fatalf("tasks = %#v, want only task-fail", report.Tasks)
		}
	})

	t.Run("max_failed_tasks stops once threshold is reached", func(t *testing.T) {
		cleanupGeneratedRunOutput(t, repoRoot)
		suiteID := writeTempRunSuite(t, repoRoot, runSuiteSpec{
			trialsPerTask: 1,
			failurePolicy: FailurePolicy{
				StopOnFirstFailure: false,
				MaxFailedTasks:     1,
			},
			tasks: []runSuiteTaskSpec{
				{id: "task-fail", expectedNote: "wrong\n"},
				{id: "task-pass", expectedNote: "hello\n"},
			},
		})

		report, err := RunSuite(context.Background(), repoRoot, suiteID, RunConfig{Mode: ModeMockProvider})
		if err != nil {
			t.Fatalf("RunSuite(): %v", err)
		}
		if report.TaskCount != 1 || report.TrialCount != 1 || report.Passed != 0 || report.Failed != 1 {
			t.Fatalf("report counts = %#v, want stop after one failed task", report)
		}
		if len(report.Tasks) != 1 || report.Tasks[0].TaskID != "task-fail" {
			t.Fatalf("tasks = %#v, want only task-fail", report.Tasks)
		}
	})

	t.Run("unknown suite id rejected", func(t *testing.T) {
		cleanupGeneratedRunOutput(t, repoRoot)
		if _, err := RunSuite(context.Background(), repoRoot, "does-not-exist", RunConfig{Mode: ModeMockProvider}); err == nil || !strings.Contains(err.Error(), "load suite") {
			t.Fatalf("RunSuite() error = %v, want load suite failure", err)
		}
	})

	t.Run("canonical bundle write failure leaves no trial output", func(t *testing.T) {
		cleanupGeneratedRunOutput(t, repoRoot)

		// Keep the fixture task path valid while forcing the canonical bundle
		// path component past the usual filesystem component limit.
		longTaskID := strings.Repeat("a", 240)
		suiteID := writeTempRunSuite(t, repoRoot, runSuiteSpec{
			trialsPerTask: 1,
			failurePolicy: FailurePolicy{
				StopOnFirstFailure: false,
				MaxFailedTasks:     10,
			},
			tasks: []runSuiteTaskSpec{{id: longTaskID, expectedNote: "hello\n"}},
		})

		_, err := RunSuite(context.Background(), repoRoot, suiteID, RunConfig{Mode: ModeMockProvider})
		if err == nil || !strings.Contains(err.Error(), "write canonical bundle") {
			t.Fatalf("RunSuite() error = %v, want canonical bundle write failure", err)
		}

		entries, err := os.ReadDir(filepath.Join(repoRoot, "evaluations", "trials"))
		if err != nil {
			t.Fatalf("ReadDir(trials): %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("trial output entries = %d, want 0 after cleanup", len(entries))
		}
	})
}

type runSuiteSpec struct {
	trialsPerTask int
	failurePolicy FailurePolicy
	tasks         []runSuiteTaskSpec
}

type runSuiteTaskSpec struct {
	id           string
	expectedNote string
}

func writeTempRunSuite(t *testing.T, repoRoot string, spec runSuiteSpec) string {
	t.Helper()

	suitesRoot := filepath.Join(repoRoot, "evaluations", "suites")
	suiteDir, err := os.MkdirTemp(suitesRoot, "task6-*")
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
		writeTestFile(t, filepath.Join(taskDir, "input", "instruction.txt"), "Create note.txt in the repo root with the contents hello and then finish.\n")
		writeTestFile(t, filepath.Join(taskDir, "input", "model-turns.json"), "[\"{\\\"type\\\":\\\"act\\\",\\\"command\\\":\\\"printf 'hello\\\\n' > note.txt\\\"}\",\"{\\\"type\\\":\\\"done\\\",\\\"summary\\\":\\\"finished\\\"}\"]\n")
		writeWorkspaceFile(t, filepath.Join(taskDir, "input", "project", "AGENTS.md"), "Keep changes tight. Work in the current directory.\n", 0o644)
		writeWorkspaceFile(t, filepath.Join(taskDir, "expected", "workspace", "AGENTS.md"), "Keep changes tight. Work in the current directory.\n", 0o644)
		writeWorkspaceFile(t, filepath.Join(taskDir, "expected", "workspace", "note.txt"), task.expectedNote, 0o644)
		writeTestFile(t, filepath.Join(taskDir, "task.json"), `{
  "id": "`+task.id+`",
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
}`)
	}

	tasksJSON, err := json.Marshal(tasks)
	if err != nil {
		t.Fatalf("json.Marshal(tasks): %v", err)
	}
	suiteID := filepath.Base(suiteDir)
	suiteJSON := `{
  "id": "` + suiteID + `",
  "description": "task6 temp suite",
  "mode": "mock-provider",
  "trials_per_task": ` + formatInt(spec.trialsPerTask) + `,
  "failure_policy": {
    "stop_on_first_failure": ` + formatBool(spec.failurePolicy.StopOnFirstFailure) + `,
    "max_failed_tasks": ` + formatInt(spec.failurePolicy.MaxFailedTasks) + `
  },
  "tasks": ` + string(tasksJSON) + `
}`
	writeTestFile(t, filepath.Join(suiteDir, "suite.json"), suiteJSON)
	return suiteID
}

func cleanupGeneratedRunOutput(t *testing.T, repoRoot string) {
	t.Helper()

	for _, rel := range []string{"evaluations/trials", "evaluations/reports"} {
		_ = os.RemoveAll(filepath.Join(repoRoot, rel))
	}
}

func formatInt(value int) string {
	return strconv.Itoa(value)
}

func formatBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
