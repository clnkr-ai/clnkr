package main

import "testing"

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
