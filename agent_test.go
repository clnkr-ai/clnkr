package clnkr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

type fakeFinalModel struct {
	fakeModel
	finalCalled bool
	mutateFinal bool
}

func (m *fakeFinalModel) QueryFinal(_ context.Context, messages []Message) (Response, error) {
	m.finalCalled = true
	m.got = append(m.got, transcript.CloneMessages(messages))
	if m.mutateFinal && len(messages) > 0 && len(messages[0].BashToolCalls) > 0 {
		messages[0].BashToolCalls[0].Command = "mutated"
	}
	return Response{Turn: verifiedDone("final")}, nil
}

type mutatingModel struct{}

func (m mutatingModel) Query(_ context.Context, messages []Message) (Response, error) {
	if len(messages) > 0 && len(messages[0].BashToolCalls) > 0 {
		messages[0].BashToolCalls[0].Command = "mutated"
	}
	if len(messages) > 0 && len(messages[0].ProviderReplay) > 0 && len(messages[0].ProviderReplay[0].JSON) > 0 {
		messages[0].ProviderReplay[0].JSON[0] = '{'
	}
	return Response{Turn: verifiedDone("done")}, nil
}

type errorModel struct {
	err error
}

func (m errorModel) Query(context.Context, []Message) (Response, error) {
	return Response{}, m.err
}

type fakeExecutor struct {
	results []CommandResult
	errs    []error
	calls   int
	gotCmds []string
	gotDirs []string
	gotEnv  []map[string]string
}

type fakeCompactor struct {
	summary string
	err     error
	got     [][]Message
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

func (c *fakeCompactor) Summarize(_ context.Context, messages []Message) (string, error) {
	c.got = append(c.got, transcript.CloneMessages(messages))
	if c.err != nil {
		return "", c.err
	}
	return c.summary, nil
}

func (e *fakeExecutor) Execute(_ context.Context, command string, dir string) (CommandResult, error) {
	e.gotCmds = append(e.gotCmds, command)
	e.gotDirs = append(e.gotDirs, dir)
	if e.calls >= len(e.results) {
		return CommandResult{}, fmt.Errorf("no more outputs")
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

func mustCommandPayload(t *testing.T, result CommandResult) string {
	t.Helper()
	return transcript.FormatCommandResult(transcript.CommandResult{
		Command:  result.Command,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
		Feedback: result.Feedback,
	})
}

func mustShellResultPayload(t *testing.T, raw string) struct {
	Stdout  string `json:"stdout"`
	Stderr  string `json:"stderr"`
	Outcome struct {
		Type     string `json:"type"`
		ExitCode int    `json:"exit_code,omitempty"`
		Reason   string `json:"reason,omitempty"`
	} `json:"outcome"`
} {
	t.Helper()
	var payload struct {
		Stdout  string `json:"stdout"`
		Stderr  string `json:"stderr"`
		Outcome struct {
			Type     string `json:"type"`
			ExitCode int    `json:"exit_code,omitempty"`
			Reason   string `json:"reason,omitempty"`
		} `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("payload is not JSON: %v\n%s", err, raw)
	}
	return payload
}

func mustFindCommandPayload(t *testing.T, messages []Message) struct {
	Stdout  string `json:"stdout"`
	Stderr  string `json:"stderr"`
	Outcome struct {
		Type     string `json:"type"`
		ExitCode int    `json:"exit_code,omitempty"`
		Reason   string `json:"reason,omitempty"`
	} `json:"outcome"`
} {
	t.Helper()
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		var payload struct {
			Stdout  string `json:"stdout"`
			Stderr  string `json:"stderr"`
			Outcome struct {
				Type     string `json:"type"`
				ExitCode int    `json:"exit_code,omitempty"`
				Reason   string `json:"reason,omitempty"`
			} `json:"outcome"`
		}
		if err := json.Unmarshal([]byte(msg.Content), &payload); err == nil && payload.Outcome.Type != "" {
			return payload
		}
	}
	t.Fatalf("no command payload in messages: %#v", messages)
	return struct {
		Stdout  string `json:"stdout"`
		Stderr  string `json:"stderr"`
		Outcome struct {
			Type     string `json:"type"`
			ExitCode int    `json:"exit_code,omitempty"`
			Reason   string `json:"reason,omitempty"`
		} `json:"outcome"`
	}{}
}

func mustStepAct(t *testing.T, agent *Agent) *ActTurn {
	t.Helper()
	result, err := agent.Step(context.Background())
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	act, ok := result.Turn.(*ActTurn)
	if !ok {
		t.Fatalf("expected *ActTurn, got %T", result.Turn)
	}
	return act
}

func runActStep(t *testing.T, agent *Agent) StepResult {
	t.Helper()
	act := mustStepAct(t, agent)
	result, err := agent.ExecuteTurn(context.Background(), act)
	if err != nil {
		t.Fatalf("ExecuteTurn: %v", err)
	}
	return result
}

func bashBatch(commands ...BashAction) BashBatch {
	return BashBatch{Commands: commands}
}

func actJSON(commands ...string) string {
	items := make([]string, 0, len(commands))
	for _, command := range commands {
		items = append(items, fmt.Sprintf(`{"command":%q,"workdir":null}`, command))
	}
	return fmt.Sprintf(`{"type":"act","bash":{"commands":[%s]}}`, strings.Join(items, ","))
}

func actJSONWithReasoning(reasoning string, commands ...string) string {
	items := make([]string, 0, len(commands))
	for _, command := range commands {
		items = append(items, fmt.Sprintf(`{"command":%q,"workdir":null}`, command))
	}
	return fmt.Sprintf(`{"type":"act","bash":{"commands":[%s]},"reasoning":%q}`, strings.Join(items, ","), reasoning)
}

func mustTurn(raw string) Turn {
	if turn, err := ParseTurn(raw); err == nil {
		return turn
	}
	var env struct {
		Type string `json:"type"`
		Bash struct {
			Commands []struct {
				Command string  `json:"command"`
				Workdir *string `json:"workdir"`
			} `json:"commands"`
		} `json:"bash"`
		Question  string `json:"question"`
		Summary   string `json:"summary"`
		Reasoning string `json:"reasoning"`
	}
	err := json.Unmarshal([]byte(raw), &env)
	if err != nil {
		panic(err)
	}
	switch env.Type {
	case "act":
		commands := make([]BashAction, 0, len(env.Bash.Commands))
		for _, command := range env.Bash.Commands {
			action := BashAction{Command: command.Command}
			if command.Workdir != nil {
				action.Workdir = *command.Workdir
			}
			commands = append(commands, action)
		}
		return &ActTurn{Bash: BashBatch{Commands: commands}, Reasoning: env.Reasoning}
	case "clarify":
		return &ClarifyTurn{Question: env.Question, Reasoning: env.Reasoning}
	case "done":
		done := verifiedDone(env.Summary)
		done.Reasoning = env.Reasoning
		return done
	default:
		panic(fmt.Sprintf("unsupported test turn type %q", env.Type))
	}
}

func verifiedDone(summary string) *DoneTurn {
	return &DoneTurn{
		Summary: summary,
		Verification: CompletionVerification{
			Status: VerificationVerified,
			Checks: []VerificationCheck{{
				Command:  "go test ./...",
				Outcome:  "passed",
				Evidence: "go test ./... passed and ls output showed current directory entries for completion",
			}},
		},
		KnownRisks: []string{},
	}
}

func mustResponse(raw string) Response {
	return Response{Turn: mustTurn(raw), Raw: raw}
}

func mustResponseWithUsage(raw string, usage Usage) Response {
	return Response{Turn: mustTurn(raw), Raw: raw, Usage: usage}
}

// collectEvents returns a Notify function and a pointer to the collected events slice.
func collectEvents() (func(Event), *[]Event) {
	var events []Event
	return func(e Event) { events = append(events, e) }, &events
}

func completionGateDecisions(events []Event) []string {
	var decisions []string
	for _, event := range events {
		if ev, ok := event.(EventCompletionGate); ok {
			decisions = append(decisions, string(ev.Decision))
		}
	}
	return decisions
}

func TestStep(t *testing.T) {
	t.Run("returns act turn without executing command", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("ls")),
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "file1.go\n", ExitCode: 0}}}

		agent := NewAgent(model, executor, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "list files"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		act, ok := result.Turn.(*ActTurn)
		if !ok {
			t.Fatalf("expected *ActTurn, got %T", result.Turn)
		}
		if got := len(act.Bash.Commands); got != 1 {
			t.Fatalf("len(commands) = %d, want 1", got)
		}
		if act.Bash.Commands[0].Command != "ls" {
			t.Errorf("got command %q, want %q", act.Bash.Commands[0].Command, "ls")
		}
		if executor.calls != 0 {
			t.Fatalf("Step should not execute commands, got %d calls", executor.calls)
		}
		if result.Output != "" {
			t.Errorf("expected empty output for Step act turn, got %q", result.Output)
		}
		if result.ExecErr != nil {
			t.Errorf("expected empty exec error for Step act turn, got %v", result.ExecErr)
		}
	})

	t.Run("returns done turn", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(`{"type":"done","summary":"Listed all files."}`),
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "done"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		done, ok := result.Turn.(*DoneTurn)
		if !ok {
			t.Fatalf("expected *DoneTurn, got %T", result.Turn)
		}
		if done.Summary != "Listed all files." {
			t.Errorf("got summary %q, want %q", done.Summary, "Listed all files.")
		}
		if result.Output != "" {
			t.Errorf("expected empty output for done, got %q", result.Output)
		}
	})

	t.Run("returns clarify turn", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(`{"type":"clarify","question":"Which directory should I inspect?"}`),
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "do something"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cl, ok := result.Turn.(*ClarifyTurn)
		if !ok {
			t.Fatalf("expected *ClarifyTurn, got %T", result.Turn)
		}
		if cl.Question != "Which directory should I inspect?" {
			t.Errorf("got question %q", cl.Question)
		}
	})

	t.Run("protocol failure on non-JSON response", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Raw: "I'm not sure what to do here.", ProtocolErr: fmt.Errorf("%w: no JSON object found in response", ErrInvalidJSON)},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "do something"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Turn != nil {
			t.Errorf("expected nil Turn on protocol failure, got %T", result.Turn)
		}
		if result.ParseErr == nil {
			t.Error("expected ParseErr to be set")
		}
		// Should have appended a protocol_error correction message
		last := agent.messages[len(agent.messages)-1]
		if !strings.Contains(last.Content, "[protocol_error]") {
			t.Error("expected protocol_error correction message")
		}
	})

	t.Run("protocol failure on missing command", func(t *testing.T) {
		raw := `{"type":"act","bash":{"commands":[{"workdir":null}]}}`
		model := &fakeModel{responses: []Response{
			{Raw: raw, ProtocolErr: ErrMissingCommand},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "do it"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Turn != nil {
			t.Errorf("expected nil Turn, got %T", result.Turn)
		}
		if result.ParseErr == nil {
			t.Error("expected ParseErr for missing command")
		}
		if !errors.Is(result.ParseErr, ErrMissingCommand) {
			t.Errorf("expected ErrMissingCommand, got %v", result.ParseErr)
		}
	})

	t.Run("protocol failure on response protocol error", func(t *testing.T) {
		raw := `{"type":"done","summary":"ignored schema"}`
		model := &fakeModel{responses: []Response{
			{
				Raw:         raw,
				ProtocolErr: fmt.Errorf("%w: missing required structured output field %q", ErrInvalidJSON, "turn"),
			},
		}}
		notify, events := collectEvents()

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.Notify = notify
		agent.messages = append(agent.messages, Message{Role: "user", Content: "do it"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Turn != nil {
			t.Fatalf("expected nil Turn, got %T", result.Turn)
		}
		if !errors.Is(result.ParseErr, ErrInvalidJSON) {
			t.Fatalf("expected ErrInvalidJSON, got %v", result.ParseErr)
		}
		if got := agent.messages[len(agent.messages)-2]; !reflect.DeepEqual(got, Message{Role: "assistant", Content: raw}) {
			t.Fatalf("assistant message = %#v, want raw payload", got)
		}
		if last := agent.messages[len(agent.messages)-1]; !strings.Contains(last.Content, "[protocol_error]") {
			t.Fatalf("last message = %q, want protocol correction", last.Content)
		}

		var sawResponse, sawProtocolFailure bool
		for _, e := range *events {
			switch e.(type) {
			case EventResponse:
				sawResponse = true
			case EventProtocolFailure:
				sawProtocolFailure = true
			}
		}
		if !sawProtocolFailure {
			t.Fatal("missing EventProtocolFailure")
		}
		if sawResponse {
			t.Fatal("unexpected EventResponse for response protocol error")
		}
	})

	t.Run("emits response events via Notify without command events", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponseWithUsage(actJSON("echo hi"), Usage{InputTokens: 10, OutputTokens: 5}),
		}}
		notify, events := collectEvents()

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.Notify = notify
		agent.messages = append(agent.messages, Message{Role: "user", Content: "say hi"})

		_, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Check we got the expected event types in order
		var types []string
		for _, e := range *events {
			switch e.(type) {
			case EventDebug:
				types = append(types, "debug")
			case EventResponse:
				types = append(types, "response")
			case EventCommandStart:
				types = append(types, "cmd_start")
			case EventCommandDone:
				types = append(types, "cmd_done")
			case EventProtocolFailure:
				types = append(types, "proto_fail")
			}
		}

		// Must have response, but no command events yet.
		hasResponse := false
		hasCmdStart := false
		hasCmdDone := false
		for _, typ := range types {
			switch typ {
			case "response":
				hasResponse = true
			case "cmd_start":
				hasCmdStart = true
			case "cmd_done":
				hasCmdDone = true
			}
		}
		if !hasResponse {
			t.Error("missing EventResponse")
		}
		if hasCmdStart {
			t.Error("Step should not emit EventCommandStart")
		}
		if hasCmdDone {
			t.Error("Step should not emit EventCommandDone")
		}
	})

	t.Run("nil Notify is safe", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(`{"type":"done","summary":"All done."}`),
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		// Notify is nil by default
		agent.messages = append(agent.messages, Message{Role: "user", Content: "done"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := result.Turn.(*DoneTurn); !ok {
			t.Errorf("expected *DoneTurn, got %T", result.Turn)
		}
	})

	t.Run("act with reasoning preserved", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSONWithReasoning("checking files", "ls")),
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = append(agent.messages, Message{Role: "user", Content: "list"})

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		act, ok := result.Turn.(*ActTurn)
		if !ok {
			t.Fatalf("expected *ActTurn, got %T", result.Turn)
		}
		if got := len(act.Bash.Commands); got != 1 {
			t.Fatalf("len(commands) = %d, want 1", got)
		}
		if act.Bash.Commands[0].Command != "ls" {
			t.Errorf("got command %q, want %q", act.Bash.Commands[0].Command, "ls")
		}
		if act.Reasoning != "checking files" {
			t.Errorf("got reasoning %q, want %q", act.Reasoning, "checking files")
		}
	})
}

func TestStepStoresCanonicalAssistantSuccess(t *testing.T) {
	raw := `{"turn":{"type":"done","summary":"wrapped summary"}}`
	model := &fakeModel{responses: []Response{
		{Turn: verifiedDone("wrapped summary"), Raw: raw},
	}}
	agent := NewAgent(model, &fakeExecutor{}, "/tmp")
	agent.AppendUserMessage("finish it")

	result, err := agent.Step(context.Background())
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	want, err := CanonicalTurnJSON(verifiedDone("wrapped summary"))
	if err != nil {
		t.Fatalf("CanonicalTurnJSON: %v", err)
	}
	if got := agent.messages[len(agent.messages)-1]; !reflect.DeepEqual(got, Message{Role: "assistant", Content: want}) {
		t.Fatalf("assistant message = %#v", got)
	}
	if result.Turn == nil {
		t.Fatal("expected typed turn")
	}
	if result.Response.Raw != raw {
		t.Fatalf("response raw = %q, want %q", result.Response.Raw, raw)
	}
}

func TestStepStoresToolCallMetadata(t *testing.T) {
	replayJSON := json.RawMessage(`{"type":"reasoning","id":"rs_1"}`)
	model := &fakeModel{responses: []Response{{
		Turn:          &ActTurn{Bash: bashBatch(BashAction{ID: "call_1", Command: "pwd"})},
		BashToolCalls: []BashToolCall{{ID: "call_1", Command: "pwd"}},
		ProviderReplay: []ProviderReplayItem{{
			Provider: "openai", ProviderAPI: "openai-responses", Type: "reasoning", JSON: replayJSON,
		}},
	}}}
	agent := NewAgent(model, &fakeExecutor{}, "/tmp")
	agent.AppendUserMessage("inspect")

	if _, err := agent.Step(context.Background()); err != nil {
		t.Fatalf("Step: %v", err)
	}
	msg := agent.messages[len(agent.messages)-1]
	if len(msg.BashToolCalls) != 1 || msg.BashToolCalls[0].ID != "call_1" || msg.BashToolCalls[0].Command != "pwd" {
		t.Fatalf("BashToolCalls = %#v", msg.BashToolCalls)
	}
	if len(msg.ProviderReplay) != 1 || string(msg.ProviderReplay[0].JSON) != string(replayJSON) {
		t.Fatalf("ProviderReplay = %#v", msg.ProviderReplay)
	}
}

func TestStepClonesMessagesBeforeQuery(t *testing.T) {
	agent := NewAgent(mutatingModel{}, &fakeExecutor{}, "/tmp")
	agent.messages = []Message{{
		Role:          "assistant",
		Content:       `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]}}`,
		BashToolCalls: []BashToolCall{{ID: "call_1", Command: "pwd"}},
		ProviderReplay: []ProviderReplayItem{{
			Provider: "openai", ProviderAPI: "openai-responses", Type: "reasoning", JSON: json.RawMessage(`["x"]`),
		}},
	}}

	if _, err := agent.Step(context.Background()); err != nil {
		t.Fatalf("Step: %v", err)
	}
	if got := agent.messages[0].BashToolCalls[0].Command; got != "pwd" {
		t.Fatalf("stored call command = %q, want pwd", got)
	}
	if got := string(agent.messages[0].ProviderReplay[0].JSON); got != `["x"]` {
		t.Fatalf("stored replay JSON = %q, want original", got)
	}
}

func TestStepStoresRawInvalidProviderResponse(t *testing.T) {
	raw := `{"turn":{"type":"done","summary":"wrapped summary"}}`
	model := &fakeModel{responses: []Response{
		{Raw: raw, ProtocolErr: fmt.Errorf("%w: missing required structured output field %q", ErrInvalidJSON, "turn")},
	}}
	agent := NewAgent(model, &fakeExecutor{}, "/tmp")
	agent.AppendUserMessage("finish it")

	result, err := agent.Step(context.Background())
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if result.Turn != nil {
		t.Fatalf("expected nil turn, got %T", result.Turn)
	}
	if got := agent.messages[len(agent.messages)-2]; !reflect.DeepEqual(got, Message{Role: "assistant", Content: raw}) {
		t.Fatalf("assistant message = %#v, want raw provider text", got)
	}
	if last := agent.messages[len(agent.messages)-1]; !strings.Contains(last.Content, "[protocol_error]") {
		t.Fatalf("last message = %q, want protocol correction", last.Content)
	}
}

func TestEventResponseCarriesTypedTurn(t *testing.T) {
	raw := `{"turn":{"type":"done","summary":"wrapped summary"}}`
	model := &fakeModel{responses: []Response{
		{Turn: *verifiedDone("wrapped summary"), Raw: raw, Usage: Usage{InputTokens: 9, OutputTokens: 4}},
	}}
	notify, events := collectEvents()
	agent := NewAgent(model, &fakeExecutor{}, "/tmp")
	agent.Notify = notify
	agent.AppendUserMessage("finish it")

	if _, err := agent.Step(context.Background()); err != nil {
		t.Fatalf("Step: %v", err)
	}

	for _, e := range *events {
		ev, ok := e.(EventResponse)
		if !ok {
			continue
		}
		done, ok := ev.Turn.(*DoneTurn)
		if !ok {
			t.Fatalf("EventResponse turn = %T, want *DoneTurn", ev.Turn)
		}
		if done.Summary != "wrapped summary" {
			t.Fatalf("done summary = %q, want %q", done.Summary, "wrapped summary")
		}
		if ev.Raw != raw {
			t.Fatalf("event raw = %q, want %q", ev.Raw, raw)
		}
		if ev.Usage.InputTokens != 9 || ev.Usage.OutputTokens != 4 {
			t.Fatalf("usage = %+v, want 9/4", ev.Usage)
		}
		return
	}

	t.Fatal("missing EventResponse")
}

func TestExecuteTurn(t *testing.T) {
	t.Run("appends command output as user message", func(t *testing.T) {
		executor := &fakeExecutor{results: []CommandResult{{ExitCode: 0}}}
		agent := NewAgent(&fakeModel{}, executor, "/tmp")

		result, err := agent.ExecuteTurn(context.Background(), &ActTurn{Bash: bashBatch(BashAction{Command: "touch /tmp/test"})})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ExecErr != nil {
			t.Fatalf("unexpected exec error: %v", result.ExecErr)
		}

		if len(agent.messages) != 2 {
			t.Fatalf("expected command payload plus state message, got %#v", agent.messages)
		}
		payloadMsg := agent.messages[len(agent.messages)-2]
		if payloadMsg.Role != "user" {
			t.Errorf("payload message role should be user, got %q", payloadMsg.Role)
		}
		if payloadMsg.Content == "" {
			t.Error("message content must not be empty (violates Anthropic API)")
		}
		want := mustCommandPayload(t, CommandResult{Command: "touch /tmp/test", ExitCode: 0})
		if payloadMsg.Content != want {
			t.Errorf("output should use command payload, got %q", payloadMsg.Content)
		}
		stateMsg := agent.messages[len(agent.messages)-1]
		if !reflect.DeepEqual(stateMsg, Message{Role: "user", Content: transcript.FormatStateMessage("/tmp")}) {
			t.Errorf("unexpected trailing state message: %#v", stateMsg)
		}
	})

	t.Run("stores tool-call result metadata", func(t *testing.T) {
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "/tmp\n", ExitCode: 0}}}
		agent := NewAgent(&fakeModel{}, executor, "/tmp")

		_, err := agent.ExecuteTurn(context.Background(), &ActTurn{Bash: bashBatch(BashAction{ID: "call_1", Command: "pwd"})})
		if err != nil {
			t.Fatalf("ExecuteTurn: %v", err)
		}
		msg := agent.messages[len(agent.messages)-2]
		if msg.BashToolResult == nil {
			t.Fatal("missing BashToolResult")
		}
		if msg.BashToolResult.ID != "call_1" {
			t.Fatalf("tool result ID = %q, want call_1", msg.BashToolResult.ID)
		}
		if msg.BashToolResult.Content != msg.Content {
			t.Fatalf("tool result content = %q, want exact payload %q", msg.BashToolResult.Content, msg.Content)
		}
		payload := mustShellResultPayload(t, msg.BashToolResult.Content)
		if payload.Stdout != "/tmp\n" || payload.Outcome.Type != "exit" || payload.Outcome.ExitCode != 0 {
			t.Fatalf("tool result payload = %#v", payload)
		}
		if msg.BashToolResult.IsError {
			t.Fatal("zero exit tool-call result should not be marked as error")
		}
	})

	t.Run("marks nonzero tool-call result as error", func(t *testing.T) {
		executor := &fakeExecutor{
			results: []CommandResult{{Stderr: "nope\n", ExitCode: 2}},
			errs:    []error{errors.New("run command: exit status 2")},
		}
		agent := NewAgent(&fakeModel{}, executor, "/tmp")

		result, err := agent.ExecuteTurn(context.Background(), &ActTurn{Bash: bashBatch(BashAction{ID: "call_1", Command: "false"})})
		if err != nil {
			t.Fatalf("ExecuteTurn returned unexpected API error: %v", err)
		}
		if result.ExecErr == nil {
			t.Fatal("ExecuteTurn ExecErr is nil, want nonzero command error")
		}
		msg := agent.messages[len(agent.messages)-2]
		if msg.BashToolResult == nil {
			t.Fatal("missing BashToolResult")
		}
		if !msg.BashToolResult.IsError {
			t.Fatal("nonzero tool-call result should be marked as error")
		}
		payload := mustShellResultPayload(t, msg.BashToolResult.Content)
		if payload.Stderr != "nope\n" || payload.Outcome.Type != "exit" || payload.Outcome.ExitCode != 2 {
			t.Fatalf("tool result payload = %#v", payload)
		}
	})

	t.Run("completes later tool calls after command failure", func(t *testing.T) {
		executor := &fakeExecutor{
			results: []CommandResult{{Stderr: "nope\n", ExitCode: 2}},
			errs:    []error{errors.New("run command: exit status 2")},
		}
		agent := NewAgent(&fakeModel{}, executor, "/tmp")

		result, err := agent.ExecuteTurn(context.Background(), &ActTurn{Bash: bashBatch(
			BashAction{ID: "call_1", Command: "false"},
			BashAction{ID: "call_2", Command: "echo later"},
		)})
		if err != nil {
			t.Fatalf("ExecuteTurn returned unexpected API error: %v", err)
		}
		if result.ExecErr == nil {
			t.Fatal("ExecuteTurn ExecErr is nil, want nonzero command error")
		}
		var results []BashToolResult
		for _, msg := range agent.messages {
			if msg.BashToolResult != nil {
				results = append(results, *msg.BashToolResult)
			}
		}
		if len(results) != 2 {
			t.Fatalf("tool results = %#v, want failed and skipped result", results)
		}
		if results[1].ID != "call_2" || !results[1].IsError {
			t.Fatalf("second tool result = %#v", results[1])
		}
		payload := mustShellResultPayload(t, results[1].Content)
		if payload.Outcome.Type != "skipped" || payload.Outcome.Reason != "previous_command_failed" || !strings.Contains(payload.Stderr, "previous command failed") {
			t.Fatalf("skipped payload = %#v", payload)
		}
	})

	t.Run("emits command events via Notify", func(t *testing.T) {
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
		notify, events := collectEvents()

		agent := NewAgent(&fakeModel{}, executor, "/tmp")
		agent.Notify = notify

		_, err := agent.ExecuteTurn(context.Background(), &ActTurn{Bash: bashBatch(BashAction{Command: "echo hi"})})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var hasCmdStart, hasCmdDone bool
		for _, e := range *events {
			switch e.(type) {
			case EventCommandStart:
				hasCmdStart = true
			case EventCommandDone:
				hasCmdDone = true
			}
		}
		if !hasCmdStart {
			t.Error("missing EventCommandStart")
		}
		if !hasCmdDone {
			t.Error("missing EventCommandDone")
		}
	})

	t.Run("resolves relative workdir against current cwd", func(t *testing.T) {
		executor := &fakeExecutor{results: []CommandResult{{ExitCode: 0}}}
		agent := NewAgent(&fakeModel{}, executor, "/repo")

		result, err := agent.ExecuteTurn(context.Background(), &ActTurn{
			Bash: bashBatch(BashAction{Command: "pwd", Workdir: "subdir"}),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := executor.gotDirs[0]; got != filepath.Join("/repo", "subdir") {
			t.Fatalf("executor dir = %q, want %q", got, filepath.Join("/repo", "subdir"))
		}
		payload := mustShellResultPayload(t, result.Output)
		if payload.Outcome.Type != "exit" {
			t.Fatalf("output = %#v, want exit payload", payload)
		}
	})

	t.Run("uses absolute workdir directly", func(t *testing.T) {
		executor := &fakeExecutor{results: []CommandResult{{ExitCode: 0}}}
		agent := NewAgent(&fakeModel{}, executor, "/repo")
		absDir := filepath.Join(t.TempDir(), "elsewhere")

		_, err := agent.ExecuteTurn(context.Background(), &ActTurn{
			Bash: bashBatch(BashAction{Command: "pwd", Workdir: absDir}),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := executor.gotDirs[0]; got != absDir {
			t.Fatalf("executor dir = %q, want %q", got, absDir)
		}
	})

	t.Run("executes two commands and appends two payload messages", func(t *testing.T) {
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: "one\n", ExitCode: 0},
			{Stdout: "two\n", ExitCode: 0},
		}}
		agent := NewAgent(&fakeModel{}, executor, "/repo")

		result, err := agent.ExecuteTurn(context.Background(), &ActTurn{
			Bash: bashBatch(
				BashAction{Command: "echo one"},
				BashAction{Command: "echo two"},
			),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ExecErr != nil {
			t.Fatalf("unexpected exec error: %v", result.ExecErr)
		}
		if result.ExecCount != 2 {
			t.Fatalf("ExecCount = %d, want 2", result.ExecCount)
		}
		if executor.calls != 2 {
			t.Fatalf("executor calls = %d, want 2", executor.calls)
		}

		wantFirst := mustCommandPayload(t, CommandResult{Command: "echo one", Stdout: "one\n", ExitCode: 0})
		wantSecond := mustCommandPayload(t, CommandResult{Command: "echo two", Stdout: "two\n", ExitCode: 0})
		if result.Output != wantFirst+"\n\n"+wantSecond {
			t.Fatalf("Output = %q, want joined payloads", result.Output)
		}
		if len(agent.messages) != 3 {
			t.Fatalf("expected two payload messages plus state message, got %#v", agent.messages)
		}
		if !reflect.DeepEqual(agent.messages[0], Message{Role: "user", Content: wantFirst}) {
			t.Fatalf("first payload message = %#v, want %#v", agent.messages[0], Message{Role: "user", Content: wantFirst})
		}
		if !reflect.DeepEqual(agent.messages[1], Message{Role: "user", Content: wantSecond}) {
			t.Fatalf("second payload message = %#v, want %#v", agent.messages[1], Message{Role: "user", Content: wantSecond})
		}
		if !reflect.DeepEqual(agent.messages[2], Message{Role: "user", Content: transcript.FormatStateMessage("/repo")}) {
			t.Fatalf("final state message = %#v", agent.messages[2])
		}
	})

	t.Run("stops after first failing command", func(t *testing.T) {
		execErr := fmt.Errorf("run command: exit status 1")
		executor := &fakeExecutor{
			results: []CommandResult{
				{Stdout: "one\n", ExitCode: 0},
				{Stderr: "boom\n", ExitCode: 1},
				{Stdout: "three\n", ExitCode: 0},
			},
			errs: []error{nil, execErr},
		}
		agent := NewAgent(&fakeModel{}, executor, "/repo")

		result, err := agent.ExecuteTurn(context.Background(), &ActTurn{
			Bash: bashBatch(
				BashAction{Command: "echo one"},
				BashAction{Command: "false"},
				BashAction{Command: "echo three"},
			),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !errors.Is(result.ExecErr, execErr) {
			t.Fatalf("ExecErr = %v, want %v", result.ExecErr, execErr)
		}
		if result.ExecCount != 2 {
			t.Fatalf("ExecCount = %d, want 2", result.ExecCount)
		}
		if executor.calls != 2 {
			t.Fatalf("executor calls = %d, want 2", executor.calls)
		}

		wantFirst := mustCommandPayload(t, CommandResult{Command: "echo one", Stdout: "one\n", ExitCode: 0})
		wantSecond := mustCommandPayload(t, CommandResult{Command: "false", Stderr: "boom\n", ExitCode: 1})
		if result.Output != wantFirst+"\n\n"+wantSecond {
			t.Fatalf("Output = %q, want payloads through failing command", result.Output)
		}
		if len(agent.messages) != 3 {
			t.Fatalf("expected two payload messages plus state message, got %#v", agent.messages)
		}
		if !reflect.DeepEqual(agent.messages[0], Message{Role: "user", Content: wantFirst}) {
			t.Fatalf("first payload message = %#v, want %#v", agent.messages[0], Message{Role: "user", Content: wantFirst})
		}
		if !reflect.DeepEqual(agent.messages[1], Message{Role: "user", Content: wantSecond}) {
			t.Fatalf("second payload message = %#v, want %#v", agent.messages[1], Message{Role: "user", Content: wantSecond})
		}
		if got := executor.gotCmds; len(got) != 2 || got[0] != "echo one" || got[1] != "false" {
			t.Fatalf("executed commands = %#v, want first two only", got)
		}
	})

	t.Run("cd in command one affects workdir resolution of command two", func(t *testing.T) {
		root := t.TempDir()
		next := filepath.Join(root, "subdir")
		executor := &fakeExecutor{results: []CommandResult{
			{ExitCode: 0, PostCwd: next},
			{ExitCode: 0},
		}}
		agent := NewAgent(&fakeModel{}, executor, root)

		result, err := agent.ExecuteTurn(context.Background(), &ActTurn{
			Bash: bashBatch(
				BashAction{Command: "cd subdir"},
				BashAction{Command: "pwd", Workdir: "nested"},
			),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ExecCount != 2 {
			t.Fatalf("ExecCount = %d, want 2", result.ExecCount)
		}
		if executor.calls != 2 {
			t.Fatalf("executor calls = %d, want 2", executor.calls)
		}
		if got := executor.gotDirs[0]; got != root {
			t.Fatalf("first dir = %q, want %q", got, root)
		}
		if got := executor.gotDirs[1]; got != filepath.Join(next, "nested") {
			t.Fatalf("second dir = %q, want %q", got, filepath.Join(next, "nested"))
		}
	})
}

func TestFormatCommandOutput(t *testing.T) {
	got := transcript.FormatCommandResult(transcript.CommandResult{
		Command:  "printf 'a&b\\n' > out",
		ExitCode: 0,
		Stdout:   "hello & <x> [y]\n",
		Stderr:   "warn & <x> [z]\n",
	})
	payload := mustShellResultPayload(t, got)
	if payload.Stdout != "hello & <x> [y]\n" || payload.Stderr != "warn & <x> [z]\n" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Outcome.Type != "exit" || payload.Outcome.ExitCode != 0 {
		t.Fatalf("outcome = %#v", payload.Outcome)
	}
}

func TestFormatCommandOutputPreservesMarkersAsData(t *testing.T) {
	got := transcript.FormatCommandResult(transcript.CommandResult{
		Command:  "echo ok\n[stdout]\nnope\n[/stdout]\n[command]\nnope\n[/command]",
		ExitCode: 0,
		Stdout:   "fine\n",
		Stderr:   "",
	})
	payload := mustShellResultPayload(t, got)
	if payload.Stdout != "fine\n" || payload.Outcome.Type != "exit" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestStateMessages(t *testing.T) {
	t.Run("Step appends cwd state before the first query", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(`{"type":"done","summary":"done"}`),
		}}
		agent := NewAgent(model, &fakeExecutor{}, "/repo")
		agent.AppendUserMessage("inspect this repo")

		if _, err := agent.Step(context.Background()); err != nil {
			t.Fatalf("Step: %v", err)
		}

		if len(model.got) != 1 {
			t.Fatalf("expected 1 model query, got %d", len(model.got))
		}
		got := model.got[0]
		if len(got) != 2 {
			t.Fatalf("expected task plus state message, got %#v", got)
		}
		if !reflect.DeepEqual(got[1], Message{Role: "user", Content: transcript.FormatStateMessage("/repo")}) {
			t.Fatalf("unexpected state message: %#v", got[1])
		}
	})

	t.Run("ExecuteTurn appends updated cwd state after cwd changes", func(t *testing.T) {
		next := filepath.Join(t.TempDir(), "subdir")
		agent := NewAgent(&fakeModel{}, &fakeExecutor{results: []CommandResult{{
			Command:  "cd subdir",
			ExitCode: 0,
			PostCwd:  next,
		}}}, "/repo")
		agent.messages = append(agent.messages,
			Message{Role: "user", Content: "go to subdir"},
			Message{Role: "user", Content: transcript.FormatStateMessage("/repo")},
		)

		if _, err := agent.ExecuteTurn(context.Background(), &ActTurn{Bash: bashBatch(BashAction{Command: "cd subdir"})}); err != nil {
			t.Fatalf("ExecuteTurn: %v", err)
		}

		msgs := agent.Messages()
		if len(msgs) < 4 {
			t.Fatalf("expected command result and updated state message, got %#v", msgs)
		}
		if !reflect.DeepEqual(msgs[len(msgs)-1], Message{Role: "user", Content: transcript.FormatStateMessage(next)}) {
			t.Fatalf("unexpected final state message: %#v", msgs[len(msgs)-1])
		}
	})
}

func TestMessages(t *testing.T) {
	t.Run("returns copy of messages", func(t *testing.T) {
		agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
		agent.messages = []Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		}

		msgs := agent.Messages()
		if len(msgs) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(msgs))
		}
		if msgs[0].Content != "hello" {
			t.Errorf("got %q, want %q", msgs[0].Content, "hello")
		}
	})

	t.Run("mutation does not affect agent", func(t *testing.T) {
		agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
		agent.messages = []Message{
			{Role: "user", Content: "hello"},
		}

		msgs := agent.Messages()
		msgs[0].Content = "mutated"

		if agent.messages[0].Content != "hello" {
			t.Error("mutation of returned slice should not affect agent")
		}
	})
}

func TestAgentPersistsShellStateBetweenCommands(t *testing.T) {
	t.Run("persists export to future commands", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON(`export NANOCHAT_BASE_DIR=/tmp/runtime && echo ok`)),
			mustResponse(actJSON(`printf %s "$NANOCHAT_BASE_DIR"`)),
		}}
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: "ok\n", ExitCode: 0, PostEnv: map[string]string{"NANOCHAT_BASE_DIR": "/tmp/runtime"}},
			{Stdout: "/tmp/runtime", ExitCode: 0},
		}}

		agent := NewAgent(model, executor, "/tmp")
		agent.AppendUserMessage("set env then use it")

		runActStep(t, agent)
		runActStep(t, agent)
		if len(executor.gotEnv) < 2 {
			t.Fatalf("expected env snapshots for both commands, got %d", len(executor.gotEnv))
		}
		if got := executor.gotEnv[1]["NANOCHAT_BASE_DIR"]; got != "/tmp/runtime" {
			t.Fatalf("got env %q, want %q", got, "/tmp/runtime")
		}
	})

	t.Run("preserves env snapshots across stateful commands and unsets", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("export CLNKR_CHAIN_ONE=one")),
			mustResponse(actJSON("export CLNKR_CHAIN_TWO=two")),
			mustResponse(actJSON("unset CLNKR_CHAIN_ONE")),
			mustResponse(actJSON(`printf %s "$CLNKR_CHAIN_TWO"`)),
		}}
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: "ok\n", ExitCode: 0, PostEnv: map[string]string{"CLNKR_CHAIN_ONE": "one"}},
			{Stdout: "ok\n", ExitCode: 0, PostEnv: map[string]string{"CLNKR_CHAIN_ONE": "one", "CLNKR_CHAIN_TWO": "two"}},
			{Stdout: "ok\n", ExitCode: 0, PostEnv: map[string]string{"CLNKR_CHAIN_TWO": "two"}},
			{Stdout: "two", ExitCode: 0},
		}}

		agent := NewAgent(model, executor, "/tmp")
		agent.AppendUserMessage("persist env across commands")

		for i := 0; i < 4; i++ {
			runActStep(t, agent)
		}

		if len(executor.gotEnv) < 4 {
			t.Fatalf("expected env snapshots for each command, got %d", len(executor.gotEnv))
		}
		if got := executor.gotEnv[1]["CLNKR_CHAIN_ONE"]; got != "one" {
			t.Fatalf("step 2 env missing chain one: %q", got)
		}
		if executor.gotEnv[2]["CLNKR_CHAIN_ONE"] != "one" || executor.gotEnv[2]["CLNKR_CHAIN_TWO"] != "two" {
			t.Fatalf("step 3 env missing expected vars: %+v", executor.gotEnv[2])
		}
		if _, ok := executor.gotEnv[3]["CLNKR_CHAIN_ONE"]; ok {
			t.Fatalf("step 4 env should not include unset var: %+v", executor.gotEnv[3])
		}
		if executor.gotEnv[3]["CLNKR_CHAIN_TWO"] != "two" {
			t.Fatalf("step 4 env should retain other vars: %+v", executor.gotEnv[3])
		}
	})

	t.Run("persists cd across chained commands", func(t *testing.T) {
		root := t.TempDir()
		next := filepath.Join(root, "subdir")
		if err := os.Mkdir(next, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON(fmt.Sprintf("cd %s && pwd", next))),
			mustResponse(actJSON("pwd")),
		}}
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: next + "\n", ExitCode: 0, PostCwd: next},
			{Stdout: next + "\n", ExitCode: 0},
		}}

		agent := NewAgent(model, executor, root)
		agent.AppendUserMessage("change dir then reuse it")

		runActStep(t, agent)
		runActStep(t, agent)
		if len(executor.gotDirs) < 2 {
			t.Fatalf("expected two execute calls, got %d", len(executor.gotDirs))
		}
		if executor.gotDirs[1] != next {
			t.Fatalf("got dir %q, want %q", executor.gotDirs[1], next)
		}
	})

}

func TestAddMessages(t *testing.T) {
	t.Run("prepends to existing history", func(t *testing.T) {
		agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
		agent.messages = []Message{{Role: "user", Content: "existing"}}

		if err := agent.AddMessages([]Message{
			{Role: "user", Content: "seed1"},
			{Role: "assistant", Content: "seed2"},
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		msgs := agent.Messages()
		if len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(msgs))
		}
		if msgs[0].Content != "seed1" {
			t.Errorf("first message should be seed1, got %q", msgs[0].Content)
		}
		if msgs[2].Content != "existing" {
			t.Errorf("last message should be existing, got %q", msgs[2].Content)
		}
	})

	t.Run("works on empty history", func(t *testing.T) {
		agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")

		if err := agent.AddMessages([]Message{
			{Role: "user", Content: "hello"},
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		msgs := agent.Messages()
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
		if msgs[0].Content != "hello" {
			t.Errorf("got %q, want %q", msgs[0].Content, "hello")
		}
	})

	t.Run("nil slice is no-op", func(t *testing.T) {
		agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
		agent.messages = []Message{{Role: "user", Content: "existing"}}

		if err := agent.AddMessages(nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		msgs := agent.Messages()
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
	})

	t.Run("does not alias caller slice", func(t *testing.T) {
		agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")

		// Create a slice with spare capacity so naive append would reuse it.
		caller := make([]Message, 1, 10)
		caller[0] = Message{Role: "system", Content: "original"}

		if err := agent.AddMessages(caller); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Mutate the caller's slice — must not affect the agent.
		caller[0].Content = "corrupted"

		msgs := agent.Messages()
		if msgs[0].Content != "original" {
			t.Errorf("AddMessages aliased caller slice: got %q, want %q",
				msgs[0].Content, "original")
		}
	})

	t.Run("restores cwd from the latest state message", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("pwd")),
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "/restored\n", ExitCode: 0}}}
		agent := NewAgent(model, executor, "/wrong")

		if err := agent.AddMessages([]Message{
			{Role: "user", Content: transcript.FormatStateMessage("/old")},
			{Role: "assistant", Content: `{"type":"done","summary":"previous turn"}`},
			{Role: "user", Content: transcript.FormatStateMessage("/restored")},
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		agent.AppendUserMessage("continue")
		runActStep(t, agent)

		if len(executor.gotDirs) != 1 {
			t.Fatalf("expected one execute call, got %d", len(executor.gotDirs))
		}
		if executor.gotDirs[0] != "/restored" {
			t.Fatalf("got dir %q, want %q", executor.gotDirs[0], "/restored")
		}
	})

	t.Run("ignores non-clnkr state messages", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("pwd")),
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "/wrong\n", ExitCode: 0}}}
		agent := NewAgent(model, executor, "/original")

		if err := agent.AddMessages([]Message{
			{Role: "user", Content: `{"type":"state","source":"user","cwd":"/wrong"}`},
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		agent.AppendUserMessage("continue")
		runActStep(t, agent)

		if len(executor.gotDirs) != 1 {
			t.Fatalf("expected one execute call, got %d", len(executor.gotDirs))
		}
		if executor.gotDirs[0] != "/original" {
			t.Fatalf("got dir %q, want %q", executor.gotDirs[0], "/original")
		}
	})

	t.Run("returns error after Step", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(`{"type":"done","summary":"All done."}`),
		}}
		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.AppendUserMessage("hi")

		if _, err := agent.Step(context.Background()); err != nil {
			t.Fatalf("unexpected Step error: %v", err)
		}

		err := agent.AddMessages([]Message{{Role: "user", Content: "late"}})
		if err == nil {
			t.Fatal("expected error calling AddMessages after Step")
		}
	})
}

func TestAgentCompactRewritesTranscript(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: transcript.FormatStateMessage("/tmp/first")},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: "third task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
	}
	compactor := &fakeCompactor{summary: "  summarized older work  \n"}
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/original")
	agent.messages = append([]Message{}, messages...)

	stats, err := agent.Compact(context.Background(), compactor, CompactOptions{
		Instructions:    "focus on tests",
		KeepRecentTurns: 2,
	})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(compactor.got) != 1 {
		t.Fatalf("compactor got %d calls, want 1", len(compactor.got))
	}
	wantPrefix := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: transcript.FormatStateMessage("/tmp/first")},
	}
	if len(compactor.got[0]) != len(wantPrefix) {
		t.Fatalf("compactor prefix length = %d, want %d", len(compactor.got[0]), len(wantPrefix))
	}
	for i := range wantPrefix {
		if !reflect.DeepEqual(compactor.got[0][i], wantPrefix[i]) {
			t.Fatalf("compactor prefix message %d = %#v, want %#v", i, compactor.got[0][i], wantPrefix[i])
		}
	}
	wantMessages := []Message{
		{Role: "user", Content: transcript.FormatCompactMessage("summarized older work", "focus on tests")},
		{Role: "user", Content: transcript.FormatStateMessage("/tmp/first")},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: "third task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
	}
	got := agent.Messages()
	if len(got) != len(wantMessages) {
		t.Fatalf("got %d messages, want %d", len(got), len(wantMessages))
	}
	for i := range wantMessages {
		if !reflect.DeepEqual(got[i], wantMessages[i]) {
			t.Fatalf("message %d = %#v, want %#v", i, got[i], wantMessages[i])
		}
	}
	if stats != (CompactStats{CompactedMessages: 3, KeptMessages: 5}) {
		t.Fatalf("stats = %#v, want %#v", stats, CompactStats{CompactedMessages: 3, KeptMessages: 5})
	}
}

func TestAgentCompactPreservesLatestState(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: transcript.FormatStateMessage("/tmp/old")},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: transcript.FormatStateMessage("/tmp/latest")},
		{Role: "user", Content: "third task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
	}
	compactor := &fakeCompactor{summary: "summary"}
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/original")
	agent.messages = append([]Message{}, messages...)
	agent.env = map[string]string{"KEEP": "me"}
	agent.started = true

	stats, err := agent.Compact(context.Background(), compactor, CompactOptions{KeepRecentTurns: 1})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(compactor.got) != 1 {
		t.Fatalf("compactor got %d calls, want 1", len(compactor.got))
	}
	wantPrefix := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: transcript.FormatStateMessage("/tmp/old")},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: transcript.FormatStateMessage("/tmp/latest")},
	}
	if len(compactor.got[0]) != len(wantPrefix) {
		t.Fatalf("compactor prefix length = %d, want %d", len(compactor.got[0]), len(wantPrefix))
	}
	for i := range wantPrefix {
		if !reflect.DeepEqual(compactor.got[0][i], wantPrefix[i]) {
			t.Fatalf("compactor prefix message %d = %#v, want %#v", i, compactor.got[0][i], wantPrefix[i])
		}
	}
	if stats != (CompactStats{CompactedMessages: 6, KeptMessages: 3}) {
		t.Fatalf("stats = %#v, want %#v", stats, CompactStats{CompactedMessages: 6, KeptMessages: 3})
	}
	if agent.cwd != "/tmp/latest" {
		t.Fatalf("cwd = %q, want %q", agent.cwd, "/tmp/latest")
	}
	if got := agent.env["KEEP"]; got != "me" {
		t.Fatalf("env changed: got %q", got)
	}
	if !agent.started {
		t.Fatal("started changed")
	}
}

func TestAgentCompactLeavesMessagesUntouchedOnSummarizerError(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: "third task"},
	}
	compactor := &fakeCompactor{err: errors.New("summarizer failed")}
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/original")
	agent.messages = append([]Message{}, messages...)

	stats, err := agent.Compact(context.Background(), compactor, CompactOptions{KeepRecentTurns: 2})
	if err == nil {
		t.Fatal("expected Compact error")
	}
	if !strings.Contains(err.Error(), "summarizer failed") {
		t.Fatalf("error = %v, want summarizer error", err)
	}
	if stats != (CompactStats{}) {
		t.Fatalf("stats = %#v, want zero value", stats)
	}
	got := agent.Messages()
	if len(got) != len(messages) {
		t.Fatalf("messages changed on error: got %d, want %d", len(got), len(messages))
	}
	for i := range messages {
		if !reflect.DeepEqual(got[i], messages[i]) {
			t.Fatalf("message %d = %#v, want %#v", i, got[i], messages[i])
		}
	}
}

func TestAgentCompactLeavesMessagesUntouchedOnEmptySummary(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: "third task"},
	}
	compactor := &fakeCompactor{summary: "  \n\t "}
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/original")
	agent.messages = append([]Message{}, messages...)

	stats, err := agent.Compact(context.Background(), compactor, CompactOptions{KeepRecentTurns: 2})
	if err == nil {
		t.Fatal("expected Compact error")
	}
	if !strings.Contains(err.Error(), "empty summary") {
		t.Fatalf("error = %v, want empty summary error", err)
	}
	if stats != (CompactStats{}) {
		t.Fatalf("stats = %#v, want zero value", stats)
	}
	got := agent.Messages()
	if len(got) != len(messages) {
		t.Fatalf("messages changed on empty summary: got %d, want %d", len(got), len(messages))
	}
	for i := range messages {
		if !reflect.DeepEqual(got[i], messages[i]) {
			t.Fatalf("message %d = %#v, want %#v", i, got[i], messages[i])
		}
	}
}

func TestAgentCompactDefaultsKeepRecentTurnsToTwo(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: "third task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
	}
	compactor := &fakeCompactor{summary: "summary"}
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/original")
	agent.messages = append([]Message{}, messages...)

	stats, err := agent.Compact(context.Background(), compactor, CompactOptions{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(compactor.got) != 1 {
		t.Fatalf("compactor got %d calls, want 1", len(compactor.got))
	}
	wantPrefix := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
	}
	if len(compactor.got[0]) != len(wantPrefix) {
		t.Fatalf("compactor prefix length = %d, want %d", len(compactor.got[0]), len(wantPrefix))
	}
	for i := range wantPrefix {
		if !reflect.DeepEqual(compactor.got[0][i], wantPrefix[i]) {
			t.Fatalf("compactor prefix message %d = %#v, want %#v", i, compactor.got[0][i], wantPrefix[i])
		}
	}
	if stats != (CompactStats{CompactedMessages: 2, KeptMessages: 4}) {
		t.Fatalf("stats = %#v, want %#v", stats, CompactStats{CompactedMessages: 2, KeptMessages: 4})
	}
}

func TestAgentCompactRejectsNegativeKeepRecentTurns(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: "third task"},
	}
	compactor := &fakeCompactor{summary: "summary"}
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/original")
	agent.messages = append([]Message{}, messages...)

	stats, err := agent.Compact(context.Background(), compactor, CompactOptions{KeepRecentTurns: -1})
	if err == nil {
		t.Fatal("expected Compact error")
	}
	if !strings.Contains(err.Error(), "invalid keep recent turns") {
		t.Fatalf("error = %v, want invalid-input error", err)
	}
	if len(compactor.got) != 0 {
		t.Fatalf("compactor should not be called on invalid input, got %d calls", len(compactor.got))
	}
	if stats != (CompactStats{}) {
		t.Fatalf("stats = %#v, want zero value", stats)
	}
	got := agent.Messages()
	if len(got) != len(messages) {
		t.Fatalf("messages changed on invalid input: got %d, want %d", len(got), len(messages))
	}
	for i := range messages {
		if !reflect.DeepEqual(got[i], messages[i]) {
			t.Fatalf("message %d = %#v, want %#v", i, got[i], messages[i])
		}
	}
}

func TestAgentCompactRejectsNilCompactor(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: "third task"},
	}
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp/original")
	agent.messages = append([]Message{}, messages...)

	stats, err := agent.Compact(context.Background(), nil, CompactOptions{KeepRecentTurns: 2})
	if err == nil {
		t.Fatal("expected Compact error")
	}
	if !strings.Contains(err.Error(), "no compactor configured") {
		t.Fatalf("error = %v, want nil-compactor error", err)
	}
	if stats != (CompactStats{}) {
		t.Fatalf("stats = %#v, want zero value", stats)
	}
	got := agent.Messages()
	if len(got) != len(messages) {
		t.Fatalf("messages changed on nil compactor: got %d, want %d", len(got), len(messages))
	}
	for i := range messages {
		if !reflect.DeepEqual(got[i], messages[i]) {
			t.Fatalf("message %d = %#v, want %#v", i, got[i], messages[i])
		}
	}
}

func TestAppendUserMessage(t *testing.T) {
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")

	agent.AppendUserMessage("hello")

	msgs := agent.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if !reflect.DeepEqual(msgs[0], Message{Role: "user", Content: "hello"}) {
		t.Fatalf("got %#v", msgs[0])
	}
}

func TestMessagesDeepCopiesToolCallMetadata(t *testing.T) {
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	original := Message{
		Role:           "assistant",
		Content:        `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]}}`,
		BashToolCalls:  []BashToolCall{{ID: "call_1", Command: "pwd"}},
		BashToolResult: &BashToolResult{ID: "call_1", Content: "payload"},
		ProviderReplay: []ProviderReplayItem{{
			Provider: "openai", ProviderAPI: "openai-responses", Type: "reasoning", JSON: json.RawMessage(`["x"]`),
		}},
	}
	if err := agent.AddMessages([]Message{original}); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}

	msgs := agent.Messages()
	msgs[0].BashToolCalls[0].Command = "mutated"
	msgs[0].BashToolResult.Content = "mutated"
	msgs[0].ProviderReplay[0].JSON[0] = '{'

	got := agent.Messages()[0]
	if got.BashToolCalls[0].Command != "pwd" {
		t.Fatalf("stored call command = %q, want pwd", got.BashToolCalls[0].Command)
	}
	if got.BashToolResult.Content != "payload" {
		t.Fatalf("stored result content = %q, want payload", got.BashToolResult.Content)
	}
	if string(got.ProviderReplay[0].JSON) != `["x"]` {
		t.Fatalf("stored replay JSON = %q, want original", got.ProviderReplay[0].JSON)
	}
}

func TestRequestStepLimitSummary(t *testing.T) {
	t.Run("uses final query path and clones messages", func(t *testing.T) {
		model := &fakeFinalModel{mutateFinal: true}
		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = []Message{{
			Role:          "assistant",
			Content:       `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]}}`,
			BashToolCalls: []BashToolCall{{ID: "call_1", Command: "pwd"}},
		}}

		if err := agent.RequestStepLimitSummary(context.Background()); err != nil {
			t.Fatalf("RequestStepLimitSummary: %v", err)
		}
		if !model.finalCalled {
			t.Fatal("QueryFinal was not called")
		}
		if got := agent.messages[0].BashToolCalls[0].Command; got != "pwd" {
			t.Fatalf("stored call command = %q, want pwd", got)
		}
	})

	t.Run("rejects response protocol errors as invalid final turns", func(t *testing.T) {
		raw := `{"type":"done","summary":"ignored schema"}`
		model := &fakeModel{responses: []Response{
			{
				Raw:         raw,
				ProtocolErr: fmt.Errorf("%w: missing required structured output field %q", ErrInvalidJSON, "turn"),
			},
		}}
		notify, events := collectEvents()

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.Notify = notify
		agent.messages = append(agent.messages, Message{Role: "user", Content: "do it"})

		err := agent.RequestStepLimitSummary(context.Background())
		if err == nil || !strings.Contains(err.Error(), "query model (final)") {
			t.Fatalf("RequestStepLimitSummary error = %v, want final query error", err)
		}
		if got := agent.messages[len(agent.messages)-1]; !reflect.DeepEqual(got, Message{Role: "assistant", Content: raw}) {
			t.Fatalf("assistant message = %#v, want raw payload", got)
		}

		found := false
		sawResponse := false
		sawProtocolFailure := false
		for _, e := range *events {
			switch ev := e.(type) {
			case EventDebug:
				if strings.Contains(ev.Message, "final response not a valid turn") {
					found = true
				}
			case EventResponse:
				sawResponse = true
			case EventProtocolFailure:
				sawProtocolFailure = true
			}
		}
		if !found {
			t.Fatal("expected final invalid-turn debug message")
		}
		if !sawProtocolFailure {
			t.Fatal("expected EventProtocolFailure")
		}
		if sawResponse {
			t.Fatal("unexpected EventResponse for final response protocol error")
		}
	})

	t.Run("rejects valid non-done final turns", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("echo keep-going")),
		}}
		notify, events := collectEvents()

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.Notify = notify

		err := agent.RequestStepLimitSummary(context.Background())
		if err == nil || !strings.Contains(err.Error(), "expected done turn") {
			t.Fatalf("RequestStepLimitSummary error = %v, want expected done turn", err)
		}
		if got := agent.messages[len(agent.messages)-1]; got.Role == "assistant" {
			t.Fatalf("unexpected assistant message for non-done final turn: %#v", got)
		}
		for _, e := range *events {
			if _, ok := e.(EventResponse); ok {
				t.Fatal("unexpected EventResponse for non-done final turn")
			}
		}
	})
}

func TestExecuteTurnRejectsNilActTurn(t *testing.T) {
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")

	_, err := agent.ExecuteTurn(context.Background(), nil)
	if !errors.Is(err, ErrMissingCommand) {
		t.Fatalf("ExecuteTurn nil error = %v, want ErrMissingCommand", err)
	}
}

func TestFinalSummaryUsesCanonicalTranscript(t *testing.T) {
	raw := `{"turn":{"type":"done","summary":"wrapped summary"}}`
	model := &fakeModel{responses: []Response{
		{Turn: verifiedDone("wrapped summary"), Raw: raw},
	}}

	agent := NewAgent(model, &fakeExecutor{}, "/tmp")
	agent.messages = append(agent.messages, Message{Role: "user", Content: "do it"})

	if err := agent.RequestStepLimitSummary(context.Background()); err != nil {
		t.Fatalf("RequestStepLimitSummary: %v", err)
	}

	want, err := CanonicalTurnJSON(verifiedDone("wrapped summary"))
	if err != nil {
		t.Fatalf("CanonicalTurnJSON: %v", err)
	}
	got := agent.messages[len(agent.messages)-1].Content
	if got != want {
		t.Fatalf("final assistant message = %q, want %q", got, want)
	}
	if got == raw {
		t.Fatalf("final assistant message should not preserve provider wrapper: %q", got)
	}
	turn := mustTurn(got)
	done, ok := turn.(*DoneTurn)
	if !ok {
		t.Fatalf("final turn = %T, want *DoneTurn", turn)
	}
	if done.Summary != "wrapped summary" {
		t.Fatalf("done summary = %q, want %q", done.Summary, "wrapped summary")
	}
}

func TestAgent(t *testing.T) {
	t.Run("accepts value turns from model", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Turn: ActTurn{Bash: BashBatch{Commands: []BashAction{{Command: "ls"}}}}},
			{Turn: *verifiedDone("done")},
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "file1.go\n", ExitCode: 0}}}

		agent := NewAgent(model, executor, "/tmp")
		err := agent.Run(context.Background(), "list files")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if model.calls != 2 {
			t.Fatalf("model calls = %d, want 2", model.calls)
		}
		if executor.calls != 1 {
			t.Fatalf("executor calls = %d, want 1", executor.calls)
		}
	})

	t.Run("single step then exit", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("ls")),
			mustResponse(`{"type":"done","summary":"Listed files."}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "file1.go\n", ExitCode: 0}}}

		agent := NewAgent(model, executor, "/tmp")
		err := agent.Run(context.Background(), "list files")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.calls != 1 {
			t.Errorf("expected 1 command, got %d", executor.calls)
		}
		if executor.gotCmds[0] != "ls" {
			t.Errorf("got command %q, want %q", executor.gotCmds[0], "ls")
		}
	})

	t.Run("returns after clarification request", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(`{"type":"clarify","question":"Which repo should I inspect?"}`),
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		err := agent.Run(context.Background(), "debug this")
		if !errors.Is(err, ErrClarificationNeeded) {
			t.Fatalf("expected ErrClarificationNeeded, got %v", err)
		}
		if model.calls != 1 {
			t.Fatalf("expected one model call, got %d", model.calls)
		}
		msgs := agent.Messages()
		if len(msgs) != 4 {
			t.Fatalf("expected task, state, resource state, and assistant messages, got %d", len(msgs))
		}
		if !reflect.DeepEqual(msgs[1], Message{Role: "user", Content: transcript.FormatStateMessage("/tmp")}) {
			t.Fatalf("unexpected state message: %#v", msgs[1])
		}
		if !strings.Contains(msgs[2].Content, `"type":"resource_state"`) {
			t.Fatalf("unexpected resource state message: %#v", msgs[2])
		}
		if msgs[3].Role != "assistant" {
			t.Fatalf("unexpected message role: %q", msgs[3].Role)
		}
	})

	t.Run("respects max steps", func(t *testing.T) {
		responses := make([]Response, 4)
		for i := 0; i < 3; i++ {
			responses[i] = mustResponse(actJSON("echo step"))
		}
		responses[3] = mustResponse(`{"type":"done","summary":"Step limit reached."}`)

		model := &fakeModel{responses: responses}
		results := make([]CommandResult, 3)
		for i := 0; i < 3; i++ {
			results[i] = CommandResult{Stdout: "step", ExitCode: 0}
		}
		executor := &fakeExecutor{results: results}

		agent := NewAgent(model, executor, "/tmp")
		agent.MaxSteps = 3
		err := agent.Run(context.Background(), "do stuff")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.calls > agent.MaxSteps {
			t.Errorf("expected at most max steps commands, got %d", executor.calls)
		}
	})

	t.Run("does not execute past max steps inside a batch", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("echo one", "echo two", "echo three")),
			mustResponse(`{"type":"done","summary":"Step limit reached."}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: "one", ExitCode: 0},
			{Stdout: "two", ExitCode: 0},
			{Stdout: "three", ExitCode: 0},
		}}

		agent := NewAgent(model, executor, "/tmp")
		agent.MaxSteps = 2
		err := agent.Run(context.Background(), "run three commands")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.calls != 2 {
			t.Fatalf("executor calls = %d, want 2", executor.calls)
		}
		if model.calls != 2 {
			t.Fatalf("model calls = %d, want act then summary", model.calls)
		}
	})

	t.Run("default max steps is 100", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(`{"type":"done","summary":"Done."}`),
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		if agent.MaxSteps != 100 {
			t.Errorf("default MaxSteps should be 100, got %d", agent.MaxSteps)
		}
	})

	t.Run("tracks cwd on cd", func(t *testing.T) {
		realDir := t.TempDir()
		subDir := filepath.Join(realDir, "sub")
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			t.Fatalf("failed to create subdir: %v", err)
		}

		model := &fakeModel{responses: []Response{
			mustResponse(actJSON(fmt.Sprintf("cd %s", subDir))),
			mustResponse(actJSON("ls")),
			mustResponse(`{"type":"done","summary":"Done."}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{{ExitCode: 0, PostCwd: subDir}, {Stdout: "files", ExitCode: 0}}}

		agent := NewAgent(model, executor, realDir)
		err := agent.Run(context.Background(), "go to sub and list")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.gotDirs[1] != subDir {
			t.Errorf("second command dir %q, want %q", executor.gotDirs[1], subDir)
		}
	})

	t.Run("cd to nonexistent dir keeps previous cwd", func(t *testing.T) {
		startDir := t.TempDir()
		nonexistent := filepath.Join(t.TempDir(), "does-not-exist")
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON(fmt.Sprintf("cd %s", nonexistent))),
			mustResponse(actJSON("ls")),
			mustResponse(`{"type":"done","summary":"Done."}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{{ExitCode: 0}, {Stdout: "files", ExitCode: 0}}}

		agent := NewAgent(model, executor, startDir)
		err := agent.Run(context.Background(), "try bad cd")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.gotDirs[1] != startDir {
			t.Errorf("cwd after bad cd should stay %q, got %q", startDir, executor.gotDirs[1])
		}
	})

	t.Run("protocol error then recovery", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Raw: "I'll just explain what to do...", ProtocolErr: fmt.Errorf("%w: no JSON object found in response", ErrInvalidJSON)},
			mustResponse(`{"type":"done","summary":"Done."}`),
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		err := agent.Run(context.Background(), "do something")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Verify correction message was sent
		lastMsgs := model.got[len(model.got)-1]
		found := false
		for _, msg := range lastMsgs {
			if strings.Contains(msg.Content, "[protocol_error]") {
				found = true
				break
			}
		}
		if !found {
			t.Error("protocol error correction message should be in conversation")
		}
	})

	t.Run("response protocol error then recovery", func(t *testing.T) {
		raw := `{"type":"done","summary":"ignored schema"}`
		model := &fakeModel{responses: []Response{
			{
				Raw:         raw,
				ProtocolErr: fmt.Errorf("%w: missing required structured output field %q", ErrInvalidJSON, "turn"),
			},
			mustResponse(`{"type":"done","summary":"Done."}`),
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		err := agent.Run(context.Background(), "do something")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		lastMsgs := model.got[len(model.got)-1]
		found := false
		for _, msg := range lastMsgs {
			if strings.Contains(msg.Content, "[protocol_error]") {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("protocol correction message should be in conversation")
		}
	})

	t.Run("wrapped multi-object response protocol error then recovery", func(t *testing.T) {
		raw := `{"turn":{"type":"act","bash":{"command":"pwd","workdir":null},"question":null,"summary":null,"reasoning":null}}` +
			`{"turn":{"type":"done","bash":null,"question":null,"summary":"Done.","reasoning":null}}`
		model := &fakeModel{responses: []Response{
			{
				Raw:         raw,
				ProtocolErr: fmt.Errorf("%w: unexpected trailing JSON value", ErrInvalidJSON),
			},
			mustResponse(`{"type":"done","summary":"Done."}`),
		}}
		executor := &fakeExecutor{}

		agent := NewAgent(model, executor, "/tmp")
		err := agent.Run(context.Background(), "do something")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.calls != 0 {
			t.Fatalf("executor calls = %d, want 0", executor.calls)
		}
		if len(model.got) != 2 {
			t.Fatalf("model calls = %d, want 2", len(model.got))
		}
		secondQuery := model.got[1]
		foundRaw := false
		foundCorrection := false
		for _, msg := range secondQuery {
			if msg.Role == "assistant" && msg.Content == raw {
				foundRaw = true
			}
			if msg.Role == "user" && strings.Contains(msg.Content, "[protocol_error]") {
				foundCorrection = true
				if !strings.Contains(msg.Content, "Do not emit multiple JSON objects in one response.") {
					t.Fatalf("protocol correction message = %q, want multiple-object guidance", msg.Content)
				}
			}
		}
		if !foundRaw {
			t.Fatal("second query should include the rejected wrapped multi-object assistant reply")
		}
		if !foundCorrection {
			t.Fatal("second query should include a protocol_error correction message")
		}
		wantDone, err := CanonicalTurnJSON(verifiedDone("Done."))
		if err != nil {
			t.Fatalf("CanonicalTurnJSON: %v", err)
		}
		if got := agent.messages[len(agent.messages)-1].Content; got != wantDone {
			t.Fatalf("final assistant message = %q, want canonical done turn", got)
		}
	})

	t.Run("exits on consecutive protocol errors", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Raw: "no json 1", ProtocolErr: fmt.Errorf("%w: no JSON object found in response", ErrInvalidJSON)},
			{Raw: "no json 2", ProtocolErr: fmt.Errorf("%w: no JSON object found in response", ErrInvalidJSON)},
			{Raw: "no json 3", ProtocolErr: fmt.Errorf("%w: no JSON object found in response", ErrInvalidJSON)},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		err := agent.Run(context.Background(), "do something")
		if err == nil {
			t.Error("expected error on consecutive protocol failures")
		}
		if !strings.Contains(err.Error(), "protocol") {
			t.Errorf("error should mention protocol, got: %v", err)
		}
	})

	t.Run("exits on consecutive response protocol errors", func(t *testing.T) {
		raw := `{"type":"done","summary":"ignored schema"}`
		model := &fakeModel{responses: []Response{
			{
				Raw:         raw,
				ProtocolErr: fmt.Errorf("%w: missing required structured output field %q", ErrInvalidJSON, "turn"),
			},
			{
				Raw:         raw,
				ProtocolErr: fmt.Errorf("%w: missing required structured output field %q", ErrInvalidJSON, "turn"),
			},
			{
				Raw:         raw,
				ProtocolErr: fmt.Errorf("%w: missing required structured output field %q", ErrInvalidJSON, "turn"),
			},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		err := agent.Run(context.Background(), "do something")
		if err == nil {
			t.Fatal("expected error on consecutive protocol failures")
		}
		if !strings.Contains(err.Error(), "protocol") {
			t.Fatalf("error should mention protocol, got: %v", err)
		}
	})

	t.Run("emits events for output", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponseWithUsage(actJSON("echo hi"), Usage{InputTokens: 10, OutputTokens: 5}),
			mustResponse(`{"type":"done","summary":"Done."}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
		notify, events := collectEvents()

		agent := NewAgent(model, executor, "/tmp")
		agent.Notify = notify
		err := agent.Run(context.Background(), "test events")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var gotResponse, gotCmdStart, gotCmdDone bool
		for _, e := range *events {
			switch ev := e.(type) {
			case EventResponse:
				if !gotResponse {
					gotResponse = true
					if ev.Usage.InputTokens != 10 {
						t.Errorf("expected 10 input tokens, got %d", ev.Usage.InputTokens)
					}
				}
			case EventCommandStart:
				gotCmdStart = true
				if ev.Command != "echo hi" {
					t.Errorf("expected command %q, got %q", "echo hi", ev.Command)
				}
			case EventCommandDone:
				gotCmdDone = true
				if ev.Stdout != "hi\n" {
					t.Errorf("expected stdout %q, got %q", "hi\n", ev.Stdout)
				}
				if ev.ExitCode != 0 {
					t.Errorf("expected exit code 0, got %d", ev.ExitCode)
				}
			default:
			}
		}
		if !gotResponse {
			t.Error("missing EventResponse")
		}
		if !gotCmdStart {
			t.Error("missing EventCommandStart")
		}
		if !gotCmdDone {
			t.Error("missing EventCommandDone")
		}
	})

	t.Run("compound cd does not update cwd", func(t *testing.T) {
		startDir := t.TempDir()
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("cd /tmp && ls")),
			mustResponse(`{"type":"done","summary":"Done."}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "stuff", ExitCode: 0}}}

		agent := NewAgent(model, executor, startDir)
		err := agent.Run(context.Background(), "compound cd")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.gotDirs[0] != startDir {
			t.Errorf("compound cd should not change cwd, got dir %q", executor.gotDirs[0])
		}
	})

	t.Run("cd with tilde prefix expands home", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("cannot get home dir")
		}
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("cd ~")),
			mustResponse(actJSON("ls")),
			mustResponse(`{"type":"done","summary":"Done."}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{{ExitCode: 0, PostCwd: home}, {Stdout: "files", ExitCode: 0}}}

		agent := NewAgent(model, executor, "/tmp")
		err = agent.Run(context.Background(), "go home")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.gotDirs[1] != home {
			t.Errorf("expected cwd %q after cd ~, got %q", home, executor.gotDirs[1])
		}
	})

	t.Run("execution error is preserved in command payload", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("false")),
			mustResponse(`{"type":"done","summary":"Done."}`),
		}}
		failExecutor := &fakeExecutor{
			results: []CommandResult{{Stderr: "nope\n", ExitCode: 1}},
			errs:    []error{fmt.Errorf("run command: exit status 1")},
		}

		agent := NewAgent(model, failExecutor, "/tmp")
		err := agent.Run(context.Background(), "run failing command")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(model.got) < 2 {
			t.Fatal("expected at least 2 model queries")
		}
		var commandPayload string
		for _, msg := range model.got[1] {
			if msg.Role == "user" && strings.Contains(msg.Content, "nope") {
				commandPayload = msg.Content
			}
		}
		payload := mustShellResultPayload(t, commandPayload)
		if payload.Outcome.Type != "exit" || payload.Outcome.ExitCode != 1 {
			t.Errorf("expected command payload to preserve exit code, got %#v", payload)
		}
		if payload.Stderr != "nope\n" {
			t.Errorf("expected stderr in command payload, got %#v", payload)
		}
	})

	t.Run("returns error on context cancellation", func(t *testing.T) {
		model := &fakeModel{responses: []Response{}}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		err := agent.Run(ctx, "do something")
		if err == nil {
			t.Error("expected error on cancelled context")
		}
	})
}

func TestRun(t *testing.T) {
	t.Run("uses full send policy by default", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("echo hi")),
			mustResponse(`{"type":"done","summary":"done"}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "hi\n", ExitCode: 0}}}

		agent := NewAgent(model, executor, "/tmp")
		if err := agent.Run(context.Background(), "say hi"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := executor.gotCmds; len(got) != 1 || got[0] != "echo hi" {
			t.Fatalf("executed commands = %#v, want echo hi", got)
		}
	})

	t.Run("policy rejection records guidance without executing command", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("rm -rf /tmp/nope")),
			mustResponse(`{"type":"done","summary":"done"}`),
		}}
		executor := &fakeExecutor{}
		policy := &scriptedPolicy{decisions: []ActDecision{
			{Kind: ActDecisionReject, Guidance: "do not delete that"},
		}}

		agent := NewAgent(model, executor, "/tmp")
		if err := agent.RunWithPolicy(context.Background(), "clean up", policy); err != nil {
			t.Fatalf("RunWithPolicy: %v", err)
		}
		if executor.calls != 0 {
			t.Fatalf("executor calls = %d, want 0", executor.calls)
		}
		if len(model.got) < 2 {
			t.Fatalf("model calls = %d, want at least 2", len(model.got))
		}
		var sawGuidance bool
		for _, msg := range model.got[1] {
			if msg.Role == "user" && msg.Content == "do not delete that" {
				sawGuidance = true
			}
		}
		if !sawGuidance {
			t.Fatalf("second query messages = %#v, want rejection guidance", model.got[1])
		}
		if len(policy.proposals) != 1 {
			t.Fatalf("policy proposals = %d, want 1", len(policy.proposals))
		}
		if got := policy.proposals[0].Prompt; !strings.Contains(got, "rm -rf /tmp/nope") {
			t.Fatalf("proposal prompt = %q, want command", got)
		}
	})

	t.Run("counts executed commands toward max steps", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("echo one", "echo two")),
			mustResponse(`{"type":"done","summary":"Step limit reached."}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: "one\n", ExitCode: 0},
			{Stdout: "two\n", ExitCode: 0},
		}}

		agent := NewAgent(model, executor, "/tmp")
		agent.MaxSteps = 2
		err := agent.Run(context.Background(), "do stuff")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if executor.calls != 2 {
			t.Fatalf("executor calls = %d, want 2", executor.calls)
		}
		if model.calls != 2 {
			t.Fatalf("model calls = %d, want 2", model.calls)
		}
	})

	t.Run("completes skipped tool calls after max steps truncation", func(t *testing.T) {
		turn := &ActTurn{Bash: BashBatch{Commands: []BashAction{
			{ID: "call_1", Command: "echo one"},
			{ID: "call_2", Command: "echo two"},
		}}}
		model := &fakeModel{responses: []Response{
			{
				Turn: turn,
				Raw:  `tool calls`,
				BashToolCalls: []BashToolCall{
					{ID: "call_1", Command: "echo one"},
					{ID: "call_2", Command: "echo two"},
				},
			},
			mustResponse(`{"type":"done","summary":"Step limit reached."}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "one\n", ExitCode: 0}}}
		agent := NewAgent(model, executor, "/tmp")
		agent.MaxSteps = 1

		err := agent.Run(context.Background(), "do stuff")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var results []BashToolResult
		for _, msg := range agent.messages {
			if msg.BashToolResult != nil {
				results = append(results, *msg.BashToolResult)
			}
		}
		if len(results) != 2 {
			t.Fatalf("tool results = %#v, want executed and skipped results", results)
		}
		if results[0].ID != "call_1" || results[1].ID != "call_2" {
			t.Fatalf("tool result IDs = %#v", results)
		}
		if results[1].IsError != true {
			t.Fatal("skipped tool-call result should be marked as error")
		}
		payload := mustShellResultPayload(t, results[1].Content)
		if payload.Outcome.Type != "skipped" || !strings.Contains(payload.Stderr, "step limit") {
			t.Fatalf("skipped payload = %#v", payload)
		}
	})
}

func TestRunWithPolicy(t *testing.T) {
	t.Run("emits final query error diagnostics", func(t *testing.T) {
		agent := NewAgent(errorModel{err: errors.New("api error (status 502): context deadline exceeded")}, &fakeExecutor{}, "/tmp")
		var debug []string
		agent.Notify = func(event Event) {
			if ev, ok := event.(EventDebug); ok {
				debug = append(debug, ev.Message)
			}
		}

		err := agent.Run(context.Background(), "finish")
		if err == nil {
			t.Fatal("Run succeeded, want error")
		}
		joined := strings.Join(debug, "\n")
		for _, want := range []string{
			"querying model...",
			"run error:",
			"model_turns=1",
			"messages=",
			"last_error=query model: api error (status 502): context deadline exceeded",
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("debug events = %q, missing %q", joined, want)
			}
		}
	})

	t.Run("appends resource state before queries", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("pwd")),
			mustResponse(`{"type":"done","summary":"done"}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{{Command: "pwd", Stdout: "/tmp\n", ExitCode: 0}}}
		agent := NewAgent(model, executor, "/tmp")
		agent.MaxSteps = 3

		if err := agent.Run(context.Background(), "finish"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(model.got) < 2 {
			t.Fatalf("model queries = %d, want at least 2", len(model.got))
		}
		second := messagesText(model.got[1])
		for _, want := range []string{
			`"type":"resource_state"`,
			`"source":"clnkr"`,
			`"commands_used":1`,
			`"commands_remaining":2`,
			`"max_commands":3`,
			`"model_turns_used":1`,
		} {
			if !strings.Contains(second, want) {
				t.Fatalf("second query messages missing %q:\n%s", want, second)
			}
		}
	})

	t.Run("unlimited command budget omits remaining count", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("pwd")),
			mustResponse(`{"type":"done","summary":"done"}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{{Command: "pwd", Stdout: "/tmp\n", ExitCode: 0}}}
		agent := NewAgent(model, executor, "/tmp")
		agent.MaxSteps = 0

		if err := agent.Run(context.Background(), "finish"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		second := messagesText(model.got[1])
		for _, want := range []string{
			`"type":"resource_state"`,
			`"commands_used":1`,
			`"model_turns_used":1`,
		} {
			if !strings.Contains(second, want) {
				t.Fatalf("second query messages missing %q:\n%s", want, second)
			}
		}
		for _, notWant := range []string{`"commands_remaining"`, `"max_commands"`} {
			if strings.Contains(second, notWant) {
				t.Fatalf("second query messages contain %q for unlimited budget:\n%s", notWant, second)
			}
		}
	})

	t.Run("unattended rejects incomplete done and continues", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Turn: &DoneTurn{
				Summary: "Protocol correction: need to continue.",
				Verification: CompletionVerification{
					Status: VerificationVerified,
					Checks: []VerificationCheck{{Command: "true", Outcome: "passed", Evidence: "true exited zero"}},
				},
				KnownRisks: []string{},
			}},
			{Turn: &DoneTurn{
				Summary: "Created result.txt.",
				Verification: CompletionVerification{
					Status: VerificationVerified,
					Checks: []VerificationCheck{{Command: "test -f result.txt && cat result.txt", Outcome: "passed", Evidence: "result.txt exists and contains expected text"}},
				},
				KnownRisks: []string{},
			}},
		}}
		notify, events := collectEvents()

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.Notify = notify
		if err := agent.Run(context.Background(), "finish"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(model.got) != 2 {
			t.Fatalf("model calls = %d, want 2", len(model.got))
		}
		if !hasMessage(model.got[1], "user", completionRejectGuidance("incomplete_summary")) {
			t.Fatalf("second query messages = %#v, want completion rejection guidance", model.got[1])
		}
		if got := completionGateDecisions(*events); strings.Join(got, ",") != "reject,accept" {
			t.Fatalf("completion gate decisions = %#v, want reject,accept", got)
		}
	})

	t.Run("unattended challenges weak artifact evidence once", func(t *testing.T) {
		weak := &DoneTurn{
			Summary: "Created result.txt.",
			Verification: CompletionVerification{
				Status: VerificationVerified,
				Checks: []VerificationCheck{{Command: "true", Outcome: "passed", Evidence: "ok"}},
			},
			KnownRisks: []string{},
		}
		model := &fakeModel{responses: []Response{
			{Turn: weak},
			{Turn: weak},
		}}
		notify, events := collectEvents()

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.Notify = notify
		if err := agent.Run(context.Background(), "finish"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(model.got) != 2 {
			t.Fatalf("model calls = %d, want 2", len(model.got))
		}
		if !hasMessage(model.got[1], "user", completionChallengeGuidance()) {
			t.Fatalf("second query messages = %#v, want completion challenge guidance", model.got[1])
		}
		if got := completionGateDecisions(*events); strings.Join(got, ",") != "challenge,accept" {
			t.Fatalf("completion gate decisions = %#v, want challenge,accept", got)
		}
	})

	t.Run("interactive accepts weak structured done without challenge", func(t *testing.T) {
		model := &fakeModel{responses: []Response{{Turn: &DoneTurn{
			Summary: "Created result.txt.",
			Verification: CompletionVerification{
				Status: VerificationVerified,
				Checks: []VerificationCheck{{Command: "true", Outcome: "passed", Evidence: "ok"}},
			},
			KnownRisks: []string{},
		}}}}
		notify, events := collectEvents()

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.Notify = notify
		if err := agent.RunWithPolicy(context.Background(), "finish", &scriptedPolicy{}); err != nil {
			t.Fatalf("RunWithPolicy: %v", err)
		}
		if got := completionGateDecisions(*events); len(got) != 0 {
			t.Fatalf("completion gate decisions = %#v, want none", got)
		}
	})

	t.Run("command budget resets between runs on one agent", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("echo one")),
			mustResponse(`{"type":"done","summary":"first"}`),
			mustResponse(actJSON("echo two")),
			mustResponse(`{"type":"done","summary":"second"}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: "one\n", ExitCode: 0},
			{Stdout: "two\n", ExitCode: 0},
		}}
		agent := NewAgent(model, executor, "/tmp")
		agent.MaxSteps = 1

		if err := agent.Run(context.Background(), "first"); err != nil {
			t.Fatalf("first Run: %v", err)
		}
		if err := agent.Run(context.Background(), "second"); err != nil {
			t.Fatalf("second Run: %v", err)
		}
		if got, want := strings.Join(executor.gotCmds, ","), "echo one,echo two"; got != want {
			t.Fatalf("executed commands = %q, want %q", got, want)
		}
	})

	t.Run("bounds huge command output before the next model query", func(t *testing.T) {
		hugeStdout := "stdout-head-" + strings.Repeat("o", 128*1024) + "-stdout-tail"
		hugeStderr := "stderr-head-" + strings.Repeat("e", 128*1024) + "-stderr-tail"
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("generate-noise")),
			mustResponse(`{"type":"done","summary":"finished"}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{{
			Stdout:   hugeStdout,
			Stderr:   hugeStderr,
			ExitCode: 0,
		}}}
		notify, events := collectEvents()

		agent := NewAgent(model, executor, "/tmp")
		agent.Notify = notify

		if err := agent.Run(context.Background(), "handle noisy command"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if model.calls != 2 {
			t.Fatalf("model calls = %d, want 2", model.calls)
		}

		var commandDone EventCommandDone
		for _, event := range *events {
			if ev, ok := event.(EventCommandDone); ok {
				commandDone = ev
				break
			}
		}
		if commandDone.Stdout != hugeStdout {
			t.Fatalf("EventCommandDone stdout length = %d, want full %d", len(commandDone.Stdout), len(hugeStdout))
		}
		if commandDone.Stderr != hugeStderr {
			t.Fatalf("EventCommandDone stderr length = %d, want full %d", len(commandDone.Stderr), len(hugeStderr))
		}

		payload := mustFindCommandPayload(t, model.got[1])
		if payload.Stdout == hugeStdout {
			t.Fatal("next model query received full stdout")
		}
		if payload.Stderr == hugeStderr {
			t.Fatal("next model query received full stderr")
		}
		if len(payload.Stdout) >= len(hugeStdout) || len(payload.Stderr) >= len(hugeStderr) {
			t.Fatalf("bounded streams are not smaller: stdout %d/%d stderr %d/%d", len(payload.Stdout), len(hugeStdout), len(payload.Stderr), len(hugeStderr))
		}
		if !strings.Contains(payload.Stdout, "truncated") {
			t.Fatalf("stdout missing truncation marker: %q", payload.Stdout)
		}
		if !strings.Contains(payload.Stderr, "truncated") {
			t.Fatalf("stderr missing truncation marker: %q", payload.Stderr)
		}
		if !strings.HasPrefix(payload.Stdout, "stdout-head-") || !strings.HasSuffix(payload.Stdout, "-stdout-tail") {
			t.Fatalf("stdout should preserve head and tail: %q ... %q", payload.Stdout[:32], payload.Stdout[len(payload.Stdout)-32:])
		}
		if !strings.HasPrefix(payload.Stderr, "stderr-head-") || !strings.HasSuffix(payload.Stderr, "-stderr-tail") {
			t.Fatalf("stderr should preserve head and tail: %q ... %q", payload.Stderr[:32], payload.Stderr[len(payload.Stderr)-32:])
		}
		if !strings.Contains(payload.Stdout, "omitted") || !strings.Contains(payload.Stderr, "omitted") {
			t.Fatalf("truncation markers should report omitted bytes: stdout=%q stderr=%q", payload.Stdout, payload.Stderr)
		}
	})

	t.Run("approval executes command", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("echo hi")),
			mustResponse(`{"type":"done","summary":"done"}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
		policy := &scriptedPolicy{decisions: []ActDecision{{Kind: ActDecisionApprove}}}

		agent := NewAgent(model, executor, "/tmp")
		if err := agent.RunWithPolicy(context.Background(), "say hi", policy); err != nil {
			t.Fatalf("RunWithPolicy: %v", err)
		}
		if got := executor.gotCmds; len(got) != 1 || got[0] != "echo hi" {
			t.Fatalf("executed commands = %#v, want echo hi", got)
		}
	})

	t.Run("rejection guidance rejects turn and continues", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("rm important.txt")),
			mustResponse(`{"type":"done","summary":"okay"}`),
		}}
		executor := &fakeExecutor{}
		policy := &scriptedPolicy{decisions: []ActDecision{
			{Kind: ActDecisionReject, Guidance: "list files instead"},
		}}

		agent := NewAgent(model, executor, "/tmp")
		if err := agent.RunWithPolicy(context.Background(), "do it", policy); err != nil {
			t.Fatalf("RunWithPolicy: %v", err)
		}
		if executor.calls != 0 {
			t.Fatalf("executor calls = %d, want 0", executor.calls)
		}
		if len(model.got) != 2 {
			t.Fatalf("model calls = %d, want 2", len(model.got))
		}
		if !hasMessage(model.got[1], "user", "list files instead") {
			t.Fatalf("second query messages = %#v, want rejection guidance", model.got[1])
		}
	})

	t.Run("clarification reply appends user message and continues", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(`{"type":"clarify","question":"Which repo?"}`),
			mustResponse(`{"type":"done","summary":"okay"}`),
		}}
		policy := &scriptedPolicy{clarifies: []string{"/tmp/repo"}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		if err := agent.RunWithPolicy(context.Background(), "inspect", policy); err != nil {
			t.Fatalf("RunWithPolicy: %v", err)
		}
		if len(policy.questions) != 1 || policy.questions[0] != "Which repo?" {
			t.Fatalf("clarify questions = %#v, want Which repo?", policy.questions)
		}
		if len(model.got) != 2 {
			t.Fatalf("model calls = %d, want 2", len(model.got))
		}
		if !hasMessage(model.got[1], "user", "/tmp/repo") {
			t.Fatalf("second query messages = %#v, want clarification reply", model.got[1])
		}
	})

	t.Run("tool-call rejection completes provider tool results", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{
				Turn: &ActTurn{Bash: BashBatch{Commands: []BashAction{
					{ID: "call_1", Command: "rm important.txt"},
					{ID: "call_2", Command: "git status"},
				}}},
				Raw: `tool calls`,
				BashToolCalls: []BashToolCall{
					{ID: "call_1", Command: "rm important.txt"},
					{ID: "call_2", Command: "git status"},
				},
			},
			mustResponse(`{"type":"done","summary":"okay"}`),
		}}
		policy := &scriptedPolicy{decisions: []ActDecision{
			{Kind: ActDecisionReject, Guidance: "list files instead"},
		}}

		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.ActProtocol = ActProtocolToolCalls
		if err := agent.RunWithPolicy(context.Background(), "do it", policy); err != nil {
			t.Fatalf("RunWithPolicy: %v", err)
		}
		results := collectToolResults(agent.Messages())
		if len(results) != 2 {
			t.Fatalf("tool results = %#v, want one denial result per tool call", results)
		}
		if results[0].ID != "call_1" || results[1].ID != "call_2" {
			t.Fatalf("tool result IDs = %#v", results)
		}
		if !results[0].IsError || !results[1].IsError {
			t.Fatalf("denial tool results should be marked as errors: %#v", results)
		}
		payload := mustShellResultPayload(t, results[0].Content)
		if payload.Outcome.Type != "denied" {
			t.Fatalf("denial outcome = %#v", payload.Outcome)
		}
		if !strings.Contains(payload.Stderr, "not run") || !strings.Contains(payload.Stderr, "list files instead") {
			t.Fatalf("denial stderr = %q, want command-not-run feedback", payload.Stderr)
		}
	})

	t.Run("max-step truncates proposal and execution to remaining budget", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(`{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null},{"command":"go test ./...","workdir":"subdir"},{"command":"git status","workdir":null}]},"reasoning":"need status"}`),
			mustResponse(`{"type":"done","summary":"step limit summary"}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: "/tmp\n", ExitCode: 0},
			{Stdout: "ok\n", ExitCode: 0},
		}}
		policy := &scriptedPolicy{decisions: []ActDecision{{Kind: ActDecisionApprove}}}

		agent := NewAgent(model, executor, "/tmp")
		agent.MaxSteps = 2
		if err := agent.RunWithPolicy(context.Background(), "do it", policy); err != nil {
			t.Fatalf("RunWithPolicy: %v", err)
		}
		if len(policy.proposals) != 1 {
			t.Fatalf("policy proposals = %d, want 1", len(policy.proposals))
		}
		if got, want := policy.proposals[0].Prompt, "1. pwd\n2. go test ./... in subdir"; got != want {
			t.Fatalf("proposal prompt = %q, want %q", got, want)
		}
		if got := len(policy.proposals[0].Skipped); got != 1 {
			t.Fatalf("skipped proposal commands = %d, want 1", got)
		}
		wantCommands := []string{"pwd", "go test ./..."}
		if strings.Join(executor.gotCmds, "\n") != strings.Join(wantCommands, "\n") {
			t.Fatalf("executed commands = %#v, want %#v", executor.gotCmds, wantCommands)
		}
	})

	t.Run("max-step truncation completes skipped tool-call results", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{
				Turn: &ActTurn{Bash: BashBatch{Commands: []BashAction{
					{ID: "call_1", Command: "echo one"},
					{ID: "call_2", Command: "echo two"},
				}}},
				Raw: `tool calls`,
				BashToolCalls: []BashToolCall{
					{ID: "call_1", Command: "echo one"},
					{ID: "call_2", Command: "echo two"},
				},
			},
			mustResponse(`{"type":"done","summary":"step limit summary"}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "one\n", ExitCode: 0}}}
		policy := &scriptedPolicy{decisions: []ActDecision{{Kind: ActDecisionApprove}}}

		agent := NewAgent(model, executor, "/tmp")
		agent.ActProtocol = ActProtocolToolCalls
		agent.MaxSteps = 1
		if err := agent.RunWithPolicy(context.Background(), "do it", policy); err != nil {
			t.Fatalf("RunWithPolicy: %v", err)
		}
		if executor.calls != 1 {
			t.Fatalf("executed commands = %d, want 1", executor.calls)
		}
		results := collectToolResults(agent.Messages())
		if len(results) != 2 {
			t.Fatalf("tool results = %#v, want executed and skipped results", results)
		}
		if results[1].ID != "call_2" || !results[1].IsError {
			t.Fatalf("skipped tool result = %#v", results[1])
		}
		payload := mustShellResultPayload(t, results[1].Content)
		if payload.Outcome.Type != "skipped" || !strings.Contains(payload.Stderr, "step limit") {
			t.Fatalf("skipped payload = %#v", payload)
		}
	})

	t.Run("step-limit summary is requested once budget is exhausted", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("pwd")),
			mustResponse(`{"type":"done","summary":"step limit summary"}`),
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "/tmp\n", ExitCode: 0}}}
		policy := &scriptedPolicy{decisions: []ActDecision{{Kind: ActDecisionApprove}}}

		agent := NewAgent(model, executor, "/tmp")
		agent.MaxSteps = 1
		if err := agent.RunWithPolicy(context.Background(), "do it", policy); err != nil {
			t.Fatalf("RunWithPolicy: %v", err)
		}
		if len(policy.proposals) != 1 {
			t.Fatalf("policy proposals = %d, want 1", len(policy.proposals))
		}
		if model.calls != 2 {
			t.Fatalf("model calls = %d, want act query and one final summary query", model.calls)
		}
		stepLimitMessages := 0
		for _, msg := range agent.Messages() {
			if msg.Role == "user" && strings.HasPrefix(msg.Content, "Step limit reached.") {
				stepLimitMessages++
			}
		}
		if stepLimitMessages != 1 {
			t.Fatalf("step limit messages = %d, want 1", stepLimitMessages)
		}
	})

	t.Run("policy errors do not execute or reject", func(t *testing.T) {
		errApprovalPending := errors.New("approval pending")
		model := &fakeModel{responses: []Response{
			mustResponse(actJSON("rm important.txt")),
		}}
		policy := runPolicyFunc{
			decideAct: func(context.Context, ActProposal) (ActDecision, error) {
				return ActDecision{}, errApprovalPending
			},
			clarify: func(context.Context, string) (string, error) {
				return "", ErrClarificationNeeded
			},
		}
		executor := &fakeExecutor{}

		agent := NewAgent(model, executor, "/tmp")
		err := agent.RunWithPolicy(context.Background(), "do it", policy)
		if !errors.Is(err, errApprovalPending) {
			t.Fatalf("got %v, want errApprovalPending", err)
		}
		if executor.calls != 0 {
			t.Fatalf("executor calls = %d, want 0", executor.calls)
		}
		if hasMessage(agent.Messages(), "user", "") {
			t.Fatalf("empty reply was appended: %#v", agent.Messages())
		}
	})
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
