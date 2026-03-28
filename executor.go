package clnkr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// CommandExecutor shells out to bash.
type CommandExecutor struct {
	Timeout      time.Duration
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
	timeout := e.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	wrapped, stateFile, cleanup, err := e.wrapCommand(command)
	if err != nil {
		return CommandResult{}, fmt.Errorf("wrap command: %w", err)
	}
	defer cleanup()

	cmd := exec.CommandContext(ctx, "bash", "-c", wrapped)
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Dir = dir
	baseEnv := os.Environ()
	if e.ExtraEnv != nil {
		baseEnv = envMapToList(e.ExtraEnv)
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
		return e.applyPostState(result, stateFile)
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	}

	result, stateErr := e.applyPostState(result, stateFile)
	if stateErr != nil {
		return result, fmt.Errorf("run command: %w (shell state: %v)", err, stateErr)
	}
	return result, fmt.Errorf("run command: %w", err)
}

func (e *CommandExecutor) wrapCommand(command string) (string, string, func(), error) {
	stateFile, err := os.CreateTemp("", "clnkr-shell-state-")
	if err != nil {
		return "", "", nil, fmt.Errorf("create state file: %w", err)
	}
	if err := stateFile.Close(); err != nil {
		_ = os.Remove(stateFile.Name())
		return "", "", nil, fmt.Errorf("close state file: %w", err)
	}
	// Absolute paths: user command may have mutated PATH.
	wrapped := "trap 'clnkr_status=$?; trap - EXIT; { /usr/bin/printf \"%s\\0\" \"$PWD\"; /usr/bin/env -0; } > \"$CLNKR_STATE_FILE\"; exit $clnkr_status' EXIT\n" + command
	return wrapped, stateFile.Name(), func() { _ = os.Remove(stateFile.Name()) }, nil
}

func (e *CommandExecutor) applyPostState(result CommandResult, stateFile string) (CommandResult, error) {
	if stateFile == "" {
		return result, nil
	}
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return result, fmt.Errorf("read state file: %w", err)
	}
	if len(data) == 0 {
		return result, nil
	}
	parts := bytes.Split(data, []byte{0})
	if len(parts[0]) > 0 {
		result.PostCwd = string(parts[0])
	}
	captured := make(map[string]string, len(parts)-1)
	for _, part := range parts[1:] {
		if len(part) == 0 {
			continue
		}
		key, value, ok := strings.Cut(string(part), "=")
		if !ok {
			continue
		}
		captured[key] = value
	}
	delete(captured, "CLNKR_STATE_FILE")
	result.PostEnv = captured
	return result, nil
}

func envMapToList(env map[string]string) []string {
	list := make([]string, 0, len(env))
	for key, value := range env {
		list = append(list, key+"="+value)
	}
	return list
}
