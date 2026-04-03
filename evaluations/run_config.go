package evaluations

import "fmt"

// RunConfig selects the provider endpoint and model for one trial run.
type RunConfig struct {
	Mode    Mode
	APIKey  string
	BaseURL string
	Model   string
}

// LoadRunConfigFromEnv loads evaluation runtime configuration from CLNKR_EVALUATION_*.
func LoadRunConfigFromEnv(getenv func(string) string) (RunConfig, error) {
	mode := Mode(getenv("CLNKR_EVALUATION_MODE"))
	if mode == "" {
		mode = ModeMockProvider
	}

	switch mode {
	case ModeMockProvider:
		return RunConfig{
			Mode:   ModeMockProvider,
			APIKey: "dummy-key",
			Model:  "test-model",
		}, nil
	case ModeLiveProvider:
		cfg := RunConfig{
			Mode:    ModeLiveProvider,
			APIKey:  getenv("CLNKR_EVALUATION_API_KEY"),
			BaseURL: getenv("CLNKR_EVALUATION_BASE_URL"),
			Model:   getenv("CLNKR_EVALUATION_MODEL"),
		}
		if cfg.Model == "" {
			cfg.Model = "gpt-5.4-nano"
		}
		if cfg.APIKey == "" {
			return RunConfig{}, fmt.Errorf("load run config: live-provider mode missing API key")
		}
		if cfg.BaseURL == "" {
			return RunConfig{}, fmt.Errorf("load run config: live-provider mode missing base URL")
		}
		return cfg, nil
	default:
		return RunConfig{}, fmt.Errorf("load run config: unknown CLNKR_EVALUATION_MODE %q", mode)
	}
}
