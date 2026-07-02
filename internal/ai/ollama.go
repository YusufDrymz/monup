package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const ollamaDefaultModel = "llama3.1"

// Ollama calls a local Ollama server (/api/chat, non-streaming).
type Ollama struct {
	model   string
	baseURL string
	httpc   *http.Client
}

// NewOllama creates a client; empty model/host use the defaults
// (llama3.1 at http://localhost:11434).
func NewOllama(model, host string) *Ollama {
	if model == "" {
		model = ollamaDefaultModel
	}
	if host == "" {
		host = "http://localhost:11434"
	}
	if !strings.HasPrefix(host, "http") {
		host = "http://" + host
	}
	return &Ollama{model: model, baseURL: strings.TrimSuffix(host, "/"),
		httpc: &http.Client{Timeout: 300 * time.Second}}
}

func (o *Ollama) Name() string { return "ollama/" + o.model }

type ollamaRequest struct {
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	Messages []ollamaMessage `json:"messages"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaResponse struct {
	Message ollamaMessage `json:"message"`
	Error   string        `json:"error"`
}

func (o *Ollama) Complete(ctx context.Context, req Request) (string, error) {
	msgs := []ollamaMessage{}
	if req.System != "" {
		msgs = append(msgs, ollamaMessage{Role: "system", Content: req.System})
	}
	msgs = append(msgs, ollamaMessage{Role: "user", Content: req.Prompt})
	body, err := json.Marshal(ollamaRequest{Model: o.model, Stream: false, Messages: msgs})
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.httpc.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var parsed ollamaResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("ollama: decode response (%s): %w", resp.Status, err)
	}
	if resp.StatusCode != http.StatusOK || parsed.Error != "" {
		msg := parsed.Error
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("ollama: %s", msg)
	}
	return parsed.Message.Content, nil
}
