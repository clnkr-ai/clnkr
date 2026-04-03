package evaluations

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/clnkr-ai/clnkr"
)

type chatCompletionRequest struct {
	Model    string          `json:"model"`
	Messages []clnkr.Message `json:"messages"`
}

// CapturedRequest is one mock provider request/response pair.
type CapturedRequest struct {
	Model       string
	Messages    []clnkr.Message
	RawRequest  string
	RawResponse string
}

// MockProvider serves deterministic OpenAI-compatible chat responses.
type MockProvider struct {
	server *httptest.Server
	turns  []string

	mu       sync.Mutex
	nextTurn int
	requests []CapturedRequest
}

// NewMockProvider creates a local provider that returns the supplied turns in order.
func NewMockProvider(turns []string) *MockProvider {
	provider := &MockProvider{
		turns: append([]string(nil), turns...),
	}
	provider.server = httptest.NewServer(http.HandlerFunc(provider.handleChatCompletions))
	return provider
}

// URL returns the provider base URL.
func (p *MockProvider) URL() string {
	return p.server.URL
}

// Close stops the mock provider server.
func (p *MockProvider) Close() {
	if p.server != nil {
		p.server.Close()
	}
}

// Requests returns a deep copy of captured requests.
func (p *MockProvider) Requests() []CapturedRequest {
	p.mu.Lock()
	defer p.mu.Unlock()

	requests := make([]CapturedRequest, 0, len(p.requests))
	for _, request := range p.requests {
		requests = append(requests, CapturedRequest{
			Model:       request.Model,
			Messages:    append([]clnkr.Message(nil), request.Messages...),
			RawRequest:  request.RawRequest,
			RawResponse: request.RawResponse,
		})
	}
	return requests
}

func (p *MockProvider) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/chat/completions" {
		http.NotFound(w, r)
		return
	}

	rawRequest, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}

	var req chatCompletionRequest
	if err := json.Unmarshal(rawRequest, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	captured := CapturedRequest{
		Model:      req.Model,
		Messages:   append([]clnkr.Message(nil), req.Messages...),
		RawRequest: string(rawRequest),
	}

	if p.nextTurn >= len(p.turns) {
		body := []byte("no more mock turns\n")
		captured.RawResponse = string(body)
		p.requests = append(p.requests, captured)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(body)
		return
	}

	responsePayload := map[string]any{
		"choices": []map[string]any{
			{
				"message": map[string]string{
					"role":    "assistant",
					"content": p.turns[p.nextTurn],
				},
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     1,
			"completion_tokens": 1,
		},
	}
	var responseBody bytes.Buffer
	if err := json.NewEncoder(&responseBody).Encode(responsePayload); err != nil {
		http.Error(w, "encode mock response", http.StatusInternalServerError)
		return
	}
	rawResponse := responseBody.String()
	captured.RawResponse = rawResponse
	p.requests = append(p.requests, captured)
	p.nextTurn++

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(responseBody.Bytes()); err != nil {
		http.Error(w, "write mock response", http.StatusInternalServerError)
		return
	}
}
