package clnkr

import (
	"context"
	"strings"
	"testing"
)

func TestRunWithPolicyCompletionGateEvents(t *testing.T) {
	weakDone := &DoneTurn{
		Summary: "Protocol correction: need to continue.",
		Verification: CompletionVerification{Status: VerificationVerified, Checks: []VerificationCheck{{
			Command: "true", Outcome: "passed", Evidence: "true exited zero",
		}}},
		KnownRisks: []string{},
	}
	fullSend := &fakeModel{responses: []Response{{Turn: weakDone}, {Turn: verifiedDone("Finished.")}}}
	fullSendAgent := NewAgent(fullSend, &fakeExecutor{}, "/tmp")
	notify, events := collectEvents()
	fullSendAgent.Notify = notify
	if err := fullSendAgent.Run(context.Background(), "finish"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	rejectTurn := messagesText(fullSend.got[1])
	if fullSend.calls != 2 ||
		!strings.Contains(rejectTurn, "Completion rejected") ||
		!strings.Contains(rejectTurn, "incomplete_summary") {
		t.Fatalf("full-send queries = %#v", fullSend.got)
	}
	if got := strings.Join(eventDecisions(*events), ","); got != "reject,accept" {
		t.Fatalf("completion gate decisions = %q", got)
	}

	challenge := &DoneTurn{
		Summary: "Created result.txt.",
		Verification: CompletionVerification{Status: VerificationVerified, Checks: []VerificationCheck{{
			Command: "true", Outcome: "passed", Evidence: "ok",
		}}},
		KnownRisks: []string{},
	}
	challenged := &fakeModel{responses: []Response{{Turn: challenge}, {Turn: challenge}}}
	challengedAgent := NewAgent(challenged, &fakeExecutor{}, "/tmp")
	notify, events = collectEvents()
	challengedAgent.Notify = notify
	if err := challengedAgent.Run(context.Background(), "finish"); err != nil {
		t.Fatalf("challenge Run: %v", err)
	}
	challengeTurn := messagesText(challenged.got[1])
	if challenged.calls != 2 ||
		!strings.Contains(challengeTurn, "Completion challenged") ||
		!strings.Contains(challengeTurn, "verification evidence") {
		t.Fatalf("challenge queries = %#v", challenged.got)
	}
	if got := strings.Join(eventDecisions(*events), ","); got != "challenge,accept" {
		t.Fatalf("completion gate decisions = %q", got)
	}

	approval := &fakeModel{responses: []Response{{Turn: weakDone}}}
	approvalAgent := NewAgent(approval, &fakeExecutor{}, "/tmp")
	notify, events = collectEvents()
	approvalAgent.Notify = notify
	if err := approvalAgent.RunWithPolicy(context.Background(), "finish", &scriptedPolicy{}); err != nil {
		t.Fatalf("RunWithPolicy: %v", err)
	}
	if approval.calls != 1 || len(eventDecisions(*events)) != 0 {
		t.Fatalf("approval calls=%d events=%#v", approval.calls, *events)
	}
}
