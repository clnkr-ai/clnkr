package main

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	hew "github.com/cosgroveb/hew"
)

func setupModel() model {
	s := defaultStyles(true)
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
	m = updateModel(t, m, eventMsg{event: hew.EventDebug{Message: "querying model..."}})

	msg := eventMsg{event: hew.EventResponse{
		Message: hew.Message{Role: "assistant", Content: "full response"},
		Usage:   hew.Usage{InputTokens: 100, OutputTokens: 50},
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
	m = updateModel(t, m, eventMsg{event: hew.EventDebug{Message: "querying model..."}})

	um := updateModel(t, m, agentDoneMsg{err: fmt.Errorf("connection lost")})

	if um.chat.streaming {
		t.Error("streaming should be false after agentDoneMsg")
	}
	if um.chat.streamBuf.Len() != 0 {
		t.Error("streamBuf should be empty after agentDoneMsg commit")
	}
}

func TestModelWindowResize(t *testing.T) {
	m := setupModel()
	um := updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})

	if um.width != 120 || um.height != 40 {
		t.Errorf("expected 120x40, got %dx%d", um.width, um.height)
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

func TestModelEventResponseUpdatesStatus(t *testing.T) {
	m := setupModel()
	m = updateModel(t, m, eventMsg{event: hew.EventResponse{
		Message: hew.Message{Role: "assistant", Content: "hi"},
		Usage:   hew.Usage{InputTokens: 500, OutputTokens: 200},
	}})

	if m.status.inputTokens != 500 {
		t.Errorf("inputTokens = %d, want 500", m.status.inputTokens)
	}
	if m.status.outputTokens != 200 {
		t.Errorf("outputTokens = %d, want 200", m.status.outputTokens)
	}
}

func TestModelCommandDoneIncrementsStep(t *testing.T) {
	m := setupModel()
	m = updateModel(t, m, eventMsg{event: hew.EventCommandStart{Command: "ls", Dir: "."}})
	m = updateModel(t, m, eventMsg{event: hew.EventCommandDone{Command: "ls", Stdout: "ok", ExitCode: 0, Err: nil}})

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
	m = updateModel(t, m, eventMsg{event: hew.EventCommandStart{Command: "touch newfile.go", Dir: "."}})
	m = updateModel(t, m, eventMsg{event: hew.EventCommandDone{Command: "touch newfile.go", ExitCode: 0, Err: nil}})

	if len(m.files.files) != 1 {
		t.Errorf("expected 1 tracked file, got %d: %v", len(m.files.files), m.files.files)
	}
	if m.files.files[0] != "newfile.go" {
		t.Errorf("expected newfile.go, got %s", m.files.files[0])
	}
}
