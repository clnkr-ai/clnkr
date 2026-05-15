package clnkr

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/clnkr-ai/clnkr/internal/core/transcript"
)

const DefaultMaxSteps = 100

var ErrClarificationNeeded = errors.New("clarification needed")

// Agent coordinates model turns and command execution.
type Agent struct {
	model       Model
	executor    Executor
	messages    []Message
	cwd         string
	env         map[string]string
	started     bool
	Notify      func(Event)
	MaxSteps    int
	ActProtocol ActProtocol
}

func NewAgent(model Model, executor Executor, cwd string) *Agent {
	return &Agent{model: model, executor: executor, cwd: cwd, MaxSteps: DefaultMaxSteps}
}

// SetEnv replaces the shell environment snapshot used for future commands.
func (a *Agent) SetEnv(env map[string]string) {
	if env == nil {
		a.env = nil
		return
	}
	a.env = make(map[string]string, len(env))
	for k, v := range env {
		a.env[k] = v
	}
}

func cloneProviderReplay(items []ProviderReplayItem) []ProviderReplayItem {
	if len(items) == 0 {
		return nil
	}
	return transcript.CloneMessage(Message{ProviderReplay: items}).ProviderReplay
}

func (a *Agent) notify(e Event) {
	if a.Notify != nil {
		a.Notify(e)
	}
}

func (a *Agent) Messages() []Message {
	return transcript.CloneMessages(a.messages)
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
	copy(combined, transcript.CloneMessages(msgs))
	copy(combined[len(msgs):], transcript.CloneMessages(a.messages))
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

func (a *Agent) appendResourceStateMessage(commandsUsed, modelTurnsUsed int) {
	content := fmt.Sprintf(`{"type":"resource_state","source":"clnkr","commands_used":%d,"model_turns_used":%d}`,
		commandsUsed, modelTurnsUsed)
	if a.MaxSteps > 0 {
		content = fmt.Sprintf(`{"type":"resource_state","source":"clnkr","commands_used":%d,"commands_remaining":%d,"max_commands":%d,"model_turns_used":%d}`,
			commandsUsed, max(a.MaxSteps-commandsUsed, 0), a.MaxSteps, modelTurnsUsed)
	}
	if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "user" && a.messages[len(a.messages)-1].Content == content {
		return
	}
	a.messages = append(a.messages, Message{Role: "user", Content: content})
}

func (a *Agent) notifyRunError(err error, commandsUsed, modelTurns int) error {
	a.notify(EventDebug{Message: fmt.Sprintf(
		"run error: model_turns=%d commands=%d messages=%d cwd=%s last_error=%v",
		modelTurns, commandsUsed, len(a.messages), a.cwd, err,
	)})
	return err
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
	if len(resp.BashToolCalls) > 0 || len(resp.ProviderReplay) > 0 {
		msg := &a.messages[len(a.messages)-1]
		msg.BashToolCalls = append([]BashToolCall(nil), resp.BashToolCalls...)
		msg.ProviderReplay = cloneProviderReplay(resp.ProviderReplay)
	}
	a.notify(EventResponse{Turn: resp.Turn, Usage: resp.Usage, Raw: resp.Raw})
	return nil
}

func (a *Agent) appendProtocolFailure(resp Response, appendCorrection bool) {
	a.messages = append(a.messages, Message{Role: "assistant", Content: resp.Raw})
	a.notify(EventProtocolFailure{Reason: errorToReason(resp.ProtocolErr), Raw: resp.Raw})
	if appendCorrection {
		a.messages = append(a.messages, Message{Role: "user", Content: protocolCorrectionMessageFor(resp.ProtocolErr, a.ActProtocol)})
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

	transcriptMessages := transcript.CloneMessages(a.messages)
	boundary, ok := transcript.FindCompactBoundary(transcriptMessages, keepRecentTurns)
	if !ok {
		return CompactStats{}, fmt.Errorf("compact transcript: not enough history to compact")
	}

	prefix := transcript.CloneMessages(a.messages[:boundary])
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
	resp, err := a.model.Query(ctx, transcript.CloneMessages(a.messages))
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
	return a.executeTurn(ctx, act, nil)
}

// ExecuteTurnWithSkipped runs an act turn and records bash tool calls that
// were skipped before execution, for example by MaxSteps truncation.
func (a *Agent) ExecuteTurnWithSkipped(ctx context.Context, act *ActTurn, skipped []BashAction) (StepResult, error) {
	return a.executeTurn(ctx, act, skipped)
}

func (a *Agent) executeTurn(ctx context.Context, act *ActTurn, skipped []BashAction) (StepResult, error) {
	return actTurnExecutor{agent: a}.Execute(ctx, act, skipped)
}

// RejectTurn records that an approval-mode act turn was not executed.
func (a *Agent) RejectTurn(act *ActTurn, reply string) {
	if act == nil || len(act.Bash.Commands) == 0 {
		a.AppendUserMessage(reply)
		return
	}
	toolCallResults := 0
	for _, action := range act.Bash.Commands {
		if action.ID == "" {
			continue
		}
		content := transcript.FormatDeniedCommandResult(reply)
		a.messages = append(a.messages, Message{
			Role:           "user",
			Content:        content,
			BashToolResult: &BashToolResult{ID: action.ID, Content: content, IsError: true},
		})
		toolCallResults++
	}
	if toolCallResults == 0 {
		a.AppendUserMessage(reply)
	}
}

func (a *Agent) appendSkippedToolResult(action BashAction, reason string) (string, bool) {
	if action.ID == "" {
		return "", false
	}
	content := transcript.FormatSkippedCommandResult(reason)
	a.messages = append(a.messages, Message{
		Role:           "user",
		Content:        content,
		BashToolResult: &BashToolResult{ID: action.ID, Content: content, IsError: true},
	})
	return content, true
}

func commandResultIsError(result CommandResult) bool {
	if result.Outcome.Type == "" || result.Outcome.Type == CommandOutcomeExit && result.Outcome.ExitCode == nil {
		return result.ExitCode != 0
	}
	if result.Outcome.Type != CommandOutcomeExit {
		return true
	}
	return *result.Outcome.ExitCode != 0
}

// RequestStepLimitSummary asks the model for a final done summary after the
// caller decides the step budget is exhausted.
func (a *Agent) RequestStepLimitSummary(ctx context.Context) error {
	a.notify(EventDebug{Message: "step limit reached, requesting summary"})
	a.AppendUserMessage("Step limit reached. Respond with " + protocolDoneExample + " summarizing your progress.")

	queryMessages := transcript.CloneMessages(a.messages)
	query := a.model.Query
	if finalModel, ok := a.model.(FinalSummaryModel); ok {
		query = finalModel.QueryFinal
	}
	resp, err := query(ctx, queryMessages)
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

func (a *Agent) Run(ctx context.Context, task string) error {
	return a.RunWithPolicy(ctx, task, FullSendPolicy{})
}

// RunWithPolicy loops Step until done, clarify, or step limit. The policy
// decides whether act turns execute and supplies clarification replies.
func (a *Agent) RunWithPolicy(ctx context.Context, task string, policy RunPolicy) error {
	run := newRunPolicyState(policy)
	a.AppendUserMessage(task)

	for ctx.Err() == nil {
		done, err := run.step(ctx, a)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
	return run.runError(a, ctx.Err())
}

func cloneBashActions(actions []BashAction) []BashAction {
	if len(actions) == 0 {
		return nil
	}
	return append([]BashAction(nil), actions...)
}

func formatActProposal(commands []BashAction) string {
	var b strings.Builder
	for i, action := range commands {
		if i > 0 {
			b.WriteByte('\n')
		}
		command := action.Command
		if workdir := strings.TrimSpace(action.Workdir); workdir != "" {
			command = fmt.Sprintf("%s in %s", command, workdir)
		}
		fmt.Fprintf(&b, "%d. %s", i+1, command)
	}
	return b.String()
}
