// Package ai is the optional LLM layer: it classifies containers the
// catalog doesn't recognize and generates dashboards and alert rules from
// scraped metric metadata. monup is fully functional without it — every
// AI result is validated before it reaches the plan, and a missing
// provider only disables the --ai flag.
//
// Providers are called over plain HTTP (no SDKs), matching the rest of
// the codebase: Anthropic Messages API, OpenAI chat completions, Ollama.
package ai

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Client is a minimal completion interface; all providers implement it.
type Client interface {
	// Complete returns the model's text output for the given prompt.
	Complete(ctx context.Context, req Request) (string, error)
	// Name identifies the provider/model for logs ("anthropic/claude-...").
	Name() string
}

// Request is a single completion request.
type Request struct {
	System    string
	Prompt    string
	MaxTokens int // default 4096
}

// ErrNoProvider is returned by New when no provider is configured.
var ErrNoProvider = errors.New(
	"no AI provider configured: set ANTHROPIC_API_KEY, OPENAI_API_KEY or OLLAMA_HOST (override with MONUP_AI_PROVIDER)")

// New picks a provider from the environment. Explicit MONUP_AI_PROVIDER
// (anthropic|openai|ollama) wins; otherwise the first configured API key
// decides. MONUP_AI_MODEL overrides the provider's default model.
func New() (Client, error) {
	model := os.Getenv("MONUP_AI_MODEL")
	provider := strings.ToLower(os.Getenv("MONUP_AI_PROVIDER"))
	if provider == "" {
		switch {
		case os.Getenv("ANTHROPIC_API_KEY") != "":
			provider = "anthropic"
		case os.Getenv("OPENAI_API_KEY") != "":
			provider = "openai"
		case os.Getenv("OLLAMA_HOST") != "":
			provider = "ollama"
		default:
			return nil, ErrNoProvider
		}
	}
	switch provider {
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("MONUP_AI_PROVIDER=anthropic but ANTHROPIC_API_KEY is not set")
		}
		return NewAnthropic(key, model, ""), nil
	case "openai":
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("MONUP_AI_PROVIDER=openai but OPENAI_API_KEY is not set")
		}
		return NewOpenAI(key, model, ""), nil
	case "ollama":
		return NewOllama(model, os.Getenv("OLLAMA_HOST")), nil
	default:
		return nil, fmt.Errorf("unknown MONUP_AI_PROVIDER %q (anthropic|openai|ollama)", provider)
	}
}

// stripFences removes a ```json ... ``` wrapper models sometimes add
// despite instructions, so downstream json.Unmarshal sees bare JSON.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}
