package plan

import (
	"strings"
	"testing"

	"github.com/YusufDrymz/monup/internal/catalog"
	"github.com/YusufDrymz/monup/internal/discover"
)

func mustCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Load()
	if err != nil {
		t.Fatalf("catalog.Load() error: %v", err)
	}
	return c
}

func TestBuildAccessDecision(t *testing.T) {
	cat := mustCatalog(t)
	tests := []struct {
		name       string
		svc        discover.Service
		wantAccess AccessKind
		wantTarget string
		wantNet    string
	}{
		{
			name: "user-defined network wins",
			svc: discover.Service{
				Name: "myapp-db", Image: "postgres:16", Ports: []int{5432},
				Published: map[int]int{5432: 55432}, Networks: []string{"myapp_default"},
				Source: "docker",
			},
			wantAccess: AccessNetwork,
			wantTarget: "myapp-db:5432",
			wantNet:    "myapp_default",
		},
		{
			name: "published port falls back to host gateway",
			svc: discover.Service{
				Name: "cache", Image: "redis:7", Ports: []int{6379},
				Published: map[int]int{6379: 16379}, Source: "docker",
			},
			wantAccess: AccessHostGateway,
			wantTarget: "host.docker.internal:16379",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Build([]discover.Service{tt.svc}, nil, cat, Options{})
			if len(p.Matches) != 1 {
				t.Fatalf("expected 1 match, got %d (skipped=%d)", len(p.Matches), len(p.Skipped))
			}
			m := p.Matches[0]
			if m.Access != tt.wantAccess || m.Target != tt.wantTarget || m.Network != tt.wantNet {
				t.Errorf("got access=%s target=%s net=%s, want %s %s %s",
					m.Access, m.Target, m.Network, tt.wantAccess, tt.wantTarget, tt.wantNet)
			}
		})
	}
}

func TestBuildUnreachableIsSkipped(t *testing.T) {
	cat := mustCatalog(t)
	svc := discover.Service{
		Name: "lonely-db", Image: "postgres:16", Ports: []int{5432}, Source: "docker",
	}
	p := Build([]discover.Service{svc}, nil, cat, Options{})
	if len(p.Matches) != 0 || len(p.Skipped) != 1 {
		t.Fatalf("expected skip, got matches=%d skipped=%d", len(p.Matches), len(p.Skipped))
	}
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "lonely-db") {
		t.Errorf("expected warning about lonely-db, got %v", p.Warnings)
	}
}

func TestBuildDirectScrapeNeedsMetricsPort(t *testing.T) {
	cat := mustCatalog(t)
	// RabbitMQ on a user-defined network: metrics port reachable by DNS.
	mq := discover.Service{
		Name: "mq", Image: "rabbitmq:3.13-management", Ports: []int{5672, 15672},
		Networks: []string{"myapp_default"}, Source: "docker",
	}
	p := Build([]discover.Service{mq}, nil, cat, Options{})
	if len(p.Matches) != 1 {
		t.Fatalf("expected 1 match, got %+v", p)
	}
	if got := p.Matches[0].ScrapeTarget; got != "mq:15692" {
		t.Errorf("ScrapeTarget = %q, want mq:15692", got)
	}

	// Same container without network and without publishing 15692 → skipped.
	mq2 := discover.Service{
		Name: "mq2", Image: "rabbitmq:3.13", Ports: []int{5672},
		Published: map[int]int{5672: 5672}, Source: "docker",
	}
	p2 := Build([]discover.Service{mq2}, nil, cat, Options{})
	if len(p2.Matches) != 0 || len(p2.Skipped) != 1 {
		t.Fatalf("expected metrics-port skip, got matches=%d skipped=%d", len(p2.Matches), len(p2.Skipped))
	}
}

func TestBuildHostPorts(t *testing.T) {
	cat := mustCatalog(t)
	p := Build(nil, []int{5432, 22, 8080}, cat, Options{})
	if len(p.Matches) != 1 {
		t.Fatalf("expected 1 host match, got %d", len(p.Matches))
	}
	m := p.Matches[0]
	if m.Entry.Name != "postgres" || m.Target != "host.docker.internal:5432" || m.Access != AccessHostGateway {
		t.Errorf("unexpected host match: %+v", m)
	}
	if len(p.IgnoredHostPorts) != 2 {
		t.Errorf("ignored ports = %v, want [22 8080]", p.IgnoredHostPorts)
	}
}

func TestBuildDisambiguatesInstances(t *testing.T) {
	cat := mustCatalog(t)
	svcs := []discover.Service{
		{Name: "app1-db", Image: "postgres:16", Ports: []int{5432}, Networks: []string{"app1_default"}, Source: "docker"},
		{Name: "app2-db", Image: "postgres:15", Ports: []int{5432}, Networks: []string{"app2_default"}, Source: "docker"},
	}
	p := Build(svcs, nil, cat, Options{})
	if len(p.Matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(p.Matches))
	}
	if p.Matches[0].Instance == p.Matches[1].Instance {
		t.Errorf("instances not disambiguated: %q", p.Matches[0].Instance)
	}
	if p.Matches[0].Instance != "postgres-app1-db" {
		t.Errorf("instance = %q, want postgres-app1-db", p.Matches[0].Instance)
	}
}

func TestBuildOnlyExclude(t *testing.T) {
	cat := mustCatalog(t)
	svcs := []discover.Service{
		{Name: "db", Image: "postgres:16", Networks: []string{"net"}, Source: "docker"},
		{Name: "cache", Image: "redis:7", Networks: []string{"net"}, Source: "docker"},
	}
	p := Build(svcs, nil, cat, Options{Only: []string{"redis"}})
	if len(p.Matches) != 1 || p.Matches[0].Entry.Name != "redis" {
		t.Fatalf("Only filter failed: %+v", p.Matches)
	}
	p = Build(svcs, nil, cat, Options{Exclude: []string{"redis"}})
	if len(p.Matches) != 1 || p.Matches[0].Entry.Name != "postgres" {
		t.Fatalf("Exclude filter failed: %+v", p.Matches)
	}
	// Always-entries respect Exclude too.
	p = Build(nil, nil, cat, Options{Exclude: []string{"node"}})
	for _, e := range p.AlwaysEntries {
		if e.Name == "node" {
			t.Errorf("node should be excluded from always entries")
		}
	}
}

func TestExternalNetworks(t *testing.T) {
	cat := mustCatalog(t)
	svcs := []discover.Service{
		{Name: "db", Image: "postgres:16", Networks: []string{"b_net"}, Source: "docker"},
		{Name: "cache", Image: "redis:7", Networks: []string{"a_net"}, Source: "docker"},
		{Name: "db2", Image: "mysql:8", Published: map[int]int{3306: 3306}, Source: "docker"},
	}
	p := Build(svcs, nil, cat, Options{})
	nets := p.ExternalNetworks()
	if len(nets) != 2 || nets[0] != "a_net" || nets[1] != "b_net" {
		t.Errorf("ExternalNetworks() = %v, want [a_net b_net]", nets)
	}
	if !p.HasHostGateway() {
		t.Errorf("HasHostGateway() = false, want true")
	}
}
