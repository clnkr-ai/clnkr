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
	"github.com/clnkr-ai/clnkr/internal/providers/anthropic"
	"github.com/clnkr-ai/clnkr/internal/providers/openai"
	"github.com/clnkr-ai/clnkr/internal/providers/openairesponses"
)

type model interface {
	clnkr.Model
	compaction.FreeformModel
}

func NewModelForConfig(cfg providerconfig.ResolvedProviderConfig, systemPrompt string) clnkr.Model {
	return newModelForConfig(cfg, systemPrompt)
}

func newModelForConfig(cfg providerconfig.ResolvedProviderConfig, systemPrompt string) model {
	opts := cfg.RequestOptions
	switch {
	case cfg.Provider == providerconfig.ProviderAnthropic:
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
		return anthropic.NewModelWithOptions(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt, anthropicOpts)
	case cfg.ProviderAPI == providerconfig.ProviderAPIOpenAIResponses:
		var effort string
		if opts.Effort.Set && opts.Effort.Level != "auto" {
			effort = opts.Effort.Level
		}
		return openairesponses.NewModelWithOptions(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt, openairesponses.Options{
			ReasoningEffort:    effort,
			MaxOutputTokens:    opts.Output.MaxOutputTokens.Value,
			HasMaxOutputTokens: opts.Output.MaxOutputTokens.Set,
		})
	default:
		return openai.NewModel(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt)
	}
}

func MakeCompactorFactory(cfg providerconfig.ResolvedProviderConfig) compaction.Factory {
	return compaction.NewFactory(func(instructions string) compaction.FreeformModel {
		return newModelForConfig(cfg, compaction.LoadCompactionPrompt(instructions))
	})
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
	return json.NewEncoder(w).Encode(struct {
		Type    string `json:"type"`
		Payload any    `json:"payload"`
	}{typ, payload})
}

// RunMetadata describes the configuration for a clnkr run, recorded as debug
// metadata and persisted alongside session files.
type RunMetadata struct {
	ClnkrVersion string                     `json:"clnkr_version"`
	Provider     providerconfig.Provider    `json:"provider"`
	ProviderAPI  providerconfig.ProviderAPI `json:"provider_api"`
	Model        string                     `json:"model"`
	PromptSHA256 string                     `json:"prompt_sha256"`
	Requested    RequestedProviderOptions   `json:"requested"`
	Effective    EffectiveProviderOptions   `json:"effective"`
	Compaction   CompactionMetadata         `json:"compaction"`
}

type RequestedProviderOptions struct {
	Effort                      *string `json:"effort,omitempty"`
	EffortOmitted               bool    `json:"effort_omitted"`
	MaxOutputTokens             *int    `json:"max_output_tokens,omitempty"`
	MaxOutputTokensOmitted      bool    `json:"max_output_tokens_omitted"`
	ThinkingBudgetTokens        *int    `json:"thinking_budget_tokens,omitempty"`
	ThinkingBudgetTokensOmitted bool    `json:"thinking_budget_tokens_omitted"`
}

type EffectiveProviderOptions struct {
	EffortOmitted               bool    `json:"effort_omitted"`
	Effort                      *string `json:"effort,omitempty"`
	AnthropicThinkingMode       *string `json:"anthropic_thinking_mode,omitempty"`
	MaxOutputTokens             *int    `json:"max_output_tokens,omitempty"`
	MaxOutputTokensOmitted      bool    `json:"max_output_tokens_omitted"`
	AnthropicMaxTokens          *int    `json:"anthropic_max_tokens,omitempty"`
	ThinkingBudgetTokens        *int    `json:"thinking_budget_tokens,omitempty"`
	ThinkingBudgetTokensOmitted bool    `json:"thinking_budget_tokens_omitted"`
}

type CompactionMetadata struct {
	Policy          string `json:"policy"`
	KeepRecentTurns int    `json:"keep_recent_turns"`
}

func NewRunMetadata(version string, cfg providerconfig.ResolvedProviderConfig, systemPrompt string) RunMetadata {
	opts := cfg.RequestOptions
	effortOmitted := !opts.Effort.Set || opts.Effort.Level == "auto"
	effortValue := opts.Effort.Level
	if effortOmitted {
		effortValue = ""
	}

	meta := RunMetadata{
		ClnkrVersion: version,
		Provider:     cfg.Provider,
		ProviderAPI:  cfg.ProviderAPI,
		Model:        cfg.Model,
		PromptSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(systemPrompt))),
		Requested: RequestedProviderOptions{
			EffortOmitted:               effortOmitted,
			Effort:                      effectiveIfSet(effortValue),
			MaxOutputTokensOmitted:      !opts.Output.MaxOutputTokens.Set,
			MaxOutputTokens:             optionalIntPtr(opts.Output.MaxOutputTokens),
			ThinkingBudgetTokensOmitted: !opts.AnthropicManual.ThinkingBudgetTokens.Set,
			ThinkingBudgetTokens:        optionalIntPtr(opts.AnthropicManual.ThinkingBudgetTokens),
		},
		Effective: EffectiveProviderOptions{
			EffortOmitted:          effortOmitted,
			Effort:                 effectiveIfSet(effortValue),
			MaxOutputTokensOmitted: !opts.Output.MaxOutputTokens.Set,
			MaxOutputTokens:        optionalIntPtr(opts.Output.MaxOutputTokens),
		},
		Compaction: CompactionMetadata{Policy: "manual", KeepRecentTurns: 2},
	}

	if cfg.Provider == providerconfig.ProviderAnthropic {
		maxTokens := providerconfig.DefaultAnthropicMaxTokens
		if opts.Output.MaxOutputTokens.Set {
			maxTokens = opts.Output.MaxOutputTokens.Value
		}
		meta.Effective.AnthropicMaxTokens = &maxTokens

		// Determine effective Anthropic thinking mode
		if opts.Effort.Set && opts.Effort.Level != "auto" {
			// Non-auto effort: also enable adaptive thinking
			adaptive := "adaptive"
			meta.Effective.AnthropicThinkingMode = &adaptive
			meta.Effective.Effort = &opts.Effort.Level
			meta.Effective.EffortOmitted = false
		} else if opts.AnthropicManual.ThinkingBudgetTokens.Set {
			enabled := "enabled"
			meta.Effective.AnthropicThinkingMode = &enabled
		}
	}

	return meta
}

func RunMetadataDebugEvent(meta RunMetadata) (clnkr.EventDebug, error) {
	data, err := json.Marshal(meta)
	if err != nil {
		return clnkr.EventDebug{}, fmt.Errorf("marshal run metadata: %w", err)
	}
	return clnkr.EventDebug{Message: string(data)}, nil
}

func effectiveIfSet(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func optionalIntPtr(value providerconfig.OptionalInt) *int {
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
		Messages *[]clnkr.Message `json:"messages"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("parse messages: %w", err)
	}
	if envelope.Messages == nil {
		return nil, fmt.Errorf("parse messages: missing messages")
	}
	return *envelope.Messages, nil
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

type ApprovalPrompter interface {
	ActReply(context.Context, string) (string, error)
	Clarify(context.Context, string) (string, error)
}

func RunApprovalTask(ctx context.Context, agent *clnkr.Agent, task string, prompter ApprovalPrompter, reportRejected func(error)) error {
	agent.AppendUserMessage(task)
	steps, protocolErrors := 0, 0
	for {
		result, err := agent.Step(ctx)
		if err != nil {
			return err
		}
		if result.ParseErr != nil {
			protocolErrors++
			if protocolErrors >= 3 {
				return fmt.Errorf("consecutive protocol failures, exiting")
			}
			continue
		}
		protocolErrors = 0
		switch turn := result.Turn.(type) {
		case *clnkr.DoneTurn:
			return nil
		case *clnkr.ClarifyTurn:
			reply, err := waitForReply(ctx, prompter.Clarify, turn.Question, reportRejected)
			if err != nil {
				return err
			}
			agent.AppendUserMessage(reply)
		case *clnkr.ActTurn:
			reply, err := waitForReply(ctx, prompter.ActReply, FormatActProposal(turn.Bash.Commands), reportRejected)
			if err != nil {
				return err
			}
			if strings.TrimSpace(reply) != "y" {
				agent.AppendUserMessage(reply)
				continue
			}
			result, err := agent.ExecuteTurn(ctx, turn)
			if err != nil {
				return err
			}
			steps += result.ExecCount
			if agent.MaxSteps > 0 && steps >= agent.MaxSteps {
				return agent.RequestStepLimitSummary(ctx)
			}
		default:
			return fmt.Errorf("unexpected turn type %T", turn)
		}
	}
}

func waitForReply(ctx context.Context, prompt func(context.Context, string) (string, error), text string, reportRejected func(error)) (string, error) {
	for {
		reply, err := prompt(ctx, text)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(reply) == "" {
			continue
		}
		if err := RejectCompactCommand(reply); err != nil {
			if reportRejected != nil {
				reportRejected(err)
			}
			continue
		}
		return reply, nil
	}
}

func FormatActProposal(commands []clnkr.BashAction) string {
	var b strings.Builder
	for i, action := range commands {
		if i > 0 {
			b.WriteByte('\n')
		}
		command := action.Command
		if workdir := strings.TrimSpace(action.Workdir); workdir != "" {
			command = fmt.Sprintf("%s in %s", command, workdir)
		}
		fmt.Fprintf(&b, "%d. %s", i+1, command)
	}
	return b.String()
}
