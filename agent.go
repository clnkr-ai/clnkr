package hew

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultMaxSteps caps agent iterations when MaxSteps is not set.
const DefaultMaxSteps = 100

// Agent runs the query-parse-execute loop.
type Agent struct {
	model    Model
	executor Executor
	messages []Message
	cwd      string
	Notify   func(Event)
	MaxSteps int
}

// NewAgent wires up an agent with the given model, executor, and working directory.
func NewAgent(model Model, executor Executor, cwd string) *Agent {
	return &Agent{
		model:    model,
		executor: executor,
		cwd:      cwd,
		MaxSteps: DefaultMaxSteps,
	}
}

func (a *Agent) notify(e Event) {
	if a.Notify != nil {
		a.Notify(e)
	}
}

// Messages returns a copy of the conversation history.
func (a *Agent) Messages() []Message {
	cp := make([]Message, len(a.messages))
	copy(cp, a.messages)
	return cp
}

// summarizeCommand returns a short display form of a command for debug output.
func summarizeCommand(cmd string) string {
	lines := strings.Split(cmd, "\n")
	first := lines[0]
	if len(lines) == 1 {
		return first
	}
	return fmt.Sprintf("%s ... (%d lines)", first, len(lines))
}

// Step runs one query-parse-execute cycle. It does not enforce step limits
// or track format errors — that is Run's job.
func (a *Agent) Step(ctx context.Context) (StepResult, error) {
	a.notify(EventDebug{Message: "querying model..."})
	resp, err := a.model.Query(ctx, a.messages)
	if err != nil {
		return StepResult{}, fmt.Errorf("query model: %w", err)
	}
	a.notify(EventDebug{Message: fmt.Sprintf("usage: %d input, %d output tokens", resp.Usage.InputTokens, resp.Usage.OutputTokens)})
	a.messages = append(a.messages, resp.Message)
	a.notify(EventResponse{Message: resp.Message, Usage: resp.Usage})

	action, err := ExtractCommand(resp.Message.Content)
	if errors.Is(err, ErrNoCommand) {
		a.notify(EventDebug{Message: "no bash block found"})
		a.notify(EventFormatError{})
		a.messages = append(a.messages, Message{
			Role:    "user",
			Content: "Your response did not include a bash code block. Include exactly one ```bash block, or ```bash\nexit\n``` to finish.",
		})
		return StepResult{Response: resp}, nil
	}
	if err != nil {
		return StepResult{}, fmt.Errorf("parse action: %w", err)
	}

	if action == "exit" {
		a.notify(EventDebug{Message: "parsed action: exit"})
		return StepResult{Response: resp, Action: "exit"}, nil
	}

	a.notify(EventDebug{Message: fmt.Sprintf("parsed action: %s", summarizeCommand(action))})
	a.updateCwd(action)
	a.notify(EventDebug{Message: fmt.Sprintf("cwd: %s", a.cwd)})
	a.notify(EventCommandStart{Command: action, Dir: a.cwd})

	output, execErr := a.executor.Execute(ctx, action, a.cwd)
	if execErr != nil {
		a.notify(EventDebug{Message: fmt.Sprintf("command error: %v", execErr)})
		output += "\n(error: " + execErr.Error() + ")"
	}
	a.notify(EventCommandDone{Output: output, Err: execErr})
	a.messages = append(a.messages, Message{Role: "user", Content: output})

	return StepResult{Response: resp, Action: action, Output: output, ExecErr: execErr}, nil
}

// Run loops Step until exit or step limit. Message history persists across calls.
func (a *Agent) Run(ctx context.Context, task string) error {
	a.messages = append(a.messages, Message{Role: "user", Content: task})
	steps := 0
	formatErrors := 0

	for {
		result, err := a.Step(ctx)
		if err != nil {
			return err
		}

		if result.Action == "exit" {
			return nil
		}

		if result.Action == "" {
			// Format error — Step already sent the reminder.
			formatErrors++
			a.notify(EventDebug{Message: fmt.Sprintf("consecutive format errors: %d", formatErrors)})
			if formatErrors >= 2 {
				return fmt.Errorf("consecutive format errors, exiting")
			}
			continue
		}

		formatErrors = 0
		steps++
		a.notify(EventDebug{Message: fmt.Sprintf("step %d/%d", steps, a.MaxSteps)})
		if a.MaxSteps > 0 && steps >= a.MaxSteps {
			a.notify(EventDebug{Message: "step limit reached, requesting summary"})
			a.messages = append(a.messages, Message{
				Role:    "user",
				Content: "Step limit reached. Summarize your progress and exit.",
			})
			resp, err := a.model.Query(ctx, a.messages)
			if err != nil {
				return fmt.Errorf("query model (final): %w", err)
			}
			a.messages = append(a.messages, resp.Message)
			a.notify(EventResponse{Message: resp.Message, Usage: resp.Usage})
			return nil
		}
	}
}

func (a *Agent) updateCwd(command string) {
	// Only track standalone cd commands — compound commands (cd /tmp && ls)
	// change directory in the subprocess but we can't reliably parse them.
	trimmed := strings.TrimSpace(command)
	if !strings.HasPrefix(trimmed, "cd ") {
		return
	}
	// Reject compound commands: &&, ||, ;, pipes, or multi-line
	if strings.ContainsAny(trimmed, "&|;\n") {
		return
	}
	parts := strings.Fields(trimmed)
	if len(parts) != 2 {
		return
	}
	target := parts[1]

	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	var newCwd string
	switch {
	case target == "~":
		if home == "" {
			return
		}
		newCwd = home
	case strings.HasPrefix(target, "~/"):
		if home == "" {
			return
		}
		newCwd = filepath.Join(home, target[2:])
	case filepath.IsAbs(target):
		newCwd = target
	default:
		newCwd = filepath.Join(a.cwd, target)
	}

	if info, err := os.Stat(newCwd); err == nil && info.IsDir() {
		a.cwd = newCwd
	}
}
