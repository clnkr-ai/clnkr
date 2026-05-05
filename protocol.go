package clnkr

import (
	"errors"
	"fmt"
	"strings"
)

// Turn is a sealed interface for structured model turns.
type Turn interface{ turn() }

type BashAction struct {
	ID      string `json:"-"`
	Command string `json:"command"`
	Workdir string `json:"workdir,omitempty"`
}

type BashBatch struct {
	Commands []BashAction `json:"commands"`
}

type ActTurn struct {
	Bash      BashBatch `json:"bash"`
	Reasoning string    `json:"reasoning,omitempty"`
}

type ClarifyTurn struct {
	Question  string `json:"question"`
	Reasoning string `json:"reasoning,omitempty"`
}

type DoneTurn struct {
	Summary      string                 `json:"summary"`
	Verification CompletionVerification `json:"verification"`
	KnownRisks   []string               `json:"known_risks"`
	Reasoning    string                 `json:"reasoning,omitempty"`
}

type VerificationStatus string

const (
	VerificationVerified          VerificationStatus = "verified"
	VerificationPartiallyVerified VerificationStatus = "partially_verified"
	VerificationNotVerified       VerificationStatus = "not_verified"
)

type CompletionVerification struct {
	Status VerificationStatus  `json:"status"`
	Checks []VerificationCheck `json:"checks"`
}

type VerificationCheck struct {
	Command  string `json:"command"`
	Outcome  string `json:"outcome"`
	Evidence string `json:"evidence"`
}

func (ActTurn) turn()     {}
func (ClarifyTurn) turn() {}
func (DoneTurn) turn()    {}

func turnPointer(turn Turn) Turn {
	switch v := turn.(type) {
	case ActTurn:
		return &v
	case ClarifyTurn:
		return &v
	case DoneTurn:
		return &v
	default:
		return turn
	}
}

type jsonEnvelope struct {
	Type         string                  `json:"type"`
	Bash         *jsonBashEnvelope       `json:"bash,omitempty"`
	Question     string                  `json:"question,omitempty"`
	Summary      string                  `json:"summary,omitempty"`
	Verification *CompletionVerification `json:"verification,omitempty"`
	KnownRisks   any                     `json:"known_risks,omitempty"`
	Reasoning    string                  `json:"reasoning,omitempty"`
}

type jsonBashEnvelope struct {
	Commands []jsonCommand `json:"commands"`
}

type jsonCommand struct {
	Command string  `json:"command"`
	Workdir *string `json:"workdir"`
}

var (
	ErrInvalidJSON     = errors.New("invalid JSON")
	ErrMissingCommand  = errors.New("act turn requires at least one command")
	ErrEmptyClarify    = errors.New("clarify turn requires non-empty question")
	ErrEmptySummary    = errors.New("done turn requires non-empty summary")
	ErrUnknownTurnType = errors.New("unknown turn type")
)

const protocolActExample = `{"type":"act","bash":{"commands":[{"command":"...","workdir":null}]}}`
const protocolDoneExample = `{"type":"done","summary":"...","verification":{"status":"verified","checks":[{"command":"...","outcome":"passed","evidence":"..."}]},"known_risks":[]}`

var protocolErrorTargets = []error{ErrInvalidJSON, ErrMissingCommand, ErrEmptyClarify, ErrEmptySummary, ErrUnknownTurnType}
var protocolErrorReasons = []string{"invalid_json", "missing_command", "empty_clarify", "empty_summary", "unknown_turn_type"}

func errorToReason(err error) string {
	for i, target := range protocolErrorTargets {
		if errors.Is(err, target) {
			return protocolErrorReasons[i]
		}
	}
	return "unknown"
}

func protocolCorrectionMessageFor(err error, protocol ActProtocol) string {
	detail := err.Error()
	var hint string
	if normalizeActProtocol(protocol) == ActProtocolToolCalls {
		hint = "Your previous response was ignored and no command ran. For command execution, call the bash tool instead of emitting JSON. For clarification or completion, respond with exactly one JSON object whose type is \"clarify\" or \"done\". If type is \"done\", include summary, \"verification\", and \"known_risks\". verification.status must be one of verified, partially_verified, or not_verified. verification.checks must be an array of concrete checks with command, outcome, and evidence. Do not emit a JSON act turn in tool-call mode. Do not jump to done unless prior command results in this conversation already prove the task is complete."
	} else {
		hint = fmt.Sprintf(
			"Your previous response was ignored and no command ran. Respond with exactly one JSON object for the next turn from the current state. Use this act example as the shape guide: %s. Set type to exactly one of \"act\", \"clarify\", or \"done\". If type is \"act\", include bash. If type is \"clarify\", include question. If type is \"done\", include summary, \"verification\", and \"known_risks\". verification.status must be one of verified, partially_verified, or not_verified. verification.checks must be an array of concrete checks with command, outcome, and evidence. When a field does not apply, omit it or set it to null. Include reasoning in every response; use null if you have nothing to add. Do not emit multiple JSON objects in one response. Do not emit an act turn and a done turn together. If you intended to run commands, resend only that act turn. Do not jump to done unless prior command results in this conversation already prove the task is complete.",
			protocolActExample,
		)
	}
	switch {
	case strings.Contains(detail, "invalid character '|' in string escape code"):
		hint += ` Your command contains \| which is not a valid JSON escape. Use \\| for a literal backslash-pipe, or just | if you do not want a backslash.`
	case strings.Contains(detail, "invalid character '`' in string escape code"):
		hint += " Your command contains \\` which is not a valid JSON escape. Use \\\\` for a literal backslash-backtick, or just ` if you do not want a backslash."
	}
	return fmt.Sprintf("[protocol_error]\nreason: %s\nhint: %s\ndetail: %s\n[/protocol_error]",
		errorToReason(err), hint, detail)
}
