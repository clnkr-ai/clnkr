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
	client               *http.Client
}

const DefaultMaxTokens = 4096

type Options struct {
	MaxTokens            int
	ThinkingBudgetTokens int
	ThinkingMode         ThinkingMode
	Effort               string
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
		client:               &http.Client{Timeout: 240 * time.Second},
	}
}

type request struct {
	Model        string           `json:"model"`
	MaxTokens    int              `json:"max_tokens"`
	System       string           `json:"system,omitempty"`
	Messages     []clnkr.Message  `json:"messages"`
	OutputConfig *outputConfig    `json:"output_config,omitempty"`
	Thinking     *thinkingOptions `json:"thinking,omitempty"`
}

type textRequest struct {
	Model        string           `json:"model"`
	MaxTokens    int              `json:"max_tokens"`
	System       string           `json:"system,omitempty"`
	Messages     []clnkr.Message  `json:"messages"`
	OutputConfig *outputConfig    `json:"output_config,omitempty"`
	Thinking     *thinkingOptions `json:"thinking,omitempty"`
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

type response struct {
	Content []contentBlock `json:"content"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
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
	messages = normalizeMessagesForProvider(messages)

	outputCfg := &outputConfig{Format: &outputFormat{Type: "json_schema", Schema: requestSchema()}}
	if m.effort != "" {
		outputCfg.Effort = m.effort
	}

	body, err := json.Marshal(request{
		Model:        m.model,
		MaxTokens:    m.maxTokens,
		System:       m.systemPrompt,
		Messages:     messages,
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

func (m *Model) QueryText(ctx context.Context, messages []clnkr.Message) (string, error) {
	var outputCfg *outputConfig
	if m.effort != "" {
		outputCfg = &outputConfig{Effort: m.effort}
	}
	body, err := json.Marshal(textRequest{
		Model:        m.model,
		MaxTokens:    m.maxTokens,
		System:       m.systemPrompt,
		Messages:     messages,
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
