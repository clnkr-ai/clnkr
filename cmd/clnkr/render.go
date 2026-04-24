package main

import (
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
)

// renderer caches the glamour renderer to avoid re-creating it on every call.
var renderer *glamour.TermRenderer
var rendererWidth int
var rendererNoColor bool

// initRenderer creates the glamour renderer with the given width.
// Called on first render and on width changes.
func initRenderer(width int, noColor bool) {
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(markdownStyle(noColor)),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		renderer = nil
		rendererWidth = 0
		rendererNoColor = false
		return
	}
	renderer = r
	rendererWidth = width
	rendererNoColor = noColor
}

// renderMarkdown passes content through glamour for styled terminal output.
// Returns plain text on error (graceful fallback).
func renderMarkdown(content string, width int, noColor bool) string {
	if renderer == nil || rendererWidth != width || rendererNoColor != noColor {
		initRenderer(width, noColor)
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

func markdownStyle(noColor bool) ansi.StyleConfig {
	if noColor {
		return monochromeMarkdownStyle()
	}
	return retroMarkdownStyle()
}

func monochromeMarkdownStyle() ansi.StyleConfig {
	cfg := glamourstyles.ASCIIStyleConfig

	cfg.Document = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			BlockPrefix: "\n",
			BlockSuffix: "\n",
		},
		Margin: uintPtr(0),
	}
	cfg.BlockQuote = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{},
		Indent:         uintPtr(1),
		IndentToken:    strPtr("│ "),
	}
	cfg.Heading = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			BlockSuffix: "\n",
			Bold:        boolPtr(true),
		},
	}
	cfg.H1 = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "# ",
			Bold:   boolPtr(true),
		},
	}
	cfg.H2 = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "## ",
			Bold:   boolPtr(true),
		},
	}
	cfg.H3 = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "### ",
			Bold:   boolPtr(true),
		},
	}
	cfg.H4 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Prefix: "#### "}}
	cfg.H5 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Prefix: "##### "}}
	cfg.H6 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Prefix: "###### "}}
	cfg.Strong = ansi.StylePrimitive{Bold: boolPtr(true)}
	cfg.Emph = ansi.StylePrimitive{Underline: boolPtr(true)}
	cfg.HorizontalRule = ansi.StylePrimitive{Format: "\n--------\n"}
	cfg.Item = ansi.StylePrimitive{BlockPrefix: "• "}
	cfg.Enumeration = ansi.StylePrimitive{BlockPrefix: ". "}
	cfg.Task = ansi.StyleTask{
		StylePrimitive: ansi.StylePrimitive{},
		Ticked:         "[x] ",
		Unticked:       "[ ] ",
	}
	cfg.Link = ansi.StylePrimitive{Underline: boolPtr(true)}
	cfg.LinkText = ansi.StylePrimitive{Bold: boolPtr(true)}
	cfg.Code = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			BlockPrefix: "`",
			BlockSuffix: "`",
		},
	}
	cfg.CodeBlock = ansi.StyleCodeBlock{
		StyleBlock: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{},
			Margin:         uintPtr(0),
		},
	}
	cfg.Table = ansi.StyleTable{
		StyleBlock: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{},
		},
		CenterSeparator: strPtr("|"),
		ColumnSeparator: strPtr("|"),
		RowSeparator:    strPtr("-"),
	}
	cfg.DefinitionDescription = ansi.StylePrimitive{BlockPrefix: "\n* "}

	return cfg
}

func retroMarkdownStyle() ansi.StyleConfig {
	cfg := glamourstyles.ASCIIStyleConfig

	cfg.Document = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			BlockPrefix: "\n",
			BlockSuffix: "\n",
		},
		Margin: uintPtr(0),
	}
	cfg.BlockQuote = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Color: strPtr("#C66A1A"),
		},
		Indent:      uintPtr(1),
		IndentToken: strPtr("│ "),
	}
	cfg.Heading = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			BlockSuffix: "\n",
			Color:       strPtr("#FF9E2C"),
			Bold:        boolPtr(true),
		},
	}
	cfg.H1 = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix:          " ",
			Suffix:          " ",
			Color:           strPtr("#050301"),
			BackgroundColor: strPtr("#FF9E2C"),
			Bold:            boolPtr(true),
		},
	}
	cfg.H2 = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "## ",
			Color:  strPtr("#FFB54A"),
			Bold:   boolPtr(true),
		},
	}
	cfg.H3 = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "### ",
			Color:  strPtr("#F08A22"),
			Bold:   boolPtr(true),
		},
	}
	cfg.H4 = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "#### ",
			Color:  strPtr("#F08A22"),
		},
	}
	cfg.H5 = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "##### ",
			Color:  strPtr("#C66A1A"),
		},
	}
	cfg.H6 = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "###### ",
			Color:  strPtr("#C66A1A"),
		},
	}
	cfg.Strong = ansi.StylePrimitive{
		Color: strPtr("#FFB54A"),
		Bold:  boolPtr(true),
	}
	cfg.Emph = ansi.StylePrimitive{
		Color: strPtr("#F08A22"),
	}
	cfg.HorizontalRule = ansi.StylePrimitive{
		Color:  strPtr("#B85D14"),
		Format: "\n--------\n",
	}
	cfg.Item = ansi.StylePrimitive{
		BlockPrefix: "• ",
		Color:       strPtr("#FF9E2C"),
	}
	cfg.Enumeration = ansi.StylePrimitive{
		BlockPrefix: ". ",
		Color:       strPtr("#FF9E2C"),
	}
	cfg.Task = ansi.StyleTask{
		StylePrimitive: ansi.StylePrimitive{
			Color: strPtr("#FF9E2C"),
		},
		Ticked:   "[x] ",
		Unticked: "[ ] ",
	}
	cfg.Link = ansi.StylePrimitive{
		Color: strPtr("#FFB54A"),
	}
	cfg.LinkText = ansi.StylePrimitive{
		Color: strPtr("#FFB54A"),
		Bold:  boolPtr(true),
	}
	cfg.Code = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			BlockPrefix: "`",
			BlockSuffix: "`",
			Color:       strPtr("#FFB54A"),
		},
	}
	cfg.CodeBlock = ansi.StyleCodeBlock{
		StyleBlock: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: strPtr("#FF9E2C"),
			},
			Margin: uintPtr(0),
		},
		Chroma: &ansi.Chroma{
			Text: ansi.StylePrimitive{
				Color: strPtr("#FF9E2C"),
			},
			Background: ansi.StylePrimitive{
				BackgroundColor: strPtr("#050301"),
			},
			Comment: ansi.StylePrimitive{
				Color: strPtr("#B85D14"),
				Faint: boolPtr(true),
			},
			Keyword: ansi.StylePrimitive{
				Color: strPtr("#FFB54A"),
				Bold:  boolPtr(true),
			},
			KeywordReserved: ansi.StylePrimitive{
				Color: strPtr("#FFB54A"),
				Bold:  boolPtr(true),
			},
			NameFunction: ansi.StylePrimitive{
				Color: strPtr("#FFB54A"),
			},
			LiteralString: ansi.StylePrimitive{
				Color: strPtr("#F08A22"),
			},
			LiteralNumber: ansi.StylePrimitive{
				Color: strPtr("#F08A22"),
			},
			Operator: ansi.StylePrimitive{
				Color: strPtr("#FF9E2C"),
			},
			Punctuation: ansi.StylePrimitive{
				Color: strPtr("#C66A1A"),
			},
		},
	}
	cfg.Table = ansi.StyleTable{
		StyleBlock: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: strPtr("#FF9E2C"),
			},
		},
		CenterSeparator: strPtr("|"),
		ColumnSeparator: strPtr("|"),
		RowSeparator:    strPtr("-"),
	}
	cfg.DefinitionDescription = ansi.StylePrimitive{
		BlockPrefix: "\n* ",
		Color:       strPtr("#F08A22"),
	}

	return cfg
}

func strPtr(s string) *string {
	return &s
}

func boolPtr(v bool) *bool {
	return &v
}

func uintPtr(v uint) *uint {
	return &v
}
