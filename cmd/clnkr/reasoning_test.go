package main

import (
	"strings"
	"testing"
)

func TestReasoningModelShowSetsVisibleAndContent(t *testing.T) {
	r := newReasoningModel(defaultStyles(true))
	r.show("line one\nline two", 80, 20)

	if !r.visible {
		t.Fatal("expected reasoning modal to be visible")
	}
	if r.content != "line one\nline two" {
		t.Fatalf("content = %q, want %q", r.content, "line one\nline two")
	}
}

func TestReasoningModelHideClearsVisibility(t *testing.T) {
	r := newReasoningModel(defaultStyles(true))
	r.show("line one", 80, 20)
	r.hide()

	if r.visible {
		t.Fatal("expected reasoning modal to be hidden")
	}
}

func TestReasoningModelViewEmptyWhenHidden(t *testing.T) {
	r := newReasoningModel(defaultStyles(true))
	if got := r.view(); got != "" {
		t.Fatalf("view = %q, want empty string", got)
	}
}

func TestReasoningModelWrapsLongLines(t *testing.T) {
	r := newReasoningModel(defaultStyles(true))
	r.show("This is a very long line that should wrap in the reasoning modal.", 20, 4)

	view := r.view()
	if !strings.Contains(view, "line that should wra") {
		t.Fatalf("expected wrapped continuation in modal view, got %q", view)
	}
}
