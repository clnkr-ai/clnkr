package hew

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPromptWithOptions_BasePrompt(t *testing.T) {
	t.Run("base prompt only", func(t *testing.T) {
		dir := t.TempDir()
		prompt := LoadPromptWithOptions(dir, PromptOptions{})
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

		prompt := LoadPromptWithOptions(dir, PromptOptions{})
		if !strings.Contains(prompt, content) {
			t.Error("prompt should include AGENTS.md content")
		}
	})

	t.Run("ignores missing AGENTS.md", func(t *testing.T) {
		dir := t.TempDir()
		prompt := LoadPromptWithOptions(dir, PromptOptions{})
		if prompt == "" {
			t.Error("prompt should not be empty without AGENTS.md")
		}
	})
}

func TestLoadPromptWithOptions(t *testing.T) {
	t.Run("includes planning workflow by default", func(t *testing.T) {
		dir := t.TempDir()
		prompt := LoadPromptWithOptions(dir, PromptOptions{})
		if !strings.Contains(prompt, "<planning-workflow>") {
			t.Error("prompt should include planning workflow by default")
		}
		if !strings.Contains(prompt, "Agent Orchestration Patterns") {
			t.Error("prompt should include orchestration patterns by default")
		}
		if !strings.Contains(prompt, "Brainstorming Ideas Into Designs") {
			t.Error("prompt should include brainstorming section by default")
		}
		if !strings.Contains(prompt, "Writing Plans") {
			t.Error("prompt should include writing plans section by default")
		}
		if !strings.Contains(prompt, "HARD-GATE") {
			t.Error("prompt should include brainstorming hard gate by default")
		}
		if !strings.Contains(prompt, "Bite-Sized Task Granularity") {
			t.Error("prompt should include writing plans task granularity by default")
		}
	})

	t.Run("excludes planning workflow when disabled", func(t *testing.T) {
		dir := t.TempDir()
		prompt := LoadPromptWithOptions(dir, PromptOptions{DisablePlanningWorkflow: true})
		if strings.Contains(prompt, "<planning-workflow>") {
			t.Error("prompt should not include planning workflow when disabled")
		}
		if strings.Contains(prompt, "Agent Orchestration Patterns") {
			t.Error("prompt should not include orchestration patterns when disabled")
		}
		if strings.Contains(prompt, "Brainstorming Ideas Into Designs") {
			t.Error("prompt should not include brainstorming section when disabled")
		}
		if strings.Contains(prompt, "Writing Plans") {
			t.Error("prompt should not include writing plans section when disabled")
		}
		if strings.Contains(prompt, "HARD-GATE") {
			t.Error("prompt should not include brainstorming hard gate when disabled")
		}
		if strings.Contains(prompt, "Bite-Sized Task Granularity") {
			t.Error("prompt should not include writing plans task granularity when disabled")
		}
	})

	t.Run("still includes AGENTS.md when present", func(t *testing.T) {
		dir := t.TempDir()
		content := "Project-specific instructions here."
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}

		prompt := LoadPromptWithOptions(dir, PromptOptions{})
		if !strings.Contains(prompt, content) {
			t.Error("prompt should include AGENTS.md content")
		}
		if !strings.Contains(prompt, "<planning-workflow>") {
			t.Error("prompt should include planning workflow")
		}
	})

	t.Run("AGENTS.md without planning workflow", func(t *testing.T) {
		dir := t.TempDir()
		content := "Project-specific instructions here."
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}

		prompt := LoadPromptWithOptions(dir, PromptOptions{DisablePlanningWorkflow: true})
		if !strings.Contains(prompt, content) {
			t.Error("prompt should include AGENTS.md content")
		}
		if strings.Contains(prompt, "<planning-workflow>") {
			t.Error("prompt should not include planning workflow when disabled")
		}
	})
}
