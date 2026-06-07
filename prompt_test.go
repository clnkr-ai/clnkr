package clnkr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPromptWithOptions_BasePrompt(t *testing.T) {
	prompt := loadPromptWithCleanEnv(t, PromptOptions{})

	mustContainAll(t, prompt, []string{
		`"type":"act"`,
		`Set type to exactly one of "act", "clarify", or "done".`,
		`"verification"`,
		`"known_risks"`,
		"verified, partially_verified, or not_verified",
		`grep 'A\\\\|B' file.txt`,
		"Each bash.commands item is an object whose command value is a JSON string",
		canonicalPromptActExample,
		`Include reasoning in every response; use a string when it helps and null when you have nothing to add.`,
		"Exported environment changes and environment updates from source or . also persist between commands.",
		"The host may require approval before running commands.",
		"Batch only mechanical follow-up steps that do not require interpreting earlier command output.",
		"Do not emit multiple JSON objects in one response.",
		"Do not emit an act turn and a done turn together.",
		"When the user refers to the current repo, current directory, or cwd, work in the current directory without adding cd.",
		"If the user names a file or path, inspect that exact path first.",
		`You may also receive JSON host state messages such as {"type":"state","source":"clnkr","cwd":"/repo"} and resource_state`,
		"commands_used",
		"commands_remaining",
		"model_turns_used",
		`"stdout"`,
		`"stderr"`,
		`"outcome"`,
		`"observation" metadata when stdout/stderr were compressed`,
		"clean pre-command git baseline",
		`do not emit "done" until you have completed the change with at least one "act" turn`,
		"Never claim to have created, modified, or verified something unless that happened through a prior command result in this conversation.",
		"If a verification command shows the result does not match the request exactly, issue another \"act\" turn to fix it instead of emitting \"done\".",
		"For exact literal text writes, prefer quoted literals such as printf 'hello\\n' > note.txt instead of shell-fragile format strings.",
		"When writing plain-text file contents like hello, write a newline-terminated line unless the user explicitly asks for no trailing newline or exact byte-for-byte content.",
		"<protocol-error-recovery>",
		"If you receive a [protocol_error] block, fix your format and respond with exactly one valid turn object.",
		"If you receive a [protocol_error] block, your previous response was rejected and no command ran.",
		"If you receive a [protocol_error] block, your previous response was rejected and no command ran. Fix the format and respond with exactly one valid turn object.",
	})
	mustNotContainAny(t, prompt, []string{
		`"turn":{`,
		`top-level "turn" field`,
		"```bash",
		"<done/>",
		`{"type":"clarify"`,
		`{"type":"done","summary"`,
		"Use 1 or 2 commands in each act turn.",
		"[command]",
		"[stdout]",
		"[command_feedback]",
		"check status before and after making changes",
		"For targeted edits use sed.",
		"legacy parser path",
	})
}

func TestLoadPromptWithOptions_ClnkrdProcessAutonomy(t *testing.T) {
	tests := []struct {
		name string
		opts PromptOptions
		want []string
	}{
		{
			name: "inline protocol",
			want: []string{
				"<parallel-work>",
				"Bash is your only tool. You may use bash to run `clnkrd` as a machine-facing stdio JSONL process whenever it would materially reduce uncertainty, parallelize independent work, verify a claim, or explore a non-blocking question.",
				"If the user writes `/delegate ...`, treat it as prompt text asking you to launch `clnkrd` through bash for bounded machine-facing JSONL work; do not treat it as a host command.",
				"workdir=$(mktemp -d /tmp/clnkr-processes.$$.XXXXXX)",
				`clnkrd --event-log "$workdir/prompt-review/events.jsonl"`,
				`wait "$pid1" "$pid2"`,
				"Treat process output as evidence to evaluate, not proof.",
			},
		},
		{
			name: "tool-call protocol",
			opts: PromptOptions{ActProtocol: ActProtocolToolCalls},
			want: []string{
				"<parallel-work>",
				"Bash is your only tool. You may use bash to run `clnkrd` as a machine-facing stdio JSONL process whenever it would materially reduce uncertainty, parallelize independent work, verify a claim, or explore a non-blocking question.",
				"If the user writes `/delegate ...`, treat it as prompt text asking you to launch `clnkrd` through bash for bounded machine-facing JSONL work; do not treat it as a host command.",
				"workdir=$(mktemp -d /tmp/clnkr-processes.$$.XXXXXX)",
				`clnkrd --event-log "$workdir/docs-review/events.jsonl"`,
				"Do not use extra processes when a normal bash command, test, grep, or file read gives the answer directly.",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := loadPromptWithCleanEnv(t, tc.opts)
			mustContainAll(t, prompt, tc.want)
		})
	}
}

func TestLoadPromptWithOptions_UnattendedPrompt(t *testing.T) {
	prompt := loadPromptWithCleanEnv(t, PromptOptions{Unattended: true})

	if strings.Contains(prompt, "clarify") {
		t.Fatalf("unattended prompt contains clarify: %q", prompt)
	}
	mustContainAll(t, prompt, []string{
		`Set type to exactly one of "act" or "done".`,
		`"verification"`,
		`"known_risks"`,
		"resource_state",
		"commands_used",
		"commands_remaining",
		"model_turns_used",
		"Prefer cheap inspection before expensive builds, downloads, training runs, or brute force.",
		"When resources are low, produce the best verifiable artifact you can and finish.",
	})
}

func TestPromptExamplesParseSuccessfully(t *testing.T) {
	prompt := loadPromptWithCleanEnv(t, PromptOptions{})

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
	if strings.Contains(
		prompt,
		`{"turn":{"type":"act","bash":{"command":"grep 'A\\\\|B' file.txt","workdir":null}`,
	) {
		t.Error(
			"prompt should teach shell escaping with a JSON string example, not a wrapped turn object",
		)
	}
}

func TestLoadPromptWithOptions_Options(t *testing.T) {
	t.Run("OmitSystemPrompt returns empty string", func(t *testing.T) {
		dir := t.TempDir()
		writeAgents(t, dir, "Should not appear.")

		prompt := LoadPromptWithOptions(dir, PromptOptions{OmitSystemPrompt: true})
		if prompt != "" {
			t.Errorf("OmitSystemPrompt should return empty string, got %d bytes", len(prompt))
		}
	})

	t.Run(
		"OmitSystemPrompt with SystemPromptAppend returns only the appended text",
		func(t *testing.T) {
			prompt := LoadPromptWithOptions(t.TempDir(), PromptOptions{
				OmitSystemPrompt:   true,
				SystemPromptAppend: "custom prompt only",
			})
			if prompt != "custom prompt only" {
				t.Errorf("expected only appended text, got %q", prompt)
			}
		},
	)

	t.Run("SystemPromptAppend appends after AGENTS.md layers", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())

		dir := t.TempDir()
		projectContent := "Project layer content."
		writeAgents(t, dir, projectContent)

		appendText := "Extra instructions appended at the end."
		prompt := LoadPromptWithOptions(dir, PromptOptions{SystemPromptAppend: appendText})

		mustContainAll(t, prompt, []string{projectContent, appendText})
		projectIdx := strings.Index(prompt, projectContent)
		appendIdx := strings.Index(prompt, appendText)
		if projectIdx >= appendIdx {
			t.Error("appended text should appear after project instructions")
		}
		if !strings.HasSuffix(prompt, appendText) {
			t.Error("appended text should be at the end of the prompt")
		}
	})
}

func TestLoadPromptWithOptions_LayeredAgentsMD(t *testing.T) {
	t.Run("loads tagged HOME, config, and project layers in order", func(t *testing.T) {
		home := t.TempDir()
		configDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", configDir)

		homeContent := "Home layer."
		configContent := "Config layer."
		projectContent := "Project layer."
		writeAgents(t, home, homeContent)
		writeConfigAgents(t, configDir, configContent)
		writeAgents(t, projectDir, projectContent)

		prompt := LoadPromptWithOptions(projectDir, PromptOptions{})
		mustContainAll(t, prompt, []string{
			homeContent,
			"<user-instructions>",
			configContent,
			"<config-instructions>",
			projectContent,
			"<project-instructions>",
		})

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
		writeAgents(t, home, content)

		prompt := LoadPromptWithOptions(home, PromptOptions{})
		if got := strings.Count(prompt, content); got != 1 {
			t.Fatalf("content appeared %d times, want 1", got)
		}
		if !strings.Contains(prompt, "<user-instructions>") {
			t.Error("prompt should keep the HOME layer when project dir matches HOME")
		}
		if strings.Contains(
			prompt,
			"<project-instructions>\n"+content+"\n</project-instructions>",
		) {
			t.Error("prompt should not duplicate HOME AGENTS.md as project instructions")
		}
	})

	t.Run("XDG_CONFIG_HOME defaults to HOME/.config", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")

		content := "Default config path."
		writeConfigAgents(t, filepath.Join(home, ".config"), content)

		prompt := LoadPromptWithOptions(t.TempDir(), PromptOptions{})
		if !strings.Contains(prompt, content) {
			t.Error(
				"prompt should load from $HOME/.config/clnkr/AGENTS.md when XDG_CONFIG_HOME is unset",
			)
		}
	})

	t.Run("missing layers are silently skipped", func(t *testing.T) {
		prompt := loadPromptWithCleanEnv(t, PromptOptions{})
		mustNotContainAny(t, prompt, []string{
			"<user-instructions>",
			"<config-instructions>",
			"<project-instructions>",
		})
	})
}

func loadPromptWithCleanEnv(t *testing.T, opts PromptOptions) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return LoadPromptWithOptions(dir, opts)
}

func writeAgents(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
}

func writeConfigAgents(t *testing.T, configDir, content string) {
	t.Helper()
	clnkrConfigDir := filepath.Join(configDir, "clnkr")
	if err := os.MkdirAll(clnkrConfigDir, 0755); err != nil {
		t.Fatalf("mkdir config AGENTS.md dir: %v", err)
	}
	writeAgents(t, clnkrConfigDir, content)
}

func mustContainAll(t *testing.T, text string, wants []string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func mustNotContainAny(t *testing.T, text string, unwanted []string) {
	t.Helper()
	for _, s := range unwanted {
		if strings.Contains(text, s) {
			t.Fatalf("contains unwanted %q", s)
		}
	}
}
