package clnkr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPromptWithOptions_BasePrompt(t *testing.T) {
	t.Run("base prompt teaches JSON protocol", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		prompt := LoadPromptWithOptions(dir, PromptOptions{})
		if !strings.Contains(prompt, `"type":"act"`) {
			t.Error("prompt should teach the json protocol")
		}
		if strings.Contains(prompt, "```bash") {
			t.Error("prompt should not instruct fenced bash blocks")
		}
		if strings.Contains(prompt, "<done/>") {
			t.Error("prompt should not reference done signal")
		}
		if !strings.Contains(prompt, `grep 'A\\\\|B' file.txt`) {
			t.Error("prompt should teach shell escaping inside JSON")
		}
		if !strings.Contains(prompt, "do not persist unless you chain them into the same command") {
			t.Error("prompt should explain stateless shell env behavior")
		}
	})

	t.Run("appends AGENTS.md when present", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
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
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		prompt := LoadPromptWithOptions(dir, PromptOptions{})
		if prompt == "" {
			t.Error("prompt should not be empty without AGENTS.md")
		}
	})
}

// extractAllJSONObjects finds all top-level JSON objects in text.
func extractAllJSONObjects(text string) []string {
	var objects []string
	remaining := text
	for {
		jsonStr, err := extractJSON(remaining)
		if err != nil {
			break
		}
		objects = append(objects, jsonStr)
		idx := strings.Index(remaining, jsonStr)
		remaining = remaining[idx+len(jsonStr):]
	}
	return objects
}

func TestPromptExamplesParseSuccessfully(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	prompt := LoadPromptWithOptions(dir, PromptOptions{})

	examples := extractAllJSONObjects(prompt)
	if len(examples) < 3 {
		t.Fatalf("prompt should contain at least 3 turn examples, found %d", len(examples))
	}
	for i, ex := range examples {
		_, err := ParseTurn(ex)
		if err != nil {
			t.Errorf("example %d failed ParseTurn: %v\n  input: %s", i, err, ex)
		}
	}
}

func TestLoadPromptWithOptions(t *testing.T) {
	t.Run("default includes base prompt and AGENTS.md", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		content := "Project-specific instructions here."
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}

		prompt := LoadPromptWithOptions(dir, PromptOptions{})
		if !strings.Contains(prompt, `"type":"act"`) {
			t.Error("default prompt should contain JSON protocol")
		}
		if !strings.Contains(prompt, content) {
			t.Error("default prompt should include AGENTS.md content")
		}
	})

	t.Run("OmitSystemPrompt returns empty string", func(t *testing.T) {
		dir := t.TempDir()
		content := "Should not appear."
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}

		prompt := LoadPromptWithOptions(dir, PromptOptions{OmitSystemPrompt: true})
		if prompt != "" {
			t.Errorf("OmitSystemPrompt should return empty string, got %d bytes", len(prompt))
		}
	})

	t.Run("SystemPromptAppend appends after AGENTS.md layers", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())

		dir := t.TempDir()
		projectContent := "Project layer content."
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(projectContent), 0644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}

		appendText := "Extra instructions appended at the end."
		prompt := LoadPromptWithOptions(dir, PromptOptions{SystemPromptAppend: appendText})

		if !strings.Contains(prompt, appendText) {
			t.Error("prompt should contain appended text")
		}

		// Verify append comes after project instructions
		projectIdx := strings.Index(prompt, projectContent)
		appendIdx := strings.Index(prompt, appendText)
		if projectIdx >= appendIdx {
			t.Error("appended text should appear after project instructions")
		}

		// Verify it's at the very end
		if !strings.HasSuffix(prompt, appendText) {
			t.Error("appended text should be at the end of the prompt")
		}
	})

	t.Run("OmitSystemPrompt with SystemPromptAppend returns only the appended text", func(t *testing.T) {
		dir := t.TempDir()
		prompt := LoadPromptWithOptions(dir, PromptOptions{
			OmitSystemPrompt:   true,
			SystemPromptAppend: "custom prompt only",
		})
		if prompt != "custom prompt only" {
			t.Errorf("expected only appended text, got %q", prompt)
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

	t.Run("loads XDG_CONFIG_HOME clnkr AGENTS.md", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		configDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", configDir)

		clnkrConfigDir := filepath.Join(configDir, "clnkr")
		if err := os.MkdirAll(clnkrConfigDir, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		content := "Clnkr-specific config instructions."
		if err := os.WriteFile(filepath.Join(clnkrConfigDir, "AGENTS.md"), []byte(content), 0644); err != nil {
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
		clnkrConfigDir := filepath.Join(configDir, "clnkr")
		if err := os.MkdirAll(clnkrConfigDir, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		configContent := "Config layer."
		if err := os.WriteFile(filepath.Join(clnkrConfigDir, "AGENTS.md"), []byte(configContent), 0644); err != nil {
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

		clnkrConfigDir := filepath.Join(home, ".config", "clnkr")
		if err := os.MkdirAll(clnkrConfigDir, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		content := "Default config path."
		if err := os.WriteFile(filepath.Join(clnkrConfigDir, "AGENTS.md"), []byte(content), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}

		prompt := LoadPromptWithOptions(t.TempDir(), PromptOptions{})
		if !strings.Contains(prompt, content) {
			t.Error("prompt should load from $HOME/.config/clnkr/AGENTS.md when XDG_CONFIG_HOME is unset")
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
