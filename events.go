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
	ExitCode int
	Feedback CommandFeedback
	Err      error
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

func (EventResponse) event()        {}
func (EventCommandStart) event()    {}
func (EventCommandDone) event()     {}
func (EventProtocolFailure) event() {}
func (EventCompletionGate) event()  {}
func (EventDebug) event()           {}

// StepResult is the outcome of one agent operation.
type StepResult struct {
	Response  Response
	Turn      Turn
	ParseErr  error
	Output    string
	ExecErr   error
	ExecCount int
}
