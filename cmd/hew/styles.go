package main

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

const (
	iconPending    = "●"
	iconSuccess    = "✓"
	iconError      = "✗"
	iconPrompt     = "❯"
	iconNewContent = "↓"
	iconWarning    = "⚠"
)

type chatStyles struct {
	UserMessage    lipgloss.Style
	AssistantReply lipgloss.Style
	StreamingText  lipgloss.Style
	CommandPending lipgloss.Style
	CommandSuccess lipgloss.Style
	CommandError   lipgloss.Style
	CommandOutput  lipgloss.Style
	Debug          lipgloss.Style
	Warning        lipgloss.Style
	NewContent     lipgloss.Style
}

type statusStyles struct {
	Bar            lipgloss.Style
	ModelName      lipgloss.Style
	Tokens         lipgloss.Style
	StepCount      lipgloss.Style
	Elapsed        lipgloss.Style
	FocusIndicator lipgloss.Style
	Separator      lipgloss.Style
}

type inputStyles struct {
	Prompt  lipgloss.Style
	Running lipgloss.Style
}

type styles struct {
	Chat   chatStyles
	Status statusStyles
	Input  inputStyles
}

func defaultStyles(hasDark bool) *styles {
	ld := lipgloss.LightDark(hasDark)

	muted := ld(c("#666666"), c("#999999"))
	dim := ld(c("#888888"), c("#666666"))
	warn := ld(c("#CC6600"), c("#FFAA00"))
	errColor := ld(c("#CC0000"), c("#FF4444"))
	success := ld(c("#006600"), c("#44FF44"))
	pending := ld(c("#666600"), c("#FFFF44"))
	info := ld(c("#0066CC"), c("#44AAFF"))
	barBg := ld(c("#EEEEEE"), c("#333333"))

	return &styles{
		Chat: chatStyles{
			UserMessage:    lipgloss.NewStyle().Bold(true),
			AssistantReply: lipgloss.NewStyle(),
			StreamingText:  lipgloss.NewStyle(),
			CommandPending: lipgloss.NewStyle().Foreground(pending),
			CommandSuccess: lipgloss.NewStyle().Foreground(success),
			CommandError:   lipgloss.NewStyle().Foreground(errColor),
			CommandOutput:  lipgloss.NewStyle().Foreground(muted),
			Debug:          lipgloss.NewStyle().Foreground(dim),
			Warning:        lipgloss.NewStyle().Foreground(warn),
			NewContent:     lipgloss.NewStyle().Foreground(info).Bold(true),
		},
		Status: statusStyles{
			Bar:            lipgloss.NewStyle().Background(barBg),
			ModelName:      lipgloss.NewStyle().Bold(true),
			Tokens:         lipgloss.NewStyle().Foreground(muted),
			StepCount:      lipgloss.NewStyle().Foreground(muted),
			Elapsed:        lipgloss.NewStyle().Foreground(muted),
			FocusIndicator: lipgloss.NewStyle().Bold(true),
			Separator:      lipgloss.NewStyle().Foreground(dim),
		},
		Input: inputStyles{
			Prompt:  lipgloss.NewStyle().Foreground(info).Bold(true),
			Running: lipgloss.NewStyle().Foreground(dim).Italic(true),
		},
	}
}

// c converts a hex color string to a color.Color.
func c(hex string) color.Color {
	return lipgloss.Color(hex)
}
