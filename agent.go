package clnkr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const DefaultMaxSteps = 100

var ErrClarificationNeeded = errors.New("clarification needed")

// Agent runs the query-parse-execute loop.
type Agent struct {
	model    Model
	executor Executor
	messages []Message
	cwd      string
	env      map[string]string
	started  bool
	Notify   func(Event)
	MaxSteps int
}

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

func (a *Agent) Messages() []Message {
	cp := make([]Message, len(a.messages))
	copy(cp, a.messages)
	return cp
}

// AddMessages prepends msgs before the first Step/Run call.
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

var sectionEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "[", "&#91;", "]", "&#93;")

func formatCommandOutput(result CommandResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[command]\n%s\n[/command]\n", result.Command)
	fmt.Fprintf(&b, "[exit_code]\n%d\n[/exit_code]\n", result.ExitCode)
	fmt.Fprintf(&b, "[stdout]\n%s\n[/stdout]\n", sectionEscaper.Replace(result.Stdout))
	fmt.Fprintf(&b, "[stderr]\n%s\n[/stderr]", sectionEscaper.Replace(result.Stderr))
	return b.String()
}

type executorEnvSetter interface {
	SetEnv(map[string]string)
}

// Step runs one query-parse-execute cycle. Policy lives in Run.
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

	turn, parseErr := ParseTurn(resp.Message.Content)
	if parseErr != nil {
		a.notify(EventProtocolFailure{Reason: errorToReason(parseErr), Raw: resp.Message.Content})
		a.messages = append(a.messages, Message{Role: "user", Content: protocolCorrectionMessage(parseErr)})
		return StepResult{Response: resp, ParseErr: parseErr}, nil
	}

	a.notify(EventDebug{Message: fmt.Sprintf("parsed turn: %T", turn)})

	act, isAct := turn.(*ActTurn)
	if !isAct {
		return StepResult{Response: resp, Turn: turn}, nil
	}
	a.syncExecutorEnv()
	a.notify(EventCommandStart{Command: act.Command, Dir: a.cwd})

	execResult, execErr := a.executor.Execute(ctx, act.Command, a.cwd)
	a.applyShellState(act.Command)
	a.notify(EventDebug{Message: fmt.Sprintf("cwd: %s", a.cwd)})
	payload := formatCommandOutput(execResult)
	if execErr != nil {
		a.notify(EventDebug{Message: fmt.Sprintf("command error: %v", execErr)})
	}
	a.notify(EventCommandDone{Command: act.Command, Stdout: execResult.Stdout, Stderr: execResult.Stderr, ExitCode: execResult.ExitCode, Err: execErr})

	a.messages = append(a.messages, Message{Role: "user", Content: payload})
	return StepResult{Response: resp, Turn: turn, Output: payload, ExecErr: execErr}, nil
}

// Run loops Step until done, clarify, or step limit.
func (a *Agent) Run(ctx context.Context, task string) error {
	a.messages = append(a.messages, Message{Role: "user", Content: task})
	steps := 0
	protocolErrors := 0

	for {
		result, err := a.Step(ctx)
		if err != nil {
			return err
		}

		if result.ParseErr != nil {
			protocolErrors++
			a.notify(EventDebug{Message: fmt.Sprintf("consecutive protocol errors: %d", protocolErrors)})
			if protocolErrors >= 3 {
				return fmt.Errorf("consecutive protocol failures, exiting")
			}
			continue
		}
		protocolErrors = 0

		switch result.Turn.(type) {
		case *DoneTurn:
			return nil
		case *ClarifyTurn:
			return ErrClarificationNeeded
		case *ActTurn:
			steps++
			a.notify(EventDebug{Message: fmt.Sprintf("step %d/%d", steps, a.MaxSteps)})
			if a.MaxSteps > 0 && steps >= a.MaxSteps {
				a.notify(EventDebug{Message: "step limit reached, requesting summary"})
				a.messages = append(a.messages, Message{
					Role:    "user",
					Content: "Step limit reached. Respond with {\"type\":\"done\",\"summary\":\"...\"} summarizing your progress.",
				})
				resp, err := a.model.Query(ctx, a.messages)
				if err != nil {
					return fmt.Errorf("query model (final): %w", err)
				}
				a.messages = append(a.messages, resp.Message)
				a.notify(EventResponse{Message: resp.Message, Usage: resp.Usage})
				if turn, parseErr := ParseTurn(resp.Message.Content); parseErr != nil {
					a.notify(EventDebug{Message: fmt.Sprintf("final response not a valid turn: %v", parseErr)})
				} else {
					a.notify(EventDebug{Message: fmt.Sprintf("final response turn: %T", turn)})
				}
				return nil
			}
		}
	}
}

func (a *Agent) syncExecutorEnv() {
	if setter, ok := a.executor.(executorEnvSetter); ok {
		setter.SetEnv(a.env)
	}
}

func (a *Agent) applyShellState(command string) {
	for _, segment := range leadingShellSegments(command) {
		if !a.applyStateSegment(segment) {
			return
		}
	}
}

func leadingShellSegments(command string) []string {
	var segments []string
	start := 0
	inSingle, inDouble, escaped := false, false, false
	for i := 0; i < len(command); i++ {
		c := command[i]
		if escaped {
			escaped = false
			continue
		}
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inDouble = false
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case ';', '\n':
			segments = append(segments, strings.TrimSpace(command[start:i]))
			start = i + 1
		case '&':
			if i+1 < len(command) && command[i+1] == '&' {
				segments = append(segments, strings.TrimSpace(command[start:i]))
				start = i + 2
				i++
				continue
			}
			return segments
		case '|':
			return segments
		}
	}
	segments = append(segments, strings.TrimSpace(command[start:]))
	return segments
}

func (a *Agent) applyStateSegment(segment string) bool {
	if segment == "" {
		return true
	}
	if exports, ok := parseExportSegment(segment); ok {
		if a.env == nil {
			a.env = make(map[string]string, len(exports))
		}
		for k, v := range exports {
			a.env[k] = v
		}
		return true
	}
	target, ok := parseCDSegment(segment)
	if !ok {
		return false
	}
	a.updateCwdTarget(target)
	return true
}

func parseCDSegment(segment string) (string, bool) {
	parts := strings.Fields(strings.TrimSpace(segment))
	if len(parts) != 2 {
		return "", false
	}
	if parts[0] != "cd" {
		return "", false
	}
	return strings.Trim(parts[1], `'"`), true
}

func parseExportSegment(segment string) (map[string]string, bool) {
	parts := strings.Fields(strings.TrimSpace(segment))
	if len(parts) < 2 || parts[0] != "export" {
		return nil, false
	}
	exports := make(map[string]string, len(parts)-1)
	for _, part := range parts[1:] {
		key, value, ok := strings.Cut(part, "=")
		if !ok || key == "" {
			return nil, false
		}
		exports[key] = strings.Trim(value, `'"`)
	}
	return exports, true
}

func (a *Agent) updateCwdTarget(target string) {
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
