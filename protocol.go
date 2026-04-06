package clnkr

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/clnkr-ai/clnkr/turnjson"
)

// Turn is a sealed interface for structured model turns.
type Turn interface{ turn() }

type BashAction struct {
	Command string `json:"command"`
	Workdir string `json:"workdir,omitempty"`
}

type ActTurn struct {
	Bash      BashAction `json:"bash"`
	Reasoning string     `json:"reasoning,omitempty"`
}

type ClarifyTurn struct {
	Question  string `json:"question"`
	Reasoning string `json:"reasoning,omitempty"`
}

type DoneTurn struct {
	Summary   string `json:"summary"`
	Reasoning string `json:"reasoning,omitempty"`
}

func (ActTurn) turn()     {}
func (ClarifyTurn) turn() {}
func (DoneTurn) turn()    {}

type jsonEnvelope struct {
	Type      string            `json:"type"`
	Bash      *jsonBashEnvelope `json:"bash,omitempty"`
	Question  string            `json:"question,omitempty"`
	Summary   string            `json:"summary,omitempty"`
	Reasoning string            `json:"reasoning,omitempty"`
}

type jsonBashEnvelope struct {
	Command string  `json:"command"`
	Workdir *string `json:"workdir"`
}

var (
	ErrInvalidJSON     = errors.New("invalid JSON")
	ErrMissingCommand  = errors.New("act turn requires command")
	ErrEmptyClarify    = errors.New("clarify turn requires non-empty question")
	ErrEmptySummary    = errors.New("done turn requires non-empty summary")
	ErrUnknownTurnType = errors.New("unknown turn type")
)

var jsonBlock = regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n?```")

func sanitizeJSONEscapes(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	inString, escaped := false, false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			b.WriteByte(c)
			continue
		}
		if escaped {
			if !isValidJSONEscape(raw, i) {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' {
			b.WriteByte(c)
			escaped = true
			continue
		}
		if c == '"' {
			inString = false
		}
		b.WriteByte(c)
	}
	return b.String()
}

func isValidJSONEscape(raw string, i int) bool {
	c := raw[i]
	switch c {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return true
	case 'u':
		if i+4 >= len(raw) {
			return false
		}
		for j := i + 1; j < i+5; j++ {
			if !isHexDigit(raw[j]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func isHexDigit(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// extractJSON finds the JSON object in model output. Tries code fences first,
// then falls back to brace-matching for bare JSON in prose.
func extractJSON(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: empty response", ErrInvalidJSON)
	}
	if m := jsonBlock.FindStringSubmatch(raw); len(m) >= 2 {
		return strings.TrimSpace(m[1]), nil
	}

	start := strings.IndexByte(raw, '{')
	if start < 0 {
		return "", fmt.Errorf("%w: no JSON object found in response", ErrInvalidJSON)
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(raw); i++ {
		c := raw[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inString {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[start : i+1], nil
			}
		}
	}
	return "", fmt.Errorf("%w: unbalanced braces in response", ErrInvalidJSON)
}

// ParseTurn extracts and validates a structured turn from model output.
func ParseTurn(raw string) (Turn, error) {
	jsonStr, err := extractJSON(raw)
	if err != nil {
		return nil, err
	}
	// We repair common model escape mistakes before unmarshal. The targeted
	// invalid-escape hinting below is still useful as a fallback.
	jsonStr = sanitizeJSONEscapes(jsonStr)
	var env jsonEnvelope
	dec := json.NewDecoder(strings.NewReader(jsonStr))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	if err := turnjson.EnsureSingleJSONObject(dec); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &fields); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}

	switch env.Type {
	case "act":
		if env.Bash == nil || strings.TrimSpace(env.Bash.Command) == "" {
			return nil, ErrMissingCommand
		}
		if err := turnjson.RequireObjectFields(fields, "bash", "command", "workdir"); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
		}
		if err := turnjson.RejectPresentNonNullField(fields, "question", "act turn only allows question when it is null"); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
		}
		if err := turnjson.RejectPresentNonNullField(fields, "summary", "act turn only allows summary when it is null"); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
		}
		act := &ActTurn{Bash: BashAction{Command: env.Bash.Command}, Reasoning: env.Reasoning}
		if env.Bash.Workdir != nil {
			act.Bash.Workdir = *env.Bash.Workdir
		}
		return act, nil
	case "clarify":
		if env.Question == "" {
			return nil, ErrEmptyClarify
		}
		if err := turnjson.RejectPresentNonNullField(fields, "bash", "clarify turn only allows bash when it is null"); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
		}
		if err := turnjson.RejectPresentNonNullField(fields, "summary", "clarify turn only allows summary when it is null"); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
		}
		return &ClarifyTurn{Question: env.Question, Reasoning: env.Reasoning}, nil
	case "done":
		if env.Summary == "" {
			return nil, ErrEmptySummary
		}
		if err := turnjson.RejectPresentNonNullField(fields, "bash", "done turn only allows bash when it is null"); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
		}
		if err := turnjson.RejectPresentNonNullField(fields, "question", "done turn only allows question when it is null"); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
		}
		return &DoneTurn{Summary: env.Summary, Reasoning: env.Reasoning}, nil
	case "":
		return nil, fmt.Errorf("%w: missing type field", ErrUnknownTurnType)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownTurnType, env.Type)
	}
}

// invalidEscapePattern is coupled to encoding/json's current error text.
// If that format changes, we gracefully fall back to generic protocol hints.
var invalidEscapePattern = regexp.MustCompile(`invalid character '(.{1})' in string escape code`)

func invalidEscapeChar(err error) (byte, bool) {
	match := invalidEscapePattern.FindStringSubmatch(err.Error())
	if len(match) != 2 || len(match[1]) != 1 {
		return 0, false
	}
	return match[1][0], true
}

func errorToReason(err error) string {
	switch {
	case errors.Is(err, ErrInvalidJSON):
		return "invalid_json"
	case errors.Is(err, ErrMissingCommand):
		return "missing_command"
	case errors.Is(err, ErrEmptyClarify):
		return "empty_clarify"
	case errors.Is(err, ErrEmptySummary):
		return "empty_summary"
	case errors.Is(err, ErrUnknownTurnType):
		return "unknown_turn_type"
	default:
		return "unknown"
	}
}

// protocolCorrectionMessage returns a tagged-text correction message for the model.
func protocolCorrectionMessage(err error) string {
	hint := "Your previous response was ignored and no command ran. Respond with exactly one JSON object for the next turn from the current state: {\"type\":\"act\",\"bash\":{\"command\":\"...\",\"workdir\":null}} or {\"type\":\"clarify\",\"question\":\"...\"} or {\"type\":\"done\",\"summary\":\"...\"}. If you intended to run a command, resend only that act turn. Do not jump to done unless prior command results in this conversation already prove the task is complete."
	invalid, hasInvalid := invalidEscapeChar(err)
	switch {
	case hasInvalid && invalid == '|':
		hint += ` Your command contains \| which is not a valid JSON escape. Use \\| for a literal backslash-pipe, or just | if you do not want a backslash.`
	case hasInvalid && invalid == '`':
		hint += " Your command contains \\` which is not a valid JSON escape. Use \\\\` for a literal backslash-backtick, or just ` if you do not want a backslash."
	}
	return fmt.Sprintf("[protocol_error]\nreason: %s\nhint: %s\ndetail: %s\n[/protocol_error]",
		errorToReason(err), hint, err.Error())
}
