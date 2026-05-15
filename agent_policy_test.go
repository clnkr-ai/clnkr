package clnkr

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunWithPolicyCompletesDeniedToolResults(t *testing.T) {
	model := &fakeModel{responses: []Response{
		{Turn: &ActTurn{Bash: BashBatch{Commands: []BashAction{
			{ID: "call_1", Command: "rm important.txt"},
			{ID: "call_2", Command: "git status"},
		}}}},
		{Turn: verifiedDone("finished")},
	}}
	policy := &scriptedPolicy{decisions: []ActDecision{{Kind: ActDecisionReject, Guidance: "list files instead"}}}
	agent := NewAgent(model, &fakeExecutor{}, "/tmp")

	if err := agent.RunWithPolicy(context.Background(), "do it", policy); err != nil {
		t.Fatalf("RunWithPolicy: %v", err)
	}
	results := collectToolResults(agent.Messages())
	if len(results) != 2 || results[0].ID != "call_1" || results[1].ID != "call_2" {
		t.Fatalf("tool results = %#v", results)
	}
	for _, result := range results {
		if !result.IsError || payloadOutcome(t, result.Content) != CommandOutcomeDenied {
			t.Fatalf("denied result = %#v", result)
		}
	}
}

func TestRunWithPolicyStopsCleanlyOnPolicyErrors(t *testing.T) {
	errApprovalPending := errors.New("approval pending")
	model := &fakeModel{responses: []Response{{Turn: &ActTurn{Bash: BashBatch{Commands: []BashAction{{Command: "rm important.txt"}}}}}}}
	executor := &fakeExecutor{}
	policy := runPolicyFunc{
		decideAct: func(context.Context, ActProposal) (ActDecision, error) {
			return ActDecision{}, errApprovalPending
		},
		clarify: func(context.Context, string) (string, error) { return "", ErrClarificationNeeded },
	}
	agent := NewAgent(model, executor, "/tmp")

	err := agent.RunWithPolicy(context.Background(), "do it", policy)
	if !errors.Is(err, errApprovalPending) {
		t.Fatalf("RunWithPolicy error = %v", err)
	}
	if executor.calls != 0 || hasMessage(agent.Messages(), "user", "") {
		t.Fatalf("executor calls=%d messages=%#v", executor.calls, agent.Messages())
	}
}

func TestRunCommandBudgetIsLocalToEachRun(t *testing.T) {
	model := &fakeModel{responses: []Response{
		{Turn: &ActTurn{Bash: BashBatch{Commands: []BashAction{{Command: "echo one"}}}}},
		{Turn: verifiedDone("first")},
		{Turn: &ActTurn{Bash: BashBatch{Commands: []BashAction{{Command: "echo two"}}}}},
		{Turn: verifiedDone("second")},
	}}
	executor := &fakeExecutor{results: []CommandResult{{Stdout: "one\n", ExitCode: 0}, {Stdout: "two\n", ExitCode: 0}}}
	agent := NewAgent(model, executor, "/tmp")
	agent.MaxSteps = 1

	if err := agent.Run(context.Background(), "first"); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if err := agent.Run(context.Background(), "second"); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if got := strings.Join(executor.gotCmds, ","); got != "echo one,echo two" {
		t.Fatalf("commands = %q", got)
	}
}

type runPolicyFunc struct {
	decideAct func(context.Context, ActProposal) (ActDecision, error)
	clarify   func(context.Context, string) (string, error)
}

func (p runPolicyFunc) DecideAct(ctx context.Context, proposal ActProposal) (ActDecision, error) {
	return p.decideAct(ctx, proposal)
}

func (p runPolicyFunc) Clarify(ctx context.Context, question string) (string, error) {
	return p.clarify(ctx, question)
}
