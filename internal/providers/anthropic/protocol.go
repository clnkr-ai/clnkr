package anthropic

import (
	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/providers/actwire"
)

func requestSchema() map[string]any {
	return actwire.RequestSchema()
}

func finalTurnSchema() map[string]any {
	return actwire.FinalTurnSchema()
}

func doneOnlySchema() map[string]any {
	return actwire.DoneOnlySchema()
}

func normalizeMessagesForProvider(messages []clnkr.Message) []clnkr.Message {
	return actwire.NormalizeMessagesForProvider(messages)
}

func parseProviderTurn(raw string) (clnkr.Turn, error) {
	return actwire.ParseProviderTurn(raw)
}
