package clnkr

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr/internal/core/transcript"
)

func TestStepRecordsModelTurnsAndProtocolFailures(t *testing.T) {
	t.Run("successful turn is sent state, stored canonically, and not executed", func(t *testing.T) {
		replayJSON := json.RawMessage(`["x"]`)
		model := &fakeModel{responses: []Response{{
			Turn:          &ActTurn{Bash: BashBatch{Commands: []BashAction{{ID: "call_1", Command: "pwd"}}}},
			Raw:           `{"turn":{"type":"act"}}`,
			Usage:         Usage{InputTokens: 9, OutputTokens: 4},
			BashToolCalls: []BashToolCall{{ID: "call_1", Command: "pwd"}},
			ProviderReplay: []ProviderReplayItem{{
				Provider: "openai", ProviderAPI: "openai-responses", Type: "reasoning", JSON: replayJSON,
			}},
		}}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "/tmp\n", ExitCode: 0}}}
		notify, events := collectEvents()
		agent := NewAgent(model, executor, "/repo")
		agent.Notify = notify
		agent.AppendUserMessage("where am I")

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("Step: %v", err)
		}
		if _, ok := result.Turn.(*ActTurn); !ok {
			t.Fatalf("turn = %T, want *ActTurn", result.Turn)
		}
		if executor.calls != 0 {
			t.Fatalf("Step executed %d commands", executor.calls)
		}
		if !hasMessage(model.got[0], "user", transcript.FormatStateMessage("/repo")) {
			t.Fatalf("model query missing cwd state: %#v", model.got[0])
		}
		assistant := agent.Messages()[len(agent.Messages())-1]
		if assistant.Role != "assistant" || len(assistant.BashToolCalls) != 1 || assistant.BashToolCalls[0].ID != "call_1" || len(assistant.ProviderReplay) != 1 || string(assistant.ProviderReplay[0].JSON) != string(replayJSON) {
			t.Fatalf("assistant transcript = %#v", assistant)
		}
		if !strings.Contains(assistant.Content, `"type":"act"`) || strings.Contains(assistant.Content, `"turn"`) {
			t.Fatalf("assistant content = %q, want canonical act turn", assistant.Content)
		}
		ev, ok := firstEvent[EventResponse](*events)
		if !ok || ev.Raw != `{"turn":{"type":"act"}}` || ev.Usage.InputTokens != 9 {
			t.Fatalf("response event = %#v", ev)
		}
	})

	t.Run("protocol failure stores raw assistant response and emits failure event", func(t *testing.T) {
		model := &fakeModel{responses: []Response{{Raw: "no json", ProtocolErr: ErrInvalidJSON}}}
		notify, events := collectEvents()
		agent := NewAgent(model, &fakeExecutor{}, "/repo")
		agent.Notify = notify
		agent.AppendUserMessage("do it")

		result, err := agent.Step(context.Background())
		if err != nil {
			t.Fatalf("Step: %v", err)
		}
		msgs := agent.Messages()
		if !errors.Is(result.ParseErr, ErrInvalidJSON) || result.Turn != nil || msgs[len(msgs)-2].Content != "no json" || !strings.Contains(msgs[len(msgs)-1].Content, "[protocol_error]") {
			t.Fatalf("result=%#v messages=%#v", result, msgs)
		}
		if !hasEvent[EventProtocolFailure](*events) || hasEvent[EventResponse](*events) {
			t.Fatalf("events = %#v", *events)
		}
	})

	t.Run("model query cannot mutate stored transcript metadata", func(t *testing.T) {
		agent := NewAgent(mutatingModel{}, &fakeExecutor{}, "/tmp")
		agent.messages = []Message{{Role: "assistant", Content: `{"type":"act","bash":{"commands":[{"command":"pwd","workdir":null}]}}`, BashToolCalls: []BashToolCall{{ID: "call_1", Command: "pwd"}}, ProviderReplay: []ProviderReplayItem{{JSON: json.RawMessage(`["x"]`)}}}}
		if _, err := agent.Step(context.Background()); err != nil {
			t.Fatalf("Step: %v", err)
		}
		if got := agent.Messages()[0]; got.BashToolCalls[0].Command != "pwd" || string(got.ProviderReplay[0].JSON) != `["x"]` {
			t.Fatalf("stored transcript was mutated: %#v", got)
		}
	})
}

func TestExecuteTurnCoordinatesCommandBatch(t *testing.T) {
	t.Run("forwards cwd and env across commands and appends observable results", func(t *testing.T) {
		root := t.TempDir()
		next := filepath.Join(root, "sub")
		executor := &fakeExecutor{results: []CommandResult{
			{Stdout: "moved\n", ExitCode: 0, PostCwd: next, PostEnv: map[string]string{"CLNKR_TEST": "ok"}},
			{Stdout: "ok\n", ExitCode: 0},
		}}
		notify, events := collectEvents()
		agent := NewAgent(&fakeModel{}, executor, root)
		agent.Notify = notify

		result, err := agent.ExecuteTurn(context.Background(), &ActTurn{Bash: BashBatch{Commands: []BashAction{
			{Command: "cd sub"},
			{ID: "call_2", Command: "printf %s $CLNKR_TEST", Workdir: "nested"},
		}}})
		if err != nil {
			t.Fatalf("ExecuteTurn: %v", err)
		}
		if result.ExecCount != 2 || result.ExecErr != nil {
			t.Fatalf("result = %#v", result)
		}
		if executor.gotDirs[0] != root || executor.gotDirs[1] != filepath.Join(next, "nested") {
			t.Fatalf("executor dirs = %#v", executor.gotDirs)
		}
		if got := executor.gotEnv[1]["CLNKR_TEST"]; got != "ok" {
			t.Fatalf("second command env = %#v", executor.gotEnv[1])
		}
		msgs := agent.Messages()
		if !hasMessage(msgs, "user", transcript.FormatStateMessage(next)) {
			t.Fatalf("messages missing updated cwd state: %#v", msgs)
		}
		results := collectToolResults(msgs)
		if len(results) != 1 || results[0].ID != "call_2" || results[0].IsError {
			t.Fatalf("tool results = %#v", results)
		}
		if !hasEvent[EventCommandStart](*events) || !hasEvent[EventCommandDone](*events) {
			t.Fatalf("command events = %#v", *events)
		}
	})

	t.Run("stops after a command error and completes later provider tool calls as skipped", func(t *testing.T) {
		execErr := errors.New("run command: exit status 2")
		executor := &fakeExecutor{
			results: []CommandResult{{Stderr: "nope\n", ExitCode: 2}},
			errs:    []error{execErr},
		}
		agent := NewAgent(&fakeModel{}, executor, "/repo")

		result, err := agent.ExecuteTurn(context.Background(), &ActTurn{Bash: BashBatch{Commands: []BashAction{
			{ID: "call_1", Command: "false"},
			{ID: "call_2", Command: "echo later"},
		}}})
		if err != nil {
			t.Fatalf("ExecuteTurn API error: %v", err)
		}
		if !errors.Is(result.ExecErr, execErr) || result.ExecCount != 1 || executor.calls != 1 {
			t.Fatalf("result = %#v executor calls=%d", result, executor.calls)
		}
		results := collectToolResults(agent.Messages())
		if len(results) != 2 || !results[0].IsError || results[1].ID != "call_2" || !results[1].IsError {
			t.Fatalf("tool results = %#v", results)
		}
		if payloadOutcome(t, results[1].Content) != CommandOutcomeSkipped {
			t.Fatalf("skipped content = %s", results[1].Content)
		}
	})
}

func TestRunWithPolicyCoordinatesTheFacadeLoop(t *testing.T) {
	t.Run("full-send executes act, sends command result and resource state, then stops on done", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Turn: &ActTurn{Bash: BashBatch{Commands: []BashAction{{Command: "echo hi"}}}}},
			{Turn: verifiedDone("finished")},
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "hi\n", ExitCode: 0}}}
		agent := NewAgent(model, executor, "/tmp")

		if err := agent.Run(context.Background(), "say hi"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if strings.Join(executor.gotCmds, ",") != "echo hi" {
			t.Fatalf("commands = %#v", executor.gotCmds)
		}
		secondQuery := messagesText(model.got[1])
		for _, want := range []string{`"stdout":"hi\n"`, `"type":"resource_state"`, `"commands_used":1`, `"model_turns_used":1`} {
			if !strings.Contains(secondQuery, want) {
				t.Fatalf("second query missing %q:\n%s", want, secondQuery)
			}
		}
	})

	t.Run("clarification and rejection are policy-mediated user turns", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Turn: &ClarifyTurn{Question: "Which repo?"}},
			{Turn: &ActTurn{Bash: BashBatch{Commands: []BashAction{{Command: "rm important.txt"}}}}},
			{Turn: verifiedDone("finished")},
		}}
		policy := &scriptedPolicy{
			clarifies: []string{"/tmp/repo"},
			decisions: []ActDecision{{Kind: ActDecisionReject, Guidance: "list files instead"}},
		}
		executor := &fakeExecutor{}
		agent := NewAgent(model, executor, "/tmp")

		if err := agent.RunWithPolicy(context.Background(), "inspect", policy); err != nil {
			t.Fatalf("RunWithPolicy: %v", err)
		}
		if executor.calls != 0 {
			t.Fatalf("executor calls = %d, want 0", executor.calls)
		}
		if len(policy.questions) != 1 || policy.questions[0] != "Which repo?" {
			t.Fatalf("questions = %#v", policy.questions)
		}
		if !hasMessage(model.got[1], "user", "/tmp/repo") || !hasMessage(model.got[2], "user", "list files instead") {
			t.Fatalf("model queries missing policy replies: %#v", model.got)
		}
	})

	t.Run("protocol failures retry with correction and stop after three consecutive failures", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Raw: "bad 1", ProtocolErr: ErrInvalidJSON},
			{Raw: "bad 2", ProtocolErr: ErrInvalidJSON},
			{Raw: "bad 3", ProtocolErr: ErrInvalidJSON},
		}}
		agent := NewAgent(model, &fakeExecutor{}, "/tmp")

		err := agent.Run(context.Background(), "do it")
		if err == nil || !strings.Contains(err.Error(), "consecutive protocol failures") {
			t.Fatalf("Run error = %v", err)
		}
		if model.calls != 3 {
			t.Fatalf("model calls = %d, want 3", model.calls)
		}
		correction := messagesText(model.got[1])
		if !strings.Contains(correction, "[protocol_error]") ||
			!strings.Contains(correction, "Your previous response was ignored and no command ran") ||
			!strings.Contains(correction, "invalid_json") {
			t.Fatalf("second query missing correction: %#v", model.got[1])
		}
	})
}

func TestRunWithPolicyEnforcesBudgetsAndCompletionGate(t *testing.T) {
	t.Run("truncates approval proposal and asks for one final summary at the step limit", func(t *testing.T) {
		model := &fakeModel{responses: []Response{
			{Turn: &ActTurn{Bash: BashBatch{Commands: []BashAction{
				{ID: "call_1", Command: "pwd"},
				{ID: "call_2", Command: "go test ./...", Workdir: "subdir"},
				{ID: "call_3", Command: "git status"},
			}}, Reasoning: "need status"}},
			{Turn: verifiedDone("step limit summary")},
		}}
		executor := &fakeExecutor{results: []CommandResult{{Stdout: "/tmp\n", ExitCode: 0}, {Stdout: "ok\n", ExitCode: 0}}}
		policy := &scriptedPolicy{decisions: []ActDecision{{Kind: ActDecisionApprove}}}
		agent := NewAgent(model, executor, "/tmp")
		agent.MaxSteps = 2

		if err := agent.RunWithPolicy(context.Background(), "do it", policy); err != nil {
			t.Fatalf("RunWithPolicy: %v", err)
		}
		if got, want := policy.proposals[0].Prompt, "1. pwd\n2. go test ./... in subdir"; got != want {
			t.Fatalf("proposal = %q, want %q", got, want)
		}
		if len(policy.proposals[0].Skipped) != 1 || strings.Join(executor.gotCmds, ",") != "pwd,go test ./..." {
			t.Fatalf("proposal=%#v commands=%#v", policy.proposals[0], executor.gotCmds)
		}
		if results := collectToolResults(agent.Messages()); len(results) != 3 || payloadOutcome(t, results[2].Content) != CommandOutcomeSkipped {
			t.Fatalf("tool results = %#v", results)
		}
		if strings.Count(messagesText(agent.Messages()), "Step limit reached.") != 1 {
			t.Fatalf("messages = %#v", agent.Messages())
		}
	})

}

func TestAgentPublicAPIGuards(t *testing.T) {
	t.Run("AddMessages seeds caller-owned history before start", func(t *testing.T) {
		agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/wrong")
		agent.messages = []Message{{Role: "user", Content: "existing"}}
		caller := []Message{{Role: "user", Content: "seed"}, {Role: "user", Content: transcript.FormatStateMessage("/restored")}}
		if err := agent.AddMessages(caller); err != nil {
			t.Fatalf("AddMessages: %v", err)
		}
		caller[0].Content = "mutated"
		msgs := agent.Messages()
		if msgs[0].Content != "seed" || msgs[2].Content != "existing" || agent.Cwd() != "/restored" {
			t.Fatalf("messages=%#v cwd=%q", msgs, agent.Cwd())
		}
		agent.AppendUserMessage("started")
		if err := agent.AddMessages([]Message{{Role: "user", Content: "late"}}); err == nil {
			t.Fatal("AddMessages after start succeeded")
		}
	})

	t.Run("ExecuteTurn rejects nil act turns", func(t *testing.T) {
		_, err := NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp").ExecuteTurn(context.Background(), nil)
		if !errors.Is(err, ErrMissingCommand) {
			t.Fatalf("ExecuteTurn nil error = %v", err)
		}
	})

	t.Run("step-limit final summary uses final query path and canonical transcript", func(t *testing.T) {
		model := &fakeFinalModel{fakeModel: fakeModel{responses: []Response{{
			Raw:  `{"turn":{"type":"done","summary":"wrapped"}}`,
			Turn: verifiedDone("wrapped"),
		}}}, mutateFinal: true}
		agent := NewAgent(model, &fakeExecutor{}, "/tmp")
		agent.messages = []Message{{Role: "assistant", Content: "previous", BashToolCalls: []BashToolCall{{ID: "call_1", Command: "pwd"}}}}
		if err := agent.RequestStepLimitSummary(context.Background()); err != nil {
			t.Fatalf("RequestStepLimitSummary: %v", err)
		}
		got := agent.Messages()[len(agent.Messages())-1].Content
		if !model.finalCalled || agent.Messages()[0].BashToolCalls[0].Command != "pwd" || strings.Contains(got, `"turn"`) || !strings.Contains(got, `"summary":"wrapped"`) {
			t.Fatalf("finalCalled=%v messages=%#v final=%q", model.finalCalled, agent.Messages(), got)
		}
		bad := NewAgent(&fakeModel{responses: []Response{{Turn: &ActTurn{Bash: BashBatch{Commands: []BashAction{{Command: "pwd"}}}}}}}, &fakeExecutor{}, "/tmp")
		if err := bad.RequestStepLimitSummary(context.Background()); err == nil || !strings.Contains(err.Error(), "expected done turn") {
			t.Fatalf("bad final error = %v", err)
		}
	})
}

func TestAgentCompactFacade(t *testing.T) {
	seed := []Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: `{"type":"done","summary":"first"}`},
		{Role: "user", Content: transcript.FormatStateMessage("/restored")},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: `{"type":"done","summary":"second"}`},
		{Role: "user", Content: "third"},
	}
	agent := NewAgent(&fakeModel{}, &fakeExecutor{}, "/wrong")
	if err := agent.AddMessages(seed); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	stats, err := agent.Compact(context.Background(), &fakeCompactor{summary: "  older work  "}, CompactOptions{KeepRecentTurns: 2})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if stats.CompactedMessages != 3 || agent.Cwd() != "/restored" || !strings.Contains(agent.Messages()[0].Content, "older work") {
		t.Fatalf("stats=%#v cwd=%q messages=%#v", stats, agent.Cwd(), agent.Messages())
	}

	before := agent.Messages()
	compactor := &fakeCompactor{summary: "summary"}
	if _, err := agent.Compact(context.Background(), compactor, CompactOptions{KeepRecentTurns: -1}); err == nil || len(compactor.got) != 0 {
		t.Fatalf("negative keep recent: err=%v calls=%d", err, len(compactor.got))
	}
	if _, err := agent.Compact(context.Background(), &fakeCompactor{summary: " \n"}, CompactOptions{KeepRecentTurns: 2}); err == nil || messagesText(agent.Messages()) != messagesText(before) {
		t.Fatalf("empty summary err=%v messages=%#v", err, agent.Messages())
	}
}
