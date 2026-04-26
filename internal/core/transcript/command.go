package transcript

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Escape only transcript delimiters inside section bodies. Literal shell and
// file content should stay readable to the model.
var sectionEscaper = strings.NewReplacer("[", "&#91;", "]", "&#93;")

// FormatCommandResult renders a command result using the host transcript envelope.
func FormatCommandResult(result CommandResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[command]\n%s\n[/command]\n", sectionEscaper.Replace(result.Command))
	fmt.Fprintf(&b, "[exit_code]\n%d\n[/exit_code]\n", result.ExitCode)
	fmt.Fprintf(&b, "[stdout]\n%s\n[/stdout]\n", sectionEscaper.Replace(result.Stdout))
	fmt.Fprintf(&b, "[stderr]\n%s\n[/stderr]", sectionEscaper.Replace(result.Stderr))
	if len(result.Feedback.ChangedFiles) > 0 || result.Feedback.Diff != "" {
		body, err := json.Marshal(result.Feedback)
		if err == nil {
			fmt.Fprintf(&b, "\n[command_feedback]\n%s\n[/command_feedback]", sectionEscaper.Replace(string(body)))
		}
	}
	return b.String()
}
