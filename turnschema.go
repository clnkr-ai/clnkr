package clnkr

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type rawTurnEnvelope struct {
	Type         string          `json:"type"`
	Bash         json.RawMessage `json:"bash"`
	Question     json.RawMessage `json:"question"`
	Summary      json.RawMessage `json:"summary"`
	Verification json.RawMessage `json:"verification"`
	KnownRisks   json.RawMessage `json:"known_risks"`
	Reasoning    string          `json:"reasoning"`
}

type rawBashEnvelope struct {
	Commands []rawCommand `json:"commands"`
}

type rawCommand struct {
	Command string          `json:"command"`
	Workdir json.RawMessage `json:"workdir"`
}

type rawVerification struct {
	Status VerificationStatus `json:"status"`
	Checks json.RawMessage    `json:"checks"`
}

type rawTurnField struct {
	name  string
	value json.RawMessage
}

// ParseTurn validates an exact canonical turn without recovery or prose extraction.
func ParseTurn(raw string) (Turn, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: empty response", ErrInvalidJSON)
	}

	var env rawTurnEnvelope
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	if err := ensureSingleTurnJSONObject(dec); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}

	return strictTurnFromEnvelope(env)
}

func strictTurnFromEnvelope(env rawTurnEnvelope) (Turn, error) {
	switch env.Type {
	case "act":
		bash, err := parseActBash(env.Bash)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
		}
		if len(bash.Commands) == 0 {
			return nil, ErrMissingCommand
		}
		if err := rejectTurnFields("act",
			rawTurnField{"question", env.Question},
			rawTurnField{"summary", env.Summary},
			rawTurnField{"verification", env.Verification},
			rawTurnField{"known_risks", env.KnownRisks},
		); err != nil {
			return nil, err
		}
		actions := make([]BashAction, 0, len(bash.Commands))
		for _, command := range bash.Commands {
			if strings.TrimSpace(command.Command) == "" {
				return nil, ErrMissingCommand
			}
			action := BashAction{Command: command.Command}
			if string(command.Workdir) != "null" {
				if err := json.Unmarshal(command.Workdir, &action.Workdir); err != nil {
					return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
				}
			}
			actions = append(actions, action)
		}
		return &ActTurn{
			Bash:      BashBatch{Commands: actions},
			Reasoning: env.Reasoning,
		}, nil
	case "clarify":
		question, err := parseStringField(env.Question)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
		}
		if strings.TrimSpace(question) == "" {
			return nil, ErrEmptyClarify
		}
		if err := rejectTurnFields("clarify",
			rawTurnField{"bash", env.Bash},
			rawTurnField{"summary", env.Summary},
			rawTurnField{"verification", env.Verification},
			rawTurnField{"known_risks", env.KnownRisks},
		); err != nil {
			return nil, err
		}
		return &ClarifyTurn{Question: question, Reasoning: env.Reasoning}, nil
	case "done":
		summary, err := parseStringField(env.Summary)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
		}
		if strings.TrimSpace(summary) == "" {
			return nil, ErrEmptySummary
		}
		if err := rejectTurnFields("done",
			rawTurnField{"bash", env.Bash},
			rawTurnField{"question", env.Question},
		); err != nil {
			return nil, err
		}
		verification, knownRisks, err := parseDoneFields(env.Verification, env.KnownRisks)
		if err != nil {
			return nil, err
		}
		if err := validateDoneVerification(verification, knownRisks); err != nil {
			return nil, err
		}
		return &DoneTurn{
			Summary:      summary,
			Verification: verification,
			KnownRisks:   knownRisks,
			Reasoning:    env.Reasoning,
		}, nil
	case "":
		return nil, fmt.Errorf("%w: missing type field", ErrUnknownTurnType)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownTurnType, env.Type)
	}
}

func parseActBash(raw json.RawMessage) (rawBashEnvelope, error) {
	if raw == nil {
		return rawBashEnvelope{}, fmt.Errorf("missing required field %q", "bash")
	}
	var bash rawBashEnvelope
	if string(raw) == "null" {
		return bash, fmt.Errorf("field %q must contain array %q", "bash", "commands")
	}
	if err := unmarshalStrict(raw, &bash); err != nil || bash.Commands == nil {
		if err != nil {
			return bash, err
		}
		return bash, fmt.Errorf("field %q must contain array %q", "bash", "commands")
	}
	for i, command := range bash.Commands {
		if command.Workdir == nil {
			return bash, fmt.Errorf(
				"missing required field %q",
				fmt.Sprintf("bash.commands[%d].workdir", i),
			)
		}
	}
	return bash, nil
}

func parseStringField(raw json.RawMessage) (string, error) {
	var value string
	if raw == nil || string(raw) == "null" {
		return value, nil
	}
	return value, json.Unmarshal(raw, &value)
}

func parseDoneFields(
	verificationJSON,
	rawRisks json.RawMessage,
) (CompletionVerification, []string, error) {
	if verificationJSON == nil {
		return CompletionVerification{}, nil, fmt.Errorf(
			"%w: missing required field %q",
			ErrInvalidJSON,
			"verification",
		)
	}
	if string(verificationJSON) == "null" {
		return CompletionVerification{}, nil, fmt.Errorf(
			"%w: verification must be an object",
			ErrInvalidJSON,
		)
	}
	var raw rawVerification
	if err := unmarshalStrict(verificationJSON, &raw); err != nil {
		return CompletionVerification{}, nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	if raw.Checks == nil {
		return CompletionVerification{}, nil, fmt.Errorf(
			"%w: missing required field %q",
			ErrInvalidJSON,
			"verification.checks",
		)
	}
	if string(raw.Checks) == "null" {
		return CompletionVerification{}, nil, fmt.Errorf(
			"%w: verification.checks must be an array",
			ErrInvalidJSON,
		)
	}
	var checks []VerificationCheck
	if err := unmarshalStrict(raw.Checks, &checks); err != nil {
		return CompletionVerification{}, nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	if rawRisks == nil {
		return CompletionVerification{}, nil, fmt.Errorf(
			"%w: missing required field %q",
			ErrInvalidJSON,
			"known_risks",
		)
	}
	if string(rawRisks) == "null" {
		return CompletionVerification{}, nil, fmt.Errorf(
			"%w: known_risks must be an array",
			ErrInvalidJSON,
		)
	}
	var risks []string
	if err := json.Unmarshal(rawRisks, &risks); err != nil {
		return CompletionVerification{}, nil, fmt.Errorf(
			"%w: known_risks must be an array",
			ErrInvalidJSON,
		)
	}
	return CompletionVerification{Status: raw.Status, Checks: checks}, risks, nil
}

func cloneStrings(values []string) []string {
	return append([]string{}, values...)
}

func rejectTurnFields(turn string, fields ...rawTurnField) error {
	for _, field := range fields {
		if field.value != nil {
			return fmt.Errorf(
				"%w: %s turn only allows %s when it is omitted",
				ErrInvalidJSON,
				turn,
				field.name,
			)
		}
	}
	return nil
}

func unmarshalStrict(raw json.RawMessage, dst any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
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
