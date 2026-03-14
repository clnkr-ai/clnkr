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

func TestLoadPromptWithOptions_RLMWorkflow(t *testing.T) {
	t.Run("excludes RLM workflow by default", func(t *testing.T) {
		dir := t.TempDir()
		prompt := LoadPromptWithOptions(dir, PromptOptions{})
		if strings.Contains(prompt, "Recursive decomposition") {
			t.Error("prompt should not include RLM workflow by default")
		}
	})

	t.Run("includes RLM workflow when enabled", func(t *testing.T) {
		dir := t.TempDir()
		prompt := LoadPromptWithOptions(dir, PromptOptions{EnableRLMWorkflow: true})
		if !strings.Contains(prompt, "Recursive decomposition") {
			t.Error("prompt should include Recursive decomposition when RLM enabled")
		}
		if !strings.Contains(prompt, "inspect, chunk, dispatch, collect, aggregate") {
			t.Error("prompt should include the decomposition pattern when RLM enabled")
		}
	})

	t.Run("RLM workflow appears after planning workflow", func(t *testing.T) {
		dir := t.TempDir()
		prompt := LoadPromptWithOptions(dir, PromptOptions{EnableRLMWorkflow: true})
		planIdx := strings.Index(prompt, "<planning-workflow>")
		rlmIdx := strings.Index(prompt, "Recursive decomposition")
		if planIdx >= rlmIdx {
			t.Error("RLM workflow should appear after planning workflow")
		}
	})

	t.Run("RLM workflow works with planning disabled", func(t *testing.T) {
		dir := t.TempDir()
		prompt := LoadPromptWithOptions(dir, PromptOptions{
			DisablePlanningWorkflow: true,
			EnableRLMWorkflow:       true,
		})
		if strings.Contains(prompt, "<planning-workflow>") {
			t.Error("planning workflow should be excluded when disabled")
		}
		if !strings.Contains(prompt, "Recursive decomposition") {
			t.Error("RLM workflow should be included regardless of planning workflow setting")
		}
	})
}

func TestLoadPromptWithOptions_LayeredAgentsMD(t *testing.T) {
	t.Run("loads HOME AGENTS.md", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // empty config dir

		content := "Global user instructions."
		if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte(content), 0644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}

		prompt := LoadPromptWithOptions(t.TempDir(), PromptOptions{})
		if !strings.Contains(prompt, content) {
			t.Error("prompt should include HOME AGENTS.md content")
		}
		if !strings.Contains(prompt, "<user-instructions>") {
			t.Error("prompt should wrap HOME AGENTS.md in user-instructions tags")
		}
	})

	t.Run("loads XDG_CONFIG_HOME hew AGENTS.md", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		configDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", configDir)

		hewConfigDir := filepath.Join(configDir, "hew")
		if err := os.MkdirAll(hewConfigDir, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		content := "Hew-specific config instructions."
		if err := os.WriteFile(filepath.Join(hewConfigDir, "AGENTS.md"), []byte(content), 0644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}

		prompt := LoadPromptWithOptions(t.TempDir(), PromptOptions{})
		if !strings.Contains(prompt, content) {
			t.Error("prompt should include XDG config AGENTS.md content")
		}
		if !strings.Contains(prompt, "<config-instructions>") {
			t.Error("prompt should wrap config AGENTS.md in config-instructions tags")
		}
	})

	t.Run("loads all three layers", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		configDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", configDir)
		projectDir := t.TempDir()

		// HOME AGENTS.md
		homeContent := "Home layer."
		if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte(homeContent), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}

		// XDG config AGENTS.md
		hewConfigDir := filepath.Join(configDir, "hew")
		if err := os.MkdirAll(hewConfigDir, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		configContent := "Config layer."
		if err := os.WriteFile(filepath.Join(hewConfigDir, "AGENTS.md"), []byte(configContent), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}

		// Project AGENTS.md
		projectContent := "Project layer."
		if err := os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte(projectContent), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}

		prompt := LoadPromptWithOptions(projectDir, PromptOptions{})
		if !strings.Contains(prompt, homeContent) {
			t.Error("prompt should include home layer")
		}
		if !strings.Contains(prompt, configContent) {
			t.Error("prompt should include config layer")
		}
		if !strings.Contains(prompt, projectContent) {
			t.Error("prompt should include project layer")
		}

		// Verify ordering: home < config < project
		homeIdx := strings.Index(prompt, homeContent)
		configIdx := strings.Index(prompt, configContent)
		projectIdx := strings.Index(prompt, projectContent)
		if homeIdx >= configIdx {
			t.Error("home layer should appear before config layer")
		}
		if configIdx >= projectIdx {
			t.Error("config layer should appear before project layer")
		}
	})

	t.Run("XDG_CONFIG_HOME defaults to HOME/.config", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")

		hewConfigDir := filepath.Join(home, ".config", "hew")
		if err := os.MkdirAll(hewConfigDir, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		content := "Default config path."
		if err := os.WriteFile(filepath.Join(hewConfigDir, "AGENTS.md"), []byte(content), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}

		prompt := LoadPromptWithOptions(t.TempDir(), PromptOptions{})
		if !strings.Contains(prompt, content) {
			t.Error("prompt should load from $HOME/.config/hew/AGENTS.md when XDG_CONFIG_HOME is unset")
		}
	})

	t.Run("missing layers are silently skipped", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())

		prompt := LoadPromptWithOptions(t.TempDir(), PromptOptions{})
		if strings.Contains(prompt, "<user-instructions>") {
			t.Error("should not have user-instructions when HOME AGENTS.md is missing")
		}
		if strings.Contains(prompt, "<config-instructions>") {
			t.Error("should not have config-instructions when config AGENTS.md is missing")
		}
		if strings.Contains(prompt, "<project-instructions>") {
			t.Error("should not have project-instructions when project AGENTS.md is missing")
		}
	})
}
