package clnkr

import "context"

// Message is one message in the conversation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Usage tracks token consumption for a single LLM call.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Response pairs an LLM reply with its token usage.
type Response struct {
	Message Message
	Usage   Usage
}

// CommandResult captures the outcome of one shell command execution.
type CommandResult struct {
	Command  string
	Stdout   string
	Stderr   string
	ExitCode int
	PostCwd  string
	PostEnv  map[string]string
}

// Model sends messages to an LLM and returns a response.
type Model interface {
	Query(ctx context.Context, messages []Message) (Response, error)
}

// Executor runs a shell command and returns its structured result.
type Executor interface {
	Execute(ctx context.Context, command string, dir string) (CommandResult, error)
}
