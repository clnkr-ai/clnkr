package evaluations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Harness builds clnku once (or locates a pre-installed binary) and runs
// evaluation trials against it.
type Harness struct {
	tempRoot   string
	trialsDir  string
	repoRoot   string
	evalsDir   string
	buildDir   string
	binaryPath string
}

// HarnessOption configures optional Harness behavior.
type HarnessOption func(*harnessOptions)

type harnessOptions struct {
	binaryPath string
	evalsDir   string
}

// WithBinary skips building clnku from source and uses the supplied binary
// path instead. If path is empty, the harness resolves "clnku" via PATH.
func WithBinary(path string) HarnessOption {
	return func(o *harnessOptions) {
		o.binaryPath = path
	}
}

// WithEvalsDir sets a custom evaluations directory instead of
// repoRoot/evaluations.
func WithEvalsDir(path string) HarnessOption {
	return func(o *harnessOptions) {
		o.evalsDir = path
	}
}

// RunArtifacts captures the raw outputs from one trial run.
type RunArtifacts struct {
	SuiteID               string
	TaskID                string
	TaskRoot              string
	TrialID               string
	SuiteTaskIndex        int
	TrialAttempt          int
	Mode                  Mode
	ProviderModel         string
	ProviderBaseURL       string
	StartedAt             time.Time
	FinishedAt            time.Time
	SystemPrompt          string
	Trajectory            string
	EventLog              string
	ProviderRequests      []CapturedRequest
	ProviderResponses     []string
	Workspace             map[string]string
	WorkspaceRoot         string
	HomeDir               string
	ConfigDir             string
	StateDir              string
	TempDir               string
	ExitCode              int
	TrialPassed           bool
	GraderResults         []GraderResult
	FailedRequiredGraders []GraderResult
	GitDiff               string
}

// NewHarness builds clnku once for reuse across trials.
// Pass WithBinary to skip building from source and use a pre-installed binary.
func NewHarness(ctx context.Context, repoRoot string, opts ...HarnessOption) (*Harness, error) {
	var o harnessOptions
	for _, opt := range opts {
		opt(&o)
	}

	tempRoot, err := os.MkdirTemp("", "clnkr-eval-harness-*")
	if err != nil {
		return nil, fmt.Errorf("create harness temp root: %w", err)
	}
	trialsDir := filepath.Join(tempRoot, "trials")
	if err := os.MkdirAll(trialsDir, 0o755); err != nil {
		_ = os.RemoveAll(tempRoot)
		return nil, fmt.Errorf("create harness trials dir: %w", err)
	}

	h := &Harness{
		tempRoot:  tempRoot,
		trialsDir: trialsDir,
		repoRoot:  repoRoot,
		evalsDir:  o.evalsDir,
	}

	if o.binaryPath != "" {
		// Explicit binary path provided.
		h.binaryPath = o.binaryPath
	} else if repoRoot == "" {
		// No repo root — resolve from PATH.
		resolved, err := exec.LookPath("clnku")
		if err != nil {
			_ = os.RemoveAll(tempRoot)
			return nil, fmt.Errorf("resolve clnku binary: %w", err)
		}
		h.binaryPath = resolved
	} else {
		// Build from source.
		buildDir := filepath.Join(tempRoot, "build")
		if err := os.MkdirAll(buildDir, 0o755); err != nil {
			_ = os.RemoveAll(tempRoot)
			return nil, fmt.Errorf("create harness build dir: %w", err)
		}
		h.buildDir = buildDir
		h.binaryPath = filepath.Join(buildDir, "clnku")
		if err := h.buildBinary(ctx); err != nil {
			_ = os.RemoveAll(tempRoot)
			return nil, err
		}
	}

	return h, nil
}

// Close removes harness-owned temporary files, including the built binary.
func (h *Harness) Close() error {
	if h == nil || h.tempRoot == "" {
		return nil
	}
	tempRoot := h.tempRoot
	h.tempRoot = ""
	h.trialsDir = ""
	h.buildDir = ""
	h.binaryPath = ""
	if err := os.RemoveAll(tempRoot); err != nil {
		return fmt.Errorf("remove harness temp root %q: %w", tempRoot, err)
	}
	return nil
}

// RunTrial materializes one task run and captures its raw artifacts.
func (h *Harness) RunTrial(ctx context.Context, suite Suite, task Task, cfg RunConfig) (RunArtifacts, error) {
	trialRoot, err := os.MkdirTemp(h.trialsDir, "trial-*")
	if err != nil {
		return RunArtifacts{}, fmt.Errorf("create trial root: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(trialRoot)
	}()

	startedAt := time.Now().UTC()
	taskRoot := resolveTaskRoot(h.repoRoot, h.evalsDir, suite.ID, task.ID)
	artifacts := RunArtifacts{
		SuiteID:        suite.ID,
		TaskID:         task.ID,
		TaskRoot:       taskRoot,
		TrialID:        filepath.Base(trialRoot),
		SuiteTaskIndex: suiteTaskIndex(suite, task.ID),
		TrialAttempt:   0,
		StartedAt:      startedAt,
	}

	mode := effectiveMode(suite, task, cfg)
	cfg, err = normalizeRunConfig(cfg, mode)
	if err != nil {
		return RunArtifacts{}, err
	}
	artifacts.Mode = mode

	inPlace := task.WorkingDirectory == "."
	var workspaceDir string
	if inPlace {
		workspaceDir = h.repoRoot
	} else {
		workspaceDir = filepath.Join(trialRoot, task.WorkingDirectory)
	}

	homeDir := filepath.Join(trialRoot, "home")
	configDir := filepath.Join(trialRoot, "config")
	stateDir := filepath.Join(trialRoot, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return RunArtifacts{}, fmt.Errorf("create state dir: %w", err)
	}

	if !inPlace {
		if err := copyTreeOptional(filepath.Join(taskRoot, "input", "workspace"), workspaceDir); err != nil {
			return RunArtifacts{}, fmt.Errorf("copy workspace input: %w", err)
		}
		if err := copyProjectAgents(filepath.Join(taskRoot, "input", "project"), workspaceDir); err != nil {
			return RunArtifacts{}, fmt.Errorf("copy project AGENTS: %w", err)
		}
	}
	if err := copyTreeOptional(filepath.Join(taskRoot, "input", "home"), homeDir); err != nil {
		return RunArtifacts{}, fmt.Errorf("copy home input: %w", err)
	}
	if err := copyTreeOptional(filepath.Join(taskRoot, "input", "config"), configDir); err != nil {
		return RunArtifacts{}, fmt.Errorf("copy config input: %w", err)
	}

	// Record git HEAD before the trial for in-place diff capture.
	var baseRef string
	if inPlace {
		headOut, _, headExit, headErr := runCommand(ctx, workspaceDir, nil, "git", "rev-parse", "HEAD")
		if headErr != nil || headExit != 0 {
			return RunArtifacts{}, fmt.Errorf("record git HEAD before trial: exit=%d err=%v", headExit, headErr)
		}
		baseRef = strings.TrimSpace(headOut)
	}

	var mockProvider *MockProvider
	if mode == ModeMockProvider {
		turns, err := loadTurns(filepath.Join(taskRoot, task.ScriptedTurnsFile))
		if err != nil {
			return RunArtifacts{}, fmt.Errorf("load mock turns: %w", err)
		}
		mockProvider = NewMockProvider(turns)
		defer mockProvider.Close()
		cfg.BaseURL = mockProvider.URL()
	}

	artifacts.ProviderModel = cfg.Model
	artifacts.ProviderBaseURL = cfg.BaseURL

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

	systemPrompt, stderrOut, exitCode, err := runCommand(ctx, workspaceDir, env, h.binaryPath, "--dump-system-prompt")
	if err != nil {
		return RunArtifacts{}, fmt.Errorf("dump system prompt: %w", err)
	}
	if exitCode != 0 {
		return RunArtifacts{}, fmt.Errorf("dump system prompt exit code %d: %s", exitCode, strings.TrimSpace(stderrOut))
	}
	artifacts.SystemPrompt = systemPrompt

	instructionBytes, err := os.ReadFile(filepath.Join(taskRoot, task.InstructionFile))
	if err != nil {
		return RunArtifacts{}, fmt.Errorf("read instruction file: %w", err)
	}

	eventLogPath, err := createTempFilePath(trialRoot, "events-*.jsonl")
	if err != nil {
		return RunArtifacts{}, fmt.Errorf("create event log path: %w", err)
	}
	trajectoryPath, err := createTempFilePath(trialRoot, "messages-*.json")
	if err != nil {
		return RunArtifacts{}, fmt.Errorf("create trajectory path: %w", err)
	}

	args := []string{
		"-p", strings.TrimSpace(string(instructionBytes)),
		"--event-log", eventLogPath,
		"--trajectory", trajectoryPath,
		"--max-steps", fmt.Sprintf("%d", task.StepLimit),
	}
	if task.FullSend {
		args = append(args, "--full-send")
	}
	if task.SeedTranscriptFile != "" {
		seedPath, err := writeSeedMessages(trialRoot, filepath.Join(taskRoot, task.SeedTranscriptFile), trialRoot, workspaceDir, homeDir, configDir, stateDir)
		if err != nil {
			return RunArtifacts{}, fmt.Errorf("prepare seed transcript: %w", err)
		}
		args = append(args, "--load-messages", seedPath)
	}

	_, _, exitCode, err = runCommand(ctx, workspaceDir, env, h.binaryPath, args...)
	if err != nil {
		return RunArtifacts{}, fmt.Errorf("run task: %w", err)
	}
	artifacts.ExitCode = exitCode

	trajectory, err := os.ReadFile(trajectoryPath)
	if err != nil {
		return RunArtifacts{}, fmt.Errorf("read trajectory: %w", err)
	}
	eventLog, err := os.ReadFile(eventLogPath)
	if err != nil {
		return RunArtifacts{}, fmt.Errorf("read event log: %w", err)
	}

	if inPlace {
		// Capture git diff against the recorded base ref.
		diffOut, _, diffExit, diffErr := runCommand(ctx, workspaceDir, nil, "git", "diff", baseRef)
		if diffErr != nil {
			return RunArtifacts{}, fmt.Errorf("capture git diff: %w", diffErr)
		}
		if diffExit != 0 {
			return RunArtifacts{}, fmt.Errorf("capture git diff exit code %d", diffExit)
		}
		artifacts.GitDiff = diffOut
		artifacts.Workspace = nil

		// Reset the workspace for the next trial.
		if _, _, rc, err := runCommand(ctx, workspaceDir, nil, "git", "reset", "--hard", baseRef); err != nil || rc != 0 {
			return RunArtifacts{}, fmt.Errorf("reset workspace to %s: exit=%d err=%v", baseRef, rc, err)
		}
		if _, _, rc, err := runCommand(ctx, workspaceDir, nil, "git", "clean", "-fd"); err != nil || rc != 0 {
			return RunArtifacts{}, fmt.Errorf("clean workspace: exit=%d err=%v", rc, err)
		}
	} else {
		workspace, err := snapshotWorkspace(workspaceDir)
		if err != nil {
			return RunArtifacts{}, fmt.Errorf("snapshot workspace: %w", err)
		}
		artifacts.Workspace = workspace
	}

	artifacts.Trajectory = string(trajectory)
	artifacts.EventLog = string(eventLog)
	artifacts.WorkspaceRoot = workspaceDir
	artifacts.HomeDir = homeDir
	artifacts.ConfigDir = configDir
	artifacts.StateDir = stateDir
	artifacts.TempDir = h.tempRoot
	if mockProvider != nil {
		artifacts.ProviderRequests = mockProvider.Requests()
		artifacts.ProviderResponses = collectProviderResponses(artifacts.ProviderRequests)
	}
	graderResults, policyResult, err := runTrialGraders(task, artifacts)
	if err != nil {
		return RunArtifacts{}, fmt.Errorf("run graders: %w", err)
	}
	artifacts.GraderResults = append([]GraderResult(nil), graderResults...)
	artifacts.TrialPassed = policyResult.Passed
	artifacts.FailedRequiredGraders = append([]GraderResult(nil), policyResult.FailedRequiredGraders...)
	artifacts.FinishedAt = time.Now().UTC()
	return artifacts, nil
}

func resolveTaskRoot(repoRoot, evalsDir, suiteID, taskID string) string {
	if evalsDir != "" {
		return filepath.Join(evalsDir, "suites", suiteID, "tasks", taskID)
	}
	return filepath.Join(repoRoot, "evaluations", "suites", suiteID, "tasks", taskID)
}

func (h *Harness) buildBinary(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "go", "build", "-o", h.binaryPath, "./cmd/clnku")
	cmd.Dir = h.repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build clnku: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func effectiveMode(suite Suite, task Task, cfg RunConfig) Mode {
	if cfg.Mode != "" {
		return cfg.Mode
	}
	if task.Mode != "" {
		return task.Mode
	}
	if suite.Mode != "" {
		return suite.Mode
	}
	return ModeMockProvider
}

func normalizeRunConfig(cfg RunConfig, mode Mode) (RunConfig, error) {
	switch mode {
	case ModeMockProvider:
		if cfg.APIKey == "" {
			cfg.APIKey = "dummy-key"
		}
		if cfg.Model == "" {
			cfg.Model = "test-model"
		}
		cfg.Mode = ModeMockProvider
		return cfg, nil
	case ModeLiveProvider:
		if cfg.Model == "" {
			cfg.Model = "gpt-5.4-nano"
		}
		if cfg.APIKey == "" {
			return RunConfig{}, fmt.Errorf("normalize run config: live-provider mode missing API key")
		}
		if cfg.BaseURL == "" {
			return RunConfig{}, fmt.Errorf("normalize run config: live-provider mode missing base URL")
		}
		cfg.Mode = ModeLiveProvider
		return cfg, nil
	default:
		return RunConfig{}, fmt.Errorf("normalize run config: unsupported mode %q", mode)
	}
}

func runCommand(ctx context.Context, cwd string, env []string, binary string, args ...string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = cwd
	cmd.Env = env

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), stderr.String(), exitErr.ExitCode(), nil
	}
	return stdout.String(), stderr.String(), 0, fmt.Errorf("run %q: %w", strings.Join(append([]string{binary}, args...), " "), err)
}

func createTempFilePath(dir, pattern string) (string, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("create temp file %q in %q: %w", pattern, dir, err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close temp file %q: %w", path, err)
	}
	return path, nil
}

func writeSeedMessages(dstRoot, srcPath, tempRoot, workspaceDir, homeDir, configDir, stateDir string) (string, error) {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return "", fmt.Errorf("read seed transcript %q: %w", srcPath, err)
	}

	replacer := strings.NewReplacer(
		"__TMP__", tempRoot,
		"__WORKDIR__", workspaceDir,
		"__HOME__", homeDir,
		"__CONFIG__", configDir,
		"__STATE__", stateDir,
	)

	seedPath := filepath.Join(dstRoot, "seed-messages.json")
	if err := os.WriteFile(seedPath, []byte(replacer.Replace(string(data))), 0o644); err != nil {
		return "", fmt.Errorf("write seed transcript %q: %w", seedPath, err)
	}
	return seedPath, nil
}

func loadTurns(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read model turns %q: %w", path, err)
	}

	var turns []string
	if err := json.Unmarshal(data, &turns); err != nil {
		return nil, fmt.Errorf("parse model turns %q: %w", path, err)
	}
	return turns, nil
}

func copyTreeOptional(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat %q: %w", src, err)
	}
	return copyTree(src, dst)
}

func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat %q: %w", src, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", src)
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
		return fmt.Errorf("copy tree %q -> %q: %w", src, dst, err)
	}
	return nil
}

func copyProjectAgents(srcDir, workspaceDir string) error {
	src := filepath.Join(srcDir, "AGENTS.md")
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat %q: %w", src, err)
	}

	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %q: %w", src, err)
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", workspaceDir, err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "AGENTS.md"), data, 0o644); err != nil {
		return fmt.Errorf("write workspace AGENTS.md: %w", err)
	}
	return nil
}

func snapshotWorkspace(root string) (map[string]string, error) {
	files := map[string]string{}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return files, nil
		}
		return nil, fmt.Errorf("stat workspace %q: %w", root, err)
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
		if filepath.Base(rel) == ".gitkeep" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walk workspace %q: %w", root, err)
	}
	return files, nil
}

func collectProviderResponses(requests []CapturedRequest) []string {
	responses := make([]string, 0, len(requests))
	for _, request := range requests {
		if request.RawResponse == "" {
			continue
		}
		responses = append(responses, request.RawResponse)
	}
	return responses
}

func suiteTaskIndex(suite Suite, taskID string) int {
	for i, id := range suite.Tasks {
		if id == taskID {
			return i
		}
	}
	return 0
}

func (artifacts RunArtifacts) normalizationRoots() normalizationRoots {
	return normalizationRoots{
		Workdir: artifacts.WorkspaceRoot,
		Home:    artifacts.HomeDir,
		Config:  artifacts.ConfigDir,
		State:   artifacts.StateDir,
		Temp:    artifacts.TempDir,
	}
}
