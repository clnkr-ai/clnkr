package providerconfig

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/clnkr-ai/clnkr"
	providerdomain "github.com/clnkr-ai/clnkr/internal/providers/providerconfig"
)

const (
	DefaultAnthropicBaseURL = "https://api.anthropic.com"
	DefaultOpenAIBaseURL    = "https://api.openai.com/v1"
)

type Inputs struct {
	Provider, ProviderAPI, Model, BaseURL string
	ActProtocol                           ActProtocolSetting
	RequestOptions                        providerdomain.ProviderRequestOptions
}

type ResolvedProviderConfig struct {
	Provider               providerdomain.Provider
	ProviderAPI            providerdomain.ProviderAPI
	Model, BaseURL, APIKey string
	ActProtocol            clnkr.ActProtocol
	RequestOptions         providerdomain.ProviderRequestOptions
}

type ActProtocolSetting string

const (
	ActProtocolSettingAuto        ActProtocolSetting = "auto"
	ActProtocolSettingClnkrInline ActProtocolSetting = "clnkr-inline"
	ActProtocolSettingToolCalls   ActProtocolSetting = "tool-calls"
)

func ParseActProtocolSetting(raw string) (ActProtocolSetting, error) {
	setting := ActProtocolSetting(strings.ToLower(strings.TrimSpace(raw)))
	switch setting {
	case "":
		return ActProtocolSettingAuto, nil
	case ActProtocolSettingAuto, ActProtocolSettingClnkrInline, ActProtocolSettingToolCalls:
		return setting, nil
	default:
		return "", fmt.Errorf(`invalid act-protocol %q (allowed: auto, clnkr-inline, tool-calls)`, raw)
	}
}

func resolveActProtocol(setting ActProtocolSetting, provider providerdomain.Provider, providerAPI providerdomain.ProviderAPI) clnkr.ActProtocol {
	if setting == ActProtocolSettingAuto {
		if providerdomain.SupportsBashToolCalls(provider, providerAPI) {
			return clnkr.ActProtocolToolCalls
		}
		return clnkr.ActProtocolClnkrInline
	}
	return clnkr.ActProtocol(setting)
}

func ResolveConfig(inputs Inputs, env func(string) string) (ResolvedProviderConfig, error) {
	provider, providerAPI, model, baseURL, err := resolveContext(inputs, env)
	if err != nil {
		return ResolvedProviderConfig{}, err
	}
	actProtocolSetting, err := ParseActProtocolSetting(string(inputs.ActProtocol))
	if err != nil {
		return ResolvedProviderConfig{}, err
	}
	actProtocol := resolveActProtocol(actProtocolSetting, provider, providerAPI)
	apiKey := strings.TrimSpace(env("CLNKR_API_KEY"))
	if apiKey == "" {
		return ResolvedProviderConfig{}, fmt.Errorf("api key is required; set CLNKR_API_KEY")
	}
	requestOptions, err := providerdomain.ValidateRequestOptions(provider, providerAPI, model, actProtocol == clnkr.ActProtocolToolCalls, inputs.RequestOptions)
	if err != nil {
		return ResolvedProviderConfig{}, err
	}
	return ResolvedProviderConfig{Provider: provider, ProviderAPI: providerAPI, Model: model, BaseURL: baseURL, APIKey: apiKey, ActProtocol: actProtocol, RequestOptions: requestOptions}, nil
}

func ResolvePromptActProtocol(inputs Inputs, env func(string) string) (clnkr.ActProtocol, error) {
	actProtocolSetting, err := ParseActProtocolSetting(string(inputs.ActProtocol))
	if err != nil {
		return "", err
	}
	if actProtocolSetting == ActProtocolSettingClnkrInline || actProtocolSetting == ActProtocolSettingToolCalls {
		return resolveActProtocol(actProtocolSetting, "", ""), nil
	}
	provider, providerAPI, _, _, err := resolveContext(inputs, env)
	if err != nil {
		return "", fmt.Errorf("%w; pass --act-protocol clnkr-inline or --act-protocol tool-calls to dump a concrete prompt without provider/model context", err)
	}
	return resolveActProtocol(actProtocolSetting, provider, providerAPI), nil
}

func resolveContext(inputs Inputs, env func(string) string) (providerdomain.Provider, providerdomain.ProviderAPI, string, string, error) {
	baseURL, baseURLSource, baseURLSet := chooseValue(inputs.BaseURL, env("CLNKR_BASE_URL"), "--base-url", "CLNKR_BASE_URL")
	providerRaw, _, providerSet := chooseValue(inputs.Provider, env("CLNKR_PROVIDER"), "--provider", "CLNKR_PROVIDER")
	var provider providerdomain.Provider
	var parsedBaseURL *url.URL
	var err error
	if providerSet {
		provider, err = providerdomain.ParseProvider(providerRaw)
		if err != nil {
			return "", "", "", "", err
		}
	} else if baseURLSet {
		parsedBaseURL, err = parseBaseURL(baseURL, baseURLSource)
		if err != nil {
			return "", "", "", "", err
		}
		provider = inferProviderFromBaseURL(parsedBaseURL)
	} else {
		return "", "", "", "", fmt.Errorf("provider is required; set --provider or CLNKR_PROVIDER")
	}

	model, _, ok := chooseValue(inputs.Model, env("CLNKR_MODEL"), "--model", "CLNKR_MODEL")
	if !ok {
		return "", "", "", "", fmt.Errorf("model is required; set --model or CLNKR_MODEL")
	}

	providerAPIRaw, _, providerAPISet := chooseValue(inputs.ProviderAPI, env("CLNKR_PROVIDER_API"), "--provider-api", "CLNKR_PROVIDER_API")
	if provider == providerdomain.ProviderAnthropic && providerAPISet {
		return "", "", "", "", fmt.Errorf(`provider-api is only valid for provider "openai"`)
	}
	providerAPI := providerdomain.ProviderAPIAuto
	if providerAPISet {
		if providerAPI, err = providerdomain.ParseProviderAPI(providerAPIRaw); err != nil {
			return "", "", "", "", err
		}
	}

	if !baseURLSet {
		baseURL, baseURLSource = DefaultAnthropicBaseURL, "default"
		if provider == providerdomain.ProviderOpenAI {
			baseURL = DefaultOpenAIBaseURL
		}
	}
	if parsedBaseURL == nil {
		parsedBaseURL, err = parseBaseURL(baseURL, baseURLSource)
		if err != nil {
			return "", "", "", "", err
		}
	}
	providerAPI, err = providerdomain.ResolveProviderAPI(provider, providerAPI, model)
	if err != nil {
		return "", "", "", "", err
	}
	return provider, providerAPI, model, parsedBaseURL.String(), nil
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
