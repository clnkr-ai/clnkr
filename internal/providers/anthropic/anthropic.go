package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/clnkr-ai/clnkr"
)

// ThinkingMode describes how Anthropic thinking is configured.
type ThinkingMode string

const (
	ThinkingModeOmitted  ThinkingMode = ""
	ThinkingModeEnabled  ThinkingMode = "enabled"
	ThinkingModeAdaptive ThinkingMode = "adaptive"
	ThinkingModeManual   ThinkingMode = "manual"
)

// Model talks to the Anthropic Messages API.
type Model struct {
	baseURL              string
	apiKey               string
	model                string
	systemPrompt         string
	maxTokens            int
	thinkingBudgetTokens int
	thinkingMode         ThinkingMode
	effort               string
	useBashToolCalls     bool
	unattended           bool
	client               *http.Client
}

const DefaultMaxTokens = 4096

type Options struct {
	MaxTokens            int
	ThinkingBudgetTokens int
	ThinkingMode         ThinkingMode
	Effort               string
	UseBashToolCalls     bool
	Unattended           bool
}

// NewModel sets up an Anthropic adapter.
func NewModel(baseURL, apiKey, model, systemPrompt string) *Model {
	return NewModelWithOptions(baseURL, apiKey, model, systemPrompt, Options{})
}

// NewModelWithOptions sets up an Anthropic adapter with request options.
func NewModelWithOptions(baseURL, apiKey, model, systemPrompt string, opts Options) *Model {
	maxTokens := DefaultMaxTokens
	if opts.MaxTokens > 0 {
		maxTokens = opts.MaxTokens
	}
	return &Model{
		baseURL:              baseURL,
		apiKey:               apiKey,
		model:                model,
		systemPrompt:         systemPrompt,
		maxTokens:            maxTokens,
		thinkingBudgetTokens: opts.ThinkingBudgetTokens,
		thinkingMode:         opts.ThinkingMode,
		effort:               opts.Effort,
		useBashToolCalls:     opts.UseBashToolCalls,
		unattended:           opts.Unattended,
		client:               &http.Client{Timeout: 240 * time.Second},
	}
}

type request struct {
	Model        string             `json:"model"`
	MaxTokens    int                `json:"max_tokens"`
	System       string             `json:"system,omitempty"`
	Messages     []anthropicMessage `json:"messages"`
	OutputConfig *outputConfig      `json:"output_config,omitempty"`
	Thinking     *thinkingOptions   `json:"thinking,omitempty"`
	Tools        []anthropicTool    `json:"tools,omitempty"`
}

type textRequest struct {
	Model        string             `json:"model"`
	MaxTokens    int                `json:"max_tokens"`
	System       string             `json:"system,omitempty"`
	Messages     []anthropicMessage `json:"messages"`
	OutputConfig *outputConfig      `json:"output_config,omitempty"`
	Thinking     *thinkingOptions   `json:"thinking,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type outputConfig struct {
	Effort string        `json:"effort,omitempty"`
	Format *outputFormat `json:"format,omitempty"`
}

type outputFormat struct {
	Type   string         `json:"type"`
	Schema map[string]any `json:"schema,omitempty"`
}

type thinkingOptions struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type response struct {
	Content []contentBlock `json:"content"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	Text  string          `json:"text,omitempty"`
}

const maxResponseBytes = 1 << 20 // 1MB

// extractErrorMessage pulls the message from an API error response body.
// Handles both {"error":{"message":"..."}} and [{...}] array-wrapped forms.
// Falls back to the raw body if parsing fails.
func extractErrorMessage(body []byte) string {
	type errorBody struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	var single errorBody
	if err := json.Unmarshal(body, &single); err == nil && single.Error.Message != "" {
		return single.Error.Message
	}

	var arr []errorBody
	if err := json.Unmarshal(body, &arr); err == nil && len(arr) > 0 && arr[0].Error.Message != "" {
		return arr[0].Error.Message
	}

	return string(body)
}

func (m *Model) Query(ctx context.Context, messages []clnkr.Message) (clnkr.Response, error) {
	var tools []anthropicTool
	schema := requestSchema()
	if m.unattended {
		schema = unattendedRequestSchema()
	}
	mappedMessages := mapTextMessages(messages)
	if m.useBashToolCalls {
		tools = []anthropicTool{bashToolSchema()}
		schema = finalTurnSchema()
		if m.unattended {
			schema = doneOnlySchema()
		}
		mappedMessages = mapToolCallMessages(messages)
	}

	outputCfg := &outputConfig{Format: &outputFormat{Type: "json_schema", Schema: schema}}
	if m.effort != "" {
		outputCfg.Effort = m.effort
	}

	body, err := json.Marshal(request{
		Model:        m.model,
		MaxTokens:    m.maxTokens,
		System:       m.systemPrompt,
		Messages:     mappedMessages,
		OutputConfig: outputCfg,
		Thinking:     m.thinkingOptions(),
		Tools:        tools,
	})
	if err != nil {
		return clnkr.Response{}, fmt.Errorf("marshal request: %w", err)
	}

	respBody, err := m.doRequest(ctx, body)
	if err != nil {
		return clnkr.Response{}, err
	}

	var apiResp response
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return clnkr.Response{}, fmt.Errorf("unmarshal response: %w", err)
	}

	if m.useBashToolCalls {
		return parseToolCallResponse(apiResp, respBody)
	}

	text, textBlocks := extractTextBlocks(apiResp.Content)
	if textBlocks != 1 {
		return clnkr.Response{}, fmt.Errorf("structured output response: expected exactly one text block, got %d", textBlocks)
	}
	if strings.TrimSpace(text) == "" {
		return clnkr.Response{}, fmt.Errorf("structured output response: empty text payload")
	}
	turn, err := parseProviderTurn(text)
	if err != nil {
		return clnkr.Response{
			Raw:         text,
			Usage:       clnkr.Usage{InputTokens: apiResp.Usage.InputTokens, OutputTokens: apiResp.Usage.OutputTokens},
			ProtocolErr: err,
		}, nil
	}
	if _, err := clnkr.CanonicalTurnJSON(turn); err != nil {
		return clnkr.Response{}, fmt.Errorf("structured output response: canonicalize turn payload: %w", err)
	}
	return clnkr.Response{
		Turn:  turn,
		Raw:   text,
		Usage: clnkr.Usage{InputTokens: apiResp.Usage.InputTokens, OutputTokens: apiResp.Usage.OutputTokens},
	}, nil
}

func (m *Model) QueryFinal(ctx context.Context, messages []clnkr.Message) (clnkr.Response, error) {
	outputCfg := &outputConfig{Format: &outputFormat{Type: "json_schema", Schema: doneOnlySchema()}}
	if m.effort != "" {
		outputCfg.Effort = m.effort
	}
	body, err := json.Marshal(request{
		Model:        m.model,
		MaxTokens:    m.maxTokens,
		System:       m.systemPrompt,
		Messages:     mapRawMessages(messages),
		OutputConfig: outputCfg,
		Thinking:     m.thinkingOptions(),
	})
	if err != nil {
		return clnkr.Response{}, fmt.Errorf("marshal request: %w", err)
	}

	respBody, err := m.doRequest(ctx, body)
	if err != nil {
		return clnkr.Response{}, err
	}

	var apiResp response
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return clnkr.Response{}, fmt.Errorf("unmarshal response: %w", err)
	}
	return parseStructuredResponse(apiResp, "structured output response")
}

func parseStructuredResponse(apiResp response, context string) (clnkr.Response, error) {
	text, textBlocks := extractTextBlocks(apiResp.Content)
	if textBlocks != 1 {
		return clnkr.Response{}, fmt.Errorf("%s: expected exactly one text block, got %d", context, textBlocks)
	}
	if strings.TrimSpace(text) == "" {
		return clnkr.Response{}, fmt.Errorf("%s: empty text payload", context)
	}
	turn, err := parseProviderTurn(text)
	if err != nil {
		return clnkr.Response{
			Raw:         text,
			Usage:       clnkr.Usage{InputTokens: apiResp.Usage.InputTokens, OutputTokens: apiResp.Usage.OutputTokens},
			ProtocolErr: err,
		}, nil
	}
	if _, err := clnkr.CanonicalTurnJSON(turn); err != nil {
		return clnkr.Response{}, fmt.Errorf("%s: canonicalize turn payload: %w", context, err)
	}
	return clnkr.Response{
		Turn:  turn,
		Raw:   text,
		Usage: clnkr.Usage{InputTokens: apiResp.Usage.InputTokens, OutputTokens: apiResp.Usage.OutputTokens},
	}, nil
}

func parseToolCallResponse(apiResp response, raw []byte) (clnkr.Response, error) {
	usage := clnkr.Usage{InputTokens: apiResp.Usage.InputTokens, OutputTokens: apiResp.Usage.OutputTokens}
	var toolUses []contentBlock
	var text strings.Builder
	for _, block := range apiResp.Content {
		switch block.Type {
		case "tool_use":
			toolUses = append(toolUses, block)
		case "text":
			text.WriteString(block.Text)
		}
	}
	outputText := text.String()
	if len(toolUses) > 0 {
		actions := make([]clnkr.BashAction, 0, len(toolUses))
		calls := make([]clnkr.BashToolCall, 0, len(toolUses))
		for _, toolUse := range toolUses {
			turn, call, err := turnFromToolUse(toolUse)
			if err != nil {
				return clnkr.Response{Raw: string(raw), Usage: usage, ProtocolErr: err}, nil
			}
			act, ok := turn.(*clnkr.ActTurn)
			if !ok || len(act.Bash.Commands) != 1 {
				return clnkr.Response{}, fmt.Errorf("tool-call response: expected one command from tool use")
			}
			actions = append(actions, act.Bash.Commands[0])
			calls = append(calls, call)
		}
		return clnkr.Response{
			Turn:          &clnkr.ActTurn{Bash: clnkr.BashBatch{Commands: actions}, Reasoning: strings.TrimSpace(outputText)},
			Raw:           string(raw),
			Usage:         usage,
			BashToolCalls: calls,
		}, nil
	}
	if strings.TrimSpace(outputText) == "" {
		return clnkr.Response{}, fmt.Errorf("tool-call response: missing text block or tool_use")
	}
	turn, err := parseProviderTurn(outputText)
	if err != nil {
		return clnkr.Response{Raw: outputText, Usage: usage, ProtocolErr: err}, nil
	}
	if _, ok := turn.(*clnkr.ActTurn); ok {
		return clnkr.Response{Raw: outputText, Usage: usage, ProtocolErr: fmt.Errorf("%w: tool-call mode does not accept text act turns", clnkr.ErrInvalidJSON)}, nil
	}
	if _, err := clnkr.CanonicalTurnJSON(turn); err != nil {
		return clnkr.Response{}, fmt.Errorf("tool-call response: canonicalize turn payload: %w", err)
	}
	return clnkr.Response{Turn: turn, Raw: outputText, Usage: usage}, nil
}

func turnFromToolUse(block contentBlock) (clnkr.Turn, clnkr.BashToolCall, error) {
	if strings.TrimSpace(block.ID) == "" {
		return nil, clnkr.BashToolCall{}, fmt.Errorf("%w: bash tool use missing id", clnkr.ErrInvalidJSON)
	}
	if block.Name != "bash" {
		return nil, clnkr.BashToolCall{}, fmt.Errorf("%w: unsupported tool %q", clnkr.ErrInvalidJSON, block.Name)
	}
	var input struct {
		Command string  `json:"command"`
		Workdir *string `json:"workdir"`
	}
	dec := json.NewDecoder(bytes.NewReader(block.Input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		return nil, clnkr.BashToolCall{}, fmt.Errorf("%w: malformed bash tool input: %v", clnkr.ErrInvalidJSON, err)
	}
	if strings.TrimSpace(input.Command) == "" {
		return nil, clnkr.BashToolCall{}, clnkr.ErrMissingCommand
	}
	call := clnkr.BashToolCall{ID: block.ID, Command: input.Command}
	action := clnkr.BashAction{ID: block.ID, Command: input.Command}
	if input.Workdir != nil {
		call.Workdir = *input.Workdir
		action.Workdir = *input.Workdir
	}
	return &clnkr.ActTurn{Bash: clnkr.BashBatch{Commands: []clnkr.BashAction{action}}}, call, nil
}

func (m *Model) QueryText(ctx context.Context, messages []clnkr.Message) (string, error) {
	var outputCfg *outputConfig
	if m.effort != "" {
		outputCfg = &outputConfig{Effort: m.effort}
	}
	body, err := json.Marshal(textRequest{
		Model:        m.model,
		MaxTokens:    m.maxTokens,
		System:       m.systemPrompt,
		Messages:     mapRawMessages(messages),
		OutputConfig: outputCfg,
		Thinking:     m.thinkingOptions(),
	})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	respBody, err := m.doRequest(ctx, body)
	if err != nil {
		return "", err
	}

	var apiResp response
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	text, textBlocks := extractTextBlocks(apiResp.Content)
	if textBlocks == 0 {
		return "", fmt.Errorf("free-form response: missing text block")
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("free-form response: empty text payload")
	}
	return text, nil
}

func mapTextMessages(messages []clnkr.Message) []anthropicMessage {
	normalized := normalizeMessagesForProvider(messages)
	return mapRawMessages(normalized)
}

func mapRawMessages(messages []clnkr.Message) []anthropicMessage {
	mapped := make([]anthropicMessage, 0, len(messages))
	for _, msg := range messages {
		mapped = append(mapped, anthropicMessage{Role: msg.Role, Content: msg.Content})
	}
	return mapped
}

func mapToolCallMessages(messages []clnkr.Message) []anthropicMessage {
	normalized := normalizeMessagesForProvider(messages)
	mapped := make([]anthropicMessage, 0, len(normalized))
	knownCalls := make(map[string]struct{})
	for i := 0; i < len(normalized); i++ {
		msg := normalized[i]
		if msg.Role == "assistant" && len(msg.BashToolCalls) > 0 {
			blocks := make([]map[string]any, 0, len(msg.BashToolCalls))
			for _, call := range msg.BashToolCalls {
				knownCalls[call.ID] = struct{}{}
				blocks = append(blocks, toolUseBlock(call))
			}
			mapped = append(mapped, anthropicMessage{Role: "assistant", Content: blocks})
			continue
		}
		if msg.BashToolResult != nil {
			if _, ok := knownCalls[msg.BashToolResult.ID]; ok {
				blocks := make([]map[string]any, 0, 1)
				for ; i < len(normalized); i++ {
					next := normalized[i]
					if next.BashToolResult == nil {
						break
					}
					if _, ok := knownCalls[next.BashToolResult.ID]; !ok {
						break
					}
					blocks = append(blocks, toolResultBlock(*next.BashToolResult))
				}
				mapped = append(mapped, anthropicMessage{
					Role:    "user",
					Content: blocks,
				})
				i--
				continue
			}
		}
		mapped = append(mapped, anthropicMessage{Role: msg.Role, Content: msg.Content})
	}
	return mapped
}

func toolResultBlock(result clnkr.BashToolResult) map[string]any {
	block := map[string]any{
		"type":        "tool_result",
		"tool_use_id": result.ID,
		"content":     result.Content,
	}
	if result.IsError {
		block["is_error"] = true
	}
	return block
}

func toolUseBlock(call clnkr.BashToolCall) map[string]any {
	input := map[string]any{"command": call.Command, "workdir": nil}
	if call.Workdir != "" {
		input["workdir"] = call.Workdir
	}
	return map[string]any{
		"type":  "tool_use",
		"id":    call.ID,
		"name":  "bash",
		"input": input,
	}
}

func bashToolSchema() anthropicTool {
	return anthropicTool{
		Name:        "bash",
		Description: "Run one bash command in the current clnkr shell session.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"command": map[string]any{"type": "string"},
				"workdir": map[string]any{
					"anyOf": []any{
						map[string]any{"type": "string"},
						map[string]any{"type": "null"},
					},
				},
			},
			"required": []string{"command", "workdir"},
		},
	}
}

func (m *Model) thinkingOptions() *thinkingOptions {
	if m.thinkingMode == ThinkingModeOmitted && m.thinkingBudgetTokens == 0 {
		return nil
	}
	// Auto-derive mode from legacy ThinkingBudgetTokens field.
	if m.thinkingMode == ThinkingModeOmitted && m.thinkingBudgetTokens > 0 {
		m.thinkingMode = ThinkingModeManual
	}
	anthropicType := m.thinkingMode
	if m.thinkingMode == ThinkingModeManual {
		anthropicType = ThinkingModeEnabled
	}
	to := &thinkingOptions{Type: string(anthropicType)}
	if m.thinkingMode == ThinkingModeManual && m.thinkingBudgetTokens > 0 {
		to.BudgetTokens = m.thinkingBudgetTokens
	}
	return to
}

func (m *Model) doRequest(ctx context.Context, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", endpointURL(m.baseURL, "/v1/messages"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", m.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api error (status %d): %s", resp.StatusCode, extractErrorMessage(respBody))
	}
	return respBody, nil
}

func endpointURL(baseURL, endpoint string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
		return joinURLBoundary(baseURL, endpoint)
	}

	escapedPath := strings.TrimRight(parsed.EscapedPath(), "/") + "/" + strings.TrimLeft(endpoint, "/")
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return joinURLBoundary(baseURL, endpoint)
	}
	parsed.Path = decodedPath
	parsed.RawPath = ""
	if escapedPath != (&url.URL{Path: decodedPath}).EscapedPath() {
		parsed.RawPath = escapedPath
	}
	return parsed.String()
}

func joinURLBoundary(baseURL, endpoint string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(endpoint, "/")
}

func extractTextBlocks(blocks []contentBlock) (string, int) {
	var text string
	textBlocks := 0
	for _, block := range blocks {
		if block.Type == "text" {
			textBlocks++
			text += block.Text
		}
	}
	return text, textBlocks
}
