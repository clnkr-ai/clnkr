package transcript

import "encoding/json"

// Message is one message in a transcript.
type Message struct {
	Role           string               `json:"role"`
	Content        string               `json:"content"`
	BashToolCalls  []BashToolCall       `json:"bash_tool_calls,omitempty"`
	BashToolResult *BashToolResult      `json:"bash_tool_result,omitempty"`
	ProviderReplay []ProviderReplayItem `json:"provider_replay,omitempty"`
}

// BashToolCall records a provider-native bash tool call in provider-neutral form.
type BashToolCall struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Workdir string `json:"workdir,omitempty"`
}

// BashToolResult records the provider-native result for a bash tool call.
type BashToolResult struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

// ProviderReplayItem carries opaque provider/API-scoped data needed for replay.
type ProviderReplayItem struct {
	Provider    string          `json:"provider"`
	ProviderAPI string          `json:"provider_api"`
	Type        string          `json:"type"`
	JSON        json.RawMessage `json:"json,omitempty"`
}

// CommandFeedback captures transcript-safe feedback for the last command.
type CommandFeedback struct {
	ChangedFiles []string `json:"changed_files,omitempty"`
	Diff         string   `json:"diff,omitempty"`
}

type CommandOutcomeType string

const (
	CommandOutcomeExit      CommandOutcomeType = "exit"
	CommandOutcomeTimeout   CommandOutcomeType = "timeout"
	CommandOutcomeCancelled CommandOutcomeType = "cancelled"
	CommandOutcomeDenied    CommandOutcomeType = "denied"
	CommandOutcomeSkipped   CommandOutcomeType = "skipped"
	CommandOutcomeError     CommandOutcomeType = "error"
)

type CommandOutcome struct {
	Type     CommandOutcomeType `json:"type"`
	ExitCode *int               `json:"exit_code,omitempty"`
	Reason   string             `json:"reason,omitempty"`
	Message  string             `json:"message,omitempty"`
}

// CommandResult captures one command transcript payload.
type CommandResult struct {
	Command  string
	Stdout   string
	Stderr   string
	ExitCode int
	Outcome  CommandOutcome
	Feedback CommandFeedback
}

// CompactStats reports how many transcript messages were summarized and kept.
type CompactStats struct {
	CompactedMessages int
	KeptMessages      int
}

// CloneMessage returns a deep copy of msg.
func CloneMessage(msg Message) Message {
	cloned := msg
	if msg.BashToolCalls != nil {
		cloned.BashToolCalls = append([]BashToolCall(nil), msg.BashToolCalls...)
	}
	if msg.BashToolResult != nil {
		result := *msg.BashToolResult
		cloned.BashToolResult = &result
	}
	if msg.ProviderReplay != nil {
		cloned.ProviderReplay = make([]ProviderReplayItem, len(msg.ProviderReplay))
		for i, item := range msg.ProviderReplay {
			cloned.ProviderReplay[i] = item
			if item.JSON != nil {
				cloned.ProviderReplay[i].JSON = append(json.RawMessage(nil), item.JSON...)
			}
		}
	}
	return cloned
}

// CloneMessages returns a deep copy of messages.
func CloneMessages(messages []Message) []Message {
	if messages == nil {
		return nil
	}
	cloned := make([]Message, len(messages))
	for i, msg := range messages {
		cloned[i] = CloneMessage(msg)
	}
	return cloned
}
