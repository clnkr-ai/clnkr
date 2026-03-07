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
	Verbose  bool
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

func (a *Agent) debug(format string, args ...interface{}) {
	if a.Verbose {
		fmt.Fprintf(a.out, "[hew] "+format+"\n", args...)
	}
}

// Run executes the agent loop for the given task. Message history persists across calls.
func (a *Agent) Run(ctx context.Context, task string) error {
	a.messages = append(a.messages, Message{Role: "user", Content: task})
	steps := 0
	formatErrors := 0

	for {
		a.debug("querying model...")
		resp, err := a.model.Query(ctx, a.messages)
		if err != nil {
			return fmt.Errorf("query model: %w", err)
		}
		a.debug("usage: %d input, %d output tokens", resp.Usage.InputTokens, resp.Usage.OutputTokens)
		a.messages = append(a.messages, resp.Message)
		fmt.Fprintln(a.out, resp.Message.Content)

		action, err := ExtractCommand(resp.Message.Content)
		if errors.Is(err, ErrNoCommand) {
			formatErrors++
			a.debug("no bash block found (consecutive: %d)", formatErrors)
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
			a.debug("parsed action: exit")
			return nil
		}

		a.debug("parsed action: %s", action)
		steps++
		a.debug("step %d/%d", steps, a.MaxSteps)
		if a.MaxSteps > 0 && steps >= a.MaxSteps {
			a.debug("step limit reached, requesting summary")
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
		a.debug("cwd: %s", a.cwd)
		fmt.Fprintf(a.out, "--- running: %s ---\n", action)
		output, execErr := a.executor.Execute(ctx, action, a.cwd)
		if execErr != nil {
			a.debug("command error: %v", execErr)
			output += "\n(error: " + execErr.Error() + ")"
		}
		fmt.Fprintln(a.out, output)
		fmt.Fprintln(a.out, "--- done ---")
		a.messages = append(a.messages, Message{Role: "user", Content: output})
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
