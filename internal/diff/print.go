package diff

import (
	"fmt"
	"io"
	"strings"
)

// ansi color codes; empty when color is disabled.
type palette struct {
	green, yellow, red, dim, reset string
}

func newPalette(color bool) palette {
	if !color {
		return palette{}
	}
	return palette{
		green:  "\x1b[32m",
		yellow: "\x1b[33m",
		red:    "\x1b[31m",
		dim:    "\x1b[2m",
		reset:  "\x1b[0m",
	}
}

// Print writes the comparison in plan style: one line per file that
// differs, a unified diff for updates, and a summary. Unchanged files
// are only counted.
func Print(w io.Writer, files []File, color bool) {
	c := newPalette(color)
	counts := map[Status]int{}
	for _, f := range files {
		counts[f.Status]++
		switch f.Status {
		case Create:
			fmt.Fprintf(w, "  %s+%s %s %s— missing, apply would create it%s\n",
				c.green, c.reset, f.Path, c.dim, c.reset)
		case Update:
			fmt.Fprintf(w, "  %s~%s %s %s— differs from plan%s\n",
				c.yellow, c.reset, f.Path, c.dim, c.reset)
			printUnified(w, f.Diff, c)
		case Stale:
			fmt.Fprintf(w, "  %s·%s %s %s— not in plan; apply leaves it, delete by hand%s\n",
				c.dim, c.reset, f.Path, c.dim, c.reset)
		}
	}
	fmt.Fprintf(w, "\n%d to create, %d to update, %d unchanged, %d stale.\n",
		counts[Create], counts[Update], counts[Unchanged], counts[Stale])
}

func printUnified(w io.Writer, text string, c palette) {
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		tint := ""
		switch {
		case strings.HasPrefix(line, "@@"):
			tint = c.dim
		case strings.HasPrefix(line, "-"):
			tint = c.red
		case strings.HasPrefix(line, "+"):
			tint = c.green
		}
		fmt.Fprintf(w, "      %s%s%s\n", tint, line, c.reset)
	}
}
