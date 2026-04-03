package evaluations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/transcript"
)

func TestNormalizePath(t *testing.T) {
	t.Parallel()

	tempRoot := t.TempDir()
	workspaceDir := filepath.Join(tempRoot, "workspace")
	homeDir := filepath.Join(tempRoot, "home")
	configDir := filepath.Join(tempRoot, "config")
	stateDir := filepath.Join(tempRoot, "state")
	configAppDir := filepath.Join(configDir, "clnkr")

	for _, dir := range []string{workspaceDir, homeDir, configAppDir, stateDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	configFile := filepath.Join(configAppDir, "prefs.json")
	if err := os.WriteFile(configFile, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", configFile, err)
	}

	workspaceFile := filepath.Join(workspaceDir, "note.txt")
	if err := os.WriteFile(workspaceFile, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", workspaceFile, err)
	}

	roots := normalizationRoots{
		Workdir: workspaceDir,
		Home:    homeDir,
		Config:  configDir,
		State:   stateDir,
		Temp:    tempRoot,
	}

	if got := normalizePath(configFile, roots); got != filepath.ToSlash("<CONFIG>/clnkr/prefs.json") {
		t.Fatalf("normalizePath(config/clnkr) = %q, want %q", got, "<CONFIG>/clnkr/prefs.json")
	}

	symlinkRoot := filepath.Join(t.TempDir(), "tmp-link")
	if err := os.Symlink(tempRoot, symlinkRoot); err != nil {
		t.Fatalf("Symlink(%q -> %q): %v", symlinkRoot, tempRoot, err)
	}
	symlinkedWorkspaceFile := filepath.Join(symlinkRoot, "workspace", "note.txt")
	if got := normalizePath(symlinkedWorkspaceFile, roots); got != filepath.ToSlash("<WORKDIR>/note.txt") {
		t.Fatalf("normalizePath(symlinked workspace) = %q, want %q", got, "<WORKDIR>/note.txt")
	}
}

func TestNormalizeTranscript(t *testing.T) {
	t.Parallel()

	artifacts := sampleRunArtifacts(t)

	records, err := NormalizeTranscript(artifacts)
	if err != nil {
		t.Fatalf("NormalizeTranscript(): %v", err)
	}

	var gotKinds []string
	for _, record := range records {
		gotKinds = append(gotKinds, record.Kind)
	}
	wantKinds := []string{
		"system_prompt",
		"user_instruction",
		"assistant_turn",
		"command_start",
		"command_result",
		"state_update",
		"clarification",
		"completion",
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("record kinds = %#v, want %#v", gotKinds, wantKinds)
	}

	if records[2].TurnType != "act" {
		t.Fatalf("assistant turn type = %q, want act", records[2].TurnType)
	}
	if records[3].Command != "printf 'hello\\n' > <WORKDIR>/note.txt" {
		t.Fatalf("command_start command = %q", records[3].Command)
	}
	if records[3].Cwd != "<WORKDIR>" {
		t.Fatalf("command_start cwd = %q, want <WORKDIR>", records[3].Cwd)
	}
	if records[4].ExitCode != 0 {
		t.Fatalf("command_result exit_code = %d, want 0", records[4].ExitCode)
	}
	if records[5].Cwd != "<WORKDIR>" {
		t.Fatalf("state_update cwd = %q, want <WORKDIR>", records[5].Cwd)
	}
	if records[6].TurnType != "clarify" {
		t.Fatalf("clarification turn type = %q, want clarify", records[6].TurnType)
	}
	if records[7].TurnType != "done" {
		t.Fatalf("completion turn type = %q, want done", records[7].TurnType)
	}
}

func TestNormalizeOutcome(t *testing.T) {
	t.Parallel()

	artifacts := sampleRunArtifacts(t)

	outcome, err := NormalizeOutcome(artifacts, filepath.ToSlash("outcome/workspace"))
	if err != nil {
		t.Fatalf("NormalizeOutcome(): %v", err)
	}

	if outcome.FinalExitCode != 0 {
		t.Fatalf("FinalExitCode = %d, want 0", outcome.FinalExitCode)
	}
	if outcome.FinalCwd != "<WORKDIR>" {
		t.Fatalf("FinalCwd = %q, want <WORKDIR>", outcome.FinalCwd)
	}
	if len(outcome.WorkspaceFiles) != 1 {
		t.Fatalf("workspace file count = %d, want 1", len(outcome.WorkspaceFiles))
	}
	if outcome.WorkspaceFiles[0].Path != "note.txt" {
		t.Fatalf("workspace file path = %q, want note.txt", outcome.WorkspaceFiles[0].Path)
	}
	if outcome.WorkspaceFiles[0].SHA256 != checksumSHA256String("hello\n") {
		t.Fatalf("workspace file sha256 = %q, want %q", outcome.WorkspaceFiles[0].SHA256, checksumSHA256String("hello\n"))
	}
	if !reflect.DeepEqual(outcome.MaterializedPaths, []string{"outcome/workspace/note.txt"}) {
		t.Fatalf("materialized paths = %#v, want %#v", outcome.MaterializedPaths, []string{"outcome/workspace/note.txt"})
	}
}

func TestWriteTrialBundle(t *testing.T) {
	t.Parallel()

	artifacts := sampleRunArtifacts(t)
	root := filepath.Join(t.TempDir(), artifacts.TrialID)

	bundle, err := WriteTrialBundle(root, artifacts, nil)
	if err != nil {
		t.Fatalf("WriteTrialBundle(): %v", err)
	}

	if bundle.Root != root {
		t.Fatalf("bundle root = %q, want %q", bundle.Root, root)
	}
	if bundle.SchemaVersion == "" {
		t.Fatal("bundle schema version is empty")
	}
	if bundle.Provider.Model != artifacts.ProviderModel {
		t.Fatalf("bundle provider model = %q, want %q", bundle.Provider.Model, artifacts.ProviderModel)
	}

	for _, rel := range []string{
		"bundle.json",
		"raw/transcript.json",
		"raw/events.jsonl",
		"raw/provider-requests.jsonl",
		"raw/provider-responses.jsonl",
		"normalized/transcript.jsonl",
		"normalized/outcome.json",
		"normalized/graders.jsonl",
		"outcome/workspace/note.txt",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%q): %v", path, err)
		}
		if info.IsDir() {
			t.Fatalf("%q is a directory, want file", path)
		}
	}

	graderData, err := os.ReadFile(filepath.Join(root, "normalized", "graders.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(graders.jsonl): %v", err)
	}
	if len(graderData) != 0 {
		t.Fatalf("graders.jsonl = %q, want empty file", string(graderData))
	}

	for _, rel := range []string{
		"raw/transcript.json",
		"raw/events.jsonl",
		"normalized/transcript.jsonl",
		"normalized/outcome.json",
	} {
		if bundle.Checksums[rel] == "" {
			t.Fatalf("checksum for %q is empty", rel)
		}
	}

	rawRequests, err := os.ReadFile(filepath.Join(root, "raw", "provider-requests.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(provider-requests.jsonl): %v", err)
	}
	if got, want := string(rawRequests), artifacts.ProviderRequests[0].RawRequest+"\n"; got != want {
		t.Fatalf("raw provider requests = %q, want %q", got, want)
	}

	rawResponses, err := os.ReadFile(filepath.Join(root, "raw", "provider-responses.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(provider-responses.jsonl): %v", err)
	}
	if got, want := string(rawResponses), artifacts.ProviderResponses[0]+"\n"; got != want {
		t.Fatalf("raw provider responses = %q, want %q", got, want)
	}
}

func TestWriteTrialBundlePersistsTrialStatus(t *testing.T) {
	t.Parallel()

	artifacts := sampleRunArtifacts(t)
	artifacts.TrialPassed = true
	artifacts.FailedRequiredGraders = []GraderResult{
		{
			GraderID:   "outcome_workspace_snapshot",
			TargetKind: "outcome",
			Passed:     false,
			Message:    "missing note.txt",
		},
	}
	root := filepath.Join(t.TempDir(), artifacts.TrialID)

	written, err := WriteTrialBundle(root, artifacts, artifacts.FailedRequiredGraders)
	if err != nil {
		t.Fatalf("WriteTrialBundle(): %v", err)
	}
	if !written.TrialPassed {
		t.Fatal("written trial_passed = false, want true")
	}

	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle(): %v", err)
	}
	if !loaded.TrialPassed {
		t.Fatal("loaded trial_passed = false, want true")
	}
	if len(loaded.FailedRequiredGraders) != 1 || loaded.FailedRequiredGraders[0].GraderID != "outcome_workspace_snapshot" {
		t.Fatalf("loaded failed required graders = %#v, want outcome failure", loaded.FailedRequiredGraders)
	}
}

func TestWriteTrialBundlePreservesRuntimeStyleProviderResponsesVerbatim(t *testing.T) {
	t.Parallel()

	artifacts := sampleRunArtifacts(t)
	artifacts.ProviderResponses = []string{
		"{\"id\":\"resp-1\"}\n",
		"{\"id\":\"resp-2\"}\n",
	}
	root := filepath.Join(t.TempDir(), artifacts.TrialID)

	if _, err := WriteTrialBundle(root, artifacts, nil); err != nil {
		t.Fatalf("WriteTrialBundle(): %v", err)
	}

	rawResponses, err := os.ReadFile(filepath.Join(root, "raw", "provider-responses.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(provider-responses.jsonl): %v", err)
	}
	want := "{\"id\":\"resp-1\"}\n{\"id\":\"resp-2\"}\n"
	if string(rawResponses) != want {
		t.Fatalf("raw provider responses = %q, want %q", string(rawResponses), want)
	}
}

func TestWriteTrialBundleCreatesEmptyWorkspaceDirectory(t *testing.T) {
	t.Parallel()

	artifacts := sampleRunArtifacts(t)
	artifacts.Workspace = map[string]string{}
	root := filepath.Join(t.TempDir(), artifacts.TrialID)

	if _, err := WriteTrialBundle(root, artifacts, nil); err != nil {
		t.Fatalf("WriteTrialBundle(): %v", err)
	}

	info, err := os.Stat(filepath.Join(root, "outcome", "workspace"))
	if err != nil {
		t.Fatalf("Stat(outcome/workspace): %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("outcome/workspace mode = %v, want directory", info.Mode())
	}
}

func TestLoadBundle(t *testing.T) {
	t.Parallel()

	artifacts := sampleRunArtifacts(t)
	root := filepath.Join(t.TempDir(), artifacts.TrialID)

	written, err := WriteTrialBundle(root, artifacts, nil)
	if err != nil {
		t.Fatalf("WriteTrialBundle(): %v", err)
	}

	loaded, err := LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle(): %v", err)
	}

	if loaded.TrialID != written.TrialID {
		t.Fatalf("loaded trial id = %q, want %q", loaded.TrialID, written.TrialID)
	}
	if loaded.Artifacts.RawTranscript != "raw/transcript.json" {
		t.Fatalf("raw transcript path = %q, want raw/transcript.json", loaded.Artifacts.RawTranscript)
	}

	rawTranscript, err := loaded.ReadRawTranscript()
	if err != nil {
		t.Fatalf("ReadRawTranscript(): %v", err)
	}
	if len(rawTranscript) != 7 {
		t.Fatalf("raw transcript len = %d, want 7", len(rawTranscript))
	}

	normalizedTranscript, err := loaded.ReadNormalizedTranscript()
	if err != nil {
		t.Fatalf("ReadNormalizedTranscript(): %v", err)
	}
	if len(normalizedTranscript) != 8 {
		t.Fatalf("normalized transcript len = %d, want 8", len(normalizedTranscript))
	}

	outcome, err := loaded.ReadNormalizedOutcome()
	if err != nil {
		t.Fatalf("ReadNormalizedOutcome(): %v", err)
	}
	if outcome.FinalCwd != "<WORKDIR>" {
		t.Fatalf("loaded final cwd = %q, want <WORKDIR>", outcome.FinalCwd)
	}

	graders, err := loaded.ReadGraders()
	if err != nil {
		t.Fatalf("ReadGraders(): %v", err)
	}
	if len(graders) != 0 {
		t.Fatalf("loaded graders len = %d, want 0", len(graders))
	}
}

func TestLoadBundleRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	artifacts := sampleRunArtifacts(t)
	root := filepath.Join(t.TempDir(), artifacts.TrialID)
	if _, err := WriteTrialBundle(root, artifacts, nil); err != nil {
		t.Fatalf("WriteTrialBundle(): %v", err)
	}

	escapeTarget := filepath.Join(t.TempDir(), "escape.json")
	if err := os.WriteFile(escapeTarget, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", escapeTarget, err)
	}
	artifactPath := filepath.Join(root, "normalized", "outcome.json")
	if err := os.Remove(artifactPath); err != nil {
		t.Fatalf("Remove(%q): %v", artifactPath, err)
	}
	if err := os.Symlink(escapeTarget, artifactPath); err != nil {
		t.Fatalf("Symlink(%q -> %q): %v", artifactPath, escapeTarget, err)
	}

	_, err := LoadBundle(root)
	if err == nil || !strings.Contains(err.Error(), "escapes bundle root") {
		t.Fatalf("LoadBundle() error = %v, want symlink escape rejection", err)
	}
}

func TestLoadBundleRejectsSymlinkEscapeWithMissingDescendants(t *testing.T) {
	t.Parallel()

	artifacts := sampleRunArtifacts(t)
	root := filepath.Join(t.TempDir(), artifacts.TrialID)
	if _, err := WriteTrialBundle(root, artifacts, nil); err != nil {
		t.Fatalf("WriteTrialBundle(): %v", err)
	}

	outsideRoot := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outsideRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", outsideRoot, err)
	}

	linkPath := filepath.Join(root, "normalized-link")
	if err := os.Symlink(outsideRoot, linkPath); err != nil {
		t.Fatalf("Symlink(%q -> %q): %v", linkPath, outsideRoot, err)
	}

	var bundleDoc map[string]any
	bundlePath := filepath.Join(root, "bundle.json")
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", bundlePath, err)
	}
	if err := json.Unmarshal(data, &bundleDoc); err != nil {
		t.Fatalf("json.Unmarshal(bundle.json): %v", err)
	}
	artifactsDoc, ok := bundleDoc["artifacts"].(map[string]any)
	if !ok {
		t.Fatalf("bundle artifacts = %#v, want object", bundleDoc["artifacts"])
	}
	artifactsDoc["normalized_outcome"] = "normalized-link/missing/outcome.json"
	rewritten, err := json.MarshalIndent(bundleDoc, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(bundle.json): %v", err)
	}
	rewritten = append(rewritten, '\n')
	if err := os.WriteFile(bundlePath, rewritten, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", bundlePath, err)
	}

	_, err = LoadBundle(root)
	if err == nil || !strings.Contains(err.Error(), "escapes bundle root") {
		t.Fatalf("LoadBundle() error = %v, want symlink ancestor escape rejection", err)
	}
}

func sampleRunArtifacts(t *testing.T) RunArtifacts {
	t.Helper()

	tempRoot := t.TempDir()
	workspaceDir := filepath.Join(tempRoot, "workspace")
	homeDir := filepath.Join(tempRoot, "home")
	configDir := filepath.Join(tempRoot, "config")
	stateDir := filepath.Join(tempRoot, "state")

	for _, dir := range []string{
		workspaceDir,
		homeDir,
		filepath.Join(configDir, "clnkr"),
		stateDir,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	if err := os.WriteFile(filepath.Join(workspaceDir, "note.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(note.txt): %v", err)
	}

	messages := []clnkr.Message{
		{Role: "system", Content: "System prompt rooted at " + workspaceDir},
		{Role: "user", Content: "Create " + filepath.Join(workspaceDir, "note.txt")},
		{Role: "assistant", Content: `{"type":"act","command":"printf 'hello\\n' > ` + filepath.ToSlash(filepath.Join(workspaceDir, "note.txt")) + `"}`},
		{Role: "user", Content: commandResultMessage(filepath.ToSlash(filepath.Join(workspaceDir, "note.txt")))},
		{Role: "user", Content: transcript.FormatStateMessage(workspaceDir)},
		{Role: "assistant", Content: `{"type":"clarify","question":"Need anything else in ` + filepath.ToSlash(homeDir) + `?"}`},
		{Role: "assistant", Content: `{"type":"done","summary":"Created ` + filepath.ToSlash(filepath.Join(workspaceDir, "note.txt")) + `"}`},
	}
	trajectoryBytes, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("json.Marshal(messages): %v", err)
	}

	command := "printf 'hello\\n' > " + filepath.ToSlash(filepath.Join(workspaceDir, "note.txt"))
	eventLog := jsonLine(t, map[string]any{
		"type": "command_start",
		"payload": map[string]any{
			"command": command,
			"dir":     workspaceDir,
		},
	}) + jsonLine(t, map[string]any{
		"type": "command_done",
		"payload": map[string]any{
			"command":   command,
			"stdout":    "",
			"stderr":    "",
			"exit_code": 0,
		},
	})

	startedAt := time.Date(2026, time.March, 31, 10, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(2 * time.Second)

	return RunArtifacts{
		SuiteID:         "default",
		TaskID:          "001-basic-edit",
		TrialID:         "trial-123",
		SuiteTaskIndex:  0,
		TrialAttempt:    0,
		Mode:            ModeMockProvider,
		ProviderModel:   "test-model",
		ProviderBaseURL: "http://127.0.0.1:9999",
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
		SystemPrompt:    messages[0].Content,
		Trajectory:      string(trajectoryBytes),
		EventLog:        eventLog,
		ProviderRequests: []CapturedRequest{
			{
				Model:      "test-model",
				Messages:   append([]clnkr.Message(nil), messages[:3]...),
				RawRequest: `{"model":"test-model"}`,
				RawResponse: `{"choices":[{"message":{"role":"assistant","content":"{\"type\":\"act\",\"command\":\"` +
					command +
					`\"}"}}]}`,
			},
		},
		ProviderResponses: []string{
			`{"choices":[{"message":{"role":"assistant","content":"{\"type\":\"act\",\"command\":\"` + command + `\"}"}}]}`,
		},
		Workspace: map[string]string{
			"note.txt": "hello\n",
		},
		WorkspaceRoot: workspaceDir,
		HomeDir:       homeDir,
		ConfigDir:     configDir,
		StateDir:      stateDir,
		TempDir:       tempRoot,
		ExitCode:      0,
	}
}

func commandResultMessage(workspaceFile string) string {
	return "[command]\nprintf 'hello\\n' > " + workspaceFile + "\n[/command]\n" +
		"[exit_code]\n0\n[/exit_code]\n" +
		"[stdout]\n\n[/stdout]\n" +
		"[stderr]\n\n[/stderr]"
}

func jsonLine(t *testing.T, value any) string {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%#v): %v", value, err)
	}
	return string(data) + "\n"
}
