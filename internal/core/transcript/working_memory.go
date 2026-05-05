package transcript

import (
	"bytes"
	"encoding/json"
)

type workingMemoryEnvelope struct {
	Source  string `json:"source"`
	Kind    string `json:"kind"`
	Version int    `json:"version"`
}

func FormatWorkingMemoryMessage(body json.RawMessage) string {
	var out bytes.Buffer
	out.WriteString("[working_memory]\n")
	if err := json.Compact(&out, body); err != nil {
		out.Write(body)
	}
	out.WriteString("\n[/working_memory]")
	return out.String()
}

func IsWorkingMemoryMessage(msg Message) bool {
	return messageKind(msg) == "working_memory"
}

func IsTrailingHostStateMessage(msg Message) bool {
	kind := messageKind(msg)
	return kind == "state" || kind == "resource_state"
}

func workingMemoryMessage(content string) bool {
	var parsed workingMemoryEnvelope
	if !extractTaggedJSONObject(content, "[working_memory]", "[/working_memory]", &parsed) {
		return false
	}
	return parsed.Source == "clnkr" && parsed.Kind == "working_memory" && parsed.Version == 1
}

func resourceStateMessage(content string) bool {
	var parsed struct {
		Type   string `json:"type"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return false
	}
	return parsed.Type == "resource_state" && parsed.Source == "clnkr"
}
