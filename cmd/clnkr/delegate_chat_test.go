package main

import (
	"strings"
	"testing"

	clnkr "github.com/clnkr-ai/clnkr"
)

func TestHydrateHistoryRendersDelegateBlockAsFriendlySummary(t *testing.T) {
	s := defaultStyles(true)
	c := newChatModel(80, 24, s, false)

	c.hydrateHistory([]clnkr.Message{
		{Role: "user", Content: "[delegate]\n" + `{"source":"clnkr","kind":"delegate","summary":"Found test patterns."}` + "\n[/delegate]"},
		{Role: "assistant", Content: "Assistant reply"},
	})

	content := c.content.String()
	if strings.Contains(content, `"kind":"delegate"`) {
		t.Fatalf("content should not render delegate json, got %q", content)
	}
	if !strings.Contains(content, "Delegation complete: Found test patterns.") {
		t.Fatalf("content should render delegate summary, got %q", content)
	}
	if !strings.Contains(content, "Assistant reply") {
		t.Fatalf("content should include later assistant content, got %q", content)
	}
}
