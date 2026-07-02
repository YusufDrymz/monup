package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/YusufDrymz/monup/internal/ai"
	"github.com/YusufDrymz/monup/internal/catalog"
	"github.com/YusufDrymz/monup/internal/discover"
	"github.com/YusufDrymz/monup/internal/plan"
)

type stubAI struct{ responses []string }

func (s *stubAI) Name() string { return "stub/test" }
func (s *stubAI) Complete(context.Context, ai.Request) (string, error) {
	if len(s.responses) == 0 {
		return "", fmt.Errorf("stub exhausted")
	}
	r := s.responses[0]
	s.responses = s.responses[1:]
	return r, nil
}

func TestAIEnrichGeneratesFromMetrics(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# TYPE orders_total counter\norders_total 5\n"))
	}))
	defer ts.Close()
	u, _ := url.Parse(ts.URL)
	hostPort, _ := strconv.Atoi(u.Port())

	svc := discover.Service{
		Name: "shop-api", Image: "shop/api:1.0", Ports: []int{8080},
		Published: map[int]int{8080: hostPort},
		Networks:  []string{"shop_default"}, Source: "docker",
	}
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	p := plan.Build([]discover.Service{svc}, nil, cat, plan.Options{})
	if len(p.Unmatched) != 1 {
		t.Fatalf("expected 1 unmatched, got %d", len(p.Unmatched))
	}

	stub := &stubAI{responses: []string{`{
		"alerts": [{"alert": "NoOrders", "expr": "rate(orders_total[5m]) == 0", "for": "10m", "severity": "warning", "summary": "no orders"}],
		"dashboard": {"title": "Shop API", "panels": [{"title": "Orders/s", "expr": "rate(orders_total[5m])"}]}
	}`}}
	var out bytes.Buffer
	aiEnrich(context.Background(), p, cat, stub, &out)

	if len(p.Unmatched) != 0 || len(p.Matches) != 1 {
		t.Fatalf("unmatched=%d matches=%d, want 0/1 (warnings: %v)", len(p.Unmatched), len(p.Matches), p.Warnings)
	}
	m := p.Matches[0]
	if m.Instance != "shop-api" || m.Entry.Exporter != nil {
		t.Errorf("match = %+v", m)
	}
	// On a user-defined network the metrics are scraped by container DNS.
	if m.ScrapeTarget != "shop-api:8080" {
		t.Errorf("ScrapeTarget = %q, want shop-api:8080", m.ScrapeTarget)
	}
	if m.Entry.Dashboard == nil || len(m.Entry.Alerts) != 1 {
		t.Errorf("generated entry incomplete: %+v", m.Entry)
	}
}

func TestAIEnrichClassifiesCustomImage(t *testing.T) {
	svc := discover.Service{
		Name: "legacy-db", Image: "corp/pg-custom:12", Ports: []int{5432},
		Networks: []string{"corp_net"}, Source: "docker",
	}
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	p := plan.Build([]discover.Service{svc}, nil, cat, plan.Options{})

	stub := &stubAI{responses: []string{
		`{"entry": "postgres", "confidence": 0.92, "reason": "pg in image name, port 5432"}`,
	}}
	var out bytes.Buffer
	aiEnrich(context.Background(), p, cat, stub, &out)

	if len(p.Matches) != 1 || p.Matches[0].Entry.Name != "postgres" {
		t.Fatalf("expected postgres match, got %+v (warnings: %v)", p.Matches, p.Warnings)
	}
	if p.Matches[0].Target != "legacy-db:5432" {
		t.Errorf("target = %q", p.Matches[0].Target)
	}
}

func TestAIEnrichLeavesUnknownAlone(t *testing.T) {
	svc := discover.Service{Name: "mystery", Image: "corp/thing:1", Ports: []int{9999},
		Networks: []string{"n"}, Source: "docker"}
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	p := plan.Build([]discover.Service{svc}, nil, cat, plan.Options{})
	stub := &stubAI{responses: []string{`{"entry": "none", "confidence": 0.9, "reason": "custom app"}`}}
	var out bytes.Buffer
	aiEnrich(context.Background(), p, cat, stub, &out)
	if len(p.Unmatched) != 1 || len(p.Matches) != 0 {
		t.Fatalf("unknown service should stay unmatched: %+v", p.Matches)
	}
}
