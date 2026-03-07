package hew

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// CommandExecutor shells out to bash.
type CommandExecutor struct {
	Timeout time.Duration
}

// Execute runs a command in dir and returns combined stdout/stderr.
func (e *CommandExecutor) Execute(ctx context.Context, command string, dir string) (string, error) {
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

	out, err := cmd.CombinedOutput()
	return string(out), err
}
