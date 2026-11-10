package hub

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MahammadAgayev/neoharness/internal/linediff"
)

// diffLimit is the largest diff a note carries, in bytes. A larger edit is
// told as counts and sections; the agent reads the artifact itself.
const diffLimit = 6000

// unifiedDiff writes the change from old to new with one line of context.
// Each hunk header names the heading it sits under, like git's function
// context, so the agent can place it without the whole document. It also
// returns those headings and the counts of added and removed lines.
func unifiedDiff(old, new string) (diff string, sections []string, added, removed int) {
	ops := linediff.Lines(strings.Split(old, "\n"), strings.Split(new, "\n"))
	const context = 1
	var b strings.Builder
	heading, fence := "", false
	headings := make([]string, len(ops))
	for i, o := range ops {
		if o.Kind != '-' {
			if strings.HasPrefix(o.Text, "```") {
				fence = !fence
			} else if !fence && strings.HasPrefix(o.Text, "#") {
				heading = o.Text
			}
		}
		headings[i] = heading
	}
	oldN, newN := make([]int, len(ops)+1), make([]int, len(ops)+1)
	for i, o := range ops {
		oldN[i+1], newN[i+1] = oldN[i], newN[i]
		if o.Kind != '+' {
			oldN[i+1]++
		}
		if o.Kind != '-' {
			newN[i+1]++
		}
	}
	for i := 0; i < len(ops); {
		if ops[i].Kind == ' ' {
			i++
			continue
		}
		start, end := max(i-context, 0), i
		for end < len(ops) {
			if ops[end].Kind != ' ' {
				end++
				continue
			}
			k := end
			for k < len(ops) && ops[k].Kind == ' ' {
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
			b.WriteByte(o.Kind)
			b.WriteString(o.Text)
			b.WriteByte('\n')
			switch o.Kind {
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

// changes lists what linediff.Lines' ops do to the base, in order.
func changes(ops []linediff.Op) []change {
	var out []change
	i := 0
	for k := 0; k < len(ops); {
		if ops[k].Kind == ' ' {
			i++
			k++
			continue
		}
		c := change{from: i, to: i}
		for ; k < len(ops) && ops[k].Kind != ' '; k++ {
			if ops[k].Kind == '-' {
				c.to++
			} else {
				c.lines = append(c.lines, ops[k].Text)
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
	cs := append(changes(linediff.Lines(b, strings.Split(mine, "\n"))), changes(linediff.Lines(b, strings.Split(theirs, "\n")))...)
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
