package clnkr

import "context"

type Compactor interface {
	Summarize(ctx context.Context, messages []Message) (string, error)
}

type CompactOptions struct {
	Instructions    string
	KeepRecentTurns int
}

type CompactStats struct{ CompactedMessages, KeptMessages int }
