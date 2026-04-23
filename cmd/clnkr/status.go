package main

import (
	"fmt"
	"strings"
	"time"

	clnkr "github.com/clnkr-ai/clnkr"
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

func (s *statusModel) updateFromResponse(u clnkr.Usage) {
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

func (s *statusModel) view(width int, mode, hints string) string {
	if mode == "" {
		mode = focusMode(s.focus)
	}
	segments := []string{mode}
	if width >= 40 && hints != "" {
		segments = append(segments, truncateStatusText(hints, 28))
	}
	if width >= 56 {
		segments = append(segments,
			truncateStatusText(s.modelName, 24),
			fmt.Sprintf("%s in / %s out", formatTokens(s.inputTokens), formatTokens(s.outputTokens)),
		)
	}
	if width >= 72 {
		segments = append(segments,
			fmt.Sprintf("step %d/%d", s.stepCount, s.maxSteps),
			s.elapsedString(),
		)
	}
	content := truncateStatusText(strings.Join(segments, " | "), width)
	return s.styles.Bar.Width(width).Render(content)
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

func focusMode(f focusTarget) string {
	switch f {
	case focusInput:
		return "INPUT"
	case focusViewport:
		return "SCROLL"
	}
	return "INPUT"
}

func truncateStatusText(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
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
