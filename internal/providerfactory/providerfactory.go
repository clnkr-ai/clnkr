package providerfactory

import (
	"context"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/providers/anthropic"
	"github.com/clnkr-ai/clnkr/internal/providers/openai"
	"github.com/clnkr-ai/clnkr/internal/providers/openairesponses"
	providerdomain "github.com/clnkr-ai/clnkr/internal/providers/providerconfig"
)

type Model interface {
	clnkr.Model
	QueryText(ctx context.Context, messages []clnkr.Message) (string, error)
}

type Config struct {
	Provider       providerdomain.Provider
	ProviderAPI    providerdomain.ProviderAPI
	Model          string
	BaseURL        string
	APIKey         string
	ActProtocol    clnkr.ActProtocol
	RequestOptions providerdomain.ProviderRequestOptions
}

type Options struct {
	Unattended bool
}

func NewModel(config Config, systemPrompt string) Model {
	return NewModelWithOptions(config, systemPrompt, Options{})
}

func NewModelWithOptions(config Config, systemPrompt string, modelOpts Options) Model {
	opts := config.RequestOptions
	switch {
	case config.Provider == providerdomain.ProviderAnthropic:
		anthropicOpts := anthropic.Options{}
		if opts.Output.MaxOutputTokens.Set {
			anthropicOpts.MaxTokens = opts.Output.MaxOutputTokens.Value
		}
		if opts.AnthropicManual.ThinkingBudgetTokens.Set {
			anthropicOpts.ThinkingBudgetTokens = opts.AnthropicManual.ThinkingBudgetTokens.Value
			anthropicOpts.ThinkingMode = anthropic.ThinkingModeManual
		}
		if opts.Effort.Set && opts.Effort.Level != "auto" {
			anthropicOpts.Effort = opts.Effort.Level
			anthropicOpts.ThinkingMode = anthropic.ThinkingModeAdaptive
		}
		anthropicOpts.UseBashToolCalls = config.ActProtocol == clnkr.ActProtocolToolCalls
		anthropicOpts.Unattended = modelOpts.Unattended
		return anthropic.NewModelWithOptions(
			config.BaseURL,
			config.APIKey,
			config.Model,
			systemPrompt,
			anthropicOpts,
		)
	case config.ProviderAPI == providerdomain.ProviderAPIOpenAIResponses:
		var effort string
		if opts.Effort.Set && opts.Effort.Level != "auto" {
			effort = opts.Effort.Level
		}
		return openairesponses.NewModelWithOptions(
			config.BaseURL,
			config.APIKey,
			config.Model,
			systemPrompt,
			openairesponses.Options{
				ReasoningEffort:    effort,
				MaxOutputTokens:    opts.Output.MaxOutputTokens.Value,
				HasMaxOutputTokens: opts.Output.MaxOutputTokens.Set,
				UseBashToolCalls:   config.ActProtocol == clnkr.ActProtocolToolCalls,
				Unattended:         modelOpts.Unattended,
			},
		)
	default:
		return openai.NewModelWithOptions(
			config.BaseURL,
			config.APIKey,
			config.Model,
			systemPrompt,
			openai.Options{Unattended: modelOpts.Unattended},
		)
	}
}
