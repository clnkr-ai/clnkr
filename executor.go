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
)

// CommandExecutor shells out to bash.
type CommandExecutor struct {
	Timeout      time.Duration
	ProcessGroup bool
}

// Execute runs a command in dir and returns separated stdout/stderr plus exit code.
func (e *CommandExecutor) Execute(ctx context.Context, command string, dir string) (CommandResult, error) {
	timeout := e.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"PAGER=cat",
		"MANPAGER=cat",
		"GIT_PAGER=cat",
		"LESS=-R",
	)

	if e.ProcessGroup {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	result := CommandResult{Command: command}
	err := cmd.Run()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()

	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}

	if err == nil {
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	}

	return result, fmt.Errorf("run command: %w", err)
}
