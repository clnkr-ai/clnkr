package clnkrapp

import (
	"context"
	"fmt"

	"github.com/clnkr-ai/clnkr"
)

func actJSON(command string) string {
	return fmt.Sprintf(`{"type":"act","bash":{"commands":[{"command":%q,"workdir":null}]}}`, command)
}

func mustTurn(raw string) clnkr.Turn {
	turn, err := clnkr.ParseTurn(raw)
	if err != nil {
		panic(err)
	}
	return turn
}

func mustResponse(raw string) clnkr.Response {
	return clnkr.Response{Turn: mustTurn(raw), Raw: raw}
}

func mustCanonicalTurn(t testingT, turn clnkr.Turn) string {
	t.Helper()

	text, err := clnkr.CanonicalTurnJSON(turn)
	if err != nil {
		t.Fatalf("CanonicalTurnJSON: %v", err)
	}
	return text
}

type testingT interface {
	Helper()
	Fatalf(string, ...any)
}

type fakeModel struct {
	responses []clnkr.Response
	calls     int
}

func (m *fakeModel) Query(_ context.Context, _ []clnkr.Message) (clnkr.Response, error) {
	if m.calls >= len(m.responses) {
		return clnkr.Response{}, fmt.Errorf("no more responses")
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

type fakeExecutor struct {
	results []clnkr.CommandResult
	errs    []error
	calls   int
	gotCmds []string
}

func (e *fakeExecutor) Execute(_ context.Context, command, _ string) (clnkr.CommandResult, error) {
	e.gotCmds = append(e.gotCmds, command)
	if e.calls >= len(e.results) {
		return clnkr.CommandResult{}, fmt.Errorf("no more results")
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

func (e *fakeExecutor) SetEnv(map[string]string) {}

type fakeCompactor struct {
	summary  string
	err      error
	calls    int
	messages []clnkr.Message
}

func (c *fakeCompactor) Summarize(_ context.Context, messages []clnkr.Message) (string, error) {
	c.calls++
	c.messages = append([]clnkr.Message{}, messages...)
	if c.err != nil {
		return "", c.err
	}
	return c.summary, nil
}

func compactableMessages() []clnkr.Message {
	return []clnkr.Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done"}`},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done again"}`},
		{Role: "user", Content: "third task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
	}
}

func hasUserMessage(msgs []clnkr.Message, want string) bool {
	for _, msg := range msgs {
		if msg.Role == "user" && msg.Content == want {
			return true
		}
	}
	return false
}
