package transcript

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type state struct {
	Type   string `json:"type"`
	Source string `json:"source"`
	Cwd    string `json:"cwd"`
}

// FormatStateMessage renders the host cwd state object stored in transcripts.
func FormatStateMessage(cwd string) string {
	body, err := json.Marshal(state{
		Type:   "state",
		Source: "clnkr",
		Cwd:    cwd,
	})
	if err != nil {
		panic(fmt.Sprintf("marshal state message: %v", err))
	}
	return string(body)
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

// ExtractStateCwd parses a single transcript state message.
func ExtractStateCwd(content string) (string, bool) {
	dec := json.NewDecoder(strings.NewReader(content))
	dec.DisallowUnknownFields()
	var parsed state
	if err := dec.Decode(&parsed); err != nil {
		return "", false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return "", false
	}
	if parsed.Type != "state" || parsed.Source != "clnkr" || parsed.Cwd == "" {
		return "", false
	}
	return parsed.Cwd, true
}
