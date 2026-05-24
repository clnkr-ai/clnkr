package openaiwire

import (
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
