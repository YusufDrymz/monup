package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/YusufDrymz/monup/internal/discover"
	"github.com/YusufDrymz/monup/internal/plan"
)

// composeFile renders docker-compose.yml. Hand-built YAML: service order
// and formatting stay stable and human-friendly, values that could
// confuse YAML are always quoted.
func composeFile(p *plan.Plan) []byte {
	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString("name: monup\n\nservices:\n")

	// Prometheus joins external networks of direct-scrape targets and
	// needs the host gateway when a direct-scrape target relies on it.
	var promNets []string
	promHostGW := false
	for _, m := range p.Matches {
		if m.Entry.Exporter == nil {
			if m.Network != "" {
				promNets = append(promNets, m.Network)
			}
			if m.Access == plan.AccessHostGateway {
				promHostGW = true
			}
		}
	}

	writeService(&b, service{
		name:  "prometheus",
		image: prometheusImage,
		volumes: []string{
			"./prometheus/prometheus.yml:/etc/prometheus/prometheus.yml:ro",
			"./prometheus/rules:/etc/prometheus/rules:ro",
			"prometheus-data:/prometheus",
		},
		ports:       []string{"9090:9090"},
		networks:    append([]string{"monup"}, dedupeSorted(promNets)...),
		hostGateway: promHostGW,
		healthTest:  "wget --no-verbose --tries=1 --spider http://localhost:9090/-/healthy || exit 1",
	})

	writeService(&b, service{
		name:  "grafana",
		image: grafanaImage,
		env: map[string]string{
			"GF_SECURITY_ADMIN_USER":     "${MONUP_GRAFANA_ADMIN_USER:-admin}",
			"GF_SECURITY_ADMIN_PASSWORD": "${MONUP_GRAFANA_ADMIN_PASSWORD:-admin}",
		},
		volumes: []string{
			"./grafana/provisioning:/etc/grafana/provisioning:ro",
			"./grafana/dashboards:/var/lib/grafana/dashboards:ro",
			"grafana-data:/var/lib/grafana",
		},
		ports:      []string{"3000:3000"},
		networks:   []string{"monup"},
		dependsOn:  []string{"prometheus"},
		healthTest: "wget --no-verbose --tries=1 --spider http://localhost:3000/api/health || exit 1",
	})

	// No rslave on the rootfs mount: Docker Desktop rejects mount
	// propagation flags, and a plain ro bind works on Linux too.
	writeService(&b, service{
		name:     "node-exporter",
		image:    nodeExporterImage,
		command:  []string{"--path.rootfs=/host"},
		pidHost:  true,
		volumes:  []string{"/:/host:ro"},
		networks: []string{"monup"},
	})

	for _, m := range p.Matches {
		exp := m.Entry.Exporter
		if exp == nil {
			continue
		}
		env := map[string]string{}
		for k, v := range exp.Env {
			env[k] = strings.ReplaceAll(v, "{{target}}", m.Target)
		}
		var args []string
		for _, a := range exp.Args {
			args = append(args, strings.ReplaceAll(a, "{{target}}", m.Target))
		}
		nets := []string{"monup"}
		if m.Network != "" {
			nets = append(nets, m.Network)
		}
		writeService(&b, service{
			name:        exporterServiceName(m),
			image:       exp.Image,
			env:         env,
			command:     args,
			networks:    nets,
			hostGateway: m.Access == plan.AccessHostGateway,
		})
	}

	b.WriteString("networks:\n  monup: {}\n")
	for _, n := range p.ExternalNetworks() {
		fmt.Fprintf(&b, "  %s:\n    external: true\n", n)
	}
	b.WriteString("\nvolumes:\n  grafana-data: {}\n  prometheus-data: {}\n")
	return []byte(b.String())
}

type service struct {
	name        string
	image       string
	env         map[string]string
	command     []string
	volumes     []string
	ports       []string
	networks    []string
	dependsOn   []string
	pidHost     bool
	hostGateway bool
	healthTest  string
}

func writeService(b *strings.Builder, s service) {
	fmt.Fprintf(b, "  %s:\n", s.name)
	fmt.Fprintf(b, "    image: %s\n", s.image)
	fmt.Fprintf(b, "    container_name: monup-%s\n", s.name)
	fmt.Fprintf(b, "    labels:\n      %s: \"true\"\n", discover.ManagedLabel)
	if len(s.command) > 0 {
		b.WriteString("    command:\n")
		for _, c := range s.command {
			fmt.Fprintf(b, "      - %q\n", c)
		}
	}
	if len(s.env) > 0 {
		b.WriteString("    environment:\n")
		keys := make([]string, 0, len(s.env))
		for k := range s.env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(b, "      %s: %q\n", k, s.env[k])
		}
	}
	if s.pidHost {
		b.WriteString("    pid: host\n")
	}
	if len(s.volumes) > 0 {
		b.WriteString("    volumes:\n")
		for _, v := range s.volumes {
			fmt.Fprintf(b, "      - %q\n", v)
		}
	}
	if len(s.ports) > 0 {
		b.WriteString("    ports:\n")
		for _, p := range s.ports {
			fmt.Fprintf(b, "      - %q\n", p)
		}
	}
	if s.hostGateway {
		b.WriteString("    extra_hosts:\n      - \"host.docker.internal:host-gateway\"\n")
	}
	if len(s.dependsOn) > 0 {
		b.WriteString("    depends_on:\n")
		for _, d := range s.dependsOn {
			fmt.Fprintf(b, "      - %s\n", d)
		}
	}
	if s.healthTest != "" {
		b.WriteString("    healthcheck:\n")
		fmt.Fprintf(b, "      test: [\"CMD-SHELL\", %q]\n", s.healthTest)
		b.WriteString("      interval: 30s\n      timeout: 5s\n      retries: 3\n")
	}
	if len(s.networks) > 0 {
		b.WriteString("    networks:\n")
		for _, n := range s.networks {
			fmt.Fprintf(b, "      - %s\n", n)
		}
	}
	b.WriteString("    restart: unless-stopped\n\n")
}

func dedupeSorted(in []string) []string {
	set := map[string]bool{}
	for _, s := range in {
		set[s] = true
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
