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
	anthropicBaseURL      = "https://api.anthropic.com"
	anthropicVersion      = "2023-06-01"
	anthropicDefaultModel = "claude-opus-4-8"
)

// Anthropic calls the Messages API (POST /v1/messages) directly.
type Anthropic struct {
	apiKey  string
	model   string
	baseURL string
	httpc   *http.Client
}

// NewAnthropic creates a client; empty model/baseURL use the defaults.
func NewAnthropic(apiKey, model, baseURL string) *Anthropic {
	if model == "" {
		model = anthropicDefaultModel
	}
	if baseURL == "" {
		baseURL = anthropicBaseURL
	}
	return &Anthropic{apiKey: apiKey, model: model, baseURL: baseURL,
		httpc: &http.Client{Timeout: 120 * time.Second}}
}

func (a *Anthropic) Name() string { return "anthropic/" + a.model }

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (a *Anthropic) Complete(ctx context.Context, req Request) (string, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	body, err := json.Marshal(anthropicRequest{
		Model:     a.model,
		MaxTokens: maxTokens,
		System:    req.System,
		Messages:  []anthropicMessage{{Role: "user", Content: req.Prompt}},
	})
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := a.httpc.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var parsed anthropicResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("anthropic: decode response (%s): %w", resp.Status, err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := resp.Status
		if parsed.Error != nil {
			msg = parsed.Error.Message
		}
		return "", fmt.Errorf("anthropic: %s", msg)
	}
	if parsed.StopReason == "refusal" || len(parsed.Content) == 0 {
		return "", fmt.Errorf("anthropic: empty response (stop_reason=%s)", parsed.StopReason)
	}
	var out string
	for _, c := range parsed.Content {
		if c.Type == "text" {
			out += c.Text
		}
	}
	return out, nil
}
