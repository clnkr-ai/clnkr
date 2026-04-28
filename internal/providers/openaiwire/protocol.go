package openaiwire

import (
	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/providers/turnwire"
)

func RequestSchema() map[string]any {
	return turnwire.RequestSchema(true)
}

func NormalizeMessagesForProvider(messages []clnkr.Message) []clnkr.Message {
	return turnwire.NormalizeMessagesForProvider(messages)
}

func ParseProviderTurn(raw string) (clnkr.Turn, error) {
	return turnwire.ParseProviderTurn(raw)
}
