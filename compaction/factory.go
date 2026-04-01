package compaction

import (
	"context"
	"strings"

	clnkr "github.com/clnkr-ai/clnkr"
)

type Factory func(instructions string) clnkr.Compactor

type ModelFactory func(instructions string) clnkr.Model

func NewFactory(makeModel ModelFactory) Factory {
	return func(instructions string) clnkr.Compactor {
		return modelCompactor{model: makeModel(instructions)}
	}
}

type modelCompactor struct {
	model clnkr.Model
}

const summarizeRequest = "Summarize the transcript prefix above according to the system instructions."

func (m modelCompactor) Summarize(ctx context.Context, messages []clnkr.Message) (string, error) {
	queryMessages := append(append([]clnkr.Message{}, messages...), clnkr.Message{
		Role:    "user",
		Content: summarizeRequest,
	})

	resp, err := m.model.Query(ctx, queryMessages)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Message.Content), nil
}
