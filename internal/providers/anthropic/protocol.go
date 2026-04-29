package anthropic

import (
	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/providers/turnwire"
)

func requestSchema() map[string]any {
	return turnwire.RequestSchema()
}

func finalTurnSchema() map[string]any {
	return turnwire.FinalTurnSchema()
}

func doneOnlySchema() map[string]any {
	return turnwire.DoneOnlySchema()
}

func normalizeMessagesForProvider(messages []clnkr.Message) []clnkr.Message {
	return turnwire.NormalizeMessagesForProvider(messages)
}

func parseProviderTurn(raw string) (clnkr.Turn, error) {
	return turnwire.ParseProviderTurn(raw)
}
