package clnkrapp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/compaction"
	"github.com/clnkr-ai/clnkr/cmd/internal/providerconfig"
	"github.com/clnkr-ai/clnkr/internal/providerfactory"
	providerdomain "github.com/clnkr-ai/clnkr/internal/providers/providerconfig"
)

type model interface {
	clnkr.Model
	compaction.FreeformModel
}

type modelOptions struct {
	Unattended bool
}

func newModelForConfig(cfg providerconfig.ResolvedProviderConfig, systemPrompt string) model {
	return newModelForConfigWithOptions(cfg, systemPrompt, modelOptions{})
}

func newModelForConfigWithOptions(
	cfg providerconfig.ResolvedProviderConfig,
	systemPrompt string,
	modelOpts modelOptions,
) model {
	return providerfactory.NewModelWithOptions(
		providerFactoryConfig(cfg),
		systemPrompt,
		providerfactory.Options{Unattended: modelOpts.Unattended},
	)
}

func providerFactoryConfig(cfg providerconfig.ResolvedProviderConfig) providerfactory.Config {
	return providerfactory.Config{
		Provider:       cfg.Provider,
		ProviderAPI:    cfg.ProviderAPI,
		Model:          cfg.Model,
		BaseURL:        cfg.BaseURL,
		APIKey:         cfg.APIKey,
		ActProtocol:    cfg.ActProtocol,
		RequestOptions: cfg.RequestOptions,
	}
}

func makeCompactorFactory(cfg providerconfig.ResolvedProviderConfig) compaction.Factory {
	return compaction.NewFactory(func(instructions string) compaction.FreeformModel {
		return newModelForConfig(cfg, compaction.LoadCompactionPrompt(instructions))
	})
}

func requestOptions(
	effort string,
	maxOutputTokens int,
	maxOutputTokensSet bool,
	thinkingBudgetTokens int,
	thinkingBudgetTokensSet bool,
) providerdomain.ProviderRequestOptions {
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

func WriteEventLog(w io.Writer, e clnkr.Event) error {
	switch e := e.(type) {
	case clnkr.EventResponse:
		canonical, err := clnkr.CanonicalTurnJSON(e.Turn)
		if err != nil {
			return nil
		}
		payload := map[string]any{
			"turn": json.RawMessage(canonical),
			"usage": map[string]int{
				"input_tokens":  e.Usage.InputTokens,
				"output_tokens": e.Usage.OutputTokens,
			},
		}
		if e.Raw != "" {
			payload["raw"] = e.Raw
		}
		return writeEvent(w, "response", payload)
	case clnkr.EventCommandStart:
		return writeEvent(w, "command_start", map[string]string{"command": e.Command, "dir": e.Dir})
	case clnkr.EventCommandDone:
		payload := map[string]any{
			"command":   e.Command,
			"stdout":    e.Stdout,
			"stderr":    e.Stderr,
			"exit_code": e.ExitCode,
			"feedback":  e.Feedback,
		}
		if e.Err != nil {
			payload["err"] = e.Err.Error()
		}
		return writeEvent(w, "command_done", payload)
	case clnkr.EventProtocolFailure:
		return writeEvent(
			w,
			"protocol_failure",
			map[string]string{"reason": e.Reason, "raw": e.Raw},
		)
	case clnkr.EventCompletionGate:
		return writeEvent(w, "completion_gate", map[string]any{
			"decision": e.Decision,
			"reasons":  e.Reasons,
			"summary":  e.Summary,
		})
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

func newRunMetadata(
	version string,
	cfg providerconfig.ResolvedProviderConfig,
	systemPrompt string,
) RunMetadata {
	opts := cfg.RequestOptions

	meta := RunMetadata{
		ClnkrVersion: version,
		Provider:     cfg.Provider,
		ProviderAPI:  cfg.ProviderAPI,
		Model:        cfg.Model,
		PromptSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(systemPrompt))),
		ActProtocol:  cfg.ActProtocol,
		Requested:    providerRequestMetadata(opts, !opts.Effort.Set),
		Effective: providerRequestMetadata(
			opts,
			!opts.Effort.Set || opts.Effort.Level == "auto",
		),
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

func providerRequestMetadata(
	opts providerdomain.ProviderRequestOptions,
	effortOmitted bool,
) ProviderRequestMetadata {
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
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, fmt.Errorf("parse messages: %w", err)
	}
	return messages, nil
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

func ParseCompactCommand(input string) (instructions string, ok bool) {
	input = strings.TrimSpace(input)
	if fields := strings.Fields(input); len(fields) == 0 || fields[0] != "/compact" {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(input, "/compact")), true
}

func HandleCompactCommand(
	ctx context.Context,
	agent *clnkr.Agent,
	input string,
	factory compaction.Factory,
) (clnkr.CompactStats, bool, error) {
	instructions, ok := ParseCompactCommand(input)
	if !ok {
		return clnkr.CompactStats{}, false, nil
	}
	stats, err := compactTranscript(ctx, agent, instructions, factory)
	return stats, err == nil, err
}

func compactTranscript(
	ctx context.Context, agent *clnkr.Agent, instructions string, factory compaction.Factory,
) (clnkr.CompactStats, error) {
	if factory == nil {
		return clnkr.CompactStats{}, fmt.Errorf("compact command: no compactor factory configured")
	}
	compactor := factory(instructions)
	if compactor == nil {
		return clnkr.CompactStats{}, fmt.Errorf("compact command: no compactor configured")
	}
	return agent.Compact(ctx, compactor, clnkr.CompactOptions{
		Instructions: instructions, KeepRecentTurns: 2,
	})
}

func RejectCompactCommand(input string) error {
	if _, ok := ParseCompactCommand(input); ok {
		return fmt.Errorf("/compact is only available at the conversational prompt")
	}
	return nil
}
