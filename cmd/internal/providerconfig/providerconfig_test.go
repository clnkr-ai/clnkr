package providerconfig

import (
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr"
	providerdomain "github.com/clnkr-ai/clnkr/internal/providers/providerconfig"
)

func TestResolveConfigRequiresProviderModelAndAPIKey(t *testing.T) {
	t.Run("provider required", func(t *testing.T) {
		_, err := ResolveConfig(Inputs{}, func(string) string { return "" })
		if err == nil || err.Error() != "provider is required; set --provider or CLNKR_PROVIDER" {
			t.Fatalf("ResolveConfig() err = %v, want provider required error", err)
		}
	})

	t.Run("model required", func(t *testing.T) {
		_, err := ResolveConfig(Inputs{Provider: "anthropic"}, func(string) string {
			return ""
		})
		if err == nil || err.Error() != "model is required; set --model or CLNKR_MODEL" {
			t.Fatalf("ResolveConfig() err = %v, want model required error", err)
		}
	})

	t.Run("api key required", func(t *testing.T) {
		_, err := ResolveConfig(Inputs{
			Provider: "openai",
			Model:    "gpt-4.1",
		}, func(string) string { return "" })
		if err == nil || err.Error() != "api key is required; set CLNKR_API_KEY" {
			t.Fatalf("ResolveConfig() err = %v, want api key required error", err)
		}
	})

	t.Run("provider inferred from explicit base url", func(t *testing.T) {
		cfg, err := ResolveConfig(Inputs{
			Model:   "test-model",
			BaseURL: "https://mock-provider.example/v1",
		}, func(key string) string {
			if key == "CLNKR_API_KEY" {
				return "test-key"
			}
			return ""
		})
		if err != nil {
			t.Fatalf("ResolveConfig(): %v", err)
		}
		if cfg.Provider != providerdomain.ProviderOpenAI {
			t.Fatalf("Provider = %q, want %q", cfg.Provider, providerdomain.ProviderOpenAI)
		}
	})
}

func TestResolveConfigPrefersFlagsOverEnv(t *testing.T) {
	cfg, err := ResolveConfig(Inputs{
		Provider:    "openai",
		ProviderAPI: "openai-chat-completions",
		Model:       "flag-model",
		BaseURL:     "https://flags.example.com/v1",
	}, envMap(map[string]string{
		"CLNKR_PROVIDER":     "anthropic",
		"CLNKR_PROVIDER_API": "openai-responses",
		"CLNKR_MODEL":        "env-model",
		"CLNKR_BASE_URL":     "https://env.example.com/v1",
		"CLNKR_API_KEY":      "env-key",
	}))
	if err != nil {
		t.Fatalf("ResolveConfig(): %v", err)
	}
	if cfg.Provider != providerdomain.ProviderOpenAI {
		t.Fatalf("Provider = %q, want %q", cfg.Provider, providerdomain.ProviderOpenAI)
	}
	if cfg.ProviderAPI != providerdomain.ProviderAPIOpenAIChatCompletions {
		t.Fatalf("ProviderAPI = %q, want %q", cfg.ProviderAPI, providerdomain.ProviderAPIOpenAIChatCompletions)
	}
	if cfg.Model != "flag-model" {
		t.Fatalf("Model = %q, want %q", cfg.Model, "flag-model")
	}
	if cfg.BaseURL != "https://flags.example.com/v1" {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, "https://flags.example.com/v1")
	}
	if cfg.APIKey != "env-key" {
		t.Fatalf("APIKey = %q, want %q", cfg.APIKey, "env-key")
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
		t.Fatalf("APIKey = %q, want %q", cfg.APIKey, "clnkr-key")
	}
	if lookups["ANTHROPIC_API_KEY"] != 0 {
		t.Fatalf("ANTHROPIC_API_KEY lookups = %d, want 0", lookups["ANTHROPIC_API_KEY"])
	}
}

func TestResolveConfigRejectsProviderAPIForAnthropic(t *testing.T) {
	_, err := ResolveConfig(Inputs{
		Provider:    "anthropic",
		ProviderAPI: "auto",
		Model:       "claude-sonnet-4-6",
	}, envMap(map[string]string{
		"CLNKR_API_KEY": "test-key",
	}))
	if err == nil || err.Error() != `provider-api is only valid for provider "openai"` {
		t.Fatalf("ResolveConfig() err = %v, want anthropic provider-api rejection", err)
	}
}

func TestResolveConfigAppliesProviderSpecificBaseURLDefaults(t *testing.T) {
	tests := []struct {
		name            string
		inputs          Inputs
		env             map[string]string
		wantProvider    providerdomain.Provider
		wantProviderAPI providerdomain.ProviderAPI
		wantBaseURL     string
	}{
		{
			name: "anthropic default",
			inputs: Inputs{
				Provider: "anthropic",
				Model:    "claude-sonnet-4-6",
			},
			env: map[string]string{
				"CLNKR_API_KEY": "anthropic-key",
			},
			wantProvider:    providerdomain.ProviderAnthropic,
			wantProviderAPI: providerdomain.ProviderAPIAuto,
			wantBaseURL:     DefaultAnthropicBaseURL,
		},
		{
			name: "openai default",
			inputs: Inputs{
				Provider: "openai",
				Model:    "proxy-model",
			},
			env: map[string]string{
				"CLNKR_API_KEY": "openai-key",
			},
			wantProvider:    providerdomain.ProviderOpenAI,
			wantProviderAPI: providerdomain.ProviderAPIOpenAIChatCompletions,
			wantBaseURL:     DefaultOpenAIBaseURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ResolveConfig(tt.inputs, envMap(tt.env))
			if err != nil {
				t.Fatalf("ResolveConfig(): %v", err)
			}
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

func TestResolveConfigInfersAnthropicFromExplicitBaseURL(t *testing.T) {
	cfg, err := ResolveConfig(Inputs{
		Model:   "claude-sonnet-4-6",
		BaseURL: "https://api.anthropic.com",
	}, envMap(map[string]string{
		"CLNKR_API_KEY": "anthropic-key",
	}))
	if err != nil {
		t.Fatalf("ResolveConfig(): %v", err)
	}
	if cfg.Provider != providerdomain.ProviderAnthropic {
		t.Fatalf("Provider = %q, want %q", cfg.Provider, providerdomain.ProviderAnthropic)
	}
	if cfg.ProviderAPI != providerdomain.ProviderAPIAuto {
		t.Fatalf("ProviderAPI = %q, want %q", cfg.ProviderAPI, providerdomain.ProviderAPIAuto)
	}
}

func TestResolveConfigKnownOpenAIResponsesModels(t *testing.T) {
	tests := []string{
		"gpt-5",
		"gpt-5.4-mini-2026-03-05",
		"gpt-5-codex",
		"gpt-5.1-codex-mini",
		"o3-pro",
	}

	for _, model := range tests {
		t.Run(model, func(t *testing.T) {
			cfg, err := ResolveConfig(Inputs{
				Provider: "openai",
				Model:    model,
			}, envMap(map[string]string{
				"CLNKR_API_KEY": "test-key",
			}))
			if err != nil {
				t.Fatalf("ResolveConfig(): %v", err)
			}
			if cfg.ProviderAPI != providerdomain.ProviderAPIOpenAIResponses {
				t.Fatalf("ProviderAPI = %q, want %q", cfg.ProviderAPI, providerdomain.ProviderAPIOpenAIResponses)
			}
		})
	}
}

func TestResolveConfigActProtocol(t *testing.T) {
	t.Run("defaults clnkr inline", func(t *testing.T) {
		cfg, err := ResolveConfig(Inputs{Provider: "anthropic", Model: "claude-sonnet-4-6"}, envMap(map[string]string{"CLNKR_API_KEY": "key"}))
		if err != nil {
			t.Fatalf("ResolveConfig: %v", err)
		}
		if cfg.ActProtocol != clnkr.ActProtocolClnkrInline {
			t.Fatalf("ActProtocol = %q, want clnkr-inline", cfg.ActProtocol)
		}
	})

	t.Run("accepts anthropic tool calls", func(t *testing.T) {
		cfg, err := ResolveConfig(Inputs{
			Provider:    "anthropic",
			Model:       "claude-sonnet-4-6",
			ActProtocol: clnkr.ActProtocolToolCalls,
		}, envMap(map[string]string{"CLNKR_API_KEY": "key"}))
		if err != nil {
			t.Fatalf("ResolveConfig: %v", err)
		}
		if cfg.ActProtocol != clnkr.ActProtocolToolCalls {
			t.Fatalf("ActProtocol = %q, want tool-calls", cfg.ActProtocol)
		}
	})

	t.Run("rejects openai chat completions tool calls", func(t *testing.T) {
		_, err := ResolveConfig(Inputs{
			Provider:    "openai",
			ProviderAPI: "openai-chat-completions",
			Model:       "proxy-model",
			ActProtocol: clnkr.ActProtocolToolCalls,
		}, envMap(map[string]string{"CLNKR_API_KEY": "key"}))
		if err == nil || !strings.Contains(err.Error(), "tool-calls") {
			t.Fatalf("ResolveConfig err = %v, want tool-calls rejection", err)
		}
	})
}

func TestResolveConfigOpenAIStyleModelsUseResponses(t *testing.T) {
	tests := []string{
		"gpt-5.2-chat-latest",
		"gpt-4o-latest",
		"codex-mini-latest",
		"swift-codex",
		"o4-mini",
		"GPT-5.9-preview",
		"  gpt-6  ",
	}

	for _, model := range tests {
		t.Run(model, func(t *testing.T) {
			cfg, err := ResolveConfig(Inputs{
				Provider: "openai",
				Model:    model,
			}, envMap(map[string]string{
				"CLNKR_API_KEY": "test-key",
			}))
			if err != nil {
				t.Fatalf("ResolveConfig(): %v", err)
			}
			if cfg.ProviderAPI != providerdomain.ProviderAPIOpenAIResponses {
				t.Fatalf("ProviderAPI = %q, want %q", cfg.ProviderAPI, providerdomain.ProviderAPIOpenAIResponses)
			}
		})
	}
}

func TestResolveConfigNegativeExamplesStayOnChatCompletions(t *testing.T) {
	tests := []string{
		"chatgpt-4o-latest",
		"chatgpt-codex",
		"orca-mini",
		"orca-codex",
		"olmo-2",
		"olmo-codex",
		"openhermes-2.5",
		"openhermes-codex",
		"llama3",
		"gemini-2.0-flash",
	}

	for _, model := range tests {
		t.Run(model, func(t *testing.T) {
			cfg, err := ResolveConfig(Inputs{
				Provider: "openai",
				Model:    model,
			}, envMap(map[string]string{
				"CLNKR_API_KEY": "test-key",
			}))
			if err != nil {
				t.Fatalf("ResolveConfig(): %v", err)
			}
			if cfg.ProviderAPI != providerdomain.ProviderAPIOpenAIChatCompletions {
				t.Fatalf("ProviderAPI = %q, want %q", cfg.ProviderAPI, providerdomain.ProviderAPIOpenAIChatCompletions)
			}
		})
	}
}

func TestResolveConfigRejectsUnsupportedProModels(t *testing.T) {
	tests := []struct {
		name   string
		inputs Inputs
	}{
		{
			name: "auto rejects gpt-5.2-pro",
			inputs: Inputs{
				Provider: "openai",
				Model:    "gpt-5.2-pro",
			},
		},
		{
			name: "explicit responses rejects gpt-5.4-pro",
			inputs: Inputs{
				Provider:    "openai",
				ProviderAPI: "openai-responses",
				Model:       "gpt-5.4-pro",
			},
		},
		{
			name: "dated snapshot rejects gpt-5.4-pro",
			inputs: Inputs{
				Provider: "openai",
				Model:    "gpt-5.4-pro-2026-03-05",
			},
		},
		{
			name: "forced chat completions still rejects gpt-5.2-pro",
			inputs: Inputs{
				Provider:    "openai",
				ProviderAPI: "openai-chat-completions",
				Model:       "gpt-5.2-pro",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveConfig(tt.inputs, envMap(map[string]string{
				"CLNKR_API_KEY": "test-key",
			}))
			if err == nil || !strings.Contains(err.Error(), "structured outputs") {
				t.Fatalf("ResolveConfig() err = %v, want structured outputs error", err)
			}
		})
	}
}

func TestResolveConfigAcceptsExplicitCodexResponsesSelections(t *testing.T) {
	tests := []string{
		"gpt-5-codex",
		"gpt-5.1-codex-mini",
		"gpt-5.3-codex",
		"future-codex-model",
		"codex-mini-latest",
	}

	for _, model := range tests {
		t.Run(model, func(t *testing.T) {
			cfg, err := ResolveConfig(Inputs{
				Provider:    "openai",
				ProviderAPI: "openai-responses",
				Model:       model,
			}, envMap(map[string]string{
				"CLNKR_API_KEY": "test-key",
			}))
			if err != nil {
				t.Fatalf("ResolveConfig(): %v", err)
			}
			if cfg.ProviderAPI != providerdomain.ProviderAPIOpenAIResponses {
				t.Fatalf("ProviderAPI = %q, want %q", cfg.ProviderAPI, providerdomain.ProviderAPIOpenAIResponses)
			}
		})
	}
}

func TestResolveConfigAcceptsExplicitCodexChatCompletionsSelections(t *testing.T) {
	tests := []string{
		"gpt-5-codex",
		"gpt-5.1-codex-max",
		"future-codex-model",
	}

	for _, model := range tests {
		t.Run(model, func(t *testing.T) {
			cfg, err := ResolveConfig(Inputs{
				Provider:    "openai",
				ProviderAPI: "openai-chat-completions",
				Model:       model,
			}, envMap(map[string]string{
				"CLNKR_API_KEY": "test-key",
			}))
			if err != nil {
				t.Fatalf("ResolveConfig(): %v", err)
			}
			if cfg.ProviderAPI != providerdomain.ProviderAPIOpenAIChatCompletions {
				t.Fatalf("ProviderAPI = %q, want %q", cfg.ProviderAPI, providerdomain.ProviderAPIOpenAIChatCompletions)
			}
		})
	}
}

func TestResolveConfigOpenAIStyleFallbackAndDatedSnapshots(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		wantAPI providerdomain.ProviderAPI
	}{
		{
			name:    "dated snapshot matches",
			model:   "gpt-5.4-2026-03-05",
			wantAPI: providerdomain.ProviderAPIOpenAIResponses,
		},
		{
			name:    "openai-looking non snapshot suffix uses responses",
			model:   "gpt-5.4-preview",
			wantAPI: providerdomain.ProviderAPIOpenAIResponses,
		},
		{
			name:    "chat latest suffix still looks openai",
			model:   "gpt-5.2-chat-latest",
			wantAPI: providerdomain.ProviderAPIOpenAIResponses,
		},
		{
			name:    "non openai looking suffix stays on chat completions",
			model:   "llama3-2026-03-05",
			wantAPI: providerdomain.ProviderAPIOpenAIChatCompletions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ResolveConfig(Inputs{
				Provider: "openai",
				Model:    tt.model,
			}, envMap(map[string]string{
				"CLNKR_API_KEY": "test-key",
			}))
			if err != nil {
				t.Fatalf("ResolveConfig(): %v", err)
			}
			if cfg.ProviderAPI != tt.wantAPI {
				t.Fatalf("ProviderAPI = %q, want %q", cfg.ProviderAPI, tt.wantAPI)
			}
		})
	}
}

func TestResolveConfigAcceptsRequestOptions(t *testing.T) {
	t.Run("openai responses effort and max output", func(t *testing.T) {
		cfg, err := ResolveConfig(Inputs{
			Provider:    "openai",
			ProviderAPI: "openai-responses",
			Model:       "gpt-5.1-codex-max",
			RequestOptions: providerdomain.ProviderRequestOptions{
				Effort: providerdomain.ProviderEffortOptions{Level: "high", Set: true},
				Output: providerdomain.ProviderOutputOptions{
					MaxOutputTokens: providerdomain.OptionalInt{Value: 8000, Set: true},
				},
			},
		}, envMap(map[string]string{"CLNKR_API_KEY": "test-key"}))
		if err != nil {
			t.Fatalf("ResolveConfig(): %v", err)
		}
		if !cfg.RequestOptions.Effort.Set || cfg.RequestOptions.Effort.Level != "high" {
			t.Fatalf("Effort = %#v, want high", cfg.RequestOptions.Effort)
		}
		if cfg.RequestOptions.Output.MaxOutputTokens != (providerdomain.OptionalInt{Value: 8000, Set: true}) {
			t.Fatalf("Output.MaxOutputTokens = %#v, want set 8000", cfg.RequestOptions.Output.MaxOutputTokens)
		}
	})

	t.Run("anthropic thinking and max output", func(t *testing.T) {
		cfg, err := ResolveConfig(Inputs{
			Provider: "anthropic",
			Model:    "claude-sonnet-4-20250514",
			RequestOptions: providerdomain.ProviderRequestOptions{
				AnthropicManual: providerdomain.AnthropicManualThinkingOptions{
					ThinkingBudgetTokens: providerdomain.OptionalInt{Value: 2048, Set: true},
				},
				Output: providerdomain.ProviderOutputOptions{
					MaxOutputTokens: providerdomain.OptionalInt{Value: 4096, Set: true},
				},
			},
		}, envMap(map[string]string{"CLNKR_API_KEY": "test-key"}))
		if err != nil {
			t.Fatalf("ResolveConfig(): %v", err)
		}
		if cfg.RequestOptions.AnthropicManual.ThinkingBudgetTokens != (providerdomain.OptionalInt{Value: 2048, Set: true}) {
			t.Fatalf("ThinkingBudgetTokens = %#v, want set 2048", cfg.RequestOptions.AnthropicManual.ThinkingBudgetTokens)
		}
		if cfg.RequestOptions.Output.MaxOutputTokens != (providerdomain.OptionalInt{Value: 4096, Set: true}) {
			t.Fatalf("Output.MaxOutputTokens = %#v, want set 4096", cfg.RequestOptions.Output.MaxOutputTokens)
		}
	})

	t.Run("anthropic opus 4 date snapshot manual thinking", func(t *testing.T) {
		_, err := ResolveConfig(Inputs{
			Provider: "anthropic",
			Model:    "claude-opus-4-20250514",
			RequestOptions: providerdomain.ProviderRequestOptions{
				AnthropicManual: providerdomain.AnthropicManualThinkingOptions{
					ThinkingBudgetTokens: providerdomain.OptionalInt{Value: 2048, Set: true},
				},
			},
		}, envMap(map[string]string{"CLNKR_API_KEY": "test-key"}))
		if err != nil {
			t.Fatalf("ResolveConfig(): %v", err)
		}
	})
}

func TestResolveConfigRejectsUnsupportedRequestOptions(t *testing.T) {
	tests := []struct {
		name string
		in   Inputs
		want string
	}{
		{
			name: "invalid effort",
			in: Inputs{
				Provider: "openai",
				Model:    "gpt-5.1",
				RequestOptions: providerdomain.ProviderRequestOptions{
					Effort: providerdomain.ProviderEffortOptions{Level: "extreme", Set: true},
				},
			},
			want: "invalid effort",
		},
		{
			name: "effort rejects chat completions",
			in: Inputs{
				Provider:    "openai",
				ProviderAPI: "openai-chat-completions",
				Model:       "gpt-5.1",
				RequestOptions: providerdomain.ProviderRequestOptions{
					Effort: providerdomain.ProviderEffortOptions{Level: "high", Set: true},
				},
			},
			want: `effort is not supported for provider-api "openai-chat-completions"`,
		},
		{
			name: "effort rejects anthropic max",
			in: Inputs{
				Provider: "anthropic",
				Model:    "claude-sonnet-4-20250514",
				RequestOptions: providerdomain.ProviderRequestOptions{
					Effort: providerdomain.ProviderEffortOptions{Level: "xhigh", Set: true},
				},
			},
			want: `effort "xhigh" is not supported for provider anthropic`,
		},
		{
			name: "gpt-5-pro rejects low",
			in: Inputs{
				Provider: "openai",
				Model:    "gpt-5-pro",
				RequestOptions: providerdomain.ProviderRequestOptions{
					Effort: providerdomain.ProviderEffortOptions{Level: "low", Set: true},
				},
			},
			want: "gpt-5-pro only supports high",
		},
		{
			name: "gpt-5-pro snapshot rejects low",
			in: Inputs{
				Provider: "openai",
				Model:    "gpt-5-pro-2026-03-05",
				RequestOptions: providerdomain.ProviderRequestOptions{
					Effort: providerdomain.ProviderEffortOptions{Level: "low", Set: true},
				},
			},
			want: "gpt-5-pro only supports high",
		},
		{
			name: "xhigh requires codex max",
			in: Inputs{
				Provider: "openai",
				Model:    "gpt-5.1",
				RequestOptions: providerdomain.ProviderRequestOptions{
					Effort: providerdomain.ProviderEffortOptions{Level: "xhigh", Set: true},
				},
			},
			want: `effort "xhigh" is not supported for model "gpt-5.1"`,
		},
		{
			name: "effort requires reasoning model",
			in: Inputs{
				Provider: "openai",
				Model:    "gpt-4.1",
				RequestOptions: providerdomain.ProviderRequestOptions{
					Effort: providerdomain.ProviderEffortOptions{Level: "high", Set: true},
				},
			},
			want: "effort requires an OpenAI reasoning-capable model",
		},
		{
			name: "max output requires positive",
			in: Inputs{
				Provider: "openai",
				Model:    "gpt-5",
				RequestOptions: providerdomain.ProviderRequestOptions{
					Output: providerdomain.ProviderOutputOptions{
						MaxOutputTokens: providerdomain.OptionalInt{Value: 0, Set: true},
					},
				},
			},
			want: "max-output-tokens must be at least 1",
		},
		{
			name: "max output rejects chat completions",
			in: Inputs{
				Provider:    "openai",
				ProviderAPI: "openai-chat-completions",
				Model:       "gpt-5",
				RequestOptions: providerdomain.ProviderRequestOptions{
					Output: providerdomain.ProviderOutputOptions{
						MaxOutputTokens: providerdomain.OptionalInt{Value: 1000, Set: true},
					},
				},
			},
			want: `max-output-tokens is not supported for provider-api "openai-chat-completions"`,
		},
		{
			name: "anthropic max output rejects streaming-sized value",
			in: Inputs{
				Provider: "anthropic",
				Model:    "claude-sonnet-4-20250514",
				RequestOptions: providerdomain.ProviderRequestOptions{
					Output: providerdomain.ProviderOutputOptions{
						MaxOutputTokens: providerdomain.OptionalInt{Value: providerdomain.MaxAnthropicNonStreamingTokens + 1, Set: true},
					},
				},
			},
			want: "while streaming is unsupported",
		},
		{
			name: "thinking budget rejects openai",
			in: Inputs{
				Provider: "openai",
				Model:    "gpt-5",
				RequestOptions: providerdomain.ProviderRequestOptions{
					AnthropicManual: providerdomain.AnthropicManualThinkingOptions{
						ThinkingBudgetTokens: providerdomain.OptionalInt{Value: 2048, Set: true},
					},
				},
			},
			want: "thinking-budget-tokens requires provider anthropic",
		},
		{
			name: "thinking budget rejects small value",
			in: Inputs{
				Provider: "anthropic",
				Model:    "claude-sonnet-4-20250514",
				RequestOptions: providerdomain.ProviderRequestOptions{
					AnthropicManual: providerdomain.AnthropicManualThinkingOptions{
						ThinkingBudgetTokens: providerdomain.OptionalInt{Value: 1023, Set: true},
					},
				},
			},
			want: "thinking-budget-tokens must be at least 1024",
		},
		{
			name: "thinking budget rejects unsupported model",
			in: Inputs{
				Provider: "anthropic",
				Model:    "claude-3-5-sonnet-20241022",
				RequestOptions: providerdomain.ProviderRequestOptions{
					AnthropicManual: providerdomain.AnthropicManualThinkingOptions{
						ThinkingBudgetTokens: providerdomain.OptionalInt{Value: 2048, Set: true},
					},
				},
			},
			want: "thinking-budget-tokens requires an Anthropic extended-thinking-capable model",
		},
		{
			name: "thinking budget must be less than default max tokens",
			in: Inputs{
				Provider: "anthropic",
				Model:    "claude-sonnet-4-20250514",
				RequestOptions: providerdomain.ProviderRequestOptions{
					AnthropicManual: providerdomain.AnthropicManualThinkingOptions{
						ThinkingBudgetTokens: providerdomain.OptionalInt{Value: providerdomain.DefaultAnthropicMaxTokens, Set: true},
					},
				},
			},
			want: "thinking-budget-tokens must be less than effective anthropic max_tokens (4096)",
		},
		{
			name: "thinking budget must be less than requested max tokens",
			in: Inputs{
				Provider: "anthropic",
				Model:    "claude-sonnet-4-20250514",
				RequestOptions: providerdomain.ProviderRequestOptions{
					AnthropicManual: providerdomain.AnthropicManualThinkingOptions{
						ThinkingBudgetTokens: providerdomain.OptionalInt{Value: 4096, Set: true},
					},
					Output: providerdomain.ProviderOutputOptions{
						MaxOutputTokens: providerdomain.OptionalInt{Value: 4096, Set: true},
					},
				},
			},
			want: "thinking-budget-tokens must be less than effective anthropic max_tokens (4096)",
		},
		{
			name: "thinking budget rejects non-auto effort",
			in: Inputs{
				Provider: "anthropic",
				Model:    "claude-sonnet-4-20250514",
				RequestOptions: providerdomain.ProviderRequestOptions{
					Effort: providerdomain.ProviderEffortOptions{Level: "high", Set: true},
					AnthropicManual: providerdomain.AnthropicManualThinkingOptions{
						ThinkingBudgetTokens: providerdomain.OptionalInt{Value: 2048, Set: true},
					},
				},
			},
			want: "thinking-budget-tokens requires either no --effort flag or --effort auto",
		},
		{
			name: "thinking budget rejects Opus 4.7+",
			in: Inputs{
				Provider: "anthropic",
				Model:    "claude-opus-4-7-20250514",
				RequestOptions: providerdomain.ProviderRequestOptions{
					AnthropicManual: providerdomain.AnthropicManualThinkingOptions{
						ThinkingBudgetTokens: providerdomain.OptionalInt{Value: 2048, Set: true},
					},
				},
			},
			want: "thinking-budget-tokens is not supported for model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveConfig(tt.in, envMap(map[string]string{"CLNKR_API_KEY": "test-key"}))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ResolveConfig() err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestResolveConfigNormalizesBaseURLTrailingSlash(t *testing.T) {
	tests := []struct {
		name    string
		inputs  Inputs
		wantURL string
	}{
		{
			name: "openai path",
			inputs: Inputs{
				Provider: "openai",
				Model:    "gpt-test",
				BaseURL:  "https://api.openai.com/v1/",
			},
			wantURL: "https://api.openai.com/v1",
		},
		{
			name: "anthropic root",
			inputs: Inputs{
				Provider: "anthropic",
				Model:    "claude-sonnet-4-6",
				BaseURL:  "https://api.anthropic.com/",
			},
			wantURL: "https://api.anthropic.com",
		},
		{
			name: "custom root",
			inputs: Inputs{
				Provider: "openai",
				Model:    "gpt-test",
				BaseURL:  "https://example.com/",
			},
			wantURL: "https://example.com",
		},
		{
			name: "preserves internal repeated slashes",
			inputs: Inputs{
				Provider: "openai",
				Model:    "gpt-test",
				BaseURL:  "https://example.com/proxy//v1/",
			},
			wantURL: "https://example.com/proxy//v1",
		},
		{
			name: "preserves escaped slash",
			inputs: Inputs{
				Provider: "openai",
				Model:    "gpt-test",
				BaseURL:  "https://example.com/proxy%2Fv1/",
			},
			wantURL: "https://example.com/proxy%2Fv1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ResolveConfig(tt.inputs, envMap(map[string]string{
				"CLNKR_API_KEY": "test-key",
			}))
			if err != nil {
				t.Fatalf("ResolveConfig(): %v", err)
			}
			if cfg.BaseURL != tt.wantURL {
				t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, tt.wantURL)
			}
		})
	}
}

func envMap(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}
