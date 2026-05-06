package clnkrapp

import (
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
		"CLNKR_PROVIDER_API": "auto",
		"CLNKR_MODEL":        "claude-sonnet-4-6",
		"CLNKR_BASE_URL":     "https://api.example.test",
		"CLNKR_ACT_PROTOCOL": "tool-calls",
	} {
		if got[key] != want {
			t.Fatalf("%s = %q, want %q", key, got[key], want)
		}
	}
	if _, ok := got["malformed"]; ok {
		t.Fatalf("malformed env entry was preserved: %#v", got)
	}
}
