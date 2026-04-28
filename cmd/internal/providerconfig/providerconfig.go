package providerconfig

import core "github.com/clnkr-ai/clnkr/internal/providers/providerconfig"

type (
	Provider                       = core.Provider
	ProviderAPI                    = core.ProviderAPI
	OptionalInt                    = core.OptionalInt
	ProviderRequestOptions         = core.ProviderRequestOptions
	ProviderEffortOptions          = core.ProviderEffortOptions
	ProviderOutputOptions          = core.ProviderOutputOptions
	AnthropicManualThinkingOptions = core.AnthropicManualThinkingOptions
	Inputs                         = core.Inputs
	ResolvedProviderConfig         = core.ResolvedProviderConfig
)

const (
	ProviderAnthropic                = core.ProviderAnthropic
	ProviderOpenAI                   = core.ProviderOpenAI
	ProviderAPIAuto                  = core.ProviderAPIAuto
	ProviderAPIOpenAIChatCompletions = core.ProviderAPIOpenAIChatCompletions
	ProviderAPIOpenAIResponses       = core.ProviderAPIOpenAIResponses
	DefaultAnthropicBaseURL          = core.DefaultAnthropicBaseURL
	DefaultOpenAIBaseURL             = core.DefaultOpenAIBaseURL
	DefaultAnthropicMaxTokens        = core.DefaultAnthropicMaxTokens
	MaxAnthropicNonStreamingTokens   = core.MaxAnthropicNonStreamingTokens
	MinAnthropicThinkingBudgetTokens = core.MinAnthropicThinkingBudgetTokens
)

func ResolveConfig(inputs Inputs, env func(string) string) (ResolvedProviderConfig, error) {
	return core.ResolveConfig(inputs, env)
}
