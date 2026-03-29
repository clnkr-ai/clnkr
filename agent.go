package clnkr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const DefaultMaxSteps = 100

var ErrClarificationNeeded = errors.New("clarification needed")

// Agent coordinates model turns and command execution.
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

func (a *Agent) Cwd() string {
	return a.cwd
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
	a.restoreExecutionStateFromMessages()
	return nil
}

// AppendUserMessage appends a user message after the agent has started or for
// frontends that drive the loop manually.
func (a *Agent) AppendUserMessage(text string) {
	a.started = true
	a.messages = append(a.messages, Message{Role: "user", Content: text})
}

var sectionEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "[", "&#91;", "]", "&#93;")

func formatCommandOutput(result CommandResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[command]\n%s\n[/command]\n", sectionEscaper.Replace(result.Command))
	fmt.Fprintf(&b, "[exit_code]\n%d\n[/exit_code]\n", result.ExitCode)
	fmt.Fprintf(&b, "[stdout]\n%s\n[/stdout]\n", sectionEscaper.Replace(result.Stdout))
	fmt.Fprintf(&b, "[stderr]\n%s\n[/stderr]", sectionEscaper.Replace(result.Stderr))
	return b.String()
}

type transcriptState struct {
	Source string `json:"source"`
	Kind   string `json:"kind"`
	Cwd    string `json:"cwd"`
}

func formatStateMessage(cwd string) string {
	body, err := json.Marshal(transcriptState{
		Source: "clnkr",
		Kind:   "state",
		Cwd:    cwd,
	})
	if err != nil {
		panic(fmt.Sprintf("marshal state message: %v", err))
	}
	return fmt.Sprintf("[state]\n%s\n[/state]", body)
}

func extractLatestCwd(messages []Message) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		if cwd, ok := extractStateCwd(messages[i].Content); ok {
			return cwd, true
		}
	}
	return "", false
}

func extractStateCwd(content string) (string, bool) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "[state]") || !strings.HasSuffix(content, "[/state]") {
		return "", false
	}
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(content, "[state]"), "[/state]"))
	var state transcriptState
	if err := json.Unmarshal([]byte(body), &state); err != nil {
		return "", false
	}
	if state.Source != "clnkr" || state.Kind != "state" || state.Cwd == "" {
		return "", false
	}
	return state.Cwd, true
}

func (a *Agent) restoreExecutionStateFromMessages() {
	if cwd, ok := extractLatestCwd(a.messages); ok {
		a.cwd = cwd
	}
}

func (a *Agent) appendStateMessageIfNeeded() {
	if cwd, ok := extractLatestCwd(a.messages); ok && cwd == a.cwd {
		return
	}
	a.messages = append(a.messages, Message{Role: "user", Content: formatStateMessage(a.cwd)})
}

// ExecutorStateSetter is optional. Executors that skip it must populate
// PostCwd/PostEnv themselves for cross-turn state.
type ExecutorStateSetter interface {
	SetEnv(map[string]string)
}

// Step runs one query-parse cycle. Policy lives in Run.
func (a *Agent) Step(ctx context.Context) (StepResult, error) {
	a.started = true
	a.appendStateMessageIfNeeded()
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
	return StepResult{Response: resp, Turn: turn}, nil
}

// ExecuteTurn runs an act turn and appends the command result payload.
func (a *Agent) ExecuteTurn(ctx context.Context, act *ActTurn) (StepResult, error) {
	if setter, ok := a.executor.(ExecutorStateSetter); ok {
		setter.SetEnv(a.env)
	}
	a.notify(EventCommandStart{Command: act.Command, Dir: a.cwd})

	execResult, execErr := a.executor.Execute(ctx, act.Command, a.cwd)
	if execResult.PostCwd != "" {
		a.cwd = execResult.PostCwd
	}
	// PostEnv is a full next-turn snapshot; nil means no new snapshot captured.
	if execResult.PostEnv != nil {
		a.env = execResult.PostEnv
	}
	a.notify(EventDebug{Message: fmt.Sprintf("cwd: %s", a.cwd)})
	payload := formatCommandOutput(execResult)
	if execErr != nil {
		a.notify(EventDebug{Message: fmt.Sprintf("command error: %v", execErr)})
	}
	a.notify(EventCommandDone{Command: act.Command, Stdout: execResult.Stdout, Stderr: execResult.Stderr, ExitCode: execResult.ExitCode, Err: execErr})

	a.messages = append(a.messages, Message{Role: "user", Content: payload})
	a.appendStateMessageIfNeeded()
	return StepResult{Turn: act, Output: payload, ExecErr: execErr}, nil
}

// RequestStepLimitSummary asks the model for a final done summary after the
// caller decides the step budget is exhausted.
func (a *Agent) RequestStepLimitSummary(ctx context.Context) error {
	a.notify(EventDebug{Message: "step limit reached, requesting summary"})
	a.AppendUserMessage("Step limit reached. Respond with {\"type\":\"done\",\"summary\":\"...\"} summarizing your progress.")

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

// Run loops Step until done, clarify, or step limit, executing act turns
// immediately as the full-send policy path.
func (a *Agent) Run(ctx context.Context, task string) error {
	a.AppendUserMessage(task)
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
			act := result.Turn.(*ActTurn)
			if _, err := a.ExecuteTurn(ctx, act); err != nil {
				return err
			}
			steps++
			a.notify(EventDebug{Message: fmt.Sprintf("step %d/%d", steps, a.MaxSteps)})
			if a.MaxSteps > 0 && steps >= a.MaxSteps {
				return a.RequestStepLimitSummary(ctx)
			}
		}
	}
}
