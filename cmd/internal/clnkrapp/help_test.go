package clnkrapp

import (
	"strings"
	"testing"
)

func TestSharedHelpFragments(t *testing.T) {
	for _, fragment := range []struct {
		name  string
		text  string
		wants []string
	}{
		{
			name: "provider",
			text: ProviderOptionsUsage,
			wants: []string{
				"--model string",
				"--base-url string",
				"--provider-api string",
				"--act-protocol string",
				"auto|clnkr-inline|tool-calls",
				"--effort string",
				"--max-output-tokens int",
				"--thinking-budget-tokens int",
			},
		},
		{
			name: "system prompt",
			text: SystemPromptUsage,
			wants: []string{
				"--no-system-prompt",
				"--system-prompt-append string",
				"--dump-system-prompt",
			},
		},
		{
			name: "environment",
			text: EnvironmentUsage,
			wants: []string{
				"CLNKR_API_KEY",
				"CLNKR_PROVIDER",
				"CLNKR_PROVIDER_API",
				"CLNKR_MODEL",
				"CLNKR_BASE_URL",
				"CLNKR_ACT_PROTOCOL",
			},
		},
	} {
		t.Run(fragment.name, func(t *testing.T) {
			if strings.TrimSpace(fragment.text) == "" {
				t.Fatal("help is empty")
			}
			for _, line := range strings.Split(fragment.text, "\n") {
				if len(line) > 79 {
					t.Fatalf("line length = %d, want <= 79: %q", len(line), line)
				}
			}
			for _, want := range fragment.wants {
				if !strings.Contains(fragment.text, want) {
					t.Fatalf("help missing %q", want)
				}
			}
		})
	}
}
