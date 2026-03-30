package eval_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

type compareConfig struct {
	Workspace     bool `json:"workspace"`
	SemanticTrace bool `json:"semantic_trace"`
}

type expectConfig struct {
	ExitCode  int      `json:"exit_code"`
	Commands  []string `json:"commands"`
	ExitCodes []int    `json:"exit_codes"`
}

type manifest struct {
	Name             string        `json:"name"`
	TaskFile         string        `json:"task_file"`
	ModelTurnsFile   string        `json:"model_turns_file"`
	SeedMessagesFile string        `json:"seed_messages_file"`
	Cwd              string        `json:"cwd"`
	FullSend         bool          `json:"full_send"`
	MaxSteps         int           `json:"max_steps"`
	Compare          compareConfig `json:"compare"`
	Expect           expectConfig  `json:"expect"`
}

type semanticTrace struct {
	Commands  []string
	ExitCodes []int
}

type caseResult struct {
	ExitCode int
	Trace    semanticTrace
}

type trajectoryMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type logEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type commandDonePayload struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
}

type openAIRequest struct {
	Model    string              `json:"model"`
	Messages []trajectoryMessage `json:"messages"`
}

type runArtifacts struct {
	SystemPrompt string
	Trajectory   string
	EventLog     string
	Workspace    map[string]string
}

type fixtureServer struct {
	server   *httptest.Server
	turns    []string
	mu       sync.Mutex
	index    int
	requests []openAIRequest
}

type evalMode string

const (
	evalModeFixture evalMode = "fixture"
	evalModeLive    evalMode = "live"
)

type runConfig struct {
	Mode    evalMode
	APIKey  string
	BaseURL string
	Model   string
}

func loadRunConfigFromEnv(getenv func(string) string) (runConfig, error) {
	mode := evalMode(firstNonEmpty(getenv("CLNKR_EVAL_MODE"), string(evalModeFixture)))
	switch mode {
	case evalModeFixture:
		return runConfig{
			Mode:   evalModeFixture,
			APIKey: "dummy-key",
			Model:  "test-model",
		}, nil
	case evalModeLive:
		cfg := runConfig{
			Mode:    evalModeLive,
			APIKey:  firstNonEmpty(getenv("CLNKR_EVAL_API_KEY"), getenv("OPENAI_API_KEY")),
			BaseURL: firstNonEmpty(getenv("CLNKR_EVAL_BASE_URL"), getenv("OPENAI_BASE_URL")),
			Model:   firstNonEmpty(getenv("CLNKR_EVAL_MODEL"), "gpt-5.4-nano"),
		}
		if cfg.APIKey == "" {
			return runConfig{}, fmt.Errorf("live eval mode missing API key: set CLNKR_EVAL_API_KEY or OPENAI_API_KEY")
		}
		if cfg.BaseURL == "" {
			return runConfig{}, fmt.Errorf("live eval mode missing base URL: set CLNKR_EVAL_BASE_URL or OPENAI_BASE_URL")
		}
		return cfg, nil
	default:
		return runConfig{}, fmt.Errorf("unknown CLNKR_EVAL_MODE %q", mode)
	}
}

func TestLoadRunConfigFromEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		want    runConfig
		wantErr string
	}{
		{
			name: "default fixture mode",
			env:  map[string]string{},
			want: runConfig{
				Mode:   evalModeFixture,
				APIKey: "dummy-key",
				Model:  "test-model",
			},
		},
		{
			name: "live mode uses openai fallbacks",
			env: map[string]string{
				"CLNKR_EVAL_MODE": "live",
				"OPENAI_API_KEY":  "openai-key",
				"OPENAI_BASE_URL": "https://openai.example/v1",
			},
			want: runConfig{
				Mode:    evalModeLive,
				APIKey:  "openai-key",
				BaseURL: "https://openai.example/v1",
				Model:   "gpt-5.4-nano",
			},
		},
		{
			name: "live mode ignores ambient clnkr env",
			env: map[string]string{
				"CLNKR_EVAL_MODE": "live",
				"CLNKR_API_KEY":   "clnkr-key",
				"CLNKR_BASE_URL":  "https://custom.example/v1",
				"CLNKR_MODEL":     "custom-model",
				"OPENAI_API_KEY":  "openai-key",
				"OPENAI_BASE_URL": "https://openai.example/v1",
			},
			want: runConfig{
				Mode:    evalModeLive,
				APIKey:  "openai-key",
				BaseURL: "https://openai.example/v1",
				Model:   "gpt-5.4-nano",
			},
		},
		{
			name: "live mode prefers eval overrides",
			env: map[string]string{
				"CLNKR_EVAL_MODE":     "live",
				"CLNKR_EVAL_API_KEY":  "eval-key",
				"CLNKR_EVAL_BASE_URL": "https://eval.example/v1",
				"CLNKR_EVAL_MODEL":    "eval-model",
				"OPENAI_API_KEY":      "openai-key",
				"OPENAI_BASE_URL":     "https://openai.example/v1",
				"CLNKR_API_KEY":       "clnkr-key",
				"CLNKR_BASE_URL":      "https://custom.example/v1",
				"CLNKR_MODEL":         "custom-model",
			},
			want: runConfig{
				Mode:    evalModeLive,
				APIKey:  "eval-key",
				BaseURL: "https://eval.example/v1",
				Model:   "eval-model",
			},
		},
		{
			name: "invalid mode",
			env: map[string]string{
				"CLNKR_EVAL_MODE": "bogus",
			},
			wantErr: "unknown CLNKR_EVAL_MODE",
		},
		{
			name: "live mode requires api key",
			env: map[string]string{
				"CLNKR_EVAL_MODE": "live",
				"CLNKR_API_KEY":   "ambient-key",
				"OPENAI_BASE_URL": "https://openai.example/v1",
			},
			wantErr: "missing API key",
		},
		{
			name: "live mode requires base url",
			env: map[string]string{
				"CLNKR_EVAL_MODE": "live",
				"OPENAI_API_KEY":  "openai-key",
				"CLNKR_BASE_URL":  "https://ambient.example/v1",
			},
			wantErr: "missing base URL",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := loadRunConfigFromEnv(func(key string) string {
				return tt.env[key]
			})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadRunConfigFromEnv: %v", err)
			}
			if got != tt.want {
				t.Fatalf("config = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestRunEvalCaseLiveModeUsesProvidedEndpoint(t *testing.T) {
	dir := filepath.Join("testdata", "cases", "001-basic-edit")
	manifest := loadManifest(t, filepath.Join(dir, "manifest.json"))
	turns := loadTurns(t, filepath.Join(dir, manifest.ModelTurnsFile))
	server := newFixtureServer(t, turns)
	defer server.Close()

	result, err := runEvalCase(t, dir, manifest, runConfig{
		Mode:    evalModeLive,
		APIKey:  "live-key",
		BaseURL: server.URL(),
		Model:   "live-model",
	})
	if err != nil {
		t.Fatalf("runEvalCase live mode: %v", err)
	}
	if result.ExitCode != manifest.Expect.ExitCode {
		t.Fatalf("exit code = %d, want %d", result.ExitCode, manifest.Expect.ExitCode)
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if server.index != len(turns) {
		t.Fatalf("consumed %d scripted turns, want %d", server.index, len(turns))
	}
	if len(server.requests) != len(turns) {
		t.Fatalf("request count = %d, want %d", len(server.requests), len(turns))
	}
	for i, req := range server.requests {
		if req.Model != "live-model" {
			t.Fatalf("request %d model = %q, want live-model", i, req.Model)
		}
	}
}

func TestCompareSemanticTrace(t *testing.T) {
	t.Parallel()

	expect := expectConfig{
		Commands:  []string{"printf 'hello\\n' > note.txt"},
		ExitCodes: []int{0},
	}

	t.Run("fixture mode keeps exact command matching", func(t *testing.T) {
		err := compareSemanticTrace(evalModeFixture, semanticTrace{
			Commands:  []string{"printf 'hello\\n' > ./note.txt"},
			ExitCodes: []int{0},
		}, expect)
		if err == nil || !strings.Contains(err.Error(), "command[0]") {
			t.Fatalf("error = %v, want command mismatch", err)
		}
	})

	t.Run("live mode ignores equivalent command text", func(t *testing.T) {
		err := compareSemanticTrace(evalModeLive, semanticTrace{
			Commands:  []string{"printf 'hello\\n' > ./note.txt"},
			ExitCodes: []int{0},
		}, expect)
		if err != nil {
			t.Fatalf("compareSemanticTrace: %v", err)
		}
	})

	t.Run("live mode still checks command count", func(t *testing.T) {
		err := compareSemanticTrace(evalModeLive, semanticTrace{
			Commands:  nil,
			ExitCodes: []int{0},
		}, expect)
		if err == nil || !strings.Contains(err.Error(), "command count") {
			t.Fatalf("error = %v, want command count mismatch", err)
		}
	})
}

func TestEvalCases(t *testing.T) {
	t.Parallel()

	cases := []string{
		"001-basic-edit",
	}
	for _, name := range cases {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join("testdata", "cases", name)
			manifest := loadManifest(t, filepath.Join(dir, "manifest.json"))
			cfg, err := loadRunConfigFromEnv(os.Getenv)
			if err != nil {
				t.Fatalf("loadRunConfigFromEnv: %v", err)
			}
			result, err := runEvalCase(t, dir, manifest, cfg)
			if err != nil {
				t.Fatalf("runEvalCase(%q): %v", name, err)
			}
			artifactRoot := filepath.Join(repoRoot(t), "eval", "artifacts", name)
			gotArtifacts := snapshotWorkspace(t, artifactRoot)
			wantArtifacts := map[string]string{
				"event-log.jsonl":     "",
				"trajectory.json":     "",
				"workspace/AGENTS.md": "",
				"workspace/note.txt":  "",
			}
			for path := range wantArtifacts {
				if readFile(t, filepath.Join(artifactRoot, path)) == "" {
					t.Fatalf("artifact %q is empty", path)
				}
			}
			for path := range gotArtifacts {
				gotArtifacts[path] = ""
			}
			if !equalWorkspace(wantArtifacts, gotArtifacts) {
				t.Fatalf("artifact set mismatch\nactual: %#v\nexpected: %#v", gotArtifacts, wantArtifacts)
			}
			if result.ExitCode != manifest.Expect.ExitCode {
				t.Fatalf("exit code = %d, want %d", result.ExitCode, manifest.Expect.ExitCode)
			}
			if manifest.Compare.SemanticTrace {
				if err := compareSemanticTrace(cfg.Mode, result.Trace, manifest.Expect); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func loadManifest(t *testing.T, path string) manifest {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest %q: %v", path, err)
	}
	var manifest manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse manifest %q: %v", path, err)
	}
	return manifest
}

func runEvalCase(t *testing.T, dir string, manifest manifest, cfg runConfig) (caseResult, error) {
	t.Helper()

	repoRoot := repoRoot(t)
	caseDir := absPath(t, dir)
	caseKey := filepath.Base(caseDir)
	tempRoot := t.TempDir()
	workspaceDir := filepath.Join(tempRoot, manifest.Cwd)
	homeDir := filepath.Join(tempRoot, "home")
	configDir := filepath.Join(tempRoot, "config")
	stateDir := filepath.Join(tempRoot, "state")
	artifactRoot := filepath.Join(repoRoot, "eval", "artifacts", caseKey)

	must(removeAllIfExists(artifactRoot))
	must(os.MkdirAll(stateDir, 0o755))

	copyTree(t, filepath.Join(caseDir, "input", "workspace"), workspaceDir)
	copyTreeOptional(t, filepath.Join(caseDir, "input", "home"), homeDir)
	copyTreeOptional(t, filepath.Join(caseDir, "input", "config"), configDir)
	copyProjectAgents(t, filepath.Join(caseDir, "input", "project"), workspaceDir)

	var server *fixtureServer
	if cfg.Mode == evalModeFixture {
		turns := loadTurns(t, filepath.Join(caseDir, manifest.ModelTurnsFile))
		server = newFixtureServer(t, turns)
		defer server.Close()
		cfg.BaseURL = server.URL()
	}

	binary := buildClnkuBinary(t, repoRoot, tempRoot)
	env := []string{
		"CLNKR_API_KEY=" + cfg.APIKey,
		"CLNKR_BASE_URL=" + cfg.BaseURL,
		"CLNKR_MODEL=" + cfg.Model,
		"HOME=" + homeDir,
		"XDG_CONFIG_HOME=" + configDir,
		"XDG_STATE_HOME=" + stateDir,
		"LC_ALL=C",
		"TZ=UTC",
		"PATH=" + os.Getenv("PATH"),
	}

	systemPromptStdout, systemPromptStderr, systemPromptExit := runCommand(t, workspaceDir, env, binary, "--dump-system-prompt")
	if systemPromptExit != 0 {
		return caseResult{}, fmt.Errorf("dump system prompt exit code = %d, stderr = %q", systemPromptExit, systemPromptStderr)
	}

	taskPath := filepath.Join(caseDir, manifest.TaskFile)
	taskBytes, err := os.ReadFile(taskPath)
	if err != nil {
		return caseResult{}, fmt.Errorf("read task %q: %w", taskPath, err)
	}

	eventLogPath := tempFilePath(t, tempRoot, "events-*.jsonl")
	trajectoryPath := tempFilePath(t, tempRoot, "messages-*.json")
	args := []string{
		"-p", strings.TrimSpace(string(taskBytes)),
		"--event-log", eventLogPath,
		"--trajectory", trajectoryPath,
		"--max-steps", fmt.Sprintf("%d", manifest.MaxSteps),
	}
	if manifest.FullSend {
		args = append(args, "--full-send")
	}
	if manifest.SeedMessagesFile != "" {
		seedSrc := filepath.Join(caseDir, manifest.SeedMessagesFile)
		seedDst := filepath.Join(tempRoot, "seed-messages.json")
		seedBytes, err := os.ReadFile(seedSrc)
		if err != nil {
			return caseResult{}, fmt.Errorf("read seed messages %q: %w", seedSrc, err)
		}
		seedReplacer := strings.NewReplacer(
			"__TMP__", tempRoot,
			"__WORKDIR__", workspaceDir,
			"__HOME__", homeDir,
			"__CONFIG__", configDir,
			"__STATE__", stateDir,
		)
		must(os.WriteFile(seedDst, []byte(seedReplacer.Replace(string(seedBytes))), 0o644))
		args = append(args, "--load-messages", seedDst)
	}

	_, _, exitCode := runCommand(t, workspaceDir, env, binary, args...)

	artifacts := loadArtifacts(t, systemPromptStdout, trajectoryPath, eventLogPath, workspaceDir)
	normalized := normalizeArtifacts(artifacts, normalizationMap(tempRoot, workspaceDir, homeDir, configDir, stateDir))
	writeTraceArtifacts(t, artifactRoot, normalized.Trajectory, normalized.EventLog)
	writeWorkspaceArtifacts(t, filepath.Join(artifactRoot, "workspace"), normalized.Workspace)

	trajectory := parseTrajectory(t, normalized.Trajectory)
	if server != nil {
		server.AssertRequests(t, normalized.SystemPrompt, normalizationMap(tempRoot, workspaceDir, homeDir, configDir, stateDir), trajectory)
	}

	expectedDir := filepath.Join(caseDir, "expected")
	events := parseEventLog(t, normalized.EventLog)
	if len(events) == 0 {
		t.Fatalf("event log is empty")
	}
	if manifest.Compare.Workspace {
		expectedWorkspace := snapshotWorkspace(t, filepath.Join(expectedDir, "workspace"))
		if !equalWorkspace(expectedWorkspace, normalized.Workspace) {
			t.Fatalf("workspace mismatch\nactual: %#v\nexpected: %#v", normalized.Workspace, expectedWorkspace)
		}
	}

	trace := extractTrace(t, normalized.EventLog)
	return caseResult{ExitCode: exitCode, Trace: trace}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func compareSemanticTrace(mode evalMode, got semanticTrace, want expectConfig) error {
	if len(got.Commands) != len(want.Commands) {
		return fmt.Errorf("command count = %d, want %d", len(got.Commands), len(want.Commands))
	}
	if mode == evalModeFixture {
		for i, command := range want.Commands {
			if got.Commands[i] != command {
				return fmt.Errorf("command[%d] = %q, want %q", i, got.Commands[i], command)
			}
		}
	}
	if len(got.ExitCodes) != len(want.ExitCodes) {
		return fmt.Errorf("exit code count = %d, want %d", len(got.ExitCodes), len(want.ExitCodes))
	}
	for i, exitCode := range want.ExitCodes {
		if got.ExitCodes[i] != exitCode {
			return fmt.Errorf("trace exit code[%d] = %d, want %d", i, got.ExitCodes[i], exitCode)
		}
	}
	return nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(cwd)
}

func absPath(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs path %q: %v", path, err)
	}
	return abs
}

func removeAllIfExists(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.RemoveAll(path)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	info, err := os.Stat(src)
	if err != nil {
		t.Fatalf("stat %q: %v", src, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", src)
	}
	if err := filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	}); err != nil {
		t.Fatalf("copy tree %q -> %q: %v", src, dst, err)
	}
}

func copyTreeOptional(t *testing.T, src, dst string) {
	t.Helper()
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("stat %q: %v", src, err)
	}
	copyTree(t, src, dst)
}

func copyProjectAgents(t *testing.T, srcDir, workspaceDir string) {
	t.Helper()
	src := filepath.Join(srcDir, "AGENTS.md")
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("stat %q: %v", src, err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %q: %v", src, err)
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", workspaceDir, err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "AGENTS.md"), data, 0o644); err != nil {
		t.Fatalf("write workspace AGENTS.md: %v", err)
	}
}

func loadTurns(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read model turns %q: %v", path, err)
	}
	var turns []string
	if err := json.Unmarshal(data, &turns); err != nil {
		t.Fatalf("parse model turns %q: %v", path, err)
	}
	return turns
}

func newFixtureServer(t *testing.T, turns []string) *fixtureServer {
	t.Helper()
	fs := &fixtureServer{turns: turns}
	fs.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var req openAIRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		fs.mu.Lock()
		defer fs.mu.Unlock()
		fs.requests = append(fs.requests, req)
		if fs.index >= len(fs.turns) {
			http.Error(w, "no more scripted turns", http.StatusInternalServerError)
			return
		}
		payload := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]string{
						"role":    "assistant",
						"content": fs.turns[fs.index],
					},
				},
			},
			"usage": map[string]int{
				"prompt_tokens":     1,
				"completion_tokens": 1,
			},
		}
		fs.index++
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Fatalf("encode fixture response: %v", err)
		}
	}))
	return fs
}

func (f *fixtureServer) Close() {
	f.server.Close()
}

func (f *fixtureServer) URL() string {
	return f.server.URL
}

func (f *fixtureServer) AssertRequests(t *testing.T, systemPrompt string, replacements []string, trajectory []trajectoryMessage) {
	t.Helper()
	replacer := strings.NewReplacer(replacements...)

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.index != len(f.turns) {
		t.Fatalf("consumed %d scripted turns, want %d", f.index, len(f.turns))
	}
	if len(f.requests) != len(f.turns) {
		t.Fatalf("request count = %d, want %d", len(f.requests), len(f.turns))
	}

	assistantIndices := make([]int, 0, len(f.turns))
	for i, message := range trajectory {
		if message.Role == "assistant" {
			assistantIndices = append(assistantIndices, i)
		}
	}
	if len(assistantIndices) != len(f.turns) {
		t.Fatalf("assistant message count = %d, want %d", len(assistantIndices), len(f.turns))
	}

	for i, req := range f.requests {
		if req.Model != "test-model" {
			t.Fatalf("request %d model = %q, want test-model", i, req.Model)
		}
		if len(req.Messages) == 0 {
			t.Fatalf("request %d has no messages", i)
		}
		if req.Messages[0].Role != "system" {
			t.Fatalf("request %d first role = %q, want system", i, req.Messages[0].Role)
		}
		if replacer.Replace(req.Messages[0].Content) != systemPrompt {
			t.Fatalf("request %d system prompt mismatch", i)
		}
		actual := normalizeMessages(req.Messages[1:], replacer)
		expected := trajectory[:assistantIndices[i]]
		if !equalMessages(actual, expected) {
			t.Fatalf("request %d transcript mismatch\nactual: %#v\nexpected: %#v", i, actual, expected)
		}
	}
}

func buildClnkuBinary(t *testing.T, repoRoot, tempRoot string) string {
	t.Helper()
	binary := filepath.Join(tempRoot, "clnku")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/clnku")
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build clnku: %v\n%s", err, output)
	}
	return binary
}

func runCommand(t *testing.T, cwd string, env []string, binary string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = cwd
	cmd.Env = env
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run %q: %v", strings.Join(append([]string{binary}, args...), " "), err)
		}
		exitCode = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), exitCode
}

func tempFilePath(t *testing.T, dir, pattern string) string {
	t.Helper()
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		t.Fatalf("create temp file %q in %q: %v", pattern, dir, err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		t.Fatalf("close temp file %q: %v", path, err)
	}
	return path
}

func loadArtifacts(t *testing.T, systemPrompt, trajectoryPath, eventLogPath, workspaceDir string) runArtifacts {
	t.Helper()
	return runArtifacts{
		SystemPrompt: systemPrompt,
		Trajectory:   readFile(t, trajectoryPath),
		EventLog:     readFile(t, eventLogPath),
		Workspace:    snapshotWorkspace(t, workspaceDir),
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(data)
}

func normalizeArtifacts(artifacts runArtifacts, replacements []string) runArtifacts {
	replacer := strings.NewReplacer(replacements...)
	workspace := make(map[string]string, len(artifacts.Workspace))
	for path, content := range artifacts.Workspace {
		workspace[replacer.Replace(path)] = replacer.Replace(content)
	}
	return runArtifacts{
		SystemPrompt: replacer.Replace(artifacts.SystemPrompt),
		Trajectory:   replacer.Replace(artifacts.Trajectory),
		EventLog:     replacer.Replace(artifacts.EventLog),
		Workspace:    workspace,
	}
}

func normalizationMap(tempRoot, workspaceDir, homeDir, configDir, stateDir string) []string {
	candidates := [][2]string{
		{workspaceDir, "<WORKDIR>"},
		{homeDir, "<HOME>"},
		{filepath.Join(configDir, "clnkr"), "<CONFIG>/clnkr"},
		{configDir, "<CONFIG>"},
		{stateDir, "<STATE>"},
		{tempRoot, "<TMP>"},
	}
	pairs := make([][2]string, 0, len(candidates)*2)
	pairs = append(pairs, candidates...)
	for _, candidate := range candidates {
		resolved, err := filepath.EvalSymlinks(candidate[0])
		if err != nil || resolved == candidate[0] {
			continue
		}
		pairs = append(pairs, [2]string{resolved, candidate[1]})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return len(pairs[i][0]) > len(pairs[j][0])
	})
	replacements := make([]string, 0, len(pairs)*2)
	seen := map[string]struct{}{}
	for _, pair := range pairs {
		key := pair[0] + "\x00" + pair[1]
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		replacements = append(replacements, pair[0], pair[1])
	}
	return replacements
}

func writeTraceArtifacts(t *testing.T, dir, trajectory, eventLog string) {
	t.Helper()
	must(os.MkdirAll(dir, 0o755))
	must(os.WriteFile(filepath.Join(dir, "trajectory.json"), []byte(trajectory), 0o644))
	must(os.WriteFile(filepath.Join(dir, "event-log.jsonl"), []byte(eventLog), 0o644))
}

func writeWorkspaceArtifacts(t *testing.T, dir string, workspace map[string]string) {
	t.Helper()
	must(os.MkdirAll(dir, 0o755))
	for path, content := range workspace {
		target := filepath.Join(dir, path)
		must(os.MkdirAll(filepath.Dir(target), 0o755))
		must(os.WriteFile(target, []byte(content), 0o644))
	}
}

func parseTrajectory(t *testing.T, data string) []trajectoryMessage {
	t.Helper()
	var messages []trajectoryMessage
	if err := json.Unmarshal([]byte(data), &messages); err != nil {
		t.Fatalf("parse trajectory: %v", err)
	}
	return messages
}

func parseEventLog(t *testing.T, data string) []logEvent {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(data), "\n")
	events := make([]logEvent, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event logEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("parse event log line %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func extractTrace(t *testing.T, eventLog string) semanticTrace {
	t.Helper()
	events := parseEventLog(t, eventLog)
	trace := semanticTrace{}
	for _, event := range events {
		if event.Type != "command_done" {
			continue
		}
		var payload commandDonePayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("parse command_done payload: %v", err)
		}
		trace.Commands = append(trace.Commands, payload.Command)
		trace.ExitCodes = append(trace.ExitCodes, payload.ExitCode)
	}
	return trace
}

func normalizeMessages(messages []trajectoryMessage, replacer *strings.Replacer) []trajectoryMessage {
	normalized := make([]trajectoryMessage, 0, len(messages))
	for _, message := range messages {
		normalized = append(normalized, trajectoryMessage{
			Role:    message.Role,
			Content: replacer.Replace(message.Content),
		})
	}
	return normalized
}

func snapshotWorkspace(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return files
		}
		t.Fatalf("stat workspace %q: %v", root, err)
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
		base := filepath.Base(rel)
		if base == ".gitkeep" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	}); err != nil {
		t.Fatalf("snapshot workspace %q: %v", root, err)
	}
	return files
}

func equalWorkspace(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for path, content := range left {
		if right[path] != content {
			return false
		}
	}
	return true
}

func equalMessages(left, right []trajectoryMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
