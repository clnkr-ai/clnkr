package clnkrapp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDelegateCommand(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantTask string
		wantOK   bool
	}{
		{name: "bare command", input: "/delegate", wantOK: true},
		{name: "trimmed command", input: "  /delegate inspect README  ", wantTask: "inspect README", wantOK: true},
		{name: "tab separator", input: "/delegate\tinspect README", wantTask: "inspect README", wantOK: true},
		{name: "not delegate", input: "delegate inspect README", wantOK: false},
		{name: "prefixed word", input: "/delegated inspect README", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTask, gotOK := ParseDelegateCommand(tt.input)
			if gotTask != tt.wantTask || gotOK != tt.wantOK {
				t.Fatalf("ParseDelegateCommand(%q) = (%q, %v), want (%q, %v)", tt.input, gotTask, gotOK, tt.wantTask, tt.wantOK)
			}
		})
	}
}

func TestChildProbeTranscriptBlockContainsSummaryOnly(t *testing.T) {
	result := ChildProbeResult{
		ChildID: "child-001",
		Status:  ChildProbeStatusDone,
		Summary: "README explains setup.",
		Artifacts: ChildProbeArtifacts{
			EventLog:   filepath.Join("delegates", "child-001", "event-log.jsonl"),
			Trajectory: filepath.Join("delegates", "child-001", "trajectory.json"),
			Result:     filepath.Join("delegates", "child-001", "result.json"),
		},
	}

	block, err := FormatChildProbeTranscriptBlock(result)
	if err != nil {
		t.Fatalf("FormatChildProbeTranscriptBlock: %v", err)
	}
	var got struct {
		Type         string              `json:"type"`
		Source       string              `json:"source"`
		ChildID      string              `json:"child_id"`
		Status       ChildProbeStatus    `json:"status"`
		Summary      string              `json:"summary"`
		Artifacts    ChildProbeArtifacts `json:"artifacts"`
		Verification string              `json:"verification_required"`
	}
	if err := json.Unmarshal([]byte(block), &got); err != nil {
		t.Fatalf("child block is not JSON: %v\n%s", err, block)
	}
	if got.Type != "child_probe_result" || got.Source != "clnkr" || got.ChildID != "child-001" {
		t.Fatalf("block identity = %#v, want child_probe_result/clnkr/child-001", got)
	}
	if got.Verification == "" {
		t.Fatalf("verification_required empty in %#v", got)
	}
	if got.Artifacts.Trajectory == "" || got.Artifacts.EventLog == "" {
		t.Fatalf("artifacts = %#v, want event_log and trajectory", got.Artifacts)
	}
}

func TestExecChildProbeRunnerWritesArtifacts(t *testing.T) {
	dir := t.TempDir()
	exe := helperChildProbeExecutable(t)
	runner := ExecChildProbeRunner{
		Executable: exe,
		BaseArgs:   []string{"--no-system-prompt"},
	}
	req := ChildProbeRequest{
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
	if result.Status != ChildProbeStatusDone || result.Summary != "child summary" {
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

func TestExecChildProbeRunnerDoesNotUseStdoutAsSummary(t *testing.T) {
	dir := t.TempDir()
	exe := helperChildProbeExecutableWithStdout(t, "command output that should stay out of summary\nchild summary\n")
	runner := ExecChildProbeRunner{Executable: exe}
	req := ChildProbeRequest{
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

func helperChildProbeExecutable(t *testing.T) string {
	t.Helper()
	return helperChildProbeExecutableWithStdout(t, "child summary\n")
}

func helperChildProbeExecutableWithStdout(t *testing.T, stdout string) string {
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
