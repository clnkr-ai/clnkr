package clnkrapp

import (
	"strings"
	"testing"
)

func TestSharedHelpFragments(t *testing.T) {
	for name, text := range map[string]string{
		"provider":      ProviderOptionsUsage,
		"system prompt": SystemPromptUsage,
		"environment":   EnvironmentUsage,
	} {
		if strings.TrimSpace(text) == "" {
			t.Fatalf("%s help is empty", name)
		}
		for _, line := range strings.Split(text, "\n") {
			if len(line) > 79 {
				t.Fatalf("%s help line length = %d, want <= 79: %q", name, len(line), line)
			}
		}
	}
	for _, want := range []string{
		"--model string",
		"--base-url string",
		"--provider-api string",
		"--act-protocol string",
		"--effort string",
		"--max-output-tokens int",
		"--thinking-budget-tokens int",
	} {
		if !strings.Contains(ProviderOptionsUsage, want) {
			t.Fatalf("provider help missing %q", want)
		}
	}
	for _, want := range []string{
		"--no-system-prompt",
		"--system-prompt-append string",
		"--dump-system-prompt",
	} {
		if !strings.Contains(SystemPromptUsage, want) {
			t.Fatalf("system prompt help missing %q", want)
		}
	}
	for _, want := range []string{
		"CLNKR_API_KEY",
		"CLNKR_PROVIDER",
		"CLNKR_PROVIDER_API",
		"CLNKR_MODEL",
		"CLNKR_BASE_URL",
	} {
		if !strings.Contains(EnvironmentUsage, want) {
			t.Fatalf("environment help missing %q", want)
		}
	}
}
