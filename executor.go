package clnkr

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/clnkr-ai/clnkr/internal/core/gitfeedback"
	"github.com/clnkr-ai/clnkr/internal/core/shellstate"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// CommandExecutor shells out to bash.
type CommandExecutor struct {
	ProcessGroup bool
	ExtraEnv     map[string]string
}

func (e *CommandExecutor) SetEnv(env map[string]string) {
	if env == nil {
		e.ExtraEnv = nil
		return
	}
	e.ExtraEnv = make(map[string]string, len(env))
	for k, v := range env {
		e.ExtraEnv[k] = v
	}
}

// Execute runs a command in dir and returns separated stdout/stderr plus exit code.
func (e *CommandExecutor) Execute(ctx context.Context, command string, dir string) (CommandResult, error) {
	baseline := gitfeedback.Detect(dir)

	wrapped, stateFile, cleanup, err := shellstate.Wrap(command)
	if err != nil {
		return CommandResult{}, fmt.Errorf("wrap command: %w", err)
	}
	defer cleanup()

	cmd := exec.CommandContext(ctx, "bash", "-c", wrapped)
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Dir = dir
	baseEnv := os.Environ()
	if e.ExtraEnv != nil {
		baseEnv = shellstate.EnvMapToList(e.ExtraEnv)
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

	if e.ProcessGroup {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

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

	if err == nil {
		result, stateErr := e.applyPostState(result, stateFile)
		if stateErr != nil {
			return result, stateErr
		}
		return applyCommandFeedback(result, baseline, dir), nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	}

	result, stateErr := e.applyPostState(result, stateFile)
	if stateErr != nil {
		return result, fmt.Errorf("run command: %w (shell state: %v)", err, stateErr)
	}
	result = applyCommandFeedback(result, baseline, dir)
	return result, fmt.Errorf("run command: %w", err)
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
