package providerconfig

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/clnkr-ai/clnkr"
)

type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	ProviderOpenAI    Provider = "openai"
)

type ProviderAPI string

const (
	ProviderAPIAuto                  ProviderAPI = "auto"
	ProviderAPIOpenAIChatCompletions ProviderAPI = "openai-chat-completions"
	ProviderAPIOpenAIResponses       ProviderAPI = "openai-responses"
)

func ParseProvider(raw string) (Provider, error) {
	provider := Provider(strings.ToLower(strings.TrimSpace(raw)))
	if provider == ProviderAnthropic || provider == ProviderOpenAI {
		return provider, nil
	}
	return "", fmt.Errorf(`invalid provider %q (allowed: anthropic, openai)`, raw)
}

func ParseProviderAPI(raw string) (ProviderAPI, error) {
	api := ProviderAPI(strings.ToLower(strings.TrimSpace(raw)))
	if api == ProviderAPIAuto || api == ProviderAPIOpenAIChatCompletions || api == ProviderAPIOpenAIResponses {
		return api, nil
	}
	return "", fmt.Errorf(`invalid provider-api %q (allowed: auto, openai-chat-completions, openai-responses)`, raw)
}

const (
	DefaultAnthropicMaxTokens        = 4096
	MaxAnthropicNonStreamingTokens   = 21333
	MinAnthropicThinkingBudgetTokens = 1024
)

type OptionalInt struct {
	Value int
	Set   bool
}

// ProviderRequestOptions holds provider-agnostic request configuration.
type ProviderRequestOptions struct {
	Effort          ProviderEffortOptions
	Output          ProviderOutputOptions
	AnthropicManual AnthropicManualThinkingOptions
	ActProtocol     clnkr.ActProtocol
}

// ProviderEffortOptions describes the user-requested effort level.
type ProviderEffortOptions struct {
	// Level is "auto", "low", "medium", "high", "xhigh", or "max".
	// "auto" means omit provider-specific effort fields.
	Level string
	// Set distinguishes omitted effort from explicit --effort auto.
	Set bool
}

// ProviderOutputOptions describes the user-requested output token cap.
type ProviderOutputOptions struct {
	MaxOutputTokens OptionalInt
}

// AnthropicManualThinkingOptions is an advanced legacy escape hatch for
// manual thinking budget (not the normal UX).
type AnthropicManualThinkingOptions struct {
	ThinkingBudgetTokens OptionalInt
}

var datedSnapshotSuffix = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)

var openAIResponsesAllowlist = map[string]struct{}{
	"gpt-5":              {},
	"gpt-5-mini":         {},
	"gpt-5-nano":         {},
	"gpt-5-pro":          {},
	"gpt-5.1":            {},
	"gpt-5.2":            {},
	"gpt-5.4":            {},
	"gpt-5.4-mini":       {},
	"gpt-5.4-nano":       {},
	"gpt-5-codex":        {},
	"gpt-5.1-codex":      {},
	"gpt-5.1-codex-mini": {},
	"gpt-5.1-codex-max":  {},
	"gpt-5.2-codex":      {},
	"gpt-5.3-codex":      {},
	"gpt-4.1":            {},
	"gpt-4.1-mini":       {},
	"gpt-4.1-nano":       {},
	"o3-pro":             {},
}

var unsupportedStructuredOpenAIModels = map[string]struct{}{
	"gpt-5.2-pro": {},
	"gpt-5.4-pro": {},
}

func ResolveProviderAPI(provider Provider, providerAPI ProviderAPI, model string) (ProviderAPI, error) {
	if provider == ProviderOpenAI {
		normalizedModel := strings.ToLower(strings.TrimSpace(model))
		if listedModel(unsupportedStructuredOpenAIModels, normalizedModel) {
			return "", fmt.Errorf("model %q is unsupported for clnkr agent turns: structured outputs are required and this model does not support that contract", model)
		}

		return resolveOpenAIProviderAPI(normalizedModel, providerAPI), nil
	}
	return providerAPI, nil
}

func ValidateRequestOptions(provider Provider, providerAPI ProviderAPI, model string, opts ProviderRequestOptions) (ProviderRequestOptions, error) {
	if opts.ActProtocol == "" {
		opts.ActProtocol = clnkr.ActProtocolClnkrInline
	}
	if _, err := clnkr.ParseActProtocol(string(opts.ActProtocol)); err != nil {
		return ProviderRequestOptions{}, err
	}
	if opts.ActProtocol == clnkr.ActProtocolToolCalls {
		if provider == ProviderOpenAI && providerAPI != ProviderAPIOpenAIResponses {
			return ProviderRequestOptions{}, fmt.Errorf("act-protocol %q requires provider-api %q for provider openai", opts.ActProtocol, ProviderAPIOpenAIResponses)
		}
	}
	opts.Effort.Level = strings.ToLower(strings.TrimSpace(opts.Effort.Level))
	if opts.Effort.Set {
		if err := validateEffort(provider, providerAPI, model, opts.Effort.Level); err != nil {
			return ProviderRequestOptions{}, err
		}
	}
	if opts.Output.MaxOutputTokens.Set {
		if opts.Output.MaxOutputTokens.Value < 1 {
			return ProviderRequestOptions{}, fmt.Errorf("max-output-tokens must be at least 1")
		}
		if provider == ProviderOpenAI && providerAPI != ProviderAPIOpenAIResponses {
			return ProviderRequestOptions{}, fmt.Errorf("max-output-tokens is not supported for provider-api %q", providerAPI)
		}
		if provider == ProviderAnthropic && opts.Output.MaxOutputTokens.Value > MaxAnthropicNonStreamingTokens {
			return ProviderRequestOptions{}, fmt.Errorf("max-output-tokens for anthropic must be at most %d while streaming is unsupported", MaxAnthropicNonStreamingTokens)
		}
	}
	if opts.AnthropicManual.ThinkingBudgetTokens.Set {
		if err := validateThinkingBudgetTokens(provider, model, opts); err != nil {
			return ProviderRequestOptions{}, err
		}
	}
	return opts, nil
}

func validateEffort(provider Provider, providerAPI ProviderAPI, model, effort string) error {
	if !allowedEffort(effort) {
		return fmt.Errorf(`invalid effort %q (allowed: auto, low, medium, high, xhigh, max)`, effort)
	}
	if effort == "auto" {
		return nil
	}
	// Non-auto effort
	if provider == ProviderAnthropic {
		switch effort {
		case "low", "medium", "high":
			return nil
		default:
			return fmt.Errorf(`effort %q is not supported for provider anthropic (allowed: low, medium, high)`, effort)
		}
	}
	if provider == ProviderOpenAI {
		if providerAPI == ProviderAPIOpenAIChatCompletions {
			return fmt.Errorf(`effort is not supported for provider-api %q`, providerAPI)
		}
		normalizedModel := strings.ToLower(strings.TrimSpace(model))
		baseModel := stripDatedSnapshotSuffix(normalizedModel)
		switch effort {
		case "max":
			return fmt.Errorf(`effort %q is not supported for OpenAI Responses`, effort)
		case "xhigh":
			if !isCodexMaxOrNewerModel(normalizedModel) {
				return fmt.Errorf(`effort %q is not supported for model %q; xhigh requires codex-max-or-newer`, effort, model)
			}
		case "low", "medium", "high":
			switch {
			case baseModel == "gpt-5-pro" && effort != "high":
				return fmt.Errorf(`effort %q is not supported for model %q; gpt-5-pro only supports high`, effort, model)
			case !isReasoningCapableOpenAIModel(normalizedModel):
				return fmt.Errorf("effort requires an OpenAI reasoning-capable model")
			}
		}
	}
	return nil
}

func allowedEffort(effort string) bool {
	switch effort {
	case "auto", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func validateThinkingBudgetTokens(provider Provider, model string, opts ProviderRequestOptions) error {
	if provider != ProviderAnthropic {
		return fmt.Errorf("thinking-budget-tokens requires provider anthropic")
	}
	if !isAnthropicExtendedThinkingModel(strings.ToLower(strings.TrimSpace(model))) {
		return fmt.Errorf("thinking-budget-tokens requires an Anthropic extended-thinking-capable model")
	}
	if isAnthropicOpus47Plus(model) {
		return fmt.Errorf("thinking-budget-tokens is not supported for model %q (Opus 4.7+ does not support manual thinking budget)", model)
	}
	if opts.Effort.Set && opts.Effort.Level != "auto" {
		return fmt.Errorf("thinking-budget-tokens requires either no --effort flag or --effort auto; non-auto effort is incompatible with manual thinking budget")
	}
	if opts.AnthropicManual.ThinkingBudgetTokens.Value < MinAnthropicThinkingBudgetTokens {
		return fmt.Errorf("thinking-budget-tokens must be at least %d", MinAnthropicThinkingBudgetTokens)
	}
	maxTokens := DefaultAnthropicMaxTokens
	if opts.Output.MaxOutputTokens.Set {
		maxTokens = opts.Output.MaxOutputTokens.Value
	}
	if opts.AnthropicManual.ThinkingBudgetTokens.Value >= maxTokens {
		return fmt.Errorf("thinking-budget-tokens must be less than effective anthropic max_tokens (%d)", maxTokens)
	}
	return nil
}

func resolveOpenAIProviderAPI(model string, providerAPI ProviderAPI) ProviderAPI {
	if providerAPI == ProviderAPIOpenAIChatCompletions || providerAPI == ProviderAPIOpenAIResponses {
		return providerAPI
	}
	if isKnownNonOpenAIModel(model) {
		return ProviderAPIOpenAIChatCompletions
	}
	if listedModel(openAIResponsesAllowlist, model) || isOpenAILookingModel(model) {
		return ProviderAPIOpenAIResponses
	}
	return ProviderAPIOpenAIChatCompletions
}

func isKnownNonOpenAIModel(model string) bool {
	return strings.HasPrefix(model, "chatgpt-") ||
		strings.HasPrefix(model, "olmo-") ||
		strings.HasPrefix(model, "openhermes-") ||
		strings.HasPrefix(model, "orca-")
}

func isOpenAILookingModel(model string) bool {
	return strings.HasPrefix(model, "gpt-") ||
		model == "codex" ||
		strings.HasPrefix(model, "codex-") ||
		strings.HasSuffix(model, "-codex") ||
		strings.Contains(model, "-codex-") ||
		len(model) > 1 && model[0] == 'o' && model[1] >= '0' && model[1] <= '9'
}

func isReasoningCapableOpenAIModel(model string) bool {
	model = stripDatedSnapshotSuffix(model)
	return strings.HasPrefix(model, "gpt-5") ||
		model == "codex" ||
		strings.HasPrefix(model, "codex-") ||
		strings.HasSuffix(model, "-codex") ||
		strings.Contains(model, "-codex-") ||
		len(model) > 1 && model[0] == 'o' && model[1] >= '0' && model[1] <= '9'
}

func isCodexMaxOrNewerModel(model string) bool {
	model = stripDatedSnapshotSuffix(model)
	return strings.Contains(model, "codex-max") ||
		strings.HasPrefix(model, "gpt-5.2-codex") ||
		strings.HasPrefix(model, "gpt-5.3-codex") ||
		strings.HasPrefix(model, "gpt-5.4-codex") ||
		strings.HasPrefix(model, "codex-max")
}

func isAnthropicExtendedThinkingModel(model string) bool {
	model = stripDatedSnapshotSuffix(model)
	return strings.Contains(model, "claude-3-7-sonnet") ||
		strings.Contains(model, "claude-sonnet-4") ||
		strings.Contains(model, "claude-opus-4")
}

func isAnthropicOpus47Plus(model string) bool {
	model = stripDatedSnapshotSuffix(strings.ToLower(strings.TrimSpace(model)))
	idx := strings.Index(model, "opus-4-")
	if idx < 0 {
		return false
	}
	after := model[idx+len("opus-4-"):]
	end := strings.IndexByte(after, '-')
	if end > 0 {
		after = after[:end]
	}
	if len(after) > 2 {
		return false
	}
	if v, err := strconv.Atoi(after); err == nil {
		return v >= 7
	}
	return false
}

// Keep this matcher intentionally conservative: exact names plus dated snapshots only.
func listedModel(models map[string]struct{}, model string) bool {
	_, ok := models[model]
	if !ok && datedSnapshotSuffix.MatchString(model) {
		_, ok = models[model[:len(model)-11]]
	}
	return ok
}

func stripDatedSnapshotSuffix(model string) string {
	if datedSnapshotSuffix.MatchString(model) {
		return model[:len(model)-11]
	}
	return model
}
