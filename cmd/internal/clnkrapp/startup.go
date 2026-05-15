package clnkrapp

import (
	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/providerconfig"
)

type StartupInputs struct {
	CWD, Version, Provider, ProviderAPI, Model, BaseURL, ActProtocol string
	Env                                                              func(string) string
	Environ                                                          []string
	ActProtocolSet, MaxOutputTokensSet, ThinkingBudgetTokensSet      bool
	Effort, SystemPromptAppend                                       string
	MaxOutputTokens, ThinkingBudgetTokens                            int
	OmitSystemPrompt, DumpSystemPrompt, Unattended                   bool
}

type Startup struct {
	SystemPrompt string
	Metadata     RunMetadata
	Agent        *clnkr.Agent
	Driver       *Driver
}

func LoadStartupPrompt(inputs StartupInputs) (string, error) {
	systemPrompt, _, err := startupPromptAndProtocol(inputs)
	return systemPrompt, err
}

func PrepareStartup(inputs StartupInputs) (Startup, error) {
	systemPrompt, actProtocolSetting, err := startupPromptAndProtocol(inputs)
	if err != nil {
		return Startup{}, err
	}
	cfg, err := providerconfig.ResolveConfig(startupProviderInputs(inputs, actProtocolSetting), startupEnv(inputs.Env))
	if err != nil {
		return Startup{}, err
	}
	agent := clnkr.NewAgent(newModelForConfigWithOptions(cfg, systemPrompt, modelOptions{Unattended: inputs.Unattended}), &clnkr.ShellExecutor{}, inputs.CWD)
	agent.SetEnv(commandEnvFromProviderConfig(cfg, inputs.Environ))
	agent.ActProtocol = cfg.ActProtocol
	return Startup{SystemPrompt: systemPrompt, Metadata: newRunMetadata(inputs.Version, cfg, systemPrompt), Agent: agent, Driver: NewDriver(agent, makeCompactorFactory(cfg))}, nil
}

func IsMissingAPIKey(err error) bool { return providerconfig.IsMissingAPIKey(err) }

func startupEnv(env func(string) string) func(string) string {
	if env == nil {
		return func(string) string { return "" }
	}
	return env
}

func startupPromptAndProtocol(inputs StartupInputs) (string, providerconfig.ActProtocolSetting, error) {
	env := startupEnv(inputs.Env)
	setting, err := providerconfig.ParseActProtocolSetting(providerconfig.ActProtocolFlagValue(inputs.ActProtocol, inputs.ActProtocolSet, env))
	if err != nil {
		return "", "", err
	}
	actProtocol := clnkr.ActProtocolClnkrInline
	if !inputs.OmitSystemPrompt {
		actProtocol, err = providerconfig.ResolvePromptActProtocol(startupProviderInputs(inputs, setting), env, inputs.DumpSystemPrompt)
		if err != nil {
			return "", "", err
		}
	}
	return clnkr.LoadPromptWithOptions(inputs.CWD, clnkr.PromptOptions{OmitSystemPrompt: inputs.OmitSystemPrompt, SystemPromptAppend: inputs.SystemPromptAppend, ActProtocol: actProtocol, Unattended: inputs.Unattended}), setting, nil
}

func startupProviderInputs(inputs StartupInputs, setting providerconfig.ActProtocolSetting) providerconfig.Inputs {
	return providerconfig.Inputs{Provider: inputs.Provider, ProviderAPI: inputs.ProviderAPI, Model: inputs.Model, BaseURL: inputs.BaseURL, ActProtocol: setting, RequestOptions: requestOptions(inputs.Effort, inputs.MaxOutputTokens, inputs.MaxOutputTokensSet, inputs.ThinkingBudgetTokens, inputs.ThinkingBudgetTokensSet)}
}
