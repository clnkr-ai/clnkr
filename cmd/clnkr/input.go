package main

import (
	"charm.land/bubbles/v2/textarea"
)

// inputHistorySize is the maximum number of previous submissions stored.
const inputHistorySize = 100
const defaultPlaceholder = "Type a task..."

type inputModel struct {
	textarea textarea.Model
	history  []string // ring buffer: oldest at [0], newest at [len-1]
	histIdx  int      // -1 = not browsing; 0..len-1 = current position (0=newest)
	styles   *inputStyles
}

func newInputModel(width int, s *inputStyles) inputModel {
	ta := textarea.New()
	ta.Placeholder = defaultPlaceholder
	ta.Prompt = iconPrompt + " "
	ta.ShowLineNumbers = false
	ta.SetWidth(width)
	ta.SetHeight(1)
	ta.MaxHeight = 5
	ta.CharLimit = 0 // no limit
	ta.Focus()       // Set focused state before bubbletea copies the model

	taStyles := textarea.DefaultDarkStyles()
	taStyles.Focused.Text = s.Text
	taStyles.Blurred.Text = s.Text
	taStyles.Focused.Prompt = s.Prompt
	taStyles.Blurred.Prompt = s.Prompt
	taStyles.Focused.Placeholder = s.Placeholder
	taStyles.Blurred.Placeholder = s.Placeholder
	taStyles.Focused.CursorLine = s.CursorLine
	taStyles.Blurred.CursorLine = s.CursorLine
	taStyles.Focused.LineNumber = s.Placeholder
	taStyles.Blurred.LineNumber = s.Placeholder
	taStyles.Focused.CursorLineNumber = s.Placeholder
	taStyles.Blurred.CursorLineNumber = s.Placeholder
	taStyles.Focused.EndOfBuffer = s.Placeholder
	taStyles.Blurred.EndOfBuffer = s.Placeholder
	taStyles.Cursor.Color = s.Cursor
	ta.SetStyles(taStyles)

	return inputModel{
		textarea: ta,
		histIdx:  -1,
		styles:   s,
	}
}

// submit captures the current text, pushes it to history, clears the textarea,
// and returns the submitted text. Returns "" if textarea is empty.
func (inp *inputModel) submit() string {
	text := inp.textarea.Value()
	if text == "" {
		return ""
	}

	// Push to history, evict oldest if at capacity
	inp.history = append(inp.history, text)
	if len(inp.history) > inputHistorySize {
		inp.history = inp.history[len(inp.history)-inputHistorySize:]
	}

	inp.histIdx = -1
	inp.textarea.Reset()
	return text
}

// historyUp recalls the previous history entry.
func (inp *inputModel) historyUp() {
	if len(inp.history) == 0 {
		return
	}

	if inp.histIdx == -1 {
		// Start browsing from the newest entry
		inp.histIdx = len(inp.history) - 1
	} else if inp.histIdx > 0 {
		inp.histIdx--
	}
	// else: already at oldest, stay put

	inp.textarea.SetValue(inp.history[inp.histIdx])
	inp.textarea.MoveToEnd()
}

// historyDown moves forward in history, clearing the input when past the newest.
func (inp *inputModel) historyDown() {
	if inp.histIdx == -1 {
		return
	}

	if inp.histIdx < len(inp.history)-1 {
		inp.histIdx++
		inp.textarea.SetValue(inp.history[inp.histIdx])
		inp.textarea.MoveToEnd()
	} else {
		// Past newest — clear input, exit history browsing
		inp.histIdx = -1
		inp.textarea.Reset()
	}
}

// lineCount returns the number of lines in the textarea content.
func (inp *inputModel) lineCount() int {
	return inp.textarea.LineCount()
}

func (inp *inputModel) view() string {
	return inp.textarea.View()
}

func (inp *inputModel) setWidth(w int) {
	inp.textarea.SetWidth(w)
}

func (inp *inputModel) setPlaceholder(text string) {
	inp.textarea.Placeholder = text
}

func (inp *inputModel) resetPlaceholder() {
	inp.textarea.Placeholder = defaultPlaceholder
}
