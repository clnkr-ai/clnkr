package compaction

import (
	"strings"
	"testing"
)

func TestLoadCompactionPrompt(t *testing.T) {
	t.Run("without instructions", func(t *testing.T) {
		got := LoadCompactionPrompt("")
		if !strings.Contains(got, "Return plain text only.") {
			t.Fatalf("prompt missing plain-text instruction: %q", got)
		}
		if !strings.Contains(got, "Do not return JSON.") {
			t.Fatalf("prompt missing no-JSON instruction: %q", got)
		}
		if !strings.Contains(got, "Do not call tools.") {
			t.Fatalf("prompt missing no-tools instruction: %q", got)
		}
		if strings.Contains(got, "Additional compact instructions:") {
			t.Fatalf("prompt should omit additional instructions section: %q", got)
		}
	})

	t.Run("with instructions", func(t *testing.T) {
		got := LoadCompactionPrompt("  focus on failing tests and edited files  ")
		if count := strings.Count(got, "focus on failing tests and edited files"); count != 1 {
			t.Fatalf("instruction count = %d, want 1 in prompt %q", count, got)
		}
		if !strings.Contains(got, "\nAdditional compact instructions:\nfocus on failing tests and edited files\n") {
			t.Fatalf("prompt missing trimmed instructions block: %q", got)
		}
	})
}
