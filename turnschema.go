package clnkr

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

type turnEnvelope struct {
	Type         string                  `json:"type"`
	Bash         *turnBashEnvelope       `json:"bash,omitempty"`
	Question     string                  `json:"question,omitempty"`
	Summary      string                  `json:"summary,omitempty"`
	Verification *completionVerification `json:"verification,omitempty"`
	KnownRisks   []string                `json:"known_risks,omitempty"`
	Reasoning    string                  `json:"reasoning,omitempty"`
}

type turnBashEnvelope struct {
	Commands []turnBashAction `json:"commands"`
}

type turnBashAction struct {
	Command string  `json:"command"`
	Workdir *string `json:"workdir"`
}

type completionVerification struct {
	Status string              `json:"status"`
	Checks []verificationCheck `json:"checks"`
}

type verificationCheck struct {
	Command  string `json:"command"`
	Outcome  string `json:"outcome"`
	Evidence string `json:"evidence"`
}

// ParseTurn validates an exact canonical turn without recovery or prose extraction.
func ParseTurn(raw string) (Turn, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: empty response", ErrInvalidJSON)
	}

	var env turnEnvelope
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

func strictTurnFromEnvelope(env turnEnvelope, fields map[string]json.RawMessage) (Turn, error) {
	switch env.Type {
	case "act":
		if err := requireNestedArrayObjectFields(fields, "bash", "commands", "workdir"); err != nil {
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
		if err := requireDoneArrayFields(fields); err != nil {
			return nil, err
		}
		verification, err := canonicalCompletionVerification(env.Verification, env.KnownRisks)
		if err != nil {
			return nil, err
		}
		return &DoneTurn{
			Summary:      env.Summary,
			Verification: verification,
			KnownRisks:   cloneStrings(env.KnownRisks),
			Reasoning:    env.Reasoning,
		}, nil
	case "":
		return nil, fmt.Errorf("%w: missing type field", ErrUnknownTurnType)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownTurnType, env.Type)
	}
}

func requireDoneArrayFields(fields map[string]json.RawMessage) error {
	rawVerification, ok := fields["verification"]
	if !ok {
		return fmt.Errorf("%w: missing required field %q", ErrInvalidJSON, "verification")
	}
	var verificationFields map[string]json.RawMessage
	if err := json.Unmarshal(rawVerification, &verificationFields); err != nil || verificationFields == nil {
		return fmt.Errorf("%w: verification must be an object", ErrInvalidJSON)
	}
	rawChecks, ok := verificationFields["checks"]
	if !ok {
		return fmt.Errorf("%w: missing required field %q", ErrInvalidJSON, "verification.checks")
	}
	if string(rawChecks) == "null" {
		return fmt.Errorf("%w: verification.checks must be an array", ErrInvalidJSON)
	}
	var checks []json.RawMessage
	if err := json.Unmarshal(rawChecks, &checks); err != nil {
		return fmt.Errorf("%w: verification.checks must be an array", ErrInvalidJSON)
	}

	rawRisks, ok := fields["known_risks"]
	if !ok {
		return fmt.Errorf("%w: missing required field %q", ErrInvalidJSON, "known_risks")
	}
	if string(rawRisks) == "null" {
		return fmt.Errorf("%w: known_risks must be an array", ErrInvalidJSON)
	}
	var risks []string
	if err := json.Unmarshal(rawRisks, &risks); err != nil {
		return fmt.Errorf("%w: known_risks must be an array", ErrInvalidJSON)
	}
	return nil
}

func canonicalCompletionVerification(raw *completionVerification, knownRisks []string) (CompletionVerification, error) {
	if raw == nil {
		return CompletionVerification{}, fmt.Errorf("%w: missing required field %q", ErrInvalidJSON, "verification")
	}
	status := VerificationStatus(strings.TrimSpace(raw.Status))
	switch status {
	case VerificationVerified, VerificationPartiallyVerified, VerificationNotVerified:
	default:
		return CompletionVerification{}, fmt.Errorf("%w: invalid verification status %q", ErrInvalidJSON, raw.Status)
	}
	checks := make([]VerificationCheck, 0, len(raw.Checks))
	for i, check := range raw.Checks {
		if strings.TrimSpace(check.Command) == "" {
			return CompletionVerification{}, fmt.Errorf("%w: verification.checks[%d].command must be non-empty", ErrInvalidJSON, i)
		}
		if strings.TrimSpace(check.Outcome) == "" {
			return CompletionVerification{}, fmt.Errorf("%w: verification.checks[%d].outcome must be non-empty", ErrInvalidJSON, i)
		}
		if strings.TrimSpace(check.Evidence) == "" {
			return CompletionVerification{}, fmt.Errorf("%w: verification.checks[%d].evidence must be non-empty", ErrInvalidJSON, i)
		}
		checks = append(checks, VerificationCheck{
			Command:  check.Command,
			Outcome:  check.Outcome,
			Evidence: check.Evidence,
		})
	}
	switch status {
	case VerificationVerified:
		if len(checks) == 0 {
			return CompletionVerification{}, fmt.Errorf("%w: verified done requires at least one verification check", ErrInvalidJSON)
		}
	case VerificationPartiallyVerified:
		if len(knownRisks) == 0 {
			return CompletionVerification{}, fmt.Errorf("%w: partially verified done requires at least one known risk", ErrInvalidJSON)
		}
	case VerificationNotVerified:
	}
	return CompletionVerification{Status: status, Checks: checks}, nil
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
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

func requireNestedArrayObjectFields(fields map[string]json.RawMessage, field, nested string, required ...string) error {
	raw, ok := fields[field]
	if !ok {
		return fmt.Errorf("missing required field %q", field)
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return fmt.Errorf("field %q must be object", field)
	}

	arrayRaw, ok := obj[nested]
	if !ok {
		return fmt.Errorf("missing required field %q", field+"."+nested)
	}

	var items []map[string]json.RawMessage
	if err := json.Unmarshal(arrayRaw, &items); err != nil {
		return fmt.Errorf("field %q must be array", field+"."+nested)
	}

	for i, item := range items {
		if item == nil {
			return fmt.Errorf("field %q must be object", fmt.Sprintf("%s.%s[%d]", field, nested, i))
		}
		for _, name := range required {
			if _, ok := item[name]; !ok {
				return fmt.Errorf("missing required field %q", fmt.Sprintf("%s.%s[%d].%s", field, nested, i, name))
			}
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
