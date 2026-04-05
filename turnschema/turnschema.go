package turnschema

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	clnkr "github.com/clnkr-ai/clnkr"
)

var ErrUnsupportedTurn = errors.New("unsupported turn")

type envelope struct {
	Type      string `json:"type"`
	Command   string `json:"command,omitempty"`
	Question  string `json:"question,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
}

// Schema returns the shared JSON schema used for structured provider output.
func Schema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"type": map[string]any{
				"type": "string",
				"enum": []string{"act", "clarify", "done"},
			},
			"command": map[string]any{
				"type": []string{"string", "null"},
			},
			"question": map[string]any{
				"type": []string{"string", "null"},
			},
			"summary": map[string]any{
				"type": []string{"string", "null"},
			},
			"reasoning": map[string]any{
				"type": []string{"string", "null"},
			},
		},
		"required": []string{"type", "command", "question", "summary", "reasoning"},
		"anyOf": []any{
			map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"type": map[string]any{
						"const": "act",
					},
					"command": map[string]any{
						"type": "string",
					},
					"question": map[string]any{
						"type": "null",
					},
					"summary": map[string]any{
						"type": "null",
					},
					"reasoning": map[string]any{
						"type": []string{"string", "null"},
					},
				},
				"required": []string{"type", "command", "question", "summary", "reasoning"},
			},
			map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"type": map[string]any{
						"const": "clarify",
					},
					"command": map[string]any{
						"type": "null",
					},
					"question": map[string]any{
						"type": "string",
					},
					"summary": map[string]any{
						"type": "null",
					},
					"reasoning": map[string]any{
						"type": []string{"string", "null"},
					},
				},
				"required": []string{"type", "command", "question", "summary", "reasoning"},
			},
			map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"type": map[string]any{
						"const": "done",
					},
					"command": map[string]any{
						"type": "null",
					},
					"question": map[string]any{
						"type": "null",
					},
					"summary": map[string]any{
						"type": "string",
					},
					"reasoning": map[string]any{
						"type": []string{"string", "null"},
					},
				},
				"required": []string{"type", "command", "question", "summary", "reasoning"},
			},
		},
	}
}

// Parse validates an exact JSON turn without recovery or prose extraction.
// It accepts both canonical internal turns and provider-shaped turns.
func Parse(raw string) (clnkr.Turn, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: empty response", clnkr.ErrInvalidJSON)
	}

	var env envelope
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return nil, fmt.Errorf("%w: %v", clnkr.ErrInvalidJSON, err)
	}
	if err := ensureSingleJSONObject(dec); err != nil {
		return nil, fmt.Errorf("%w: %v", clnkr.ErrInvalidJSON, err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil {
		return nil, fmt.Errorf("%w: %v", clnkr.ErrInvalidJSON, err)
	}

	return strictTurnFromEnvelope(env, fields)
}

// ParseProvider validates a provider payload against the required schema shape
// before parsing it into a canonical turn.
func ParseProvider(raw string) (clnkr.Turn, error) {
	if err := validateProviderShape(raw); err != nil {
		return nil, err
	}
	return Parse(raw)
}

// CanonicalJSON marshals a validated turn into canonical compact JSON.
func CanonicalJSON(turn clnkr.Turn) (string, error) {
	env, err := strictEnvelopeFromTurn(turn)
	if err != nil {
		return "", err
	}

	body, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("marshal canonical turn json: %w", err)
	}
	return string(body), nil
}

func ensureSingleJSONObject(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func validateProviderShape(raw string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return fmt.Errorf("%w: %v", clnkr.ErrInvalidJSON, err)
	}
	for _, field := range []string{"type", "command", "question", "summary", "reasoning"} {
		if _, ok := fields[field]; !ok {
			return fmt.Errorf("%w: missing required structured output field %q", clnkr.ErrInvalidJSON, field)
		}
	}
	return nil
}

func strictTurnFromEnvelope(env envelope, fields map[string]json.RawMessage) (clnkr.Turn, error) {
	switch env.Type {
	case "act":
		if env.Command == "" {
			return nil, clnkr.ErrMissingCommand
		}
		if err := rejectPresentNonNullField(fields, "question", "act turn only allows question when it is null"); err != nil {
			return nil, err
		}
		if err := rejectPresentNonNullField(fields, "summary", "act turn only allows summary when it is null"); err != nil {
			return nil, err
		}
		return &clnkr.ActTurn{Command: env.Command, Reasoning: env.Reasoning}, nil
	case "clarify":
		if env.Question == "" {
			return nil, clnkr.ErrEmptyClarify
		}
		if err := rejectPresentNonNullField(fields, "command", "clarify turn only allows command when it is null"); err != nil {
			return nil, err
		}
		if err := rejectPresentNonNullField(fields, "summary", "clarify turn only allows summary when it is null"); err != nil {
			return nil, err
		}
		return &clnkr.ClarifyTurn{Question: env.Question, Reasoning: env.Reasoning}, nil
	case "done":
		if env.Summary == "" {
			return nil, clnkr.ErrEmptySummary
		}
		if err := rejectPresentNonNullField(fields, "command", "done turn only allows command when it is null"); err != nil {
			return nil, err
		}
		if err := rejectPresentNonNullField(fields, "question", "done turn only allows question when it is null"); err != nil {
			return nil, err
		}
		return &clnkr.DoneTurn{Summary: env.Summary, Reasoning: env.Reasoning}, nil
	case "":
		return nil, fmt.Errorf("%w: missing type field", clnkr.ErrUnknownTurnType)
	default:
		return nil, fmt.Errorf("%w: %q", clnkr.ErrUnknownTurnType, env.Type)
	}
}

func rejectPresentNonNullField(fields map[string]json.RawMessage, field, detail string) error {
	if fields == nil {
		return nil
	}
	raw, ok := fields[field]
	if !ok {
		return nil
	}
	if string(raw) == "null" {
		return nil
	}
	return fmt.Errorf("%w: %s", clnkr.ErrInvalidJSON, detail)
}

func strictEnvelopeFromTurn(turn clnkr.Turn) (envelope, error) {
	switch v := turn.(type) {
	case clnkr.ActTurn:
		env := envelope{Type: "act", Command: v.Command, Reasoning: v.Reasoning}
		if _, err := strictTurnFromEnvelope(env, nil); err != nil {
			return envelope{}, err
		}
		return env, nil
	case *clnkr.ActTurn:
		if v == nil {
			return envelope{}, fmt.Errorf("canonical turn json: %w: nil *ActTurn", ErrUnsupportedTurn)
		}
		env := envelope{Type: "act", Command: v.Command, Reasoning: v.Reasoning}
		if _, err := strictTurnFromEnvelope(env, nil); err != nil {
			return envelope{}, err
		}
		return env, nil
	case clnkr.ClarifyTurn:
		env := envelope{Type: "clarify", Question: v.Question, Reasoning: v.Reasoning}
		if _, err := strictTurnFromEnvelope(env, nil); err != nil {
			return envelope{}, err
		}
		return env, nil
	case *clnkr.ClarifyTurn:
		if v == nil {
			return envelope{}, fmt.Errorf("canonical turn json: %w: nil *ClarifyTurn", ErrUnsupportedTurn)
		}
		env := envelope{Type: "clarify", Question: v.Question, Reasoning: v.Reasoning}
		if _, err := strictTurnFromEnvelope(env, nil); err != nil {
			return envelope{}, err
		}
		return env, nil
	case clnkr.DoneTurn:
		env := envelope{Type: "done", Summary: v.Summary, Reasoning: v.Reasoning}
		if _, err := strictTurnFromEnvelope(env, nil); err != nil {
			return envelope{}, err
		}
		return env, nil
	case *clnkr.DoneTurn:
		if v == nil {
			return envelope{}, fmt.Errorf("canonical turn json: %w: nil *DoneTurn", ErrUnsupportedTurn)
		}
		env := envelope{Type: "done", Summary: v.Summary, Reasoning: v.Reasoning}
		if _, err := strictTurnFromEnvelope(env, nil); err != nil {
			return envelope{}, err
		}
		return env, nil
	case nil:
		return envelope{}, fmt.Errorf("canonical turn json: %w: nil turn", ErrUnsupportedTurn)
	default:
		return envelope{}, fmt.Errorf("canonical turn json: %w: %T", ErrUnsupportedTurn, turn)
	}
}
