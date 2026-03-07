package hew

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AnthropicModel implements Model for the Anthropic Messages API.
type AnthropicModel struct {
	baseURL      string
	apiKey       string
	model        string
	systemPrompt string
	client       *http.Client
}

// NewAnthropicModel creates an Anthropic adapter.
func NewAnthropicModel(baseURL, apiKey, model, systemPrompt string) *AnthropicModel {
	return &AnthropicModel{
		baseURL:      baseURL,
		apiKey:       apiKey,
		model:        model,
		systemPrompt: systemPrompt,
		client:       &http.Client{Timeout: 120 * time.Second},
	}
}

type anthropicRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []Message `json:"messages"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

const maxResponseBytes = 1 << 20 // 1MB

func (m *AnthropicModel) Query(ctx context.Context, messages []Message) (Response, error) {
	body, err := json.Marshal(anthropicRequest{
		Model:     m.model,
		MaxTokens: 4096,
		System:    m.systemPrompt,
		Messages:  messages,
	})
	if err != nil {
		return Response{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", m.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", m.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := m.client.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return Response{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("api error (status %d): %s", resp.StatusCode, respBody)
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return Response{}, fmt.Errorf("unmarshal response: %w", err)
	}

	var text string
	for _, block := range apiResp.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return Response{
		Message: Message{Role: "assistant", Content: text},
		Usage:   Usage{InputTokens: apiResp.Usage.InputTokens, OutputTokens: apiResp.Usage.OutputTokens},
	}, nil
}
