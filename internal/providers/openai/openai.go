package openai

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

// Model talks to any OpenAI-compatible chat completions API.
type Model struct {
	baseURL      string
	apiKey       string
	model        string
	systemPrompt string
	unattended   bool
	client       *http.Client
}

// Options configures OpenAI-compatible request behavior.
type Options struct {
	Unattended bool
}

// NewModel sets up an OpenAI-compatible adapter.
func NewModel(baseURL, apiKey, model, systemPrompt string) *Model {
	return NewModelWithOptions(baseURL, apiKey, model, systemPrompt, Options{})
}

// NewModelWithOptions sets up an OpenAI-compatible adapter with request options.
func NewModelWithOptions(baseURL, apiKey, model, systemPrompt string, opts Options) *Model {
	return &Model{
		baseURL:      baseURL,
		apiKey:       apiKey,
		model:        model,
		systemPrompt: systemPrompt,
		unattended:   opts.Unattended,
		client:       &http.Client{Timeout: 240 * time.Second},
	}
}

type request struct {
	Model          string          `json:"model"`
	Messages       []clnkr.Message `json:"messages"`
	ResponseFormat responseFormat  `json:"response_format"`
}

type textRequest struct {
	Model    string          `json:"model"`
	Messages []clnkr.Message `json:"messages"`
}

type responseFormat struct {
	Type       string             `json:"type"`
	JSONSchema responseJSONSchema `json:"json_schema"`
}

type responseJSONSchema struct {
	Name   string         `json:"name,omitempty"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type response struct {
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
			Refusal string `json:"refusal"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

const (
	maxResponseBytes = 1 << 20 // 1MB
	maxAttempts      = 5
	baseRetryDelay   = time.Second
)

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
	messages = openaiwire.NormalizeMessagesForProvider(messages)
	schema := openaiwire.RequestSchema()
	if m.unattended {
		schema = openaiwire.UnattendedRequestSchema()
	}
	allMessages := make([]clnkr.Message, 0, len(messages)+1)
	allMessages = append(allMessages, clnkr.Message{Role: "system", Content: m.systemPrompt})
	allMessages = append(allMessages, messages...)

	body, err := json.Marshal(request{
		Model:    m.model,
		Messages: allMessages,
		ResponseFormat: responseFormat{
			Type: "json_schema",
			JSONSchema: responseJSONSchema{
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
			return parseResponse(respBody)
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
	allMessages := make([]clnkr.Message, 0, len(messages)+1)
	allMessages = append(allMessages, clnkr.Message{Role: "system", Content: m.systemPrompt})
	allMessages = append(allMessages, messages...)

	body, err := json.Marshal(textRequest{
		Model:    m.model,
		Messages: allMessages,
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

func (m *Model) doRequest(ctx context.Context, body []byte) ([]byte, int, string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", endpointURL(m.baseURL, "/chat/completions"), bytes.NewReader(body))
	if err != nil {
		return nil, 0, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.apiKey)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, 0, "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup

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

func parseResponse(respBody []byte) (clnkr.Response, error) {
	var apiResp response
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return clnkr.Response{}, fmt.Errorf("unmarshal response: %w", err)
	}
	if len(apiResp.Choices) == 0 {
		return clnkr.Response{}, fmt.Errorf("no choices in response")
	}

	choice := apiResp.Choices[0]
	if strings.TrimSpace(choice.Message.Refusal) != "" {
		return clnkr.Response{}, fmt.Errorf("structured output refusal: %s", choice.Message.Refusal)
	}
	if strings.TrimSpace(choice.Message.Content) == "" {
		return clnkr.Response{}, fmt.Errorf("empty choice content")
	}
	turn, err := openaiwire.ParseProviderTurn(choice.Message.Content)
	if err != nil {
		return clnkr.Response{
			Raw:         choice.Message.Content,
			Usage:       clnkr.Usage{InputTokens: apiResp.Usage.PromptTokens, OutputTokens: apiResp.Usage.CompletionTokens},
			ProtocolErr: err,
		}, nil
	}
	if _, err := clnkr.CanonicalTurnJSON(turn); err != nil {
		return clnkr.Response{}, fmt.Errorf("canonicalize structured output payload: %w", err)
	}
	return clnkr.Response{
		Turn:  turn,
		Raw:   choice.Message.Content,
		Usage: clnkr.Usage{InputTokens: apiResp.Usage.PromptTokens, OutputTokens: apiResp.Usage.CompletionTokens},
	}, nil
}

func parseTextResponse(respBody []byte) (string, error) {
	var apiResp response
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if len(apiResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	choice := apiResp.Choices[0]
	if strings.TrimSpace(choice.Message.Refusal) != "" {
		return "", fmt.Errorf("free-form refusal: %s", choice.Message.Refusal)
	}
	if strings.TrimSpace(choice.Message.Content) == "" {
		return "", fmt.Errorf("empty choice content")
	}
	return choice.Message.Content, nil
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
