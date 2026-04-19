package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clnkr "github.com/clnkr-ai/clnkr"
)

func TestRunnerRunReturnsDoneSummary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-clnku")
	script := `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --trajectory) out="$2"; shift 2 ;;
    *) shift ;;
  esac
done
printf '%s\n' '[{"role":"assistant","content":"{\"type\":\"done\",\"summary\":\"delegated ok\"}"}]' > "$out"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := delegateProcessRunner{Binary: bin}.Run(context.Background(), delegateRequest{
		Task:       "inspect tests",
		WorkingDir: dir,
		Parent:     []clnkr.Message{{Role: "user", Content: "parent task"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary != "delegated ok" {
		t.Fatalf("Summary = %q, want %q", result.Summary, "delegated ok")
	}
	if len(result.Messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(result.Messages))
	}
}

func TestRunnerRunFallsBackToFinalAssistantContent(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-clnku")
	script := `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --trajectory) out="$2"; shift 2 ;;
    *) shift ;;
  esac
done
printf '%s\n' '[{"role":"assistant","content":"plain final content"}]' > "$out"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := delegateProcessRunner{Binary: bin}.Run(context.Background(), delegateRequest{Task: "inspect", WorkingDir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary != "plain final content" {
		t.Fatalf("Summary = %q, want plain final content", result.Summary)
	}
}

func TestRunnerRunReturnsChildError(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-clnku")
	script := `#!/bin/sh
echo child failed >&2
exit 7
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := delegateProcessRunner{Binary: bin}.Run(context.Background(), delegateRequest{Task: "inspect", WorkingDir: dir})
	if err == nil || !strings.Contains(err.Error(), "child failed") {
		t.Fatalf("err = %v, want child failure output", err)
	}
}

func TestFormatArtifact(t *testing.T) {
	got := formatDelegateArtifact("inspect tests", "delegated ok")
	if !strings.HasPrefix(got, "[delegate]\n") {
		t.Fatalf("artifact = %q, want delegate prefix", got)
	}
	if !strings.Contains(got, `"kind":"delegate"`) {
		t.Fatalf("artifact = %q, want delegate json", got)
	}
	if !strings.Contains(got, `"summary":"delegated ok"`) {
		t.Fatalf("artifact = %q, want summary", got)
	}
}
