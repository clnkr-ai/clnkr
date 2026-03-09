package main

import (
	"github.com/charmbracelet/glamour"
)

// renderer caches the glamour renderer to avoid re-creating it on every call.
var renderer *glamour.TermRenderer

// initRenderer creates the glamour renderer with the given width.
// Called on first render and on width changes.
func initRenderer(width int) {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		renderer = nil
		return
	}
	renderer = r
}

// renderMarkdown passes content through glamour for styled terminal output.
// Returns plain text on error (graceful fallback).
func renderMarkdown(content string, width int) string {
	if renderer == nil {
		initRenderer(width)
	}
	if renderer == nil {
		return content
	}
	out, err := renderer.Render(content)
	if err != nil {
		return content
	}
	return out
}
