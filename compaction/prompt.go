package compaction

import "strings"

// LoadCompactionPrompt builds the summarizer prompt for manual transcript compaction.
func LoadCompactionPrompt(instructions string) string {
	var b strings.Builder
	b.WriteString("You are summarizing an older transcript prefix for a coding agent session.\n")
	b.WriteString("Return plain text only. Do not return JSON. Do not call tools.\n")
	b.WriteString("Summarize the user's goals, key decisions, files touched, errors, fixes, current state, and unfinished work.\n")

	trimmed := strings.TrimSpace(instructions)
	if trimmed != "" {
		b.WriteString("\nAdditional compact instructions:\n")
		b.WriteString(trimmed)
		b.WriteString("\n")
	}

	return b.String()
}
