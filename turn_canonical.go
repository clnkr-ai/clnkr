package clnkr

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CanonicalTurnJSON marshals a validated turn into the canonical transcript form.
func CanonicalTurnJSON(turn Turn) (string, error) {
	var env jsonEnvelope
	switch v := turnPointer(turn).(type) {
	case *ActTurn:
		if v == nil {
			return "", fmt.Errorf("canonical turn json: nil *ActTurn")
		}
		env.Type, env.Reasoning = "act", v.Reasoning
		if len(v.Bash.Commands) == 0 {
			return "", ErrMissingCommand
		}
		commands := make([]jsonCommand, 0, len(v.Bash.Commands))
		for _, command := range v.Bash.Commands {
			if strings.TrimSpace(command.Command) == "" {
				return "", ErrMissingCommand
			}
			workdir := (*string)(nil)
			if command.Workdir != "" {
				workdir = &command.Workdir
			}
			commands = append(commands, jsonCommand{Command: command.Command, Workdir: workdir})
		}
		env.Bash = &jsonBashEnvelope{Commands: commands}
	case *ClarifyTurn:
		if v == nil {
			return "", fmt.Errorf("canonical turn json: nil *ClarifyTurn")
		}
		if strings.TrimSpace(v.Question) == "" {
			return "", ErrEmptyClarify
		}
		env = jsonEnvelope{Type: "clarify", Question: v.Question, Reasoning: v.Reasoning}
	case *DoneTurn:
		if v == nil {
			return "", fmt.Errorf("canonical turn json: nil *DoneTurn")
		}
		if strings.TrimSpace(v.Summary) == "" {
			return "", ErrEmptySummary
		}
		if err := validateDoneVerification(v.Verification, v.KnownRisks); err != nil {
			return "", fmt.Errorf("canonical turn json: %w", err)
		}
		env = jsonEnvelope{
			Type:         "done",
			Summary:      v.Summary,
			Verification: &v.Verification,
			KnownRisks:   cloneStrings(v.KnownRisks),
			Reasoning:    v.Reasoning,
		}
	case nil:
		return "", fmt.Errorf("canonical turn json: nil turn")
	default:
		return "", fmt.Errorf("canonical turn json: unsupported turn type %T", turn)
	}

	body, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("canonical turn json: marshal: %w", err)
	}
	return string(body), nil
}

func validateDoneVerification(verification CompletionVerification, knownRisks []string) error {
	switch verification.Status {
	case VerificationVerified, VerificationPartiallyVerified, VerificationNotVerified:
	default:
		return fmt.Errorf("%w: invalid verification status %q", ErrInvalidJSON, verification.Status)
	}
	for i, check := range verification.Checks {
		if strings.TrimSpace(check.Command) == "" {
			return fmt.Errorf("%w: verification.checks[%d].command must be non-empty", ErrInvalidJSON, i)
		}
		if strings.TrimSpace(check.Outcome) == "" {
			return fmt.Errorf("%w: verification.checks[%d].outcome must be non-empty", ErrInvalidJSON, i)
		}
		if strings.TrimSpace(check.Evidence) == "" {
			return fmt.Errorf("%w: verification.checks[%d].evidence must be non-empty", ErrInvalidJSON, i)
		}
	}
	if verification.Status == VerificationVerified && len(verification.Checks) == 0 {
		return fmt.Errorf("%w: verified done requires at least one verification check", ErrInvalidJSON)
	}
	if verification.Status == VerificationPartiallyVerified && len(knownRisks) == 0 {
		return fmt.Errorf("%w: partially verified done requires at least one known risk", ErrInvalidJSON)
	}
	return nil
}
