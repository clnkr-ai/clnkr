package providerconfig

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
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

const (
	DefaultAnthropicBaseURL = "https://api.anthropic.com"
	DefaultOpenAIBaseURL    = "https://api.openai.com/v1"
)

const (
	DefaultAnthropicMaxTokens        = 4096
	MaxAnthropicNonStreamingTokens   = 21333
	MinAnthropicThinkingBudgetTokens = 1024
)

type OptionalInt struct {
	Value int
	Set   bool
}

type HarnessOptions struct {
	ReasoningEffort      string
	ThinkingBudgetTokens OptionalInt
	MaxOutputTokens      OptionalInt
}

type Inputs struct {
	Provider       string
	ProviderAPI    string
	Model          string
	BaseURL        string
	HarnessOptions HarnessOptions
}

type ResolvedProviderConfig struct {
	Provider       Provider
	ProviderAPI    ProviderAPI
	Model          string
	BaseURL        string
	APIKey         string
	HarnessOptions HarnessOptions
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

func ResolveConfig(inputs Inputs, env func(string) string) (ResolvedProviderConfig, error) {
	baseURL, baseURLSource, baseURLSet := chooseValue(inputs.BaseURL, env("CLNKR_BASE_URL"), "--base-url", "CLNKR_BASE_URL")

	providerRaw, _, providerSet := chooseValue(inputs.Provider, env("CLNKR_PROVIDER"), "--provider", "CLNKR_PROVIDER")
	var (
		provider Provider
		err      error
	)
	if providerSet {
		provider, err = parseProvider(providerRaw)
		if err != nil {
			return ResolvedProviderConfig{}, err
		}
	} else if baseURLSet {
		parsedBaseURL, err := parseBaseURL(baseURL, baseURLSource)
		if err != nil {
			return ResolvedProviderConfig{}, err
		}
		provider = inferProviderFromBaseURL(parsedBaseURL)
	} else {
		return ResolvedProviderConfig{}, fmt.Errorf("provider is required; set --provider or CLNKR_PROVIDER")
	}

	model, _, ok := chooseValue(inputs.Model, env("CLNKR_MODEL"), "--model", "CLNKR_MODEL")
	if !ok {
		return ResolvedProviderConfig{}, fmt.Errorf("model is required; set --model or CLNKR_MODEL")
	}

	apiKey := strings.TrimSpace(env("CLNKR_API_KEY"))
	if apiKey == "" {
		return ResolvedProviderConfig{}, fmt.Errorf("api key is required; set CLNKR_API_KEY")
	}

	providerAPIRaw, _, providerAPISet := chooseValue(inputs.ProviderAPI, env("CLNKR_PROVIDER_API"), "--provider-api", "CLNKR_PROVIDER_API")
	if provider == ProviderAnthropic && providerAPISet {
		return ResolvedProviderConfig{}, fmt.Errorf(`provider-api is only valid for provider "openai"`)
	}

	providerAPI := ProviderAPIAuto
	if providerAPISet {
		providerAPI, err = parseProviderAPI(providerAPIRaw)
		if err != nil {
			return ResolvedProviderConfig{}, err
		}
	}

	if !baseURLSet {
		baseURL = DefaultAnthropicBaseURL
		if provider == ProviderOpenAI {
			baseURL = DefaultOpenAIBaseURL
		}
		baseURLSource = "default"
	}
	if _, err := parseBaseURL(baseURL, baseURLSource); err != nil {
		return ResolvedProviderConfig{}, err
	}

	if provider == ProviderOpenAI {
		normalizedModel := strings.ToLower(strings.TrimSpace(model))
		if listedModel(unsupportedStructuredOpenAIModels, normalizedModel) {
			return ResolvedProviderConfig{}, fmt.Errorf("model %q is unsupported for clnkr agent turns: structured outputs are required and this model does not support that contract", model)
		}

		providerAPI = resolveOpenAIProviderAPI(normalizedModel, providerAPI)
	}

	harnessOptions, err := validateHarnessOptions(provider, providerAPI, model, inputs.HarnessOptions)
	if err != nil {
		return ResolvedProviderConfig{}, err
	}

	return ResolvedProviderConfig{
		Provider:       provider,
		ProviderAPI:    providerAPI,
		Model:          model,
		BaseURL:        baseURL,
		APIKey:         apiKey,
		HarnessOptions: harnessOptions,
	}, nil
}

func validateHarnessOptions(provider Provider, providerAPI ProviderAPI, model string, opts HarnessOptions) (HarnessOptions, error) {
	opts.ReasoningEffort = strings.ToLower(strings.TrimSpace(opts.ReasoningEffort))
	if opts.ReasoningEffort != "" {
		if err := validateReasoningEffort(provider, providerAPI, model, opts.ReasoningEffort); err != nil {
			return HarnessOptions{}, err
		}
	}
	if opts.MaxOutputTokens.Set {
		if opts.MaxOutputTokens.Value < 1 {
			return HarnessOptions{}, fmt.Errorf("max-output-tokens must be at least 1")
		}
		if provider == ProviderOpenAI && providerAPI != ProviderAPIOpenAIResponses {
			return HarnessOptions{}, fmt.Errorf("max-output-tokens is not supported for provider-api %q", providerAPI)
		}
		if provider == ProviderAnthropic && opts.MaxOutputTokens.Value > MaxAnthropicNonStreamingTokens {
			return HarnessOptions{}, fmt.Errorf("max-output-tokens for anthropic must be at most %d while streaming is unsupported", MaxAnthropicNonStreamingTokens)
		}
	}
	if opts.ThinkingBudgetTokens.Set {
		if err := validateThinkingBudgetTokens(provider, model, opts); err != nil {
			return HarnessOptions{}, err
		}
	}
	return opts, nil
}

func validateReasoningEffort(provider Provider, providerAPI ProviderAPI, model, effort string) error {
	if !allowedReasoningEffort(effort) {
		return fmt.Errorf(`invalid reasoning-effort %q (allowed: none, minimal, low, medium, high, xhigh)`, effort)
	}
	if provider != ProviderOpenAI || providerAPI != ProviderAPIOpenAIResponses {
		return fmt.Errorf("reasoning-effort requires provider openai with provider-api openai-responses")
	}
	normalizedModel := strings.ToLower(strings.TrimSpace(model))
	baseModel := stripDatedSnapshotSuffix(normalizedModel)
	switch {
	case baseModel == "gpt-5-pro" && effort != "high":
		return fmt.Errorf(`reasoning-effort %q is not supported for model %q; gpt-5-pro only supports high`, effort, model)
	case isGPT51Model(normalizedModel) && effort == "minimal":
		return fmt.Errorf(`reasoning-effort %q is not supported for model %q`, effort, model)
	case effort == "none" && !isGPT51OrNewerModel(normalizedModel):
		return fmt.Errorf(`reasoning-effort "none" is not supported for model %q`, model)
	case effort == "xhigh" && !isCodexMaxOrNewerModel(normalizedModel):
		return fmt.Errorf(`reasoning-effort "xhigh" is not supported for model %q`, model)
	case !isReasoningCapableOpenAIModel(normalizedModel):
		return fmt.Errorf("reasoning-effort requires an OpenAI reasoning-capable model")
	default:
		return nil
	}
}

func allowedReasoningEffort(effort string) bool {
	switch effort {
	case "none", "minimal", "low", "medium", "high", "xhigh":
		return true
	default:
		return false
	}
}

func validateThinkingBudgetTokens(provider Provider, model string, opts HarnessOptions) error {
	if provider != ProviderAnthropic {
		return fmt.Errorf("thinking-budget-tokens requires provider anthropic")
	}
	if opts.ThinkingBudgetTokens.Value < MinAnthropicThinkingBudgetTokens {
		return fmt.Errorf("thinking-budget-tokens must be at least %d", MinAnthropicThinkingBudgetTokens)
	}
	if !isAnthropicExtendedThinkingModel(strings.ToLower(strings.TrimSpace(model))) {
		return fmt.Errorf("thinking-budget-tokens requires an Anthropic extended-thinking-capable model")
	}
	maxTokens := DefaultAnthropicMaxTokens
	if opts.MaxOutputTokens.Set {
		maxTokens = opts.MaxOutputTokens.Value
	}
	if opts.ThinkingBudgetTokens.Value >= maxTokens {
		return fmt.Errorf("thinking-budget-tokens must be less than effective anthropic max_tokens (%d)", maxTokens)
	}
	return nil
}

func chooseValue(flagValue, envValue, flagSource, envSource string) (string, string, bool) {
	flagValue = strings.TrimSpace(flagValue)
	if flagValue != "" {
		return flagValue, flagSource, true
	}

	envValue = strings.TrimSpace(envValue)
	if envValue != "" {
		return envValue, envSource, true
	}

	return "", "", false
}

func parseProvider(raw string) (Provider, error) {
	provider := Provider(strings.ToLower(strings.TrimSpace(raw)))
	switch provider {
	case ProviderAnthropic, ProviderOpenAI:
		return provider, nil
	default:
		return "", fmt.Errorf(`invalid provider %q (allowed: anthropic, openai)`, raw)
	}
}

func parseProviderAPI(raw string) (ProviderAPI, error) {
	api := ProviderAPI(strings.ToLower(strings.TrimSpace(raw)))
	switch api {
	case ProviderAPIAuto, ProviderAPIOpenAIChatCompletions, ProviderAPIOpenAIResponses:
		return api, nil
	default:
		return "", fmt.Errorf(`invalid provider-api %q (allowed: auto, openai-chat-completions, openai-responses)`, raw)
	}
}

func inferProviderFromBaseURL(baseURL *url.URL) Provider {
	if host := strings.ToLower(baseURL.Hostname()); host == "anthropic.com" || strings.HasSuffix(host, ".anthropic.com") {
		return ProviderAnthropic
	}
	return ProviderOpenAI
}

func parseBaseURL(baseURL, source string) (*url.URL, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL %q (from %s): %w", baseURL, source, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid base URL %q (from %s): must start with http:// or https://", baseURL, source)
	}
	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid base URL %q (from %s): missing host", baseURL, source)
	}
	return parsed, nil
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

func isGPT51Model(model string) bool {
	model = stripDatedSnapshotSuffix(model)
	return model == "gpt-5.1" || strings.HasPrefix(model, "gpt-5.1-")
}

func isGPT51OrNewerModel(model string) bool {
	model = stripDatedSnapshotSuffix(model)
	return strings.HasPrefix(model, "gpt-5.") &&
		len(model) > len("gpt-5.") &&
		model[len("gpt-5.")] >= '1' && model[len("gpt-5.")] <= '9'
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
