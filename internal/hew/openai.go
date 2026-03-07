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

// OpenAIModel implements Model for OpenAI-compatible chat completions APIs.
type OpenAIModel struct {
	baseURL      string
	apiKey       string
	model        string
	systemPrompt string
	client       *http.Client
}

// NewOpenAIModel creates an OpenAI-compatible adapter.
func NewOpenAIModel(baseURL, apiKey, model, systemPrompt string) *OpenAIModel {
	return &OpenAIModel{
		baseURL:      baseURL,
		apiKey:       apiKey,
		model:        model,
		systemPrompt: systemPrompt,
		client:       &http.Client{Timeout: 120 * time.Second},
	}
}

type openaiRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

type openaiResponse struct {
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

func (m *OpenAIModel) Query(ctx context.Context, messages []Message) (Response, error) {
	allMessages := make([]Message, 0, len(messages)+1)
	allMessages = append(allMessages, Message{Role: "system", Content: m.systemPrompt})
	allMessages = append(allMessages, messages...)

	body, err := json.Marshal(openaiRequest{Model: m.model, Messages: allMessages})
	if err != nil {
		return Response{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", m.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.apiKey)

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

	var apiResp openaiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return Response{}, fmt.Errorf("unmarshal response: %w", err)
	}
	if len(apiResp.Choices) == 0 {
		return Response{}, fmt.Errorf("no choices in response")
	}

	choice := apiResp.Choices[0]
	return Response{
		Message: Message{Role: choice.Message.Role, Content: choice.Message.Content},
		Usage:   Usage{InputTokens: apiResp.Usage.PromptTokens, OutputTokens: apiResp.Usage.CompletionTokens},
	}, nil
}
