package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/YusufDrymz/monup/internal/catalog"
	"github.com/YusufDrymz/monup/internal/discover"
)

const generateSystem = `You generate Prometheus alert rules and Grafana panel specs for a service, using ONLY the metric names provided by the user. Output ONLY a JSON object, no prose, no markdown fences, with this exact shape:
{
  "alerts": [
    {"alert": "PascalCaseName", "expr": "<PromQL>", "for": "5m", "severity": "warning|critical", "summary": "<one line, may use {{ $labels.instance }}>"}
  ],
  "dashboard": {
    "title": "<short service title>",
    "panels": [
      {"title": "<short>", "expr": "<PromQL>", "type": "timeseries|stat", "unit": "<grafana unit id or empty>"}
    ]
  }
}
Rules: 2-4 alerts, 4-8 panels. Every expr must reference at least one provided metric name. Use rate() over [5m] for counters. Prefer a stat panel for up/health. Do not invent metric names.`

// generated mirrors the JSON the model must return.
type generated struct {
	Alerts []struct {
		Alert    string `json:"alert"`
		Expr     string `json:"expr"`
		For      string `json:"for"`
		Severity string `json:"severity"`
		Summary  string `json:"summary"`
	} `json:"alerts"`
	Dashboard struct {
		Title  string          `json:"title"`
		Panels []catalog.Panel `json:"panels"`
	} `json:"dashboard"`
}

// GenerateEntry asks the model for alerts and a dashboard based on the
// metrics scraped from a service, validates the result, and retries once
// with the validation error before giving up. The returned entry is a
// direct-scrape catalog entry for the given metrics port.
func GenerateEntry(ctx context.Context, c Client, name string, metricsPort int, metricsPath string, metrics []discover.Metric) (catalog.Entry, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Service name: %s\nMetrics (name type help):\n", name)
	for _, m := range metrics {
		fmt.Fprintf(&b, "%s %s %s\n", m.Name, m.Type, m.Help)
	}
	prompt := b.String()

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		p := prompt
		if lastErr != nil {
			p += "\nYour previous output was rejected: " + lastErr.Error() + "\nFix it and output the corrected JSON only."
		}
		raw, err := c.Complete(ctx, Request{System: generateSystem, Prompt: p, MaxTokens: 8192})
		if err != nil {
			return catalog.Entry{}, err
		}
		entry, err := parseGenerated(name, metricsPort, metricsPath, raw, metrics)
		if err == nil {
			return entry, nil
		}
		lastErr = err
	}
	return catalog.Entry{}, fmt.Errorf("AI output failed validation twice: %w", lastErr)
}

func parseGenerated(name string, metricsPort int, metricsPath, raw string, metrics []discover.Metric) (catalog.Entry, error) {
	var g generated
	if err := json.Unmarshal([]byte(stripFences(raw)), &g); err != nil {
		return catalog.Entry{}, fmt.Errorf("output is not valid JSON: %w", err)
	}
	if g.Dashboard.Title == "" || len(g.Dashboard.Panels) == 0 {
		return catalog.Entry{}, fmt.Errorf("dashboard must have a title and at least one panel")
	}
	known := map[string]bool{}
	for _, m := range metrics {
		known[m.Name] = true
	}
	refsKnown := func(expr string) bool {
		for n := range known {
			if strings.Contains(expr, n) {
				return true
			}
		}
		return false
	}

	e := catalog.Entry{
		Name:   name,
		Match:  catalog.Match{Ports: []int{metricsPort}},
		Scrape: catalog.Scrape{Port: metricsPort, MetricsPath: metricsPath},
		Dashboard: &catalog.Dashboard{
			Title: g.Dashboard.Title,
		},
	}
	for _, p := range g.Dashboard.Panels {
		if p.Title == "" || p.Expr == "" {
			return catalog.Entry{}, fmt.Errorf("panel %q is missing title or expr", p.Title)
		}
		if !refsKnown(p.Expr) {
			return catalog.Entry{}, fmt.Errorf("panel %q references no provided metric: %s", p.Title, p.Expr)
		}
		if p.Type != "" && p.Type != "timeseries" && p.Type != "stat" {
			p.Type = "timeseries"
		}
		e.Dashboard.Panels = append(e.Dashboard.Panels, p)
	}
	for _, a := range g.Alerts {
		if a.Alert == "" || a.Expr == "" {
			return catalog.Entry{}, fmt.Errorf("alert %q is missing name or expr", a.Alert)
		}
		if !refsKnown(a.Expr) {
			return catalog.Entry{}, fmt.Errorf("alert %q references no provided metric: %s", a.Alert, a.Expr)
		}
		sev := a.Severity
		if sev != "critical" {
			sev = "warning"
		}
		e.Alerts = append(e.Alerts, catalog.Alert{
			Alert:       a.Alert,
			Expr:        a.Expr,
			For:         a.For,
			Labels:      map[string]string{"severity": sev},
			Annotations: map[string]string{"summary": a.Summary},
		})
	}
	if err := e.Validate(); err != nil {
		return catalog.Entry{}, err
	}
	return e, nil
}
