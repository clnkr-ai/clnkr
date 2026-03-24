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

// DoneSignal is the marker the model emits (outside any code block) to signal task completion.
const DoneSignal = "<done/>"

// ErrClarificationNeeded means the model stopped to request more user input.
var ErrClarificationNeeded = errors.New("clarification needed")

// Agent runs the query-parse-execute loop.
type Agent struct {
	model    Model
	executor Executor
	messages []Message
	cwd      string
	started  bool
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

// AddMessages prepends msgs to the conversation history.
// Must be called before Run or Step; returns an error if the agent has already started.
func (a *Agent) AddMessages(msgs []Message) error {
	if a.started {
		return fmt.Errorf("add messages: agent already started")
	}
	if len(msgs) == 0 {
		return nil
	}
	combined := make([]Message, len(msgs)+len(a.messages))
	copy(combined, msgs)
	copy(combined[len(msgs):], a.messages)
	a.messages = combined
	return nil
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

func formatCommandOutput(result CommandResult) string {
	var b strings.Builder
	b.WriteString("[command]\n")
	b.WriteString(escapeSectionContent(result.Command))
	b.WriteString("\n[/command]\n")
	b.WriteString("[exit_code]\n")
	fmt.Fprintf(&b, "%d", result.ExitCode)
	b.WriteString("\n[/exit_code]\n")
	b.WriteString("[stdout]\n")
	b.WriteString(escapeSectionContent(result.Stdout))
	b.WriteString("\n[/stdout]\n")
	b.WriteString("[stderr]\n")
	b.WriteString(escapeSectionContent(result.Stderr))
	b.WriteString("\n[/stderr]")
	return b.String()
}

func escapeSectionContent(content string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"[", "&#91;",
		"]", "&#93;",
	)
	return replacer.Replace(content)
}

func hasCommandResult(messages []Message) bool {
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		if strings.Contains(msg.Content, "[command]\n") &&
			strings.Contains(msg.Content, "[stdout]\n") &&
			strings.Contains(msg.Content, "[stderr]\n") {
			return true
		}
	}
	return false
}

// Step runs one query-parse-execute cycle. It does not enforce step limits
// or track format errors — that is Run's job.
func (a *Agent) Step(ctx context.Context) (StepResult, error) {
	a.started = true
	a.notify(EventDebug{Message: "querying model..."})
	resp, err := a.model.Query(ctx, a.messages)
	if err != nil {
		return StepResult{}, fmt.Errorf("query model: %w", err)
	}
	a.notify(EventDebug{Message: fmt.Sprintf("usage: %d input, %d output tokens", resp.Usage.InputTokens, resp.Usage.OutputTokens)})
	a.messages = append(a.messages, resp.Message)
	a.notify(EventResponse{Message: resp.Message, Usage: resp.Usage})

	actions, parseErr := ExtractCommands(resp.Message.Content)
	hasDone := strings.Contains(resp.Message.Content, DoneSignal)

	if errors.Is(parseErr, ErrNoCommand) {
		if hasDone {
			a.notify(EventDebug{Message: "done signal received"})
			return StepResult{Response: resp, Action: DoneSignal}, nil
		}
		hasPriorCommandResult := hasCommandResult(a.messages)
		if !hasPriorCommandResult && !strings.Contains(resp.Message.Content, "```bash") && strings.TrimSpace(resp.Message.Content) != "" {
			a.notify(EventDebug{Message: "awaiting user clarification"})
			return StepResult{Response: resp, Action: ClarifySignal}, nil
		}
		a.notify(EventDebug{Message: "no bash block found"})
		a.notify(EventFormatError{})
		reminder := "Your response did not include a bash code block. Include one or more ```bash blocks, or include <done/> (with no code block) when finished."
		if hasPriorCommandResult {
			a.notify(EventDebug{Message: "post-command no-op guard triggered"})
			reminder = "After commands have run, do not ask the user for more pasted command output or clarification without acting. Inspect the working tree and the prior [command]/[stdout]/[stderr] results already in the conversation, then continue with a ```bash block, or include <done/> when finished."
		}
		a.messages = append(a.messages, Message{
			Role:    "user",
			Content: reminder,
		})
		return StepResult{Response: resp}, nil
	}
	if parseErr != nil {
		return StepResult{}, fmt.Errorf("parse action: %w", parseErr)
	}

	a.notify(EventDebug{Message: fmt.Sprintf("parsed %d bash block(s)", len(actions))})

	var combinedOutput string
	var lastErr error
	for _, action := range actions {
		a.notify(EventDebug{Message: fmt.Sprintf("parsed action: %s", summarizeCommand(action))})
		a.updateCwd(action)
		a.notify(EventDebug{Message: fmt.Sprintf("cwd: %s", a.cwd)})
		a.notify(EventCommandStart{Command: action, Dir: a.cwd})

		execResult, execErr := a.executor.Execute(ctx, action, a.cwd)
		payload := formatCommandOutput(execResult)
		if execErr != nil {
			a.notify(EventDebug{Message: fmt.Sprintf("command error: %v", execErr)})
		}
		a.notify(EventCommandDone{
			Command:  action,
			Stdout:   execResult.Stdout,
			Stderr:   execResult.Stderr,
			ExitCode: execResult.ExitCode,
			Err:      execErr,
		})

		if combinedOutput != "" && payload != "" {
			combinedOutput += "\n"
		}
		combinedOutput += payload
		lastErr = execErr
	}

	content := combinedOutput
	if content == "" {
		content = "(command completed with no output)"
	}
	a.messages = append(a.messages, Message{Role: "user", Content: content})

	result := StepResult{Response: resp, Action: actions[0], Output: combinedOutput, ExecErr: lastErr}

	// If the response also contained <done/>, signal completion after executing commands.
	if hasDone {
		a.notify(EventDebug{Message: "done signal received"})
		result.Action = DoneSignal
	}

	return result, nil
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

		if result.Action == DoneSignal {
			return nil
		}
		if result.Action == ClarifySignal {
			return ErrClarificationNeeded
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
				Content: "Step limit reached. Summarize your progress and include <done/> to finish.",
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
