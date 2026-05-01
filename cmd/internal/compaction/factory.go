package compaction

import (
	"context"
	"strings"

	clnkr "github.com/clnkr-ai/clnkr"
)

// Factory builds a compactor for the given instruction set.
type Factory func(instructions string) clnkr.Compactor

// FreeformModel summarizes transcript messages as plain text.
type FreeformModel interface {
	QueryText(ctx context.Context, messages []clnkr.Message) (string, error)
}

// ModelFactory builds a summarizer model for the given instruction set.
type ModelFactory func(instructions string) FreeformModel

// NewFactory wraps a model factory as a clnkr compactor factory.
func NewFactory(makeModel ModelFactory) Factory {
	return func(instructions string) clnkr.Compactor {
		return modelCompactor{model: makeModel(instructions)}
	}
}

type modelCompactor struct {
	model FreeformModel
}

const (
	sourceTextOpen           = "<source_text>\n"
	sourceTextClose          = "\n</source_text>"
	summarizeRequest         = "Write the handoff summary using exactly these sections:\nGoal:\nConstraints:\nKey decisions:\nDiscoveries:\nRelevant files and artifacts:\nCurrent state:\nOpen questions / next steps:\n\nFor any section with no supported content, write:\n- none"
	summarizeInputCharBudget = 120000
	truncationHeader         = "[compact_context_truncated]\n"
)

func (m modelCompactor) Summarize(ctx context.Context, messages []clnkr.Message) (string, error) {
	queryMessages := buildSummarizeMessages(messages)

	summary, err := m.model.QueryText(ctx, queryMessages)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(summary), nil
}

func buildSummarizeMessages(messages []clnkr.Message) []clnkr.Message {
	sourceBody := formatSourceText(messages)
	available := summarizeInputCharBudget - len(sourceTextOpen) - len(sourceTextClose) - len(summarizeRequest)
	if len(sourceBody) > available {
		tailBudget := available - len(truncationHeader)
		if tailBudget < 0 {
			tailBudget = 0
		}
		sourceBody = truncationHeader + tailStringWithinByteBudget(sourceBody, tailBudget)
	}
	return []clnkr.Message{
		{Role: "user", Content: sourceTextOpen + sourceBody + sourceTextClose},
		{Role: "user", Content: summarizeRequest},
	}
}

func formatSourceText(messages []clnkr.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString("[")
		b.WriteString(msg.Role)
		b.WriteString("]\n")
		b.WriteString(msg.Content)
		b.WriteString("\n\n")
	}
	return b.String()
}

func tailStringWithinByteBudget(content string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if len(content) <= budget {
		return content
	}
	for i := range content {
		if len(content)-i <= budget {
			return content[i:]
		}
	}
	return content
}
