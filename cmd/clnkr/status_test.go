package main

import (
	"strings"
	"testing"
	"time"

	clnkr "github.com/clnkr-ai/clnkr"
)

func newTestStatus() statusModel {
	s := defaultStyles(true)
	return newStatusModel("test-model", 100, &s.Status)
}

func TestStatusRendersModelName(t *testing.T) {
	st := newTestStatus()
	view := st.view(80, "", "")
	if !strings.Contains(view, "test-model") {
		t.Errorf("status bar should contain model name, got: %q", view)
	}
}

func TestStatusRendersTokenCounts(t *testing.T) {
	st := newTestStatus()
	st.updateFromResponse(clnkr.Usage{InputTokens: 1200, OutputTokens: 800})
	view := st.view(80, "", "")
	if !strings.Contains(view, "1.2k in") {
		t.Errorf("should show input tokens, got: %q", view)
	}
	if !strings.Contains(view, "800 out") {
		t.Errorf("should show output tokens, got: %q", view)
	}
}

func TestStatusTokenCountsCumulative(t *testing.T) {
	st := newTestStatus()
	st.updateFromResponse(clnkr.Usage{InputTokens: 500, OutputTokens: 300})
	st.updateFromResponse(clnkr.Usage{InputTokens: 700, OutputTokens: 200})

	if st.inputTokens != 1200 {
		t.Errorf("inputTokens = %d, want 1200", st.inputTokens)
	}
	if st.outputTokens != 500 {
		t.Errorf("outputTokens = %d, want 500", st.outputTokens)
	}
}

func TestStatusRendersStepCount(t *testing.T) {
	st := newTestStatus()
	st.incrementStep()
	st.incrementStep()
	st.incrementStep()
	view := st.view(80, "", "")
	if !strings.Contains(view, "3/100") {
		t.Errorf("should show step count 3/100, got: %q", view)
	}
}

func TestStatusRendersElapsedTime(t *testing.T) {
	st := newTestStatus()
	st.startRun()

	// Simulate some elapsed time
	st.runStart = time.Now().Add(-5 * time.Second)
	view := st.view(80, "", "")
	if !strings.Contains(view, "5s") {
		t.Errorf("should show elapsed time, got: %q", view)
	}
}

func TestStatusStopRunFreezesElapsed(t *testing.T) {
	st := newTestStatus()
	st.startRun()
	st.runStart = time.Now().Add(-3 * time.Second)
	st.stopRun()

	if st.running {
		t.Error("running should be false after stopRun")
	}
	// Frozen elapsed should be ~3s
	if st.elapsed < 3*time.Second || st.elapsed > 4*time.Second {
		t.Errorf("elapsed should be ~3s, got %s", st.elapsed)
	}
}

func TestStatusRendersFocusIndicator(t *testing.T) {
	st := newTestStatus()
	st.setFocus(focusInput)
	view := st.view(80, "", "")
	if !strings.Contains(view, "INPUT") {
		t.Errorf("should show INPUT focus indicator, got: %q", view)
	}

	st.setFocus(focusViewport)
	view = st.view(80, "", "")
	if !strings.Contains(view, "SCROLL") {
		t.Errorf("should show SCROLL focus indicator, got: %q", view)
	}
}

func TestStatusViewShowsModeAndHints(t *testing.T) {
	st := newTestStatus()

	view := st.view(100, "HELP", "?/Esc close")
	if !strings.Contains(view, "HELP") {
		t.Fatalf("status should show mode, got %q", view)
	}
	if !strings.Contains(view, "?/Esc close") {
		t.Fatalf("status should show hints, got %q", view)
	}
}

func TestStatusNarrowViewKeepsModeWithLongModel(t *testing.T) {
	s := defaultStyles(true)
	st := newStatusModel("very-long-model-name-that-could-wrap-status", 100, &s.Status)

	for _, width := range []int{32, 56, 72} {
		view := st.view(width, "APPROVAL", "y Enter approve | Ctrl+Y approve")
		if !strings.Contains(view, "APPROVAL") {
			t.Fatalf("width %d narrow status should keep mode, got %q", width, view)
		}
		if strings.Count(view, "\n") != 0 {
			t.Fatalf("width %d status should stay one line, got %q", width, view)
		}
	}
}

func TestStatusStartRunResetsPerRunState(t *testing.T) {
	st := newTestStatus()
	st.stepCount = 5
	st.elapsed = 10 * time.Second

	st.startRun()
	if st.stepCount != 0 {
		t.Errorf("stepCount should reset on startRun, got %d", st.stepCount)
	}
	if !st.running {
		t.Error("running should be true after startRun")
	}
}
