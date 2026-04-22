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

type Inputs struct {
	Provider    string
	ProviderAPI string
	Model       string
	BaseURL     string
}

type ResolvedProviderConfig struct {
	Provider    Provider
	ProviderAPI ProviderAPI
	Model       string
	BaseURL     string
	APIKey      string
}

var datedSnapshotSuffix = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)

var openAIResponsesAllowlist = map[string]struct{}{
	"gpt-5":        {},
	"gpt-5-mini":   {},
	"gpt-5.1":      {},
	"gpt-5.2":      {},
	"gpt-5.4":      {},
	"gpt-5.4-mini": {},
	"gpt-5.4-nano": {},
	"gpt-5-nano":   {},
	"gpt-5-pro":    {},
	"gpt-4.1":      {},
	"gpt-4.1-mini": {},
	"gpt-4.1-nano": {},
	"o3-pro":       {},
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
		baseURL = defaultBaseURL(provider)
		baseURLSource = "default"
	}
	if _, err := parseBaseURL(baseURL, baseURLSource); err != nil {
		return ResolvedProviderConfig{}, err
	}

	if provider == ProviderOpenAI {
		if _, unsupported := unsupportedStructuredOpenAIModels[model]; unsupported {
			return ResolvedProviderConfig{}, unsupportedStructuredOutputsError(model)
		}

		providerAPI = resolveOpenAIProviderAPI(model, providerAPI)
		if providerAPI == ProviderAPIOpenAIResponses && isCodexFamilyModel(model) {
			return ResolvedProviderConfig{}, unsupportedStructuredOutputsError(model)
		}
	}

	return ResolvedProviderConfig{
		Provider:    provider,
		ProviderAPI: providerAPI,
		Model:       model,
		BaseURL:     baseURL,
		APIKey:      apiKey,
	}, nil
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
	switch Provider(strings.ToLower(strings.TrimSpace(raw))) {
	case ProviderAnthropic:
		return ProviderAnthropic, nil
	case ProviderOpenAI:
		return ProviderOpenAI, nil
	default:
		return "", fmt.Errorf(`invalid provider %q (allowed: anthropic, openai)`, raw)
	}
}

func parseProviderAPI(raw string) (ProviderAPI, error) {
	switch ProviderAPI(strings.ToLower(strings.TrimSpace(raw))) {
	case ProviderAPIAuto:
		return ProviderAPIAuto, nil
	case ProviderAPIOpenAIChatCompletions:
		return ProviderAPIOpenAIChatCompletions, nil
	case ProviderAPIOpenAIResponses:
		return ProviderAPIOpenAIResponses, nil
	default:
		return "", fmt.Errorf(`invalid provider-api %q (allowed: auto, openai-chat-completions, openai-responses)`, raw)
	}
}

func defaultBaseURL(provider Provider) string {
	if provider == ProviderOpenAI {
		return DefaultOpenAIBaseURL
	}
	return DefaultAnthropicBaseURL
}

func inferProviderFromBaseURL(baseURL *url.URL) Provider {
	host := strings.ToLower(baseURL.Hostname())
	if host == "anthropic.com" || strings.HasSuffix(host, ".anthropic.com") {
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
	if isAllowlistedResponsesModel(model) {
		return ProviderAPIOpenAIResponses
	}
	return ProviderAPIOpenAIChatCompletions
}

// Keep this matcher intentionally conservative: exact names plus dated snapshots only.
func isAllowlistedResponsesModel(model string) bool {
	model = strings.TrimSpace(model)
	if _, ok := openAIResponsesAllowlist[model]; ok {
		return true
	}
	if !datedSnapshotSuffix.MatchString(model) {
		return false
	}
	baseModel := model[:len(model)-11]
	_, ok := openAIResponsesAllowlist[baseModel]
	return ok
}

func isCodexFamilyModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return model == "codex" ||
		strings.HasPrefix(model, "codex-") ||
		strings.HasSuffix(model, "-codex") ||
		strings.Contains(model, "-codex-")
}

func unsupportedStructuredOutputsError(model string) error {
	return fmt.Errorf("model %q is unsupported for clnkr agent turns: structured outputs are required and this model does not support that contract in this pass", model)
}
