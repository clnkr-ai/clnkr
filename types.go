package clnkr

import (
	"context"

	"github.com/clnkr-ai/clnkr/internal/core/transcript"
)

// Message is one message in the conversation.
type Message = transcript.Message

// BashToolCall records a provider-native bash tool call in provider-neutral form.
type BashToolCall = transcript.BashToolCall

// BashToolResult records the provider-native result for a bash tool call.
type BashToolResult = transcript.BashToolResult

// ProviderReplayItem carries opaque provider/API-scoped data needed for replay.
type ProviderReplayItem = transcript.ProviderReplayItem

// Usage tracks token consumption for a single LLM call.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Response captures one model reply.
// On success Turn is non-nil and ProtocolErr is nil.
// On protocol failure Raw preserves the provider's original assistant text.
type Response struct {
	Turn           Turn
	Raw            string
	Usage          Usage
	ProtocolErr    error
	BashToolCalls  []BashToolCall
	ProviderReplay []ProviderReplayItem
}

// ActDecisionKind is the policy decision for an act turn.
type ActDecisionKind string

const (
	// ActDecisionApprove allows the act turn to execute.
	ActDecisionApprove ActDecisionKind = "approve"
	// ActDecisionReject records guidance instead of executing the act turn.
	ActDecisionReject ActDecisionKind = "reject"
)

// ActProposal describes an act turn being considered by a run policy.
type ActProposal struct {
	Turn     *ActTurn
	Skipped  []BashAction
	Commands []BashAction
	Prompt   string
}

// ActDecision is a policy response for an act proposal.
type ActDecision struct {
	Kind     ActDecisionKind
	Guidance string
}

// RunPolicy supplies decisions for act and clarify turns during RunWithPolicy.
type RunPolicy interface {
	DecideAct(context.Context, ActProposal) (ActDecision, error)
	Clarify(context.Context, string) (string, error)
}

// FullSendPolicy approves every act turn and does not answer clarifications.
type FullSendPolicy struct{}

// DecideAct approves every act turn.
func (FullSendPolicy) DecideAct(context.Context, ActProposal) (ActDecision, error) {
	return ActDecision{Kind: ActDecisionApprove}, nil
}

// Clarify returns ErrClarificationNeeded.
func (FullSendPolicy) Clarify(context.Context, string) (string, error) {
	return "", ErrClarificationNeeded
}

// CommandFeedback captures git-backed host feedback for the last command.
type CommandFeedback = transcript.CommandFeedback

// CommandOutcomeType describes how a command finished or why it did not run.
type CommandOutcomeType = transcript.CommandOutcomeType

const (
	CommandOutcomeExit      = transcript.CommandOutcomeExit
	CommandOutcomeTimeout   = transcript.CommandOutcomeTimeout
	CommandOutcomeCancelled = transcript.CommandOutcomeCancelled
	CommandOutcomeDenied    = transcript.CommandOutcomeDenied
	CommandOutcomeSkipped   = transcript.CommandOutcomeSkipped
	CommandOutcomeError     = transcript.CommandOutcomeError
)

// CommandOutcome captures a command's completion state.
type CommandOutcome = transcript.CommandOutcome

// CommandResult captures one shell command execution.
// Zero PostCwd = no change; nil PostEnv = no snapshot.
type CommandResult struct {
	Command  string
	Stdout   string
	Stderr   string
	ExitCode int
	PostCwd  string
	PostEnv  map[string]string
	Outcome  CommandOutcome
	Feedback CommandFeedback
}

// Model sends messages to an LLM and returns a response.
type Model interface {
	Query(ctx context.Context, messages []Message) (Response, error)
}

// FinalSummaryModel is implemented by models that can make a final done-only
// query without exposing command tools.
type FinalSummaryModel interface {
	QueryFinal(ctx context.Context, messages []Message) (Response, error)
}

// Executor runs a shell command and returns its structured result.
type Executor interface {
	Execute(ctx context.Context, command string, dir string) (CommandResult, error)
}
