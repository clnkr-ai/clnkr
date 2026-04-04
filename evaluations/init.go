package evaluations

import (
	"fmt"
	"os"
	"path/filepath"
)

const defaultSuiteJSON = `{
  "id": "default",
  "description": "Default evaluation suite",
  "mode": "live-provider",
  "trials_per_task": 1,
  "failure_policy": {
    "stop_on_first_failure": false,
    "max_failed_tasks": 10
  },
  "tasks": [
    "001-example"
  ]
}
`

const defaultTaskJSON = `{
  "id": "001-example",
  "instruction_file": "input/instruction.txt",
  "working_directory": ".",
  "full_send": true,
  "step_limit": 30,
  "mode": "live-provider",
  "graders": {
    "outcome_diff": {
      "enabled": true,
      "required": true
    }
  }
}
`

const defaultInstruction = `Fix the failing test.
`

// Init scaffolds an evaluations directory with a default suite and example task.
func Init(evalsDir string) error {
	dirs := []string{
		filepath.Join(evalsDir, "suites", "default", "tasks", "001-example", "input"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("init create dir %q: %w", dir, err)
		}
	}

	files := []struct {
		path    string
		content string
	}{
		{filepath.Join(evalsDir, "suites", "default", "suite.json"), defaultSuiteJSON},
		{filepath.Join(evalsDir, "suites", "default", "tasks", "001-example", "task.json"), defaultTaskJSON},
		{filepath.Join(evalsDir, "suites", "default", "tasks", "001-example", "input", "instruction.txt"), defaultInstruction},
	}
	for _, f := range files {
		if err := os.WriteFile(f.path, []byte(f.content), 0o644); err != nil {
			return fmt.Errorf("init write %q: %w", f.path, err)
		}
	}

	return nil
}
