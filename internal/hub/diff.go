package hub

import (
	"fmt"
	"slices"
	"strings"
)

// diffLimit is the largest diff a note carries, in bytes. A larger edit is
// told as counts and sections; the agent reads the artifact itself.
const diffLimit = 6000

// maxDiffCells bounds the line-matching table. Past it, the changed middle
// of the document shows as removed and added as a whole.
const maxDiffCells = 4_000_000

// op is one line of a diff: ' ' kept, '-' removed, '+' added.
type op struct {
	kind byte
	text string
}

// lineDiff matches the lines of a and b. The common start and end are cut
// first, so an edit in one place costs only the size of that place.
func lineDiff(a, b []string) []op {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	var out []op
	for _, l := range a[:p] {
		out = append(out, op{' ', l})
	}
	ma, mb := a[p:len(a)-s], b[p:len(b)-s]
	n, m := len(ma), len(mb)
	if (n+1)*(m+1) > maxDiffCells {
		for _, l := range ma {
			out = append(out, op{'-', l})
		}
		for _, l := range mb {
			out = append(out, op{'+', l})
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
				out = append(out, op{' ', ma[i]})
				i, j = i+1, j+1
			case i < n && (j == m || *at(i+1, j) >= *at(i, j+1)):
				out = append(out, op{'-', ma[i]})
				i++
			default:
				out = append(out, op{'+', mb[j]})
				j++
			}
		}
	}
	for _, l := range a[len(a)-s:] {
		out = append(out, op{' ', l})
	}
	return out
}

// unifiedDiff writes the change from old to new with one line of context.
// Each hunk header names the heading it sits under, like git's function
// context, so the agent can place it without the whole document. It also
// returns those headings and the counts of added and removed lines.
func unifiedDiff(old, new string) (diff string, sections []string, added, removed int) {
	ops := lineDiff(strings.Split(old, "\n"), strings.Split(new, "\n"))
	const context = 1
	var b strings.Builder
	heading, fence := "", false
	headings := make([]string, len(ops))
	for i, o := range ops {
		if o.kind != '-' {
			if strings.HasPrefix(o.text, "```") {
				fence = !fence
			} else if !fence && strings.HasPrefix(o.text, "#") {
				heading = o.text
			}
		}
		headings[i] = heading
	}
	oldN, newN := make([]int, len(ops)+1), make([]int, len(ops)+1)
	for i, o := range ops {
		oldN[i+1], newN[i+1] = oldN[i], newN[i]
		if o.kind != '+' {
			oldN[i+1]++
		}
		if o.kind != '-' {
			newN[i+1]++
		}
	}
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start, end := max(i-context, 0), i
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			k := end
			for k < len(ops) && ops[k].kind == ' ' {
				k++
			}
			if k == len(ops) || k-end > 2*context {
				break
			}
			end = k
		}
		end = min(end+context, len(ops))
		section := ""
		if start > 0 {
			section = headings[start-1]
		}
		if section != "" && !slices.Contains(sections, section) {
			sections = append(sections, section)
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@", oldN[start]+1, oldN[end]-oldN[start], newN[start]+1, newN[end]-newN[start])
		if section != "" {
			b.WriteString(" " + section)
		}
		b.WriteByte('\n')
		for _, o := range ops[start:end] {
			b.WriteByte(o.kind)
			b.WriteString(o.text)
			b.WriteByte('\n')
			switch o.kind {
			case '+':
				added++
			case '-':
				removed++
			}
		}
		i = end
	}
	return strings.TrimRight(b.String(), "\n"), sections, added, removed
}

// change replaces base lines from..to with lines.
type change struct {
	from, to int
	lines    []string
}

// changes lists what lineDiff's ops do to the base, in order.
func changes(ops []op) []change {
	var out []change
	i := 0
	for k := 0; k < len(ops); {
		if ops[k].kind == ' ' {
			i++
			k++
			continue
		}
		c := change{from: i, to: i}
		for ; k < len(ops) && ops[k].kind != ' '; k++ {
			if ops[k].kind == '-' {
				c.to++
			} else {
				c.lines = append(c.lines, ops[k].text)
			}
		}
		i = c.to
		out = append(out, c)
	}
	return out
}

// merge3 joins two edits of base by lines, as a merge tool does. Changes to
// different lines both stay, even when they touch; changes to the same
// lines must be the same, or the merge fails.
func merge3(base, mine, theirs string) (string, bool) {
	b := strings.Split(base, "\n")
	cs := append(changes(lineDiff(b, strings.Split(mine, "\n"))), changes(lineDiff(b, strings.Split(theirs, "\n")))...)
	// By start; at one start an insertion goes before a replacement.
	slices.SortStableFunc(cs, func(x, y change) int {
		if x.from != y.from {
			return x.from - y.from
		}
		return (x.to - x.from) - (y.to - y.from)
	})
	var out []string
	i := 0
	for k, c := range cs {
		if k > 0 {
			p := cs[k-1]
			same := p.from == c.from && p.to == c.to && slices.Equal(p.lines, c.lines)
			if same {
				continue
			}
			if c.from < p.to || c.from == p.from && p.from == p.to && c.from == c.to {
				return "", false
			}
		}
		out = append(out, b[i:c.from]...)
		out = append(out, c.lines...)
		i = c.to
	}
	return strings.Join(append(out, b[i:]...), "\n"), true
}
