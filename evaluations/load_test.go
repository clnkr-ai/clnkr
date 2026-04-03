package evaluations

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSuite(t *testing.T) {
	t.Run("malformed json", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "suite.json")
		writeTestFile(t, path, "{")

		if _, err := LoadSuite(path); err == nil {
			t.Fatal("LoadSuite() error = nil, want parse failure")
		}
	})

	t.Run("missing required fields", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "suite.json")
		writeTestFile(t, path, `{"id":"default"}`)

		if _, err := LoadSuite(path); err == nil {
			t.Fatal("LoadSuite() error = nil, want validation failure")
		}
	})

	t.Run("invalid mode value", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "suite.json")
		writeTestFile(t, path, `{
  "id": "default",
  "description": "suite",
  "mode": "bogus",
  "trials_per_task": 1,
  "failure_policy": {
    "stop_on_first_failure": true,
    "max_failed_tasks": 1
  },
  "tasks": ["001-basic-edit"]
}`)

		if _, err := LoadSuite(path); err == nil {
			t.Fatal("LoadSuite() error = nil, want invalid mode failure")
		}
	})

	t.Run("loads canonical fixture", func(t *testing.T) {
		got, err := LoadSuite(filepath.Join("suites", "default", "suite.json"))
		if err != nil {
			t.Fatalf("LoadSuite(): %v", err)
		}
		if got.ID != "default" {
			t.Fatalf("suite id = %q, want %q", got.ID, "default")
		}
		if got.Description != "Baseline evaluation suite for clnku" {
			t.Fatalf("suite description = %q, want %q", got.Description, "Baseline evaluation suite for clnku")
		}
		if got.Mode != ModeMockProvider {
			t.Fatalf("suite mode = %q, want %q", got.Mode, ModeMockProvider)
		}
		if got.TrialsPerTask != 1 {
			t.Fatalf("trials_per_task = %d, want 1", got.TrialsPerTask)
		}
		if len(got.Tasks) != 1 || got.Tasks[0] != "001-basic-edit" {
			t.Fatalf("suite tasks = %#v, want [001-basic-edit]", got.Tasks)
		}
		if !got.FailurePolicy.StopOnFirstFailure {
			t.Fatal("failure policy stop_on_first_failure = false, want true")
		}
		if got.FailurePolicy.MaxFailedTasks != 1 {
			t.Fatalf("failure policy max_failed_tasks = %d, want 1", got.FailurePolicy.MaxFailedTasks)
		}
	})
}

func TestLoadTask(t *testing.T) {
	t.Run("malformed json", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "task.json")
		writeTestFile(t, path, "{")

		if _, err := LoadTask(path); err == nil {
			t.Fatal("LoadTask() error = nil, want parse failure")
		}
	})

	t.Run("missing required fields", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "task.json")
		writeTestFile(t, path, `{"id":"001-basic-edit","graders":{"outcome_workspace_snapshot":{"enabled":true,"required":true},"transcript_command_trace":{"enabled":true,"required":false}}}`)

		if _, err := LoadTask(path); err == nil {
			t.Fatal("LoadTask() error = nil, want validation failure")
		}
	})

	t.Run("missing required grader fields", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "task.json")
		writeTestFile(t, path, `{
  "id": "001-basic-edit",
  "instruction_file": "input/instruction.txt",
  "scripted_turns_file": "input/model-turns.json",
  "working_directory": "workspace",
  "full_send": true,
  "step_limit": 10,
  "graders": {
    "outcome_workspace_snapshot": {
      "required": true
    },
    "transcript_command_trace": {
      "enabled": true,
      "required": false
    }
  }
}`)

		if _, err := LoadTask(path); err == nil {
			t.Fatal("LoadTask() error = nil, want grader validation failure")
		}
	})

	t.Run("invalid task mode value", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "task.json")
		writeTestFile(t, path, `{
  "id": "001-basic-edit",
  "instruction_file": "input/instruction.txt",
  "scripted_turns_file": "input/model-turns.json",
  "working_directory": "workspace",
  "mode": "bogus",
  "full_send": true,
  "step_limit": 10,
  "graders": {
    "outcome_workspace_snapshot": {
      "enabled": true,
      "required": true
    },
    "transcript_command_trace": {
      "enabled": true,
      "required": false
    }
  }
}`)

		if _, err := LoadTask(path); err == nil {
			t.Fatal("LoadTask() error = nil, want invalid mode failure")
		}
	})

	t.Run("loads canonical fixture", func(t *testing.T) {
		got, err := LoadTask(filepath.Join("suites", "default", "tasks", "001-basic-edit", "task.json"))
		if err != nil {
			t.Fatalf("LoadTask(): %v", err)
		}
		if got.ID != "001-basic-edit" {
			t.Fatalf("task id = %q, want %q", got.ID, "001-basic-edit")
		}
		if got.InstructionFile != "input/instruction.txt" {
			t.Fatalf("instruction_file = %q, want %q", got.InstructionFile, "input/instruction.txt")
		}
		if got.ScriptedTurnsFile != "input/model-turns.json" {
			t.Fatalf("scripted_turns_file = %q, want %q", got.ScriptedTurnsFile, "input/model-turns.json")
		}
		if got.WorkingDirectory != "workspace" {
			t.Fatalf("working_directory = %q, want %q", got.WorkingDirectory, "workspace")
		}
		if !got.FullSend {
			t.Fatal("full_send = false, want true")
		}
		if got.StepLimit != 10 {
			t.Fatalf("step_limit = %d, want 10", got.StepLimit)
		}
		if !got.Graders.OutcomeWorkspaceSnapshot.Enabled || !got.Graders.OutcomeWorkspaceSnapshot.Required {
			t.Fatalf("outcome_workspace_snapshot = %#v, want enabled+required", got.Graders.OutcomeWorkspaceSnapshot)
		}
		if !got.Graders.TranscriptCommandTrace.Enabled || got.Graders.TranscriptCommandTrace.Required {
			t.Fatalf("transcript_command_trace = %#v, want enabled and not required", got.Graders.TranscriptCommandTrace)
		}
	})
}

func TestLoadSuiteTasks(t *testing.T) {
	t.Run("loads tasks in declared order", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "suite.json"), `{
  "id": "ordered",
  "description": "ordered suite",
  "mode": "mock-provider",
  "trials_per_task": 1,
  "failure_policy": {
    "stop_on_first_failure": true,
    "max_failed_tasks": 1
  },
  "tasks": ["b-task", "a-task"]
}`)
		writeTestFile(t, filepath.Join(root, "tasks", "a-task", "task.json"), `{
  "id": "a-task",
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
      "enabled": true,
      "required": false
    }
  }
}`)
		writeTestFile(t, filepath.Join(root, "tasks", "b-task", "task.json"), `{
  "id": "b-task",
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
      "enabled": true,
      "required": false
    }
  }
}`)

		suite, err := LoadSuite(filepath.Join(root, "suite.json"))
		if err != nil {
			t.Fatalf("LoadSuite(): %v", err)
		}
		got, err := LoadSuiteTasks(root, suite)
		if err != nil {
			t.Fatalf("LoadSuiteTasks(): %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("task count = %d, want 2", len(got))
		}
		if got[0].ID != "b-task" || got[1].ID != "a-task" {
			t.Fatalf("task order = [%q, %q], want [b-task, a-task]", got[0].ID, got[1].ID)
		}
	})

	t.Run("resolves task file under tasks/id", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "suite.json"), `{
  "id": "single",
  "description": "single task suite",
  "mode": "mock-provider",
  "trials_per_task": 1,
  "failure_policy": {
    "stop_on_first_failure": true,
    "max_failed_tasks": 1
  },
  "tasks": ["001-basic-edit"]
}`)
		writeTestFile(t, filepath.Join(root, "tasks", "001-basic-edit", "task.json"), `{
  "id": "001-basic-edit",
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
      "enabled": true,
      "required": false
    }
  }
}`)

		suite, err := LoadSuite(filepath.Join(root, "suite.json"))
		if err != nil {
			t.Fatalf("LoadSuite(): %v", err)
		}
		got, err := LoadSuiteTasks(root, suite)
		if err != nil {
			t.Fatalf("LoadSuiteTasks(): %v", err)
		}
		if len(got) != 1 || got[0].ID != "001-basic-edit" {
			t.Fatalf("tasks = %#v, want one task with id 001-basic-edit", got)
		}
	})

	t.Run("rejects task id mismatch", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "suite.json"), `{
  "id": "mismatch",
  "description": "mismatch suite",
  "mode": "mock-provider",
  "trials_per_task": 1,
  "failure_policy": {
    "stop_on_first_failure": true,
    "max_failed_tasks": 1
  },
  "tasks": ["wrong-id"]
}`)
		writeTestFile(t, filepath.Join(root, "tasks", "wrong-id", "task.json"), `{
  "id": "other-id",
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
      "enabled": true,
      "required": false
    }
  }
}`)

		suite, err := LoadSuite(filepath.Join(root, "suite.json"))
		if err != nil {
			t.Fatalf("LoadSuite(): %v", err)
		}
		if _, err := LoadSuiteTasks(root, suite); err == nil {
			t.Fatal("LoadSuiteTasks() error = nil, want task id mismatch")
		}
	})

	t.Run("rejects duplicate task ids", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "suite.json"), `{
  "id": "duplicates",
  "description": "duplicate suite",
  "mode": "mock-provider",
  "trials_per_task": 1,
  "failure_policy": {
    "stop_on_first_failure": true,
    "max_failed_tasks": 1
  },
  "tasks": ["dup", "dup"]
}`)
		writeTestFile(t, filepath.Join(root, "tasks", "dup", "task.json"), `{
  "id": "dup",
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
      "enabled": true,
      "required": false
    }
  }
}`)

		suite, err := LoadSuite(filepath.Join(root, "suite.json"))
		if err != nil {
			t.Fatalf("LoadSuite(): %v", err)
		}
		if _, err := LoadSuiteTasks(root, suite); err == nil {
			t.Fatal("LoadSuiteTasks() error = nil, want duplicate task id failure")
		}
	})

	t.Run("rejects path escape task ids", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "suite.json"), `{
  "id": "escape",
  "description": "escape suite",
  "mode": "mock-provider",
  "trials_per_task": 1,
  "failure_policy": {
    "stop_on_first_failure": true,
    "max_failed_tasks": 1
  },
  "tasks": ["../escaped"]
}`)

		suite, err := LoadSuite(filepath.Join(root, "suite.json"))
		if err != nil {
			t.Fatalf("LoadSuite(): %v", err)
		}
		if _, err := LoadSuiteTasks(root, suite); err == nil {
			t.Fatal("LoadSuiteTasks() error = nil, want path escape failure")
		}
	})

	t.Run("rejects mock-provider task missing scripted_turns_file when effective mode is mock-provider", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "suite.json"), `{
  "id": "mock-provider",
  "description": "mock-provider suite",
  "mode": "mock-provider",
  "trials_per_task": 1,
  "failure_policy": {
    "stop_on_first_failure": true,
    "max_failed_tasks": 1
  },
  "tasks": ["001-basic-edit"]
}`)
		writeTestFile(t, filepath.Join(root, "tasks", "001-basic-edit", "task.json"), `{
  "id": "001-basic-edit",
  "instruction_file": "input/instruction.txt",
  "working_directory": "workspace",
  "full_send": true,
  "step_limit": 10,
  "graders": {
    "outcome_workspace_snapshot": {
      "enabled": true,
      "required": true
    },
    "transcript_command_trace": {
      "enabled": true,
      "required": false
    }
  }
}`)

		suite, err := LoadSuite(filepath.Join(root, "suite.json"))
		if err != nil {
			t.Fatalf("LoadSuite(): %v", err)
		}
		if _, err := LoadSuiteTasks(root, suite); err == nil {
			t.Fatal("LoadSuiteTasks() error = nil, want scripted_turns_file failure")
		}
	})
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}
