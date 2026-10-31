package clnkr

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/clnkr-ai/clnkr/internal/core/transcript"
)

type actTurnExecutor struct {
	agent *Agent
}

const timeoutGuidance = "The previous command timed out. Use a narrower command, inspect partial output if available, or choose a cheaper verification path. Do not rerun the same long command unchanged."

func (e actTurnExecutor) Execute(
	ctx context.Context,
	act *ActTurn,
	skipped []BashAction,
) (ExecutionResult, error) {
	a := e.agent
	if act == nil || len(act.Bash.Commands) == 0 {
		return ExecutionResult{}, fmt.Errorf("execute act turn: %w", ErrMissingCommand)
	}

	execCount := 0
	for i, action := range act.Bash.Commands {
		result := e.executeCommand(ctx, action)
		execCount++
		if commandOutcomeIsError(result.Outcome) {
			for _, notRun := range act.Bash.Commands[i+1:] {
				e.agent.appendSkippedToolResult(
					notRun,
					"previous command failed",
				)
			}
			break
		}
	}

	for _, action := range skipped {
		a.appendSkippedToolResult(action, "max steps")
	}
	a.appendStateMessageIfNeeded()
	return ExecutionResult{CommandsExecuted: execCount}, nil
}

func (e actTurnExecutor) executeCommand(ctx context.Context, action BashAction) CommandResult {
	a := e.agent
	if setter, ok := a.executor.(ExecutorStateSetter); ok {
		setter.SetEnv(a.env)
	}

	execDir := e.executionDir(action)
	a.notify(EventCommandStart{Command: action.Command, Dir: execDir})

	execResult := normalizeExecutorResult(a.executor.Execute(ctx, action.Command, execDir))
	e.applyPostCommandState(execResult)
	payload := e.formatCommandResult(action.Command, execResult)
	a.notify(EventCommandDone{
		Command:  action.Command,
		Stdout:   execResult.Stdout,
		Stderr:   execResult.Stderr,
		Outcome:  execResult.Outcome,
		Feedback: execResult.Feedback,
	})
	e.appendCommandMessage(action, execResult, payload)
	return execResult
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

func (e actTurnExecutor) formatCommandResult(command string, result CommandResult) string {
	guidance := ""
	if result.Outcome.Type == CommandOutcomeTimeout {
		guidance = timeoutGuidance
	}
	return transcript.FormatCommandResult(transcript.CommandResult{
		Command:  command,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		Outcome:  result.Outcome,
		Feedback: result.Feedback,
		Guidance: guidance,
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
			IsError: commandOutcomeIsError(result.Outcome),
		}
	}
	e.agent.messages = append(e.agent.messages, msg)
}

func normalizeExecutorResult(result CommandResult) CommandResult {
	outcome := result.Outcome
	switch outcome.Type {
	case CommandOutcomeExit:
		if outcome.ExitCode == nil {
			result.Outcome = commandOutcomeError(
				fmt.Errorf("executor returned exit outcome without exit code"),
			)
			return result
		}
		result.Outcome = commandOutcomeExit(*outcome.ExitCode)
	case CommandOutcomeTimeout, CommandOutcomeCancelled, CommandOutcomeDenied:
		result.Outcome = CommandOutcome{Type: outcome.Type}
	case CommandOutcomeSkipped:
		result.Outcome = CommandOutcome{Type: outcome.Type, Reason: outcome.Reason}
	case CommandOutcomeError:
		result.Outcome = CommandOutcome{Type: outcome.Type, Message: outcome.Message}
	case "":
		result.Outcome = commandOutcomeError(fmt.Errorf("executor returned no command outcome"))
	default:
		result.Outcome = commandOutcomeError(
			fmt.Errorf("executor returned unknown command outcome %q", outcome.Type),
		)
	}
	return result
}
