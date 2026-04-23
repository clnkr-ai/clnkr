package main

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	"github.com/charmbracelet/x/ansi"
)

const searchPlaceholder = "Search transcript..."

type searchModel struct {
	textarea  textarea.Model
	visible   bool
	current   int
	total     int
	lastQuery string
}

func newSearchModel(width int, s *inputStyles) searchModel {
	ta := textarea.New()
	ta.Placeholder = searchPlaceholder
	ta.Prompt = "find> "
	ta.ShowLineNumbers = false
	ta.SetWidth(width)
	ta.SetHeight(1)
	ta.MaxHeight = 1
	ta.CharLimit = 0

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

	m := searchModel{textarea: ta}
	m.updatePrompt()
	return m
}

func (s *searchModel) show(width int) {
	s.visible = true
	s.textarea.SetWidth(width)
	s.textarea.Reset()
	s.current = 0
	s.total = 0
	s.lastQuery = ""
	s.updatePrompt()
}

func (s *searchModel) hide() {
	s.visible = false
	s.textarea.Reset()
	s.current = 0
	s.total = 0
	s.lastQuery = ""
	s.updatePrompt()
}

func (s *searchModel) setWidth(width int) {
	s.textarea.SetWidth(width)
}

func (s *searchModel) setCounts(current, total int) {
	s.current = current
	s.total = total
	s.updatePrompt()
}

func (s searchModel) query() string {
	return s.textarea.Value()
}

func (s searchModel) lineCount() int {
	return 1
}

func (s searchModel) view() string {
	return s.textarea.View()
}

func (s *searchModel) updatePrompt() {
	switch {
	case s.total > 0:
		s.textarea.Prompt = "find " + strings.TrimSpace(strings.Join([]string{itoa(s.current), "/", itoa(s.total)}, "")) + "> "
	case strings.TrimSpace(s.textarea.Value()) != "":
		s.textarea.Prompt = "find 0/0> "
	default:
		s.textarea.Prompt = "find> "
	}
}

func strippedSearchMatches(content, query string) [][]int {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	plain := ansi.Strip(content)
	var matches [][]int
	start := 0
	for {
		idx := strings.Index(plain[start:], query)
		if idx < 0 {
			break
		}
		matchStart := start + idx
		matchEnd := matchStart + len(query)
		matches = append(matches, []int{matchStart, matchEnd})
		start = matchEnd
	}
	return matches
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
