package turnschema

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	clnkr "github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/turnjson"
)

var ErrUnsupportedTurn = errors.New("unsupported turn")

type envelope struct {
	Type      string        `json:"type"`
	Bash      *bashEnvelope `json:"bash,omitempty"`
	Question  string        `json:"question,omitempty"`
	Summary   string        `json:"summary,omitempty"`
	Reasoning string        `json:"reasoning,omitempty"`
}

type bashEnvelope struct {
	Command string  `json:"command"`
	Workdir *string `json:"workdir"`
}

// Schema returns the shared JSON schema used for structured provider output.
func Schema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"turn": map[string]any{
				"anyOf": []any{
					map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]any{
							"type": map[string]any{
								"type":  "string",
								"const": "act",
							},
							"bash": map[string]any{
								"type":                 "object",
								"additionalProperties": false,
								"properties": map[string]any{
									"command": map[string]any{
										"type": "string",
									},
									"workdir": map[string]any{
										"type": []string{"string", "null"},
									},
								},
								"required": []string{"command", "workdir"},
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
						"required": []string{"type", "bash", "question", "summary", "reasoning"},
					},
					map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]any{
							"type": map[string]any{
								"type":  "string",
								"const": "clarify",
							},
							"bash": map[string]any{
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
						"required": []string{"type", "bash", "question", "summary", "reasoning"},
					},
					map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]any{
							"type": map[string]any{
								"type":  "string",
								"const": "done",
							},
							"bash": map[string]any{
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
						"required": []string{"type", "bash", "question", "summary", "reasoning"},
					},
				},
			},
		},
		"required": []string{"turn"},
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
	innerRaw, err := extractProviderTurn(raw)
	if err != nil {
		return nil, err
	}
	if err := validateProviderTurnShape(innerRaw); err != nil {
		return nil, err
	}
	return Parse(innerRaw)
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

func extractProviderTurn(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%w: empty response", clnkr.ErrInvalidJSON)
	}

	var envFields map[string]json.RawMessage
	dec := json.NewDecoder(strings.NewReader(trimmed))
	if err := dec.Decode(&envFields); err != nil {
		return "", fmt.Errorf("%w: %v", clnkr.ErrInvalidJSON, err)
	}
	if err := ensureSingleJSONObject(dec); err != nil {
		return "", fmt.Errorf("%w: %v", clnkr.ErrInvalidJSON, err)
	}

	turnRaw, ok := envFields["turn"]
	if !ok {
		return "", fmt.Errorf("%w: missing required structured output field %q", clnkr.ErrInvalidJSON, "turn")
	}
	if len(envFields) != 1 {
		for field := range envFields {
			if field != "turn" {
				return "", fmt.Errorf("%w: unknown structured output field %q", clnkr.ErrInvalidJSON, field)
			}
		}
	}

	var turnFields map[string]json.RawMessage
	if err := json.Unmarshal(turnRaw, &turnFields); err != nil {
		return "", fmt.Errorf("%w: structured output field %q must be object", clnkr.ErrInvalidJSON, "turn")
	}
	if turnFields == nil {
		return "", fmt.Errorf("%w: structured output field %q must be object", clnkr.ErrInvalidJSON, "turn")
	}
	return string(turnRaw), nil
}

func validateProviderTurnShape(raw string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return fmt.Errorf("%w: %v", clnkr.ErrInvalidJSON, err)
	}
	for _, field := range []string{"type", "bash", "question", "summary", "reasoning"} {
		if _, ok := fields[field]; !ok {
			return fmt.Errorf("%w: missing required structured output field %q", clnkr.ErrInvalidJSON, field)
		}
	}

	var turnType string
	if err := json.Unmarshal(fields["type"], &turnType); err != nil {
		return fmt.Errorf("%w: structured output field %q must be string", clnkr.ErrInvalidJSON, "type")
	}

	switch turnType {
	case "act":
		var bashFields map[string]json.RawMessage
		if err := json.Unmarshal(fields["bash"], &bashFields); err != nil {
			return fmt.Errorf("%w: structured output field %q must be object", clnkr.ErrInvalidJSON, "bash")
		}
		if bashFields == nil {
			return fmt.Errorf("%w: structured output field %q must be object", clnkr.ErrInvalidJSON, "bash")
		}
		for _, field := range []string{"command", "workdir"} {
			if _, ok := bashFields[field]; !ok {
				return fmt.Errorf("%w: missing required structured output field %q", clnkr.ErrInvalidJSON, "bash."+field)
			}
		}
		var command string
		if err := json.Unmarshal(bashFields["command"], &command); err != nil {
			return fmt.Errorf("%w: structured output field %q must be string", clnkr.ErrInvalidJSON, "bash.command")
		}
		if string(bashFields["workdir"]) != "null" {
			var workdir string
			if err := json.Unmarshal(bashFields["workdir"], &workdir); err != nil {
				return fmt.Errorf("%w: structured output field %q must be string or null", clnkr.ErrInvalidJSON, "bash.workdir")
			}
		}
	case "clarify", "done":
		if string(fields["bash"]) != "null" {
			return fmt.Errorf("%w: structured output field %q must be null", clnkr.ErrInvalidJSON, "bash")
		}
	}
	return nil
}

func strictTurnFromEnvelope(env envelope, fields map[string]json.RawMessage) (clnkr.Turn, error) {
	switch env.Type {
	case "act":
		if env.Bash == nil || env.Bash.Command == "" {
			return nil, clnkr.ErrMissingCommand
		}
		if fields != nil {
			if err := turnjson.RequireObjectFields(fields, "bash", "command", "workdir"); err != nil {
				return nil, fmt.Errorf("%w: %v", clnkr.ErrInvalidJSON, err)
			}
		}
		if err := rejectPresentNonNullField(fields, "question", "act turn only allows question when it is null"); err != nil {
			return nil, err
		}
		if err := rejectPresentNonNullField(fields, "summary", "act turn only allows summary when it is null"); err != nil {
			return nil, err
		}
		act := &clnkr.ActTurn{
			Bash:      clnkr.BashAction{Command: env.Bash.Command},
			Reasoning: env.Reasoning,
		}
		if env.Bash.Workdir != nil {
			act.Bash.Workdir = *env.Bash.Workdir
		}
		return act, nil
	case "clarify":
		if env.Question == "" {
			return nil, clnkr.ErrEmptyClarify
		}
		if err := rejectPresentNonNullField(fields, "bash", "clarify turn only allows bash when it is null"); err != nil {
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
		if err := rejectPresentNonNullField(fields, "bash", "done turn only allows bash when it is null"); err != nil {
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
		env := envelope{Type: "act", Bash: bashEnvelopeFromAction(v.Bash), Reasoning: v.Reasoning}
		if _, err := strictTurnFromEnvelope(env, nil); err != nil {
			return envelope{}, err
		}
		return env, nil
	case *clnkr.ActTurn:
		if v == nil {
			return envelope{}, fmt.Errorf("canonical turn json: %w: nil *ActTurn", ErrUnsupportedTurn)
		}
		env := envelope{Type: "act", Bash: bashEnvelopeFromAction(v.Bash), Reasoning: v.Reasoning}
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

func bashEnvelopeFromAction(action clnkr.BashAction) *bashEnvelope {
	var workdir *string
	if action.Workdir != "" {
		workdir = &action.Workdir
	}
	return &bashEnvelope{
		Command: action.Command,
		Workdir: workdir,
	}
}
