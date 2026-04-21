package compaction

import (
	"context"
	"strings"
	"unicode/utf8"

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
	summarizeRequest         = "Summarize the transcript prefix above according to the system instructions."
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
	prefix := append([]clnkr.Message{}, messages...)
	if summarizeMessagesLen(prefix)+len(summarizeRequest) <= summarizeInputCharBudget {
		return append(prefix, clnkr.Message{Role: "user", Content: summarizeRequest})
	}

	available := summarizeInputCharBudget - len(truncationHeader) - len(summarizeRequest)
	if available < 0 {
		available = 0
	}
	truncated := clnkr.Message{Role: "user", Content: truncationHeader + tailWithinBudget(prefix, available)}
	return []clnkr.Message{truncated, {Role: "user", Content: summarizeRequest}}
}

func summarizeMessagesLen(messages []clnkr.Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content)
	}
	return total
}

func tailWithinBudget(messages []clnkr.Message, budget int) string {
	if budget <= 0 {
		return ""
	}

	parts := make([]string, 0, len(messages))
	used := 0
	for i := len(messages) - 1; i >= 0; i-- {
		content := messages[i].Content
		if content == "" {
			continue
		}
		sep := 0
		if len(parts) > 0 {
			sep = 1
		}
		if used+sep+len(content) <= budget {
			used += sep + len(content)
			parts = append(parts, content)
			continue
		}
		remaining := budget - used - sep
		if remaining <= 0 {
			continue
		}
		content = tailStringWithinByteBudget(content, remaining)
		used = budget
		parts = append(parts, content)
	}

	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "\n")
}

func tailStringWithinByteBudget(content string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if len(content) <= budget {
		return content
	}

	start := len(content)
	for start > 0 {
		prevStart := start
		prev, size := utf8.DecodeLastRuneInString(content[:start])
		if prev == utf8.RuneError && size == 0 {
			break
		}
		start -= size
		if len(content)-start > budget {
			return content[prevStart:]
		}
	}

	return content
}
