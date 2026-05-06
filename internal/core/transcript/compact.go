package transcript

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

type compactState struct {
	Source       string `json:"source"`
	Kind         string `json:"kind"`
	Instructions string `json:"instructions,omitempty"`
	Summary      string `json:"summary"`
}

func messageKind(msg Message) string {
	if msg.Role != "user" {
		return ""
	}
	content := strings.TrimSpace(msg.Content)
	switch {
	case compactMessage(content):
		return "compact"
	case stateMessage(content):
		return "state"
	case commandResultMessage(content):
		return "command"
	case strings.HasPrefix(content, "[protocol_error]") &&
		strings.HasSuffix(content, "[/protocol_error]"):
		return "protocol"
	default:
		return "authored"
	}
}

func commandResultMessage(content string) bool {
	var payload struct {
		Stdout  *string         `json:"stdout"`
		Stderr  *string         `json:"stderr"`
		Outcome *CommandOutcome `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return false
	}
	return payload.Stdout != nil && payload.Stderr != nil && payload.Outcome != nil && payload.Outcome.Type != ""
}

// IsCompactMessage reports whether the message is a clnkr compact block.
func IsCompactMessage(msg Message) bool {
	return messageKind(msg) == "compact"
}

func isUserAuthoredMessage(msg Message) bool {
	return messageKind(msg) == "authored"
}

// FormatCompactMessage renders the compact-summary transcript block.
func FormatCompactMessage(summary, instructions string) string {
	body, err := json.Marshal(compactState{
		Source:       "clnkr",
		Kind:         "compact",
		Instructions: instructions,
		Summary:      summary,
	})
	if err != nil {
		panic(fmt.Sprintf("marshal compact message: %v", err))
	}
	return fmt.Sprintf("[compact]\n%s\n[/compact]", body)
}

// FindCompactBoundary returns the start index of the recent tail to keep.
func FindCompactBoundary(messages []Message, keepRecentTurns int) (int, bool) {
	if keepRecentTurns < 1 {
		return 0, false
	}

	authoredSeen := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if !isUserAuthoredMessage(messages[i]) {
			continue
		}
		authoredSeen++
		if authoredSeen == keepRecentTurns {
			for _, earlier := range messages[:i] {
				if isUserAuthoredMessage(earlier) {
					return i, true
				}
			}
			return 0, false
		}
	}
	return 0, false
}

// RewriteForCompaction replaces the older transcript prefix with a compact block.
func RewriteForCompaction(messages []Message, summary, instructions string, keepRecentTurns int) ([]Message, CompactStats, error) {
	if keepRecentTurns < 1 {
		return nil, CompactStats{}, fmt.Errorf("rewrite for compaction: keep recent turns must be at least 1")
	}

	boundary, ok := FindCompactBoundary(messages, keepRecentTurns)
	if !ok {
		return nil, CompactStats{}, fmt.Errorf("rewrite for compaction: not enough history to compact")
	}

	keptTail := make([]Message, 0, len(messages)-boundary)
	for _, msg := range messages[boundary:] {
		if IsCompactMessage(msg) {
			continue
		}
		keptTail = append(keptTail, CloneMessage(msg))
	}

	rewritten := make([]Message, 0, len(keptTail)+2)
	rewritten = append(rewritten, Message{Role: "user", Content: FormatCompactMessage(summary, instructions)})

	keptMessages := len(keptTail)
	for i := len(messages) - 1; i >= 0; i-- {
		if messageKind(messages[i]) != "state" {
			continue
		}
		stateMsg, present := CloneMessage(messages[i]), false
		for _, msg := range keptTail {
			if sameMessage(msg, stateMsg) {
				present = true
				break
			}
		}
		if !present {
			rewritten = append(rewritten, stateMsg)
			keptMessages++
		}
		break
	}

	rewritten = append(rewritten, keptTail...)

	return rewritten, CompactStats{
		CompactedMessages: boundary,
		KeptMessages:      keptMessages,
	}, nil
}

func sameMessage(a, b Message) bool {
	return a.Role == b.Role &&
		a.Content == b.Content &&
		reflect.DeepEqual(a.BashToolCalls, b.BashToolCalls) &&
		reflect.DeepEqual(a.BashToolResult, b.BashToolResult) &&
		reflect.DeepEqual(a.ProviderReplay, b.ProviderReplay)
}

func compactMessage(content string) bool {
	var parsed compactState
	if !extractTaggedJSONObject(content, "[compact]", "[/compact]", &parsed) {
		return false
	}
	return parsed.Source == "clnkr" && parsed.Kind == "compact"
}

func stateMessage(content string) bool {
	_, ok := ExtractStateCwd(content)
	return ok
}

func extractTaggedJSONObject(content, openTag, closeTag string, dst any) bool {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, openTag) || !strings.HasSuffix(content, closeTag) {
		return false
	}
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(content, openTag), closeTag))
	if err := json.Unmarshal([]byte(body), dst); err != nil {
		return false
	}
	return true
}
