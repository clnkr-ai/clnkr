package transcript

import (
	"encoding/json"
	"fmt"
)

type state struct {
	Source string `json:"source"`
	Kind   string `json:"kind"`
	Cwd    string `json:"cwd"`
}

// FormatStateMessage renders the host cwd state block stored in transcripts.
func FormatStateMessage(cwd string) string {
	body, err := json.Marshal(state{
		Source: "clnkr",
		Kind:   "state",
		Cwd:    cwd,
	})
	if err != nil {
		panic(fmt.Sprintf("marshal state message: %v", err))
	}
	return fmt.Sprintf("[state]\n%s\n[/state]", body)
}

// ExtractLatestCwd returns the latest valid clnkr cwd state from a transcript.
func ExtractLatestCwd(messages []Message) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		if cwd, ok := ExtractStateCwd(messages[i].Content); ok {
			return cwd, true
		}
	}
	return "", false
}

// ExtractStateCwd parses a single transcript state block.
func ExtractStateCwd(content string) (string, bool) {
	var parsed state
	if !extractTaggedJSONObject(content, "[state]", "[/state]", &parsed) {
		return "", false
	}
	if parsed.Source != "clnkr" || parsed.Kind != "state" || parsed.Cwd == "" {
		return "", false
	}
	return parsed.Cwd, true
}
