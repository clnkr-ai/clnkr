package clnkr

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/clnkr-ai/clnkr/internal/core/transcript"
)

type actTurnExecutor struct {
	agent *Agent
}

func (e actTurnExecutor) Execute(
	ctx context.Context,
	act *ActTurn,
	skipped []BashAction,
) (StepResult, error) {
	a := e.agent
	if act == nil || len(act.Bash.Commands) == 0 {
		return StepResult{Turn: act}, fmt.Errorf("execute act turn: %w", ErrMissingCommand)
	}

	outputs, execCount := make([]string, 0, len(act.Bash.Commands)), 0
	var execErr error

	for i, action := range act.Bash.Commands {
		payload, commandErr := e.executeCommand(ctx, action)
		execCount++

		outputs = append(outputs, payload)
		if execErr = commandErr; execErr != nil {
			for _, notRun := range act.Bash.Commands[i+1:] {
				if payload, ok := e.agent.appendSkippedToolResult(
					notRun,
					"previous command failed",
				); ok {
					outputs = append(outputs, payload)
				}
			}
			break
		}
	}

	for _, action := range skipped {
		if payload, ok := a.appendSkippedToolResult(action, "max steps"); ok {
			outputs = append(outputs, payload)
		}
	}
	a.appendStateMessageIfNeeded()
	return StepResult{
		Turn:      act,
		Output:    strings.Join(outputs, "\n\n"),
		ExecErr:   execErr,
		ExecCount: execCount,
	}, nil
}

func (e actTurnExecutor) executeCommand(ctx context.Context, action BashAction) (string, error) {
	a := e.agent
	if setter, ok := a.executor.(ExecutorStateSetter); ok {
		setter.SetEnv(a.env)
	}

	execDir := e.executionDir(action)
	a.notify(EventCommandStart{Command: action.Command, Dir: execDir})

	execResult, commandErr := a.executor.Execute(ctx, action.Command, execDir)
	e.applyPostCommandState(execResult)
	payload := e.formatCommandResult(execResult)
	if commandErr != nil {
		a.notify(EventDebug{Message: fmt.Sprintf("command error: %v", commandErr)})
	}
	a.notify(EventCommandDone{
		Command:  action.Command,
		Stdout:   execResult.Stdout,
		Stderr:   execResult.Stderr,
		ExitCode: execResult.ExitCode,
		Feedback: execResult.Feedback,
		Err:      commandErr,
	})
	e.appendCommandMessage(action, execResult, payload)
	return payload, commandErr
}

func (e actTurnExecutor) executionDir(action BashAction) string {
	if action.Workdir == "" {
		return e.agent.cwd
	}
	if filepath.IsAbs(action.Workdir) {
		return action.Workdir
	}
	return filepath.Join(e.agent.cwd, action.Workdir)
}

func (e actTurnExecutor) applyPostCommandState(result CommandResult) {
	a := e.agent
	if result.PostCwd != "" {
		a.cwd = result.PostCwd
	}
	// PostEnv is a full next-turn snapshot; nil means no new snapshot captured.
	if result.PostEnv != nil {
		a.env = result.PostEnv
	}
	a.notify(EventDebug{Message: fmt.Sprintf("cwd: %s", a.cwd)})
}

func (e actTurnExecutor) formatCommandResult(result CommandResult) string {
	return transcript.FormatCommandResult(transcript.CommandResult{
		Command:  result.Command,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
		Outcome:  result.Outcome,
		Feedback: result.Feedback,
	})
}

func (e actTurnExecutor) appendCommandMessage(
	action BashAction,
	result CommandResult,
	payload string,
) {
	msg := Message{Role: "user", Content: payload}
	if action.ID != "" {
		msg.BashToolResult = &BashToolResult{
			ID:      action.ID,
			Content: payload,
			IsError: commandResultIsError(result),
		}
	}
	e.agent.messages = append(e.agent.messages, msg)
}
