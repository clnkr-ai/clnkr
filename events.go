package hew

// Event is the sealed interface for agent lifecycle events.
// Only types in this package can implement it (unexported marker method).
// No unit test — the compiler enforces the seal at usage sites. Use exhaustive linter for switch coverage.
type Event interface{ event() }

// EventResponse is emitted when the model returns a response.
type EventResponse struct {
	Message Message
	Usage   Usage
}

// EventCommandStart is emitted before executing a command.
type EventCommandStart struct {
	Command string
	Dir     string
}

// EventCommandDone is emitted after a command finishes.
type EventCommandDone struct {
	Output string
	Err    error
}

// EventFormatError is emitted when the LLM response has no bash block.
type EventFormatError struct{}

// EventDebug is emitted for internal diagnostic messages.
type EventDebug struct {
	Message string
}

func (EventResponse) event()     {}
func (EventCommandStart) event() {}
func (EventCommandDone) event()  {}
func (EventFormatError) event()  {}
func (EventDebug) event()        {}

// StepResult holds the outcome of a single agent step.
type StepResult struct {
	Response Response // the LLM response
	Action   string   // parsed command, "exit" for exit, "" for format error
	Output   string   // command output, "" if no command ran
	ExecErr  error    // nil if command succeeded or didn't run
}
