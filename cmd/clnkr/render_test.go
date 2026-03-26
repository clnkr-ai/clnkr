package main

import (
	"strings"
	"testing"
)

func TestRenderMarkdownBasic(t *testing.T) {
	// Reset renderer for clean test
	renderer = nil

	out := renderMarkdown("# Hello\n\nWorld", 80)
	if out == "" {
		t.Error("renderMarkdown should produce output")
	}
	// Glamour should produce styled output different from input
	if out == "# Hello\n\nWorld" {
		t.Error("renderMarkdown should transform markdown")
	}
}

func TestRenderMarkdownCodeBlock(t *testing.T) {
	renderer = nil

	md := "```go\nfmt.Println(\"hello\")\n```"
	out := renderMarkdown(md, 80)
	if !strings.Contains(out, "Println") {
		t.Errorf("code block should preserve code content, got: %q", out)
	}
}

func TestRenderMarkdownFallsBackOnEmpty(t *testing.T) {
	renderer = nil

	// Empty string should still return something (glamour may add whitespace)
	out := renderMarkdown("", 80)
	_ = out // just verify no panic
}

func TestRenderMarkdownWidthChange(t *testing.T) {
	renderer = nil

	// Render at one width, then reset and render at another
	_ = renderMarkdown("test", 40)
	renderer = nil // simulate width change
	out := renderMarkdown("test", 120)
	if out == "" {
		t.Error("should render after width change")
	}
}
