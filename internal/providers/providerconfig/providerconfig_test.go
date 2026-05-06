package providerconfig

import (
	"strings"
	"testing"
)

func TestSupportsBashToolCalls(t *testing.T) {
	tests := []struct {
		name        string
		provider    Provider
		providerAPI ProviderAPI
		want        bool
	}{
		{name: "anthropic auto", provider: ProviderAnthropic, providerAPI: ProviderAPIAuto, want: true},
		{name: "openai responses", provider: ProviderOpenAI, providerAPI: ProviderAPIOpenAIResponses, want: true},
		{name: "openai chat completions", provider: ProviderOpenAI, providerAPI: ProviderAPIOpenAIChatCompletions, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SupportsBashToolCalls(tt.provider, tt.providerAPI)
			if got != tt.want {
				t.Fatalf("SupportsBashToolCalls(%q, %q) = %v, want %v", tt.provider, tt.providerAPI, got, tt.want)
			}
		})
	}
}

func TestValidateRequestOptionsRejectsUnsupportedBashToolCalls(t *testing.T) {
	_, err := ValidateRequestOptions(ProviderOpenAI, ProviderAPIOpenAIChatCompletions, "proxy-model", true, ProviderRequestOptions{})
	if err == nil || !strings.Contains(err.Error(), "bash tool calls") {
		t.Fatalf("ValidateRequestOptions err = %v, want bash tool calls rejection", err)
	}
}
