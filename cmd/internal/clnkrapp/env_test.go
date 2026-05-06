package clnkrapp

import (
	"flag"
	"testing"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/providerconfig"
	providerdomain "github.com/clnkr-ai/clnkr/internal/providers/providerconfig"
)

func TestCommandEnvFromProviderConfig(t *testing.T) {
	got := CommandEnvFromProviderConfig(providerconfig.ResolvedProviderConfig{
		Provider:    providerdomain.ProviderAnthropic,
		ProviderAPI: providerdomain.ProviderAPIAuto,
		Model:       "claude-sonnet-4-6",
		BaseURL:     "https://api.example.test",
		ActProtocol: clnkr.ActProtocolToolCalls,
	}, []string{
		"PATH=/usr/bin",
		"CLNKR_PROVIDER=old",
		"malformed",
	})

	for key, want := range map[string]string{
		"PATH":               "/usr/bin",
		"CLNKR_PROVIDER":     "anthropic",
		"CLNKR_MODEL":        "claude-sonnet-4-6",
		"CLNKR_BASE_URL":     "https://api.example.test",
		"CLNKR_ACT_PROTOCOL": "tool-calls",
	} {
		if got[key] != want {
			t.Fatalf("%s = %q, want %q", key, got[key], want)
		}
	}
	if _, ok := got["CLNKR_PROVIDER_API"]; ok {
		t.Fatalf("CLNKR_PROVIDER_API was preserved for Anthropic: %#v", got)
	}
	if _, ok := got["malformed"]; ok {
		t.Fatalf("malformed env entry was preserved: %#v", got)
	}
}

func TestActProtocolFlagValueUsesEnvWhenFlagUnset(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flagValue := flags.String("act-protocol", "clnkr-inline", "")
	if err := flags.Parse(nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	got := ActProtocolFlagValue(flags, *flagValue, func(key string) string {
		if key == "CLNKR_ACT_PROTOCOL" {
			return "tool-calls"
		}
		return ""
	})
	if got != "tool-calls" {
		t.Fatalf("act protocol = %q, want tool-calls", got)
	}
}

func TestActProtocolFlagValuePrefersFlag(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flagValue := flags.String("act-protocol", "clnkr-inline", "")
	if err := flags.Parse([]string{"--act-protocol", "tool-calls"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	got := ActProtocolFlagValue(flags, *flagValue, func(string) string {
		return "clnkr-inline"
	})
	if got != "tool-calls" {
		t.Fatalf("act protocol = %q, want tool-calls", got)
	}
}

func TestCommandEnvFromProviderConfigOpenAIProviderAPI(t *testing.T) {
	got := CommandEnvFromProviderConfig(providerconfig.ResolvedProviderConfig{
		Provider:    providerdomain.ProviderOpenAI,
		ProviderAPI: providerdomain.ProviderAPIOpenAIResponses,
		Model:       "gpt-test",
		BaseURL:     "https://api.example.test/v1",
	}, []string{"CLNKR_PROVIDER_API=old"})

	if got["CLNKR_PROVIDER_API"] != "openai-responses" {
		t.Fatalf("CLNKR_PROVIDER_API = %q, want openai-responses", got["CLNKR_PROVIDER_API"])
	}
}

func TestCommandEnvFromProviderConfigAnthropicResolves(t *testing.T) {
	env := CommandEnvFromProviderConfig(providerconfig.ResolvedProviderConfig{
		Provider:    providerdomain.ProviderAnthropic,
		ProviderAPI: providerdomain.ProviderAPIAuto,
		Model:       "claude-sonnet-4-6",
		BaseURL:     "https://api.example.test",
		ActProtocol: clnkr.ActProtocolClnkrInline,
	}, []string{
		"CLNKR_API_KEY=test-key",
		"CLNKR_PROVIDER_API=auto",
	})

	_, err := providerconfig.ResolveConfig(providerconfig.Inputs{}, func(key string) string {
		return env[key]
	})
	if err != nil {
		t.Fatalf("ResolveConfig seeded from Anthropic command env: %v", err)
	}
}
