package hew

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Turn is a sealed interface for structured model turns.
type Turn interface{ turn() }

type ActTurn struct {
	Command   string `json:"command"`
	Reasoning string `json:"reasoning,omitempty"`
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
	Type      string `json:"type"`
	Command   string `json:"command,omitempty"`
	Question  string `json:"question,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
}

var (
	ErrInvalidJSON     = errors.New("invalid JSON")
	ErrMissingCommand  = errors.New("act turn requires command")
	ErrEmptyClarify    = errors.New("clarify turn requires non-empty question")
	ErrEmptySummary    = errors.New("done turn requires non-empty summary")
	ErrUnknownTurnType = errors.New("unknown turn type")
)

var jsonBlock = regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n?```")

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
	var env jsonEnvelope
	if err := json.Unmarshal([]byte(jsonStr), &env); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}

	switch env.Type {
	case "act":
		if env.Command == "" {
			return nil, ErrMissingCommand
		}
		return &ActTurn{Command: env.Command, Reasoning: env.Reasoning}, nil
	case "clarify":
		if env.Question == "" {
			return nil, ErrEmptyClarify
		}
		return &ClarifyTurn{Question: env.Question, Reasoning: env.Reasoning}, nil
	case "done":
		if env.Summary == "" {
			return nil, ErrEmptySummary
		}
		return &DoneTurn{Summary: env.Summary, Reasoning: env.Reasoning}, nil
	case "":
		return nil, fmt.Errorf("%w: missing type field", ErrUnknownTurnType)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownTurnType, env.Type)
	}
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
	return fmt.Sprintf("[protocol_error]\nreason: %s\nhint: Respond with a single JSON object: {\"type\":\"act\",\"command\":\"...\"} or {\"type\":\"clarify\",\"question\":\"...\"} or {\"type\":\"done\",\"summary\":\"...\"}.\ndetail: %s\n[/protocol_error]",
		errorToReason(err), err.Error())
}
