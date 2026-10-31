package clnkr

// Event is a sealed interface for agent events.
type Event interface{ event() }

// EventResponse fires when the model returns a valid structured turn.
type EventResponse struct {
	Turn  Turn
	Usage Usage
	Raw   string // original provider text for debugging only
}

// EventCommandStart fires before running a command.
type EventCommandStart struct {
	Command string
	Dir     string
}

// EventCommandDone fires after a command finishes.
type EventCommandDone struct {
	Command  string
	Stdout   string
	Stderr   string
	Outcome  CommandOutcome
	Feedback CommandFeedback
}

// EventProtocolFailure fires when the model response fails protocol parsing.
type EventProtocolFailure struct {
	Reason string // machine-readable reason from errorToReason()
	Raw    string // model's original response text
}

// EventCompletionGate fires when unattended completion policy accepts, rejects,
// or challenges a done turn.
type EventCompletionGate struct {
	Decision string
	Reasons  []string
	Summary  string
}

// EventDebug carries internal diagnostic messages.
type EventDebug struct{ Message string }

const ContextLengthBackstopCompactingDebug = "context_length_backstop: compacting after provider context limit"

const ContextLengthBackstopRetryingDebug = "context_length_backstop: retrying failed model step once"

func (EventResponse) event()        {}
func (EventCommandStart) event()    {}
func (EventCommandDone) event()     {}
func (EventProtocolFailure) event() {}
func (EventCompletionGate) event()  {}
func (EventDebug) event()           {}

// ExecutionResult reports work attempted for one accepted act turn.
type ExecutionResult struct {
	CommandsExecuted int
}
