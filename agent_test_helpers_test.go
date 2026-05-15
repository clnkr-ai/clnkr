package clnkr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr/internal/core/transcript"
)

type fakeModel struct {
	responses []Response
	calls     int
	got       [][]Message
}

func (m *fakeModel) Query(_ context.Context, messages []Message) (Response, error) {
	m.got = append(m.got, transcript.CloneMessages(messages))
	if m.calls >= len(m.responses) {
		return Response{}, fmt.Errorf("no more responses")
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

type mutatingModel struct{}

func (mutatingModel) Query(_ context.Context, messages []Message) (Response, error) {
	messages[0].BashToolCalls[0].Command, messages[0].ProviderReplay[0].JSON[0] = "mutated", '{'
	return Response{Turn: verifiedDone("done")}, nil
}

type fakeFinalModel struct {
	fakeModel
	finalCalled bool
	mutateFinal bool
}

func (m *fakeFinalModel) QueryFinal(ctx context.Context, messages []Message) (Response, error) {
	m.finalCalled = true
	if m.mutateFinal {
		messages[0].BashToolCalls[0].Command = "mutated"
	}
	return m.Query(ctx, messages)
}

type fakeCompactor struct {
	summary string
	err     error
	got     [][]Message
}

func (c *fakeCompactor) Summarize(_ context.Context, messages []Message) (string, error) {
	c.got = append(c.got, transcript.CloneMessages(messages))
	if c.err != nil {
		return "", c.err
	}
	return c.summary, nil
}

type fakeExecutor struct {
	results []CommandResult
	errs    []error
	calls   int
	gotCmds []string
	gotDirs []string
	gotEnv  []map[string]string
}

func (e *fakeExecutor) Execute(_ context.Context, command, dir string) (CommandResult, error) {
	e.gotCmds = append(e.gotCmds, command)
	e.gotDirs = append(e.gotDirs, dir)
	if e.calls >= len(e.results) {
		return CommandResult{}, fmt.Errorf("no more results")
	}
	result := e.results[e.calls]
	if result.Command == "" {
		result.Command = command
	}
	var err error
	if e.calls < len(e.errs) {
		err = e.errs[e.calls]
	}
	e.calls++
	return result, err
}

func (e *fakeExecutor) SetEnv(env map[string]string) {
	cp := make(map[string]string, len(env))
	for k, v := range env {
		cp[k] = v
	}
	e.gotEnv = append(e.gotEnv, cp)
}

type scriptedPolicy struct {
	decisions []ActDecision
	clarifies []string
	proposals []ActProposal
	questions []string
}

func (p *scriptedPolicy) DecideAct(_ context.Context, proposal ActProposal) (ActDecision, error) {
	p.proposals = append(p.proposals, proposal)
	if len(p.decisions) == 0 {
		return ActDecision{Kind: ActDecisionApprove}, nil
	}
	decision := p.decisions[0]
	p.decisions = p.decisions[1:]
	return decision, nil
}

func (p *scriptedPolicy) Clarify(_ context.Context, question string) (string, error) {
	p.questions = append(p.questions, question)
	if len(p.clarifies) == 0 {
		return "", ErrClarificationNeeded
	}
	reply := p.clarifies[0]
	p.clarifies = p.clarifies[1:]
	return reply, nil
}

func verifiedDone(summary string) *DoneTurn {
	return &DoneTurn{
		Summary: summary,
		Verification: CompletionVerification{
			Status: VerificationVerified,
			Checks: []VerificationCheck{{Command: "go test ./...", Outcome: "passed", Evidence: "tests passed and expected files exist"}},
		},
		KnownRisks: []string{},
	}
}

func collectEvents() (func(Event), *[]Event) {
	var events []Event
	return func(e Event) { events = append(events, e) }, &events
}

func hasEvent[T Event](events []Event) bool {
	for _, event := range events {
		if _, ok := event.(T); ok {
			return true
		}
	}
	return false
}

func firstEvent[T Event](events []Event) (T, bool) {
	for _, event := range events {
		if ev, ok := event.(T); ok {
			return ev, true
		}
	}
	var zero T
	return zero, false
}

func eventDecisions(events []Event) []string {
	var decisions []string
	for _, event := range events {
		if ev, ok := event.(EventCompletionGate); ok {
			decisions = append(decisions, ev.Decision)
		}
	}
	return decisions
}

func hasMessage(messages []Message, role, content string) bool {
	for _, msg := range messages {
		if msg.Role == role && msg.Content == content {
			return true
		}
	}
	return false
}

func messagesText(messages []Message) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(msg.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

func collectToolResults(messages []Message) []BashToolResult {
	var results []BashToolResult
	for _, msg := range messages {
		if msg.BashToolResult != nil {
			results = append(results, *msg.BashToolResult)
		}
	}
	return results
}

func payloadOutcome(t *testing.T, raw string) CommandOutcomeType {
	t.Helper()
	var payload struct {
		Outcome CommandOutcome `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("payload is not JSON: %v\n%s", err, raw)
	}
	return payload.Outcome.Type
}

func TestMessagesReturnsCallerOwnedCopy(t *testing.T) {
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	agent.messages = []Message{{
		Role:           "user",
		Content:        "original",
		BashToolCalls:  []BashToolCall{{ID: "call_1", Command: "pwd"}},
		BashToolResult: &BashToolResult{ID: "call_1", Content: "payload"},
		ProviderReplay: []ProviderReplayItem{{JSON: json.RawMessage(`["x"]`)}},
	}}

	msgs := agent.Messages()
	msgs[0].Content = "mutated"
	msgs[0].BashToolCalls[0].Command = "mutated"
	msgs[0].BashToolResult.Content = "mutated"
	msgs[0].ProviderReplay[0].JSON[0] = '{'

	got := agent.Messages()[0]
	if got.Content != "original" || got.BashToolCalls[0].Command != "pwd" || got.BashToolResult.Content != "payload" || string(got.ProviderReplay[0].JSON) != `["x"]` {
		t.Fatalf("stored message was mutated: %#v", got)
	}
}
