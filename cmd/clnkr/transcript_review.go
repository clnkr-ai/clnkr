package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	clnkr "github.com/clnkr-ai/clnkr"
)

type transcriptPagerDoneMsg struct {
	err     error
	cleanup func()
}

type executableLookup func(string) (string, error)

var prepareTranscriptPagerFunc = prepareTranscriptPager

func transcriptScrollbackBlock(content string) string {
	return "----- clnkr transcript -----\n" +
		strings.TrimRight(content, "\n") +
		"\n----- end clnkr transcript -----"
}

func renderTranscriptReview(messages []clnkr.Message) (string, bool) {
	var b strings.Builder
	lastCwd := ""

	for _, msg := range messages {
		switch msg.Role {
		case "user":
			renderUserReviewMessage(&b, msg.Content, &lastCwd)
		case "assistant":
			renderAssistantReviewMessage(&b, msg.Content)
		default:
			label := strings.TrimSpace(msg.Role)
			if label == "" {
				label = "Message"
			}
			writeReviewBlock(&b, label, msg.Content)
		}
	}

	out := strings.TrimSpace(sanitizeReviewText(b.String()))
	return out + "\n", out != ""
}

func sanitizeReviewText(s string) string {
	s = ansi.Strip(s)
	var b strings.Builder
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && size == 1 {
			s = s[size:]
			continue
		}
		s = s[size:]
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == '\r':
			b.WriteByte('\n')
		case r < 0x20 || r == 0x7f:
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func renderUserReviewMessage(b *strings.Builder, content string, lastCwd *string) {
	if isStateTranscript(content) {
		if cwd, ok := parseStateTranscript(content); ok && cwd != *lastCwd {
			writeReviewBlock(b, "Host", "cwd "+cwd)
			*lastCwd = cwd
		}
		return
	}
	if isCompactTranscript(content) {
		if summary, ok := parseCompactSummary(content); ok {
			writeReviewBlock(b, "Host", "compacted transcript: "+summary)
		}
		return
	}
	if strings.HasPrefix(strings.TrimSpace(content), "[protocol_error]") {
		writeReviewBlock(b, "Host", content)
		return
	}
	if summary, ok := parseDelegateTranscript(content); ok {
		writeReviewBlock(b, "Host", "delegation complete: "+summary)
		return
	}
	if transcript, ok := parseCommandTranscript(content); ok {
		renderCommandReviewMessage(b, transcript)
		return
	}
	writeReviewBlock(b, "User", content)
}

func renderAssistantReviewMessage(b *strings.Builder, content string) {
	turn, err := clnkr.ParseTurn(content)
	if err != nil {
		writeReviewBlock(b, "Assistant", content)
		return
	}

	switch t := turn.(type) {
	case *clnkr.ActTurn:
		for i, action := range t.Bash.Commands {
			label := "Assistant proposed command"
			if len(t.Bash.Commands) > 1 {
				label = fmt.Sprintf("Assistant proposed command %d", i+1)
			}
			body := action.Command
			if strings.TrimSpace(action.Workdir) != "" {
				body = "workdir: " + action.Workdir + "\n" + body
			}
			writeReviewBlock(b, label, body)
		}
	case *clnkr.ClarifyTurn:
		writeReviewBlock(b, "Assistant", t.Question)
	case *clnkr.DoneTurn:
		writeReviewBlock(b, "Assistant", t.Summary)
	default:
	}
}

func renderCommandReviewMessage(b *strings.Builder, transcript transcriptCommand) {
	var body strings.Builder
	fmt.Fprintf(&body, "$ %s\n", transcript.command)
	fmt.Fprintf(&body, "exit code: %d\n", transcript.exitCode)
	if transcript.stdout != "" {
		body.WriteString("\nstdout:\n")
		body.WriteString(transcript.stdout)
		body.WriteString("\n")
	}
	if transcript.stderr != "" {
		body.WriteString("\nstderr:\n")
		body.WriteString(transcript.stderr)
		body.WriteString("\n")
	}
	if len(transcript.feedback.ChangedFiles) > 0 {
		body.WriteString("\nchanged files:\n")
		for _, path := range transcript.feedback.ChangedFiles {
			body.WriteString("- ")
			body.WriteString(path)
			body.WriteString("\n")
		}
	}
	if transcript.feedback.Diff != "" {
		body.WriteString("\ndiff:\n")
		body.WriteString(transcript.feedback.Diff)
		body.WriteString("\n")
	}
	writeReviewBlock(b, "Command", strings.TrimRight(body.String(), "\n"))
}

func writeReviewBlock(b *strings.Builder, label, body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString(label)
	b.WriteString(":\n")
	b.WriteString(body)
}

func parseStateTranscript(content string) (string, bool) {
	body, ok := extractTaggedSection(content, "state")
	if !ok {
		return "", false
	}
	var payload struct {
		Source string `json:"source"`
		Kind   string `json:"kind"`
		Cwd    string `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return "", false
	}
	if payload.Source != "clnkr" || payload.Kind != "state" || strings.TrimSpace(payload.Cwd) == "" {
		return "", false
	}
	return payload.Cwd, true
}

func parseCompactSummary(content string) (string, bool) {
	body, ok := extractTaggedSection(content, "compact")
	if !ok {
		return "", false
	}
	var payload struct {
		Source  string `json:"source"`
		Kind    string `json:"kind"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return "", false
	}
	if payload.Source != "clnkr" || payload.Kind != "compact" || strings.TrimSpace(payload.Summary) == "" {
		return "", false
	}
	return strings.TrimSpace(payload.Summary), true
}

func prepareTranscriptPager(messages []clnkr.Message, getenv func(string) string, lookPath executableLookup) (*exec.Cmd, func(), bool, error) {
	content, ok := renderTranscriptReview(messages)
	if !ok {
		return nil, nil, false, nil
	}

	file, err := os.CreateTemp("", "clnkr-transcript-*.txt")
	if err != nil {
		return nil, nil, false, fmt.Errorf("create transcript file: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }

	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		cleanup()
		return nil, nil, false, fmt.Errorf("write transcript file: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return nil, nil, false, fmt.Errorf("close transcript file: %w", err)
	}

	cmd := transcriptPagerCommand(path, getenv, lookPath)
	return cmd, cleanup, true, nil
}

func transcriptPagerCommand(path string, getenv func(string) string, lookPath executableLookup) *exec.Cmd {
	if getenv == nil {
		getenv = os.Getenv
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	fields := strings.Fields(getenv("PAGER"))
	if len(fields) > 0 {
		args := append([]string{}, fields[1:]...)
		args = append(args, path)
		return exec.Command(fields[0], args...)
	}

	if _, err := lookPath("less"); err == nil {
		return exec.Command("less", "-R", "-X", path)
	}
	return exec.Command("cat", path)
}
