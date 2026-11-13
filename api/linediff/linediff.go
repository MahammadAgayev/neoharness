// Package linediff matches the lines of two texts, for the hub's notes to
// an agent and the TUI's diff mode.
package linediff

// maxDiffCells bounds the line-matching table. Past it, the changed middle
// of the document shows as removed and added as a whole.
const maxDiffCells = 4_000_000

// Op is one line of a diff: ' ' kept, '-' removed, '+' added.
type Op struct {
	Kind byte
	Text string
}

// Lines matches the lines of a and b. The common start and end are cut
// first, so an edit in one place costs only the size of that place.
func Lines(a, b []string) []Op {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	var out []Op
	for _, l := range a[:p] {
		out = append(out, Op{' ', l})
	}
	ma, mb := a[p:len(a)-s], b[p:len(b)-s]
	n, m := len(ma), len(mb)
	if (n+1)*(m+1) > maxDiffCells {
		for _, l := range ma {
			out = append(out, Op{'-', l})
		}
		for _, l := range mb {
			out = append(out, Op{'+', l})
		}
	} else {
		// lcs[i][j] is the longest common run of ma[i:] and mb[j:].
		lcs := make([]int32, (n+1)*(m+1))
		at := func(i, j int) *int32 { return &lcs[i*(m+1)+j] }
		for i := n - 1; i >= 0; i-- {
			for j := m - 1; j >= 0; j-- {
				if ma[i] == mb[j] {
					*at(i, j) = *at(i+1, j+1) + 1
				} else {
					*at(i, j) = max(*at(i+1, j), *at(i, j+1))
				}
			}
		}
		i, j := 0, 0
		for i < n || j < m {
			switch {
			case i < n && j < m && ma[i] == mb[j]:
				out = append(out, Op{' ', ma[i]})
				i, j = i+1, j+1
			case i < n && (j == m || *at(i+1, j) >= *at(i, j+1)):
				out = append(out, Op{'-', ma[i]})
				i++
			default:
				out = append(out, Op{'+', mb[j]})
				j++
			}
		}
	}
	for _, l := range a[len(a)-s:] {
		out = append(out, Op{' ', l})
	}
	return out
}
