package providerconfig

import (
	"fmt"
	"net/url"
	"strings"

	providerdomain "github.com/clnkr-ai/clnkr/internal/providers/providerconfig"
)

const (
	DefaultAnthropicBaseURL = "https://api.anthropic.com"
	DefaultOpenAIBaseURL    = "https://api.openai.com/v1"
)

type Inputs struct {
	Provider       string
	ProviderAPI    string
	Model          string
	BaseURL        string
	RequestOptions providerdomain.ProviderRequestOptions
}

type ResolvedProviderConfig struct {
	Provider       providerdomain.Provider
	ProviderAPI    providerdomain.ProviderAPI
	Model          string
	BaseURL        string
	APIKey         string
	RequestOptions providerdomain.ProviderRequestOptions
}

func ResolveConfig(inputs Inputs, env func(string) string) (ResolvedProviderConfig, error) {
	baseURL, baseURLSource, baseURLSet := chooseValue(inputs.BaseURL, env("CLNKR_BASE_URL"), "--base-url", "CLNKR_BASE_URL")

	providerRaw, _, providerSet := chooseValue(inputs.Provider, env("CLNKR_PROVIDER"), "--provider", "CLNKR_PROVIDER")
	var (
		provider providerdomain.Provider
		err      error
	)
	if providerSet {
		provider, err = providerdomain.ParseProvider(providerRaw)
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
	if provider == providerdomain.ProviderAnthropic && providerAPISet {
		return ResolvedProviderConfig{}, fmt.Errorf(`provider-api is only valid for provider "openai"`)
	}

	providerAPI := providerdomain.ProviderAPIAuto
	if providerAPISet {
		providerAPI, err = providerdomain.ParseProviderAPI(providerAPIRaw)
		if err != nil {
			return ResolvedProviderConfig{}, err
		}
	}

	if !baseURLSet {
		baseURL = DefaultAnthropicBaseURL
		if provider == providerdomain.ProviderOpenAI {
			baseURL = DefaultOpenAIBaseURL
		}
		baseURLSource = "default"
	}
	if _, err := parseBaseURL(baseURL, baseURLSource); err != nil {
		return ResolvedProviderConfig{}, err
	}

	providerAPI, err = providerdomain.ResolveProviderAPI(provider, providerAPI, model)
	if err != nil {
		return ResolvedProviderConfig{}, err
	}
	requestOptions, err := providerdomain.ValidateRequestOptions(provider, providerAPI, model, inputs.RequestOptions)
	if err != nil {
		return ResolvedProviderConfig{}, err
	}

	return ResolvedProviderConfig{
		Provider:       provider,
		ProviderAPI:    providerAPI,
		Model:          model,
		BaseURL:        baseURL,
		APIKey:         apiKey,
		RequestOptions: requestOptions,
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

func inferProviderFromBaseURL(baseURL *url.URL) providerdomain.Provider {
	if host := strings.ToLower(baseURL.Hostname()); host == "anthropic.com" || strings.HasSuffix(host, ".anthropic.com") {
		return providerdomain.ProviderAnthropic
	}
	return providerdomain.ProviderOpenAI
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
