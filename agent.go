package clnkr

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/clnkr-ai/clnkr/internal/core/transcript"
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
	return &Agent{model: model, executor: executor, cwd: cwd, MaxSteps: DefaultMaxSteps}
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

func (a *Agent) Cwd() string { return a.cwd }

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

func (a *Agent) restoreExecutionStateFromMessages() {
	if cwd, ok := transcript.ExtractLatestCwd(a.messages); ok {
		a.cwd = cwd
	}
}

func (a *Agent) appendStateMessageIfNeeded() {
	if cwd, ok := transcript.ExtractLatestCwd(a.messages); ok && cwd == a.cwd {
		return
	}
	a.messages = append(a.messages, Message{Role: "user", Content: transcript.FormatStateMessage(a.cwd)})
}

func (a *Agent) appendSuccessfulResponse(resp *Response) error {
	resp.Turn = turnPointer(resp.Turn)
	if resp.Turn == nil {
		return fmt.Errorf("query model: missing turn")
	}
	canonicalText, err := CanonicalTurnJSON(resp.Turn)
	if err != nil {
		return fmt.Errorf("canonicalize model turn: %w", err)
	}
	a.messages = append(a.messages, Message{Role: "assistant", Content: canonicalText})
	a.notify(EventResponse{Turn: resp.Turn, Usage: resp.Usage, Raw: resp.Raw})
	return nil
}

func (a *Agent) appendProtocolFailure(resp Response, addCorrection bool) {
	a.messages = append(a.messages, Message{Role: "assistant", Content: resp.Raw})
	a.notify(EventProtocolFailure{Reason: errorToReason(resp.ProtocolErr), Raw: resp.Raw})
	if addCorrection {
		a.messages = append(a.messages, Message{Role: "user", Content: protocolCorrectionMessage(resp.ProtocolErr)})
	}
}

// Compact rewrites the transcript by summarizing an older prefix and keeping a
// recent tail of user-authored turns intact.
func (a *Agent) Compact(ctx context.Context, compactor Compactor, opts CompactOptions) (CompactStats, error) {
	if compactor == nil {
		return CompactStats{}, fmt.Errorf("compact transcript: no compactor configured")
	}

	keepRecentTurns := opts.KeepRecentTurns
	if keepRecentTurns < 0 {
		return CompactStats{}, fmt.Errorf("compact transcript: invalid keep recent turns: %d", keepRecentTurns)
	}
	if keepRecentTurns == 0 {
		keepRecentTurns = 2
	}

	transcriptMessages := a.messages
	boundary, ok := transcript.FindCompactBoundary(transcriptMessages, keepRecentTurns)
	if !ok {
		return CompactStats{}, fmt.Errorf("compact transcript: not enough history to compact")
	}

	prefix := append([]Message{}, a.messages[:boundary]...)
	summary, err := compactor.Summarize(ctx, prefix)
	if err != nil {
		return CompactStats{}, fmt.Errorf("compact transcript: summarize prefix: %w", err)
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return CompactStats{}, fmt.Errorf("compact transcript: empty summary")
	}

	rewritten, stats, err := transcript.RewriteForCompaction(transcriptMessages, summary, opts.Instructions, keepRecentTurns)
	if err != nil {
		return CompactStats{}, fmt.Errorf("compact transcript: %w", err)
	}

	a.messages = rewritten
	a.restoreExecutionStateFromMessages()
	return CompactStats(stats), nil
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

	if resp.ProtocolErr != nil {
		a.appendProtocolFailure(resp, true)
		return StepResult{Response: resp, ParseErr: resp.ProtocolErr}, nil
	}

	if err := a.appendSuccessfulResponse(&resp); err != nil {
		return StepResult{}, err
	}
	a.notify(EventDebug{Message: fmt.Sprintf("parsed turn: %T", resp.Turn)})
	return StepResult{Response: resp, Turn: resp.Turn}, nil
}

// ExecuteTurn runs an act turn and appends the command result payload.
func (a *Agent) ExecuteTurn(ctx context.Context, act *ActTurn) (StepResult, error) {
	if len(act.Bash.Commands) == 0 {
		return StepResult{Turn: act}, fmt.Errorf("execute act turn: %w", ErrMissingCommand)
	}

	outputs, execCount := make([]string, 0, len(act.Bash.Commands)), 0
	var execErr error

	for _, action := range act.Bash.Commands {
		if setter, ok := a.executor.(ExecutorStateSetter); ok {
			setter.SetEnv(a.env)
		}

		execDir := a.cwd
		if action.Workdir != "" {
			if filepath.IsAbs(action.Workdir) {
				execDir = action.Workdir
			} else {
				execDir = filepath.Join(a.cwd, action.Workdir)
			}
		}
		a.notify(EventCommandStart{Command: action.Command, Dir: execDir})

		execResult, commandErr := a.executor.Execute(ctx, action.Command, execDir)
		execCount++
		if execResult.PostCwd != "" {
			a.cwd = execResult.PostCwd
		}
		// PostEnv is a full next-turn snapshot; nil means no new snapshot captured.
		if execResult.PostEnv != nil {
			a.env = execResult.PostEnv
		}
		a.notify(EventDebug{Message: fmt.Sprintf("cwd: %s", a.cwd)})
		payload := transcript.FormatCommandResult(transcript.CommandResult{
			Command:  execResult.Command,
			Stdout:   execResult.Stdout,
			Stderr:   execResult.Stderr,
			ExitCode: execResult.ExitCode,
			Feedback: execResult.Feedback,
		})
		if commandErr != nil {
			a.notify(EventDebug{Message: fmt.Sprintf("command error: %v", commandErr)})
		}
		a.notify(EventCommandDone{
			Command:  action.Command,
			Stdout:   execResult.Stdout,
			Stderr:   execResult.Stderr,
			ExitCode: execResult.ExitCode,
			Feedback: execResult.Feedback,
			Err:      commandErr,
		})

		a.messages = append(a.messages, Message{Role: "user", Content: payload})
		outputs = append(outputs, payload)
		if execErr = commandErr; execErr != nil {
			break
		}
	}

	a.appendStateMessageIfNeeded()
	return StepResult{Turn: act, Output: strings.Join(outputs, "\n\n"), ExecErr: execErr, ExecCount: execCount}, nil
}

// RequestStepLimitSummary asks the model for a final done summary after the
// caller decides the step budget is exhausted.
func (a *Agent) RequestStepLimitSummary(ctx context.Context) error {
	a.notify(EventDebug{Message: "step limit reached, requesting summary"})
	a.AppendUserMessage("Step limit reached. Respond with " + protocolDoneExample + " summarizing your progress.")

	resp, err := a.model.Query(ctx, a.messages)
	if err != nil {
		return fmt.Errorf("query model (final): %w", err)
	}
	if resp.ProtocolErr != nil {
		a.appendProtocolFailure(resp, false)
		a.notify(EventDebug{Message: fmt.Sprintf("final response not a valid turn: %v", resp.ProtocolErr)})
		return fmt.Errorf("query model (final): %w", resp.ProtocolErr)
	}
	resp.Turn = turnPointer(resp.Turn)
	if _, ok := resp.Turn.(*DoneTurn); !ok {
		return fmt.Errorf("query model (final): expected done turn, got %T", resp.Turn)
	}
	if err := a.appendSuccessfulResponse(&resp); err != nil {
		return fmt.Errorf("query model (final): %w", err)
	}
	a.notify(EventDebug{Message: fmt.Sprintf("final response turn: %T", resp.Turn)})
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

		switch turn := result.Turn.(type) {
		case *DoneTurn:
			return nil
		case *ClarifyTurn:
			return ErrClarificationNeeded
		case *ActTurn:
			execResult, err := a.ExecuteTurn(ctx, turn)
			if err != nil {
				return err
			}
			steps += execResult.ExecCount
			a.notify(EventDebug{Message: fmt.Sprintf("step %d/%d", steps, a.MaxSteps)})
			if a.MaxSteps > 0 && steps >= a.MaxSteps {
				return a.RequestStepLimitSummary(ctx)
			}
		}
	}
}
