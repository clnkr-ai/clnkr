package providerconfig

import (
	"strings"
	"testing"
)

func TestValidateRequestOptionsRejectsUnsupportedBashToolCalls(t *testing.T) {
	_, err := ValidateRequestOptions(
		ProviderOpenAI,
		ProviderAPIOpenAIChatCompletions,
		"proxy-model",
		true,
		ProviderRequestOptions{},
	)
	if err == nil || !strings.Contains(err.Error(), "bash tool calls") {
		t.Fatalf("ValidateRequestOptions err = %v, want bash tool calls rejection", err)
	}
}
