package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	clnkr "github.com/clnkr-ai/clnkr"
)

func TestTranscriptReviewRendersFullCommandOutput(t *testing.T) {
	var stdout strings.Builder
	for i := 1; i <= 25; i++ {
		stdout.WriteString("line ")
		stdout.WriteString(string(rune('A' + i - 1)))
		stdout.WriteString("\n")
	}
	messages := []clnkr.Message{
		{Role: "user", Content: "inspect output"},
		{Role: "assistant", Content: `{"type":"act","bash":{"commands":[{"command":"printf many","workdir":null}]}}`},
		{Role: "user", Content: formatCommandTranscriptForTest("printf many", 0, stdout.String(), "warn\n", clnkr.CommandFeedback{
			ChangedFiles: []string{"note.txt"},
			Diff:         "diff --git a/note.txt b/note.txt",
		})},
		{Role: "assistant", Content: `{"type":"done","summary":"finished"}`},
	}

	got, ok := renderTranscriptReview(messages)
	if !ok {
		t.Fatal("expected transcript")
	}
	for _, want := range []string{
		"User:\ninspect output",
		"Assistant proposed command:\nprintf many",
		"Command:\n$ printf many",
		"line Y",
		"stderr:\nwarn",
		"changed files:\n- note.txt",
		"diff:\ndiff --git a/note.txt b/note.txt",
		"Assistant:\nfinished",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("review transcript missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "... (") {
		t.Fatalf("review transcript should not use viewport truncation:\n%s", got)
	}
}

func TestTranscriptReviewRendersHostArtifacts(t *testing.T) {
	messages := []clnkr.Message{
		{Role: "user", Content: `[state]` + "\n" + `{"source":"clnkr","kind":"state","cwd":"/repo"}` + "\n" + `[/state]`},
		{Role: "user", Content: `[state]` + "\n" + `{"source":"clnkr","kind":"state","cwd":"/repo"}` + "\n" + `[/state]`},
		{Role: "user", Content: `[state]` + "\n" + `{"source":"clnkr","kind":"state","cwd":"/repo/sub"}` + "\n" + `[/state]`},
		{Role: "user", Content: `[compact]` + "\n" + `{"source":"clnkr","kind":"compact","summary":"Older work summarized."}` + "\n" + `[/compact]`},
		{Role: "user", Content: formatDelegateArtifact("inspect tests", "delegated ok")},
	}

	got, ok := renderTranscriptReview(messages)
	if !ok {
		t.Fatal("expected transcript")
	}
	if strings.Count(got, "cwd /repo\n") != 1 {
		t.Fatalf("expected one /repo cwd note, got:\n%s", got)
	}
	for _, want := range []string{
		"Host:\ncwd /repo/sub",
		"Host:\ncompacted transcript: Older work summarized.",
		"Host:\ndelegation complete: delegated ok",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("review transcript missing %q:\n%s", want, got)
		}
	}
}

func TestTranscriptReviewEmpty(t *testing.T) {
	got, ok := renderTranscriptReview(nil)
	if ok {
		t.Fatalf("empty messages ok=true, transcript=%q", got)
	}
}

func TestTranscriptReviewSanitizesTerminalControls(t *testing.T) {
	messages := []clnkr.Message{
		{Role: "user", Content: formatCommandTranscriptForTest("printf controls", 0, "\x1b[2Jhidden\x1b]52;c;AAAA\x07\nok\tvalue\nbad\x00byte\n", "\x1b[31mred\x1b[0m\n", clnkr.CommandFeedback{})},
	}

	got, ok := renderTranscriptReview(messages)
	if !ok {
		t.Fatal("expected transcript")
	}
	if strings.ContainsAny(got, "\x00\x1b\x07") {
		t.Fatalf("review transcript contains terminal controls: %q", got)
	}
	for _, want := range []string{"hidden", "ok\tvalue", "badbyte", "red"} {
		if !strings.Contains(got, want) {
			t.Fatalf("review transcript missing %q: %q", want, got)
		}
	}
}

func TestTranscriptScrollbackBlock(t *testing.T) {
	got := transcriptScrollbackBlock("User:\nreview this\n")
	want := "----- clnkr transcript -----\nUser:\nreview this\n----- end clnkr transcript -----"
	if got != want {
		t.Fatalf("scrollback block = %q, want %q", got, want)
	}
}

func TestTranscriptPagerCommandUsesPagerEnv(t *testing.T) {
	cmd := transcriptPagerCommand("/tmp/transcript.txt", func(name string) string {
		if name == "PAGER" {
			return "less -S"
		}
		return ""
	}, nil)

	want := []string{"less", "-S", "/tmp/transcript.txt"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", cmd.Args, want)
	}
}

func TestTranscriptPagerCommandFallsBackToCatWhenLessMissing(t *testing.T) {
	cmd := transcriptPagerCommand("/tmp/transcript.txt", func(string) string { return "" }, func(name string) (string, error) {
		if name == "less" {
			return "", errors.New("not found")
		}
		return "/bin/" + name, nil
	})

	want := []string{"cat", "/tmp/transcript.txt"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", cmd.Args, want)
	}
}

func TestTranscriptPagerCommandUsesLessNoInitByDefault(t *testing.T) {
	cmd := transcriptPagerCommand("/tmp/transcript.txt", func(string) string { return "" }, func(name string) (string, error) {
		return "/bin/" + name, nil
	})

	want := []string{"less", "-R", "-X", "/tmp/transcript.txt"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", cmd.Args, want)
	}
}

func TestPrepareTranscriptPagerWritesTempFile(t *testing.T) {
	cmd, cleanup, ok, err := prepareTranscriptPager([]clnkr.Message{
		{Role: "user", Content: "review this"},
	}, func(string) string { return "cat" }, nil)
	if err != nil {
		t.Fatalf("prepareTranscriptPager: %v", err)
	}
	if !ok {
		t.Fatal("expected transcript")
	}
	defer cleanup()

	path := cmd.Args[len(cmd.Args)-1]
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp transcript: %v", err)
	}
	if got := string(data); got != "User:\nreview this\n" {
		t.Fatalf("temp transcript = %q", got)
	}
}
