package transcript

import (
	"encoding/json"
	"fmt"
	"strings"
)

type compactState struct {
	Source       string `json:"source"`
	Kind         string `json:"kind"`
	Instructions string `json:"instructions,omitempty"`
	Summary      string `json:"summary"`
}

func isStateMessage(msg Message) bool {
	if msg.Role != "user" {
		return false
	}
	_, ok := ExtractStateCwd(msg.Content)
	return ok
}

func isCommandResultMessage(msg Message) bool {
	if msg.Role != "user" {
		return false
	}
	content := strings.TrimSpace(msg.Content)
	return strings.HasPrefix(content, "[command]\n") &&
		strings.Contains(content, "\n[/command]\n[exit_code]\n") &&
		strings.Contains(content, "\n[/exit_code]\n[stdout]\n") &&
		strings.Contains(content, "\n[/stdout]\n[stderr]\n") &&
		strings.HasSuffix(content, "\n[/stderr]")
}

func isProtocolErrorMessage(msg Message) bool {
	if msg.Role != "user" {
		return false
	}
	content := strings.TrimSpace(msg.Content)
	return strings.HasPrefix(content, "[protocol_error]") &&
		strings.HasSuffix(content, "[/protocol_error]")
}

// IsCompactMessage reports whether the message is a clnkr compact block.
func IsCompactMessage(msg Message) bool {
	if msg.Role != "user" {
		return false
	}
	_, ok := extractCompactState(msg.Content)
	return ok
}

func isUserAuthoredMessage(msg Message) bool {
	if msg.Role != "user" {
		return false
	}
	return !isStateMessage(msg) &&
		!isCommandResultMessage(msg) &&
		!isProtocolErrorMessage(msg) &&
		!IsCompactMessage(msg)
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
		if authoredSeen != keepRecentTurns {
			continue
		}
		if hasEarlierUserAuthoredMessage(messages[:i]) {
			return i, true
		}
		return 0, false
	}

	return 0, false
}

func latestStateMessage(messages []Message) (Message, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if isStateMessage(messages[i]) {
			return messages[i], true
		}
	}
	return Message{}, false
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
		keptTail = append(keptTail, msg)
	}

	rewritten := make([]Message, 0, len(keptTail)+2)
	rewritten = append(rewritten, Message{Role: "user", Content: FormatCompactMessage(summary, instructions)})

	keptMessages := len(keptTail)
	if stateMsg, ok := latestStateMessage(messages); ok && !containsMessage(keptTail, stateMsg) {
		rewritten = append(rewritten, stateMsg)
		keptMessages++
	}

	rewritten = append(rewritten, keptTail...)

	return rewritten, CompactStats{
		CompactedMessages: boundary,
		KeptMessages:      keptMessages,
	}, nil
}

func extractCompactState(content string) (compactState, bool) {
	var parsed compactState
	if !extractTaggedJSONObject(content, "[compact]", "[/compact]", &parsed) {
		return compactState{}, false
	}
	if parsed.Source != "clnkr" || parsed.Kind != "compact" {
		return compactState{}, false
	}
	return parsed, true
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

func hasEarlierUserAuthoredMessage(messages []Message) bool {
	for _, msg := range messages {
		if isUserAuthoredMessage(msg) {
			return true
		}
	}
	return false
}

func containsMessage(messages []Message, want Message) bool {
	for _, msg := range messages {
		if msg == want {
			return true
		}
	}
	return false
}
