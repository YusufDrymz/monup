package diff

import (
	"fmt"
	"strings"
)

const contextLines = 3

// Lines returns a unified diff (3 lines of context, @@ hunk headers, no
// file headers) from a to b. Empty string when the contents are equal.
func Lines(a, b []byte) string {
	ops := diffOps(splitLines(a), splitLines(b))
	return formatHunks(ops)
}

func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// op is one line of the edit script: ' ' kept, '-' removed, '+' added.
type op struct {
	kind byte
	text string
}

func diffOps(a, b []string) []op {
	// Trim the common prefix and suffix first; generated files usually
	// differ in a few spots, and this keeps the LCS table small.
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}

	ops := make([]op, 0, len(a)+len(b))
	for _, l := range a[:p] {
		ops = append(ops, op{' ', l})
	}
	ops = append(ops, lcsOps(a[p:len(a)-s], b[p:len(b)-s])...)
	for _, l := range a[len(a)-s:] {
		ops = append(ops, op{' ', l})
	}
	return ops
}

func lcsOps(a, b []string) []op {
	n, m := len(a), len(b)
	if n*m > 4_000_000 {
		// Degenerate case (two huge, mostly different files): a full
		// remove/add beats an O(n·m) table.
		ops := make([]op, 0, n+m)
		for _, l := range a {
			ops = append(ops, op{'-', l})
		}
		for _, l := range b {
			ops = append(ops, op{'+', l})
		}
		return ops
	}

	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var ops []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{'-', a[i]})
			i++
		default:
			ops = append(ops, op{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, op{'+', b[j]})
	}
	return ops
}

func formatHunks(ops []op) string {
	var changed []int
	for i, o := range ops {
		if o.kind != ' ' {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return ""
	}

	// aNum[i] / bNum[i]: lines of a / b consumed before op i.
	aNum := make([]int, len(ops)+1)
	bNum := make([]int, len(ops)+1)
	for i, o := range ops {
		aNum[i+1], bNum[i+1] = aNum[i], bNum[i]
		if o.kind != '+' {
			aNum[i+1]++
		}
		if o.kind != '-' {
			bNum[i+1]++
		}
	}

	var b strings.Builder
	for h := 0; h < len(changed); {
		start := max(changed[h]-contextLines, 0)
		end := changed[h] + contextLines + 1
		h++
		for h < len(changed) && changed[h]-contextLines <= end {
			end = changed[h] + contextLines + 1
			h++
		}
		end = min(end, len(ops))

		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n",
			aNum[start]+1, aNum[end]-aNum[start],
			bNum[start]+1, bNum[end]-bNum[start])
		for _, o := range ops[start:end] {
			b.WriteByte(o.kind)
			b.WriteString(o.text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
