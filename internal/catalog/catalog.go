// Package catalog defines the built-in service catalog: fingerprints for
// well-known services and the monitoring recipe (exporter, scrape config,
// alert rules, dashboard) that monup generates for each of them.
package catalog

import (
	"embed"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed builtin/*.yaml
var builtinFS embed.FS

// Entry is one service definition in the catalog.
type Entry struct {
	Name string `yaml:"name"`
	// Always marks entries that are part of every plan regardless of
	// discovery (e.g. node exporter for host metrics).
	Always bool  `yaml:"always,omitempty"`
	Match  Match `yaml:"match,omitempty"`
	// Exporter is the sidecar that translates the service's own protocol
	// into Prometheus metrics. Nil means the target exposes /metrics
	// itself and is scraped directly (see Scrape.Port).
	Exporter  *Exporter  `yaml:"exporter,omitempty"`
	Scrape    Scrape     `yaml:"scrape,omitempty"`
	Alerts    []Alert    `yaml:"alerts,omitempty"`
	Dashboard *Dashboard `yaml:"dashboard,omitempty"`
}

// Match describes how a discovered service is recognized as this entry.
type Match struct {
	// Images are exact image references without tag: either a bare name
	// ("postgres") matched against the last path segment, or a full repo
	// path ("bitnami/postgresql") matched against the whole path.
	Images []string `yaml:"images,omitempty"`
	// Ports are well-known ports used as a weaker fallback signal.
	Ports []int `yaml:"ports,omitempty"`
}

// Exporter describes the exporter container to run for a matched service.
// Env values and Args may contain the {{target}} token, replaced at plan
// time with the address of the matched service ("host:port"), and ${VAR}
// references left for the user to fill via .env.
type Exporter struct {
	Image string            `yaml:"image"`
	Port  int               `yaml:"port"`
	Env   map[string]string `yaml:"env,omitempty"`
	Args  []string          `yaml:"args,omitempty"`
}

// Scrape tunes the generated Prometheus scrape job.
type Scrape struct {
	JobName     string `yaml:"job_name,omitempty"`     // default: entry name
	MetricsPath string `yaml:"metrics_path,omitempty"` // default: /metrics
	// Port is only used when Exporter is nil: the target's own port that
	// serves Prometheus metrics (e.g. RabbitMQ's built-in 15692).
	Port int `yaml:"port,omitempty"`
}

// Alert is a Prometheus alerting rule template.
type Alert struct {
	Alert       string            `yaml:"alert"`
	Expr        string            `yaml:"expr"`
	For         string            `yaml:"for,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

// Dashboard is a minimal declarative Grafana dashboard: a list of panels
// rendered into provisioning JSON by the render package.
type Dashboard struct {
	Title  string  `yaml:"title"`
	Panels []Panel `yaml:"panels"`
}

// Panel is a single dashboard panel backed by one PromQL expression.
type Panel struct {
	Title string `yaml:"title"`
	Expr  string `yaml:"expr"`
	Type  string `yaml:"type,omitempty"` // "timeseries" (default) or "stat"
	Unit  string `yaml:"unit,omitempty"`
}

// Catalog is the loaded set of entries, ordered by name for determinism.
type Catalog struct {
	Entries []Entry
}

// Load parses and validates the embedded built-in catalog.
func Load() (*Catalog, error) {
	files, err := builtinFS.ReadDir("builtin")
	if err != nil {
		return nil, fmt.Errorf("read builtin catalog: %w", err)
	}
	var entries []Entry
	for _, f := range files {
		data, err := builtinFS.ReadFile("builtin/" + f.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f.Name(), err)
		}
		var e Entry
		if err := yaml.Unmarshal(data, &e); err != nil {
			return nil, fmt.Errorf("parse %s: %w", f.Name(), err)
		}
		if err := e.validate(); err != nil {
			return nil, fmt.Errorf("invalid catalog entry %s: %w", f.Name(), err)
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return &Catalog{Entries: entries}, nil
}

func (e Entry) validate() error {
	if e.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !e.Always && len(e.Match.Images) == 0 && len(e.Match.Ports) == 0 {
		return fmt.Errorf("entry needs at least one match rule (or always: true)")
	}
	if e.Exporter != nil {
		if e.Exporter.Image == "" || e.Exporter.Port == 0 {
			return fmt.Errorf("exporter requires image and port")
		}
	} else if !e.Always && e.Scrape.Port == 0 {
		return fmt.Errorf("entry without exporter requires scrape.port")
	}
	for _, a := range e.Alerts {
		if a.Alert == "" || a.Expr == "" {
			return fmt.Errorf("alert requires alert and expr")
		}
	}
	if e.Dashboard != nil {
		if e.Dashboard.Title == "" || len(e.Dashboard.Panels) == 0 {
			return fmt.Errorf("dashboard requires title and at least one panel")
		}
		for _, p := range e.Dashboard.Panels {
			if p.Title == "" || p.Expr == "" {
				return fmt.Errorf("panel requires title and expr")
			}
		}
	}
	return nil
}

// JobName returns the Prometheus job name for the entry.
func (e Entry) JobName() string {
	if e.Scrape.JobName != "" {
		return e.Scrape.JobName
	}
	return e.Name
}

// MetricsPath returns the scrape path for the entry.
func (e Entry) MetricsPath() string {
	if e.Scrape.MetricsPath != "" {
		return e.Scrape.MetricsPath
	}
	return "/metrics"
}

// MatchImage reports whether the given image reference (possibly with
// registry, tag or digest) matches this entry's image fingerprints.
func (e Entry) MatchImage(imageRef string) bool {
	repo := normalizeImage(imageRef)
	last := repo
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		last = repo[i+1:]
	}
	for _, want := range e.Match.Images {
		if strings.Contains(want, "/") {
			// Full repo path fingerprint: match with or without registry.
			if repo == want || strings.HasSuffix(repo, "/"+want) {
				return true
			}
		} else if last == want {
			return true
		}
	}
	return false
}

// MatchPort reports whether any of the given ports is a fingerprint port.
func (e Entry) MatchPort(ports []int) (int, bool) {
	for _, want := range e.Match.Ports {
		for _, p := range ports {
			if p == want {
				return p, true
			}
		}
	}
	return 0, false
}

// normalizeImage strips tag and digest from an image reference.
func normalizeImage(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	// A colon after the last slash is a tag separator, not a registry port.
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		ref = ref[:colon]
	}
	return ref
}

// MatchReason describes which signal matched a service to an entry.
type MatchReason string

const (
	ReasonImage MatchReason = "image"
	ReasonPort  MatchReason = "port"
)

// Result is a successful catalog match.
type Result struct {
	Entry  Entry
	Reason MatchReason
	// Port is the service-side port the recipe should target.
	Port int
}

// MatchService matches a discovered image and port list against the
// catalog. Image fingerprints win over port fingerprints; entries are
// evaluated in name order for determinism. Always-entries never match.
func (c *Catalog) MatchService(imageRef string, ports []int) (Result, bool) {
	var portHit *Result
	for _, e := range c.Entries {
		if e.Always {
			continue
		}
		if imageRef != "" && e.MatchImage(imageRef) {
			port := 0
			if p, ok := e.MatchPort(ports); ok {
				port = p
			} else if len(e.Match.Ports) > 0 {
				// Assume the well-known port when the container does not
				// expose it explicitly.
				port = e.Match.Ports[0]
			}
			return Result{Entry: e, Reason: ReasonImage, Port: port}, true
		}
		if portHit == nil {
			if p, ok := e.MatchPort(ports); ok {
				portHit = &Result{Entry: e, Reason: ReasonPort, Port: p}
			}
		}
	}
	if portHit != nil {
		return *portHit, true
	}
	return Result{}, false
}
