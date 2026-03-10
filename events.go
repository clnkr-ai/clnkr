package hew

// Event is a sealed interface for agent events.
// Only types in this package can implement it (unexported marker method).
// The compiler enforces the seal; the exhaustive linter catches missing switch cases.
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
	Output string
	Err    error
}

// EventFormatError fires when the LLM response has no bash block.
type EventFormatError struct{}

// EventDebug carries internal diagnostic messages.
type EventDebug struct {
	Message string
}

func (EventResponse) event()     {}
func (EventCommandStart) event() {}
func (EventCommandDone) event()  {}
func (EventFormatError) event()  {}
func (EventDebug) event()        {}

// StepResult is the outcome of one Step call.
type StepResult struct {
	Response Response // the LLM response
	Action   string   // parsed command, DoneSignal for completion, "" for format error
	Output   string   // command output, "" if no command ran
	ExecErr  error    // nil if command succeeded or didn't run
}
