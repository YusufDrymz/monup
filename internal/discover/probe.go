package discover

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Metric is one entry scraped from a Prometheus /metrics endpoint.
type Metric struct {
	Name string
	Type string // counter, gauge, histogram, summary, "" if unknown
	Help string
}

// maxProbeMetrics caps how much metadata we keep (and later feed to the
// AI layer) from a single endpoint.
const maxProbeMetrics = 300

// ProbeMetrics fetches a URL and, if it serves Prometheus text format,
// returns the metric metadata found there.
func ProbeMetrics(ctx context.Context, url string) ([]Metric, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("probe %s: %s", url, resp.Status)
	}

	byName := map[string]*Metric{}
	var order []string
	add := func(name string) *Metric {
		if m, ok := byName[name]; ok {
			return m
		}
		if len(order) >= maxProbeMetrics {
			return nil
		}
		m := &Metric{Name: name}
		byName[name] = m
		order = append(order, name)
		return m
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "<") {
			return nil, fmt.Errorf("probe %s: not prometheus text format", url)
		}
		switch {
		case strings.HasPrefix(line, "# HELP "):
			if name, help, ok := strings.Cut(strings.TrimPrefix(line, "# HELP "), " "); ok {
				if m := add(name); m != nil {
					m.Help = help
				}
			} else if m := add(strings.TrimPrefix(line, "# HELP ")); m != nil {
				_ = m
			}
		case strings.HasPrefix(line, "# TYPE "):
			if name, typ, ok := strings.Cut(strings.TrimPrefix(line, "# TYPE "), " "); ok {
				if m := add(name); m != nil {
					m.Type = typ
				}
			}
		case strings.HasPrefix(line, "#"):
			// other comments ignored
		default:
			name := line
			if i := strings.IndexAny(name, "{ "); i > 0 {
				name = name[:i]
			}
			if isMetricName(name) {
				add(name)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(order) == 0 {
		return nil, fmt.Errorf("probe %s: no metrics found", url)
	}
	out := make([]Metric, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, nil
}

func isMetricName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_', r == ':':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
