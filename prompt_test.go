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
		if strings.Contains(prompt, `"turn":{`) {
			t.Error("prompt should not teach provider wire wrappers")
		}
		if strings.Contains(prompt, `top-level "turn" field`) {
			t.Error("prompt should not require a provider-specific top-level turn field")
		}
		if !strings.Contains(prompt, `Set type to exactly one of "act", "clarify", or "done".`) {
			t.Error("prompt should describe type explicitly")
		}
		if !strings.Contains(prompt, `"verification"`) {
			t.Error("prompt should require done verification")
		}
		if !strings.Contains(prompt, `"known_risks"`) {
			t.Error("prompt should require known_risks")
		}
		if !strings.Contains(prompt, "verified, partially_verified, or not_verified") {
			t.Error("prompt should describe verification statuses")
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
		if !strings.Contains(prompt, "Each bash.commands item is an object whose command value is a JSON string") {
			t.Error("prompt should describe commands[] items as objects with command strings")
		}
		if !strings.Contains(prompt, canonicalPromptActExample) {
			t.Error("prompt should teach the canonical commands[] act shape")
		}
		if !strings.Contains(prompt, `Include reasoning in every response; use a string when it helps and null when you have nothing to add.`) {
			t.Error("prompt should require the reasoning field even when it is null")
		}
		if strings.Contains(prompt, `{"type":"clarify"`) {
			t.Error("prompt should not condition the model with a separate clarify object example")
		}
		if strings.Contains(prompt, `{"type":"done","summary"`) {
			t.Error("prompt should not condition the model with a separate done object example")
		}
		if !strings.Contains(prompt, "Exported environment changes and environment updates from source or . also persist between commands.") {
			t.Error("prompt should explain shell env persistence")
		}
		if !strings.Contains(prompt, "The host may require approval before running commands.") {
			t.Error("prompt should mention host approval mode")
		}
		if strings.Contains(prompt, "Use 1 or 2 commands in each act turn.") {
			t.Error("prompt should not impose an arbitrary command-count cap")
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
		if !strings.Contains(prompt, `You may also receive JSON host state messages such as {"type":"state","source":"clnkr","cwd":"/repo"} and resource_state`) {
			t.Error("prompt should explain host state messages")
		}
		if !strings.Contains(prompt, "commands_used") || !strings.Contains(prompt, "commands_remaining") || !strings.Contains(prompt, "model_turns_used") {
			t.Error("prompt should explain resource state fields")
		}
		if strings.Contains(prompt, "[command]") || strings.Contains(prompt, "[stdout]") || strings.Contains(prompt, "[command_feedback]") {
			t.Error("prompt should not describe bracketed command-result sections")
		}
		if !strings.Contains(prompt, `"stdout"`) || !strings.Contains(prompt, `"stderr"`) || !strings.Contains(prompt, `"outcome"`) {
			t.Error("prompt should describe structured command-result JSON")
		}
		if !strings.Contains(prompt, `"observation" metadata when stdout/stderr were compressed`) {
			t.Error("prompt should describe compressed command-result observation metadata")
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
		if !strings.Contains(prompt, "If you receive a [protocol_error] block, fix your format and respond with exactly one valid turn object.") {
			t.Error("prompt should keep the base protocol_error formatting guidance in the protocol section")
		}
		if !strings.Contains(prompt, "If you receive a [protocol_error] block, your previous response was rejected and no command ran.") {
			t.Error("prompt should explain protocol_error blocks in general runtime terms")
		}
		if !strings.Contains(prompt, "If you receive a [protocol_error] block, your previous response was rejected and no command ran. Fix the format and respond with exactly one valid turn object.") {
			t.Error("prompt should keep the recovery section aligned with the canonical-turn requirement")
		}
		if strings.Contains(prompt, "legacy parser path") {
			t.Error("prompt should not scope protocol_error guidance to legacy parser paths")
		}
	})

	t.Run("unattended prompt omits clarify", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		prompt := LoadPromptWithOptions(dir, PromptOptions{Unattended: true})
		if strings.Contains(prompt, "clarify") {
			t.Fatalf("unattended prompt contains clarify: %q", prompt)
		}
		if !strings.Contains(prompt, `Set type to exactly one of "act" or "done".`) {
			t.Fatalf("unattended prompt should describe act/done contract")
		}
		if !strings.Contains(prompt, `"verification"`) || !strings.Contains(prompt, `"known_risks"`) {
			t.Fatalf("unattended prompt should require structured done verification")
		}
	})

	t.Run("unattended prompt teaches resource awareness", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		prompt := LoadPromptWithOptions(t.TempDir(), PromptOptions{Unattended: true})
		for _, want := range []string{
			"resource_state",
			"commands_used",
			"commands_remaining",
			"model_turns_used",
			"Prefer cheap inspection before expensive builds, downloads, training runs, or brute force.",
			"When resources are low, produce the best verifiable artifact you can and finish.",
		} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("unattended prompt missing %q", want)
			}
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

func TestPromptExamplesParseSuccessfully(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	prompt := LoadPromptWithOptions(dir, PromptOptions{})

	if count := strings.Count(prompt, canonicalPromptActExample); count != 1 {
		t.Fatalf("prompt should contain exactly 1 canonical turn example, found %d", count)
	}
	got, err := CanonicalTurnJSON(&ActTurn{
		Bash: BashBatch{Commands: []BashAction{
			{Command: "ls -la /tmp"},
			{Command: "sed -n '1,40p' README.md"},
		}},
		Reasoning: "Inspect the directory and the file before deciding.",
	})
	if err != nil {
		t.Fatalf("CanonicalTurnJSON(example): %v", err)
	}
	if got != canonicalPromptActExample {
		t.Fatalf("canonical prompt example drifted: got %q want %q", got, canonicalPromptActExample)
	}
	if strings.Contains(prompt, `{"turn":{"type":"act","bash":{"command":"grep 'A\\\\|B' file.txt","workdir":null}`) {
		t.Error("prompt should teach shell escaping with a JSON string example, not a wrapped turn object")
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

	t.Run("project dir matching HOME is not loaded twice", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())

		content := "Home instructions should only appear once."
		if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte(content), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}

		prompt := LoadPromptWithOptions(home, PromptOptions{})
		if got := strings.Count(prompt, content); got != 1 {
			t.Fatalf("content appeared %d times, want 1", got)
		}
		if !strings.Contains(prompt, "<user-instructions>") {
			t.Error("prompt should keep the HOME layer when project dir matches HOME")
		}
		if strings.Contains(prompt, "<project-instructions>\n"+content+"\n</project-instructions>") {
			t.Error("prompt should not duplicate HOME AGENTS.md as project instructions")
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
