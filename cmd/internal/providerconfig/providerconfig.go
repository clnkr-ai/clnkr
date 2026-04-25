package providerconfig

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
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

type Inputs struct{ Provider, ProviderAPI, Model, BaseURL string }

type ResolvedProviderConfig struct {
	Provider               Provider
	ProviderAPI            ProviderAPI
	Model, BaseURL, APIKey string
}

var datedSnapshotSuffix = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)

var openAIResponsesAllowlist = []string{
	"gpt-5",
	"gpt-5-mini",
	"gpt-5-nano",
	"gpt-5-pro",
	"gpt-5.1",
	"gpt-5.2",
	"gpt-5.4",
	"gpt-5.4-mini",
	"gpt-5.4-nano",
	"gpt-5-codex",
	"gpt-5.1-codex",
	"gpt-5.1-codex-mini",
	"gpt-5.1-codex-max",
	"gpt-5.2-codex",
	"gpt-5.3-codex",
	"gpt-4.1",
	"gpt-4.1-mini",
	"gpt-4.1-nano",
	"o3-pro",
}

var unsupportedStructuredOpenAIModels = []string{
	"gpt-5.2-pro",
	"gpt-5.4-pro",
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

	return ResolvedProviderConfig{
		Provider:    provider,
		ProviderAPI: providerAPI,
		Model:       model,
		BaseURL:     baseURL,
		APIKey:      apiKey,
	}, nil
}

func chooseValue(flagValue, envValue, flagSource, envSource string) (string, string, bool) {
	if flagValue = strings.TrimSpace(flagValue); flagValue != "" {
		return flagValue, flagSource, true
	}
	if envValue = strings.TrimSpace(envValue); envValue != "" {
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
	if listedModel(openAIResponsesAllowlist, model) {
		return ProviderAPIOpenAIResponses
	}
	return ProviderAPIOpenAIChatCompletions
}

// Keep this matcher intentionally conservative: exact names plus dated snapshots only.
func listedModel(models []string, model string) bool {
	return slices.Contains(models, model) ||
		datedSnapshotSuffix.MatchString(model) && slices.Contains(models, model[:len(model)-11])
}
