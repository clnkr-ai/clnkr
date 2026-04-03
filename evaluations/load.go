package evaluations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type suiteJSON struct {
	ID            *string            `json:"id"`
	Description   *string            `json:"description"`
	Mode          *string            `json:"mode"`
	TrialsPerTask *int               `json:"trials_per_task"`
	FailurePolicy *failurePolicyJSON `json:"failure_policy"`
	Tasks         []string           `json:"tasks"`
}

type failurePolicyJSON struct {
	StopOnFirstFailure *bool `json:"stop_on_first_failure"`
	MaxFailedTasks     *int  `json:"max_failed_tasks"`
}

type taskJSON struct {
	ID                 *string     `json:"id"`
	InstructionFile    *string     `json:"instruction_file"`
	ScriptedTurnsFile  *string     `json:"scripted_turns_file"`
	WorkingDirectory   *string     `json:"working_directory"`
	StepLimit          *int        `json:"step_limit"`
	FullSend           *bool       `json:"full_send"`
	SeedTranscriptFile *string     `json:"seed_transcript_file"`
	Mode               *string     `json:"mode"`
	Graders            *graderJSON `json:"graders"`
}

type graderJSON struct {
	TranscriptCommandTrace   *transcriptCommandTraceJSON   `json:"transcript_command_trace"`
	OutcomeWorkspaceSnapshot *outcomeWorkspaceSnapshotJSON `json:"outcome_workspace_snapshot"`
}

type transcriptCommandTraceJSON struct {
	Enabled           *bool    `json:"enabled"`
	Required          *bool    `json:"required"`
	ExpectedCommands  []string `json:"expected_commands"`
	ExpectedExitCodes []int    `json:"expected_exit_codes"`
	MaxCommandCount   *int     `json:"max_command_count"`
}

type outcomeWorkspaceSnapshotJSON struct {
	Enabled  *bool `json:"enabled"`
	Required *bool `json:"required"`
}

// LoadSuite loads and validates a suite definition from suite.json or a suite directory.
func LoadSuite(path string) (Suite, error) {
	path = resolveJSONPath(path, "suite.json")

	var raw suiteJSON
	if err := decodeStrictJSONFile(path, &raw); err != nil {
		return Suite{}, fmt.Errorf("load suite %q: %w", path, err)
	}

	suite, err := validateSuiteJSON(path, raw)
	if err != nil {
		return Suite{}, err
	}
	return suite, nil
}

// LoadTask loads and validates a task definition from task.json or a task directory.
func LoadTask(path string) (Task, error) {
	path = resolveJSONPath(path, "task.json")

	var raw taskJSON
	if err := decodeStrictJSONFile(path, &raw); err != nil {
		return Task{}, fmt.Errorf("load task %q: %w", path, err)
	}

	task, err := validateTaskJSON(path, raw)
	if err != nil {
		return Task{}, err
	}
	return task, nil
}

// LoadSuiteTasks loads tasks in the order declared by the suite.
func LoadSuiteTasks(root string, suite Suite) ([]Task, error) {
	suiteRoot := root
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		suiteRoot = filepath.Dir(root)
	}

	tasks := make([]Task, 0, len(suite.Tasks))
	seen := make(map[string]struct{}, len(suite.Tasks))
	for _, taskID := range suite.Tasks {
		taskID = strings.TrimSpace(taskID)
		if err := validateTaskID(suite.ID, taskID); err != nil {
			return nil, err
		}
		if _, ok := seen[taskID]; ok {
			return nil, fmt.Errorf("suite %q has duplicate task id %q", suite.ID, taskID)
		}
		seen[taskID] = struct{}{}

		taskPath := filepath.Join(suiteRoot, "tasks", taskID, "task.json")
		task, err := LoadTask(taskPath)
		if err != nil {
			return nil, fmt.Errorf("load task %q for suite %q: %w", taskID, suite.ID, err)
		}
		if task.ID != taskID {
			return nil, fmt.Errorf("task %q at %q has id %q", taskID, taskPath, task.ID)
		}
		effectiveMode := suite.Mode
		if task.Mode != "" {
			effectiveMode = task.Mode
		}
		if effectiveMode == ModeMockProvider && strings.TrimSpace(task.ScriptedTurnsFile) == "" {
			return nil, fmt.Errorf("task %q in suite %q requires scripted_turns_file for mock-provider mode", taskID, suite.ID)
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func validateTaskID(suiteID, taskID string) error {
	if taskID == "" {
		return fmt.Errorf("suite %q has empty task id", suiteID)
	}
	if taskID == "." || taskID == ".." {
		return fmt.Errorf("suite %q has invalid task id %q", suiteID, taskID)
	}
	if strings.ContainsAny(taskID, `/\`) {
		return fmt.Errorf("suite %q has invalid task id %q", suiteID, taskID)
	}
	if filepath.Base(taskID) != taskID {
		return fmt.Errorf("suite %q has invalid task id %q", suiteID, taskID)
	}
	return nil
}

func validateSuiteJSON(path string, raw suiteJSON) (Suite, error) {
	id, err := requiredString(path, "id", raw.ID)
	if err != nil {
		return Suite{}, err
	}
	description, err := requiredString(path, "description", raw.Description)
	if err != nil {
		return Suite{}, err
	}
	mode, err := requiredMode(path, "mode", raw.Mode)
	if err != nil {
		return Suite{}, err
	}
	trialsPerTask, err := requiredPositiveInt(path, "trials_per_task", raw.TrialsPerTask)
	if err != nil {
		return Suite{}, err
	}
	if len(raw.Tasks) == 0 {
		return Suite{}, fmt.Errorf("%s: missing required field %q", path, "tasks")
	}
	for i, taskID := range raw.Tasks {
		if strings.TrimSpace(taskID) == "" {
			return Suite{}, fmt.Errorf("%s: tasks[%d] must be non-empty", path, i)
		}
	}
	if raw.FailurePolicy == nil {
		return Suite{}, fmt.Errorf("%s: missing required field %q", path, "failure_policy")
	}
	failurePolicy, err := validateFailurePolicyJSON(path, *raw.FailurePolicy)
	if err != nil {
		return Suite{}, err
	}

	return Suite{
		ID:            id,
		Description:   description,
		Mode:          mode,
		TrialsPerTask: trialsPerTask,
		Tasks:         append([]string(nil), raw.Tasks...),
		FailurePolicy: failurePolicy,
	}, nil
}

func validateFailurePolicyJSON(path string, raw failurePolicyJSON) (FailurePolicy, error) {
	stopOnFirstFailure, err := requiredBool(path, "failure_policy.stop_on_first_failure", raw.StopOnFirstFailure)
	if err != nil {
		return FailurePolicy{}, err
	}
	maxFailedTasks, err := requiredPositiveInt(path, "failure_policy.max_failed_tasks", raw.MaxFailedTasks)
	if err != nil {
		return FailurePolicy{}, err
	}
	return FailurePolicy{
		StopOnFirstFailure: stopOnFirstFailure,
		MaxFailedTasks:     maxFailedTasks,
	}, nil
}

func validateTaskJSON(path string, raw taskJSON) (Task, error) {
	id, err := requiredString(path, "id", raw.ID)
	if err != nil {
		return Task{}, err
	}
	instructionFile, err := requiredString(path, "instruction_file", raw.InstructionFile)
	if err != nil {
		return Task{}, err
	}
	workingDirectory, err := requiredString(path, "working_directory", raw.WorkingDirectory)
	if err != nil {
		return Task{}, err
	}
	stepLimit, err := requiredPositiveInt(path, "step_limit", raw.StepLimit)
	if err != nil {
		return Task{}, err
	}
	if raw.FullSend == nil {
		return Task{}, fmt.Errorf("%s: missing required field %q", path, "full_send")
	}
	if raw.Graders == nil {
		return Task{}, fmt.Errorf("%s: missing required field %q", path, "graders")
	}
	graders, err := validateGradersJSON(path, *raw.Graders)
	if err != nil {
		return Task{}, err
	}

	task := Task{
		ID:               id,
		InstructionFile:  instructionFile,
		WorkingDirectory: workingDirectory,
		StepLimit:        stepLimit,
		FullSend:         *raw.FullSend,
		Graders:          graders,
	}

	if raw.SeedTranscriptFile != nil {
		seedTranscriptFile := strings.TrimSpace(*raw.SeedTranscriptFile)
		if seedTranscriptFile == "" {
			return Task{}, fmt.Errorf("%s: seed_transcript_file must be non-empty when present", path)
		}
		task.SeedTranscriptFile = seedTranscriptFile
	}
	if raw.Mode != nil {
		mode, err := requiredMode(path, "mode", raw.Mode)
		if err != nil {
			return Task{}, err
		}
		task.Mode = mode
	}
	if raw.ScriptedTurnsFile != nil {
		scriptedTurnsFile := strings.TrimSpace(*raw.ScriptedTurnsFile)
		if scriptedTurnsFile == "" {
			return Task{}, fmt.Errorf("%s: scripted_turns_file must be non-empty when present", path)
		}
		task.ScriptedTurnsFile = scriptedTurnsFile
	}
	if task.Mode == ModeMockProvider && task.ScriptedTurnsFile == "" {
		return Task{}, fmt.Errorf("%s: scripted_turns_file is required when mode is mock-provider", path)
	}
	return task, nil
}

func validateGradersJSON(path string, raw graderJSON) (GraderConfig, error) {
	outcomeWorkspaceSnapshot, err := validateOutcomeWorkspaceSnapshotJSON(path, raw.OutcomeWorkspaceSnapshot)
	if err != nil {
		return GraderConfig{}, err
	}
	transcriptCommandTrace, err := validateTranscriptCommandTraceJSON(path, raw.TranscriptCommandTrace)
	if err != nil {
		return GraderConfig{}, err
	}
	return GraderConfig{
		OutcomeWorkspaceSnapshot: outcomeWorkspaceSnapshot,
		TranscriptCommandTrace:   transcriptCommandTrace,
	}, nil
}

func validateOutcomeWorkspaceSnapshotJSON(path string, raw *outcomeWorkspaceSnapshotJSON) (OutcomeWorkspaceSnapshotConfig, error) {
	if raw == nil {
		return OutcomeWorkspaceSnapshotConfig{}, fmt.Errorf("%s: missing required grader %q", path, "outcome_workspace_snapshot")
	}
	enabled, err := requiredBool(path, "graders.outcome_workspace_snapshot.enabled", raw.Enabled)
	if err != nil {
		return OutcomeWorkspaceSnapshotConfig{}, err
	}
	required, err := requiredBool(path, "graders.outcome_workspace_snapshot.required", raw.Required)
	if err != nil {
		return OutcomeWorkspaceSnapshotConfig{}, err
	}
	return OutcomeWorkspaceSnapshotConfig{
		Enabled:  enabled,
		Required: required,
	}, nil
}

func validateTranscriptCommandTraceJSON(path string, raw *transcriptCommandTraceJSON) (TranscriptCommandTraceConfig, error) {
	if raw == nil {
		return TranscriptCommandTraceConfig{}, fmt.Errorf("%s: missing required grader %q", path, "transcript_command_trace")
	}
	enabled, err := requiredBool(path, "graders.transcript_command_trace.enabled", raw.Enabled)
	if err != nil {
		return TranscriptCommandTraceConfig{}, err
	}
	required, err := requiredBool(path, "graders.transcript_command_trace.required", raw.Required)
	if err != nil {
		return TranscriptCommandTraceConfig{}, err
	}
	maxCommandCount := 0
	if raw.MaxCommandCount != nil {
		if *raw.MaxCommandCount <= 0 {
			return TranscriptCommandTraceConfig{}, fmt.Errorf("%s: graders.transcript_command_trace.max_command_count must be > 0", path)
		}
		maxCommandCount = *raw.MaxCommandCount
	}
	return TranscriptCommandTraceConfig{
		Enabled:           enabled,
		Required:          required,
		ExpectedCommands:  append([]string(nil), raw.ExpectedCommands...),
		ExpectedExitCodes: append([]int(nil), raw.ExpectedExitCodes...),
		MaxCommandCount:   maxCommandCount,
	}, nil
}

func decodeStrictJSONFile(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decode %q: %w", path, err)
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode %q: unexpected trailing JSON", path)
		}
		return fmt.Errorf("decode %q: %w", path, err)
	}
	return nil
}

func resolveJSONPath(path, filename string) string {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return filepath.Join(path, filename)
	}
	return path
}

func requiredString(path, field string, value *string) (string, error) {
	if value == nil {
		return "", fmt.Errorf("%s: missing required field %q", path, field)
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return "", fmt.Errorf("%s: field %q must be non-empty", path, field)
	}
	return trimmed, nil
}

func requiredMode(path, field string, value *string) (Mode, error) {
	str, err := requiredString(path, field, value)
	if err != nil {
		return "", err
	}
	mode := Mode(str)
	switch mode {
	case ModeMockProvider, ModeLiveProvider:
		return mode, nil
	default:
		return "", fmt.Errorf("%s: field %q must be %q or %q, got %q", path, field, ModeMockProvider, ModeLiveProvider, mode)
	}
}

func requiredPositiveInt(path, field string, value *int) (int, error) {
	if value == nil {
		return 0, fmt.Errorf("%s: missing required field %q", path, field)
	}
	if *value <= 0 {
		return 0, fmt.Errorf("%s: field %q must be > 0", path, field)
	}
	return *value, nil
}

func requiredBool(path, field string, value *bool) (bool, error) {
	if value == nil {
		return false, fmt.Errorf("%s: missing required field %q", path, field)
	}
	return *value, nil
}
