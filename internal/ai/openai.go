package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	openaiBaseURL      = "https://api.openai.com"
	openaiDefaultModel = "gpt-4o-mini"
)

// OpenAI calls the chat completions API.
type OpenAI struct {
	apiKey  string
	model   string
	baseURL string
	httpc   *http.Client
}

// NewOpenAI creates a client; empty model/baseURL use the defaults.
func NewOpenAI(apiKey, model, baseURL string) *OpenAI {
	if model == "" {
		model = openaiDefaultModel
	}
	if baseURL == "" {
		baseURL = openaiBaseURL
	}
	return &OpenAI{apiKey: apiKey, model: model, baseURL: baseURL,
		httpc: &http.Client{Timeout: 120 * time.Second}}
}

func (o *OpenAI) Name() string { return "openai/" + o.model }

type openaiRequest struct {
	Model               string          `json:"model"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Messages            []openaiMessage `json:"messages"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiResponse struct {
	Choices []struct {
		Message openaiMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (o *OpenAI) Complete(ctx context.Context, req Request) (string, error) {
	msgs := []openaiMessage{}
	if req.System != "" {
		msgs = append(msgs, openaiMessage{Role: "system", Content: req.System})
	}
	msgs = append(msgs, openaiMessage{Role: "user", Content: req.Prompt})
	body, err := json.Marshal(openaiRequest{
		Model: o.model, MaxCompletionTokens: req.MaxTokens, Messages: msgs,
	})
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.httpc.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var parsed openaiResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("openai: decode response (%s): %w", resp.Status, err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := resp.Status
		if parsed.Error != nil {
			msg = parsed.Error.Message
		}
		return "", fmt.Errorf("openai: %s", msg)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("openai: empty response")
	}
	return parsed.Choices[0].Message.Content, nil
}
