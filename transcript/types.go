package transcript

// Message is one message in a transcript.
type Message struct {
	Role    string
	Content string
}

// CommandResult captures one command transcript payload.
type CommandResult struct {
	Command  string
	Stdout   string
	Stderr   string
	ExitCode int
}

// CompactStats reports how many transcript messages were summarized and kept.
type CompactStats struct {
	CompactedMessages int
	KeptMessages      int
}
