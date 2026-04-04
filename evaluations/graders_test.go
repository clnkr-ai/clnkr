package evaluations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutcomeWorkspaceSnapshot(t *testing.T) {
	t.Run("matches expected files by relative path and bytes only", func(t *testing.T) {
		taskRoot := t.TempDir()
		writeWorkspaceFile(t, filepath.Join(taskRoot, "expected", "workspace", "note.txt"), "hello\n", 0o755)
		if err := os.MkdirAll(filepath.Join(taskRoot, "expected", "workspace", "empty", "nested"), 0o755); err != nil {
			t.Fatalf("MkdirAll(empty dirs): %v", err)
		}

		result, err := GradeOutcomeWorkspaceSnapshot(Task{}, RunArtifacts{
			TaskRoot: taskRoot,
			Workspace: map[string]string{
				"note.txt": "hello\n",
			},
		})
		if err != nil {
			t.Fatalf("GradeOutcomeWorkspaceSnapshot(): %v", err)
		}
		if result.GraderID != outcomeWorkspaceSnapshotGraderID {
			t.Fatalf("grader id = %q, want %q", result.GraderID, outcomeWorkspaceSnapshotGraderID)
		}
		if result.TargetKind != graderTargetOutcome {
			t.Fatalf("target kind = %q, want %q", result.TargetKind, graderTargetOutcome)
		}
		if !result.Passed {
			t.Fatalf("passed = false, want true: %#v", result)
		}
		evidence, ok := result.Evidence.(WorkspaceSnapshotEvidence)
		if !ok {
			t.Fatalf("evidence type = %T, want WorkspaceSnapshotEvidence", result.Evidence)
		}
		if len(evidence.Missing) != 0 || len(evidence.Unexpected) != 0 || len(evidence.Mismatched) != 0 {
			t.Fatalf("evidence = %#v, want no diffs", evidence)
		}
	})

	t.Run("reports missing, unexpected, and mismatched files", func(t *testing.T) {
		taskRoot := t.TempDir()
		writeWorkspaceFile(t, filepath.Join(taskRoot, "expected", "workspace", "keep.txt"), "keep\n", 0o644)
		writeWorkspaceFile(t, filepath.Join(taskRoot, "expected", "workspace", "mismatch.txt"), "expected\n", 0o644)
		if err := os.MkdirAll(filepath.Join(taskRoot, "expected", "workspace", "empty", "dir"), 0o755); err != nil {
			t.Fatalf("MkdirAll(empty dirs): %v", err)
		}

		result, err := GradeOutcomeWorkspaceSnapshot(Task{}, RunArtifacts{
			TaskRoot: taskRoot,
			Workspace: map[string]string{
				"mismatch.txt": "actual\n",
				"extra.txt":    "extra\n",
			},
		})
		if err != nil {
			t.Fatalf("GradeOutcomeWorkspaceSnapshot(): %v", err)
		}
		if result.Passed {
			t.Fatalf("passed = true, want false: %#v", result)
		}
		evidence, ok := result.Evidence.(WorkspaceSnapshotEvidence)
		if !ok {
			t.Fatalf("evidence type = %T, want WorkspaceSnapshotEvidence", result.Evidence)
		}
		if got, want := strings.Join(evidence.Missing, ","), "keep.txt"; got != want {
			t.Fatalf("missing = %q, want %q", got, want)
		}
		if got, want := strings.Join(evidence.Unexpected, ","), "extra.txt"; got != want {
			t.Fatalf("unexpected = %q, want %q", got, want)
		}
		if len(evidence.Mismatched) != 1 || evidence.Mismatched[0].Path != "mismatch.txt" {
			t.Fatalf("mismatched = %#v, want one mismatch for mismatch.txt", evidence.Mismatched)
		}
		if evidence.Mismatched[0].ExpectedSHA256 == evidence.Mismatched[0].ActualSHA256 {
			t.Fatalf("mismatch sha256 should differ: %#v", evidence.Mismatched[0])
		}
		if !strings.Contains(result.Message, "missing files") || !strings.Contains(result.Message, "unexpected files") || !strings.Contains(result.Message, "mismatched files") {
			t.Fatalf("message = %q, want all diff categories", result.Message)
		}
	})
}

func TestTranscriptCommandTrace(t *testing.T) {
	t.Run("mock-provider mode requires exact command and exit-code sequences", func(t *testing.T) {
		task := graderTask(true, true, true, false, []string{"printf 'hello\\n' > note.txt"}, []int{0}, 0)
		result, err := GradeTranscriptCommandTrace(task, RunArtifacts{
			Mode:     ModeMockProvider,
			EventLog: traceEventLog(t, []string{"printf 'hello\\n' > note.txt"}, []int{0}),
		})
		if err != nil {
			t.Fatalf("GradeTranscriptCommandTrace(): %v", err)
		}
		if !result.Passed {
			t.Fatalf("passed = false, want true: %#v", result)
		}
		if result.TargetKind != graderTargetTranscript {
			t.Fatalf("target kind = %q, want %q", result.TargetKind, graderTargetTranscript)
		}
		evidence, ok := result.Evidence.(TranscriptCommandTraceEvidence)
		if !ok {
			t.Fatalf("evidence type = %T, want TranscriptCommandTraceEvidence", result.Evidence)
		}
		if got, want := strings.Join(evidence.Commands, ","), "printf 'hello\\n' > note.txt"; got != want {
			t.Fatalf("commands = %q, want %q", got, want)
		}
	})

	t.Run("mock-provider mode ignores command_start text and uses command_done payloads", func(t *testing.T) {
		task := graderTask(true, true, true, false, []string{"printf hi > note.txt"}, []int{0}, 0)
		result, err := GradeTranscriptCommandTrace(task, RunArtifacts{
			Mode: ModeMockProvider,
			EventLog: jsonLine(t, eventEnvelope{
				Type: "command_start",
				Payload: json.RawMessage(mustJSON(t, commandStartEvent{
					Command: "pwd",
					Dir:     "/tmp/work",
				})),
			}) + jsonLine(t, eventEnvelope{
				Type: "command_done",
				Payload: json.RawMessage(mustJSON(t, commandDoneEvent{
					Command:  "printf hi > note.txt",
					ExitCode: 0,
				})),
			}),
		})
		if err != nil {
			t.Fatalf("GradeTranscriptCommandTrace(): %v", err)
		}
		if !result.Passed {
			t.Fatalf("passed = false, want true: %#v", result)
		}
		evidence, ok := result.Evidence.(TranscriptCommandTraceEvidence)
		if !ok {
			t.Fatalf("evidence type = %T, want TranscriptCommandTraceEvidence", result.Evidence)
		}
		if got, want := strings.Join(evidence.Commands, ","), "printf hi > note.txt"; got != want {
			t.Fatalf("commands = %q, want %q", got, want)
		}
	})

	t.Run("mock-provider mode rejects command mismatches", func(t *testing.T) {
		task := graderTask(true, true, true, false, []string{"printf 'hello\\n' > note.txt"}, []int{0}, 0)
		result, err := GradeTranscriptCommandTrace(task, RunArtifacts{
			Mode:     ModeMockProvider,
			EventLog: traceEventLog(t, []string{"printf 'hello\\n' > ./note.txt"}, []int{0}),
		})
		if err != nil {
			t.Fatalf("GradeTranscriptCommandTrace(): %v", err)
		}
		if result.Passed {
			t.Fatalf("passed = true, want false: %#v", result)
		}
		if !strings.Contains(result.Message, "command[0]") {
			t.Fatalf("message = %q, want command mismatch", result.Message)
		}
	})

	t.Run("mock-provider mode rejects exit-code mismatches", func(t *testing.T) {
		task := graderTask(true, true, true, false, []string{"printf 'hello\\n' > note.txt"}, []int{0}, 0)
		result, err := GradeTranscriptCommandTrace(task, RunArtifacts{
			Mode:     ModeMockProvider,
			EventLog: traceEventLog(t, []string{"printf 'hello\\n' > note.txt"}, []int{1}),
		})
		if err != nil {
			t.Fatalf("GradeTranscriptCommandTrace(): %v", err)
		}
		if result.Passed {
			t.Fatalf("passed = true, want false: %#v", result)
		}
		if !strings.Contains(result.Message, "exit code[0]") {
			t.Fatalf("message = %q, want exit code mismatch", result.Message)
		}
	})

	t.Run("mock-provider mode rejects extra commands", func(t *testing.T) {
		task := graderTask(true, true, true, false, []string{"printf 'hello\\n' > note.txt"}, []int{0}, 0)
		result, err := GradeTranscriptCommandTrace(task, RunArtifacts{
			Mode: ModeMockProvider,
			EventLog: traceEventLog(t, []string{
				"pwd",
				"printf 'hello\\n' > note.txt",
			}, []int{0, 0}),
		})
		if err != nil {
			t.Fatalf("GradeTranscriptCommandTrace(): %v", err)
		}
		if result.Passed {
			t.Fatalf("passed = true, want false: %#v", result)
		}
		if !strings.Contains(result.Message, "command count") {
			t.Fatalf("message = %q, want command count mismatch", result.Message)
		}
	})

	t.Run("live-provider mode ignores equivalent command text", func(t *testing.T) {
		task := graderTask(true, true, true, false, []string{"ignored"}, []int{0}, 3)
		result, err := GradeTranscriptCommandTrace(task, RunArtifacts{
			Mode:     ModeLiveProvider,
			EventLog: traceEventLog(t, []string{"printf 'hello\\n' > ./note.txt"}, []int{0}),
		})
		if err != nil {
			t.Fatalf("GradeTranscriptCommandTrace(): %v", err)
		}
		if !result.Passed {
			t.Fatalf("passed = false, want true: %#v", result)
		}
	})

	t.Run("live-provider mode enforces max command count", func(t *testing.T) {
		task := graderTask(true, true, true, false, []string{"ignored"}, []int{0}, 1)
		result, err := GradeTranscriptCommandTrace(task, RunArtifacts{
			Mode: ModeLiveProvider,
			EventLog: traceEventLog(t, []string{
				"pwd",
				"printf 'hello\\n' > note.txt",
			}, []int{0, 0}),
		})
		if err != nil {
			t.Fatalf("GradeTranscriptCommandTrace(): %v", err)
		}
		if result.Passed {
			t.Fatalf("passed = true, want false: %#v", result)
		}
		if !strings.Contains(result.Message, "max command count") {
			t.Fatalf("message = %q, want max command count mismatch", result.Message)
		}
	})

	t.Run("live-provider mode enforces allowed exit-code set", func(t *testing.T) {
		task := graderTask(true, true, true, false, []string{"ignored"}, []int{0}, 3)
		result, err := GradeTranscriptCommandTrace(task, RunArtifacts{
			Mode:     ModeLiveProvider,
			EventLog: traceEventLog(t, []string{"pwd"}, []int{1}),
		})
		if err != nil {
			t.Fatalf("GradeTranscriptCommandTrace(): %v", err)
		}
		if result.Passed {
			t.Fatalf("passed = true, want false: %#v", result)
		}
		if !strings.Contains(result.Message, "unexpected exit code") {
			t.Fatalf("message = %q, want unexpected exit code mismatch", result.Message)
		}
	})
}

func TestEvaluateTaskPassPolicy(t *testing.T) {
	t.Run("disabled grader does not create a failing requirement", func(t *testing.T) {
		task := graderTask(true, true, false, true, nil, nil, 0)
		result := EvaluateTaskPassPolicy(task, []GraderResult{
			{
				GraderID:   outcomeWorkspaceSnapshotGraderID,
				TargetKind: graderTargetOutcome,
				Passed:     true,
			},
		})
		if !result.Passed {
			t.Fatalf("passed = false, want true: %#v", result)
		}
		if len(result.FailedRequiredGraders) != 0 {
			t.Fatalf("failed required graders = %#v, want empty", result.FailedRequiredGraders)
		}
	})

	t.Run("optional grader failure does not fail task", func(t *testing.T) {
		task := graderTask(true, true, true, false, nil, nil, 0)
		result := EvaluateTaskPassPolicy(task, []GraderResult{
			{
				GraderID:   outcomeWorkspaceSnapshotGraderID,
				TargetKind: graderTargetOutcome,
				Passed:     true,
			},
			{
				GraderID:   transcriptCommandTraceGraderID,
				TargetKind: graderTargetTranscript,
				Passed:     false,
				Message:    "compatibility failure",
			},
		})
		if !result.Passed {
			t.Fatalf("passed = false, want true: %#v", result)
		}
		if len(result.FailedRequiredGraders) != 0 {
			t.Fatalf("failed required graders = %#v, want empty", result.FailedRequiredGraders)
		}
	})

	t.Run("required grader failure fails task", func(t *testing.T) {
		task := graderTask(true, true, true, true, nil, nil, 0)
		result := EvaluateTaskPassPolicy(task, []GraderResult{
			{
				GraderID:   outcomeWorkspaceSnapshotGraderID,
				TargetKind: graderTargetOutcome,
				Passed:     true,
			},
			{
				GraderID:   transcriptCommandTraceGraderID,
				TargetKind: graderTargetTranscript,
				Passed:     false,
				Message:    "required failure",
			},
		})
		if result.Passed {
			t.Fatalf("passed = true, want false: %#v", result)
		}
		if len(result.FailedRequiredGraders) != 1 || result.FailedRequiredGraders[0].GraderID != transcriptCommandTraceGraderID {
			t.Fatalf("failed required graders = %#v, want transcript failure", result.FailedRequiredGraders)
		}
	})

	t.Run("grader record schema shape", func(t *testing.T) {
		raw, err := json.Marshal(GraderResult{
			GraderID:   outcomeWorkspaceSnapshotGraderID,
			TargetKind: graderTargetOutcome,
			Passed:     true,
			Evidence: WorkspaceSnapshotEvidence{
				Missing: []string{},
			},
			Message: "workspace matches",
		})
		if err != nil {
			t.Fatalf("json.Marshal(): %v", err)
		}

		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("json.Unmarshal(): %v", err)
		}
		for _, key := range []string{"grader_id", "target_kind", "passed", "evidence", "message"} {
			if _, ok := got[key]; !ok {
				t.Fatalf("json keys = %#v, missing %q", got, key)
			}
		}
		if _, ok := got["score"]; ok {
			t.Fatalf("json keys = %#v, score should be omitted when nil", got)
		}
	})

	t.Run("required command output grader failure fails task", func(t *testing.T) {
		task := Task{
			Graders: GraderConfig{
				OutcomeWorkspaceSnapshot: OutcomeWorkspaceSnapshotConfig{Enabled: false},
				TranscriptCommandTrace:   TranscriptCommandTraceConfig{Enabled: false},
				OutcomeCommandOutput: OutcomeCommandOutputConfig{
					Enabled:  true,
					Required: true,
					Command:  []string{"echo"},
				},
			},
		}
		result := EvaluateTaskPassPolicy(task, []GraderResult{
			{
				GraderID:   outcomeCommandOutputGraderID,
				TargetKind: graderTargetOutcome,
				Passed:     false,
				Message:    "exit code mismatch",
			},
		})
		if result.Passed {
			t.Fatalf("passed = true, want false")
		}
		if len(result.FailedRequiredGraders) != 1 || result.FailedRequiredGraders[0].GraderID != outcomeCommandOutputGraderID {
			t.Fatalf("failed required graders = %#v, want command output failure", result.FailedRequiredGraders)
		}
	})
}

func TestOutcomeCommandOutput(t *testing.T) {
	ctx := context.Background()

	t.Run("passes when exit code matches and no pattern constraints", func(t *testing.T) {
		workspaceDir := t.TempDir()
		task := graderTaskWithCommandOutput(true, true,
			[]string{"echo", "hello"}, 0, nil, nil, 0)
		result, err := GradeOutcomeCommandOutput(ctx, task, RunArtifacts{
			WorkspaceRoot: workspaceDir,
		})
		if err != nil {
			t.Fatalf("GradeOutcomeCommandOutput(): %v", err)
		}
		if !result.Passed {
			t.Fatalf("passed = false, want true: message=%q", result.Message)
		}
		if result.GraderID != outcomeCommandOutputGraderID {
			t.Fatalf("grader id = %q, want %q", result.GraderID, outcomeCommandOutputGraderID)
		}
		if result.TargetKind != graderTargetOutcome {
			t.Fatalf("target kind = %q, want %q", result.TargetKind, graderTargetOutcome)
		}
	})

	t.Run("fails when exit code does not match", func(t *testing.T) {
		workspaceDir := t.TempDir()
		task := graderTaskWithCommandOutput(true, true,
			[]string{"false"}, 0, nil, nil, 0)
		result, err := GradeOutcomeCommandOutput(ctx, task, RunArtifacts{
			WorkspaceRoot: workspaceDir,
		})
		if err != nil {
			t.Fatalf("GradeOutcomeCommandOutput(): %v", err)
		}
		if result.Passed {
			t.Fatalf("passed = true, want false")
		}
		if !strings.Contains(result.Message, "exit code") {
			t.Fatalf("message = %q, want exit code mismatch", result.Message)
		}
	})

	t.Run("passes when expected exit code is nonzero and matches", func(t *testing.T) {
		workspaceDir := t.TempDir()
		task := graderTaskWithCommandOutput(true, true,
			[]string{"false"}, 1, nil, nil, 0)
		result, err := GradeOutcomeCommandOutput(ctx, task, RunArtifacts{
			WorkspaceRoot: workspaceDir,
		})
		if err != nil {
			t.Fatalf("GradeOutcomeCommandOutput(): %v", err)
		}
		if !result.Passed {
			t.Fatalf("passed = false, want true: message=%q", result.Message)
		}
	})

	t.Run("passes when stdout contains required pattern", func(t *testing.T) {
		workspaceDir := t.TempDir()
		task := graderTaskWithCommandOutput(true, true,
			[]string{"echo", "all tests passed"}, 0,
			[]string{"tests passed"}, nil, 0)
		result, err := GradeOutcomeCommandOutput(ctx, task, RunArtifacts{
			WorkspaceRoot: workspaceDir,
		})
		if err != nil {
			t.Fatalf("GradeOutcomeCommandOutput(): %v", err)
		}
		if !result.Passed {
			t.Fatalf("passed = false, want true: message=%q", result.Message)
		}
	})

	t.Run("fails when stdout missing required pattern", func(t *testing.T) {
		workspaceDir := t.TempDir()
		task := graderTaskWithCommandOutput(true, true,
			[]string{"echo", "no match here"}, 0,
			[]string{"tests passed"}, nil, 0)
		result, err := GradeOutcomeCommandOutput(ctx, task, RunArtifacts{
			WorkspaceRoot: workspaceDir,
		})
		if err != nil {
			t.Fatalf("GradeOutcomeCommandOutput(): %v", err)
		}
		if result.Passed {
			t.Fatalf("passed = true, want false")
		}
		if !strings.Contains(result.Message, "stdout does not contain") {
			t.Fatalf("message = %q, want stdout pattern mismatch", result.Message)
		}
	})

	t.Run("fails when stderr contains forbidden pattern", func(t *testing.T) {
		workspaceDir := t.TempDir()
		task := graderTaskWithCommandOutput(true, true,
			[]string{"sh", "-c", "echo 'fatal error' >&2"}, 0,
			nil, []string{"fatal error"}, 0)
		result, err := GradeOutcomeCommandOutput(ctx, task, RunArtifacts{
			WorkspaceRoot: workspaceDir,
		})
		if err != nil {
			t.Fatalf("GradeOutcomeCommandOutput(): %v", err)
		}
		if result.Passed {
			t.Fatalf("passed = true, want false")
		}
		if !strings.Contains(result.Message, "stderr contains forbidden") {
			t.Fatalf("message = %q, want stderr forbidden match", result.Message)
		}
	})

	t.Run("expands workspace template variable in command args", func(t *testing.T) {
		workspaceDir := t.TempDir()
		writeWorkspaceFile(t, filepath.Join(workspaceDir, "probe.txt"), "found-it\n", 0o644)
		task := graderTaskWithCommandOutput(true, true,
			[]string{"cat", "{{workspace}}/probe.txt"}, 0,
			[]string{"found-it"}, nil, 0)
		result, err := GradeOutcomeCommandOutput(ctx, task, RunArtifacts{
			WorkspaceRoot: workspaceDir,
		})
		if err != nil {
			t.Fatalf("GradeOutcomeCommandOutput(): %v", err)
		}
		if !result.Passed {
			t.Fatalf("passed = false, want true: message=%q", result.Message)
		}
		evidence, ok := result.Evidence.(CommandOutputEvidence)
		if !ok {
			t.Fatalf("evidence type = %T, want CommandOutputEvidence", result.Evidence)
		}
		if evidence.ExpandedCommand[1] != filepath.Join(workspaceDir, "probe.txt") {
			t.Fatalf("expanded command = %q, want workspace path", evidence.ExpandedCommand)
		}
	})

	t.Run("times out and fails", func(t *testing.T) {
		workspaceDir := t.TempDir()
		task := graderTaskWithCommandOutput(true, true,
			[]string{"sleep", "10"}, 0, nil, nil, 1)
		result, err := GradeOutcomeCommandOutput(ctx, task, RunArtifacts{
			WorkspaceRoot: workspaceDir,
		})
		if err != nil {
			t.Fatalf("GradeOutcomeCommandOutput(): %v", err)
		}
		if result.Passed {
			t.Fatalf("passed = true, want false")
		}
		if !strings.Contains(result.Message, "timed out") {
			t.Fatalf("message = %q, want timeout", result.Message)
		}
		evidence, ok := result.Evidence.(CommandOutputEvidence)
		if !ok {
			t.Fatalf("evidence type = %T, want CommandOutputEvidence", result.Evidence)
		}
		if !evidence.TimedOut {
			t.Fatalf("evidence.TimedOut = false, want true")
		}
	})

	t.Run("evidence captures both original and expanded command", func(t *testing.T) {
		workspaceDir := t.TempDir()
		task := graderTaskWithCommandOutput(true, true,
			[]string{"echo", "ok"}, 0, nil, nil, 0)
		result, err := GradeOutcomeCommandOutput(ctx, task, RunArtifacts{
			WorkspaceRoot: workspaceDir,
		})
		if err != nil {
			t.Fatalf("GradeOutcomeCommandOutput(): %v", err)
		}
		evidence, ok := result.Evidence.(CommandOutputEvidence)
		if !ok {
			t.Fatalf("evidence type = %T, want CommandOutputEvidence", result.Evidence)
		}
		if len(evidence.Command) != 2 || evidence.Command[0] != "echo" {
			t.Fatalf("evidence.Command = %v, want [echo ok]", evidence.Command)
		}
	})

	t.Run("command output evidence serializes correctly", func(t *testing.T) {
		raw, err := json.Marshal(GraderResult{
			GraderID:   outcomeCommandOutputGraderID,
			TargetKind: graderTargetOutcome,
			Passed:     true,
			Evidence: CommandOutputEvidence{
				Command:          []string{"go", "vet"},
				ExpandedCommand:  []string{"go", "vet"},
				ExitCode:         0,
				ExpectedExitCode: 0,
				Stdout:           "",
				Stderr:           "",
			},
			Message: "command output matches",
		})
		if err != nil {
			t.Fatalf("json.Marshal(): %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("json.Unmarshal(): %v", err)
		}
		evidence, ok := got["evidence"].(map[string]any)
		if !ok {
			t.Fatalf("evidence type = %T, want map", got["evidence"])
		}
		for _, key := range []string{"command", "expanded_command", "exit_code", "expected_exit_code", "stdout", "stderr"} {
			if _, ok := evidence[key]; !ok {
				t.Fatalf("evidence keys = %#v, missing %q", evidence, key)
			}
		}
		if _, ok := evidence["timed_out"]; ok {
			t.Fatalf("timed_out should be omitted when false")
		}
	})
}

func graderTaskWithCommandOutput(
	cmdEnabled, cmdRequired bool,
	command []string, expectedExitCode int,
	stdoutContains, stderrMustNotContain []string,
	timeoutSeconds int,
) Task {
	return Task{
		Graders: GraderConfig{
			OutcomeWorkspaceSnapshot: OutcomeWorkspaceSnapshotConfig{Enabled: false},
			TranscriptCommandTrace:   TranscriptCommandTraceConfig{Enabled: false},
			OutcomeCommandOutput: OutcomeCommandOutputConfig{
				Enabled:              cmdEnabled,
				Required:             cmdRequired,
				Command:              append([]string(nil), command...),
				ExpectedExitCode:     expectedExitCode,
				StdoutContains:       append([]string(nil), stdoutContains...),
				StderrMustNotContain: append([]string(nil), stderrMustNotContain...),
				TimeoutSeconds:       timeoutSeconds,
			},
		},
	}
}

func graderTask(outcomeEnabled, outcomeRequired, transcriptEnabled, transcriptRequired bool, expectedCommands []string, expectedExitCodes []int, maxCommandCount int) Task {
	return Task{
		Graders: GraderConfig{
			OutcomeWorkspaceSnapshot: OutcomeWorkspaceSnapshotConfig{
				Enabled:  outcomeEnabled,
				Required: outcomeRequired,
			},
			TranscriptCommandTrace: TranscriptCommandTraceConfig{
				Enabled:           transcriptEnabled,
				Required:          transcriptRequired,
				ExpectedCommands:  append([]string(nil), expectedCommands...),
				ExpectedExitCodes: append([]int(nil), expectedExitCodes...),
				MaxCommandCount:   maxCommandCount,
			},
		},
	}
}

func traceEventLog(t *testing.T, commands []string, exitCodes []int) string {
	t.Helper()
	if len(commands) != len(exitCodes) {
		t.Fatalf("commands len = %d, exitCodes len = %d, want equal", len(commands), len(exitCodes))
	}

	var builder strings.Builder
	for i, command := range commands {
		builder.WriteString(jsonLine(t, eventEnvelope{
			Type:    "command_start",
			Payload: json.RawMessage(mustJSON(t, commandStartEvent{Command: command, Dir: "/tmp/work"})),
		}))
		builder.WriteString(jsonLine(t, eventEnvelope{
			Type:    "command_done",
			Payload: json.RawMessage(mustJSON(t, commandDoneEvent{Command: command, ExitCode: exitCodes[i]})),
		}))
	}
	return builder.String()
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%#v): %v", value, err)
	}
	return data
}

func writeWorkspaceFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("Chmod(%q): %v", path, err)
	}
}
