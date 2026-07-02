package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/YusufDrymz/monup/internal/discover"
)

const classifySystem = `You map a Docker container to one of the known service types, or "none" if it is something else (like a custom application). Output ONLY a JSON object, no prose, no markdown fences:
{"entry": "<one of the known names or none>", "confidence": 0.0-1.0, "reason": "<one short line>"}`

// Classification is the model's verdict for an unknown container.
type Classification struct {
	Entry      string  `json:"entry"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// Classify asks the model whether an unrecognized container is actually
// one of the known catalog entries (e.g. a custom-built postgres image).
// Only container metadata is sent — never env values or command lines,
// which may contain credentials.
func Classify(ctx context.Context, c Client, svc discover.Service, knownEntries []string) (Classification, error) {
	labelKeys := make([]string, 0, len(svc.Labels))
	for k := range svc.Labels {
		labelKeys = append(labelKeys, k)
	}
	prompt := fmt.Sprintf(
		"Known service types: %s\nContainer name: %s\nImage: %s\nPorts: %v\nLabel keys: %s",
		strings.Join(knownEntries, ", "), svc.Name, svc.Image, svc.Ports, strings.Join(labelKeys, ", "))

	raw, err := c.Complete(ctx, Request{System: classifySystem, Prompt: prompt, MaxTokens: 512})
	if err != nil {
		return Classification{}, err
	}
	var cl Classification
	if err := json.Unmarshal([]byte(stripFences(raw)), &cl); err != nil {
		return Classification{}, fmt.Errorf("classification is not valid JSON: %w", err)
	}
	// Guard against hallucinated entry names.
	if cl.Entry != "none" && !contains(knownEntries, cl.Entry) {
		return Classification{}, fmt.Errorf("model suggested unknown entry %q", cl.Entry)
	}
	return cl, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
