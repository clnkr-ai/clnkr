package openairesponses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/providers/openaiwire"
)

// Model talks to the OpenAI Responses API.
type Model struct {
	baseURL            string
	apiKey             string
	model              string
	systemPrompt       string
	reasoningEffort    string
	maxOutputTokens    int
	hasMaxOutputTokens bool
	useBashToolCalls   bool
	unattended         bool
	client             *http.Client
}

type Options struct {
	ReasoningEffort    string
	MaxOutputTokens    int
	HasMaxOutputTokens bool
	UseBashToolCalls   bool
	Unattended         bool
}

// NewModel sets up an OpenAI Responses adapter.
func NewModel(baseURL, apiKey, model, systemPrompt string) *Model {
	return NewModelWithOptions(baseURL, apiKey, model, systemPrompt, Options{})
}

// NewModelWithOptions sets up an OpenAI Responses adapter with request options.
func NewModelWithOptions(baseURL, apiKey, model, systemPrompt string, opts Options) *Model {
	return &Model{
		baseURL:            baseURL,
		apiKey:             apiKey,
		model:              model,
		systemPrompt:       systemPrompt,
		reasoningEffort:    opts.ReasoningEffort,
		maxOutputTokens:    opts.MaxOutputTokens,
		hasMaxOutputTokens: opts.HasMaxOutputTokens,
		useBashToolCalls:   opts.UseBashToolCalls,
		unattended:         opts.Unattended,
		client:             &http.Client{Timeout: 240 * time.Second},
	}
}

type request struct {
	Model           string               `json:"model"`
	Instructions    string               `json:"instructions,omitempty"`
	Input           []responsesInputItem `json:"input"`
	Include         []string             `json:"include,omitempty"`
	Text            *textOptions         `json:"text,omitempty"`
	Reasoning       *reasoningOptions    `json:"reasoning,omitempty"`
	MaxOutputTokens *int                 `json:"max_output_tokens,omitempty"`
	Tools           []openAITool         `json:"tools,omitempty"`
	ParallelTools   *bool                `json:"parallel_tool_calls,omitempty"`
}

type textOptions struct {
	Format *responseFormat `json:"format,omitempty"`
}

type reasoningOptions struct {
	Effort string `json:"effort"`
}

type responseFormat struct {
	Type   string         `json:"type"`
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type openAITool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Strict      bool           `json:"strict"`
	Parameters  map[string]any `json:"parameters"`
}

type responsesInputItem struct {
	Raw       json.RawMessage        `json:"-"`
	Type      string                 `json:"type,omitempty"`
	ID        string                 `json:"id,omitempty"`
	CallID    string                 `json:"call_id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Arguments string                 `json:"arguments,omitempty"`
	Output    string                 `json:"output,omitempty"`
	Role      string                 `json:"role,omitempty"`
	Status    string                 `json:"status,omitempty"`
	Content   []responsesContentItem `json:"content,omitempty"`
}

func (i responsesInputItem) MarshalJSON() ([]byte, error) {
	if len(i.Raw) > 0 {
		return i.Raw, nil
	}
	type alias responsesInputItem
	return json.Marshal(alias(i))
}

type responsesContentItem struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Annotations *[]any `json:"annotations,omitempty"`
}

type response struct {
	Output    []outputItem      `json:"output"`
	RawOutput []json.RawMessage `json:"-"`
	Usage     struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type outputItem struct {
	Type      string         `json:"type"`
	ID        string         `json:"id"`
	CallID    string         `json:"call_id"`
	Name      string         `json:"name"`
	Arguments string         `json:"arguments"`
	Status    string         `json:"status"`
	Role      string         `json:"role"`
	Content   []responseItem `json:"content"`
}

type responseItem struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

const (
	maxResponseBytes  = 1 << 20 // 1MB
	maxAttempts       = 5
	baseRetryDelay    = time.Second
	replayProvider    = "openai"
	replayProviderAPI = "openai-responses"
	includeReasoning  = "reasoning.encrypted_content"
)

func (m *Model) Query(ctx context.Context, messages []clnkr.Message) (clnkr.Response, error) {
	schema := openaiwire.RequestSchema()
	if m.unattended {
		schema = openaiwire.UnattendedRequestSchema()
	}
	input := mapMessages(messages)
	var tools []openAITool
	var parallelTools *bool
	if m.useBashToolCalls {
		schema = openaiwire.FinalTurnSchema()
		if m.unattended {
			schema = openaiwire.DoneOnlySchema()
		}
		input = mapToolCallMessages(messages)
		tools = []openAITool{bashToolSchema()}
	}
	return m.queryStructured(ctx, input, schema, tools, parallelTools)
}

func (m *Model) QueryFinal(ctx context.Context, messages []clnkr.Message) (clnkr.Response, error) {
	return m.queryStructured(ctx, mapMessages(messages), openaiwire.DoneOnlySchema(), nil, nil)
}

func (m *Model) queryStructured(ctx context.Context, input []responsesInputItem, schema map[string]any, tools []openAITool, parallelTools *bool) (clnkr.Response, error) {
	body, err := json.Marshal(request{
		Model:           m.model,
		Instructions:    m.systemPrompt,
		Input:           input,
		Include:         includeOptions(tools),
		Reasoning:       m.reasoningOptions(),
		MaxOutputTokens: m.maxOutputTokensValue(),
		Tools:           tools,
		ParallelTools:   parallelTools,
		Text: &textOptions{
			Format: &responseFormat{
				Type:   "json_schema",
				Name:   "agent_turn",
				Strict: true,
				Schema: schema,
			},
		},
	})
	if err != nil {
		return clnkr.Response{}, fmt.Errorf("marshal request: %w", err)
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		respBody, statusCode, retryAfter, err := m.doRequest(ctx, body)
		if err != nil {
			return clnkr.Response{}, err
		}
		if statusCode == http.StatusOK {
			if m.useBashToolCalls && len(tools) > 0 {
				return parseToolCallResponse(respBody)
			}
			return parseStructuredResponse(respBody)
		}

		apiErr := fmt.Errorf("api error (status %d): %s", statusCode, extractErrorMessage(respBody))
		if !retryableStatus(statusCode) || attempt == maxAttempts {
			return clnkr.Response{}, apiErr
		}
		if err := waitForRetry(ctx, retryDelay(retryAfter, attempt)); err != nil {
			return clnkr.Response{}, fmt.Errorf("wait before retry: %w", err)
		}
	}

	return clnkr.Response{}, fmt.Errorf("retry loop exhausted")
}

func (m *Model) QueryText(ctx context.Context, messages []clnkr.Message) (string, error) {
	body, err := json.Marshal(request{
		Model:           m.model,
		Instructions:    m.systemPrompt,
		Input:           mapMessages(messages),
		Reasoning:       m.reasoningOptions(),
		MaxOutputTokens: m.maxOutputTokensValue(),
	})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		respBody, statusCode, retryAfter, err := m.doRequest(ctx, body)
		if err != nil {
			return "", err
		}
		if statusCode == http.StatusOK {
			return parseTextResponse(respBody)
		}

		apiErr := fmt.Errorf("api error (status %d): %s", statusCode, extractErrorMessage(respBody))
		if !retryableStatus(statusCode) || attempt == maxAttempts {
			return "", apiErr
		}
		if err := waitForRetry(ctx, retryDelay(retryAfter, attempt)); err != nil {
			return "", fmt.Errorf("wait before retry: %w", err)
		}
	}

	return "", fmt.Errorf("retry loop exhausted")
}

func (m *Model) reasoningOptions() *reasoningOptions {
	if m.reasoningEffort == "" {
		return nil
	}
	return &reasoningOptions{Effort: m.reasoningEffort}
}

func (m *Model) maxOutputTokensValue() *int {
	if !m.hasMaxOutputTokens {
		return nil
	}
	return &m.maxOutputTokens
}

func mapMessages(messages []clnkr.Message) []responsesInputItem {
	normalized := openaiwire.NormalizeMessagesForProvider(messages)
	input := make([]responsesInputItem, 0, len(normalized))
	assistantIndex := 0
	for _, msg := range normalized {
		if msg.Role == "assistant" {
			assistantIndex++
			annotations := []any{}
			input = append(input, responsesInputItem{
				Type:   "message",
				ID:     fmt.Sprintf("msg_prev_%d", assistantIndex),
				Role:   "assistant",
				Status: "completed",
				Content: []responsesContentItem{{
					Type:        "output_text",
					Text:        msg.Content,
					Annotations: &annotations,
				}},
			})
			continue
		}

		input = append(input, responsesInputItem{
			Type: "message",
			Role: msg.Role,
			Content: []responsesContentItem{{
				Type: "input_text",
				Text: msg.Content,
			}},
		})
	}
	return input
}

func mapToolCallMessages(messages []clnkr.Message) []responsesInputItem {
	normalized := openaiwire.NormalizeMessagesForProvider(messages)
	input := make([]responsesInputItem, 0, len(normalized))
	knownCalls := make(map[string]struct{})
	assistantIndex := 0
	for _, msg := range normalized {
		if msg.Role == "assistant" && len(msg.BashToolCalls) > 0 {
			for _, item := range msg.ProviderReplay {
				input = appendProviderReplayInput(input, item)
			}
			for _, call := range msg.BashToolCalls {
				knownCalls[call.ID] = struct{}{}
				input = append(input, functionCallInput(call))
			}
			continue
		}
		if msg.BashToolResult != nil {
			if _, ok := knownCalls[msg.BashToolResult.ID]; ok {
				input = append(input, responsesInputItem{
					Type:   "function_call_output",
					CallID: msg.BashToolResult.ID,
					Output: msg.BashToolResult.Content,
				})
				continue
			}
		}
		if msg.Role == "assistant" {
			assistantIndex++
			annotations := []any{}
			input = append(input, responsesInputItem{
				Type:   "message",
				ID:     fmt.Sprintf("msg_prev_%d", assistantIndex),
				Role:   "assistant",
				Status: "completed",
				Content: []responsesContentItem{{
					Type:        "output_text",
					Text:        msg.Content,
					Annotations: &annotations,
				}},
			})
			continue
		}
		input = append(input, responsesInputItem{
			Type: "message",
			Role: msg.Role,
			Content: []responsesContentItem{{
				Type: "input_text",
				Text: msg.Content,
			}},
		})
	}
	return input
}

func includeOptions(tools []openAITool) []string {
	if len(tools) == 0 {
		return nil
	}
	return []string{includeReasoning}
}

func appendProviderReplayInput(input []responsesInputItem, item clnkr.ProviderReplayItem) []responsesInputItem {
	if item.Provider != replayProvider || item.ProviderAPI != replayProviderAPI || len(item.JSON) == 0 {
		return input
	}
	if item.Type == "reasoning" && !hasEncryptedReasoning(item.JSON) {
		return input
	}
	return append(input, responsesInputItem{Raw: append(json.RawMessage(nil), item.JSON...)})
}

func hasEncryptedReasoning(raw json.RawMessage) bool {
	var item struct {
		EncryptedContent string `json:"encrypted_content"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return false
	}
	return strings.TrimSpace(item.EncryptedContent) != ""
}

func functionCallInput(call clnkr.BashToolCall) responsesInputItem {
	args := struct {
		Command string  `json:"command"`
		Workdir *string `json:"workdir"`
	}{Command: call.Command}
	if call.Workdir != "" {
		args.Workdir = &call.Workdir
	}
	body, err := json.Marshal(args)
	if err != nil {
		body = []byte(`{"command":"","workdir":null}`)
	}
	return responsesInputItem{
		Type:      "function_call",
		CallID:    call.ID,
		Name:      "bash",
		Arguments: string(body),
		Status:    "completed",
	}
}

func bashToolSchema() openAITool {
	return openAITool{
		Type:        "function",
		Name:        "bash",
		Description: "Run one bash command in the current clnkr shell session.",
		Strict:      true,
		Parameters: map[string]any{
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

func (m *Model) doRequest(ctx context.Context, body []byte) ([]byte, int, string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", endpointURL(m.baseURL, "/responses"), bytes.NewReader(body))
	if err != nil {
		return nil, 0, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.apiKey)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, 0, "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, 0, "", fmt.Errorf("read response: %w", err)
	}
	return respBody, resp.StatusCode, resp.Header.Get("Retry-After"), nil
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

func parseStructuredResponse(respBody []byte) (clnkr.Response, error) {
	text, usage, err := extractOutputText(respBody, "structured output")
	if err != nil {
		return clnkr.Response{}, err
	}

	turn, err := openaiwire.ParseProviderTurn(text)
	if err != nil {
		return clnkr.Response{
			Raw:         text,
			Usage:       usage,
			ProtocolErr: err,
		}, nil
	}
	if _, err := clnkr.CanonicalTurnJSON(turn); err != nil {
		return clnkr.Response{}, fmt.Errorf("canonicalize structured output payload: %w", err)
	}
	return clnkr.Response{
		Turn:  turn,
		Raw:   text,
		Usage: usage,
	}, nil
}

func parseToolCallResponse(respBody []byte) (clnkr.Response, error) {
	apiResp, err := unmarshalResponse(respBody)
	if err != nil {
		return clnkr.Response{}, err
	}
	usage := clnkr.Usage{InputTokens: apiResp.Usage.InputTokens, OutputTokens: apiResp.Usage.OutputTokens}

	var calls []outputItem
	var text strings.Builder
	replay := make([]clnkr.ProviderReplayItem, 0)
	for i, item := range apiResp.Output {
		switch item.Type {
		case "function_call":
			calls = append(calls, item)
		case "message":
			if item.Role != "assistant" {
				continue
			}
			for _, content := range item.Content {
				switch content.Type {
				case "output_text":
					text.WriteString(content.Text)
				case "refusal":
					if strings.TrimSpace(content.Refusal) != "" {
						return clnkr.Response{}, fmt.Errorf("tool-call refusal: %s", content.Refusal)
					}
				}
			}
		default:
			if i < len(apiResp.RawOutput) {
				replay = append(replay, clnkr.ProviderReplayItem{
					Provider:    replayProvider,
					ProviderAPI: replayProviderAPI,
					Type:        item.Type,
					JSON:        append(json.RawMessage(nil), apiResp.RawOutput[i]...),
				})
			}
		}
	}

	outputText := text.String()
	if len(calls) > 0 && strings.TrimSpace(outputText) != "" {
		return clnkr.Response{Raw: string(respBody), Usage: usage, ProtocolErr: fmt.Errorf("%w: mixed bash tool call and structured text", clnkr.ErrInvalidJSON)}, nil
	}
	if len(calls) > 0 {
		actions := make([]clnkr.BashAction, 0, len(calls))
		toolCalls := make([]clnkr.BashToolCall, 0, len(calls))
		for _, item := range calls {
			turn, call, err := turnFromFunctionCall(item)
			if err != nil {
				return clnkr.Response{Raw: string(respBody), Usage: usage, ProtocolErr: err}, nil
			}
			act, ok := turn.(*clnkr.ActTurn)
			if !ok || len(act.Bash.Commands) != 1 {
				return clnkr.Response{}, fmt.Errorf("tool-call response: expected one command from function call")
			}
			actions = append(actions, act.Bash.Commands[0])
			toolCalls = append(toolCalls, call)
		}
		return clnkr.Response{
			Turn:           &clnkr.ActTurn{Bash: clnkr.BashBatch{Commands: actions}},
			Raw:            string(respBody),
			Usage:          usage,
			BashToolCalls:  toolCalls,
			ProviderReplay: replay,
		}, nil
	}
	if strings.TrimSpace(outputText) == "" {
		return clnkr.Response{}, fmt.Errorf("tool-call response: no usable output_text or bash tool call")
	}
	turn, err := openaiwire.ParseProviderTurn(outputText)
	if err != nil {
		return clnkr.Response{Raw: outputText, Usage: usage, ProtocolErr: err}, nil
	}
	if _, ok := turn.(*clnkr.ActTurn); ok {
		return clnkr.Response{Raw: outputText, Usage: usage, ProtocolErr: fmt.Errorf("%w: tool-call mode does not accept text act turns", clnkr.ErrInvalidJSON)}, nil
	}
	if _, err := clnkr.CanonicalTurnJSON(turn); err != nil {
		return clnkr.Response{}, fmt.Errorf("canonicalize tool-call payload: %w", err)
	}
	return clnkr.Response{Turn: turn, Raw: outputText, Usage: usage}, nil
}

func turnFromFunctionCall(item outputItem) (clnkr.Turn, clnkr.BashToolCall, error) {
	if strings.TrimSpace(item.CallID) == "" {
		return nil, clnkr.BashToolCall{}, fmt.Errorf("%w: bash tool call missing call_id", clnkr.ErrInvalidJSON)
	}
	if item.Name != "bash" {
		return nil, clnkr.BashToolCall{}, fmt.Errorf("%w: unsupported tool %q", clnkr.ErrInvalidJSON, item.Name)
	}
	var args struct {
		Command string  `json:"command"`
		Workdir *string `json:"workdir"`
	}
	dec := json.NewDecoder(strings.NewReader(item.Arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, clnkr.BashToolCall{}, fmt.Errorf("%w: malformed bash tool arguments: %v", clnkr.ErrInvalidJSON, err)
	}
	if strings.TrimSpace(args.Command) == "" {
		return nil, clnkr.BashToolCall{}, clnkr.ErrMissingCommand
	}
	call := clnkr.BashToolCall{ID: item.CallID, Command: args.Command}
	action := clnkr.BashAction{ID: item.CallID, Command: args.Command}
	if args.Workdir != nil {
		call.Workdir = *args.Workdir
		action.Workdir = *args.Workdir
	}
	return &clnkr.ActTurn{Bash: clnkr.BashBatch{Commands: []clnkr.BashAction{action}}}, call, nil
}

func unmarshalResponse(respBody []byte) (response, error) {
	var raw struct {
		Output []json.RawMessage `json:"output"`
		Usage  struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return response{}, fmt.Errorf("unmarshal response: %w", err)
	}
	apiResp := response{RawOutput: raw.Output}
	apiResp.Usage = raw.Usage
	apiResp.Output = make([]outputItem, len(raw.Output))
	for i, item := range raw.Output {
		if err := json.Unmarshal(item, &apiResp.Output[i]); err != nil {
			return response{}, fmt.Errorf("unmarshal response output item %d: %w", i, err)
		}
	}
	return apiResp, nil
}

func parseTextResponse(respBody []byte) (string, error) {
	text, _, err := extractOutputText(respBody, "free-form")
	if err != nil {
		return "", err
	}
	return text, nil
}

func extractOutputText(respBody []byte, mode string) (string, clnkr.Usage, error) {
	var apiResp response
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", clnkr.Usage{}, fmt.Errorf("unmarshal response: %w", err)
	}

	var b strings.Builder
	for _, item := range apiResp.Output {
		if item.Type != "message" || item.Role != "assistant" {
			continue
		}
		for _, content := range item.Content {
			switch content.Type {
			case "output_text":
				b.WriteString(content.Text)
			case "refusal":
				if strings.TrimSpace(content.Refusal) != "" {
					return "", clnkr.Usage{}, fmt.Errorf("%s refusal: %s", mode, content.Refusal)
				}
			}
		}
	}

	text := b.String()
	if strings.TrimSpace(text) == "" {
		return "", clnkr.Usage{}, fmt.Errorf("no usable output_text in response")
	}
	return text, clnkr.Usage{
		InputTokens:  apiResp.Usage.InputTokens,
		OutputTokens: apiResp.Usage.OutputTokens,
	}, nil
}

// extractErrorMessage pulls the message from an API error response body.
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

func retryableStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return statusCode >= 500 && statusCode < 600
	}
}

func retryDelay(retryAfter string, attempt int) time.Duration {
	if delay, ok := parseRetryAfter(retryAfter); ok {
		return delay
	}

	delay := baseRetryDelay
	if attempt > 1 {
		delay <<= (attempt - 1)
	}
	maxJitter := delay / 4
	if maxJitter == 0 {
		return delay
	}
	jitter := time.Duration(rand.Int64N(int64(maxJitter)*2+1)) - maxJitter
	return delay + jitter
}

func parseRetryAfter(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0, true
		}
		return time.Duration(seconds) * time.Second, true
	}
	if t, err := http.ParseTime(value); err == nil {
		if !t.After(time.Now()) {
			return 0, true
		}
		return time.Until(t), true
	}
	return 0, false
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
