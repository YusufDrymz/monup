package render

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/YusufDrymz/monup/internal/catalog"
	"github.com/YusufDrymz/monup/internal/plan"
)

// prometheusConfig renders prometheus.yml with one scrape job per match
// plus the self and node jobs. Hand-built for stable, readable output.
func prometheusConfig(p *plan.Plan) []byte {
	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString(`global:
  scrape_interval: 15s
  evaluation_interval: 15s

rule_files:
  - rules/*.yml

scrape_configs:
  - job_name: prometheus
    static_configs:
      - targets: ["localhost:9090"]
  - job_name: node
    static_configs:
      - targets: ["node-exporter:9100"]
`)
	for _, m := range p.Matches {
		b.WriteString(scrapeJob(m))
	}
	return []byte(b.String())
}

func scrapeJob(m plan.Match) string {
	target := m.ScrapeTarget
	if m.Entry.Exporter != nil {
		target = fmt.Sprintf("%s:%d", exporterServiceName(m), m.Entry.Exporter.Port)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  - job_name: %s\n", m.Instance)
	if path := m.Entry.MetricsPath(); path != "/metrics" {
		fmt.Fprintf(&b, "    metrics_path: %s\n", path)
	}
	b.WriteString("    static_configs:\n")
	fmt.Fprintf(&b, "      - targets: [%q]\n", target)
	fmt.Fprintf(&b, "        labels:\n          monup_service: %q\n", m.Entry.Name)
	return b.String()
}

// exporterServiceName is the compose service (and DNS) name of the
// exporter generated for a match.
func exporterServiceName(m plan.Match) string {
	return m.Instance + "-exporter"
}

// Prometheus alerting rule file structures (yaml.v3 sorts map keys, so
// marshaling is deterministic).
type ruleFile struct {
	Groups []ruleGroup `yaml:"groups"`
}

type ruleGroup struct {
	Name  string          `yaml:"name"`
	Rules []catalog.Alert `yaml:"rules"`
}

func ruleFileFor(e catalog.Entry) ([]byte, error) {
	rf := ruleFile{Groups: []ruleGroup{{Name: "monup-" + e.Name, Rules: e.Alerts}}}
	data, err := yaml.Marshal(rf)
	if err != nil {
		return nil, err
	}
	return append([]byte(generatedHeader), data...), nil
}
