package openairesponses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/providers/openaiwire"
)

// Model talks to the OpenAI Responses API.
type Model struct {
	baseURL      string
	apiKey       string
	model        string
	systemPrompt string
	client       *http.Client
}

// NewModel sets up an OpenAI Responses adapter.
func NewModel(baseURL, apiKey, model, systemPrompt string) *Model {
	return &Model{
		baseURL:      baseURL,
		apiKey:       apiKey,
		model:        model,
		systemPrompt: systemPrompt,
		client:       &http.Client{Timeout: 240 * time.Second},
	}
}

type request struct {
	Model        string         `json:"model"`
	Instructions string         `json:"instructions,omitempty"`
	Input        []inputMessage `json:"input"`
	Text         *textOptions   `json:"text,omitempty"`
}

type textOptions struct {
	Format *responseFormat `json:"format,omitempty"`
}

type responseFormat struct {
	Type   string         `json:"type"`
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type inputMessage struct {
	Role    string          `json:"role"`
	Content []inputTextItem `json:"content"`
}

type inputTextItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type response struct {
	Output []outputItem `json:"output"`
	Usage  struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type outputItem struct {
	Type    string         `json:"type"`
	Role    string         `json:"role"`
	Content []responseItem `json:"content"`
}

type responseItem struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

const (
	maxResponseBytes = 1 << 20 // 1MB
	maxAttempts      = 5
	baseRetryDelay   = time.Second
)

func (m *Model) Query(ctx context.Context, messages []clnkr.Message) (clnkr.Response, error) {
	body, err := json.Marshal(request{
		Model:        m.model,
		Instructions: m.systemPrompt,
		Input:        mapMessages(messages),
		Text: &textOptions{
			Format: &responseFormat{
				Type:   "json_schema",
				Name:   "agent_turn",
				Strict: true,
				Schema: openaiwire.RequestSchema(),
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
			return parseStructuredResponse(respBody)
		}

		apiErr := fmt.Errorf("api error (status %d): %s", statusCode, extractErrorMessage(respBody))
		if statusCode != http.StatusTooManyRequests || attempt == maxAttempts {
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
		Model:        m.model,
		Instructions: m.systemPrompt,
		Input:        mapMessages(messages),
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
		if statusCode != http.StatusTooManyRequests || attempt == maxAttempts {
			return "", apiErr
		}
		if err := waitForRetry(ctx, retryDelay(retryAfter, attempt)); err != nil {
			return "", fmt.Errorf("wait before retry: %w", err)
		}
	}

	return "", fmt.Errorf("retry loop exhausted")
}

func mapMessages(messages []clnkr.Message) []inputMessage {
	normalized := openaiwire.NormalizeMessagesForProvider(messages)
	input := make([]inputMessage, 0, len(normalized))
	for _, msg := range normalized {
		input = append(input, inputMessage{
			Role: msg.Role,
			Content: []inputTextItem{{
				Type: "input_text",
				Text: msg.Content,
			}},
		})
	}
	return input
}

func (m *Model) doRequest(ctx context.Context, body []byte) ([]byte, int, string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", m.baseURL+"/responses", bytes.NewReader(body))
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
