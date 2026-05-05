package transcript

import (
	"encoding/json"
	"strconv"
	"strings"
)

const maxCommandStreamBytes = 64 * 1024

// FormatCommandResult renders a command result as structured shell output.
func FormatCommandResult(result CommandResult) string {
	payload := struct {
		Command  string           `json:"command,omitempty"`
		Stdout   string           `json:"stdout"`
		Stderr   string           `json:"stderr"`
		Outcome  CommandOutcome   `json:"outcome"`
		Feedback *CommandFeedback `json:"feedback,omitempty"`
	}{
		Command: result.Command,
		Stdout:  truncateCommandStream("stdout", result.Stdout),
		Stderr:  truncateCommandStream("stderr", result.Stderr),
		Outcome: normalizedOutcome(result.Outcome, result.ExitCode),
	}
	if len(result.Feedback.ChangedFiles) > 0 || result.Feedback.Diff != "" {
		payload.Feedback = &result.Feedback
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return `{"stdout":"","stderr":"failed to marshal command result","outcome":{"type":"error","message":"failed to marshal command result"}}`
	}
	return string(body)
}

func truncateCommandStream(name string, stream string) string {
	if len(stream) <= maxCommandStreamBytes {
		return stream
	}
	omitted := len(stream) - maxCommandStreamBytes + len("\n[clnkr: "+name+" truncated; omitted  bytes]\n")
	digits := len(strconv.Itoa(omitted))
	if len(strconv.Itoa(omitted+digits)) > digits {
		digits++
	}
	marker := "\n[clnkr: " + name + " truncated; omitted " + strconv.Itoa(omitted+digits) + " bytes]\n"
	keep := maxCommandStreamBytes - len(marker)
	head := keep / 2
	return stream[:head] + marker + stream[len(stream)-(keep-head):]
}

func FormatDeniedCommandResult(reply string) string {
	stderr := "Command was not run because the user denied approval."
	if trimmed := strings.TrimSpace(reply); trimmed != "" {
		stderr += "\nUser guidance: " + trimmed
	}
	return FormatCommandResult(CommandResult{Stderr: stderr, Outcome: CommandOutcome{Type: CommandOutcomeDenied}})
}

func FormatSkippedCommandResult(reason string) string {
	reason = strings.TrimSpace(reason)
	outcomeReason := "skipped"
	stderr := "Command was not run."
	switch reason {
	case "max steps":
		outcomeReason = "max_steps"
		stderr = "Command was not run because the step limit was reached."
	case "previous command failed":
		outcomeReason = "previous_command_failed"
		stderr = "Command was not run because a previous command failed."
	case "":
	default:
		stderr += "\nReason: " + reason
	}
	return FormatCommandResult(CommandResult{Stderr: stderr, Outcome: CommandOutcome{Type: CommandOutcomeSkipped, Reason: outcomeReason}})
}

func normalizedOutcome(outcome CommandOutcome, fallbackExitCode int) CommandOutcome {
	if outcome.Type == "" {
		return exitOutcome(fallbackExitCode)
	}
	if outcome.Type == CommandOutcomeExit && outcome.ExitCode == nil {
		code := fallbackExitCode
		outcome.ExitCode = &code
	}
	return outcome
}

func exitOutcome(code int) CommandOutcome {
	return CommandOutcome{Type: CommandOutcomeExit, ExitCode: &code}
}
