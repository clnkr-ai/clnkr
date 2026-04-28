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
	switch {
	case cfg.Provider == providerconfig.ProviderAnthropic:
		opts := anthropic.Options{}
		if cfg.HarnessOptions.MaxOutputTokens.Set {
			opts.MaxTokens = cfg.HarnessOptions.MaxOutputTokens.Value
		}
		if cfg.HarnessOptions.ThinkingBudgetTokens.Set {
			opts.ThinkingBudgetTokens = cfg.HarnessOptions.ThinkingBudgetTokens.Value
		}
		return anthropic.NewModelWithOptions(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt, opts)
	case cfg.ProviderAPI == providerconfig.ProviderAPIOpenAIResponses:
		return openairesponses.NewModelWithOptions(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt, openairesponses.Options{
			ReasoningEffort:    cfg.HarnessOptions.ReasoningEffort,
			MaxOutputTokens:    cfg.HarnessOptions.MaxOutputTokens.Value,
			HasMaxOutputTokens: cfg.HarnessOptions.MaxOutputTokens.Set,
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

type HarnessMetadata struct {
	ClnkrVersion string                     `json:"clnkr_version"`
	Provider     providerconfig.Provider    `json:"provider"`
	ProviderAPI  providerconfig.ProviderAPI `json:"provider_api"`
	Model        string                     `json:"model"`
	PromptSHA256 string                     `json:"prompt_sha256"`
	Requested    HarnessRequestedMetadata   `json:"requested"`
	Effective    HarnessEffectiveMetadata   `json:"effective"`
	Compaction   CompactionMetadata         `json:"compaction"`
}

type HarnessRequestedMetadata struct {
	ReasoningEffort      *string `json:"reasoning_effort"`
	ThinkingBudgetTokens *int    `json:"thinking_budget_tokens"`
	MaxOutputTokens      *int    `json:"max_output_tokens"`
}

type HarnessEffectiveMetadata struct {
	ReasoningEffortOmitted      bool    `json:"reasoning_effort_omitted"`
	ReasoningEffort             *string `json:"reasoning_effort"`
	ThinkingBudgetTokensOmitted bool    `json:"thinking_budget_tokens_omitted"`
	ThinkingBudgetTokens        *int    `json:"thinking_budget_tokens"`
	MaxOutputTokensOmitted      bool    `json:"max_output_tokens_omitted"`
	MaxOutputTokens             *int    `json:"max_output_tokens"`
	AnthropicMaxTokens          *int    `json:"anthropic_max_tokens"`
}

type CompactionMetadata struct {
	Policy          string `json:"policy"`
	KeepRecentTurns int    `json:"keep_recent_turns"`
}

func NewHarnessMetadata(version string, cfg providerconfig.ResolvedProviderConfig, systemPrompt string) HarnessMetadata {
	opts := cfg.HarnessOptions
	meta := HarnessMetadata{
		ClnkrVersion: version,
		Provider:     cfg.Provider,
		ProviderAPI:  cfg.ProviderAPI,
		Model:        cfg.Model,
		PromptSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(systemPrompt))),
		Requested: HarnessRequestedMetadata{
			ReasoningEffort:      stringPtr(opts.ReasoningEffort),
			ThinkingBudgetTokens: optionalIntPtr(opts.ThinkingBudgetTokens),
			MaxOutputTokens:      optionalIntPtr(opts.MaxOutputTokens),
		},
		Effective: HarnessEffectiveMetadata{
			ReasoningEffortOmitted:      opts.ReasoningEffort == "",
			ReasoningEffort:             effectiveReasoningEffort(cfg),
			ThinkingBudgetTokensOmitted: !opts.ThinkingBudgetTokens.Set,
			ThinkingBudgetTokens:        optionalIntPtr(opts.ThinkingBudgetTokens),
			MaxOutputTokensOmitted:      !opts.MaxOutputTokens.Set,
			MaxOutputTokens:             optionalIntPtr(opts.MaxOutputTokens),
		},
		Compaction: CompactionMetadata{Policy: "manual", KeepRecentTurns: 2},
	}
	if cfg.Provider == providerconfig.ProviderAnthropic {
		maxTokens := providerconfig.DefaultAnthropicMaxTokens
		if opts.MaxOutputTokens.Set {
			maxTokens = opts.MaxOutputTokens.Value
		}
		meta.Effective.AnthropicMaxTokens = &maxTokens
	}
	return meta
}

func HarnessMetadataDebugEvent(meta HarnessMetadata) (clnkr.EventDebug, error) {
	data, err := json.Marshal(meta)
	if err != nil {
		return clnkr.EventDebug{}, fmt.Errorf("marshal harness metadata: %w", err)
	}
	return clnkr.EventDebug{Message: string(data)}, nil
}

func effectiveReasoningEffort(cfg providerconfig.ResolvedProviderConfig) *string {
	if cfg.ProviderAPI != providerconfig.ProviderAPIOpenAIResponses || cfg.HarnessOptions.ReasoningEffort == "" {
		return nil
	}
	return &cfg.HarnessOptions.ReasoningEffort
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
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
