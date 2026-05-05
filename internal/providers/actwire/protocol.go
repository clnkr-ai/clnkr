package actwire

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
	Type         string            `json:"type"`
	Bash         *wireBash         `json:"bash"`
	Question     any               `json:"question"`
	Summary      any               `json:"summary"`
	Verification *wireVerification `json:"verification,omitempty"`
	KnownRisks   any               `json:"known_risks,omitempty"`
	Reasoning    any               `json:"reasoning"`
}

type wireBash struct {
	Commands []wireCommand `json:"commands"`
}

type wireCommand struct {
	Command string  `json:"command"`
	Workdir *string `json:"workdir"`
}

type wireVerification struct {
	Status string      `json:"status"`
	Checks []wireCheck `json:"checks"`
}

type wireCheck struct {
	Command  string `json:"command"`
	Outcome  string `json:"outcome"`
	Evidence string `json:"evidence"`
}

func RequestSchema() map[string]any {
	return structuredOutputSchema(true)
}

// UnattendedRequestSchema accepts act and done turns, but not clarify.
func UnattendedRequestSchema() map[string]any {
	return structuredOutputSchema(false)
}

func FinalTurnSchema() map[string]any {
	return finalTurnSchema(false)
}

func DoneOnlySchema() map[string]any {
	return finalTurnSchema(true)
}

func structuredOutputSchema(allowClarify bool) map[string]any {
	choices := []any{actTurnSchema(), doneTurnSchema()}
	if allowClarify {
		choices = []any{actTurnSchema(), clarifyTurnSchema(), doneTurnSchema()}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"turn": map[string]any{
				"anyOf": choices,
			},
		},
		"required": []string{"turn"},
	}
}

func finalTurnSchema(doneOnly bool) map[string]any {
	choices := []any{doneTurnSchema()}
	if !doneOnly {
		choices = append([]any{clarifyTurnSchema()}, choices...)
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"turn": map[string]any{
				"anyOf": choices,
			},
		},
		"required": []string{"turn"},
	}
}

func actTurnSchema() map[string]any {
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
	checkSchema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"command":  map[string]any{"type": "string"},
			"outcome":  map[string]any{"type": "string"},
			"evidence": map[string]any{"type": "string"},
		},
		"required": []string{"command", "outcome", "evidence"},
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"type": map[string]any{
				"type":  "string",
				"const": "done",
			},
			"bash":     map[string]any{"type": "null"},
			"question": map[string]any{"type": "null"},
			"summary":  map[string]any{"type": "string"},
			"verification": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"status": map[string]any{
						"type": "string",
						"enum": []string{"verified", "partially_verified", "not_verified"},
					},
					"checks": map[string]any{
						"type":  "array",
						"items": checkSchema,
					},
				},
				"required": []string{"status", "checks"},
			},
			"known_risks": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"reasoning": nullableStringSchema(),
		},
		"required": []string{"type", "bash", "question", "summary", "verification", "known_risks", "reasoning"},
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
	Type         string            `json:"type"`
	Bash         *wireBash         `json:"bash"`
	Question     string            `json:"question"`
	Summary      string            `json:"summary"`
	Verification *wireVerification `json:"verification"`
	KnownRisks   []string          `json:"known_risks"`
	Reasoning    string            `json:"reasoning"`
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
		for _, field := range []string{"summary", "verification", "known_risks"} {
			if _, ok := fields[field]; !ok {
				return fmt.Errorf("%w: missing required structured output field %q", clnkr.ErrInvalidJSON, field)
			}
		}
		var risks []string
		if err := json.Unmarshal(fields["known_risks"], &risks); err != nil {
			return fmt.Errorf("%w: structured output field %q must be array of strings", clnkr.ErrInvalidJSON, "known_risks")
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
		verification, err := providerCompletionVerification(payload.Verification, payload.KnownRisks)
		if err != nil {
			return nil, err
		}
		return &clnkr.DoneTurn{
			Summary:      payload.Summary,
			Verification: verification,
			KnownRisks:   cloneStrings(payload.KnownRisks),
			Reasoning:    payload.Reasoning,
		}, nil
	case "":
		return nil, fmt.Errorf("%w: missing type field", clnkr.ErrUnknownTurnType)
	default:
		return nil, fmt.Errorf("%w: %q", clnkr.ErrUnknownTurnType, payload.Type)
	}
}

func providerCompletionVerification(raw *wireVerification, knownRisks []string) (clnkr.CompletionVerification, error) {
	if raw == nil {
		return clnkr.CompletionVerification{}, fmt.Errorf("%w: missing required field %q", clnkr.ErrInvalidJSON, "verification")
	}
	status := clnkr.VerificationStatus(strings.TrimSpace(raw.Status))
	switch status {
	case clnkr.VerificationVerified, clnkr.VerificationPartiallyVerified, clnkr.VerificationNotVerified:
	default:
		return clnkr.CompletionVerification{}, fmt.Errorf("%w: invalid verification status %q", clnkr.ErrInvalidJSON, raw.Status)
	}
	checks := make([]clnkr.VerificationCheck, 0, len(raw.Checks))
	for i, check := range raw.Checks {
		if strings.TrimSpace(check.Command) == "" {
			return clnkr.CompletionVerification{}, fmt.Errorf("%w: verification.checks[%d].command must be non-empty", clnkr.ErrInvalidJSON, i)
		}
		if strings.TrimSpace(check.Outcome) == "" {
			return clnkr.CompletionVerification{}, fmt.Errorf("%w: verification.checks[%d].outcome must be non-empty", clnkr.ErrInvalidJSON, i)
		}
		if strings.TrimSpace(check.Evidence) == "" {
			return clnkr.CompletionVerification{}, fmt.Errorf("%w: verification.checks[%d].evidence must be non-empty", clnkr.ErrInvalidJSON, i)
		}
		checks = append(checks, clnkr.VerificationCheck{
			Command:  check.Command,
			Outcome:  check.Outcome,
			Evidence: check.Evidence,
		})
	}
	if status == clnkr.VerificationVerified && len(checks) == 0 {
		return clnkr.CompletionVerification{}, fmt.Errorf("%w: verified done requires at least one verification check", clnkr.ErrInvalidJSON)
	}
	if status == clnkr.VerificationPartiallyVerified && len(knownRisks) == 0 {
		return clnkr.CompletionVerification{}, fmt.Errorf("%w: partially verified done requires at least one known risk", clnkr.ErrInvalidJSON)
	}
	return clnkr.CompletionVerification{Status: status, Checks: checks}, nil
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
				Type:         "done",
				Bash:         nil,
				Question:     nil,
				Summary:      v.Summary,
				Verification: wireCompletionVerification(v.Verification),
				KnownRisks:   cloneStrings(v.KnownRisks),
				Reasoning:    nullableString(v.Reasoning),
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

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

func wireCompletionVerification(verification clnkr.CompletionVerification) *wireVerification {
	checks := make([]wireCheck, 0, len(verification.Checks))
	for _, check := range verification.Checks {
		checks = append(checks, wireCheck{
			Command:  check.Command,
			Outcome:  check.Outcome,
			Evidence: check.Evidence,
		})
	}
	return &wireVerification{
		Status: string(verification.Status),
		Checks: checks,
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
