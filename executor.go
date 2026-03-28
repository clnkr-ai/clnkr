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
	Analysis     shellAnalysis
}

func (e *CommandExecutor) SetEnv(env map[string]string) {
	e.ExtraEnv = nil
	if len(env) == 0 {
		return
	}
	e.ExtraEnv = make(map[string]string, len(env))
	for k, v := range env {
		e.ExtraEnv[k] = v
	}
}

func (e *CommandExecutor) SetShellAnalysis(analysis shellAnalysis) {
	e.Analysis = analysis
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
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"PAGER=cat",
		"MANPAGER=cat",
		"GIT_PAGER=cat",
		"LESS=-R",
	)
	for key, value := range e.ExtraEnv {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	if stateFile != "" {
		cmd.Env = append(cmd.Env, "CLNKR_STATE_FILE="+stateFile)
	}
	baseEnv := envListToMap(cmd.Env)

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
		return e.applyPostState(result, stateFile, baseEnv)
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	}

	result, stateErr := e.applyPostState(result, stateFile, baseEnv)
	if stateErr != nil {
		return result, fmt.Errorf("run command: %w (shell state: %v)", err, stateErr)
	}
	return result, fmt.Errorf("run command: %w", err)
}

func (e *CommandExecutor) wrapCommand(command string) (string, string, func(), error) {
	if !e.Analysis.CaptureState {
		return command, "", func() {}, nil
	}
	stateFile, err := os.CreateTemp("", "clnkr-shell-state-")
	if err != nil {
		return "", "", nil, fmt.Errorf("create state file: %w", err)
	}
	if err := stateFile.Close(); err != nil {
		_ = os.Remove(stateFile.Name())
		return "", "", nil, fmt.Errorf("close state file: %w", err)
	}
	// env -0 is widely available on Linux/macOS and gives robust null-delimited output.
	wrapped := "trap 'clnkr_status=$?; trap - EXIT; { printf \"%s\\0\" \"$PWD\"; env -0; } > \"$CLNKR_STATE_FILE\"; exit $clnkr_status' EXIT\n" + command
	return wrapped, stateFile.Name(), func() { _ = os.Remove(stateFile.Name()) }, nil
}

func (e *CommandExecutor) applyPostState(result CommandResult, stateFile string, baseEnv map[string]string) (CommandResult, error) {
	if !e.Analysis.CaptureState || stateFile == "" {
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

	delta := make(map[string]string)
	for key, value := range captured {
		if base, ok := baseEnv[key]; !ok || base != value {
			delta[key] = value
		}
	}
	if len(delta) > 0 {
		result.PostEnv = delta
	}
	return result, nil
}

func envListToMap(list []string) map[string]string {
	env := make(map[string]string, len(list))
	for _, item := range list {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		env[key] = value
	}
	return env
}
