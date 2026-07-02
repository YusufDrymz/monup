package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YusufDrymz/monup/internal/discover"
)

func TestAnthropicComplete(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("missing auth headers")
		}
		var req anthropicRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "claude-opus-4-8" || req.MaxTokens == 0 {
			t.Errorf("unexpected request: %+v", req)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"content":     []map[string]string{{"type": "text", "text": "hello"}},
			"stop_reason": "end_turn",
		})
	}))
	defer ts.Close()

	c := NewAnthropic("test-key", "", ts.URL)
	got, err := c.Complete(context.Background(), Request{Prompt: "hi"})
	if err != nil || got != "hello" {
		t.Fatalf("Complete() = %q, %v", got, err)
	}
}

func TestAnthropicError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad model"}}`))
	}))
	defer ts.Close()
	_, err := NewAnthropic("k", "", ts.URL).Complete(context.Background(), Request{Prompt: "hi"})
	if err == nil || !strings.Contains(err.Error(), "bad model") {
		t.Fatalf("expected API error, got %v", err)
	}
}

func TestOpenAIComplete(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing bearer token")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "hi there"}}},
		})
	}))
	defer ts.Close()
	got, err := NewOpenAI("test-key", "", ts.URL).Complete(context.Background(), Request{Prompt: "hi"})
	if err != nil || got != "hi there" {
		t.Fatalf("Complete() = %q, %v", got, err)
	}
}

func TestOllamaComplete(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"role": "assistant", "content": "ok"}})
	}))
	defer ts.Close()
	got, err := NewOllama("", ts.URL).Complete(context.Background(), Request{Prompt: "hi"})
	if err != nil || got != "ok" {
		t.Fatalf("Complete() = %q, %v", got, err)
	}
}

func TestNewProviderSelection(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("MONUP_AI_PROVIDER", "")
	t.Setenv("MONUP_AI_MODEL", "")
	if _, err := New(); err == nil {
		t.Fatal("expected ErrNoProvider with clean env")
	}
	t.Setenv("ANTHROPIC_API_KEY", "k")
	c, err := New()
	if err != nil || !strings.HasPrefix(c.Name(), "anthropic/") {
		t.Fatalf("New() = %v, %v", c, err)
	}
	t.Setenv("MONUP_AI_PROVIDER", "ollama")
	c, err = New()
	if err != nil || !strings.HasPrefix(c.Name(), "ollama/") {
		t.Fatalf("explicit provider override failed: %v, %v", c, err)
	}
}

// stubClient returns queued responses in order.
type stubClient struct {
	responses []string
	calls     int
}

func (s *stubClient) Name() string { return "stub/test" }
func (s *stubClient) Complete(context.Context, Request) (string, error) {
	if s.calls >= len(s.responses) {
		return "", fmt.Errorf("stub exhausted after %d calls", s.calls)
	}
	r := s.responses[s.calls]
	s.calls++
	return r, nil
}

var testMetrics = []discover.Metric{
	{Name: "orders_total", Type: "counter", Help: "Total orders"},
	{Name: "queue_depth", Type: "gauge", Help: "Jobs waiting"},
}

const goodGenerated = `{
  "alerts": [
    {"alert": "QueueBacklog", "expr": "queue_depth > 100", "for": "5m", "severity": "warning", "summary": "queue is backing up"}
  ],
  "dashboard": {
    "title": "Shop API",
    "panels": [
      {"title": "Orders/s", "expr": "rate(orders_total[5m])", "type": "timeseries", "unit": "ops"},
      {"title": "Queue depth", "expr": "queue_depth"}
    ]
  }
}`

func TestGenerateEntry(t *testing.T) {
	stub := &stubClient{responses: []string{"```json\n" + goodGenerated + "\n```"}}
	e, err := GenerateEntry(context.Background(), stub, "shop-api", 8080, "/metrics", testMetrics)
	if err != nil {
		t.Fatalf("GenerateEntry() error: %v", err)
	}
	if e.Name != "shop-api" || e.Scrape.Port != 8080 || e.Exporter != nil {
		t.Errorf("unexpected entry: %+v", e)
	}
	if len(e.Alerts) != 1 || e.Alerts[0].Labels["severity"] != "warning" {
		t.Errorf("alerts = %+v", e.Alerts)
	}
	if e.Dashboard == nil || len(e.Dashboard.Panels) != 2 {
		t.Errorf("dashboard = %+v", e.Dashboard)
	}
}

func TestGenerateEntryRetriesOnBadMetric(t *testing.T) {
	bad := strings.ReplaceAll(goodGenerated, "queue_depth", "invented_metric")
	stub := &stubClient{responses: []string{bad, goodGenerated}}
	e, err := GenerateEntry(context.Background(), stub, "shop-api", 8080, "/metrics", testMetrics)
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if stub.calls != 2 {
		t.Errorf("calls = %d, want 2 (one retry)", stub.calls)
	}
	if e.Dashboard.Title != "Shop API" {
		t.Errorf("entry = %+v", e)
	}
}

func TestGenerateEntryGivesUpAfterTwo(t *testing.T) {
	stub := &stubClient{responses: []string{"not json", "still not json"}}
	if _, err := GenerateEntry(context.Background(), stub, "x", 1, "/metrics", testMetrics); err == nil {
		t.Fatal("expected failure after two invalid outputs")
	}
}

func TestClassify(t *testing.T) {
	svc := discover.Service{Name: "legacy-db", Image: "corp/pg-custom:12", Ports: []int{5432},
		Labels: map[string]string{"com.docker.compose.project": "shop"}}
	known := []string{"mysql", "postgres", "redis"}

	stub := &stubClient{responses: []string{`{"entry": "postgres", "confidence": 0.95, "reason": "port 5432 and pg in image name"}`}}
	cl, err := Classify(context.Background(), stub, svc, known)
	if err != nil || cl.Entry != "postgres" || cl.Confidence < 0.9 {
		t.Fatalf("Classify() = %+v, %v", cl, err)
	}

	// Hallucinated entry names are rejected.
	stub = &stubClient{responses: []string{`{"entry": "oracle", "confidence": 0.9, "reason": "?"}`}}
	if _, err := Classify(context.Background(), stub, svc, known); err == nil {
		t.Fatal("expected rejection of unknown entry name")
	}
}
