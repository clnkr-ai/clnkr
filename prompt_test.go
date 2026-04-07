package clnkr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr/turnjson"
)

func TestLoadPromptWithOptions_BasePrompt(t *testing.T) {
	t.Run("base prompt teaches JSON protocol", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		prompt := LoadPromptWithOptions(dir, PromptOptions{})
		expectedActExample := turnjson.MustWireActJSON(
			[]turnjson.WireCommand{
				{Command: "ls -la /tmp", Workdir: nil},
				{Command: "sed -n '1,40p' README.md", Workdir: nil},
			},
			stringPtr("Inspect the directory and the file before deciding."),
		)
		if !strings.Contains(prompt, `"type":"act"`) {
			t.Error("prompt should teach the json protocol")
		}
		if !strings.Contains(prompt, `"turn":{`) {
			t.Error("prompt should teach the wrapped provider wire protocol")
		}
		if !strings.Contains(prompt, `top-level "turn" field`) {
			t.Error("prompt should require a single top-level turn field")
		}
		if !strings.Contains(prompt, `turn.type`) {
			t.Error("prompt should describe turn.type explicitly")
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
		if !strings.Contains(prompt, "Each turn.bash.commands item is an object whose command value is a JSON string") {
			t.Error("prompt should describe commands[] items as objects with command strings")
		}
		if !strings.Contains(prompt, expectedActExample) {
			t.Error("prompt should teach the wrapped commands[] act wire shape")
		}
		if !strings.Contains(prompt, `Include turn.reasoning in every response; use a string when it helps and null when you have nothing to add.`) {
			t.Error("prompt should require the reasoning field even when it is null")
		}
		if strings.Contains(prompt, `"turn":{"type":"clarify"`) {
			t.Error("prompt should not condition the model with a separate wrapped clarify object example")
		}
		if strings.Contains(prompt, `"turn":{"type":"done"`) {
			t.Error("prompt should not condition the model with a separate wrapped done object example")
		}
		if !strings.Contains(prompt, "Exported environment changes and environment updates from source or . also persist between commands.") {
			t.Error("prompt should explain shell env persistence")
		}
		if !strings.Contains(prompt, "The host may require approval before running commands.") {
			t.Error("prompt should mention host approval mode")
		}
		if !strings.Contains(prompt, "Use 1 or 2 commands in each act turn.") {
			t.Error("prompt should bound act turns to 1 or 2 commands")
		}
		if !strings.Contains(prompt, "Batch only mechanical follow-up steps that do not require interpreting earlier command output.") {
			t.Error("prompt should limit batching to mechanical follow-up steps")
		}
		if !strings.Contains(prompt, "Do not emit multiple JSON objects in one response.") {
			t.Error("prompt should explicitly forbid multiple JSON objects")
		}
		if !strings.Contains(prompt, "Do not emit an act turn and a done turn together.") {
			t.Error("prompt should explicitly forbid act-plus-done replies")
		}
		if !strings.Contains(prompt, "When the user refers to the current repo, current directory, or cwd, work in the current directory without adding cd.") {
			t.Error("prompt should keep cwd tasks in the current directory")
		}
		if !strings.Contains(prompt, "If the user names a file or path, inspect that exact path first.") {
			t.Error("prompt should prioritize exact user-named paths")
		}
		if !strings.Contains(prompt, "You may also receive a [state] block containing JSON host execution state such as the current working directory.") {
			t.Error("prompt should explain host state messages")
		}
		if !strings.Contains(prompt, "[command_feedback]") {
			t.Error("prompt should mention command feedback")
		}
		if !strings.Contains(prompt, "clean pre-command git baseline") {
			t.Error("prompt should explain feedback scope")
		}
		if !strings.Contains(prompt, `do not emit "done" until you have completed the change with at least one "act" turn`) {
			t.Error("prompt should require an act turn before done for workspace-changing tasks")
		}
		if !strings.Contains(prompt, "Never claim to have created, modified, or verified something unless that happened through a prior command result in this conversation.") {
			t.Error("prompt should forbid unsupported completion claims")
		}
		if !strings.Contains(prompt, "If a verification command shows the result does not match the request exactly, issue another \"act\" turn to fix it instead of emitting \"done\".") {
			t.Error("prompt should require a corrective act turn after failed verification")
		}
		if !strings.Contains(prompt, "For exact literal text writes, prefer quoted literals such as printf 'hello\\n' > note.txt instead of shell-fragile format strings.") {
			t.Error("prompt should teach a stable literal-text write pattern")
		}
		if strings.Contains(prompt, "check status before and after making changes") {
			t.Error("prompt should not recommend git status before and after edits")
		}
		if strings.Contains(prompt, "For targeted edits use sed.") {
			t.Error("prompt should not default targeted edits to sed")
		}
		if !strings.Contains(prompt, "When writing plain-text file contents like hello, write a newline-terminated line unless the user explicitly asks for no trailing newline or exact byte-for-byte content.") {
			t.Error("prompt should default plain-text file writes to newline-terminated lines")
		}
		if !strings.Contains(prompt, "<protocol-error-recovery>") {
			t.Error("prompt should isolate protocol_error guidance in a dedicated recovery section")
		}
		if !strings.Contains(prompt, "If you receive a [protocol_error] block, fix your format and respond with exactly one valid wrapped turn object.") {
			t.Error("prompt should keep the base protocol_error formatting guidance in the protocol section")
		}
		if !strings.Contains(prompt, "If you receive a [protocol_error] block, your previous response was rejected and no command ran.") {
			t.Error("prompt should explain protocol_error blocks in general runtime terms")
		}
		if !strings.Contains(prompt, "If you receive a [protocol_error] block, your previous response was rejected and no command ran. Fix the format and respond with exactly one valid wrapped turn object.") {
			t.Error("prompt should keep the recovery section aligned with the wrapped-turn requirement")
		}
		if strings.Contains(prompt, "legacy parser path") {
			t.Error("prompt should not scope protocol_error guidance to legacy parser paths")
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
	if len(examples) != 1 {
		t.Fatalf("prompt should contain exactly 1 wrapped turn example, found %d", len(examples))
	}
	for i, ex := range examples {
		inner, ok, err := turnjson.ExtractTurnEnvelope(ex)
		if err != nil {
			t.Errorf("example %d failed envelope validation: %v\n  input: %s", i, err, ex)
			continue
		}
		if !ok {
			t.Errorf("example %d should use the wrapped provider wire shape\n  input: %s", i, ex)
			continue
		}
		_, err = ParseTurn(inner)
		if err != nil {
			t.Errorf("example %d failed ParseTurn: %v\n  input: %s", i, err, ex)
		}
	}
	if strings.Contains(prompt, `{"turn":{"type":"act","bash":{"command":"grep 'A\\\\|B' file.txt","workdir":null}`) {
		t.Error("prompt should teach shell escaping with a JSON string example, not a second wrapped turn object")
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
