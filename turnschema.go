package clnkr

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// ParseTurn validates an exact canonical turn without recovery or prose extraction.
func ParseTurn(raw string) (Turn, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: empty response", ErrInvalidJSON)
	}

	var env jsonEnvelope
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	if err := ensureSingleTurnJSONObject(dec); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	env.Question = normalizeTurnHumanText(env.Question)
	env.Summary = normalizeTurnHumanText(env.Summary)
	env.Reasoning = normalizeTurnHumanText(env.Reasoning)
	return strictTurnFromEnvelope(env, fields)
}

func strictTurnFromEnvelope(env jsonEnvelope, fields map[string]json.RawMessage) (Turn, error) {
	switch env.Type {
	case "act":
		if err := requireActCommandWorkdirs(fields); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
		}
		if env.Bash == nil || len(env.Bash.Commands) == 0 {
			return nil, ErrMissingCommand
		}
		if err := rejectPresentField(fields, "question", "act turn only allows question when it is omitted"); err != nil {
			return nil, err
		}
		if err := rejectPresentField(fields, "summary", "act turn only allows summary when it is omitted"); err != nil {
			return nil, err
		}
		actions := make([]BashAction, 0, len(env.Bash.Commands))
		for _, command := range env.Bash.Commands {
			if strings.TrimSpace(command.Command) == "" {
				return nil, ErrMissingCommand
			}
			action := BashAction{Command: command.Command}
			if command.Workdir != nil {
				action.Workdir = *command.Workdir
			}
			actions = append(actions, action)
		}
		return &ActTurn{
			Bash:      BashBatch{Commands: actions},
			Reasoning: env.Reasoning,
		}, nil
	case "clarify":
		if strings.TrimSpace(env.Question) == "" {
			return nil, ErrEmptyClarify
		}
		if err := rejectPresentField(fields, "bash", "clarify turn only allows bash when it is omitted"); err != nil {
			return nil, err
		}
		if err := rejectPresentField(fields, "summary", "clarify turn only allows summary when it is omitted"); err != nil {
			return nil, err
		}
		return &ClarifyTurn{Question: env.Question, Reasoning: env.Reasoning}, nil
	case "done":
		if strings.TrimSpace(env.Summary) == "" {
			return nil, ErrEmptySummary
		}
		if err := rejectPresentField(fields, "bash", "done turn only allows bash when it is omitted"); err != nil {
			return nil, err
		}
		if err := rejectPresentField(fields, "question", "done turn only allows question when it is omitted"); err != nil {
			return nil, err
		}
		knownRisks, err := requireDoneArrayFields(fields)
		if err != nil {
			return nil, err
		}
		if env.Verification == nil {
			return nil, fmt.Errorf("%w: missing required field %q", ErrInvalidJSON, "verification")
		}
		if err := validateDoneVerification(*env.Verification, knownRisks); err != nil {
			return nil, err
		}
		return &DoneTurn{
			Summary:      env.Summary,
			Verification: *env.Verification,
			KnownRisks:   knownRisks,
			Reasoning:    env.Reasoning,
		}, nil
	case "":
		return nil, fmt.Errorf("%w: missing type field", ErrUnknownTurnType)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownTurnType, env.Type)
	}
}

func requireDoneArrayFields(fields map[string]json.RawMessage) ([]string, error) {
	rawVerification, ok := fields["verification"]
	if !ok {
		return nil, fmt.Errorf("%w: missing required field %q", ErrInvalidJSON, "verification")
	}
	var verificationFields map[string]json.RawMessage
	if err := json.Unmarshal(rawVerification, &verificationFields); err != nil || verificationFields == nil {
		return nil, fmt.Errorf("%w: verification must be an object", ErrInvalidJSON)
	}
	rawChecks, ok := verificationFields["checks"]
	if !ok {
		return nil, fmt.Errorf("%w: missing required field %q", ErrInvalidJSON, "verification.checks")
	}
	if string(rawChecks) == "null" {
		return nil, fmt.Errorf("%w: verification.checks must be an array", ErrInvalidJSON)
	}
	var checks []json.RawMessage
	if err := json.Unmarshal(rawChecks, &checks); err != nil {
		return nil, fmt.Errorf("%w: verification.checks must be an array", ErrInvalidJSON)
	}

	rawRisks, ok := fields["known_risks"]
	if !ok {
		return nil, fmt.Errorf("%w: missing required field %q", ErrInvalidJSON, "known_risks")
	}
	if string(rawRisks) == "null" {
		return nil, fmt.Errorf("%w: known_risks must be an array", ErrInvalidJSON)
	}
	var risks []string
	if err := json.Unmarshal(rawRisks, &risks); err != nil {
		return nil, fmt.Errorf("%w: known_risks must be an array", ErrInvalidJSON)
	}
	return risks, nil
}

func cloneStrings(values []string) []string {
	return append([]string{}, values...)
}

func rejectPresentField(fields map[string]json.RawMessage, field, detail string) error {
	if _, ok := fields[field]; ok {
		return fmt.Errorf("%w: %s", ErrInvalidJSON, detail)
	}
	return nil
}

func ensureSingleTurnJSONObject(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func requireActCommandWorkdirs(fields map[string]json.RawMessage) error {
	raw, ok := fields["bash"]
	if !ok {
		return fmt.Errorf("missing required field %q", "bash")
	}
	var bash struct {
		Commands []map[string]json.RawMessage `json:"commands"`
	}
	if err := json.Unmarshal(raw, &bash); err != nil || bash.Commands == nil {
		return fmt.Errorf("field %q must contain array %q", "bash", "commands")
	}
	for i, item := range bash.Commands {
		if item == nil {
			return fmt.Errorf("field %q must be object", fmt.Sprintf("bash.commands[%d]", i))
		}
		if _, ok := item["workdir"]; !ok {
			return fmt.Errorf("missing required field %q", fmt.Sprintf("bash.commands[%d].workdir", i))
		}
	}
	return nil
}

var escapedTurnListMarker = regexp.MustCompile(`(?m)^\\([*-])\s`)
var inlineEscapedTurnListMarker = regexp.MustCompile(`\\([*-])\s`)

func normalizeTurnHumanText(value string) string {
	if value == "" || !strings.Contains(value, `\`) {
		return value
	}

	normalized := value
	for range 2 {
		decoded, ok := decodeEscapedTurnHumanText(normalized)
		if !ok || decoded == normalized {
			break
		}
		normalized = decoded
	}

	normalized = escapedTurnListMarker.ReplaceAllString(normalized, `$1 `)
	if strings.Contains(normalized, "\n- ") ||
		strings.Contains(normalized, "\n* ") ||
		strings.HasPrefix(normalized, "- ") ||
		strings.HasPrefix(normalized, "* ") {
		normalized = inlineEscapedTurnListMarker.ReplaceAllString(normalized, "\n$1 ")
	}
	return normalized
}

func decodeEscapedTurnHumanText(value string) (string, bool) {
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
