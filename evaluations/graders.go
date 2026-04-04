package evaluations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	outcomeWorkspaceSnapshotGraderID = "outcome_workspace_snapshot"
	outcomeDiffGraderID              = "outcome_diff"
	transcriptCommandTraceGraderID   = "transcript_command_trace"
	outcomeCommandOutputGraderID     = "outcome_command_output"

	graderTargetOutcome    = "outcome"
	graderTargetTranscript = "transcript"
)

// WorkspaceSnapshotMismatch describes one file-level mismatch in the outcome workspace.
type WorkspaceSnapshotMismatch struct {
	Path              string `json:"path"`
	ExpectedSizeBytes int64  `json:"expected_size_bytes"`
	ExpectedSHA256    string `json:"expected_sha256"`
	ActualSizeBytes   int64  `json:"actual_size_bytes"`
	ActualSHA256      string `json:"actual_sha256"`
}

// WorkspaceSnapshotEvidence captures structured file-level diffs for the outcome grader.
type WorkspaceSnapshotEvidence struct {
	Missing    []string                    `json:"missing,omitempty"`
	Unexpected []string                    `json:"unexpected,omitempty"`
	Mismatched []WorkspaceSnapshotMismatch `json:"mismatched,omitempty"`
}

// TranscriptCommandTraceEvidence captures the command trace used by the transcript grader.
type TranscriptCommandTraceEvidence struct {
	Mode              string   `json:"mode"`
	Commands          []string `json:"commands,omitempty"`
	ExitCodes         []int    `json:"exit_codes,omitempty"`
	ExpectedCommands  []string `json:"expected_commands,omitempty"`
	ExpectedExitCodes []int    `json:"expected_exit_codes,omitempty"`
	MaxCommandCount   int      `json:"max_command_count,omitempty"`
}

// GradeOutcomeWorkspaceSnapshot compares the materialized workspace against expected/workspace.
func GradeOutcomeWorkspaceSnapshot(task Task, artifacts RunArtifacts) (GraderResult, error) {
	expectedRoot := filepath.Join(artifacts.TaskRoot, "expected", "workspace")
	expectedFiles, err := readWorkspaceSnapshot(expectedRoot)
	if err != nil {
		return GraderResult{}, fmt.Errorf("grade outcome workspace snapshot: %w", err)
	}
	actualFiles := normalizeWorkspaceFiles(artifacts.Workspace)

	result := GraderResult{
		GraderID:   outcomeWorkspaceSnapshotGraderID,
		TargetKind: graderTargetOutcome,
		Passed:     true,
	}
	evidence, message := compareWorkspaceSnapshots(expectedFiles, actualFiles)
	result.Evidence = evidence
	result.Message = message
	if len(evidence.Missing) > 0 || len(evidence.Unexpected) > 0 || len(evidence.Mismatched) > 0 {
		result.Passed = false
	}
	return result, nil
}

// OutcomeDiffEvidence captures the git diff produced by the agent.
type OutcomeDiffEvidence struct {
	DiffSize int  `json:"diff_size"`
	HasDiff  bool `json:"has_diff"`
}

// GradeOutcomeDiff checks that the agent produced a non-empty git diff.
func GradeOutcomeDiff(task Task, artifacts RunArtifacts) (GraderResult, error) {
	diff := strings.TrimSpace(artifacts.GitDiff)
	evidence := OutcomeDiffEvidence{
		DiffSize: len(diff),
		HasDiff:  diff != "",
	}

	result := GraderResult{
		GraderID:   outcomeDiffGraderID,
		TargetKind: graderTargetOutcome,
		Passed:     diff != "",
		Evidence:   evidence,
	}
	if diff == "" {
		result.Message = "agent produced no diff"
	} else {
		result.Message = fmt.Sprintf("agent produced %d byte diff", len(diff))
	}
	return result, nil
}

// GradeTranscriptCommandTrace compares the command lifecycle trace against task configuration.
func GradeTranscriptCommandTrace(task Task, artifacts RunArtifacts) (GraderResult, error) {
	_, dones, err := parseCommandLifecycleEvents(artifacts.EventLog)
	if err != nil {
		return GraderResult{}, fmt.Errorf("grade transcript command trace: %w", err)
	}

	commands := make([]string, 0, len(dones))
	for _, done := range dones {
		commands = append(commands, done.Command)
	}
	exitCodes := make([]int, 0, len(dones))
	for _, done := range dones {
		exitCodes = append(exitCodes, done.ExitCode)
	}

	evidence := TranscriptCommandTraceEvidence{
		Mode:              string(artifacts.Mode),
		Commands:          append([]string(nil), commands...),
		ExitCodes:         append([]int(nil), exitCodes...),
		ExpectedCommands:  append([]string(nil), task.Graders.TranscriptCommandTrace.ExpectedCommands...),
		ExpectedExitCodes: append([]int(nil), task.Graders.TranscriptCommandTrace.ExpectedExitCodes...),
		MaxCommandCount:   task.Graders.TranscriptCommandTrace.MaxCommandCount,
	}

	result := GraderResult{
		GraderID:   transcriptCommandTraceGraderID,
		TargetKind: graderTargetTranscript,
		Passed:     true,
		Evidence:   evidence,
	}

	switch artifacts.Mode {
	case ModeMockProvider:
		if len(commands) != len(task.Graders.TranscriptCommandTrace.ExpectedCommands) {
			result.Passed = false
			result.Message = fmt.Sprintf("command count = %d, want %d", len(commands), len(task.Graders.TranscriptCommandTrace.ExpectedCommands))
			return result, nil
		}
		for i, want := range task.Graders.TranscriptCommandTrace.ExpectedCommands {
			if commands[i] != want {
				result.Passed = false
				result.Message = fmt.Sprintf("command[%d] = %q, want %q", i, commands[i], want)
				return result, nil
			}
		}
		if len(exitCodes) != len(task.Graders.TranscriptCommandTrace.ExpectedExitCodes) {
			result.Passed = false
			result.Message = fmt.Sprintf("exit code count = %d, want %d", len(exitCodes), len(task.Graders.TranscriptCommandTrace.ExpectedExitCodes))
			return result, nil
		}
		for i, want := range task.Graders.TranscriptCommandTrace.ExpectedExitCodes {
			if exitCodes[i] != want {
				result.Passed = false
				result.Message = fmt.Sprintf("exit code[%d] = %d, want %d", i, exitCodes[i], want)
				return result, nil
			}
		}
	case ModeLiveProvider:
		if task.Graders.TranscriptCommandTrace.MaxCommandCount > 0 && len(commands) > task.Graders.TranscriptCommandTrace.MaxCommandCount {
			result.Passed = false
			result.Message = fmt.Sprintf("max command count = %d, got %d", task.Graders.TranscriptCommandTrace.MaxCommandCount, len(commands))
			return result, nil
		}
		if len(task.Graders.TranscriptCommandTrace.ExpectedExitCodes) > 0 {
			allowed := make(map[int]struct{}, len(task.Graders.TranscriptCommandTrace.ExpectedExitCodes))
			for _, exitCode := range task.Graders.TranscriptCommandTrace.ExpectedExitCodes {
				allowed[exitCode] = struct{}{}
			}
			for i, exitCode := range exitCodes {
				if _, ok := allowed[exitCode]; !ok {
					result.Passed = false
					result.Message = fmt.Sprintf("unexpected exit code[%d] = %d", i, exitCode)
					return result, nil
				}
			}
		}
	default:
		return GraderResult{}, fmt.Errorf("grade transcript command trace: unsupported mode %q", artifacts.Mode)
	}

	result.Message = "command trace matches"
	return result, nil
}

// CommandOutputEvidence captures the command execution result for the outcome command grader.
type CommandOutputEvidence struct {
	Command          []string `json:"command"`
	ExpandedCommand  []string `json:"expanded_command"`
	ExitCode         int      `json:"exit_code"`
	ExpectedExitCode int      `json:"expected_exit_code"`
	Stdout           string   `json:"stdout"`
	Stderr           string   `json:"stderr"`
	TimedOut         bool     `json:"timed_out,omitempty"`
}

// GradeOutcomeCommandOutput runs a command against the workspace and checks its output.
func GradeOutcomeCommandOutput(ctx context.Context, task Task, artifacts RunArtifacts) (GraderResult, error) {
	cfg := task.Graders.OutcomeCommandOutput
	if len(cfg.Command) == 0 {
		return GraderResult{}, fmt.Errorf("grade outcome command output: command is empty")
	}

	// Expand {{workspace}} template variable in all command arguments.
	expanded := make([]string, len(cfg.Command))
	for i, arg := range cfg.Command {
		expanded[i] = strings.ReplaceAll(arg, "{{workspace}}", artifacts.WorkspaceRoot)
	}

	// Apply timeout.
	timeout := 30 * time.Second
	if cfg.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, expanded[0], expanded[1:]...)
	cmd.Dir = artifacts.WorkspaceRoot

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	exitCode := 0
	timedOut := false
	if runErr != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			timedOut = true
			exitCode = -1
		} else {
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				return GraderResult{}, fmt.Errorf("grade outcome command output: %w", runErr)
			}
		}
	}

	evidence := CommandOutputEvidence{
		Command:          append([]string(nil), cfg.Command...),
		ExpandedCommand:  expanded,
		ExitCode:         exitCode,
		ExpectedExitCode: cfg.ExpectedExitCode,
		Stdout:           stdout.String(),
		Stderr:           stderr.String(),
		TimedOut:         timedOut,
	}

	result := GraderResult{
		GraderID:   outcomeCommandOutputGraderID,
		TargetKind: graderTargetOutcome,
		Passed:     true,
		Evidence:   evidence,
	}

	// Check: timeout
	if timedOut {
		result.Passed = false
		result.Message = fmt.Sprintf("command timed out after %s", timeout)
		return result, nil
	}

	// Check: exit code
	if exitCode != cfg.ExpectedExitCode {
		result.Passed = false
		result.Message = fmt.Sprintf("exit code = %d, want %d", exitCode, cfg.ExpectedExitCode)
		return result, nil
	}

	// Check: stdout contains
	stdoutStr := stdout.String()
	for _, pattern := range cfg.StdoutContains {
		if !strings.Contains(stdoutStr, pattern) {
			result.Passed = false
			result.Message = fmt.Sprintf("stdout does not contain %q", pattern)
			return result, nil
		}
	}

	// Check: stderr must not contain
	stderrStr := stderr.String()
	for _, pattern := range cfg.StderrMustNotContain {
		if strings.Contains(stderrStr, pattern) {
			result.Passed = false
			result.Message = fmt.Sprintf("stderr contains forbidden pattern %q", pattern)
			return result, nil
		}
	}

	result.Message = "command output matches"
	return result, nil
}

// EvaluateTaskPassPolicy applies the first-wave required-grader pass policy.
func EvaluateTaskPassPolicy(task Task, graderResults []GraderResult) TrialPolicyResult {
	resultsByID := make(map[string]GraderResult, len(graderResults))
	for _, result := range graderResults {
		resultsByID[result.GraderID] = result
	}

	failed := make([]GraderResult, 0, 3)
	for _, spec := range []struct {
		id       string
		target   string
		enabled  bool
		required bool
	}{
		{
			id:       outcomeWorkspaceSnapshotGraderID,
			target:   graderTargetOutcome,
			enabled:  task.Graders.OutcomeWorkspaceSnapshot.Enabled,
			required: task.Graders.OutcomeWorkspaceSnapshot.Required,
		},
		{
			id:       outcomeDiffGraderID,
			target:   graderTargetOutcome,
			enabled:  task.Graders.OutcomeDiff.Enabled,
			required: task.Graders.OutcomeDiff.Required,
		},
		{
			id:       transcriptCommandTraceGraderID,
			target:   graderTargetTranscript,
			enabled:  task.Graders.TranscriptCommandTrace.Enabled,
			required: task.Graders.TranscriptCommandTrace.Required,
		},
		{
			id:       outcomeCommandOutputGraderID,
			target:   graderTargetOutcome,
			enabled:  task.Graders.OutcomeCommandOutput.Enabled,
			required: task.Graders.OutcomeCommandOutput.Required,
		},
	} {
		if !spec.enabled || !spec.required {
			continue
		}
		result, ok := resultsByID[spec.id]
		if !ok {
			failed = append(failed, GraderResult{
				GraderID:   spec.id,
				TargetKind: spec.target,
				Passed:     false,
				Message:    "required grader did not run",
			})
			continue
		}
		if !result.Passed {
			failed = append(failed, result)
		}
	}

	return TrialPolicyResult{
		Passed:                len(failed) == 0,
		FailedRequiredGraders: failed,
	}
}

func runTrialGraders(ctx context.Context, task Task, artifacts RunArtifacts) ([]GraderResult, TrialPolicyResult, error) {
	results := make([]GraderResult, 0, 3)
	if task.Graders.OutcomeWorkspaceSnapshot.Enabled {
		result, err := GradeOutcomeWorkspaceSnapshot(task, artifacts)
		if err != nil {
			return nil, TrialPolicyResult{}, err
		}
		results = append(results, result)
	}
	if task.Graders.OutcomeDiff.Enabled {
		result, err := GradeOutcomeDiff(task, artifacts)
		if err != nil {
			return nil, TrialPolicyResult{}, err
		}
		results = append(results, result)
	}
	if task.Graders.TranscriptCommandTrace.Enabled {
		result, err := GradeTranscriptCommandTrace(task, artifacts)
		if err != nil {
			return nil, TrialPolicyResult{}, err
		}
		results = append(results, result)
	}
	if task.Graders.OutcomeCommandOutput.Enabled {
		result, err := GradeOutcomeCommandOutput(ctx, task, artifacts)
		if err != nil {
			return nil, TrialPolicyResult{}, err
		}
		results = append(results, result)
	}
	return results, EvaluateTaskPassPolicy(task, results), nil
}

func compareWorkspaceSnapshots(expectedFiles, actualFiles map[string]workspaceFileSnapshot) (WorkspaceSnapshotEvidence, string) {
	evidence := WorkspaceSnapshotEvidence{}

	expectedPaths := sortedMapKeys(expectedFiles)
	actualPaths := sortedMapKeys(actualFiles)
	expectedSet := make(map[string]struct{}, len(expectedFiles))
	for _, path := range expectedPaths {
		expectedSet[path] = struct{}{}
	}

	for _, path := range expectedPaths {
		expected := expectedFiles[path]
		actual, ok := actualFiles[path]
		if !ok {
			evidence.Missing = append(evidence.Missing, path)
			continue
		}
		if expected.SizeBytes != actual.SizeBytes || expected.SHA256 != actual.SHA256 {
			evidence.Mismatched = append(evidence.Mismatched, WorkspaceSnapshotMismatch{
				Path:              path,
				ExpectedSizeBytes: expected.SizeBytes,
				ExpectedSHA256:    expected.SHA256,
				ActualSizeBytes:   actual.SizeBytes,
				ActualSHA256:      actual.SHA256,
			})
		}
	}

	for _, path := range actualPaths {
		if _, ok := expectedSet[path]; !ok {
			evidence.Unexpected = append(evidence.Unexpected, path)
		}
	}

	if len(evidence.Missing) == 0 && len(evidence.Unexpected) == 0 && len(evidence.Mismatched) == 0 {
		return evidence, "workspace snapshot matches expected/workspace"
	}

	parts := make([]string, 0, 3)
	if len(evidence.Missing) > 0 {
		parts = append(parts, "missing files: "+strings.Join(evidence.Missing, ", "))
	}
	if len(evidence.Unexpected) > 0 {
		parts = append(parts, "unexpected files: "+strings.Join(evidence.Unexpected, ", "))
	}
	if len(evidence.Mismatched) > 0 {
		paths := make([]string, 0, len(evidence.Mismatched))
		for _, mismatch := range evidence.Mismatched {
			paths = append(paths, mismatch.Path)
		}
		parts = append(parts, "mismatched files: "+strings.Join(paths, ", "))
	}
	return evidence, strings.Join(parts, "; ")
}

type workspaceFileSnapshot struct {
	SizeBytes int64
	SHA256    string
}

func readWorkspaceSnapshot(root string) (map[string]workspaceFileSnapshot, error) {
	files := make(map[string]workspaceFileSnapshot)
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return files, nil
		}
		return nil, fmt.Errorf("stat expected workspace %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("expected workspace %q is not a directory", root)
	}

	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = workspaceFileSnapshot{
			SizeBytes: int64(len(data)),
			SHA256:    checksumSHA256Bytes(data),
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walk expected workspace %q: %w", root, err)
	}
	return files, nil
}

func normalizeWorkspaceFiles(workspace map[string]string) map[string]workspaceFileSnapshot {
	files := make(map[string]workspaceFileSnapshot, len(workspace))
	for path, content := range workspace {
		files[filepath.ToSlash(path)] = workspaceFileSnapshot{
			SizeBytes: int64(len(content)),
			SHA256:    checksumSHA256String(content),
		}
	}
	return files
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
