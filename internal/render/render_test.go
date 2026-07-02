package render

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/YusufDrymz/monup/internal/catalog"
	"github.com/YusufDrymz/monup/internal/discover"
	"github.com/YusufDrymz/monup/internal/plan"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fixturePlan builds a representative plan: an exporter target reached
// via a user network, one via the host gateway, a direct-scrape target
// and one unmatched container.
func fixturePlan(t *testing.T) *plan.Plan {
	t.Helper()
	cat, err := catalog.Load()
	if err != nil {
		t.Fatalf("catalog.Load() error: %v", err)
	}
	services := []discover.Service{
		{Name: "myapp-db", Image: "postgres:16", Ports: []int{5432},
			Networks: []string{"myapp_default"}, Source: "docker"},
		{Name: "cache", Image: "redis:7-alpine", Ports: []int{6379},
			Published: map[int]int{6379: 16379}, Source: "docker"},
		{Name: "mq", Image: "rabbitmq:3.13-management", Ports: []int{5672},
			Networks: []string{"myapp_default"}, Source: "docker"},
		{Name: "api", Image: "mycorp/api:2.1", Ports: []int{8080},
			Networks: []string{"myapp_default"}, Source: "docker"},
	}
	return plan.Build(services, nil, cat, plan.Options{})
}

func TestFilesGolden(t *testing.T) {
	files, err := Files(fixturePlan(t))
	if err != nil {
		t.Fatalf("Files() error: %v", err)
	}

	wantPaths := []string{
		".env.example",
		"docker-compose.yml",
		"grafana/dashboards/node.json",
		"grafana/dashboards/postgres.json",
		"grafana/dashboards/rabbitmq.json",
		"grafana/dashboards/redis.json",
		"grafana/provisioning/dashboards/monup.yml",
		"grafana/provisioning/datasources/monup.yml",
		"prometheus/prometheus.yml",
		"prometheus/rules/node.yml",
		"prometheus/rules/postgres.yml",
		"prometheus/rules/rabbitmq.yml",
		"prometheus/rules/redis.yml",
	}
	gotPaths := Paths(files)
	if len(gotPaths) != len(wantPaths) {
		t.Fatalf("paths = %v\nwant %v", gotPaths, wantPaths)
	}
	for i := range wantPaths {
		if gotPaths[i] != wantPaths[i] {
			t.Fatalf("paths[%d] = %q, want %q", i, gotPaths[i], wantPaths[i])
		}
	}

	for path, got := range files {
		goldenPath := filepath.Join("testdata", "golden", path)
		if *update {
			if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(goldenPath)
		if err != nil {
			t.Fatalf("missing golden file %s (run `go test ./internal/render -update`): %v", goldenPath, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s differs from golden file (run with -update after reviewing)\ngot:\n%s", path, got)
		}
	}
}

// All generated YAML must actually parse, and dashboards must be valid JSON.
func TestFilesAreWellFormed(t *testing.T) {
	files, err := Files(fixturePlan(t))
	if err != nil {
		t.Fatalf("Files() error: %v", err)
	}
	for path, data := range files {
		switch filepath.Ext(path) {
		case ".yml", ".yaml":
			var v any
			if err := yaml.Unmarshal(data, &v); err != nil {
				t.Errorf("%s is not valid YAML: %v", path, err)
			}
		case ".json":
			var v any
			if err := json.Unmarshal(data, &v); err != nil {
				t.Errorf("%s is not valid JSON: %v", path, err)
			}
		}
	}
}

// The compose file must wire exporters to their targets correctly.
func TestComposeWiring(t *testing.T) {
	files, err := Files(fixturePlan(t))
	if err != nil {
		t.Fatalf("Files() error: %v", err)
	}
	compose := string(files["docker-compose.yml"])

	for _, want := range []string{
		// postgres exporter reaches the DB by container DNS on the joined network
		"myapp-db:5432",
		"- myapp_default",
		// redis exporter goes through the host gateway to the published port
		"redis://host.docker.internal:16379",
		"host.docker.internal:host-gateway",
		// managed label so monup ignores its own stack on re-discovery
		discover.ManagedLabel,
		// external network declared exactly once
		"external: true",
	} {
		if !contains(compose, want) {
			t.Errorf("docker-compose.yml missing %q", want)
		}
	}

	prom := string(files["prometheus/prometheus.yml"])
	for _, want := range []string{
		"job_name: postgres",
		"job_name: redis",
		"job_name: rabbitmq",
		`targets: ["postgres-exporter:9187"]`,
		`targets: ["redis-exporter:9121"]`,
		// direct scrape: prometheus reaches rabbitmq's own metrics port
		`targets: ["mq:15692"]`,
	} {
		if !contains(prom, want) {
			t.Errorf("prometheus.yml missing %q", want)
		}
	}

	env := string(files[".env.example"])
	for _, want := range []string{"MONUP_PG_USER=", "MONUP_PG_PASSWORD=", "MONUP_GRAFANA_ADMIN_PASSWORD=admin"} {
		if !contains(env, want) {
			t.Errorf(".env.example missing %q", want)
		}
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
