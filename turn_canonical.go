package clnkr

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CanonicalTurnJSON marshals a validated turn into the canonical transcript form.
func CanonicalTurnJSON(turn Turn) (string, error) {
	var env jsonEnvelope
	switch v := turn.(type) {
	case ActTurn:
		env.Type, env.Reasoning = "act", v.Reasoning
		if len(v.Bash.Commands) == 0 {
			return "", ErrMissingCommand
		}
		if len(v.Bash.Commands) > 3 {
			return "", ErrTooManyCommands
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
	case *ActTurn:
		if v == nil {
			return "", fmt.Errorf("canonical turn json: nil *ActTurn")
		}
		return CanonicalTurnJSON(*v)
	case ClarifyTurn:
		if strings.TrimSpace(v.Question) == "" {
			return "", ErrEmptyClarify
		}
		env = jsonEnvelope{Type: "clarify", Question: v.Question, Reasoning: v.Reasoning}
	case *ClarifyTurn:
		if v == nil {
			return "", fmt.Errorf("canonical turn json: nil *ClarifyTurn")
		}
		return CanonicalTurnJSON(*v)
	case DoneTurn:
		if strings.TrimSpace(v.Summary) == "" {
			return "", ErrEmptySummary
		}
		env = jsonEnvelope{Type: "done", Summary: v.Summary, Reasoning: v.Reasoning}
	case *DoneTurn:
		if v == nil {
			return "", fmt.Errorf("canonical turn json: nil *DoneTurn")
		}
		return CanonicalTurnJSON(*v)
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
