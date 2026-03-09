package main

import (
	"testing"
)

func TestInputSubmitReturnsTextAndClears(t *testing.T) {
	s := defaultStyles(true)
	inp := newInputModel(80, &s.Input)
	inp.textarea.SetValue("hello world")

	got := inp.submit()
	if got != "hello world" {
		t.Errorf("submit() = %q, want %q", got, "hello world")
	}
	if inp.textarea.Value() != "" {
		t.Errorf("textarea should be empty after submit, got %q", inp.textarea.Value())
	}
}

func TestInputSubmitEmptyReturnsEmpty(t *testing.T) {
	s := defaultStyles(true)
	inp := newInputModel(80, &s.Input)

	got := inp.submit()
	if got != "" {
		t.Errorf("submit() on empty input = %q, want %q", got, "")
	}
}

func TestInputSubmitPushesHistory(t *testing.T) {
	s := defaultStyles(true)
	inp := newInputModel(80, &s.Input)

	inp.textarea.SetValue("first")
	inp.submit()
	inp.textarea.SetValue("second")
	inp.submit()

	// Up should recall "second" (most recent)
	inp.historyUp()
	if inp.textarea.Value() != "second" {
		t.Errorf("first up = %q, want %q", inp.textarea.Value(), "second")
	}

	// Another up should recall "first"
	inp.historyUp()
	if inp.textarea.Value() != "first" {
		t.Errorf("second up = %q, want %q", inp.textarea.Value(), "first")
	}

	// Up at top stays at top
	inp.historyUp()
	if inp.textarea.Value() != "first" {
		t.Errorf("up at top = %q, want %q", inp.textarea.Value(), "first")
	}
}

func TestInputHistoryDown(t *testing.T) {
	s := defaultStyles(true)
	inp := newInputModel(80, &s.Input)

	inp.textarea.SetValue("first")
	inp.submit()
	inp.textarea.SetValue("second")
	inp.submit()

	// Navigate up to "first"
	inp.historyUp()
	inp.historyUp()
	if inp.textarea.Value() != "first" {
		t.Fatalf("expected %q, got %q", "first", inp.textarea.Value())
	}

	// Down goes to "second"
	inp.historyDown()
	if inp.textarea.Value() != "second" {
		t.Errorf("down from first = %q, want %q", inp.textarea.Value(), "second")
	}

	// Down past end clears to empty
	inp.historyDown()
	if inp.textarea.Value() != "" {
		t.Errorf("down past end = %q, want empty", inp.textarea.Value())
	}
}

func TestInputHistoryUpOnEmptyDoesNothing(t *testing.T) {
	s := defaultStyles(true)
	inp := newInputModel(80, &s.Input)

	// No history — up should do nothing
	inp.historyUp()
	if inp.textarea.Value() != "" {
		t.Errorf("up with no history = %q, want empty", inp.textarea.Value())
	}
}

func TestInputHistoryCapacityBounded(t *testing.T) {
	s := defaultStyles(true)
	inp := newInputModel(80, &s.Input)

	for i := range inputHistorySize + 20 {
		inp.textarea.SetValue("entry")
		_ = i
		inp.submit()
	}

	if len(inp.history) > inputHistorySize {
		t.Errorf("history length = %d, want <= %d", len(inp.history), inputHistorySize)
	}
}

func TestInputHistoryResetsIndexOnSubmit(t *testing.T) {
	s := defaultStyles(true)
	inp := newInputModel(80, &s.Input)

	inp.textarea.SetValue("first")
	inp.submit()

	// Navigate into history
	inp.historyUp()
	if inp.textarea.Value() != "first" {
		t.Fatal("expected to recall 'first'")
	}

	// Submit the recalled value — history index should reset
	inp.submit()

	// New up should recall "first" (the re-submitted value)
	inp.historyUp()
	if inp.textarea.Value() != "first" {
		t.Errorf("after re-submit, up = %q, want %q", inp.textarea.Value(), "first")
	}
}

func TestInputViewContainsPrompt(t *testing.T) {
	s := defaultStyles(true)
	inp := newInputModel(80, &s.Input)

	view := inp.view()
	// View should contain the textarea rendering
	if view == "" {
		t.Error("view should not be empty")
	}
}
