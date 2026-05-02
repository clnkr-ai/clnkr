package providerconfig

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/clnkr-ai/clnkr"
	providerdomain "github.com/clnkr-ai/clnkr/internal/providers/providerconfig"
)

const (
	DefaultAnthropicBaseURL   = "https://api.anthropic.com"
	DefaultOpenAIBaseURL      = "https://api.openai.com/v1"
	DefaultOpenAICodexBaseURL = "https://chatgpt.com/backend-api/codex"
)

type Inputs struct {
	Provider       string
	ProviderAPI    string
	Model          string
	BaseURL        string
	ActProtocol    clnkr.ActProtocol
	RequestOptions providerdomain.ProviderRequestOptions
}

type ResolvedProviderConfig struct {
	Provider       providerdomain.Provider
	ProviderAPI    providerdomain.ProviderAPI
	Model          string
	BaseURL        string
	APIKey         string
	ActProtocol    clnkr.ActProtocol
	RequestOptions providerdomain.ProviderRequestOptions
}

func ResolveConfig(inputs Inputs, env func(string) string) (ResolvedProviderConfig, error) {
	baseURL, baseURLSource, baseURLSet := chooseValue(inputs.BaseURL, env("CLNKR_BASE_URL"), "--base-url", "CLNKR_BASE_URL")

	providerRaw, _, providerSet := chooseValue(inputs.Provider, env("CLNKR_PROVIDER"), "--provider", "CLNKR_PROVIDER")
	var provider providerdomain.Provider
	var err error
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
	if apiKey == "" && provider != providerdomain.ProviderOpenAICodex {
		return ResolvedProviderConfig{}, fmt.Errorf("api key is required; set CLNKR_API_KEY")
	}

	providerAPIRaw, _, providerAPISet := chooseValue(inputs.ProviderAPI, env("CLNKR_PROVIDER_API"), "--provider-api", "CLNKR_PROVIDER_API")
	if provider != providerdomain.ProviderOpenAI && providerAPISet {
		return ResolvedProviderConfig{}, fmt.Errorf(`provider-api is only valid for provider "openai"`)
	}

	providerAPI := providerdomain.ProviderAPIAuto
	if providerAPISet {
		if providerAPI, err = providerdomain.ParseProviderAPI(providerAPIRaw); err != nil {
			return ResolvedProviderConfig{}, err
		}
	}

	if !baseURLSet {
		baseURL, baseURLSource = DefaultAnthropicBaseURL, "default"
		if provider == providerdomain.ProviderOpenAI {
			baseURL = DefaultOpenAIBaseURL
		}
		if provider == providerdomain.ProviderOpenAICodex {
			baseURL = DefaultOpenAICodexBaseURL
		}
	}
	parsedBaseURL, err := parseBaseURL(baseURL, baseURLSource)
	if err != nil {
		return ResolvedProviderConfig{}, err
	}
	baseURL = parsedBaseURL.String()

	providerAPI, err = providerdomain.ResolveProviderAPI(provider, providerAPI, model)
	if err != nil {
		return ResolvedProviderConfig{}, err
	}
	actProtocol := inputs.ActProtocol
	if actProtocol == "" {
		actProtocol = clnkr.ActProtocolClnkrInline
	} else if parsed, err := clnkr.ParseActProtocol(string(actProtocol)); err != nil {
		return ResolvedProviderConfig{}, err
	} else {
		actProtocol = parsed
	}
	requestInputs := inputs.RequestOptions
	useBashToolCalls := actProtocol == clnkr.ActProtocolToolCalls
	requestOptions, err := providerdomain.ValidateRequestOptions(provider, providerAPI, model, useBashToolCalls, requestInputs)
	if err != nil {
		return ResolvedProviderConfig{}, err
	}

	return ResolvedProviderConfig{Provider: provider, ProviderAPI: providerAPI, Model: model, BaseURL: baseURL, APIKey: apiKey, ActProtocol: actProtocol, RequestOptions: requestOptions}, nil
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
	escapedPath := strings.TrimRight(parsed.EscapedPath(), "/")
	path, err := url.PathUnescape(escapedPath)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL %q (from %s): invalid path escape: %w", baseURL, source, err)
	}
	parsed.Path = path
	parsed.RawPath = ""
	if escapedPath != (&url.URL{Path: parsed.Path}).EscapedPath() {
		parsed.RawPath = escapedPath
	}
	return parsed, nil
}
