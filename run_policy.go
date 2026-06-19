package clnkr

import (
	"context"
	"fmt"
)

type runPolicyState struct {
	policy            RunPolicy
	commandsUsed      int
	modelTurns        int
	protocolErrors    int
	completionGate    CompletionGate
	completionRejects int
	gateCompletions   bool
	guidancePressure  resourcePressure
}

func newRunPolicyState(policy RunPolicy) runPolicyState {
	if policy == nil {
		policy = FullSendPolicy{}
	}
	return runPolicyState{
		policy:          policy,
		gateCompletions: policyUsesCompletionGate(policy),
	}
}

func (s *runPolicyState) step(ctx context.Context, a *Agent) (bool, error) {
	a.appendStateMessageIfNeeded()
	a.appendResourceStateMessage(
		s.commandsUsed,
		s.modelTurns,
		s.resourcePressureGuidanceDue(a),
	)
	result, err := a.Step(ctx)
	s.modelTurns++
	if err != nil {
		return false, s.runError(a, err)
	}
	if result.ParseErr != nil {
		return s.handleProtocolFailure(a)
	}
	s.protocolErrors = 0

	switch turn := result.Turn.(type) {
	case *DoneTurn:
		return s.handleDone(a, turn)
	case *ClarifyTurn:
		return false, s.handleClarify(ctx, a, turn)
	case *ActTurn:
		return s.handleAct(ctx, a, turn)
	default:
		return false, s.runError(a, fmt.Errorf("unhandled turn type %T", turn))
	}
}

func (s *runPolicyState) handleProtocolFailure(a *Agent) (bool, error) {
	s.protocolErrors++
	a.notify(EventDebug{Message: fmt.Sprintf("consecutive protocol errors: %d", s.protocolErrors)})
	if s.protocolErrors >= 3 {
		return false, s.runError(a, fmt.Errorf("consecutive protocol failures, exiting"))
	}
	return false, nil
}

func (s *runPolicyState) handleDone(a *Agent, turn *DoneTurn) (bool, error) {
	if !s.gateCompletions {
		return true, nil
	}

	decision, reasons, guidance := s.completionGate.Decide(turn, s.commandsUsed, a.MaxSteps)
	a.notify(EventCompletionGate{
		Decision: decision,
		Reasons:  cloneStrings(reasons),
		Summary:  turn.Summary,
	})
	if decision == CompletionAccept {
		return true, nil
	}
	if decision == CompletionReject {
		s.completionRejects++
		if s.completionRejects >= 3 {
			return false, s.runError(
				a,
				fmt.Errorf("consecutive completion gate rejections, exiting"),
			)
		}
	}
	if decision == CompletionChallenge {
		s.completionRejects = 0
	}
	a.AppendUserMessage(guidance)
	return false, nil
}

func (s *runPolicyState) handleClarify(ctx context.Context, a *Agent, turn *ClarifyTurn) error {
	reply, err := s.policy.Clarify(ctx, turn.Question)
	if err != nil {
		return s.runError(a, fmt.Errorf("clarify: %w", err))
	}
	s.completionRejects = 0
	a.AppendUserMessage(reply)
	return nil
}

func (s *runPolicyState) handleAct(ctx context.Context, a *Agent, turn *ActTurn) (bool, error) {
	limited, skipped := s.limitActTurn(a, turn)
	commands := cloneBashActions(limited.Bash.Commands)
	decision, err := s.policy.DecideAct(ctx, ActProposal{
		Turn: &ActTurn{
			Bash:      BashBatch{Commands: cloneBashActions(commands)},
			Reasoning: limited.Reasoning,
		},
		Skipped:  cloneBashActions(skipped),
		Commands: commands,
		Prompt:   formatActProposal(commands),
	})
	if err != nil {
		return false, s.runError(a, fmt.Errorf("decide act: %w", err))
	}
	switch decision.Kind {
	case ActDecisionReject:
		allCommands := append(cloneBashActions(limited.Bash.Commands), skipped...)
		a.RejectTurn(
			&ActTurn{Bash: BashBatch{Commands: allCommands}, Reasoning: limited.Reasoning},
			decision.Guidance,
		)
		return false, nil
	case ActDecisionApprove:
	default:
		return false, s.runError(a, fmt.Errorf("decide act: unknown decision %q", decision.Kind))
	}

	execResult, err := a.ExecuteTurnWithSkipped(ctx, limited, skipped)
	if err != nil {
		return false, s.runError(a, err)
	}
	s.completionRejects = 0
	s.commandsUsed += execResult.ExecCount
	a.notify(EventDebug{Message: fmt.Sprintf("step %d/%d", s.commandsUsed, a.MaxSteps)})
	if a.MaxSteps > 0 && s.commandsUsed >= a.MaxSteps {
		return s.requestStepLimitSummary(ctx, a)
	}
	return false, nil
}

func (s *runPolicyState) limitActTurn(a *Agent, turn *ActTurn) (*ActTurn, []BashAction) {
	if remaining := a.MaxSteps - s.commandsUsed; a.MaxSteps > 0 &&
		len(turn.Bash.Commands) > remaining {
		skipped := append([]BashAction(nil), turn.Bash.Commands[remaining:]...)
		limited := &ActTurn{
			Bash:      BashBatch{Commands: turn.Bash.Commands[:remaining]},
			Reasoning: turn.Reasoning,
		}
		return limited, skipped
	}
	return turn, nil
}

func (s *runPolicyState) requestStepLimitSummary(ctx context.Context, a *Agent) (bool, error) {
	a.appendResourceStateMessage(
		s.commandsUsed,
		s.modelTurns,
		s.resourcePressureGuidanceDue(a),
	)
	if err := a.RequestStepLimitSummary(ctx); err != nil {
		s.modelTurns++
		return false, s.runError(a, err)
	}
	s.modelTurns++
	return true, nil
}

func (s *runPolicyState) resourcePressureGuidanceDue(a *Agent) bool {
	pressure := commandBudgetPressure(s.commandsUsed, a.MaxSteps)
	if pressure == resourcePressureNormal || pressure == s.guidancePressure {
		return false
	}
	s.guidancePressure = pressure
	return true
}

func (s *runPolicyState) runError(a *Agent, err error) error {
	return a.notifyRunError(err, s.commandsUsed, s.modelTurns)
}

func policyUsesCompletionGate(policy RunPolicy) bool {
	switch policy.(type) {
	case FullSendPolicy, *FullSendPolicy:
		return true
	default:
		return false
	}
}
