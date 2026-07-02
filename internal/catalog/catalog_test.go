package catalog

import (
	"strings"
	"testing"
)

func TestLoadBuiltin(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(c.Entries) < 6 {
		t.Fatalf("expected at least 6 builtin entries, got %d", len(c.Entries))
	}
	// Entries must be sorted by name for deterministic matching.
	for i := 1; i < len(c.Entries); i++ {
		if c.Entries[i-1].Name >= c.Entries[i].Name {
			t.Errorf("entries not sorted: %q >= %q", c.Entries[i-1].Name, c.Entries[i].Name)
		}
	}
	// Every non-always entry must be reachable via exporter or direct scrape.
	for _, e := range c.Entries {
		if !e.Always && e.Exporter == nil && e.Scrape.Port == 0 {
			t.Errorf("entry %s has neither exporter nor scrape.port", e.Name)
		}
	}
}

func TestNormalizeImage(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"postgres", "postgres"},
		{"postgres:16", "postgres"},
		{"postgres:16.3-alpine", "postgres"},
		{"bitnami/postgresql:latest", "bitnami/postgresql"},
		{"quay.io/prometheuscommunity/postgres-exporter:v0.15.0", "quay.io/prometheuscommunity/postgres-exporter"},
		{"registry.local:5000/myapp:1.2", "registry.local:5000/myapp"},
		{"redis@sha256:abcdef", "redis"},
	}
	for _, tt := range tests {
		if got := normalizeImage(tt.in); got != tt.want {
			t.Errorf("normalizeImage(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMatchService(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	tests := []struct {
		name      string
		image     string
		ports     []int
		wantEntry string
		wantWhy   MatchReason
		wantOK    bool
	}{
		{"postgres by image with tag", "postgres:16", nil, "postgres", ReasonImage, true},
		{"postgres full path", "bitnami/postgresql:15", nil, "postgres", ReasonImage, true},
		{"postgres with registry prefix", "docker.io/library/postgres:16", nil, "postgres", ReasonImage, true},
		{"timescale is postgres", "timescale/timescaledb:latest-pg16", nil, "postgres", ReasonImage, true},
		{"redis by image", "redis:7-alpine", []int{6379}, "redis", ReasonImage, true},
		{"valkey is redis", "valkey/valkey:8", nil, "redis", ReasonImage, true},
		{"mariadb is mysql", "mariadb:11", nil, "mysql", ReasonImage, true},
		{"unknown image known port", "internal/legacy-db:1", []int{5432}, "postgres", ReasonPort, true},
		{"image beats port", "redis:7", []int{5432}, "redis", ReasonImage, true},
		{"exporter does not match service", "quay.io/prometheuscommunity/postgres-exporter:v0.15.0", nil, "", "", false},
		{"unknown", "mycorp/api:2.1", []int{8080}, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := c.MatchService(tt.image, tt.ports)
			if ok != tt.wantOK {
				t.Fatalf("MatchService(%q, %v) ok = %v, want %v", tt.image, tt.ports, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.Entry.Name != tt.wantEntry {
				t.Errorf("matched entry = %q, want %q", got.Entry.Name, tt.wantEntry)
			}
			if got.Reason != tt.wantWhy {
				t.Errorf("match reason = %q, want %q", got.Reason, tt.wantWhy)
			}
		})
	}
}

func TestMatchServicePickPort(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	// Container exposes the well-known port → that port is picked.
	got, ok := c.MatchService("postgres:16", []int{5432})
	if !ok || got.Port != 5432 {
		t.Fatalf("expected port 5432, got %+v ok=%v", got, ok)
	}
	// Container exposes nothing → fall back to the entry's default port.
	got, ok = c.MatchService("postgres:16", nil)
	if !ok || got.Port != 5432 {
		t.Fatalf("expected default port 5432, got %+v ok=%v", got, ok)
	}
}

func TestEntryHelpers(t *testing.T) {
	e := Entry{Name: "foo"}
	if e.JobName() != "foo" {
		t.Errorf("JobName default = %q", e.JobName())
	}
	if e.MetricsPath() != "/metrics" {
		t.Errorf("MetricsPath default = %q", e.MetricsPath())
	}
	e.Scrape = Scrape{JobName: "bar", MetricsPath: "/m"}
	if e.JobName() != "bar" || e.MetricsPath() != "/m" {
		t.Errorf("JobName/MetricsPath override failed: %q %q", e.JobName(), e.MetricsPath())
	}
}

func TestAlertExprsLookSane(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	for _, e := range c.Entries {
		for _, a := range e.Alerts {
			if strings.TrimSpace(a.Expr) == "" {
				t.Errorf("%s/%s: empty expr", e.Name, a.Alert)
			}
			if a.Labels["severity"] == "" {
				t.Errorf("%s/%s: missing severity label", e.Name, a.Alert)
			}
		}
	}
}
