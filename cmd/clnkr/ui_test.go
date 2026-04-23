package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	clnkr "github.com/clnkr-ai/clnkr"
)

func setupModel() model {
	s := defaultStyles(true)
	return setupModelWithStyles(s)
}

func setupModelWithStyles(s *styles) model {
	m := newModel(modelOpts{
		styles:    s,
		modelName: "test-model",
		maxSteps:  100,
	})
	m.width = 80
	m.height = 24
	m.chat.resize(80, 21) // leave room for status (1) + input (2)
	m.input.setWidth(80)
	m.running = true
	return m
}

func actTurn(command string) *clnkr.ActTurn {
	return &clnkr.ActTurn{Bash: clnkr.BashBatch{Commands: []clnkr.BashAction{{Command: command}}}}
}

// updateModel is a test helper that calls Update and asserts the result is a model.
func updateModel(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	result, _ := m.Update(msg)
	um, ok := result.(model)
	if !ok {
		t.Fatalf("expected model, got %T", result)
	}
	return um
}

func TestModelHandlesEventResponse(t *testing.T) {
	m := setupModel()
	m = updateModel(t, m, eventMsg{event: clnkr.EventDebug{Message: "querying model..."}})

	msg := eventMsg{event: clnkr.EventResponse{
		Turn:  &clnkr.DoneTurn{Summary: "full response"},
		Usage: clnkr.Usage{InputTokens: 100, OutputTokens: 50},
	}}
	um := updateModel(t, m, msg)

	if um.chat.streaming {
		t.Error("expected streaming=false after EventResponse")
	}
	view := um.View()
	if !strings.Contains(view.Content, "full response") {
		t.Error("View should contain authoritative response content")
	}
}

func TestUINoColorViewStaysMonochromeWithNewContentIndicator(t *testing.T) {
	m := setupModelWithStyles(startupStyles(true, true))
	m.chat.content.WriteString("assistant output\n")
	m.chat.hasNew = true
	m.chat.viewport.SetContent(m.chat.content.String())
	m.input.textarea.SetValue("follow up")

	view := m.View()
	if ansiColorPattern.MatchString(view.Content) {
		t.Fatalf("no-color view should not emit ANSI color codes, got %q", view.Content)
	}
	plain := stripANSI(view.Content)
	for _, want := range []string{"new content", "RUNNING", iconPrompt, "follow up"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view should contain %q, got %q", want, plain)
		}
	}
}

func TestModelFocusToggle(t *testing.T) {
	m := setupModel()

	if m.focus != focusInput {
		t.Error("default focus should be input")
	}

	um := updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if um.focus != focusViewport {
		t.Error("Esc should switch to viewport focus")
	}

	um = updateModel(t, um, tea.KeyPressMsg{Code: 'i'})
	if um.focus != focusInput {
		t.Error("i should switch to input focus")
	}
}

func questionKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: '/', Mod: tea.ModShift, Text: "?"}
}

func TestModelQuestionMarkOpensHelpOverlay(t *testing.T) {
	m := setupModel()

	m = updateModel(t, m, questionKey())
	if !m.help.visible {
		t.Fatal("expected help overlay to be visible")
	}
	view := m.View()
	for _, want := range []string{"clnkr help", "/compact [instructions]", "/delegate <task>", "Ctrl+Y"} {
		if !strings.Contains(view.Content, want) {
			t.Fatalf("help view missing %q: %q", want, view.Content)
		}
	}
}

func TestModelQuestionMarkDoesNotStealComposedInput(t *testing.T) {
	m := setupModel()
	m.running = false
	m.input.textarea.SetValue("what changed")

	m = updateModel(t, m, questionKey())
	if m.help.visible {
		t.Fatal("? should not open help while composing non-empty input")
	}
	if got := m.input.textarea.Value(); got != "what changed?" {
		t.Fatalf("input = %q, want question mark inserted", got)
	}
}

func TestModelQuestionMarkDoesNotStealApprovalOrClarificationInput(t *testing.T) {
	for _, tc := range []struct {
		name          string
		approval      bool
		clarification bool
	}{
		{name: "approval", approval: true},
		{name: "clarification", clarification: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := setupModel()
			m.running = true
			m.awaitingApproval = tc.approval
			m.awaitingClarification = tc.clarification
			m.input.textarea.SetValue("why")

			m = updateModel(t, m, questionKey())
			if m.help.visible {
				t.Fatal("? should not open help during active prompt text entry")
			}
			if got := m.input.textarea.Value(); got != "why?" {
				t.Fatalf("input = %q, want question mark inserted", got)
			}

			m.input.textarea.Reset()
			m = updateModel(t, m, questionKey())
			if m.help.visible {
				t.Fatal("? should not open help during empty active prompt text entry")
			}
			if got := m.input.textarea.Value(); got != "?" {
				t.Fatalf("empty prompt input = %q, want question mark inserted", got)
			}
		})
	}
}

func TestModelQuestionMarkOpensHelpInScrollModeDuringPrompts(t *testing.T) {
	for _, tc := range []struct {
		name          string
		approval      bool
		clarification bool
	}{
		{name: "approval", approval: true},
		{name: "clarification", clarification: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := setupModel()
			m.running = true
			m.awaitingApproval = tc.approval
			m.awaitingClarification = tc.clarification
			m.focus = focusViewport
			m.input.textarea.Blur()

			m = updateModel(t, m, questionKey())
			if !m.help.visible {
				t.Fatal("? should open help from scroll mode during prompts")
			}
		})
	}
}

func TestModelHelpHidesOtherOverlays(t *testing.T) {
	m := setupModel()
	m.diff.visible = true
	m.reasoning.show("trace", 80, 20)

	m = updateModel(t, m, questionKey())
	if !m.help.visible {
		t.Fatal("expected help overlay to be visible")
	}
	if m.diff.visible || m.reasoning.visible {
		t.Fatalf("help should hide other overlays, diff=%v reasoning=%v", m.diff.visible, m.reasoning.visible)
	}
}

func TestModelEscapeDismissesHelpOverlay(t *testing.T) {
	m := setupModel()
	m.focus = focusInput
	m.help.show(80, 20)

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.help.visible {
		t.Fatal("expected help overlay to be hidden")
	}
	if m.focus != focusInput {
		t.Fatalf("help dismissal should not change focus, got %v", m.focus)
	}
}

func TestModelQuestionMarkDismissesHelpOverlay(t *testing.T) {
	m := setupModel()
	m.help.show(80, 20)

	m = updateModel(t, m, questionKey())
	if m.help.visible {
		t.Fatal("expected ? to hide visible help overlay")
	}
}

func TestModelHelpOverlayBlocksNormalKeys(t *testing.T) {
	m := setupModel()
	m.help.show(80, 20)
	m.focus = focusViewport

	m = updateModel(t, m, tea.KeyPressMsg{Code: 'i'})
	if m.focus != focusViewport {
		t.Fatal("help overlay should block normal focus-changing keys")
	}
	if !m.help.visible {
		t.Fatal("help overlay should remain visible after unrelated key")
	}
}

func TestModelHelpOverlayLetsCtrlCCancel(t *testing.T) {
	cancelled := false
	m := setupModel()
	m.running = true
	m.cancel = func() { cancelled = true }
	m.help.show(80, 20)

	m = updateModel(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !cancelled {
		t.Fatal("Ctrl+C should cancel while help is visible")
	}
}

func TestModelOpeningHelpClearsPendingGGChord(t *testing.T) {
	m := setupModel()
	m.focus = focusViewport
	m.pendingG = true

	m = updateModel(t, m, questionKey())
	if !m.help.visible {
		t.Fatal("expected help overlay to be visible")
	}
	if m.pendingG {
		t.Fatal("opening help should clear pending gg chord")
	}
}

func TestModelHelpOverlayHandlesDocumentedScrollKeys(t *testing.T) {
	m := setupModel()
	m.help.show(40, 6)
	m.focus = focusViewport

	for _, key := range []tea.KeyPressMsg{
		{Code: 'j'},
		{Code: 'k'},
		{Code: 'd', Mod: tea.ModCtrl},
		{Code: 'u', Mod: tea.ModCtrl},
		{Code: tea.KeyPgDown},
		{Code: tea.KeyPgUp},
		{Code: tea.KeyEnd},
		{Code: tea.KeyHome},
		{Code: 'G'},
		{Code: 'g'},
	} {
		m = updateModel(t, m, key)
		if !m.help.visible {
			t.Fatalf("help overlay should remain visible after %v", key)
		}
		if m.focus != focusViewport {
			t.Fatalf("help scroll key %v should not change focus", key)
		}
	}
}

func TestModelHelpOverlayKeepsStatusModeAndInputVisibleOnSmallTerminal(t *testing.T) {
	m := setupModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 12})
	m = updateModel(t, m, questionKey())

	view := m.View()
	if !strings.Contains(view.Content, "clnkr help") {
		t.Fatalf("help content should render, got %q", view.Content)
	}
	lines := strings.Split(view.Content, "\n")
	if len(lines) < 2 {
		t.Fatalf("view should include status and input lines, got %q", view.Content)
	}
	statusLine := lines[len(lines)-2]
	if !strings.Contains(statusLine, "HELP") {
		t.Fatalf("status mode should remain visible, status=%q view=%q", statusLine, view.Content)
	}
	if !strings.Contains(view.Content, iconPrompt) {
		t.Fatalf("input should remain visible, got %q", view.Content)
	}
	if got, max := strings.Count(view.Content, "\n")+1, 12; got > max {
		t.Fatalf("view line count = %d, want <= %d; view=%q", got, max, view.Content)
	}
}

func TestModelStatusModeReflectsInput(t *testing.T) {
	m := setupModel()
	m.running = false
	m.focus = focusInput

	view := m.View()
	if !strings.Contains(view.Content, "INPUT") {
		t.Fatalf("status should show INPUT mode, got %q", view.Content)
	}
}

func TestModelStatusModeReflectsScroll(t *testing.T) {
	m := setupModel()
	m.running = false
	m.focus = focusViewport

	view := m.View()
	if !strings.Contains(view.Content, "SCROLL") {
		t.Fatalf("status should show SCROLL mode, got %q", view.Content)
	}
}

func TestModelStatusModeReflectsHelpOverlay(t *testing.T) {
	m := setupModel()
	m.help.show(80, 20)

	view := m.View()
	if !strings.Contains(view.Content, "HELP") {
		t.Fatalf("status should show HELP mode, got %q", view.Content)
	}
}

func TestModelStatusModeReflectsDiffOverlay(t *testing.T) {
	m := setupModel()
	m.diff.visible = true

	view := m.View()
	if !strings.Contains(view.Content, "DIFF") {
		t.Fatalf("status should show DIFF mode, got %q", view.Content)
	}
}

func TestModelStatusModeReflectsReasoningOverlay(t *testing.T) {
	m := setupModel()
	m.reasoning.visible = true

	view := m.View()
	if !strings.Contains(view.Content, "REASONING") {
		t.Fatalf("status should show REASONING mode, got %q", view.Content)
	}
}

func TestModelStatusModeReflectsApproval(t *testing.T) {
	m := setupModel()
	m.awaitingApproval = true

	view := m.View()
	if !strings.Contains(view.Content, "APPROVAL") {
		t.Fatalf("status should show APPROVAL mode, got %q", view.Content)
	}
}

func TestModelStatusModeReflectsClarification(t *testing.T) {
	m := setupModel()
	m.awaitingClarification = true

	view := m.View()
	if !strings.Contains(view.Content, "CLARIFY") {
		t.Fatalf("status should show CLARIFY mode, got %q", view.Content)
	}
}

func TestModelStatusModeReflectsRunningWithoutPrompt(t *testing.T) {
	m := setupModel()
	m.running = true
	m.awaitingApproval = false
	m.awaitingClarification = false

	view := m.View()
	if !strings.Contains(view.Content, "RUNNING") {
		t.Fatalf("status should show RUNNING mode, got %q", view.Content)
	}
}

func TestModelAgentDone(t *testing.T) {
	m := setupModel()
	um := updateModel(t, m, agentDoneMsg{err: nil})

	if um.running {
		t.Error("running should be false after agentDoneMsg")
	}
	if um.agentErr != nil {
		t.Error("agentErr should be nil on success")
	}
}

func TestModelAgentDonePreservesError(t *testing.T) {
	m := setupModel()
	um := updateModel(t, m, agentDoneMsg{err: fmt.Errorf("connection lost")})

	if um.agentErr == nil {
		t.Error("agentErr should be preserved for exit code propagation")
	}
	if um.agentErr.Error() != "connection lost" {
		t.Errorf("expected %q, got %q", "connection lost", um.agentErr.Error())
	}
}

func TestModelAgentDoneCommitsStreamBuffer(t *testing.T) {
	m := setupModel()
	m = updateModel(t, m, eventMsg{event: clnkr.EventDebug{Message: "querying model..."}})

	um := updateModel(t, m, agentDoneMsg{err: fmt.Errorf("connection lost")})

	if um.chat.streaming {
		t.Error("streaming should be false after agentDoneMsg")
	}
	if um.chat.streamBuf.Len() != 0 {
		t.Error("streamBuf should be empty after agentDoneMsg commit")
	}
}

func TestModelAgentDoneClarificationRendersQuestion(t *testing.T) {
	m := setupModel()
	m.shared.agent = clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := m.shared.agent.AddMessages([]clnkr.Message{
		{Role: "assistant", Content: `{"type":"clarify","question":"Which interface should I use?"}`},
	}); err != nil {
		t.Fatalf("seed messages: %v", err)
	}

	um := updateModel(t, m, agentDoneMsg{err: clnkr.ErrClarificationNeeded})
	if !strings.Contains(um.chat.content.String(), "Which interface should I use?") {
		t.Fatalf("expected clarify question in chat, got: %q", um.chat.content.String())
	}
}

func TestModelAgentDoneClarificationNormalizesEscapedQuestion(t *testing.T) {
	m := setupModel()
	m.shared.agent = clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := m.shared.agent.AddMessages([]clnkr.Message{
		{Role: "assistant", Content: `{"type":"clarify","question":"Here are the skills I found:\\n- **citations-agent**: Verify claims.\\\\- **humanizer**: Sound more natural.\\nWhat would you like to work on?"}`},
	}); err != nil {
		t.Fatalf("seed messages: %v", err)
	}

	um := updateModel(t, m, agentDoneMsg{err: clnkr.ErrClarificationNeeded})
	content := um.chat.content.String()
	if strings.Contains(content, `\n-`) {
		t.Fatalf("clarify question should not render literal escaped newlines, got: %q", content)
	}
	if strings.Contains(content, `\- **humanizer**`) {
		t.Fatalf("clarify question should not render escaped list markers, got: %q", content)
	}
	if !strings.Contains(content, "- **citations-agent**: Verify claims.") {
		t.Fatalf("expected first bullet in chat, got: %q", content)
	}
	if !strings.Contains(content, "- **humanizer**: Sound more natural.") {
		t.Fatalf("expected second bullet in chat, got: %q", content)
	}
}

func TestModelWindowResize(t *testing.T) {
	m := setupModel()
	um := updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})

	if um.width != 120 || um.height != 40 {
		t.Errorf("expected 120x40, got %dx%d", um.width, um.height)
	}
}

func TestModelCtrlYOpensReasoningModal(t *testing.T) {
	m := setupModel()
	m.reasoningInfo.enabled = true
	m.reasoningInfo.latest = "checked parser -> protocol -> ui"

	m = updateModel(t, m, tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	if !m.reasoning.visible {
		t.Fatal("expected reasoning modal to be visible")
	}
	if !strings.Contains(m.reasoning.view(), "checked parser -> protocol -> ui") {
		t.Fatalf("expected reasoning modal content, got: %q", m.reasoning.view())
	}
}

func TestModelEscapeDismissesReasoningModal(t *testing.T) {
	m := setupModel()
	m.reasoningInfo.enabled = true
	m.reasoningInfo.latest = "checked parser -> protocol -> ui"
	m.reasoning.show(m.reasoningInfo.latest, 80, 20)
	m.focus = focusInput

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.reasoning.visible {
		t.Fatal("expected reasoning modal to be dismissed")
	}
	if m.focus != focusInput {
		t.Fatalf("expected focus to remain unchanged after dismiss, got %v", m.focus)
	}
}

func TestModelCtrlYWithoutReasoningDoesNotOpenModal(t *testing.T) {
	m := setupModel()
	m.reasoningInfo.enabled = true

	m = updateModel(t, m, tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	if m.reasoning.visible {
		t.Fatal("did not expect reasoning modal to open")
	}
	if !strings.Contains(m.chat.content.String(), "No reasoning trace available.") {
		t.Fatalf("expected no-reasoning note, got: %q", m.chat.content.String())
	}
}

func TestModelViewportScrollKeys(t *testing.T) {
	m := setupModel()
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	m = updateModel(t, m, tea.KeyPressMsg{Code: 'j'})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'k'})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'G'})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	_ = updateModel(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
}

func TestModelGGChord(t *testing.T) {
	m := setupModel()

	for i := range 50 {
		fmt.Fprintf(m.chat.content, "line %d\n", i)
	}
	m.chat.updateViewport()

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	// First g sets pending
	um := updateModel(t, m, tea.KeyPressMsg{Code: 'g'})
	if !um.pendingG {
		t.Error("first g should set pendingG=true")
	}

	// Second g within timeout triggers GotoTop
	um = updateModel(t, um, tea.KeyPressMsg{Code: 'g'})
	if um.pendingG {
		t.Error("second g should clear pendingG")
	}
}

func TestModelTwoPaneLayout(t *testing.T) {
	m := setupModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	view := m.View()
	// View should contain both chat viewport and input area
	if view.Content == "" {
		t.Error("view should not be empty")
	}
}

func TestModelCtrlCIdleQuits(t *testing.T) {
	m := setupModel()
	m.running = false

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Error("Ctrl-C when idle should produce a quit command")
	}
}

func TestModelCtrlCRunningCancels(t *testing.T) {
	cancelled := false
	m := setupModel()
	m.cancel = func() { cancelled = true }
	m.running = true

	um := updateModel(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !cancelled {
		t.Error("Ctrl-C when running should call cancel")
	}
	// Should NOT quit on first Ctrl-C while running
	if um.quitting {
		t.Error("single Ctrl-C while running should not quit")
	}
}

func TestModelDoubleCtrlCAlwaysQuits(t *testing.T) {
	m := setupModel()
	m.running = true

	// First Ctrl-C
	um := updateModel(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	// Second Ctrl-C rapidly
	_, cmd := um.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Error("double Ctrl-C should produce a quit command")
	}
}

func TestModelInputSubmitShowsInChat(t *testing.T) {
	m := setupModel()
	m.running = false
	m.shared.agent = nil // prevent actual agent run

	m.input.textarea.SetValue("hello agent")

	// We can't fully test startTask without an agent, but we can test
	// that the submit path extracts text correctly
	text := m.input.submit()
	if text != "hello agent" {
		t.Errorf("submit should return %q, got %q", "hello agent", text)
	}
}

func TestModelCtrlUScrollsFromInputMode(t *testing.T) {
	m := setupModel()
	m.focus = focusInput

	// Should not error — Ctrl+U scrolls viewport from input mode
	_ = updateModel(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
}

func TestModelCtrlDScrollsWhenInputEmpty(t *testing.T) {
	m := setupModel()
	m.focus = focusInput

	// With empty input, Ctrl+D should scroll viewport
	_ = updateModel(t, m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
}

func TestModelViewContainsStatusBar(t *testing.T) {
	m := setupModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	view := m.View()
	if !strings.Contains(view.Content, "test-model") {
		t.Error("view should contain model name from status bar")
	}
}

func TestModelViewRequestsTerminalFocusReports(t *testing.T) {
	m := setupModel()
	view := m.View()
	if !view.ReportFocus {
		t.Fatal("view should request terminal focus reports")
	}
}

func TestModelTerminalBlurDoesNotChangeAppFocusMode(t *testing.T) {
	m := setupModel()

	m = updateModel(t, m, tea.BlurMsg{})
	if m.focus != focusInput {
		t.Fatalf("terminal blur should preserve app focus mode, got %v", m.focus)
	}
	if m.status.focus != focusInput {
		t.Fatalf("terminal blur should preserve status focus, got %v", m.status.focus)
	}
	if !m.input.textarea.Focused() {
		t.Fatal("terminal blur should not switch the textarea out of input mode")
	}

	m = updateModel(t, m, tea.FocusMsg{})
	if m.focus != focusInput {
		t.Fatalf("terminal focus should preserve app focus mode, got %v", m.focus)
	}
	if m.status.focus != focusInput {
		t.Fatalf("terminal focus should preserve status focus, got %v", m.status.focus)
	}
	if !m.input.textarea.Focused() {
		t.Fatal("terminal focus should keep the textarea in input mode")
	}
}

func TestModelEventResponseUpdatesStatus(t *testing.T) {
	m := setupModel()
	m = updateModel(t, m, eventMsg{event: clnkr.EventResponse{
		Turn:  &clnkr.DoneTurn{Summary: "hi"},
		Usage: clnkr.Usage{InputTokens: 500, OutputTokens: 200},
	}})

	if m.status.inputTokens != 500 {
		t.Errorf("inputTokens = %d, want 500", m.status.inputTokens)
	}
	if m.status.outputTokens != 200 {
		t.Errorf("outputTokens = %d, want 200", m.status.outputTokens)
	}
}

func TestLatestClarifyQuestion(t *testing.T) {
	msgs := []clnkr.Message{
		{Role: "assistant", Content: `{"type":"act","bash":{"commands":[{"command":"ls","workdir":null}]}}`},
		{Role: "assistant", Content: `{"type":"clarify","question":"Bind to 0.0.0.0?"}`},
		{Role: "user", Content: "yes"},
	}

	question, ok := latestClarifyQuestion(msgs)
	if !ok {
		t.Fatal("expected to find clarify question")
	}
	if question != "Bind to 0.0.0.0?" {
		t.Fatalf("question = %q, want %q", question, "Bind to 0.0.0.0?")
	}
}

func TestModelCommandDoneIncrementsStep(t *testing.T) {
	m := setupModel()
	m = updateModel(t, m, eventMsg{event: clnkr.EventCommandStart{Command: "ls", Dir: "."}})
	m = updateModel(t, m, eventMsg{event: clnkr.EventCommandDone{Command: "ls", Stdout: "ok", ExitCode: 0, Err: nil}})

	if m.status.stepCount != 1 {
		t.Errorf("stepCount = %d, want 1", m.status.stepCount)
	}
}

func TestModelFocusChangeUpdatesStatus(t *testing.T) {
	m := setupModel()
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.status.focus != focusViewport {
		t.Error("status focus should be viewport after Esc")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: 'i'})
	if m.status.focus != focusInput {
		t.Error("status focus should be input after i")
	}
}

func TestModelAgentDoneStopsStatusTimer(t *testing.T) {
	m := setupModel()
	m.status.startRun()
	m = updateModel(t, m, agentDoneMsg{err: nil})

	if m.status.running {
		t.Error("status.running should be false after agentDoneMsg")
	}
}

func TestModelDiffOverlayToggle(t *testing.T) {
	m := setupModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.shared.cwd = "/tmp"

	// Switch to viewport mode first
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	// Ctrl+F should toggle diff overlay
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if !m.diff.visible {
		t.Error("diff overlay should be visible after Ctrl+F")
	}

	// Esc should dismiss
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.diff.visible {
		t.Error("diff overlay should be hidden after Esc")
	}
}

func TestModelDiffOverlayUsesAgentCwd(t *testing.T) {
	repo := t.TempDir()
	cmd := exec.Command("git", "init", repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	file := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(file, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write tracked file: %v", err)
	}

	m := setupModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.shared.cwd = "/definitely/wrong"
	m.shared.agent = clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, repo)
	m.files.files = []string{"tracked.txt"}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})

	if !m.diff.visible {
		t.Fatal("diff overlay should be visible after Ctrl+F")
	}
	if strings.Contains(m.diff.content, "git diff error") {
		t.Fatalf("diff should use agent cwd, got %q", m.diff.content)
	}
}

func TestModelDiffOverlayRebasesFeedbackPathsAcrossCwdChanges(t *testing.T) {
	repo := t.TempDir()
	cmd := exec.Command("git", "init", repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", repo, "config", "user.name", "clnkr test").CombinedOutput(); err != nil {
		t.Fatalf("git config user.name: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", repo, "config", "user.email", "clnkr@example.com").CombinedOutput(); err != nil {
		t.Fatalf("git config user.email: %v\n%s", err, out)
	}
	dir1 := filepath.Join(repo, "dir1")
	dir2 := filepath.Join(repo, "dir2")
	if err := os.MkdirAll(dir1, 0o755); err != nil {
		t.Fatalf("mkdir dir1: %v", err)
	}
	if err := os.MkdirAll(dir2, 0o755); err != nil {
		t.Fatalf("mkdir dir2: %v", err)
	}
	file := filepath.Join(dir1, "local.txt")
	if err := os.WriteFile(file, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write local.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", "dir1/local.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", repo, "commit", "-qm", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	if err := os.WriteFile(file, []byte("after\n"), 0o644); err != nil {
		t.Fatalf("rewrite local.txt: %v", err)
	}

	m := setupModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.shared.agent = clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, dir1)
	m = updateModel(t, m, eventMsg{event: clnkr.EventCommandDone{
		Command:  "printf 'after\\n' > local.txt",
		ExitCode: 0,
		Feedback: clnkr.CommandFeedback{ChangedFiles: []string{"local.txt"}},
	}})
	m.shared.agent = clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, dir2)

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if !m.diff.visible {
		t.Fatal("diff overlay should be visible after Ctrl+F")
	}
	if strings.Contains(m.diff.content, "git diff error") {
		t.Fatalf("diff should not fail after cwd change, got %q", m.diff.content)
	}
	if strings.Contains(m.diff.content, "(no uncommitted changes") {
		t.Fatalf("diff should include the tracked change after cwd rebasing, got %q", m.diff.content)
	}
	if !strings.Contains(m.diff.content, "local.txt") {
		t.Fatalf("diff should mention local.txt, got %q", m.diff.content)
	}
}

func TestModelDiffOverlayBlocksOtherKeys(t *testing.T) {
	m := setupModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.shared.cwd = "/tmp"
	m.diff.visible = true

	// 'i' should NOT switch focus when diff is visible
	prevFocus := m.focus
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'i'})
	if m.focus != prevFocus {
		t.Error("keys should not change focus while diff overlay is visible")
	}
}

func TestModelTracksFilesFromCommands(t *testing.T) {
	m := setupModel()
	m = updateModel(t, m, eventMsg{event: clnkr.EventCommandStart{Command: "touch newfile.go", Dir: "."}})
	m = updateModel(t, m, eventMsg{event: clnkr.EventCommandDone{Command: "touch newfile.go", ExitCode: 0, Err: nil}})

	if len(m.files.files) != 1 {
		t.Errorf("expected 1 tracked file, got %d: %v", len(m.files.files), m.files.files)
	}
	if m.files.files[0] != "newfile.go" {
		t.Errorf("expected newfile.go, got %s", m.files.files[0])
	}
}

func TestModelPrefersFeedbackFilesOverHeuristics(t *testing.T) {
	m := setupModel()
	m.chat.lastCmd = "touch wrong.txt"
	m = updateModel(t, m, eventMsg{event: clnkr.EventCommandDone{
		Command:  "touch wrong.txt",
		ExitCode: 0,
		Feedback: clnkr.CommandFeedback{ChangedFiles: []string{"right.txt"}},
	}})

	if len(m.files.files) != 1 || m.files.files[0] != "right.txt" {
		t.Fatalf("tracked files = %#v, want right.txt from feedback", m.files.files)
	}
}

func TestModelApprovalReplyBecomesUserGuidance(t *testing.T) {
	m := setupModel()
	m.pendingAct = actTurn("rm important.txt")
	m.chat.setProposedCommand(formatActProposal([]clnkr.BashAction{{Command: "rm important.txt"}}))
	m.awaitingApproval = true
	m.input.textarea.SetValue("list files instead")
	m.running = true
	m.shared.agent = clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	m.runCtx = context.Background()

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.awaitingApproval {
		t.Fatal("guidance should clear awaitingApproval")
	}
	if m.awaitingClarification {
		t.Fatal("guidance should not enter clarification mode")
	}
	if m.pendingAct != nil {
		t.Fatal("guidance should clear pendingAct")
	}
	if m.chat.pendingCmd != "" {
		t.Fatal("guidance should clear pending command banner")
	}
	if !strings.Contains(m.chat.content.String(), "list files instead") {
		t.Fatal("guidance should be rendered into chat")
	}
}

func TestModelClarificationSubmitAppendsUserMessage(t *testing.T) {
	m := setupModel()
	m.awaitingClarification = true
	m.clarificationPrompt = "What should the agent do instead?"
	m.input.textarea.SetValue("list files instead")
	m.running = true
	m.shared.agent = clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	m.runCtx = context.Background()

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.awaitingClarification {
		t.Fatal("submit should clear awaitingClarification")
	}
	if !strings.Contains(m.chat.content.String(), "list files instead") {
		t.Fatal("clarification should be rendered into chat")
	}
}

func TestModelApproveProposalStartsExecution(t *testing.T) {
	m := setupModel()
	m.pendingAct = actTurn("echo hi")
	m.awaitingApproval = true
	m.input.textarea.SetValue("y")
	m.running = true
	m.shared.agent = clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	m.cancel = func() {}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if cmd == nil {
		t.Fatal("approve should start an execute command")
	}
	if um.awaitingApproval {
		t.Fatal("approve should clear awaitingApproval")
	}
}

func TestModelEnterWhileRunningWithoutPendingPromptPreservesDraftInput(t *testing.T) {
	m := setupModel()
	m.running = true
	m.input.textarea.SetValue("draft clarification")

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.input.textarea.Value(); got != "draft clarification" {
		t.Fatalf("input should be preserved while running, got %q", got)
	}
}

func TestModelApprovalHintAppearsWhenCommandIsProposed(t *testing.T) {
	m := setupModel()

	updated, cmd := m.Update(stepDoneMsg{result: clnkr.StepResult{Turn: actTurn("echo hi")}})
	if cmd == nil {
		t.Fatal("proposal should focus the input")
	}
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if !um.awaitingApproval {
		t.Fatal("proposal should enter approval mode")
	}
	if got := um.input.textarea.Placeholder; got != approvalPromptText {
		t.Fatalf("placeholder = %q", got)
	}
	if !strings.Contains(um.chat.content.String(), approvalPromptText) {
		t.Fatal("chat should explain how to approve")
	}
}

func TestModelApprovalHintShowsWorkdir(t *testing.T) {
	m := setupModel()

	updated, _ := m.Update(stepDoneMsg{result: clnkr.StepResult{Turn: &clnkr.ActTurn{
		Bash: clnkr.BashBatch{Commands: []clnkr.BashAction{{Command: "echo hi", Workdir: "subdir"}}},
	}}})
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if !strings.Contains(um.chat.pendingCmd, "1. echo hi in subdir") {
		t.Fatalf("pending command should show workdir, got %q", um.chat.pendingCmd)
	}
}

func TestModelExecuteDoneCountsBatchCommands(t *testing.T) {
	m := setupModel()
	m.running = true
	m.pendingAct = actTurn("echo hi")
	m.runCtx = context.Background()
	m.shared.agent = clnkr.NewAgent(&fakeModel{responses: []clnkr.Response{
		mustResponse(`{"type":"done","summary":"step limit summary"}`),
	}}, &fakeExecutor{}, "/tmp")
	m.shared.agent.MaxSteps = 2

	updated, cmd := m.handleExecuteDone(executeDoneMsg{execCount: 2})
	if cmd == nil {
		t.Fatal("batch execution should trigger the final summary when max steps are reached")
	}
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if um.executedSteps != 2 {
		t.Fatalf("executedSteps = %d, want 2", um.executedSteps)
	}

	msg := cmd()
	done, ok := msg.(agentDoneMsg)
	if !ok {
		t.Fatalf("expected agentDoneMsg, got %T", msg)
	}
	if done.err != nil {
		t.Fatalf("agentDoneMsg.err = %v, want nil", done.err)
	}
}

func TestModelApprovalRequiresEnter(t *testing.T) {
	m := setupModel()
	m.pendingAct = actTurn("echo hi")
	m.awaitingApproval = true
	m.input.textarea.SetValue("y")
	m.running = true

	m = updateModel(t, m, tea.KeyPressMsg{Code: 'y'})
	if !m.awaitingApproval {
		t.Fatal("typing y should not approve before Enter")
	}
	if got := m.input.textarea.Value(); got != "y" {
		t.Fatalf("input should remain pending, got %q", got)
	}
}

func TestModelCtrlYApprovesPendingCommand(t *testing.T) {
	m := setupModel()
	m.pendingAct = actTurn("echo hi")
	m.awaitingApproval = true
	m.running = true
	m.shared.agent = clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if cmd == nil {
		t.Fatal("Ctrl+Y should start an execute command")
	}
	if um.awaitingApproval {
		t.Fatal("Ctrl+Y should clear awaitingApproval")
	}
}

func TestModelSingleTaskDoneQuitsAfterBridgeDrains(t *testing.T) {
	m := setupModel()
	m.exitOnRunFinish = true

	updated, cmd := m.Update(stepDoneMsg{result: clnkr.StepResult{Turn: &clnkr.DoneTurn{Summary: "done"}}})
	if cmd != nil {
		t.Fatal("step completion should wait for the bridge to drain before quitting")
	}
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if um.running {
		t.Fatal("single-task done should stop the run")
	}
	_, quitCmd := um.Update(bridgeDrainedMsg{})
	if quitCmd == nil {
		t.Fatal("bridge drain should quit the TUI for single-task runs")
	}
}

func TestModelSingleTaskAgentDoneQuitsIfBridgeDrainedFirst(t *testing.T) {
	m := setupModel()
	m.exitOnRunFinish = true

	updated, cmd := m.Update(bridgeDrainedMsg{})
	if cmd != nil {
		t.Fatal("bridge drain before completion should not quit immediately")
	}
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if !um.bridgeDrained {
		t.Fatal("bridge drain message should mark bridgeDrained")
	}

	_, quitCmd := um.Update(agentDoneMsg{err: nil})
	if quitCmd == nil {
		t.Fatal("agentDone should quit if the bridge already drained")
	}
}

func TestModelSingleTaskDoneKeepsBridgeAliveForBufferedResponse(t *testing.T) {
	m := setupModel()
	m.exitOnRunFinish = true
	m.eventCh = make(chan clnkr.Event)
	m.closeEventChOnFinish = true

	updated, cmd := m.Update(stepDoneMsg{result: clnkr.StepResult{Turn: &clnkr.DoneTurn{Summary: "done"}}})
	if cmd != nil {
		t.Fatal("step completion should not quit before buffered events render")
	}

	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if um.eventCh == nil {
		t.Fatal("finishRun should keep the closed event channel available until the bridge drains")
	}

	updated, cmd = um.Update(eventMsg{event: clnkr.EventResponse{
		Turn: &clnkr.DoneTurn{Summary: "done"},
	}})
	if cmd == nil {
		t.Fatal("buffered response should resubscribe to the bridge")
	}

	um, ok = updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}

	_, quitCmd := um.Update(bridgeDrainedMsg{})
	if quitCmd == nil {
		t.Fatal("bridge drain should quit after the buffered response is rendered")
	}
}

type recordingCompactor struct {
	summary string
	err     error

	started chan struct{}
	release chan struct{}

	mu       sync.Mutex
	calls    int
	messages []clnkr.Message
}

func (c *recordingCompactor) Summarize(_ context.Context, messages []clnkr.Message) (string, error) {
	c.mu.Lock()
	c.calls++
	c.messages = append([]clnkr.Message(nil), messages...)
	started := c.started
	release := c.release
	summary := c.summary
	err := c.err
	c.mu.Unlock()

	if started != nil {
		close(started)
	}
	if release != nil {
		<-release
	}
	return summary, err
}

func (c *recordingCompactor) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func runReturnedCmdAsync(t *testing.T, cmd tea.Cmd) <-chan tea.Msg {
	t.Helper()
	msgCh := make(chan tea.Msg, 8)
	go func() {
		defer close(msgCh)
		dispatchCmdResult(msgCh, cmd())
	}()
	return msgCh
}

func dispatchCmdResult(msgCh chan<- tea.Msg, msg tea.Msg) {
	switch msg := msg.(type) {
	case nil:
		return
	case tea.BatchMsg:
		var wg sync.WaitGroup
		for _, sub := range msg {
			if sub == nil {
				continue
			}
			wg.Add(1)
			go func(sub tea.Cmd) {
				defer wg.Done()
				dispatchCmdResult(msgCh, sub())
			}(sub)
		}
		wg.Wait()
	default:
		msgCh <- msg
	}
}

func waitForCompactDone(t *testing.T, msgCh <-chan tea.Msg) compactDoneMsg {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				t.Fatal("command channel closed before compactDoneMsg")
			}
			done, ok := msg.(compactDoneMsg)
			if ok {
				return done
			}
		case <-timeout:
			t.Fatal("timed out waiting for compactDoneMsg")
		}
	}
}

func TestCompactCommandInterceptsIdleInput(t *testing.T) {
	m := setupModel()
	m.running = false
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("seed messages: %v", err)
	}
	m.shared.agent = agent

	compactor := &recordingCompactor{summary: "Older work summarized."}
	var instructions []string
	m.shared.compactorFactory = func(input string) clnkr.Compactor {
		instructions = append(instructions, input)
		return compactor
	}
	m.input.textarea.SetValue("/compact focus on failing tests")

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("compact command should start async work")
	}
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if !um.running {
		t.Fatal("compact command should mark the model as running")
	}
	if !um.status.running {
		t.Fatal("compact command should start the status timer")
	}
	if len(instructions) != 1 || instructions[0] != "focus on failing tests" {
		t.Fatalf("factory instructions = %#v", instructions)
	}
	if hasUserMessage(agent.Messages(), "/compact focus on failing tests") {
		t.Fatalf("literal compact command leaked into transcript: %#v", agent.Messages())
	}
	if strings.Contains(um.chat.content.String(), "Session compacted:") {
		t.Fatal("host feedback should not be appended before completion")
	}

	um.input.textarea.SetValue("/compact again")
	updated, secondCmd := um.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if secondCmd != nil {
		t.Fatal("second submission should be rejected while compaction is busy")
	}
	um, ok = updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if got := um.input.textarea.Value(); got != "/compact again" {
		t.Fatalf("busy submission should preserve draft, got %q", got)
	}
	if len(instructions) != 1 {
		t.Fatalf("factory called %d times, want 1", len(instructions))
	}

	done := waitForCompactDone(t, runReturnedCmdAsync(t, cmd))
	if done.err != nil {
		t.Fatalf("compactDoneMsg.err = %v", done.err)
	}
}

func TestCompactCommandAppendsHostFeedback(t *testing.T) {
	m := setupModel()
	m.running = false
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("seed messages: %v", err)
	}
	m.shared.agent = agent
	compactor := &recordingCompactor{summary: "Older work summarized."}
	m.shared.compactorFactory = func(string) clnkr.Compactor { return compactor }
	m.input.textarea.SetValue("/compact focus on failing tests")

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("compact command should start async work")
	}
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}

	doneMsg := waitForCompactDone(t, runReturnedCmdAsync(t, cmd))
	updated, followCmd := um.Update(doneMsg)
	if followCmd != nil {
		t.Fatal("compact completion should not schedule another command")
	}
	um, ok = updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if um.running {
		t.Fatal("compact completion should clear running state")
	}
	if um.status.running {
		t.Fatal("compact completion should stop the status timer")
	}
	if !strings.Contains(um.chat.content.String(), "Session compacted: summarized 2 messages, kept 4.") {
		t.Fatalf("chat = %q, want host feedback", um.chat.content.String())
	}
	msgs := agent.Messages()
	if len(msgs) == 0 || !strings.HasPrefix(msgs[0].Content, "[compact]\n") {
		t.Fatalf("expected compact block at start, got %#v", msgs)
	}
	if hasUserMessage(msgs, "/compact focus on failing tests") {
		t.Fatalf("literal compact command leaked into transcript: %#v", msgs)
	}
}

func TestCompactCommandRunsThroughAsyncCmd(t *testing.T) {
	m := setupModel()
	m.running = false
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("seed messages: %v", err)
	}
	m.shared.agent = agent

	compactor := &recordingCompactor{
		summary: "Older work summarized.",
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	m.shared.compactorFactory = func(string) clnkr.Compactor { return compactor }
	m.input.textarea.SetValue("/compact")

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("compact command should start async work")
	}
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if !um.running {
		t.Fatal("compact command should leave model busy until completion")
	}

	msgCh := runReturnedCmdAsync(t, cmd)

	<-compactor.started
	if compactor.callCount() != 1 {
		t.Fatalf("compactor calls = %d, want 1", compactor.callCount())
	}
	if msgs := agent.Messages(); len(msgs) > 0 && strings.HasPrefix(msgs[0].Content, "[compact]\n") {
		t.Fatalf("transcript should not rewrite before async completion, got %#v", msgs)
	}

	um.input.textarea.SetValue("follow-up task")
	updated, blockedCmd := um.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if blockedCmd != nil {
		t.Fatal("busy model should reject another submission while compaction is running")
	}
	um, ok = updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if got := um.input.textarea.Value(); got != "follow-up task" {
		t.Fatalf("busy submission should preserve draft, got %q", got)
	}

	close(compactor.release)
	doneMsg := waitForCompactDone(t, msgCh)
	if doneMsg.err != nil {
		t.Fatalf("compactDoneMsg.err = %v", doneMsg.err)
	}
}

func TestCompactCommandRepeatedCompactionStaysStable(t *testing.T) {
	m := setupModel()
	m.running = false
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(alreadyCompactedMessages()); err != nil {
		t.Fatalf("seed messages: %v", err)
	}
	m.shared.agent = agent
	compactor := &recordingCompactor{summary: "Newer summary."}
	m.shared.compactorFactory = func(string) clnkr.Compactor { return compactor }
	m.input.textarea.SetValue("/compact")

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("compact command should start async work")
	}
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}

	doneMsg := waitForCompactDone(t, runReturnedCmdAsync(t, cmd))
	updated, followCmd := um.Update(doneMsg)
	if followCmd != nil {
		t.Fatal("compact completion should not schedule another command")
	}
	um, ok = updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}

	msgs := agent.Messages()
	if countCompactMessages(msgs) != 1 {
		t.Fatalf("compact block count = %d, want 1; msgs=%#v", countCompactMessages(msgs), msgs)
	}
	if !strings.Contains(um.chat.content.String(), "Session compacted: summarized 3 messages, kept 4.") {
		t.Fatalf("chat = %q, want compact completion feedback", um.chat.content.String())
	}
}

func TestCompactCommandNotInterceptedDuringApproval(t *testing.T) {
	m := setupModel()
	m.running = true
	m.awaitingApproval = true
	m.pendingAct = actTurn("echo hi")
	m.chat.setProposedCommand(formatActProposal([]clnkr.BashAction{{Command: "echo hi"}}))
	m.runCtx = context.Background()

	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	m.shared.agent = agent

	factoryCalls := 0
	m.shared.compactorFactory = func(string) clnkr.Compactor {
		factoryCalls++
		return &recordingCompactor{summary: "should not run"}
	}
	m.input.textarea.SetValue("/compact focus on tests")

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("approval guidance should continue through the normal async path")
	}
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if um.awaitingApproval {
		t.Fatal("approval submission should clear awaitingApproval")
	}
	if factoryCalls != 0 {
		t.Fatalf("compactor factory called %d times during approval", factoryCalls)
	}
	if !hasUserMessage(agent.Messages(), "/compact focus on tests") {
		t.Fatalf("approval should append the literal reply, got %#v", agent.Messages())
	}
}

func TestCompactCommandNotInterceptedDuringClarification(t *testing.T) {
	m := setupModel()
	m.running = true
	m.awaitingClarification = true
	m.clarificationPrompt = "Need more detail"
	m.runCtx = context.Background()

	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	m.shared.agent = agent

	factoryCalls := 0
	m.shared.compactorFactory = func(string) clnkr.Compactor {
		factoryCalls++
		return &recordingCompactor{summary: "should not run"}
	}
	m.input.textarea.SetValue("/compact focus on tests")

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("clarification reply should continue through the normal async path")
	}
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if um.awaitingClarification {
		t.Fatal("clarification submission should clear awaitingClarification")
	}
	if factoryCalls != 0 {
		t.Fatalf("compactor factory called %d times during clarification", factoryCalls)
	}
	if !hasUserMessage(agent.Messages(), "/compact focus on tests") {
		t.Fatalf("clarification should append the literal reply, got %#v", agent.Messages())
	}
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

func alreadyCompactedMessages() []clnkr.Message {
	return []clnkr.Message{
		{
			Role:    "user",
			Content: `[compact]` + "\n" + `{"source":"clnkr","kind":"compact","summary":"Older work summarized."}` + "\n" + `[/compact]`,
		},
		{Role: "user", Content: "third task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
		{Role: "user", Content: "fourth task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done fourth"}`},
		{Role: "user", Content: "fifth task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done fifth"}`},
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

func countCompactMessages(msgs []clnkr.Message) int {
	count := 0
	for _, msg := range msgs {
		if msg.Role == "user" && strings.HasPrefix(msg.Content, "[compact]\n") {
			count++
		}
	}
	return count
}

type stubDelegateRunner struct {
	result  delegateResult
	err     error
	got     []delegateRequest
	start   chan struct{}
	release chan struct{}
}

func (r *stubDelegateRunner) Run(_ context.Context, req delegateRequest) (delegateResult, error) {
	r.got = append(r.got, req)
	if r.start != nil {
		close(r.start)
	}
	if r.release != nil {
		<-r.release
	}
	if r.err != nil {
		return delegateResult{}, r.err
	}
	return r.result, nil
}

func waitForDelegateDone(t *testing.T, msgCh <-chan tea.Msg) delegateDoneMsg {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				t.Fatal("command channel closed before delegateDoneMsg")
			}
			done, ok := msg.(delegateDoneMsg)
			if ok {
				return done
			}
		case <-timeout:
			t.Fatal("timed out waiting for delegateDoneMsg")
		}
	}
}

func TestDelegateCommandInterceptsIdleInput(t *testing.T) {
	m := setupModel()
	m.running = false
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("seed messages: %v", err)
	}
	m.shared.agent = agent

	runner := &stubDelegateRunner{result: delegateResult{Summary: "Found test patterns."}}
	m.shared.delegateRunner = runner
	m.input.textarea.SetValue("/delegate inspect compaction tests")

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("delegate command should start async work")
	}
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if !um.running {
		t.Fatal("delegate command should mark model as running")
	}
	if hasUserMessage(agent.Messages(), "/delegate inspect compaction tests") {
		t.Fatalf("literal delegate command leaked into transcript: %#v", agent.Messages())
	}

	done := waitForDelegateDone(t, runReturnedCmdAsync(t, cmd))
	if done.err != nil {
		t.Fatalf("delegateDoneMsg.err = %v", done.err)
	}
	if len(runner.got) != 1 || runner.got[0].Task != "inspect compaction tests" {
		t.Fatalf("runner requests = %#v", runner.got)
	}
}

func TestDelegateCommandAppendsHostFeedbackAndArtifact(t *testing.T) {
	m := setupModel()
	m.running = false
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("seed messages: %v", err)
	}
	m.shared.agent = agent
	m.shared.delegateRunner = &stubDelegateRunner{result: delegateResult{Summary: "Found test patterns."}}
	m.input.textarea.SetValue("/delegate inspect compaction tests")

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("delegate command should start async work")
	}
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}

	doneMsg := waitForDelegateDone(t, runReturnedCmdAsync(t, cmd))
	updated, followCmd := um.Update(doneMsg)
	if followCmd != nil {
		t.Fatal("delegate completion should not schedule another command")
	}
	um, ok = updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if um.running {
		t.Fatal("delegate completion should clear running state")
	}
	if !strings.Contains(um.chat.content.String(), "Delegation complete: Found test patterns.") {
		t.Fatalf("chat = %q, want delegate host feedback", um.chat.content.String())
	}
	msgs := agent.Messages()
	if len(msgs) == 0 || !strings.HasPrefix(msgs[len(msgs)-1].Content, "[delegate]\n") {
		t.Fatalf("expected delegate artifact at end, got %#v", msgs)
	}
	if hasUserMessage(msgs, "/delegate inspect compaction tests") {
		t.Fatalf("literal delegate command leaked into transcript: %#v", msgs)
	}
}

func TestDelegateCommandRunsThroughAsyncCmd(t *testing.T) {
	m := setupModel()
	m.running = false
	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	if err := agent.AddMessages(compactableMessages()); err != nil {
		t.Fatalf("seed messages: %v", err)
	}
	m.shared.agent = agent
	runner := &stubDelegateRunner{
		result:  delegateResult{Summary: "Found test patterns."},
		start:   make(chan struct{}),
		release: make(chan struct{}),
	}
	m.shared.delegateRunner = runner
	m.input.textarea.SetValue("/delegate inspect compaction tests")

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("delegate command should start async work")
	}
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if !um.running {
		t.Fatal("delegate command should leave model busy until completion")
	}

	msgCh := runReturnedCmdAsync(t, cmd)
	<-runner.start
	if len(runner.got) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(runner.got))
	}

	um.input.textarea.SetValue("follow-up task")
	updated, blockedCmd := um.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if blockedCmd != nil {
		t.Fatal("busy model should reject another submission while delegation is running")
	}
	um, ok = updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if got := um.input.textarea.Value(); got != "follow-up task" {
		t.Fatalf("busy submission should preserve draft, got %q", got)
	}

	close(runner.release)
	doneMsg := waitForDelegateDone(t, msgCh)
	if doneMsg.err != nil {
		t.Fatalf("delegateDoneMsg.err = %v", doneMsg.err)
	}
}

func TestDelegateCommandNotInterceptedDuringApproval(t *testing.T) {
	m := setupModel()
	m.running = true
	m.awaitingApproval = true
	m.pendingAct = actTurn("echo hi")
	m.chat.setProposedCommand(formatActProposal([]clnkr.BashAction{{Command: "echo hi"}}))
	m.runCtx = context.Background()

	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	m.shared.agent = agent
	m.shared.delegateRunner = &stubDelegateRunner{result: delegateResult{Summary: "should not run"}}
	m.input.textarea.SetValue("/delegate inspect compaction tests")

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("approval guidance should continue through the normal async path")
	}
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if um.awaitingApproval {
		t.Fatal("approval submission should clear awaitingApproval")
	}
	if !hasUserMessage(agent.Messages(), "/delegate inspect compaction tests") {
		t.Fatalf("approval should append the literal reply, got %#v", agent.Messages())
	}
}

func TestDelegateCommandNotInterceptedDuringClarification(t *testing.T) {
	m := setupModel()
	m.running = true
	m.awaitingClarification = true
	m.clarificationPrompt = "Need more detail"
	m.runCtx = context.Background()

	agent := clnkr.NewAgent(&fakeModel{}, &fakeExecutor{}, "/tmp")
	m.shared.agent = agent
	m.shared.delegateRunner = &stubDelegateRunner{result: delegateResult{Summary: "should not run"}}
	m.input.textarea.SetValue("/delegate inspect compaction tests")

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("clarification reply should continue through the normal async path")
	}
	um, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model, got %T", updated)
	}
	if um.awaitingClarification {
		t.Fatal("clarification submission should clear awaitingClarification")
	}
	if !hasUserMessage(agent.Messages(), "/delegate inspect compaction tests") {
		t.Fatalf("clarification should append the literal reply, got %#v", agent.Messages())
	}
}
