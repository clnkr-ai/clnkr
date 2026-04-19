package clnkr

import (
	"context"

	"github.com/clnkr-ai/clnkr/internal/core/transcript"
)

// Message is one message in the conversation.
type Message = transcript.Message

// Usage tracks token consumption for a single LLM call.
type Usage struct{ InputTokens, OutputTokens int }

// Response captures one model reply.
// On success Turn is non-nil and ProtocolErr is nil.
// On protocol failure Raw preserves the provider's original assistant text.
type Response struct {
	Turn        Turn
	Raw         string
	Usage       Usage
	ProtocolErr error
}

// CommandFeedback captures git-backed host feedback for the last command.
type CommandFeedback = transcript.CommandFeedback

// CommandResult captures one shell command execution.
// Zero PostCwd = no change; nil PostEnv = no snapshot.
type CommandResult struct {
	Command  string
	Stdout   string
	Stderr   string
	ExitCode int
	PostCwd  string
	PostEnv  map[string]string
	Feedback CommandFeedback
}

// Model sends messages to an LLM and returns a response.
type Model interface {
	Query(ctx context.Context, messages []Message) (Response, error)
}

// Executor runs a shell command and returns its structured result.
type Executor interface {
	Execute(ctx context.Context, command string, dir string) (CommandResult, error)
}
