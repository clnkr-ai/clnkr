package clnkrapp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/compaction"
)

// DriverEvent is a sealed interface for frontend-facing driver events.
type DriverEvent interface{ driverEvent() }

type EventApprovalRequest struct {
	Commands []clnkr.BashAction
	Prompt   string
}
type EventClarificationRequest struct{ Question string }
type EventDone struct{ Summary string }
type EventCompacted struct{ Stats clnkr.CompactStats }
type EventChildProbeStart struct{ Request ChildProbeRequest }
type EventChildProbeDone struct{ Result ChildProbeResult }
type EventChildProbeDenied struct {
	ChildID string
	Reason  string
}
type EventError struct{ Err error }

func (EventApprovalRequest) driverEvent()      {}
func (EventClarificationRequest) driverEvent() {}
func (EventDone) driverEvent()                 {}
func (EventCompacted) driverEvent()            {}
func (EventChildProbeStart) driverEvent()      {}
func (EventChildProbeDone) driverEvent()       {}
func (EventChildProbeDenied) driverEvent()     {}
func (EventError) driverEvent()                {}

const (
	PromptModeApproval = "approval"
	PromptModeFullSend = "full_send"
)

const (
	PendingNone          = ""
	PendingApproval      = "approval"
	PendingClarification = "clarification"
)

type Driver struct {
	agent            *clnkr.Agent
	compactorFactory compaction.Factory
	events           chan DriverEvent
	delegateRunner   ChildProbeRunner
	delegateConfig   DelegateConfig
	childCount       int

	mu      sync.Mutex
	running bool
	pending *pendingReply
}

type pendingReply struct {
	kind  string
	reply chan string
	done  chan struct{}
}

func NewDriver(agent *clnkr.Agent, compactorFactory compaction.Factory) *Driver {
	return &Driver{
		agent:            agent,
		compactorFactory: compactorFactory,
		events:           make(chan DriverEvent, 16),
	}
}

func (d *Driver) Events() <-chan DriverEvent {
	return d.events
}

func (d *Driver) SetDelegateRunner(runner ChildProbeRunner, cfg DelegateConfig) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.delegateRunner = runner
	d.delegateConfig = cfg
}

func (d *Driver) Prompt(ctx context.Context, text string, mode string) error {
	if err := d.setRunning(true); err != nil {
		_ = d.emit(ctx, EventError{Err: err})
		return err
	}
	defer d.setRunning(false) //nolint:errcheck

	delegated, err := d.handleDelegateCommand(ctx, text)
	if err != nil {
		_ = d.emit(ctx, EventError{Err: err})
		return err
	}
	if delegated {
		return nil
	}

	stats, compacted, err := HandleCompactCommand(ctx, d.agent, text, d.compactorFactory)
	if err != nil {
		_ = d.emit(ctx, EventError{Err: err})
		return err
	}
	if compacted {
		return d.emit(ctx, EventCompacted{Stats: stats})
	}

	var doneSummary string
	previousNotify := d.agent.Notify
	d.agent.Notify = func(event clnkr.Event) {
		if response, ok := event.(clnkr.EventResponse); ok {
			switch turn := response.Turn.(type) {
			case *clnkr.DoneTurn:
				doneSummary = turn.Summary
			}
		}
		if previousNotify != nil {
			previousNotify(event)
		}
	}
	defer func() {
		d.agent.Notify = previousNotify
	}()

	switch mode {
	case PromptModeFullSend:
		err = d.agent.Run(ctx, text)
	case PromptModeApproval, "":
		err = d.agent.RunWithPolicy(ctx, text, d)
	default:
		err = fmt.Errorf("unknown prompt mode %q", mode)
	}
	if err != nil {
		_ = d.emit(ctx, EventError{Err: err})
		return err
	}
	return d.emit(ctx, EventDone{Summary: doneSummary})
}

func (d *Driver) handleDelegateCommand(ctx context.Context, text string) (bool, error) {
	task, ok := ParseDelegateCommand(text)
	if !ok {
		return false, nil
	}

	d.mu.Lock()
	runner := d.delegateRunner
	cfg := d.delegateConfig
	childCount := d.childCount
	d.mu.Unlock()

	if runner == nil || !cfg.Enabled {
		_ = d.emit(ctx, EventChildProbeDenied{Reason: "delegation is not enabled"})
		return true, fmt.Errorf("delegate command: delegation is not enabled")
	}
	req, err := PrepareChildProbeRequest(d.agent.Cwd(), task, cfg, childCount)
	if err != nil {
		_ = d.emit(ctx, EventChildProbeDenied{Reason: err.Error()})
		return true, err
	}
	if err := d.emit(ctx, EventChildProbeStart{Request: req}); err != nil {
		return true, err
	}

	result, runErr := runner.RunChildProbe(ctx, req)
	if result.ChildID == "" {
		result.ChildID = req.ChildID
	}
	if result.Artifacts.Input == "" && result.Artifacts.EventLog == "" {
		result.Artifacts.Input = req.ArtifactDir
	}
	if runErr != nil && result.Status == "" {
		result = ChildProbeResult{
			ChildID:      req.ChildID,
			Status:       ChildProbeStatusFailed,
			Summary:      "child probe failed",
			ErrorMessage: runErr.Error(),
		}
	}
	block, err := FormatChildProbeTranscriptBlock(result)
	if err != nil {
		return true, err
	}
	d.agent.AppendUserMessage(block)

	d.mu.Lock()
	d.childCount++
	d.mu.Unlock()

	if err := d.emit(ctx, EventChildProbeDone{Result: result}); err != nil {
		return true, err
	}
	if runErr != nil {
		return true, fmt.Errorf("delegate command: %w", runErr)
	}
	return true, nil
}

func (d *Driver) Reply(ctx context.Context, text string) error {
	d.mu.Lock()
	pending := d.pending
	d.mu.Unlock()

	if pending == nil {
		return fmt.Errorf("reply: no pending request")
	}
	select {
	case pending.reply <- text:
	case <-pending.done:
		return fmt.Errorf("reply: no pending request")
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-pending.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Driver) Pending() string {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.pending == nil {
		return PendingNone
	}
	return d.pending.kind
}

func (d *Driver) DecideAct(ctx context.Context, proposal clnkr.ActProposal) (clnkr.ActDecision, error) {
	reply, err := d.waitForReply(ctx, PendingApproval, EventApprovalRequest{
		Commands: proposal.Commands,
		Prompt:   proposal.Prompt,
	})
	if err != nil {
		return clnkr.ActDecision{}, err
	}
	if strings.TrimSpace(reply) != "y" {
		return clnkr.ActDecision{Kind: clnkr.ActDecisionReject, Guidance: reply}, nil
	}
	return clnkr.ActDecision{Kind: clnkr.ActDecisionApprove}, nil
}

func (d *Driver) Clarify(ctx context.Context, question string) (string, error) {
	return d.waitForReply(ctx, PendingClarification, EventClarificationRequest{Question: question})
}

func (d *Driver) setRunning(running bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if running && d.running {
		return fmt.Errorf("driver run already in progress")
	}
	d.running = running
	return nil
}

func (d *Driver) waitForReply(ctx context.Context, kind string, event DriverEvent) (string, error) {
	for {
		reply, err := d.requestReply(ctx, kind, event)
		if err != nil {
			return "", err
		}
		reply = strings.TrimSpace(reply)
		if reply == "" {
			continue
		}
		if err := RejectCompactCommand(reply); err != nil {
			if emitErr := d.emit(ctx, EventError{Err: err}); emitErr != nil {
				return "", emitErr
			}
			continue
		}
		return reply, nil
	}
}

func (d *Driver) requestReply(ctx context.Context, kind string, event DriverEvent) (string, error) {
	pending := &pendingReply{
		kind:  kind,
		reply: make(chan string),
		done:  make(chan struct{}),
	}
	if err := d.setPending(pending); err != nil {
		return "", err
	}
	defer d.clearPending(pending)

	if err := d.emit(ctx, event); err != nil {
		return "", err
	}
	select {
	case reply := <-pending.reply:
		return reply, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (d *Driver) setPending(pending *pendingReply) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.pending != nil {
		return fmt.Errorf("driver pending request already exists")
	}
	d.pending = pending
	return nil
}

func (d *Driver) clearPending(pending *pendingReply) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.pending == pending {
		d.pending = nil
		close(pending.done)
	}
}

func (d *Driver) emit(ctx context.Context, event DriverEvent) error {
	select {
	case d.events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
