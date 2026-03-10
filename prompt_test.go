package hew

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPrompt(t *testing.T) {
	t.Run("base prompt only", func(t *testing.T) {
		dir := t.TempDir()
		prompt := LoadPrompt(dir)
		if !strings.Contains(prompt, "```bash") {
			t.Error("base prompt should contain bash code block instructions")
		}
		if !strings.Contains(prompt, "<done/>") {
			t.Error("base prompt should contain done signal instructions")
		}
	})

	t.Run("appends AGENTS.md when present", func(t *testing.T) {
		dir := t.TempDir()
		content := "Always use gofmt before committing."
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}

		prompt := LoadPrompt(dir)
		if !strings.Contains(prompt, content) {
			t.Error("prompt should include AGENTS.md content")
		}
	})

	t.Run("ignores missing AGENTS.md", func(t *testing.T) {
		dir := t.TempDir()
		prompt := LoadPrompt(dir)
		if prompt == "" {
			t.Error("prompt should not be empty without AGENTS.md")
		}
	})
}
