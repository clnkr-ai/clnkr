package turnwire

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/clnkr-ai/clnkr"
)

type wireEnvelope struct {
	Turn wireTurn `json:"turn"`
}

type wireTurn struct {
	Type      string    `json:"type"`
	Bash      *wireBash `json:"bash"`
	Question  any       `json:"question"`
	Summary   any       `json:"summary"`
	Reasoning any       `json:"reasoning"`
}

type wireBash struct {
	Commands []wireCommand `json:"commands"`
}

type wireCommand struct {
	Command string  `json:"command"`
	Workdir *string `json:"workdir"`
}

func RequestSchema(includeMaxItems bool) map[string]any {
	return structuredOutputSchema(includeMaxItems)
}

func structuredOutputSchema(includeMaxItems bool) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"turn": map[string]any{
				"anyOf": []any{
					actTurnSchema(includeMaxItems),
					clarifyTurnSchema(),
					doneTurnSchema(),
				},
			},
		},
		"required": []string{"turn"},
	}
}

func actTurnSchema(includeMaxItems bool) map[string]any {
	commands := map[string]any{
		"type":     "array",
		"minItems": 1,
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"command": map[string]any{"type": "string"},
				"workdir": nullableStringSchema(),
			},
			"required": []string{"command", "workdir"},
		},
	}
	if includeMaxItems {
		commands["maxItems"] = 3
	}

	return map[string]any{
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
					"commands": commands,
				},
				"required": []string{"commands"},
			},
			"question":  map[string]any{"type": "null"},
			"summary":   map[string]any{"type": "null"},
			"reasoning": nullableStringSchema(),
		},
		"required": []string{"type", "bash", "question", "summary", "reasoning"},
	}
}

func clarifyTurnSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"type": map[string]any{
				"type":  "string",
				"const": "clarify",
			},
			"bash":      map[string]any{"type": "null"},
			"question":  map[string]any{"type": "string"},
			"summary":   map[string]any{"type": "null"},
			"reasoning": nullableStringSchema(),
		},
		"required": []string{"type", "bash", "question", "summary", "reasoning"},
	}
}

func doneTurnSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"type": map[string]any{
				"type":  "string",
				"const": "done",
			},
			"bash":      map[string]any{"type": "null"},
			"question":  map[string]any{"type": "null"},
			"summary":   map[string]any{"type": "string"},
			"reasoning": nullableStringSchema(),
		},
		"required": []string{"type", "bash", "question", "summary", "reasoning"},
	}
}

func nullableStringSchema() map[string]any {
	return map[string]any{
		"anyOf": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "null"},
		},
	}
}

func NormalizeMessagesForProvider(messages []clnkr.Message) []clnkr.Message {
	normalized := make([]clnkr.Message, len(messages))
	for i, msg := range messages {
		normalized[i] = msg
		if msg.Role != "assistant" {
			continue
		}
		if turn, err := ParseProviderTurn(msg.Content); err == nil {
			if wrapped, err := providerJSON(turn); err == nil {
				normalized[i].Content = wrapped
			}
			continue
		}
		if turn, err := clnkr.ParseTurn(msg.Content); err == nil {
			if wrapped, err := providerJSON(turn); err == nil {
				normalized[i].Content = wrapped
			}
		}
	}
	return normalized
}

func ParseProviderTurn(raw string) (clnkr.Turn, error) {
	innerRaw, err := extractWrappedTurn(raw)
	if err != nil {
		return nil, err
	}
	if err := validateWrappedTurnShape(innerRaw); err != nil {
		return nil, err
	}

	var payload wireTurnPayload
	dec := json.NewDecoder(strings.NewReader(innerRaw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		return nil, fmt.Errorf("%w: %v", clnkr.ErrInvalidJSON, err)
	}
	if err := ensureSingleJSONObject(dec); err != nil {
		return nil, fmt.Errorf("%w: %v", clnkr.ErrInvalidJSON, err)
	}
	payload.Question = normalizeHumanText(payload.Question)
	payload.Summary = normalizeHumanText(payload.Summary)
	payload.Reasoning = normalizeHumanText(payload.Reasoning)
	return turnFromProviderPayload(payload)
}

type wireTurnPayload struct {
	Type      string    `json:"type"`
	Bash      *wireBash `json:"bash"`
	Question  string    `json:"question"`
	Summary   string    `json:"summary"`
	Reasoning string    `json:"reasoning"`
}

func extractWrappedTurn(raw string) (string, error) {
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

func validateWrappedTurnShape(raw string) error {
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
		if _, ok := bashFields["commands"]; !ok {
			return fmt.Errorf("%w: missing required structured output field %q", clnkr.ErrInvalidJSON, "bash.commands")
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
		for i, rawCommand := range commands {
			var commandFields map[string]json.RawMessage
			if err := json.Unmarshal(rawCommand, &commandFields); err != nil {
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
		if err := rejectNonNullField(fields, "question", "act turn only allows question when it is null or omitted"); err != nil {
			return err
		}
		if err := rejectNonNullField(fields, "summary", "act turn only allows summary when it is null or omitted"); err != nil {
			return err
		}
	case "clarify":
		if err := rejectNonNullField(fields, "bash", "clarify turn only allows bash when it is null or omitted"); err != nil {
			return err
		}
		if err := rejectNonNullField(fields, "summary", "clarify turn only allows summary when it is null or omitted"); err != nil {
			return err
		}
	case "done":
		if err := rejectNonNullField(fields, "bash", "done turn only allows bash when it is null or omitted"); err != nil {
			return err
		}
		if err := rejectNonNullField(fields, "question", "done turn only allows question when it is null or omitted"); err != nil {
			return err
		}
	}
	return nil
}

func rejectNonNullField(fields map[string]json.RawMessage, field, detail string) error {
	raw, ok := fields[field]
	if ok && string(raw) != "null" {
		return fmt.Errorf("%w: %s", clnkr.ErrInvalidJSON, detail)
	}
	return nil
}

func turnFromProviderPayload(payload wireTurnPayload) (clnkr.Turn, error) {
	switch payload.Type {
	case "act":
		if payload.Bash == nil || len(payload.Bash.Commands) == 0 {
			return nil, clnkr.ErrMissingCommand
		}
		if len(payload.Bash.Commands) > 3 {
			return nil, clnkr.ErrTooManyCommands
		}
		actions := make([]clnkr.BashAction, 0, len(payload.Bash.Commands))
		for _, command := range payload.Bash.Commands {
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
			Reasoning: payload.Reasoning,
		}, nil
	case "clarify":
		if payload.Question == "" {
			return nil, clnkr.ErrEmptyClarify
		}
		return &clnkr.ClarifyTurn{Question: payload.Question, Reasoning: payload.Reasoning}, nil
	case "done":
		if payload.Summary == "" {
			return nil, clnkr.ErrEmptySummary
		}
		return &clnkr.DoneTurn{Summary: payload.Summary, Reasoning: payload.Reasoning}, nil
	case "":
		return nil, fmt.Errorf("%w: missing type field", clnkr.ErrUnknownTurnType)
	default:
		return nil, fmt.Errorf("%w: %q", clnkr.ErrUnknownTurnType, payload.Type)
	}
}

func providerJSON(turn clnkr.Turn) (string, error) {
	env, err := wrappedEnvelope(turn)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("provider turn json: %w", err)
	}
	return string(body), nil
}

func wrappedEnvelope(turn clnkr.Turn) (wireEnvelope, error) {
	switch v := turn.(type) {
	case clnkr.ActTurn:
		return wireEnvelope{
			Turn: wireTurn{
				Type:      "act",
				Bash:      &wireBash{Commands: wireCommands(v.Bash.Commands)},
				Question:  nil,
				Summary:   nil,
				Reasoning: nullableString(v.Reasoning),
			},
		}, nil
	case *clnkr.ActTurn:
		if v == nil {
			return wireEnvelope{}, fmt.Errorf("provider turn json: %w: nil *ActTurn", errUnsupportedTurn)
		}
		return wrappedEnvelope(*v)
	case clnkr.ClarifyTurn:
		return wireEnvelope{
			Turn: wireTurn{
				Type:      "clarify",
				Bash:      nil,
				Question:  v.Question,
				Summary:   nil,
				Reasoning: nullableString(v.Reasoning),
			},
		}, nil
	case *clnkr.ClarifyTurn:
		if v == nil {
			return wireEnvelope{}, fmt.Errorf("provider turn json: %w: nil *ClarifyTurn", errUnsupportedTurn)
		}
		return wrappedEnvelope(*v)
	case clnkr.DoneTurn:
		return wireEnvelope{
			Turn: wireTurn{
				Type:      "done",
				Bash:      nil,
				Question:  nil,
				Summary:   v.Summary,
				Reasoning: nullableString(v.Reasoning),
			},
		}, nil
	case *clnkr.DoneTurn:
		if v == nil {
			return wireEnvelope{}, fmt.Errorf("provider turn json: %w: nil *DoneTurn", errUnsupportedTurn)
		}
		return wrappedEnvelope(*v)
	case nil:
		return wireEnvelope{}, fmt.Errorf("provider turn json: %w: nil turn", errUnsupportedTurn)
	default:
		return wireEnvelope{}, fmt.Errorf("provider turn json: %w: %T", errUnsupportedTurn, turn)
	}
}

func wireCommands(commands []clnkr.BashAction) []wireCommand {
	wire := make([]wireCommand, 0, len(commands))
	for _, command := range commands {
		wire = append(wire, wireCommand{
			Command: command.Command,
			Workdir: workdirPtr(command.Workdir),
		})
	}
	return wire
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

var errUnsupportedTurn = errors.New("unsupported turn")

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

var escapedListMarker = regexp.MustCompile(`(?m)^\\([*-])\s`)
var inlineEscapedListMarker = regexp.MustCompile(`\\([*-])\s`)

func normalizeHumanText(value string) string {
	if value == "" || !strings.Contains(value, `\`) {
		return value
	}

	normalized := value
	for range 2 {
		decoded, ok := decodeEscapedHumanText(normalized)
		if !ok || decoded == normalized {
			break
		}
		normalized = decoded
	}

	normalized = escapedListMarker.ReplaceAllString(normalized, `$1 `)
	if strings.Contains(normalized, "\n- ") ||
		strings.Contains(normalized, "\n* ") ||
		strings.HasPrefix(normalized, "- ") ||
		strings.HasPrefix(normalized, "* ") {
		normalized = inlineEscapedListMarker.ReplaceAllString(normalized, "\n$1 ")
	}
	return normalized
}

func decodeEscapedHumanText(value string) (string, bool) {
	if !strings.Contains(value, `\n`) &&
		!strings.Contains(value, `\r`) &&
		!strings.Contains(value, `\\-`) &&
		!strings.Contains(value, `\\*`) &&
		!strings.Contains(value, `\"`) {
		return "", false
	}

	quoted := `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	decoded, err := strconv.Unquote(quoted)
	if err != nil {
		return "", false
	}
	return decoded, true
}

func workdirPtr(workdir string) *string {
	if workdir == "" {
		return nil
	}
	return &workdir
}
