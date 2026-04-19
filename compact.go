package clnkr

import (
	"context"

	"github.com/clnkr-ai/clnkr/internal/core/transcript"
)

type Compactor interface {
	Summarize(ctx context.Context, messages []Message) (string, error)
}

type CompactOptions struct {
	Instructions    string
	KeepRecentTurns int
}

type CompactStats = transcript.CompactStats
