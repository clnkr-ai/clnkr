package clnkrapp

import (
	"strings"
	"testing"
)

func TestLoadStartupPromptDumpAutoDoesNotRequireAPIKey(t *testing.T) {
	isolatePromptEnv(t)

	prompt, err := LoadStartupPrompt(StartupInputs{
		CWD:              t.TempDir(),
		Provider:         "openai",
		ProviderAPI:      "openai-responses",
		Model:            "gpt-5",
		DumpSystemPrompt: true,
		Env:              func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("LoadStartupPrompt: %v", err)
	}
	if !strings.Contains(prompt, "call the bash tool") {
		t.Fatalf("prompt missing tool-calls instructions: %q", prompt)
	}
}

func TestLoadStartupPromptConcreteInlineNeedsNoProviderContext(t *testing.T) {
	isolatePromptEnv(t)

	prompt, err := LoadStartupPrompt(StartupInputs{
		CWD:              t.TempDir(),
		ActProtocol:      "clnkr-inline",
		DumpSystemPrompt: true,
		Env:              func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("LoadStartupPrompt: %v", err)
	}
	if !strings.Contains(prompt, "Every response must be exactly one JSON object") {
		t.Fatalf("prompt missing inline instructions: %q", prompt)
	}
}

func TestPrepareStartupMissingAPIKeyIsClassified(t *testing.T) {
	_, err := PrepareStartup(StartupInputs{
		CWD:      t.TempDir(),
		Provider: "openai",
		Model:    "gpt-5",
		Env:      func(string) string { return "" },
	})
	if err == nil {
		t.Fatal("PrepareStartup succeeded, want missing API key error")
	}
	if !IsMissingAPIKey(err) {
		t.Fatalf("IsMissingAPIKey(%v) = false, want true", err)
	}
}

func TestPrepareStartupBuildsAgentDriverAndMetadata(t *testing.T) {
	isolatePromptEnv(t)

	startup, err := PrepareStartup(StartupInputs{
		CWD:      t.TempDir(),
		Version:  "test-version",
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
		Env: envMap(map[string]string{
			"CLNKR_API_KEY": "test-key",
		}),
		Environ: []string{"PATH=/usr/bin"},
	})
	if err != nil {
		t.Fatalf("PrepareStartup: %v", err)
	}
	if startup.Agent == nil {
		t.Fatal("Agent = nil")
	}
	if startup.Driver == nil {
		t.Fatal("Driver = nil")
	}
	if startup.Metadata.ClnkrVersion != "test-version" {
		t.Fatalf("ClnkrVersion = %q, want test-version", startup.Metadata.ClnkrVersion)
	}
	if startup.Agent.ActProtocol != startup.Metadata.ActProtocol {
		t.Fatalf("agent ActProtocol = %q, want %q", startup.Agent.ActProtocol, startup.Metadata.ActProtocol)
	}
}

func TestPrepareStartupUsesUnattendedPrompt(t *testing.T) {
	isolatePromptEnv(t)

	startup, err := PrepareStartup(StartupInputs{
		CWD:         t.TempDir(),
		Provider:    "anthropic",
		Model:       "claude-sonnet-4-6",
		ActProtocol: "clnkr-inline",
		Unattended:  true,
		Env: envMap(map[string]string{
			"CLNKR_API_KEY": "test-key",
		}),
	})
	if err != nil {
		t.Fatalf("PrepareStartup: %v", err)
	}
	if strings.Contains(startup.SystemPrompt, "clarify") {
		t.Fatalf("unattended prompt contains clarify: %q", startup.SystemPrompt)
	}
	if !strings.Contains(startup.SystemPrompt, `Set type to exactly one of "act" or "done".`) {
		t.Fatalf("unattended prompt missing act/done contract: %q", startup.SystemPrompt)
	}
}

func TestPrepareStartupDefaultPromptAllowsClarify(t *testing.T) {
	isolatePromptEnv(t)

	startup, err := PrepareStartup(StartupInputs{
		CWD:      t.TempDir(),
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
		Env: envMap(map[string]string{
			"CLNKR_API_KEY": "test-key",
		}),
	})
	if err != nil {
		t.Fatalf("PrepareStartup: %v", err)
	}
	if !strings.Contains(startup.SystemPrompt, "clarify") {
		t.Fatalf("default prompt missing clarify: %q", startup.SystemPrompt)
	}
}

func envMap(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func isolatePromptEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}
