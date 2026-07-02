package main

import (
	"context"
	"fmt"
	"io"
	"runtime"

	"github.com/YusufDrymz/monup/internal/ai"
	"github.com/YusufDrymz/monup/internal/catalog"
	"github.com/YusufDrymz/monup/internal/discover"
	"github.com/YusufDrymz/monup/internal/plan"
)

// aiEnrich upgrades unmatched containers using the AI layer. For each
// one it first probes published ports for a /metrics endpoint — if the
// service exposes Prometheus metrics, the model generates a tailored
// dashboard and alerts (validated before use). Otherwise the model tries
// to classify the container as one of the known catalog entries (custom
// images the fingerprints miss). Anything still unknown stays unmatched;
// AI never degrades the plan, it only adds to it.
func aiEnrich(ctx context.Context, p *plan.Plan, cat *catalog.Catalog, client ai.Client, out io.Writer) {
	known := make([]string, 0, len(cat.Entries))
	for _, e := range cat.Entries {
		if !e.Always {
			known = append(known, e.Name)
		}
	}
	used := map[string]bool{}
	for _, m := range p.Matches {
		used[m.Instance] = true
	}
	uniqueInstance := func(base string, svc discover.Service) string {
		name := base
		if used[name] {
			name = base + "-" + plan.Slug(svc.Name)
		}
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		used[name] = true
		return name
	}

	var still []discover.Service
	for _, svc := range p.Unmatched {
		if m, ok := tryGenerate(ctx, p, client, svc, uniqueInstance, out); ok {
			p.Matches = append(p.Matches, m)
			continue
		}
		if m, ok := tryClassify(ctx, p, cat, client, svc, known, uniqueInstance, out); ok {
			p.Matches = append(p.Matches, m)
			continue
		}
		still = append(still, svc)
	}
	p.Unmatched = still
}

// probeTarget is one candidate /metrics endpoint of a container: the URL
// to probe from the host and the service-side port the recipe targets.
type probeTarget struct {
	url  string
	port int
}

// probeTargets lists candidate endpoints: published ports via localhost
// everywhere; on linux also unpublished ports via the container IP,
// which the host can reach directly (network-only containers).
func probeTargets(svc discover.Service, goos string) []probeTarget {
	var targets []probeTarget
	for _, port := range svc.Ports {
		switch pub, ok := svc.Published[port]; {
		case ok:
			targets = append(targets, probeTarget{fmt.Sprintf("http://127.0.0.1:%d/metrics", pub), port})
		case goos == "linux" && svc.IP != "":
			targets = append(targets, probeTarget{fmt.Sprintf("http://%s:%d/metrics", svc.IP, port), port})
		}
	}
	return targets
}

// tryGenerate probes the container's reachable ports for a /metrics
// endpoint and, on success, has the model generate a recipe for it.
func tryGenerate(ctx context.Context, p *plan.Plan, client ai.Client, svc discover.Service,
	uniqueInstance func(string, discover.Service) string, out io.Writer) (plan.Match, bool) {

	for _, pt := range probeTargets(svc, runtime.GOOS) {
		metrics, err := discover.ProbeMetrics(ctx, pt.url)
		if err != nil {
			continue
		}
		fmt.Fprintf(out, "ai: %s exposes %d metrics on :%d, generating dashboard and alerts (%s) ...\n",
			svc.Name, len(metrics), pt.port, client.Name())
		name := uniqueInstance(plan.Slug(svc.Name), svc)
		entry, err := ai.GenerateEntry(ctx, client, name, pt.port, "/metrics", metrics)
		if err != nil {
			p.Warnings = append(p.Warnings, fmt.Sprintf("ai: %s: %v", svc.Name, err))
			return plan.Match{}, false
		}
		m, warn, ok := plan.Bind(svc, entry, pt.port, "ai-metrics")
		if !ok {
			p.Warnings = append(p.Warnings, "ai: "+warn)
			return plan.Match{}, false
		}
		m.Instance = name
		return m, true
	}
	return plan.Match{}, false
}

// tryClassify asks the model whether the container is a known service
// type behind an unrecognized image name.
func tryClassify(ctx context.Context, p *plan.Plan, cat *catalog.Catalog, client ai.Client,
	svc discover.Service, known []string,
	uniqueInstance func(string, discover.Service) string, out io.Writer) (plan.Match, bool) {

	cl, err := ai.Classify(ctx, client, svc, known)
	if err != nil {
		p.Warnings = append(p.Warnings, fmt.Sprintf("ai: classify %s: %v", svc.Name, err))
		return plan.Match{}, false
	}
	if cl.Entry == "none" || cl.Confidence < 0.6 {
		return plan.Match{}, false
	}
	for _, e := range cat.Entries {
		if e.Name != cl.Entry {
			continue
		}
		port := 0
		if pp, ok := e.MatchPort(svc.Ports); ok {
			port = pp
		} else if len(e.Match.Ports) > 0 {
			port = e.Match.Ports[0]
		}
		m, warn, ok := plan.Bind(svc, e, port, "ai-classify")
		if !ok {
			p.Warnings = append(p.Warnings, "ai: "+warn)
			return plan.Match{}, false
		}
		m.Instance = uniqueInstance(e.Name, svc)
		fmt.Fprintf(out, "ai: %s (%s) classified as %s — %s\n", svc.Name, svc.Image, cl.Entry, cl.Reason)
		return m, true
	}
	return plan.Match{}, false
}
