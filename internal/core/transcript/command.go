package transcript

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

const commandStreamBudget = 64 * 1024

var salientPattern = regexp.MustCompile(`(?i)(error|failed|failure|panic|exception|traceback|fatal|undefined|cannot|no such file|permission denied|denied|timeout|killed|segmentation fault|failures|expected|actual|assertion|FAIL|FAILED|--- FAIL|:[0-9]+:)`)

// FormatCommandResult renders a command result as structured shell output.
func FormatCommandResult(result CommandResult) string {
	outcome := normalizedOutcome(result.Outcome, result.ExitCode)
	diagnostic := outcome.Type != CommandOutcomeExit || outcome.ExitCode != nil && *outcome.ExitCode != 0
	stdout, stdoutMeta := compressCommandStream("stdout", result.Stdout, diagnostic && result.Stderr == "")
	stderr, stderrMeta := compressCommandStream("stderr", result.Stderr, diagnostic)
	payload := map[string]any{"stdout": stdout, "stderr": stderr, "outcome": outcome}
	if result.Command != "" {
		payload["command"] = result.Command
	}
	if len(result.Feedback.ChangedFiles) > 0 || result.Feedback.Diff != "" {
		payload["feedback"] = result.Feedback
	}
	if stdoutMeta != nil || stderrMeta != nil {
		payload["observation"] = map[string]any{"source": "clnkr", "version": 1, "stdout": stdoutMeta, "stderr": stderrMeta}
	}
	body, _ := json.Marshal(payload)
	return string(body)
}

func compressCommandStream(name, stream string, salient bool) (string, map[string]any) {
	if len(stream) <= commandStreamBudget {
		return stream, nil
	}
	marker := "[clnkr: " + name + " compressed; original " + strconv.Itoa(len(stream)) + " bytes, omitted about " + strconv.Itoa(len(stream)-commandStreamBudget) + " bytes]\n"
	keep := max(0, commandStreamBudget-len(marker)-len("\n[head]\n\n[tail]\n\n[salient]\n"))
	head, tail := keep/3, keep/3
	middle := salientLines(stream[head:len(stream)-tail], keep-head-tail, salient)
	shown := marker + "[head]\n" + stream[:head]
	if middle != "" {
		shown += "\n[salient]\n" + middle
	}
	shown += "\n[tail]\n" + stream[len(stream)-tail:]
	shown = strings.ToValidUTF8(shown, "")
	return shown, map[string]any{"original_bytes": len(stream), "shown_bytes": len(shown), "omitted_bytes": len(stream) - head - tail - len(middle), "mode": "compressed"}
}

func salientLines(stream string, budget int, ok bool) string {
	var out string
	seen := map[string]bool{}
	for _, line := range strings.SplitAfter(stream, "\n") {
		if ok && salientPattern.MatchString(line) && !seen[line] && len(out)+len(line) <= budget {
			seen[line], out = true, out+line
		}
	}
	return out
}

func FormatDeniedCommandResult(reply string) string {
	stderr := "Command was not run because the user denied approval."
	if trimmed := strings.TrimSpace(reply); trimmed != "" {
		stderr += "\nUser guidance: " + trimmed
	}
	return FormatCommandResult(CommandResult{Stderr: stderr, Outcome: CommandOutcome{Type: CommandOutcomeDenied}})
}

func FormatSkippedCommandResult(reason string) string {
	switch reason = strings.TrimSpace(reason); reason {
	case "max steps":
		return FormatCommandResult(CommandResult{Stderr: "Command was not run because the step limit was reached.", Outcome: CommandOutcome{Type: CommandOutcomeSkipped, Reason: "max_steps"}})
	case "previous command failed":
		return FormatCommandResult(CommandResult{Stderr: "Command was not run because a previous command failed.", Outcome: CommandOutcome{Type: CommandOutcomeSkipped, Reason: "previous_command_failed"}})
	case "":
		return FormatCommandResult(CommandResult{Stderr: "Command was not run.", Outcome: CommandOutcome{Type: CommandOutcomeSkipped, Reason: "skipped"}})
	default:
		return FormatCommandResult(CommandResult{Stderr: "Command was not run.\nReason: " + reason, Outcome: CommandOutcome{Type: CommandOutcomeSkipped, Reason: "skipped"}})
	}
}

func normalizedOutcome(outcome CommandOutcome, fallbackExitCode int) CommandOutcome {
	if outcome.Type == "" {
		return CommandOutcome{Type: CommandOutcomeExit, ExitCode: &fallbackExitCode}
	}
	if outcome.Type == CommandOutcomeExit && outcome.ExitCode == nil {
		code := fallbackExitCode
		outcome.ExitCode = &code
	}
	return outcome
}
