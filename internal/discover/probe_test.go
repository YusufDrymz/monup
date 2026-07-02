package discover

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const metricsFixture = `# HELP orders_total Total orders processed
# TYPE orders_total counter
orders_total{status="ok"} 1027
orders_total{status="failed"} 3
# HELP queue_depth Jobs waiting in the queue
# TYPE queue_depth gauge
queue_depth 12
go_goroutines 8
`

func TestProbeMetrics(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(metricsFixture))
	}))
	defer ts.Close()

	got, err := ProbeMetrics(context.Background(), ts.URL+"/metrics")
	if err != nil {
		t.Fatalf("ProbeMetrics() error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 metrics, got %d: %+v", len(got), got)
	}
	if got[0].Name != "orders_total" || got[0].Type != "counter" || got[0].Help == "" {
		t.Errorf("first metric = %+v", got[0])
	}
	if got[2].Name != "go_goroutines" || got[2].Type != "" {
		t.Errorf("bare metric = %+v", got[2])
	}
}

func TestProbeMetricsRejectsHTML(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>welcome</body></html>"))
	}))
	defer ts.Close()
	if _, err := ProbeMetrics(context.Background(), ts.URL); err == nil {
		t.Fatal("expected error for HTML response")
	}
}

func TestProbeMetricsRejectsEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ts.Close()
	if _, err := ProbeMetrics(context.Background(), ts.URL); err == nil {
		t.Fatal("expected error for empty response")
	}
}
