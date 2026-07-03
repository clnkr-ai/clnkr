package openaiwire

import (
	"encoding/json"
	"strings"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/providers/actwire"
)

func RequestSchema() map[string]any {
	return actwire.RequestSchema()
}

// UnattendedRequestSchema accepts act and done turns, but not clarify.
func UnattendedRequestSchema() map[string]any {
	return actwire.UnattendedRequestSchema()
}

func FinalTurnSchema() map[string]any {
	return actwire.FinalTurnSchema()
}

func DoneOnlySchema() map[string]any {
	return actwire.DoneOnlySchema()
}

func NormalizeMessagesForProvider(messages []clnkr.Message) []clnkr.Message {
	return actwire.NormalizeMessagesForProvider(messages)
}

func ParseProviderTurn(raw string) (clnkr.Turn, error) {
	return actwire.ParseProviderTurn(raw)
}

func ParseChatCompletionTurn(raw string) (clnkr.Turn, error) {
	turn, err := actwire.ParseProviderTurn(raw)
	if err == nil {
		return turn, nil
	}
	if canonical, canonicalErr := clnkr.ParseTurn(raw); canonicalErr == nil {
		return canonical, nil
	}
	return nil, err
}

func FormatErrorMessage(code json.RawMessage, message string) string {
	codeString := errorCodeText(code)
	switch {
	case codeString != "" && message != "":
		return codeString + ": " + message
	case codeString != "":
		return codeString
	case message != "":
		return message
	default:
		return ""
	}
}

func errorCodeText(code json.RawMessage) string {
	var text string
	if err := json.Unmarshal(code, &text); err == nil {
		return text
	}
	return ""
}

func IsContextLengthErrorText(message string) bool {
	normalized := strings.ToLower(message)
	if strings.Contains(normalized, "context_length_exceeded") {
		return true
	}
	if strings.Contains(normalized, "context window") &&
		(strings.Contains(normalized, "exceed") || strings.Contains(normalized, "exceeds")) {
		return true
	}
	if strings.Contains(normalized, "maximum context length") {
		return true
	}
	return strings.Contains(normalized, "context length") &&
		(strings.Contains(normalized, "exceed") || strings.Contains(normalized, "resulted in"))
}
