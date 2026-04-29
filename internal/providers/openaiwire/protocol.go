package openaiwire

import (
	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/providers/actwire"
)

func RequestSchema() map[string]any {
	return actwire.RequestSchema()
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
