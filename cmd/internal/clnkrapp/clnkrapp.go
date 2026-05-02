package clnkrapp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/compaction"
	"github.com/clnkr-ai/clnkr/cmd/internal/providerconfig"
	"github.com/clnkr-ai/clnkr/cmd/internal/session"
	"github.com/clnkr-ai/clnkr/internal/providers/anthropic"
	"github.com/clnkr-ai/clnkr/internal/providers/openai"
	"github.com/clnkr-ai/clnkr/internal/providers/openaicodexauth"
	"github.com/clnkr-ai/clnkr/internal/providers/openairesponses"
	providerdomain "github.com/clnkr-ai/clnkr/internal/providers/providerconfig"
)

type model interface {
	clnkr.Model
	compaction.FreeformModel
}

func NewModelForConfig(cfg providerconfig.ResolvedProviderConfig, systemPrompt string) model {
	opts := cfg.RequestOptions
	switch {
	case cfg.Provider == providerdomain.ProviderAnthropic:
		anthropicOpts := anthropic.Options{}
		if opts.Output.MaxOutputTokens.Set {
			anthropicOpts.MaxTokens = opts.Output.MaxOutputTokens.Value
		}
		if opts.AnthropicManual.ThinkingBudgetTokens.Set {
			anthropicOpts.ThinkingBudgetTokens = opts.AnthropicManual.ThinkingBudgetTokens.Value
			anthropicOpts.ThinkingMode = anthropic.ThinkingModeManual
		}
		if opts.Effort.Set && opts.Effort.Level != "auto" {
			anthropicOpts.Effort = opts.Effort.Level
			anthropicOpts.ThinkingMode = anthropic.ThinkingModeAdaptive
		}
		anthropicOpts.UseBashToolCalls = cfg.ActProtocol == clnkr.ActProtocolToolCalls
		return anthropic.NewModelWithOptions(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt, anthropicOpts)
	case cfg.Provider == providerdomain.ProviderOpenAICodex:
		return openairesponses.NewModelWithRequestOptions(cfg.BaseURL, "", cfg.Model, systemPrompt, opts.Effort.Level, opts.Effort.Set, opts.Output.MaxOutputTokens.Value, opts.Output.MaxOutputTokens.Set, cfg.ActProtocol == clnkr.ActProtocolToolCalls, true, openaicodexauth.NewManager(openaicodexauth.Config{}))
	case cfg.ProviderAPI == providerdomain.ProviderAPIOpenAIResponses:
		return openairesponses.NewModelWithRequestOptions(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt, opts.Effort.Level, opts.Effort.Set, opts.Output.MaxOutputTokens.Value, opts.Output.MaxOutputTokens.Set, cfg.ActProtocol == clnkr.ActProtocolToolCalls, false, nil)
	default:
		return openai.NewModel(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt)
	}
}

func MakeCompactorFactory(cfg providerconfig.ResolvedProviderConfig) compaction.Factory {
	return compaction.NewFactory(func(instructions string) compaction.FreeformModel {
		return NewModelForConfig(cfg, compaction.LoadCompactionPrompt(instructions))
	})
}

func RequestOptions(effort string, maxOutputTokens int, maxOutputTokensSet bool, thinkingBudgetTokens int, thinkingBudgetTokensSet bool) providerdomain.ProviderRequestOptions {
	return providerdomain.ProviderRequestOptions{
		Effort: providerdomain.ProviderEffortOptions{
			Level: effort,
			Set:   effort != "",
		},
		Output: providerdomain.ProviderOutputOptions{
			MaxOutputTokens: requestInt(maxOutputTokens, maxOutputTokensSet),
		},
		AnthropicManual: providerdomain.AnthropicManualThinkingOptions{
			ThinkingBudgetTokens: requestInt(thinkingBudgetTokens, thinkingBudgetTokensSet),
		},
	}
}

func requestInt(value int, set bool) providerdomain.OptionalInt {
	return providerdomain.OptionalInt{Value: value, Set: set}
}

func RequestOptionFlagsSet(flags *flag.FlagSet) (maxOutputTokensSet, thinkingBudgetTokensSet bool) {
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "max-output-tokens":
			maxOutputTokensSet = true
		case "thinking-budget-tokens":
			thinkingBudgetTokensSet = true
		}
	})
	return maxOutputTokensSet, thinkingBudgetTokensSet
}

func WriteEventLog(w io.Writer, e clnkr.Event) error {
	switch e := e.(type) {
	case clnkr.EventResponse:
		canonical, err := clnkr.CanonicalTurnJSON(e.Turn)
		if err != nil {
			return nil
		}
		payload := map[string]any{"turn": json.RawMessage(canonical), "usage": map[string]int{"input_tokens": e.Usage.InputTokens, "output_tokens": e.Usage.OutputTokens}}
		if e.Raw != "" {
			payload["raw"] = e.Raw
		}
		return writeEvent(w, "response", payload)
	case clnkr.EventCommandStart:
		return writeEvent(w, "command_start", map[string]string{"command": e.Command, "dir": e.Dir})
	case clnkr.EventCommandDone:
		payload := map[string]any{"command": e.Command, "stdout": e.Stdout, "stderr": e.Stderr, "exit_code": e.ExitCode, "feedback": e.Feedback}
		if e.Err != nil {
			payload["err"] = e.Err.Error()
		}
		return writeEvent(w, "command_done", payload)
	case clnkr.EventProtocolFailure:
		return writeEvent(w, "protocol_failure", map[string]string{"reason": e.Reason, "raw": e.Raw})
	case clnkr.EventDebug:
		return writeEvent(w, "debug", map[string]string{"message": e.Message})
	default:
		return nil
	}
}

func writeEvent(w io.Writer, typ string, payload any) error {
	return json.NewEncoder(w).Encode(map[string]any{"type": typ, "payload": payload})
}

// RunMetadata describes the configuration for a clnkr run, recorded as debug
// metadata and persisted alongside session files.
type RunMetadata struct {
	ClnkrVersion string                     `json:"clnkr_version"`
	Provider     providerdomain.Provider    `json:"provider"`
	ProviderAPI  providerdomain.ProviderAPI `json:"provider_api"`
	Model        string                     `json:"model"`
	PromptSHA256 string                     `json:"prompt_sha256"`
	ActProtocol  clnkr.ActProtocol          `json:"act_protocol"`
	Requested    ProviderRequestMetadata    `json:"requested"`
	Effective    ProviderRequestMetadata    `json:"effective"`
}

type ProviderRequestMetadata struct {
	Effort          EffortMetadata          `json:"effort"`
	Output          OutputMetadata          `json:"output"`
	AnthropicManual AnthropicManualMetadata `json:"anthropic_manual"`
}

type EffortMetadata struct {
	LevelOmitted          bool    `json:"level_omitted"`
	Level                 *string `json:"level,omitempty"`
	AnthropicThinkingMode *string `json:"anthropic_thinking_mode,omitempty"`
}

type OutputMetadata struct {
	MaxOutputTokensOmitted bool `json:"max_output_tokens_omitted"`
	MaxOutputTokens        *int `json:"max_output_tokens,omitempty"`
	AnthropicMaxTokens     *int `json:"anthropic_max_tokens,omitempty"`
}

type AnthropicManualMetadata struct {
	ThinkingBudgetTokensOmitted bool `json:"thinking_budget_tokens_omitted"`
	ThinkingBudgetTokens        *int `json:"thinking_budget_tokens,omitempty"`
}

func NewRunMetadata(version string, cfg providerconfig.ResolvedProviderConfig, systemPrompt string) RunMetadata {
	opts := cfg.RequestOptions

	meta := RunMetadata{
		ClnkrVersion: version,
		Provider:     cfg.Provider,
		ProviderAPI:  cfg.ProviderAPI,
		Model:        cfg.Model,
		PromptSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(systemPrompt))),
		ActProtocol:  cfg.ActProtocol,
		Requested:    providerRequestMetadata(opts, !opts.Effort.Set),
		Effective:    providerRequestMetadata(opts, !opts.Effort.Set || opts.Effort.Level == "auto"),
	}

	if cfg.Provider == providerdomain.ProviderAnthropic {
		maxTokens := providerdomain.DefaultAnthropicMaxTokens
		if opts.Output.MaxOutputTokens.Set {
			maxTokens = opts.Output.MaxOutputTokens.Value
		}
		meta.Effective.Output.AnthropicMaxTokens = &maxTokens

		if opts.Effort.Set && opts.Effort.Level != "auto" {
			adaptive := "adaptive"
			meta.Effective.Effort.AnthropicThinkingMode = &adaptive
			meta.Effective.Effort.Level = &opts.Effort.Level
			meta.Effective.Effort.LevelOmitted = false
		} else if opts.AnthropicManual.ThinkingBudgetTokens.Set {
			enabled := "enabled"
			meta.Effective.Effort.AnthropicThinkingMode = &enabled
		}
	}

	return meta
}

func providerRequestMetadata(opts providerdomain.ProviderRequestOptions, effortOmitted bool) ProviderRequestMetadata {
	meta := ProviderRequestMetadata{
		Effort: EffortMetadata{LevelOmitted: effortOmitted},
		Output: OutputMetadata{
			MaxOutputTokensOmitted: !opts.Output.MaxOutputTokens.Set,
			MaxOutputTokens:        optionalIntPtr(opts.Output.MaxOutputTokens),
		},
		AnthropicManual: AnthropicManualMetadata{
			ThinkingBudgetTokensOmitted: !opts.AnthropicManual.ThinkingBudgetTokens.Set,
			ThinkingBudgetTokens:        optionalIntPtr(opts.AnthropicManual.ThinkingBudgetTokens),
		},
	}
	if !effortOmitted {
		meta.Effort.Level = &opts.Effort.Level
	}
	return meta
}

func RunMetadataDebugEvent(meta RunMetadata) clnkr.EventDebug {
	data, _ := json.Marshal(meta)
	return clnkr.EventDebug{Message: string(data)}
}

func optionalIntPtr(value providerdomain.OptionalInt) *int {
	if !value.Set {
		return nil
	}
	return &value.Value
}

func WriteTrajectory(path string, messages []clnkr.Message) error {
	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot marshal trajectory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("cannot write trajectory %q: %w", path, err)
	}
	return nil
}

func LoadMessages(data []byte) ([]clnkr.Message, error) {
	var messages []clnkr.Message
	if err := json.Unmarshal(data, &messages); err == nil {
		return messages, nil
	}
	var envelope struct {
		Messages []clnkr.Message `json:"messages"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("parse messages: %w", err)
	}
	if envelope.Messages == nil {
		return nil, fmt.Errorf("parse messages: missing messages")
	}
	return envelope.Messages, nil
}

func AddMessagesFile(agent *clnkr.Agent, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read messages file %q: %w", path, err)
	}
	msgs, err := LoadMessages(data)
	if err != nil {
		return fmt.Errorf("cannot parse messages file %q: %w", path, err)
	}
	if err := agent.AddMessages(msgs); err != nil {
		return fmt.Errorf("cannot load messages: %w", err)
	}
	return nil
}

func ResumeLatestSession(agent *clnkr.Agent, cwd string) (int, bool, error) {
	msgs, err := session.LoadLatestSession(cwd)
	if err != nil {
		return 0, false, fmt.Errorf("cannot load session: %w", err)
	}
	if msgs == nil {
		return 0, false, nil
	}
	if err := agent.AddMessages(msgs); err != nil {
		return 0, false, fmt.Errorf("cannot resume session: %w", err)
	}
	return len(msgs), true, nil
}

func ParseCompactCommand(input string) (instructions string, ok bool) {
	input = strings.TrimSpace(input)
	if fields := strings.Fields(input); len(fields) == 0 || fields[0] != "/compact" {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(input, "/compact")), true
}

func HandleCompactCommand(ctx context.Context, agent *clnkr.Agent, input string, factory compaction.Factory) (clnkr.CompactStats, bool, error) {
	instructions, ok := ParseCompactCommand(input)
	if !ok {
		return clnkr.CompactStats{}, false, nil
	}
	if factory == nil {
		return clnkr.CompactStats{}, false, fmt.Errorf("compact command: no compactor factory configured")
	}
	compactor := factory(instructions)
	if compactor == nil {
		return clnkr.CompactStats{}, false, fmt.Errorf("compact command: no compactor configured")
	}
	stats, err := agent.Compact(ctx, compactor, clnkr.CompactOptions{Instructions: instructions, KeepRecentTurns: 2})
	if err != nil {
		return clnkr.CompactStats{}, false, err
	}
	return stats, true, nil
}

func RejectCompactCommand(input string) error {
	if _, ok := ParseCompactCommand(input); ok {
		return fmt.Errorf("/compact is only available at the conversational prompt")
	}
	return nil
}
