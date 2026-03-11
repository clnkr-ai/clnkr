package main

import (
	"fmt"
	"strings"
	"time"

	hew "github.com/cosgroveb/hew"
)

type statusModel struct {
	modelName    string
	inputTokens  int
	outputTokens int
	stepCount    int
	maxSteps     int
	elapsed      time.Duration
	runStart     time.Time
	running      bool
	focus        focusTarget
	styles       *statusStyles
}

func newStatusModel(modelName string, maxSteps int, s *statusStyles) statusModel {
	return statusModel{
		modelName: modelName,
		maxSteps:  maxSteps,
		focus:     focusInput,
		styles:    s,
	}
}

func (s *statusModel) updateFromResponse(u hew.Usage) {
	s.inputTokens += u.InputTokens
	s.outputTokens += u.OutputTokens
}

func (s *statusModel) incrementStep() {
	s.stepCount++
}

func (s *statusModel) startRun() {
	s.running = true
	s.runStart = time.Now()
	s.stepCount = 0
	s.elapsed = 0
}

func (s *statusModel) stopRun() {
	s.running = false
	s.elapsed = time.Since(s.runStart)
}

func (s *statusModel) setFocus(f focusTarget) {
	s.focus = f
}

func (s *statusModel) view(width int) string {
	sep := s.styles.Separator.Render(" │ ")

	segments := []string{
		s.styles.ModelName.Render(s.modelName),
		s.styles.Tokens.Render(fmt.Sprintf("%s in / %s out", formatTokens(s.inputTokens), formatTokens(s.outputTokens))),
		s.styles.StepCount.Render(fmt.Sprintf("step %d/%d", s.stepCount, s.maxSteps)),
		s.styles.Elapsed.Render(s.elapsedString()),
		s.focusString(),
	}

	content := strings.Join(segments, sep)
	// Pad or truncate to fill the full width
	bar := s.styles.Bar.Width(width).Render(content)
	return bar
}

func (s *statusModel) elapsedString() string {
	var d time.Duration
	if s.running {
		d = time.Since(s.runStart)
	} else {
		d = s.elapsed
	}
	return formatDuration(d)
}

func (s *statusModel) focusString() string {
	var label string
	switch s.focus {
	case focusInput:
		label = "INPUT"
	case focusViewport:
		label = "SCROLL"
	}
	return s.styles.FocusIndicator.Render(label)
}

// formatTokens returns a human-readable token count (e.g., "1.2k", "15.3k", "800").
func formatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// formatDuration returns a compact duration string.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
