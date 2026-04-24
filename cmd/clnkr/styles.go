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
	Prompt      lipgloss.Style
	Text        lipgloss.Style
	Placeholder lipgloss.Style
	CursorLine  lipgloss.Style
	Cursor      color.Color
	Running     lipgloss.Style
}

type styles struct {
	NoColor bool
	Chat    chatStyles
	Status  statusStyles
	Input   inputStyles
}

func defaultStyles(hasDark bool) *styles {
	_ = hasDark

	bg := c("#050301")
	bgSoft := c("#0A0602")
	ink := c("#FF9E2C")
	inkSoft := c("#F08A22")
	inkDim := c("#C66A1A")
	line := c("#B85D14")
	errColor := c("#D65A31")

	return &styles{
		NoColor: false,
		Chat: chatStyles{
			UserMessage:    lipgloss.NewStyle().Foreground(ink).Bold(true),
			AssistantReply: lipgloss.NewStyle().Foreground(ink),
			StreamingText:  lipgloss.NewStyle().Foreground(ink),
			CommandPending: lipgloss.NewStyle().Foreground(inkSoft),
			CommandSuccess: lipgloss.NewStyle().Foreground(ink).Bold(true),
			CommandError:   lipgloss.NewStyle().Foreground(errColor).Bold(true),
			CommandOutput:  lipgloss.NewStyle().Foreground(inkDim),
			Debug:          lipgloss.NewStyle().Foreground(line),
			Warning:        lipgloss.NewStyle().Foreground(inkSoft).Bold(true),
			NewContent:     lipgloss.NewStyle().Foreground(bg).Background(ink).Bold(true),
		},
		Status: statusStyles{
			Bar:            lipgloss.NewStyle().Foreground(bg).Background(ink),
			ModelName:      lipgloss.NewStyle().Foreground(bg).Background(ink).Bold(true),
			Tokens:         lipgloss.NewStyle().Foreground(bg).Background(ink),
			StepCount:      lipgloss.NewStyle().Foreground(bg).Background(ink),
			Elapsed:        lipgloss.NewStyle().Foreground(bg).Background(ink),
			FocusIndicator: lipgloss.NewStyle().Foreground(bg).Background(ink).Bold(true),
			Separator:      lipgloss.NewStyle().Foreground(bg).Background(ink),
		},
		Input: inputStyles{
			Prompt:      lipgloss.NewStyle().Foreground(ink).Bold(true),
			Text:        lipgloss.NewStyle().Foreground(ink),
			Placeholder: lipgloss.NewStyle().Foreground(inkDim),
			CursorLine:  lipgloss.NewStyle().Foreground(ink).Background(bgSoft),
			Cursor:      ink,
			Running:     lipgloss.NewStyle().Foreground(inkDim),
		},
	}
}

func monochromeStyles(hasDark bool) *styles {
	_ = hasDark

	noColor := lipgloss.NoColor{}

	return &styles{
		NoColor: true,
		Chat: chatStyles{
			UserMessage:    lipgloss.NewStyle().Bold(true),
			AssistantReply: lipgloss.NewStyle(),
			StreamingText:  lipgloss.NewStyle(),
			CommandPending: lipgloss.NewStyle().Faint(true),
			CommandSuccess: lipgloss.NewStyle().Bold(true),
			CommandError:   lipgloss.NewStyle().Bold(true).Underline(true),
			CommandOutput:  lipgloss.NewStyle().Faint(true),
			Debug:          lipgloss.NewStyle().Faint(true),
			Warning:        lipgloss.NewStyle().Bold(true),
			NewContent:     lipgloss.NewStyle().Foreground(noColor).Background(noColor).Bold(true).Reverse(true),
		},
		Status: statusStyles{
			Bar:            lipgloss.NewStyle().Bold(true).Reverse(true),
			ModelName:      lipgloss.NewStyle().Bold(true).Reverse(true),
			Tokens:         lipgloss.NewStyle().Reverse(true),
			StepCount:      lipgloss.NewStyle().Reverse(true),
			Elapsed:        lipgloss.NewStyle().Reverse(true),
			FocusIndicator: lipgloss.NewStyle().Bold(true).Reverse(true),
			Separator:      lipgloss.NewStyle().Reverse(true),
		},
		Input: inputStyles{
			Prompt:      lipgloss.NewStyle().Bold(true),
			Text:        lipgloss.NewStyle(),
			Placeholder: lipgloss.NewStyle().Faint(true),
			CursorLine:  lipgloss.NewStyle(),
			Cursor:      noColor,
			Running:     lipgloss.NewStyle().Faint(true),
		},
	}
}

// c converts a hex color string to a color.Color.
func c(hex string) color.Color {
	return lipgloss.Color(hex)
}
