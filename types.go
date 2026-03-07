package hew

import "context"

// Message represents a single message in the conversation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Usage tracks token consumption for a single LLM call.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Response wraps an LLM response with usage metadata.
type Response struct {
	Message Message
	Usage   Usage
}

// Model sends messages to an LLM and returns a response.
type Model interface {
	Query(ctx context.Context, messages []Message) (Response, error)
}

// Executor runs a shell command and returns its output.
type Executor interface {
	Execute(ctx context.Context, command string, dir string) (string, error)
}
