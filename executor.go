package clnkr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/clnkr-ai/clnkr/internal/core/gitfeedback"
	"github.com/clnkr-ai/clnkr/internal/core/shellstate"
)

// CommandExecutor runs bash commands, captures post-command shell state, and
// always runs commands in their own process group.
type CommandExecutor struct {
	// BaseEnv, when non-nil, is the complete base environment snapshot for
	// command execution. Direct mutation is caller-owned.
	BaseEnv map[string]string
}

// SetEnv copies a full environment snapshot for future command execution.
func (e *CommandExecutor) SetEnv(env map[string]string) {
	if env == nil {
		e.BaseEnv = nil
		return
	}
	e.BaseEnv = make(map[string]string, len(env))
	for k, v := range env {
		e.BaseEnv[k] = v
	}
}

// Execute runs a command in dir and returns separated stdout/stderr plus exit code.
func (e *CommandExecutor) Execute(ctx context.Context, command string, dir string) (CommandResult, error) {
	baseline := gitfeedback.Detect(dir)

	wrapped, stateFile, cleanup, err := shellstate.Wrap(command)
	if err != nil {
		return CommandResult{Command: command, Outcome: commandOutcomeError(err)}, fmt.Errorf("wrap command: %w", err)
	}
	defer cleanup()

	cmd := exec.CommandContext(ctx, "bash", "-c", wrapped)
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Dir = dir
	baseEnv := os.Environ()
	if e.BaseEnv != nil {
		baseEnv = shellstate.EnvMapToList(e.BaseEnv)
	}
	cmd.Env = append([]string{}, baseEnv...)
	// Appended last; exec uses last-wins for duplicate keys.
	cmd.Env = append(cmd.Env,
		"PAGER=cat",
		"MANPAGER=cat",
		"GIT_PAGER=cat",
		"LESS=-R",
	)
	if stateFile != "" {
		cmd.Env = append(cmd.Env, "CLNKR_STATE_FILE="+stateFile)
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	result := CommandResult{Command: command}
	err = cmd.Run()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()

	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	}
	result.Outcome = classifyCommandOutcome(ctx, err, result.ExitCode)

	if err == nil {
		result, stateErr := e.applyPostState(result, stateFile)
		if stateErr != nil {
			result.Outcome = commandOutcomeError(stateErr)
			return result, stateErr
		}
		return applyCommandFeedback(result, baseline, dir), nil
	}

	result, stateErr := e.applyPostState(result, stateFile)
	if stateErr != nil {
		return result, fmt.Errorf("run command: %w (shell state: %v)", err, stateErr)
	}
	result = applyCommandFeedback(result, baseline, dir)
	return result, fmt.Errorf("run command: %w", err)
}

func classifyCommandOutcome(ctx context.Context, err error, exitCode int) CommandOutcome {
	if err == nil {
		return commandOutcomeExit(exitCode)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return CommandOutcome{Type: CommandOutcomeTimeout}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return CommandOutcome{Type: CommandOutcomeCancelled}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return commandOutcomeExit(exitCode)
	}
	return commandOutcomeError(err)
}

func commandOutcomeExit(code int) CommandOutcome {
	return CommandOutcome{Type: CommandOutcomeExit, ExitCode: &code}
}

func commandOutcomeError(err error) CommandOutcome {
	message := ""
	if err != nil {
		message = err.Error()
	}
	return CommandOutcome{Type: CommandOutcomeError, Message: message}
}

func (e *CommandExecutor) applyPostState(result CommandResult, stateFile string) (CommandResult, error) {
	snapshot, err := shellstate.Load(stateFile)
	if err != nil {
		return result, err
	}
	result.PostCwd = snapshot.Cwd
	result.PostEnv = snapshot.Env
	return result, nil
}

func applyCommandFeedback(result CommandResult, baseline gitfeedback.Baseline, dir string) CommandResult {
	finalCwd := dir
	if result.PostCwd != "" {
		finalCwd = result.PostCwd
	}

	summary, ok := baseline.Collect(finalCwd)
	if !ok {
		return result
	}
	result.Feedback = CommandFeedback{
		ChangedFiles: summary.ChangedFiles,
		Diff:         summary.Diff,
	}
	return result
}
