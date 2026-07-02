// Package plan turns discovered services and the catalog into a concrete
// monitoring plan: which exporters to run, how they reach their targets,
// and what could not be matched.
package plan

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/YusufDrymz/monup/internal/catalog"
	"github.com/YusufDrymz/monup/internal/discover"
)

// AccessKind is how a generated exporter (or Prometheus itself, for
// direct-scrape targets) reaches the discovered service.
type AccessKind string

const (
	// AccessNetwork joins the target's user-defined Docker network and
	// resolves it by container name. The cleanest path.
	AccessNetwork AccessKind = "network"
	// AccessHostGateway goes through host.docker.internal to a
	// host-published (or host-local) port.
	AccessHostGateway AccessKind = "host-gateway"
	// AccessNone means there is no reachable path; the service is
	// reported but skipped.
	AccessNone AccessKind = "none"
)

// HostGateway is the DNS name exporters use to reach the host.
const HostGateway = "host.docker.internal"

// Match is one discovered service bound to a catalog recipe.
type Match struct {
	Service discover.Service
	Entry   catalog.Entry
	Reason  catalog.MatchReason
	// Instance uniquely names this match in generated artifacts (job
	// name, exporter service name). Equals Entry.Name unless several
	// instances of the same entry are discovered.
	Instance string
	// Target is the service address ("host:port") as seen from inside
	// the generated compose network, with Access already applied.
	Target string
	Access AccessKind
	// Network is the external Docker network to join (AccessNetwork only).
	Network string
	// ScrapeTarget is set for direct-scrape entries (no exporter): the
	// address Prometheus scrapes.
	ScrapeTarget string
}

// Plan is the full result of discovery × catalog.
type Plan struct {
	Matches []Match
	// Skipped are matched services with no reachable path (AccessNone).
	Skipped []Match
	// Unmatched are discovered containers with no catalog match.
	Unmatched []discover.Service
	// AlwaysEntries are catalog entries included in every plan (node).
	AlwaysEntries []catalog.Entry
	// IgnoredHostPorts are host listeners that matched nothing.
	IgnoredHostPorts []int
	Warnings         []string
}

// Options tunes plan building.
type Options struct {
	Only    []string // include only these entry names
	Exclude []string // exclude these entry names
}

// Build computes the plan from discovered containers and host ports.
func Build(services []discover.Service, hostPorts []int, cat *catalog.Catalog, opts Options) *Plan {
	p := &Plan{}
	allow := func(name string) bool {
		if contains(opts.Exclude, name) {
			return false
		}
		if len(opts.Only) > 0 && !contains(opts.Only, name) {
			return false
		}
		return true
	}

	for _, svc := range services {
		res, ok := cat.MatchService(svc.Image, svc.Ports)
		if !ok {
			p.Unmatched = append(p.Unmatched, svc)
			continue
		}
		if !allow(res.Entry.Name) {
			continue
		}
		m := Match{Service: svc, Entry: res.Entry, Reason: res.Reason, Instance: res.Entry.Name}
		m.Access, m.Network, m.Target = resolveAccess(svc, res.Port)
		if m.Access == AccessNone {
			p.Skipped = append(p.Skipped, m)
			p.Warnings = append(p.Warnings, fmt.Sprintf(
				"%s (%s): matched catalog entry %q but has no user-defined network and no published port; skipped — attach it to a network or publish port %d",
				svc.Name, svc.Image, res.Entry.Name, res.Port))
			continue
		}
		if res.Entry.Exporter == nil {
			// Direct scrape: Prometheus needs the metrics port, which can
			// differ from the matched service port (e.g. RabbitMQ 15692).
			st, ok := resolveScrapeTarget(svc, res.Entry.Scrape.Port, m.Access)
			if !ok {
				p.Skipped = append(p.Skipped, m)
				p.Warnings = append(p.Warnings, fmt.Sprintf(
					"%s (%s): metrics port %d is not reachable (not published); skipped",
					svc.Name, svc.Image, res.Entry.Scrape.Port))
				continue
			}
			m.ScrapeTarget = st
		}
		p.Matches = append(p.Matches, m)
	}

	// Host listeners: match by port only.
	for _, port := range hostPorts {
		res, ok := cat.MatchService("", []int{port})
		if !ok {
			p.IgnoredHostPorts = append(p.IgnoredHostPorts, port)
			continue
		}
		if !allow(res.Entry.Name) {
			continue
		}
		m := Match{
			Service:  discover.Service{Name: "host", Ports: []int{port}, Source: "host"},
			Entry:    res.Entry,
			Reason:   res.Reason,
			Instance: res.Entry.Name,
			Target:   fmt.Sprintf("%s:%d", HostGateway, port),
			Access:   AccessHostGateway,
		}
		if res.Entry.Exporter == nil {
			m.ScrapeTarget = fmt.Sprintf("%s:%d", HostGateway, res.Entry.Scrape.Port)
		}
		p.Matches = append(p.Matches, m)
	}

	disambiguate(p.Matches)

	for _, e := range cat.Entries {
		if e.Always && allow(e.Name) {
			p.AlwaysEntries = append(p.AlwaysEntries, e)
		}
	}
	return p
}

// resolveAccess picks the reachability strategy for a container target.
func resolveAccess(svc discover.Service, port int) (AccessKind, string, string) {
	if svc.Source == "host" {
		return AccessHostGateway, "", fmt.Sprintf("%s:%d", HostGateway, port)
	}
	if len(svc.Networks) > 0 {
		return AccessNetwork, svc.Networks[0], fmt.Sprintf("%s:%d", svc.Name, port)
	}
	if pub, ok := svc.Published[port]; ok {
		return AccessHostGateway, "", fmt.Sprintf("%s:%d", HostGateway, pub)
	}
	return AccessNone, "", ""
}

// resolveScrapeTarget computes the address Prometheus scrapes for
// direct-scrape entries, honoring the already-chosen access kind.
func resolveScrapeTarget(svc discover.Service, metricsPort int, access AccessKind) (string, bool) {
	switch access {
	case AccessNetwork:
		return fmt.Sprintf("%s:%d", svc.Name, metricsPort), true
	case AccessHostGateway:
		if pub, ok := svc.Published[metricsPort]; ok {
			return fmt.Sprintf("%s:%d", HostGateway, pub), true
		}
		return "", false
	}
	return "", false
}

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

func slug(s string) string {
	s = slugRe.ReplaceAllString(strings.ToLower(s), "-")
	return strings.Trim(s, "-")
}

// disambiguate suffixes Instance names with the container name when
// several instances of the same catalog entry were matched.
func disambiguate(matches []Match) {
	count := map[string]int{}
	for _, m := range matches {
		count[m.Entry.Name]++
	}
	used := map[string]bool{}
	for i := range matches {
		m := &matches[i]
		if count[m.Entry.Name] > 1 {
			m.Instance = m.Entry.Name + "-" + slug(m.Service.Name)
		}
		// Guarantee uniqueness even for identical container names.
		base := m.Instance
		for n := 2; used[m.Instance]; n++ {
			m.Instance = fmt.Sprintf("%s-%d", base, n)
		}
		used[m.Instance] = true
	}
}

// ExternalNetworks returns the sorted set of external Docker networks the
// generated stack must join.
func (p *Plan) ExternalNetworks() []string {
	set := map[string]bool{}
	for _, m := range p.Matches {
		if m.Network != "" {
			set[m.Network] = true
		}
	}
	nets := make([]string, 0, len(set))
	for n := range set {
		nets = append(nets, n)
	}
	sort.Strings(nets)
	return nets
}

// HasHostGateway reports whether any match relies on host.docker.internal
// (which needs extra_hosts on Linux).
func (p *Plan) HasHostGateway() bool {
	for _, m := range p.Matches {
		if m.Access == AccessHostGateway {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
