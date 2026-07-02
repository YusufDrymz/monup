package plan

import (
	"fmt"
	"io"
	"sort"
)

// Delta is the difference between two consecutive plans as human-readable
// lines; watch uses it to report services that appeared or disappeared.
type Delta struct {
	Added   []string
	Removed []string
}

func (d Delta) Empty() bool { return len(d.Added) == 0 && len(d.Removed) == 0 }

// DeltaFrom compares p against an older plan.
func (p *Plan) DeltaFrom(old *Plan) Delta {
	oldSet, newSet := summaryLines(old), summaryLines(p)
	var d Delta
	for line := range newSet {
		if !oldSet[line] {
			d.Added = append(d.Added, line)
		}
	}
	for line := range oldSet {
		if !newSet[line] {
			d.Removed = append(d.Removed, line)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	return d
}

func summaryLines(p *Plan) map[string]bool {
	set := map[string]bool{}
	for _, m := range p.Matches {
		set[fmt.Sprintf("%s  %s", m.Instance, sourceDesc(m))] = true
	}
	for _, m := range p.Skipped {
		set[fmt.Sprintf("%s  %s (unreachable, skipped)", m.Entry.Name, sourceDesc(m))] = true
	}
	for _, u := range p.Unmatched {
		set[fmt.Sprintf("unknown  container %s (%s)", u.Name, u.Image)] = true
	}
	return set
}

func sourceDesc(m Match) string {
	if m.Service.Source == "host" {
		return fmt.Sprintf("host listener :%d", m.Service.Ports[0])
	}
	return fmt.Sprintf("container %s (%s)", m.Service.Name, m.Service.Image)
}

// Print writes the delta with +/- prefixes.
func (d Delta) Print(w io.Writer, color bool) {
	c := newPalette(color)
	for _, l := range d.Added {
		fmt.Fprintf(w, "  %s+%s %s\n", c.green, c.reset, l)
	}
	for _, l := range d.Removed {
		fmt.Fprintf(w, "  %s-%s %s\n", c.red, c.reset, l)
	}
}
