package main

import (
	"fmt"
	"html"
	"strings"

	"charm.land/bubbles/v2/viewport"
	clnkr "github.com/clnkr-ai/clnkr"
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
	if authoritative != "" {
		c.writeRendered(authoritative)
	}
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

func (c *chatModel) appendEvent(e clnkr.Event) {
	switch ev := e.(type) {
	case clnkr.EventResponse:
		c.pendingQuery = ""
		if c.streaming {
			c.commitStream(c.renderAssistantMessage(ev.Message.Content, false))
		} else {
			rendered := c.renderAssistantMessage(ev.Message.Content, false)
			if rendered != "" {
				c.writeRendered(rendered)
			}
		}
	case clnkr.EventCommandStart:
		c.lastCmd = ev.Command
		c.pendingCmd = c.styles.Chat.CommandPending.Render(
			fmt.Sprintf("%s running: %s", iconPending, summarizeCommand(ev.Command)),
		)
	case clnkr.EventCommandDone:
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
	case clnkr.EventProtocolFailure:
		c.content.WriteString(c.styles.Chat.Warning.Render(
			fmt.Sprintf("%s Protocol error: %s", iconWarning, ev.Reason),
		))
		c.content.WriteString("\n\n")
	case clnkr.EventDebug:
		if ev.Message == "querying model..." {
			c.resetStreaming()
			c.pendingQuery = c.styles.Chat.CommandPending.Render(
				fmt.Sprintf("%s thunking...", iconPending),
			)
		}
		if c.verbose {
			c.content.WriteString(c.styles.Chat.Debug.Render(
				fmt.Sprintf("[clnkr] %s", ev.Message),
			))
			c.content.WriteString("\n")
		}
	default:
	}
}

func (c *chatModel) appendUserMessage(text string) {
	c.content.WriteString(c.styles.Chat.UserMessage.Render(text))
	c.content.WriteString("\n\n")
}

func (c *chatModel) appendHostNote(text string) {
	c.content.WriteString(c.styles.Chat.Warning.Render(text))
	c.content.WriteString("\n\n")
}

func (c *chatModel) setProposedCommand(command string) {
	c.lastCmd = command
	c.pendingCmd = c.styles.Chat.CommandPending.Render(
		fmt.Sprintf("%s proposed: %s", iconPending, summarizeCommand(command)),
	)
}

func (c *chatModel) clearPendingCommand() {
	c.lastCmd = ""
	c.pendingCmd = ""
}

func (c *chatModel) hydrateHistory(messages []clnkr.Message) {
	for _, msg := range messages {
		switch msg.Role {
		case "assistant":
			rendered := c.renderAssistantMessage(msg.Content, true)
			if rendered != "" {
				c.writeRendered(rendered)
			}
		case "user":
			if isStateTranscript(msg.Content) {
				continue
			}
			if transcript, ok := parseCommandTranscript(msg.Content); ok {
				style := c.styles.Chat.CommandSuccess
				icon := iconSuccess
				if transcript.exitCode != 0 {
					style = c.styles.Chat.CommandError
					icon = iconError
				}
				c.content.WriteString(style.Render(fmt.Sprintf("%s ran: %s", icon, summarizeCommand(transcript.command))))
				c.content.WriteString("\n")

				output := transcript.stdout
				if transcript.stderr != "" {
					if output != "" {
						output += "\n"
					}
					output += transcript.stderr
				}
				if output != "" {
					c.content.WriteString(c.styles.Chat.CommandOutput.Render(truncateOutput(output, 20)))
					c.content.WriteString("\n")
				}
				c.content.WriteString("\n")
				continue
			}
			c.appendUserMessage(msg.Content)
		}
	}
}

// renderAssistantMessage converts structured protocol JSON turns into user-facing
// text for TUI rendering. Returning "" suppresses message rendering.
func (c *chatModel) renderAssistantMessage(content string, includeClarify bool) string {
	turn, err := clnkr.ParseTurn(content)
	if err != nil {
		return content
	}

	switch t := turn.(type) {
	case *clnkr.ActTurn:
		// Act turns are rendered by command proposal/execution UI.
		return ""
	case *clnkr.ClarifyTurn:
		if includeClarify {
			return t.Question
		}
		return ""
	case *clnkr.DoneTurn:
		return t.Summary
	default:
		return content
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
// Matches the core library behavior.
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

type transcriptCommand struct {
	command  string
	stdout   string
	stderr   string
	exitCode int
}

func parseCommandTranscript(content string) (transcriptCommand, bool) {
	command, ok := extractTaggedSection(content, "command")
	if !ok {
		return transcriptCommand{}, false
	}
	stdout, ok := extractTaggedSection(content, "stdout")
	if !ok {
		return transcriptCommand{}, false
	}
	stderr, ok := extractTaggedSection(content, "stderr")
	if !ok {
		return transcriptCommand{}, false
	}
	exitCodeText, ok := extractTaggedSection(content, "exit_code")
	if !ok {
		return transcriptCommand{}, false
	}

	var exitCode int
	if _, err := fmt.Sscanf(strings.TrimSpace(exitCodeText), "%d", &exitCode); err != nil {
		return transcriptCommand{}, false
	}

	return transcriptCommand{
		command:  html.UnescapeString(command),
		stdout:   html.UnescapeString(stdout),
		stderr:   html.UnescapeString(stderr),
		exitCode: exitCode,
	}, true
}

func extractTaggedSection(content, tag string) (string, bool) {
	startTag := "[" + tag + "]"
	endTag := "[/" + tag + "]"
	start := strings.Index(content, startTag)
	if start < 0 {
		return "", false
	}
	start += len(startTag)
	end := strings.Index(content[start:], endTag)
	if end < 0 {
		return "", false
	}
	return strings.Trim(strings.TrimSpace(content[start:start+end]), "\n"), true
}

func isStateTranscript(content string) bool {
	content = strings.TrimSpace(content)
	return strings.HasPrefix(content, "[state]") && strings.HasSuffix(content, "[/state]")
}
