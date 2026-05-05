package delegation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	DefaultMaxDepth    = 1
	DefaultMaxChildren = 3
	DefaultMaxCommands = 10
	DefaultTimeout     = 10 * time.Minute
)

type Status string

const (
	StatusDone      Status = "done"
	StatusFailed    Status = "failed"
	StatusTimeout   Status = "timeout"
	StatusCancelled Status = "cancelled"
)

type Config struct {
	Enabled     bool
	MaxDepth    int
	MaxChildren int
	MaxCommands int
	Timeout     time.Duration
	ArtifactDir string
}

func DefaultConfig() Config {
	return Config{
		MaxDepth:    DefaultMaxDepth,
		MaxChildren: DefaultMaxChildren,
		MaxCommands: DefaultMaxCommands,
		Timeout:     DefaultTimeout,
	}
}

type Request struct {
	ChildID     string `json:"child_id"`
	ParentCwd   string `json:"parent_cwd"`
	Task        string `json:"task"`
	Depth       int    `json:"depth"`
	MaxCommands int    `json:"max_commands"`
	Timeout     string `json:"timeout"`
	ArtifactDir string `json:"artifact_dir"`
}

type Artifacts struct {
	Input      string `json:"input,omitempty"`
	EventLog   string `json:"event_log,omitempty"`
	Trajectory string `json:"trajectory,omitempty"`
	Result     string `json:"result,omitempty"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
}

type Result struct {
	ChildID      string    `json:"child_id"`
	Status       Status    `json:"status"`
	Summary      string    `json:"summary"`
	Artifacts    Artifacts `json:"artifacts"`
	ErrorMessage string    `json:"error,omitempty"`
}

type Runner interface {
	RunChildProbe(context.Context, Request) (Result, error)
}

func PrepareRequest(parentCwd, task string, cfg Config, childCount int) (Request, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return Request{}, fmt.Errorf("delegate command: task is required")
	}
	if cfg.MaxDepth < 1 {
		return Request{}, fmt.Errorf("delegate command: max depth must be at least 1")
	}
	if cfg.MaxChildren < 1 {
		return Request{}, fmt.Errorf("delegate command: max children must be at least 1")
	}
	if childCount >= cfg.MaxChildren {
		return Request{}, fmt.Errorf("delegate command: child limit reached (%d)", cfg.MaxChildren)
	}
	if cfg.MaxCommands < 1 {
		return Request{}, fmt.Errorf("delegate command: max commands must be at least 1")
	}
	if cfg.Timeout <= 0 {
		return Request{}, fmt.Errorf("delegate command: timeout must be positive")
	}

	childID, err := newChildID(childCount + 1)
	if err != nil {
		return Request{}, err
	}
	artifactDir := cfg.ArtifactDir
	if artifactDir == "" {
		artifactDir = filepath.Join(os.TempDir(), "clnkr-delegates")
	}
	return Request{
		ChildID:     childID,
		ParentCwd:   parentCwd,
		Task:        task,
		Depth:       1,
		MaxCommands: cfg.MaxCommands,
		Timeout:     cfg.Timeout.String(),
		ArtifactDir: filepath.Join(artifactDir, childID),
	}, nil
}

type ExecRunner struct {
	Executable string
	BaseArgs   []string
	Env        []string
}

func (r ExecRunner) RunChildProbe(ctx context.Context, req Request) (Result, error) {
	if r.Executable == "" {
		return Result{}, fmt.Errorf("child probe runner: executable is required")
	}
	if err := os.MkdirAll(req.ArtifactDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create child artifact dir: %w", err)
	}

	artifacts := artifactPaths(req.ArtifactDir)
	if err := writeJSONFile(artifacts.Input, req); err != nil {
		return Result{}, err
	}

	timeout, err := time.ParseDuration(req.Timeout)
	if err != nil {
		return Result{}, fmt.Errorf("parse child timeout: %w", err)
	}
	childCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := execCommandContext(childCtx, r.Executable, r.childArgs(req, artifacts)...)
	cmd.Dir = req.ParentCwd
	if len(r.Env) > 0 {
		cmd.Env = append(os.Environ(), r.Env...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	stderrFile, err := os.Create(artifacts.Stderr)
	if err != nil {
		return Result{}, fmt.Errorf("create child stderr artifact: %w", err)
	}
	defer stderrFile.Close() //nolint:errcheck
	cmd.Stderr = stderrFile

	output, runErr := cmd.Output()
	if err := os.WriteFile(artifacts.Stdout, output, 0o644); err != nil {
		return Result{}, fmt.Errorf("write child stdout artifact: %w", err)
	}
	result := Result{
		ChildID:   req.ChildID,
		Status:    statusForError(childCtx, runErr),
		Summary:   lastDoneSummary(artifacts.EventLog),
		Artifacts: artifacts,
	}
	if result.Summary == "" && runErr != nil {
		result.Summary = "child probe failed"
	}
	if runErr != nil {
		result.ErrorMessage = runErr.Error()
	}
	if err := writeJSONFile(artifacts.Result, result); err != nil {
		return Result{}, err
	}
	if runErr != nil {
		return result, fmt.Errorf("child probe %s: %w", req.ChildID, runErr)
	}
	return result, nil
}

var execCommandContext = exec.CommandContext

func (r ExecRunner) childArgs(req Request, artifacts Artifacts) []string {
	args := append([]string{}, r.BaseArgs...)
	return append(args,
		"--delegate-child-read-only",
		"--delegate-depth", fmt.Sprint(req.Depth),
		"--max-steps", fmt.Sprint(req.MaxCommands),
		"--event-log", artifacts.EventLog,
		"--trajectory", artifacts.Trajectory,
		"--system-prompt-append", childPromptAppend(req),
		"-p", req.Task,
	)
}

func artifactPaths(dir string) Artifacts {
	return Artifacts{
		Input:      filepath.Join(dir, "input.json"),
		EventLog:   filepath.Join(dir, "event-log.jsonl"),
		Trajectory: filepath.Join(dir, "trajectory.json"),
		Result:     filepath.Join(dir, "result.json"),
		Stdout:     filepath.Join(dir, "stdout.txt"),
		Stderr:     filepath.Join(dir, "stderr.txt"),
	}
}

func newChildID(n int) (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("create child id: %w", err)
	}
	return fmt.Sprintf("child-%03d-%s", n, hex.EncodeToString(b[:])), nil
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func statusForError(ctx context.Context, err error) Status {
	if err == nil {
		return StatusDone
	}
	if ctx.Err() == context.DeadlineExceeded {
		return StatusTimeout
	}
	if ctx.Err() == context.Canceled {
		return StatusCancelled
	}
	return StatusFailed
}

func childPromptAppend(req Request) string {
	return fmt.Sprintf(`<child-probe>
You are a bounded child probe for clnkr.
Answer only this delegated task: %s
Mode: read-only.
Do not edit files in read-only mode.
Do not spawn child agents.
Report concise evidence, files inspected, confidence, and uncertainty.
Your output is advisory; the parent must verify it before acting.
</child-probe>`, req.Task)
}

func lastDoneSummary(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var event struct {
			Type    string `json:"type"`
			Payload struct {
				Turn json.RawMessage `json:"turn"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(lines[i]), &event); err != nil || event.Type != "response" {
			continue
		}
		var turn struct {
			Type    string `json:"type"`
			Summary string `json:"summary"`
		}
		if err := json.Unmarshal(event.Payload.Turn, &turn); err == nil && turn.Type == "done" {
			return turn.Summary
		}
	}
	return ""
}
