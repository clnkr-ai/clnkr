package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	hew "github.com/cosgroveb/hew"
)

type chatModel struct {
	viewport     viewport.Model
	content      *strings.Builder
	streamBuf    *strings.Builder
	pendingCmd   string
	pendingQuery string
	streaming    bool
	wasAtBottom  bool
	hasNew       bool // new content arrived while scrolled up
	verbose      bool
	styles       *styles
	// lastCmd caches the raw command text from EventCommandStart
	// for use in the EventCommandDone commit line.
	lastCmd string
}

func newChatModel(width, height int, s *styles, verbose bool) chatModel {
	vp := viewport.New()
	vp.SetWidth(width)
	vp.SetHeight(height)
	return chatModel{
		viewport:  vp,
		content:   &strings.Builder{},
		streamBuf: &strings.Builder{},
		styles:    s,
		verbose:   verbose,
	}
}

func (c *chatModel) appendToken(text string) {
	if !c.streaming {
		c.streaming = true
		c.wasAtBottom = c.viewport.AtBottom()
		c.streamBuf.Reset()
		c.pendingQuery = ""
	}
	c.streamBuf.WriteString(text)
}

func (c *chatModel) commitStream(authoritative string) {
	c.writeRendered(authoritative)
	c.streamBuf.Reset()
	c.streaming = false
}

func (c *chatModel) commitPartialStream() {
	if c.streamBuf.Len() > 0 {
		c.content.WriteString(c.streamBuf.String())
		c.content.WriteString("\n\n")
		c.streamBuf.Reset()
	}
	c.streaming = false
}

func (c *chatModel) resetStreaming() {
	c.streamBuf.Reset()
	c.streaming = false
}

func (c *chatModel) appendEvent(e hew.Event) {
	switch ev := e.(type) {
	case hew.EventResponse:
		c.pendingQuery = ""
		if c.streaming {
			c.commitStream(ev.Message.Content)
		} else {
			c.writeRendered(ev.Message.Content)
		}
	case hew.EventCommandStart:
		c.lastCmd = ev.Command
		c.pendingCmd = c.styles.Chat.CommandPending.Render(
			fmt.Sprintf("%s running: %s", iconPending, summarizeCommand(ev.Command)),
		)
	case hew.EventCommandDone:
		icon := iconSuccess
		style := c.styles.Chat.CommandSuccess
		if ev.Err != nil {
			icon = iconError
			style = c.styles.Chat.CommandError
		}
		command := c.lastCmd
		if ev.Command != "" {
			command = ev.Command
		}
		c.content.WriteString(style.Render(fmt.Sprintf("%s ran: %s", icon, summarizeCommand(command))))
		c.content.WriteString("\n")
		output := ev.Stdout
		if ev.Stderr != "" {
			if output != "" {
				output += "\n"
			}
			output += ev.Stderr
		}
		if output != "" {
			c.content.WriteString(c.styles.Chat.CommandOutput.Render(truncateOutput(output, 20)))
			c.content.WriteString("\n")
		}
		c.content.WriteString("\n")
		c.pendingCmd = ""
		c.lastCmd = ""
	case hew.EventFormatError:
		c.content.WriteString(c.styles.Chat.Warning.Render(
			fmt.Sprintf("%s No bash block found in response — format error", iconWarning),
		))
		c.content.WriteString("\n\n")
	case hew.EventDebug:
		if ev.Message == "querying model..." {
			c.resetStreaming()
			c.pendingQuery = c.styles.Chat.CommandPending.Render(
				fmt.Sprintf("%s thunking...", iconPending),
			)
		}
		if c.verbose {
			c.content.WriteString(c.styles.Chat.Debug.Render(
				fmt.Sprintf("[hew] %s", ev.Message),
			))
			c.content.WriteString("\n")
		}
	default:
	}
}

func (c *chatModel) updateViewport() {
	full := c.content.String() + c.pendingQuery + c.pendingCmd + c.streamBuf.String()
	wasBottom := c.wasAtBottom
	if !c.streaming {
		wasBottom = c.viewport.AtBottom()
	}

	c.viewport.SetContent(full)

	if wasBottom {
		c.viewport.GotoBottom()
		c.hasNew = false
	} else if c.viewport.TotalLineCount() > c.viewport.VisibleLineCount() {
		c.hasNew = true
	}
}

// writeRendered renders markdown content through glamour and appends to committed content.
func (c *chatModel) writeRendered(content string) {
	rendered := renderMarkdown(content, c.viewport.Width())
	c.content.WriteString(rendered)
}

func (c *chatModel) resize(width, height int) {
	c.viewport.SetWidth(width)
	c.viewport.SetHeight(height)
	// Reset renderer on width change so glamour re-wraps
	renderer = nil
}

// summarizeCommand returns a short display form of a command.
// Matches the core library's summarizeCommand behavior.
func summarizeCommand(cmd string) string {
	lines := strings.Split(cmd, "\n")
	first := lines[0]
	if len(lines) == 1 {
		return first
	}
	return fmt.Sprintf("%s ... (%d lines)", first, len(lines))
}

func truncateOutput(output string, maxLines int) string {
	lines := strings.Split(output, "\n")
	if len(lines) <= maxLines {
		return output
	}
	truncated := strings.Join(lines[:maxLines], "\n")
	return truncated + fmt.Sprintf("\n... (%d more lines)", len(lines)-maxLines)
}
