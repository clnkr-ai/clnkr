package main

import (
	"fmt"
	"strings"
	"testing"

	clnkr "github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/transcript"
)

func chatActJSON(command string) string {
	return fmt.Sprintf(`{"type":"act","bash":{"commands":[{"command":%q,"workdir":null}]}}`, command)
}

func TestChatAppendEventResponse(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	c.appendEvent(clnkr.EventResponse{
		Message: clnkr.Message{Role: "assistant", Content: "hello world"},
	})

	content := c.content.String()
	if !strings.Contains(content, "hello world") {
		t.Errorf("content should contain response text, got: %q", content)
	}
}

func TestChatSuppressesStructuredActResponse(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	c.appendEvent(clnkr.EventResponse{
		Message: clnkr.Message{Role: "assistant", Content: chatActJSON("ls -la")},
	})

	content := c.content.String()
	if strings.Contains(content, `"type":"act"`) {
		t.Fatalf("content should not contain raw act JSON, got: %q", content)
	}
	if strings.Contains(content, "ls -la") {
		t.Fatalf("content should not render act command from EventResponse, got: %q", content)
	}
}

func TestChatSuppressesStructuredClarifyResponseInLiveStream(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	c.appendEvent(clnkr.EventResponse{
		Message: clnkr.Message{Role: "assistant", Content: `{"type":"clarify","question":"Which interface?"}`},
	})

	content := c.content.String()
	if strings.Contains(content, `"type":"clarify"`) {
		t.Fatalf("content should not contain raw clarify JSON, got: %q", content)
	}
	if strings.Contains(content, "Which interface?") {
		t.Fatalf("live EventResponse should not render clarify text directly, got: %q", content)
	}
}

func TestChatRendersDoneSummaryFromStructuredResponse(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	c.appendEvent(clnkr.EventResponse{
		Message: clnkr.Message{Role: "assistant", Content: `{"type":"done","summary":"All set."}`},
	})

	content := c.content.String()
	if strings.Contains(content, `"type":"done"`) {
		t.Fatalf("content should not contain raw done JSON, got: %q", content)
	}
	if !strings.Contains(content, "All set.") {
		t.Fatalf("content should render done summary, got: %q", content)
	}
}

func TestChatStreamingBuffer(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	c.appendToken("Hello")
	if !c.streaming {
		t.Error("expected streaming=true after first token")
	}
	if c.streamBuf.String() != "Hello" {
		t.Errorf("streamBuf: got %q, want %q", c.streamBuf.String(), "Hello")
	}

	c.appendToken(" world")
	if c.streamBuf.String() != "Hello world" {
		t.Errorf("streamBuf: got %q, want %q", c.streamBuf.String(), "Hello world")
	}

	c.commitStream("Hello world!")
	if c.streaming {
		t.Error("expected streaming=false after commit")
	}
	if c.streamBuf.Len() != 0 {
		t.Error("streamBuf should be empty after commit")
	}
	if !strings.Contains(c.content.String(), "Hello world!") {
		t.Errorf("committed content should contain authoritative text, got: %q", c.content.String())
	}
}

func TestChatStreamingDroppedTokensRecoveredOnCommit(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	// Simulate partial streaming (some tokens dropped)
	c.appendToken("Hello")
	c.appendToken(" world")

	// Commit with full authoritative content
	c.commitStream("Hello world and goodbye")

	content := c.content.String()
	if !strings.Contains(content, "Hello world and goodbye") {
		t.Errorf("committed content should have authoritative text, got: %q", content)
	}
}

func TestChatPendingCommandBuffer(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	c.appendEvent(clnkr.EventCommandStart{Command: "ls -la", Dir: "/tmp"})
	if c.pendingCmd == "" {
		t.Error("pendingCmd should be set after EventCommandStart")
	}
	if strings.Contains(c.content.String(), "ls -la") {
		t.Error("pending command should not be in committed content")
	}

	c.appendEvent(clnkr.EventCommandDone{Command: "ls -la", Stdout: "total 0\n", ExitCode: 0, Err: nil})
	if c.pendingCmd != "" {
		t.Error("pendingCmd should be cleared after EventCommandDone")
	}
	if !strings.Contains(c.content.String(), "ls -la") {
		t.Error("committed content should contain the command after EventCommandDone")
	}
}

func TestChatCommandDoneShowsChangedFilesNote(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	c.appendEvent(clnkr.EventCommandDone{
		Command:  "touch note.txt",
		ExitCode: 0,
		Feedback: clnkr.CommandFeedback{ChangedFiles: []string{"note.txt"}},
	})

	if !strings.Contains(c.content.String(), "Changed: note.txt") {
		t.Fatalf("content should show changed-files note, got %q", c.content.String())
	}
}

func TestChatViewportWrapsLongLines(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(20, 4, s, false)

	c.content.WriteString("This is a very long line that should wrap in a narrow viewport.")
	c.updateViewport()

	view := c.viewport.View()
	if !strings.Contains(view, "line that should wra") {
		t.Fatalf("expected wrapped continuation in viewport, got %q", view)
	}
}

func TestParseCommandTranscriptPreservesLiteralBodyText(t *testing.T) {
	content := transcript.FormatCommandResult(transcript.CommandResult{
		Command:  "printf 'a&b <c> [d]'",
		ExitCode: 7,
		Stdout:   "one [stdout]\ntwo & < >\n[/stdout]\n",
		Stderr:   "[stderr]\nerr & < >\n[/stderr]\n",
	})

	got, ok := parseCommandTranscript(content)
	if !ok {
		t.Fatal("expected command transcript to parse")
	}
	if got.command != "printf 'a&b <c> [d]'" {
		t.Fatalf("command = %q, want %q", got.command, "printf 'a&b <c> [d]'")
	}
	if got.stdout != "one [stdout]\ntwo & < >\n[/stdout]" {
		t.Fatalf("stdout = %q, want %q", got.stdout, "one [stdout]\ntwo & < >\n[/stdout]")
	}
	if got.stderr != "[stderr]\nerr & < >\n[/stderr]" {
		t.Fatalf("stderr = %q, want %q", got.stderr, "[stderr]\nerr & < >\n[/stderr]")
	}
	if got.exitCode != 7 {
		t.Fatalf("exitCode = %d, want 7", got.exitCode)
	}
}

func TestParseCommandTranscriptParsesFeedback(t *testing.T) {
	content := transcript.FormatCommandResult(transcript.CommandResult{
		Command:  "printf hi",
		ExitCode: 0,
		Feedback: transcript.CommandFeedback{
			ChangedFiles: []string{"note.txt"},
			Diff:         "@@ -1 +1 @@\n+[/command_feedback]\n",
		},
	})

	got, ok := parseCommandTranscript(content)
	if !ok {
		t.Fatal("expected command transcript to parse")
	}
	if got.feedback.Diff != "@@ -1 +1 @@\n+[/command_feedback]\n" {
		t.Fatalf("feedback diff = %q, want literal command_feedback marker", got.feedback.Diff)
	}
	if len(got.feedback.ChangedFiles) != 1 || got.feedback.ChangedFiles[0] != "note.txt" {
		t.Fatalf("feedback changed files = %#v, want note.txt", got.feedback.ChangedFiles)
	}
}

func TestChatHydrateHistoryRendersClarifyQuestion(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	c.hydrateHistory([]clnkr.Message{
		{Role: "assistant", Content: `{"type":"clarify","question":"Use 0.0.0.0?"}`},
	})

	content := c.content.String()
	if strings.Contains(content, `"type":"clarify"`) {
		t.Fatalf("hydrated content should not contain raw clarify JSON, got: %q", content)
	}
	if !strings.Contains(content, "Use 0.0.0.0?") {
		t.Fatalf("hydrated content should contain clarify question, got: %q", content)
	}
}

func TestChatSetProposedCommand(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	c.setProposedCommand(formatActProposal([]clnkr.BashAction{{Command: "rm important.txt"}}))
	if c.pendingCmd == "" {
		t.Fatal("pendingCmd should be set after proposing a command")
	}
	if !strings.Contains(c.pendingCmd, "1. rm important.txt") {
		t.Fatalf("pendingCmd should include numbered proposal, got %q", c.pendingCmd)
	}
	if strings.Contains(c.content.String(), "rm important.txt") {
		t.Fatal("proposed command should not be committed before execution")
	}
}

func TestChatSetProposedCommandShowsWorkdir(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	c.setProposedCommand(formatActProposal([]clnkr.BashAction{{Command: "rm important.txt", Workdir: "subdir"}}))
	if !strings.Contains(c.pendingCmd, "1. rm important.txt in subdir") {
		t.Fatalf("pendingCmd should show workdir, got %q", c.pendingCmd)
	}
}

func TestChatBufferInvariant(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	// Stream some tokens — pendingCmd must be empty during streaming
	c.appendToken("hello")
	if c.pendingCmd != "" {
		t.Error("pendingCmd must be empty while streaming")
	}
	if c.streamBuf.Len() == 0 {
		t.Error("streamBuf should be non-empty after appendToken")
	}

	// Commit stream, then start a command — streamBuf must be empty during command
	c.commitStream("hello")
	c.appendEvent(clnkr.EventCommandStart{Command: "ls", Dir: "."})
	if c.streamBuf.Len() != 0 {
		t.Error("streamBuf must be empty while command is pending")
	}
	if c.pendingCmd == "" {
		t.Error("pendingCmd should be non-empty after EventCommandStart")
	}
}

func TestChatResetStreamingState(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	c.appendToken("partial")
	if !c.streaming {
		t.Error("expected streaming=true")
	}

	c.resetStreaming()
	if c.streaming {
		t.Error("expected streaming=false after reset")
	}
	if c.streamBuf.Len() != 0 {
		t.Error("streamBuf should be empty after reset")
	}
}

func TestChatProtocolFailure(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	c.appendEvent(clnkr.EventProtocolFailure{Reason: "parse_error", Raw: "not json"})
	content := c.content.String()
	if !strings.Contains(content, "parse_error") {
		t.Errorf("content should contain protocol error reason, got: %q", content)
	}
}

func TestChatDebugVerbose(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, true) // verbose=true

	c.appendEvent(clnkr.EventDebug{Message: "test debug"})
	content := c.content.String()
	if !strings.Contains(content, "test debug") {
		t.Errorf("verbose mode should show debug messages, got: %q", content)
	}
}

func TestChatDebugNonVerbose(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false) // verbose=false

	c.appendEvent(clnkr.EventDebug{Message: "test debug"})
	content := c.content.String()
	if strings.Contains(content, "test debug") {
		t.Errorf("non-verbose mode should not show debug messages, got: %q", content)
	}
}

func TestTruncateOutput(t *testing.T) {
	short := "line1\nline2\nline3"
	if got := truncateOutput(short, 5); got != short {
		t.Errorf("short output should be unchanged, got: %q", got)
	}

	var lines []string
	for i := range 30 {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	long := strings.Join(lines, "\n")
	got := truncateOutput(long, 20)
	if !strings.HasSuffix(got, "... (10 more lines)") {
		t.Errorf("expected truncation suffix, got: %q", got)
	}
	// Should contain the first 20 lines
	if !strings.Contains(got, "line 0") || !strings.Contains(got, "line 19") {
		t.Error("truncated output should contain the first 20 lines")
	}
	if strings.Contains(got, "line 20\n") {
		t.Error("truncated output should not contain line 20")
	}
}

func TestChatDebugResetsStreaming(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	c.appendToken("partial")
	if !c.streaming {
		t.Error("expected streaming=true")
	}

	// "querying model..." signal resets streaming state
	c.appendEvent(clnkr.EventDebug{Message: "querying model..."})
	if c.streaming {
		t.Error("expected streaming=false after 'querying model...' debug event")
	}
	if c.streamBuf.Len() != 0 {
		t.Error("streamBuf should be empty after streaming reset")
	}
}

func TestHydrateHistorySkipsCompactBlock(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	c.hydrateHistory([]clnkr.Message{
		{
			Role:    "user",
			Content: "[compact]\n" + `{"source":"clnkr","kind":"compact","summary":"Older work summarized."}` + "\n[/compact]",
		},
		{Role: "assistant", Content: "Current assistant reply"},
		{Role: "user", Content: "Current user follow-up"},
	})

	content := c.content.String()
	if strings.Contains(content, "[compact]") {
		t.Fatalf("content should not render compact block tags, got %q", content)
	}
	if strings.Contains(content, `"kind":"compact"`) {
		t.Fatalf("content should not render compact block json, got %q", content)
	}
	if strings.Contains(content, "Older work summarized.") {
		t.Fatalf("content should skip the compact block entirely, got %q", content)
	}
	if !strings.Contains(content, "Current assistant reply") {
		t.Fatalf("content should include later assistant content, got %q", content)
	}
	if !strings.Contains(content, "Current user follow-up") {
		t.Fatalf("content should include later user content, got %q", content)
	}
}

func TestHydrateHistoryRendersForeignCompactBlockAsUserContent(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	foreignCompact := "[compact]\n" + `{"source":"user","kind":"compact","summary":"Keep me visible."}` + "\n[/compact]"
	c.hydrateHistory([]clnkr.Message{
		{Role: "user", Content: foreignCompact},
		{Role: "assistant", Content: "Assistant reply"},
	})

	content := c.content.String()
	if !strings.Contains(content, "Keep me visible.") {
		t.Fatalf("foreign compact-tagged content should render, got %q", content)
	}
	if !strings.Contains(content, "Assistant reply") {
		t.Fatalf("content should include later assistant content, got %q", content)
	}
}

func TestChatAppendEventResponseAddsReasoningBreadcrumbWhenEnabled(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)
	c.reasoningEnabled = true
	c.reasoningKeyHint = "Ctrl+Y"

	c.appendEvent(clnkr.EventResponse{
		Message: clnkr.Message{Role: "assistant", Content: `{"type":"done","summary":"Finished.","reasoning":"checked the parser path first"}`},
	})

	content := c.content.String()
	if !strings.Contains(content, "Finished.") {
		t.Fatalf("expected summary in content, got: %q", content)
	}
	if !strings.Contains(content, "Reasoning trace available (press Ctrl+Y)") {
		t.Fatalf("expected reasoning breadcrumb in content, got: %q", content)
	}
}

func TestChatAppendEventResponseOmitsReasoningBreadcrumbWhenReasoningEmpty(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)
	c.reasoningEnabled = true
	c.reasoningKeyHint = "Ctrl+Y"

	c.appendEvent(clnkr.EventResponse{
		Message: clnkr.Message{Role: "assistant", Content: `{"type":"done","summary":"Finished.","reasoning":""}`},
	})

	content := c.content.String()
	if strings.Contains(content, "Reasoning trace available") {
		t.Fatalf("did not expect reasoning breadcrumb, got: %q", content)
	}
}

func TestChatAppendEventResponseDoesNotAddReasoningBreadcrumbWhenDisabled(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)
	c.reasoningEnabled = false
	c.reasoningKeyHint = "Ctrl+Y"

	c.appendEvent(clnkr.EventResponse{
		Message: clnkr.Message{Role: "assistant", Content: `{"type":"done","summary":"Finished.","reasoning":"checked the parser path first"}`},
	})

	content := c.content.String()
	if !strings.Contains(content, "Finished.") {
		t.Fatalf("expected summary in content, got: %q", content)
	}
	if strings.Contains(content, "Reasoning trace available") {
		t.Fatalf("did not expect reasoning breadcrumb when disabled, got: %q", content)
	}
}
