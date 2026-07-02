package plan

import (
	"fmt"
	"io"
)

// ansi color codes; empty when color is disabled.
type palette struct {
	green, yellow, cyan, red, dim, reset string
}

func newPalette(color bool) palette {
	if !color {
		return palette{}
	}
	return palette{
		green:  "\x1b[32m",
		yellow: "\x1b[33m",
		cyan:   "\x1b[36m",
		red:    "\x1b[31m",
		dim:    "\x1b[2m",
		reset:  "\x1b[0m",
	}
}

// Print writes a terraform-plan-style human-readable summary. files is
// the list of paths that apply would write (may be nil for plan-only).
func (p *Plan) Print(w io.Writer, files []string, color bool) {
	c := newPalette(color)

	fmt.Fprintf(w, "\nDiscovered services:\n")
	if len(p.Matches) == 0 && len(p.Skipped) == 0 && len(p.Unmatched) == 0 {
		fmt.Fprintf(w, "  (none)\n")
	}
	for _, m := range p.Matches {
		how := ""
		switch m.Access {
		case AccessNetwork:
			how = fmt.Sprintf("via network %q", m.Network)
		case AccessHostGateway:
			how = fmt.Sprintf("via %s", m.Target)
		}
		fmt.Fprintf(w, "  %s✓%s %-12s %s %s— %s, matched by %s%s\n",
			c.green, c.reset, m.Instance, sourceDesc(m), c.dim, how, m.Reason, c.reset)
	}
	for _, m := range p.Skipped {
		fmt.Fprintf(w, "  %s!%s %-12s container %s (%s) %s— matched but unreachable, skipped%s\n",
			c.yellow, c.reset, m.Entry.Name, m.Service.Name, m.Service.Image, c.dim, c.reset)
	}
	for _, u := range p.Unmatched {
		fmt.Fprintf(w, "  %s?%s %-12s container %s (%s) %s— no catalog match%s\n",
			c.cyan, c.reset, "unknown", u.Name, u.Image, c.dim, c.reset)
	}
	if n := len(p.IgnoredHostPorts); n > 0 {
		fmt.Fprintf(w, "  %s· %d other host listener(s) ignored%s\n", c.dim, n, c.reset)
	}

	fmt.Fprintf(w, "\nCore stack: prometheus, grafana, node-exporter\n")

	if len(p.Warnings) > 0 {
		fmt.Fprintf(w, "\nWarnings:\n")
		for _, warn := range p.Warnings {
			fmt.Fprintf(w, "  %s!%s %s\n", c.yellow, c.reset, warn)
		}
	}

	if files != nil {
		fmt.Fprintf(w, "\nFiles to generate (%d):\n", len(files))
		for _, f := range files {
			fmt.Fprintf(w, "  %s+%s %s\n", c.green, c.reset, f)
		}
	}
	fmt.Fprintf(w, "\n")
}
