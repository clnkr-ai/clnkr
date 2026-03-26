package hew

// Event is a sealed interface for agent events.
type Event interface{ event() }

// EventResponse fires when the model returns a response.
type EventResponse struct {
	Message Message
	Usage   Usage
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
	Err      error
}

// EventProtocolFailure fires when the model response fails protocol parsing.
type EventProtocolFailure struct {
	Reason string // machine-readable reason from errorToReason()
	Raw    string // model's original response text
}

// EventDebug carries internal diagnostic messages.
type EventDebug struct{ Message string }

func (EventResponse) event()        {}
func (EventCommandStart) event()    {}
func (EventCommandDone) event()     {}
func (EventProtocolFailure) event() {}
func (EventDebug) event()           {}

// StepResult is the outcome of one Step call.
type StepResult struct {
	Response Response
	Turn     Turn
	ParseErr error
	Output   string
	ExecErr  error
}
