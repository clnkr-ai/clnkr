package transcript

// Message is one message in a transcript.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CommandFeedback captures transcript-safe feedback for the last command.
type CommandFeedback struct {
	ChangedFiles []string `json:"changed_files,omitempty"`
	Diff         string   `json:"diff,omitempty"`
}

// CommandResult captures one command transcript payload.
type CommandResult struct {
	Command  string
	Stdout   string
	Stderr   string
	ExitCode int
	Feedback CommandFeedback
}

// CompactStats reports how many transcript messages were summarized and kept.
type CompactStats struct {
	CompactedMessages int
	KeptMessages      int
}
