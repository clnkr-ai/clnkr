package hew

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DefaultMaxSteps is the safety limit for agent iterations when no override is set.
const DefaultMaxSteps = 100

// Agent implements the core query-parse-execute loop.
type Agent struct {
	model    Model
	executor Executor
	messages []Message
	cwd      string
	out      io.Writer
	MaxSteps int
}

// NewAgent creates an agent with the given model, executor, working directory, and output writer.
func NewAgent(model Model, executor Executor, cwd string, out io.Writer) *Agent {
	return &Agent{
		model:    model,
		executor: executor,
		cwd:      cwd,
		out:      out,
		MaxSteps: DefaultMaxSteps,
	}
}

// Run executes the agent loop for the given task. Message history persists across calls.
func (a *Agent) Run(ctx context.Context, task string) error {
	a.messages = append(a.messages, Message{Role: "user", Content: task})
	steps := 0
	formatErrors := 0

	for {
		resp, err := a.model.Query(ctx, a.messages)
		if err != nil {
			return fmt.Errorf("query model: %w", err)
		}
		a.messages = append(a.messages, resp.Message)
		fmt.Fprintln(a.out, resp.Message.Content)

		action, err := ExtractCommand(resp.Message.Content)
		if errors.Is(err, ErrNoCommand) {
			formatErrors++
			if formatErrors >= 2 {
				return fmt.Errorf("consecutive format errors, exiting")
			}
			a.messages = append(a.messages, Message{
				Role:    "user",
				Content: "Your response did not include a bash code block. Include exactly one ```bash block, or ```bash\nexit\n``` to finish.",
			})
			continue
		}
		if err != nil {
			return fmt.Errorf("parse action: %w", err)
		}
		formatErrors = 0

		if action == "exit" {
			return nil
		}

		steps++
		if a.MaxSteps > 0 && steps >= a.MaxSteps {
			a.messages = append(a.messages, Message{
				Role:    "user",
				Content: "Step limit reached. Summarize your progress and exit.",
			})
			resp, err := a.model.Query(ctx, a.messages)
			if err != nil {
				return fmt.Errorf("query model (final): %w", err)
			}
			a.messages = append(a.messages, resp.Message)
			fmt.Fprintln(a.out, resp.Message.Content)
			return nil
		}

		a.updateCwd(action)
		output, execErr := a.executor.Execute(ctx, action, a.cwd)
		if execErr != nil {
			output += "\n(error: " + execErr.Error() + ")"
		}
		fmt.Fprintln(a.out, output)
		a.messages = append(a.messages, Message{Role: "user", Content: output})
	}
}

func (a *Agent) updateCwd(command string) {
	trimmed := strings.TrimSpace(command)
	if !strings.HasPrefix(trimmed, "cd ") {
		return
	}
	parts := strings.Fields(trimmed)
	if len(parts) != 2 {
		return
	}
	target := parts[1]

	var newCwd string
	switch {
	case target == "~":
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		newCwd = home
	case filepath.IsAbs(target):
		newCwd = target
	default:
		newCwd = filepath.Join(a.cwd, target)
	}

	if info, err := os.Stat(newCwd); err == nil && info.IsDir() {
		a.cwd = newCwd
	}
}
