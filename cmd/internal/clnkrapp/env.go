package clnkrapp

import (
	"strings"

	"github.com/clnkr-ai/clnkr/cmd/internal/providerconfig"
)

func CommandEnvFromProviderConfig(cfg providerconfig.ResolvedProviderConfig, env []string) map[string]string {
	base := envMapFromList(env)
	base["CLNKR_PROVIDER"] = string(cfg.Provider)
	base["CLNKR_PROVIDER_API"] = string(cfg.ProviderAPI)
	base["CLNKR_MODEL"] = cfg.Model
	base["CLNKR_BASE_URL"] = cfg.BaseURL
	if cfg.ActProtocol != "" {
		base["CLNKR_ACT_PROTOCOL"] = string(cfg.ActProtocol)
	}
	return base
}

func envMapFromList(env []string) map[string]string {
	values := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		values[key] = value
	}
	return values
}
