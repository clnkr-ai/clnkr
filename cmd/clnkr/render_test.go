package main

import (
	"regexp"
	"strings"
	"testing"
)

var ansiColorPattern = regexp.MustCompile(`\x1b\[(?:[0-9;]*;)?(?:3[0-7]|4[0-7]|9[0-7]|10[0-7]|38;[0-9;]*|48;[0-9;]*)m`)
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

func TestRenderMarkdownBasic(t *testing.T) {
	// Reset renderer for clean test
	renderer = nil

	out := renderMarkdown("# Hello\n\nWorld", 80, false)
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
	out := renderMarkdown(md, 80, false)
	if !strings.Contains(out, "Println") {
		t.Errorf("code block should preserve code content, got: %q", out)
	}
}

func TestRenderMarkdownFallsBackOnEmpty(t *testing.T) {
	renderer = nil

	// Empty string should still return something (glamour may add whitespace)
	out := renderMarkdown("", 80, false)
	_ = out // just verify no panic
}

func TestRenderMarkdownWidthChange(t *testing.T) {
	renderer = nil

	// Render at one width, then reset and render at another
	_ = renderMarkdown("test", 40, false)
	renderer = nil // simulate width change
	out := renderMarkdown("test", 120, false)
	if out == "" {
		t.Error("should render after width change")
	}
}

func TestRenderMarkdownNoColorModeDropsColorButKeepsStructure(t *testing.T) {
	renderer = nil

	md := "# Heading\n\n> quote\n\n```go\nfmt.Println(\"hi\")\n```\n\n| a | b |\n| - | - |\n| 1 | 2 |"
	out := renderMarkdown(md, 80, true)
	if ansiColorPattern.MatchString(out) {
		t.Fatalf("no-color markdown should not emit ANSI color codes, got %q", out)
	}
	plain := stripANSI(out)
	for _, want := range []string{"Heading", "│ ", "fmt.Println", "|", "a", "b"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("no-color markdown should preserve %q, got %q", want, plain)
		}
	}
}

func TestRenderMarkdownRebuildsRendererWhenModeChangesInProcess(t *testing.T) {
	renderer = nil

	md := "# Heading"
	colorOut := renderMarkdown(md, 80, false)
	if !ansiColorPattern.MatchString(colorOut) {
		t.Fatalf("color markdown should emit ANSI color codes, got %q", colorOut)
	}

	noColorOut := renderMarkdown(md, 80, true)
	if ansiColorPattern.MatchString(noColorOut) {
		t.Fatalf("no-color markdown should rebuild without ANSI color codes, got %q", noColorOut)
	}
	if !strings.Contains(noColorOut, "Heading") {
		t.Fatalf("no-color markdown should still contain heading text, got %q", noColorOut)
	}
}
