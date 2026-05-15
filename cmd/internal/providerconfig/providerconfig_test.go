package providerconfig

import (
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr"
	providerdomain "github.com/clnkr-ai/clnkr/internal/providers/providerconfig"
)

func TestResolveConfigRequiresProviderModelAndAPIKey(t *testing.T) {
	tests := []struct {
		name              string
		inputs            Inputs
		env               map[string]string
		want              string
		wantMissingAPIKey bool
	}{
		{
			name: "provider required",
			want: "provider is required; set --provider or CLNKR_PROVIDER",
		},
		{
			name:   "model required",
			inputs: Inputs{Provider: "anthropic"},
			want:   "model is required; set --model or CLNKR_MODEL",
		},
		{
			name:              "api key required",
			inputs:            Inputs{Provider: "openai", Model: "gpt-4.1"},
			want:              "api key is required; set CLNKR_API_KEY",
			wantMissingAPIKey: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveConfig(tt.inputs, envMap(tt.env))
			if err == nil || err.Error() != tt.want {
				t.Fatalf("ResolveConfig() err = %v, want %q", err, tt.want)
			}
			if IsMissingAPIKey(err) != tt.wantMissingAPIKey {
				t.Fatalf("IsMissingAPIKey(%v) = %v, want %v", err, IsMissingAPIKey(err), tt.wantMissingAPIKey)
			}
		})
	}
}

func TestActProtocolFlagValue(t *testing.T) {
	tests := []struct {
		name      string
		flagValue string
		flagSet   bool
		env       string
		want      string
	}{
		{name: "uses env when flag unset", flagValue: "auto", env: "tool-calls", want: "tool-calls"},
		{name: "prefers flag", flagValue: "clnkr-inline", flagSet: true, env: "tool-calls", want: "clnkr-inline"},
		{name: "defaults auto", flagValue: "auto", want: "auto"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ActProtocolFlagValue(tt.flagValue, tt.flagSet, func(key string) string {
				if key == "CLNKR_ACT_PROTOCOL" {
					return tt.env
				}
				return ""
			})
			if got != tt.want {
				t.Fatalf("ActProtocolFlagValue() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveConfigPrefersFlagsOverEnv(t *testing.T) {
	cfg := mustResolveConfig(t, Inputs{
		Provider:    "openai",
		ProviderAPI: "openai-chat-completions",
		Model:       "flag-model",
		BaseURL:     "https://flags.example.com/v1",
	}, map[string]string{
		"CLNKR_PROVIDER":     "anthropic",
		"CLNKR_PROVIDER_API": "openai-responses",
		"CLNKR_MODEL":        "env-model",
		"CLNKR_BASE_URL":     "https://env.example.com/v1",
		"CLNKR_API_KEY":      "env-key",
	})
	if cfg.Provider != providerdomain.ProviderOpenAI {
		t.Fatalf("Provider = %q, want %q", cfg.Provider, providerdomain.ProviderOpenAI)
	}
	if cfg.ProviderAPI != providerdomain.ProviderAPIOpenAIChatCompletions {
		t.Fatalf("ProviderAPI = %q, want %q", cfg.ProviderAPI, providerdomain.ProviderAPIOpenAIChatCompletions)
	}
	if cfg.Model != "flag-model" {
		t.Fatalf("Model = %q, want flag-model", cfg.Model)
	}
	if cfg.BaseURL != "https://flags.example.com/v1" {
		t.Fatalf("BaseURL = %q, want https://flags.example.com/v1", cfg.BaseURL)
	}
	if cfg.APIKey != "env-key" {
		t.Fatalf("APIKey = %q, want env-key", cfg.APIKey)
	}
}

func TestResolveConfigUsesOnlyCLNKREnvSurface(t *testing.T) {
	lookups := make(map[string]int)
	cfg, err := ResolveConfig(Inputs{}, func(key string) string {
		lookups[key]++
		switch key {
		case "CLNKR_PROVIDER":
			return "anthropic"
		case "CLNKR_MODEL":
			return "claude-sonnet-4-6"
		case "CLNKR_API_KEY":
			return "clnkr-key"
		case "ANTHROPIC_API_KEY":
			return "anthropic-key"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("ResolveConfig(): %v", err)
	}
	if cfg.APIKey != "clnkr-key" {
		t.Fatalf("APIKey = %q, want clnkr-key", cfg.APIKey)
	}
	if lookups["ANTHROPIC_API_KEY"] != 0 {
		t.Fatalf("ANTHROPIC_API_KEY lookups = %d, want 0", lookups["ANTHROPIC_API_KEY"])
	}
}

func TestResolveConfigRejectsProviderAPIForAnthropic(t *testing.T) {
	_, err := ResolveConfig(Inputs{Provider: "anthropic", ProviderAPI: "auto", Model: "claude-sonnet-4-6"}, keyedEnv())
	if err == nil || err.Error() != `provider-api is only valid for provider "openai"` {
		t.Fatalf("ResolveConfig() err = %v, want anthropic provider-api rejection", err)
	}
}

func TestResolveConfigProviderAndBaseURLSelection(t *testing.T) {
	tests := []struct {
		name            string
		inputs          Inputs
		wantProvider    providerdomain.Provider
		wantProviderAPI providerdomain.ProviderAPI
		wantBaseURL     string
	}{
		{
			name:            "anthropic default",
			inputs:          Inputs{Provider: "anthropic", Model: "claude-sonnet-4-6"},
			wantProvider:    providerdomain.ProviderAnthropic,
			wantProviderAPI: providerdomain.ProviderAPIAuto,
			wantBaseURL:     DefaultAnthropicBaseURL,
		},
		{
			name:            "openai default",
			inputs:          Inputs{Provider: "openai", Model: "proxy-model"},
			wantProvider:    providerdomain.ProviderOpenAI,
			wantProviderAPI: providerdomain.ProviderAPIOpenAIChatCompletions,
			wantBaseURL:     DefaultOpenAIBaseURL,
		},
		{
			name:            "custom base url infers openai",
			inputs:          Inputs{Model: "test-model", BaseURL: "https://mock-provider.example/v1"},
			wantProvider:    providerdomain.ProviderOpenAI,
			wantProviderAPI: providerdomain.ProviderAPIOpenAIChatCompletions,
			wantBaseURL:     "https://mock-provider.example/v1",
		},
		{
			name:            "anthropic base url infers anthropic",
			inputs:          Inputs{Model: "claude-sonnet-4-6", BaseURL: "https://api.anthropic.com"},
			wantProvider:    providerdomain.ProviderAnthropic,
			wantProviderAPI: providerdomain.ProviderAPIAuto,
			wantBaseURL:     "https://api.anthropic.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := mustResolveConfig(t, tt.inputs, keyedEnvMap())
			if cfg.Provider != tt.wantProvider {
				t.Fatalf("Provider = %q, want %q", cfg.Provider, tt.wantProvider)
			}
			if cfg.ProviderAPI != tt.wantProviderAPI {
				t.Fatalf("ProviderAPI = %q, want %q", cfg.ProviderAPI, tt.wantProviderAPI)
			}
			if cfg.BaseURL != tt.wantBaseURL {
				t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, tt.wantBaseURL)
			}
		})
	}
}

func TestResolveConfigOpenAIProviderAPISelection(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		providerAPI string
		want        providerdomain.ProviderAPI
	}{
		{name: "allowlisted gpt-5", model: "gpt-5", want: providerdomain.ProviderAPIOpenAIResponses},
		{name: "allowlisted dated mini", model: "gpt-5.4-mini-2026-03-05", want: providerdomain.ProviderAPIOpenAIResponses},
		{name: "allowlisted codex", model: "gpt-5-codex", want: providerdomain.ProviderAPIOpenAIResponses},
		{name: "allowlisted codex mini", model: "gpt-5.1-codex-mini", want: providerdomain.ProviderAPIOpenAIResponses},
		{name: "allowlisted o-series", model: "o3-pro", want: providerdomain.ProviderAPIOpenAIResponses},
		{name: "openai style latest", model: "gpt-4o-latest", want: providerdomain.ProviderAPIOpenAIResponses},
		{name: "codex prefix", model: "codex-mini-latest", want: providerdomain.ProviderAPIOpenAIResponses},
		{name: "codex suffix", model: "swift-codex", want: providerdomain.ProviderAPIOpenAIResponses},
		{name: "o-series mini", model: "o4-mini", want: providerdomain.ProviderAPIOpenAIResponses},
		{name: "case and space normalized", model: "  GPT-5.9-preview  ", want: providerdomain.ProviderAPIOpenAIResponses},
		{name: "future gpt", model: "gpt-6", want: providerdomain.ProviderAPIOpenAIResponses},
		{name: "dated snapshot", model: "gpt-5.4-2026-03-05", want: providerdomain.ProviderAPIOpenAIResponses},
		{name: "openai-looking non snapshot suffix", model: "gpt-5.4-preview", want: providerdomain.ProviderAPIOpenAIResponses},
		{name: "chat latest suffix still openai-looking", model: "gpt-5.2-chat-latest", want: providerdomain.ProviderAPIOpenAIResponses},
		{name: "known non-openai chatgpt", model: "chatgpt-4o-latest", want: providerdomain.ProviderAPIOpenAIChatCompletions},
		{name: "known non-openai chatgpt codex", model: "chatgpt-codex", want: providerdomain.ProviderAPIOpenAIChatCompletions},
		{name: "known non-openai orca", model: "orca-mini", want: providerdomain.ProviderAPIOpenAIChatCompletions},
		{name: "known non-openai orca codex", model: "orca-codex", want: providerdomain.ProviderAPIOpenAIChatCompletions},
		{name: "known non-openai olmo", model: "olmo-2", want: providerdomain.ProviderAPIOpenAIChatCompletions},
		{name: "known non-openai olmo codex", model: "olmo-codex", want: providerdomain.ProviderAPIOpenAIChatCompletions},
		{name: "known non-openai openhermes", model: "openhermes-2.5", want: providerdomain.ProviderAPIOpenAIChatCompletions},
		{name: "known non-openai openhermes codex", model: "openhermes-codex", want: providerdomain.ProviderAPIOpenAIChatCompletions},
		{name: "unmatched llama", model: "llama3", want: providerdomain.ProviderAPIOpenAIChatCompletions},
		{name: "unmatched gemini", model: "gemini-2.0-flash", want: providerdomain.ProviderAPIOpenAIChatCompletions},
		{name: "non openai-looking dated suffix", model: "llama3-2026-03-05", want: providerdomain.ProviderAPIOpenAIChatCompletions},
		{name: "explicit codex responses", model: "future-codex-model", providerAPI: "openai-responses", want: providerdomain.ProviderAPIOpenAIResponses},
		{name: "explicit codex chat completions", model: "gpt-5.1-codex-max", providerAPI: "openai-chat-completions", want: providerdomain.ProviderAPIOpenAIChatCompletions},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := mustResolveConfig(t, Inputs{Provider: "openai", ProviderAPI: tt.providerAPI, Model: tt.model}, keyedEnvMap())
			if cfg.ProviderAPI != tt.want {
				t.Fatalf("ProviderAPI = %q, want %q", cfg.ProviderAPI, tt.want)
			}
		})
	}
}

func TestResolveConfigRejectsUnsupportedProModels(t *testing.T) {
	tests := []struct {
		name   string
		inputs Inputs
	}{
		{name: "auto rejects gpt-5.2-pro", inputs: Inputs{Provider: "openai", Model: "gpt-5.2-pro"}},
		{name: "explicit responses rejects gpt-5.4-pro", inputs: Inputs{Provider: "openai", ProviderAPI: "openai-responses", Model: "gpt-5.4-pro"}},
		{name: "dated snapshot rejects gpt-5.4-pro", inputs: Inputs{Provider: "openai", Model: "gpt-5.4-pro-2026-03-05"}},
		{name: "forced chat completions still rejects gpt-5.2-pro", inputs: Inputs{Provider: "openai", ProviderAPI: "openai-chat-completions", Model: "gpt-5.2-pro"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveConfig(tt.inputs, keyedEnv())
			requireErrorContains(t, err, "structured outputs")
		})
	}
}

func TestParseActProtocolSetting(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    ActProtocolSetting
		wantErr string
	}{
		{name: "default", want: ActProtocolSettingAuto},
		{name: "auto", raw: " AUTO ", want: ActProtocolSettingAuto},
		{name: "inline", raw: "clnkr-inline", want: ActProtocolSettingClnkrInline},
		{name: "tool calls", raw: "tool-calls", want: ActProtocolSettingToolCalls},
		{name: "rejects unknown", raw: "native-bash-tools", wantErr: "invalid act-protocol"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseActProtocolSetting(tt.raw)
			if tt.wantErr != "" {
				requireErrorContains(t, err, tt.wantErr)
				requireErrorContains(t, err, "auto, clnkr-inline, tool-calls")
				return
			}
			if err != nil {
				t.Fatalf("ParseActProtocolSetting(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("ParseActProtocolSetting(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestResolveConfigActProtocol(t *testing.T) {
	tests := []struct {
		name    string
		inputs  Inputs
		want    clnkr.ActProtocol
		wantErr string
	}{
		{name: "defaults auto to anthropic tool calls", inputs: Inputs{Provider: "anthropic", Model: "claude-sonnet-4-6"}, want: clnkr.ActProtocolToolCalls},
		{name: "auto uses openai responses tool calls", inputs: Inputs{Provider: "openai", ProviderAPI: "openai-responses", Model: "gpt-5"}, want: clnkr.ActProtocolToolCalls},
		{name: "auto falls back for openai chat completions", inputs: Inputs{Provider: "openai", ProviderAPI: "openai-chat-completions", Model: "proxy-model"}, want: clnkr.ActProtocolClnkrInline},
		{name: "explicit inline stays inline", inputs: Inputs{Provider: "anthropic", Model: "claude-sonnet-4-6", ActProtocol: ActProtocolSettingClnkrInline}, want: clnkr.ActProtocolClnkrInline},
		{name: "accepts explicit anthropic tool calls", inputs: Inputs{Provider: "anthropic", Model: "claude-sonnet-4-6", ActProtocol: ActProtocolSettingToolCalls}, want: clnkr.ActProtocolToolCalls},
		{name: "rejects openai chat completions tool calls", inputs: Inputs{Provider: "openai", ProviderAPI: "openai-chat-completions", Model: "proxy-model", ActProtocol: ActProtocolSettingToolCalls}, wantErr: "bash tool calls"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ResolveConfig(tt.inputs, keyedEnv())
			if tt.wantErr != "" {
				requireErrorContains(t, err, tt.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("ResolveConfig(): %v", err)
			}
			if cfg.ActProtocol != tt.want {
				t.Fatalf("ActProtocol = %q, want %q", cfg.ActProtocol, tt.want)
			}
		})
	}
}

func TestResolvePromptActProtocol(t *testing.T) {
	tests := []struct {
		name             string
		inputs           Inputs
		dumpSystemPrompt bool
		want             clnkr.ActProtocol
		wantErr          string
		rejectErr        string
	}{
		{name: "concrete inline needs no provider config", inputs: Inputs{ActProtocol: ActProtocolSettingClnkrInline}, want: clnkr.ActProtocolClnkrInline},
		{name: "concrete tool calls needs no provider config", inputs: Inputs{ActProtocol: ActProtocolSettingToolCalls}, want: clnkr.ActProtocolToolCalls},
		{name: "auto resolves anthropic without api key", inputs: Inputs{Provider: "anthropic", Model: "claude-sonnet-4-6"}, want: clnkr.ActProtocolToolCalls},
		{name: "auto resolves openai responses without api key", inputs: Inputs{Provider: "openai", ProviderAPI: "openai-responses", Model: "gpt-5"}, want: clnkr.ActProtocolToolCalls},
		{name: "auto resolves openai chat completions without api key", inputs: Inputs{Provider: "openai", ProviderAPI: "openai-chat-completions", Model: "proxy-model"}, want: clnkr.ActProtocolClnkrInline},
		{name: "auto reports missing context", wantErr: "provider is required", rejectErr: "--act-protocol clnkr-inline"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolvePromptActProtocol(tt.inputs, func(string) string { return "" }, tt.dumpSystemPrompt)
			if tt.wantErr != "" {
				requireErrorContains(t, err, tt.wantErr)
				if strings.Contains(err.Error(), tt.rejectErr) {
					t.Fatalf("ResolvePromptActProtocol err = %q, want no %q", err.Error(), tt.rejectErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolvePromptActProtocol(): %v", err)
			}
			if got != tt.want {
				t.Fatalf("ActProtocol = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveConfigAcceptsRequestOptions(t *testing.T) {
	t.Run("openai responses effort and max output", func(t *testing.T) {
		cfg := mustResolveConfig(t, Inputs{
			Provider:    "openai",
			ProviderAPI: "openai-responses",
			Model:       "gpt-5.1-codex-max",
			RequestOptions: providerdomain.ProviderRequestOptions{
				Effort: providerdomain.ProviderEffortOptions{Level: "high", Set: true},
				Output: providerdomain.ProviderOutputOptions{MaxOutputTokens: providerdomain.OptionalInt{Value: 8000, Set: true}},
			},
		}, keyedEnvMap())
		if !cfg.RequestOptions.Effort.Set || cfg.RequestOptions.Effort.Level != "high" {
			t.Fatalf("Effort = %#v, want high", cfg.RequestOptions.Effort)
		}
		if cfg.RequestOptions.Output.MaxOutputTokens != (providerdomain.OptionalInt{Value: 8000, Set: true}) {
			t.Fatalf("Output.MaxOutputTokens = %#v, want set 8000", cfg.RequestOptions.Output.MaxOutputTokens)
		}
	})

	t.Run("anthropic thinking and max output", func(t *testing.T) {
		cfg := mustResolveConfig(t, Inputs{
			Provider: "anthropic",
			Model:    "claude-sonnet-4-20250514",
			RequestOptions: providerdomain.ProviderRequestOptions{
				AnthropicManual: providerdomain.AnthropicManualThinkingOptions{ThinkingBudgetTokens: providerdomain.OptionalInt{Value: 2048, Set: true}},
				Output:          providerdomain.ProviderOutputOptions{MaxOutputTokens: providerdomain.OptionalInt{Value: 4096, Set: true}},
			},
		}, keyedEnvMap())
		if cfg.RequestOptions.AnthropicManual.ThinkingBudgetTokens != (providerdomain.OptionalInt{Value: 2048, Set: true}) {
			t.Fatalf("ThinkingBudgetTokens = %#v, want set 2048", cfg.RequestOptions.AnthropicManual.ThinkingBudgetTokens)
		}
		if cfg.RequestOptions.Output.MaxOutputTokens != (providerdomain.OptionalInt{Value: 4096, Set: true}) {
			t.Fatalf("Output.MaxOutputTokens = %#v, want set 4096", cfg.RequestOptions.Output.MaxOutputTokens)
		}
	})

	t.Run("anthropic opus 4 date snapshot manual thinking", func(t *testing.T) {
		mustResolveConfig(t, Inputs{
			Provider: "anthropic",
			Model:    "claude-opus-4-20250514",
			RequestOptions: providerdomain.ProviderRequestOptions{
				AnthropicManual: providerdomain.AnthropicManualThinkingOptions{ThinkingBudgetTokens: providerdomain.OptionalInt{Value: 2048, Set: true}},
			},
		}, keyedEnvMap())
	})
}

func TestResolveConfigRejectsUnsupportedRequestOptions(t *testing.T) {
	tests := []struct {
		name string
		in   Inputs
		want string
	}{
		{name: "invalid effort", in: Inputs{Provider: "openai", Model: "gpt-5.1", RequestOptions: requestOptions(effort("extreme"))}, want: "invalid effort"},
		{name: "effort rejects chat completions", in: Inputs{Provider: "openai", ProviderAPI: "openai-chat-completions", Model: "gpt-5.1", RequestOptions: requestOptions(effort("high"))}, want: `effort is not supported for provider-api "openai-chat-completions"`},
		{name: "effort rejects anthropic max", in: Inputs{Provider: "anthropic", Model: "claude-sonnet-4-20250514", RequestOptions: requestOptions(effort("xhigh"))}, want: `effort "xhigh" is not supported for provider anthropic`},
		{name: "gpt-5-pro rejects low", in: Inputs{Provider: "openai", Model: "gpt-5-pro", RequestOptions: requestOptions(effort("low"))}, want: "gpt-5-pro only supports high"},
		{name: "gpt-5-pro snapshot rejects low", in: Inputs{Provider: "openai", Model: "gpt-5-pro-2026-03-05", RequestOptions: requestOptions(effort("low"))}, want: "gpt-5-pro only supports high"},
		{name: "xhigh requires codex max", in: Inputs{Provider: "openai", Model: "gpt-5.1", RequestOptions: requestOptions(effort("xhigh"))}, want: `effort "xhigh" is not supported for model "gpt-5.1"`},
		{name: "effort requires reasoning model", in: Inputs{Provider: "openai", Model: "gpt-4.1", RequestOptions: requestOptions(effort("high"))}, want: "effort requires an OpenAI reasoning-capable model"},
		{name: "max output requires positive", in: Inputs{Provider: "openai", Model: "gpt-5", RequestOptions: requestOptions(maxOutput(0))}, want: "max-output-tokens must be at least 1"},
		{name: "max output rejects chat completions", in: Inputs{Provider: "openai", ProviderAPI: "openai-chat-completions", Model: "gpt-5", RequestOptions: requestOptions(maxOutput(1000))}, want: `max-output-tokens is not supported for provider-api "openai-chat-completions"`},
		{name: "anthropic max output rejects streaming-sized value", in: Inputs{Provider: "anthropic", Model: "claude-sonnet-4-20250514", RequestOptions: requestOptions(maxOutput(providerdomain.MaxAnthropicNonStreamingTokens + 1))}, want: "while streaming is unsupported"},
		{name: "thinking budget rejects openai", in: Inputs{Provider: "openai", Model: "gpt-5", RequestOptions: requestOptions(thinkingBudget(2048))}, want: "thinking-budget-tokens requires provider anthropic"},
		{name: "thinking budget rejects small value", in: Inputs{Provider: "anthropic", Model: "claude-sonnet-4-20250514", RequestOptions: requestOptions(thinkingBudget(1023))}, want: "thinking-budget-tokens must be at least 1024"},
		{name: "thinking budget rejects unsupported model", in: Inputs{Provider: "anthropic", Model: "claude-3-5-sonnet-20241022", RequestOptions: requestOptions(thinkingBudget(2048))}, want: "thinking-budget-tokens requires an Anthropic extended-thinking-capable model"},
		{name: "thinking budget must be less than default max tokens", in: Inputs{Provider: "anthropic", Model: "claude-sonnet-4-20250514", RequestOptions: requestOptions(thinkingBudget(providerdomain.DefaultAnthropicMaxTokens))}, want: "thinking-budget-tokens must be less than effective anthropic max_tokens (4096)"},
		{name: "thinking budget must be less than requested max tokens", in: Inputs{Provider: "anthropic", Model: "claude-sonnet-4-20250514", RequestOptions: requestOptions(thinkingBudget(4096), maxOutput(4096))}, want: "thinking-budget-tokens must be less than effective anthropic max_tokens (4096)"},
		{name: "thinking budget rejects non-auto effort", in: Inputs{Provider: "anthropic", Model: "claude-sonnet-4-20250514", RequestOptions: requestOptions(effort("high"), thinkingBudget(2048))}, want: "thinking-budget-tokens requires either no --effort flag or --effort auto"},
		{name: "thinking budget rejects Opus 4.7+", in: Inputs{Provider: "anthropic", Model: "claude-opus-4-7-20250514", RequestOptions: requestOptions(thinkingBudget(2048))}, want: "thinking-budget-tokens is not supported for model"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveConfig(tt.in, keyedEnv())
			requireErrorContains(t, err, tt.want)
		})
	}
}

func TestResolveConfigNormalizesBaseURLTrailingSlash(t *testing.T) {
	tests := []struct {
		name    string
		inputs  Inputs
		wantURL string
	}{
		{name: "openai path", inputs: Inputs{Provider: "openai", Model: "gpt-test", BaseURL: "https://api.openai.com/v1/"}, wantURL: "https://api.openai.com/v1"},
		{name: "anthropic root", inputs: Inputs{Provider: "anthropic", Model: "claude-sonnet-4-6", BaseURL: "https://api.anthropic.com/"}, wantURL: "https://api.anthropic.com"},
		{name: "custom root", inputs: Inputs{Provider: "openai", Model: "gpt-test", BaseURL: "https://example.com/"}, wantURL: "https://example.com"},
		{name: "preserves internal repeated slashes", inputs: Inputs{Provider: "openai", Model: "gpt-test", BaseURL: "https://example.com/proxy//v1/"}, wantURL: "https://example.com/proxy//v1"},
		{name: "preserves escaped slash", inputs: Inputs{Provider: "openai", Model: "gpt-test", BaseURL: "https://example.com/proxy%2Fv1/"}, wantURL: "https://example.com/proxy%2Fv1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := mustResolveConfig(t, tt.inputs, keyedEnvMap())
			if cfg.BaseURL != tt.wantURL {
				t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, tt.wantURL)
			}
		})
	}
}

type requestOption func(*providerdomain.ProviderRequestOptions)

func effort(level string) requestOption {
	return func(opts *providerdomain.ProviderRequestOptions) {
		opts.Effort = providerdomain.ProviderEffortOptions{Level: level, Set: true}
	}
}

func maxOutput(tokens int) requestOption {
	return func(opts *providerdomain.ProviderRequestOptions) {
		opts.Output.MaxOutputTokens = providerdomain.OptionalInt{Value: tokens, Set: true}
	}
}

func thinkingBudget(tokens int) requestOption {
	return func(opts *providerdomain.ProviderRequestOptions) {
		opts.AnthropicManual.ThinkingBudgetTokens = providerdomain.OptionalInt{Value: tokens, Set: true}
	}
}

func requestOptions(options ...requestOption) providerdomain.ProviderRequestOptions {
	var opts providerdomain.ProviderRequestOptions
	for _, option := range options {
		option(&opts)
	}
	return opts
}

func mustResolveConfig(t *testing.T, inputs Inputs, env map[string]string) ResolvedProviderConfig {
	t.Helper()
	cfg, err := ResolveConfig(inputs, envMap(env))
	if err != nil {
		t.Fatalf("ResolveConfig(): %v", err)
	}
	return cfg
}

func requireErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want containing %q", err, want)
	}
}

func keyedEnv() func(string) string {
	return envMap(keyedEnvMap())
}

func keyedEnvMap() map[string]string {
	return map[string]string{"CLNKR_API_KEY": "test-key"}
}

func envMap(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}
