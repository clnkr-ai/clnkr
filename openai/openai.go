package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cosgroveb/hew"
)

// Model implements hew.Model for OpenAI-compatible chat completions APIs.
type Model struct {
	baseURL      string
	apiKey       string
	model        string
	systemPrompt string
	client       *http.Client
}

// NewModel creates an OpenAI-compatible adapter.
func NewModel(baseURL, apiKey, model, systemPrompt string) *Model {
	return &Model{
		baseURL:      baseURL,
		apiKey:       apiKey,
		model:        model,
		systemPrompt: systemPrompt,
		client:       &http.Client{Timeout: 120 * time.Second},
	}
}

type request struct {
	Model    string        `json:"model"`
	Messages []hew.Message `json:"messages"`
}

type response struct {
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

const maxResponseBytes = 1 << 20 // 1MB

func (m *Model) Query(ctx context.Context, messages []hew.Message) (hew.Response, error) {
	allMessages := make([]hew.Message, 0, len(messages)+1)
	allMessages = append(allMessages, hew.Message{Role: "system", Content: m.systemPrompt})
	allMessages = append(allMessages, messages...)

	body, err := json.Marshal(request{Model: m.model, Messages: allMessages})
	if err != nil {
		return hew.Response{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", m.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return hew.Response{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.apiKey)

	resp, err := m.client.Do(req)
	if err != nil {
		return hew.Response{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return hew.Response{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return hew.Response{}, fmt.Errorf("api error (status %d): %s", resp.StatusCode, respBody)
	}

	var apiResp response
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return hew.Response{}, fmt.Errorf("unmarshal response: %w", err)
	}
	if len(apiResp.Choices) == 0 {
		return hew.Response{}, fmt.Errorf("no choices in response")
	}

	choice := apiResp.Choices[0]
	return hew.Response{
		Message: hew.Message{Role: choice.Message.Role, Content: choice.Message.Content},
		Usage:   hew.Usage{InputTokens: apiResp.Usage.PromptTokens, OutputTokens: apiResp.Usage.CompletionTokens},
	}, nil
}
