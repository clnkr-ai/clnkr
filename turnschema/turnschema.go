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
	Commands []bashAction `json:"commands"`
}

type bashAction struct {
	Command string  `json:"command"`
	Workdir *string `json:"workdir"`
}

func nullableStringSchema() map[string]any {
	return map[string]any{
		"anyOf": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "null"},
		},
	}
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
									"commands": map[string]any{
										"type":     "array",
										"minItems": 1,
										"maxItems": 3,
										"items": map[string]any{
											"type":                 "object",
											"additionalProperties": false,
											"properties": map[string]any{
												"command": map[string]any{
													"type": "string",
												},
												"workdir": nullableStringSchema(),
											},
											"required": []string{"command", "workdir"},
										},
									},
								},
								"required": []string{"commands"},
							},
							"question": map[string]any{
								"type": "null",
							},
							"summary": map[string]any{
								"type": "null",
							},
							"reasoning": nullableStringSchema(),
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
							"reasoning": nullableStringSchema(),
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
							"reasoning": nullableStringSchema(),
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
// It accepts canonical internal turns and unwrapped provider-shaped turns.
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
	env.Question, env.Summary, env.Reasoning = turnjson.NormalizeHumanText(env.Question), turnjson.NormalizeHumanText(env.Summary), turnjson.NormalizeHumanText(env.Reasoning)

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

// ProviderJSON marshals a validated turn into the wrapped provider wire shape.
func ProviderJSON(turn clnkr.Turn) (string, error) {
	switch v := turn.(type) {
	case clnkr.ActTurn:
		return turnjson.WireActJSON(batchToWireCommands(v.Bash.Commands), nilIfEmpty(v.Reasoning))
	case *clnkr.ActTurn:
		if v == nil {
			return "", fmt.Errorf("provider turn json: %w: nil *ActTurn", ErrUnsupportedTurn)
		}
		return turnjson.WireActJSON(batchToWireCommands(v.Bash.Commands), nilIfEmpty(v.Reasoning))
	case clnkr.ClarifyTurn:
		return turnjson.WireClarifyJSON(v.Question, nilIfEmpty(v.Reasoning))
	case *clnkr.ClarifyTurn:
		if v == nil {
			return "", fmt.Errorf("provider turn json: %w: nil *ClarifyTurn", ErrUnsupportedTurn)
		}
		return turnjson.WireClarifyJSON(v.Question, nilIfEmpty(v.Reasoning))
	case clnkr.DoneTurn:
		return turnjson.WireDoneJSON(v.Summary, nilIfEmpty(v.Reasoning))
	case *clnkr.DoneTurn:
		if v == nil {
			return "", fmt.Errorf("provider turn json: %w: nil *DoneTurn", ErrUnsupportedTurn)
		}
		return turnjson.WireDoneJSON(v.Summary, nilIfEmpty(v.Reasoning))
	case nil:
		return "", fmt.Errorf("provider turn json: %w: nil turn", ErrUnsupportedTurn)
	default:
		return "", fmt.Errorf("provider turn json: %w: %T", ErrUnsupportedTurn, turn)
	}
}

// NormalizeMessagesForProvider rewrites assistant turns into the wrapped wire
// shape so the model sees one deterministic protocol throughout history.
func NormalizeMessagesForProvider(messages []clnkr.Message) []clnkr.Message {
	normalized := make([]clnkr.Message, len(messages))
	for i, msg := range messages {
		normalized[i] = msg
		if msg.Role != "assistant" {
			continue
		}
		if turn, err := ParseProvider(msg.Content); err == nil {
			if wrapped, err := ProviderJSON(turn); err == nil {
				normalized[i].Content = wrapped
			}
			continue
		}
		if turn, err := Parse(msg.Content); err == nil {
			if wrapped, err := ProviderJSON(turn); err == nil {
				normalized[i].Content = wrapped
			}
		}
	}
	return normalized
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
	if _, ok := fields["type"]; !ok {
		return fmt.Errorf("%w: missing required structured output field %q", clnkr.ErrInvalidJSON, "type")
	}

	var turnType string
	if err := json.Unmarshal(fields["type"], &turnType); err != nil {
		return fmt.Errorf("%w: structured output field %q must be string", clnkr.ErrInvalidJSON, "type")
	}

	// Some providers omit explicit null siblings on wrapped clarify/done turns.
	// Keep act-specific structural checks here and let Parse enforce turn semantics.
	switch turnType {
	case "act":
		rawBash, ok := fields["bash"]
		if !ok {
			return fmt.Errorf("%w: missing required structured output field %q", clnkr.ErrInvalidJSON, "bash")
		}
		var bashFields map[string]json.RawMessage
		if err := json.Unmarshal(rawBash, &bashFields); err != nil {
			return fmt.Errorf("%w: structured output field %q must be object", clnkr.ErrInvalidJSON, "bash")
		}
		if bashFields == nil {
			return fmt.Errorf("%w: structured output field %q must be object", clnkr.ErrInvalidJSON, "bash")
		}
		for _, field := range []string{"commands"} {
			if _, ok := bashFields[field]; !ok {
				return fmt.Errorf("%w: missing required structured output field %q", clnkr.ErrInvalidJSON, "bash."+field)
			}
		}
		var commands []json.RawMessage
		if err := json.Unmarshal(bashFields["commands"], &commands); err != nil {
			return fmt.Errorf("%w: structured output field %q must be array", clnkr.ErrInvalidJSON, "bash.commands")
		}
		if len(commands) == 0 {
			return fmt.Errorf("%w: structured output field %q must contain at least 1 item", clnkr.ErrInvalidJSON, "bash.commands")
		}
		if len(commands) > 3 {
			return fmt.Errorf("%w: structured output field %q must contain at most 3 items", clnkr.ErrInvalidJSON, "bash.commands")
		}
		for i, raw := range commands {
			var commandFields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &commandFields); err != nil {
				return fmt.Errorf("%w: structured output field %q must be object", clnkr.ErrInvalidJSON, fmt.Sprintf("bash.commands[%d]", i))
			}
			if commandFields == nil {
				return fmt.Errorf("%w: structured output field %q must be object", clnkr.ErrInvalidJSON, fmt.Sprintf("bash.commands[%d]", i))
			}
			for _, field := range []string{"command", "workdir"} {
				if _, ok := commandFields[field]; !ok {
					return fmt.Errorf("%w: missing required structured output field %q", clnkr.ErrInvalidJSON, fmt.Sprintf("bash.commands[%d].%s", i, field))
				}
			}
			var command string
			if err := json.Unmarshal(commandFields["command"], &command); err != nil {
				return fmt.Errorf("%w: structured output field %q must be string", clnkr.ErrInvalidJSON, fmt.Sprintf("bash.commands[%d].command", i))
			}
			if string(commandFields["workdir"]) != "null" {
				var workdir string
				if err := json.Unmarshal(commandFields["workdir"], &workdir); err != nil {
					return fmt.Errorf("%w: structured output field %q must be string or null", clnkr.ErrInvalidJSON, fmt.Sprintf("bash.commands[%d].workdir", i))
				}
			}
		}
	}
	return nil
}

func strictTurnFromEnvelope(env envelope, fields map[string]json.RawMessage) (clnkr.Turn, error) {
	switch env.Type {
	case "act":
		if fields != nil {
			if err := turnjson.RequireNestedArrayObjectFields(fields, "bash", "commands", "workdir"); err != nil {
				return nil, fmt.Errorf("%w: %v", clnkr.ErrInvalidJSON, err)
			}
		}
		if env.Bash == nil || len(env.Bash.Commands) == 0 {
			return nil, clnkr.ErrMissingCommand
		}
		if len(env.Bash.Commands) > 3 {
			return nil, clnkr.ErrTooManyCommands
		}
		if err := rejectPresentNonNullField(fields, "question", "act turn only allows question when it is null"); err != nil {
			return nil, err
		}
		if err := rejectPresentNonNullField(fields, "summary", "act turn only allows summary when it is null"); err != nil {
			return nil, err
		}
		actions := make([]clnkr.BashAction, 0, len(env.Bash.Commands))
		for _, command := range env.Bash.Commands {
			if strings.TrimSpace(command.Command) == "" {
				return nil, clnkr.ErrMissingCommand
			}
			action := clnkr.BashAction{Command: command.Command}
			if command.Workdir != nil {
				action.Workdir = *command.Workdir
			}
			actions = append(actions, action)
		}
		return &clnkr.ActTurn{
			Bash:      clnkr.BashBatch{Commands: actions},
			Reasoning: env.Reasoning,
		}, nil
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
		env := envelope{Type: "act", Bash: bashEnvelopeFromBatch(v.Bash), Reasoning: v.Reasoning}
		if _, err := strictTurnFromEnvelope(env, nil); err != nil {
			return envelope{}, err
		}
		return env, nil
	case *clnkr.ActTurn:
		if v == nil {
			return envelope{}, fmt.Errorf("canonical turn json: %w: nil *ActTurn", ErrUnsupportedTurn)
		}
		env := envelope{Type: "act", Bash: bashEnvelopeFromBatch(v.Bash), Reasoning: v.Reasoning}
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

func bashEnvelopeFromBatch(batch clnkr.BashBatch) *bashEnvelope {
	commands := make([]bashAction, 0, len(batch.Commands))
	for _, command := range batch.Commands {
		commands = append(commands, bashAction{
			Command: command.Command,
			Workdir: workdirPtr(command.Workdir),
		})
	}
	return &bashEnvelope{Commands: commands}
}

func batchToWireCommands(commands []clnkr.BashAction) []turnjson.WireCommand {
	wireCommands := make([]turnjson.WireCommand, 0, len(commands))
	for _, command := range commands {
		wireCommands = append(wireCommands, turnjson.WireCommand{
			Command: command.Command,
			Workdir: workdirPtr(command.Workdir),
		})
	}
	return wireCommands
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func workdirPtr(workdir string) *string {
	if workdir == "" {
		return nil
	}
	return &workdir
}
