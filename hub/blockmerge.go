package hub

import (
	"regexp"
	"slices"
	"strings"

	"github.com/MahammadAgayev/neoharness/api/linediff"
	"github.com/MahammadAgayev/neoharness/api/model"
)

var (
	fenceRE = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	itemRE  = regexp.MustCompile(`^\s*([-*+]|\d+[.)])(\s|$)`)
)

func blank(l string) bool { return strings.TrimSpace(l) == "" }

// blocks splits markdown into its top-level blocks, each with the blank
// lines after it, so the blocks joined are the text byte for byte. A block
// is a run of lines between blank lines; fenced code and lists stay whole
// across their blank lines. It does not try to match a markdown parser: a
// client maps line ranges to its own blocks.
func blocks(text string) []string {
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var out []string
	i := 0
	for i < len(lines) && blank(lines[i]) {
		i++
	}
	if i > 0 {
		out = append(out, strings.Join(lines[:i], ""))
	}
	for i < len(lines) {
		start := i
		list := itemRE.MatchString(lines[i])
		for {
			fence := ""
			for i < len(lines) && (fence != "" || !blank(lines[i])) {
				switch m := fenceRE.FindStringSubmatch(lines[i]); {
				case fence == "" && m != nil:
					fence = m[1]
				case fence != "" && closes(lines[i], fence):
					fence = ""
				}
				i++
			}
			j := i
			for j < len(lines) && blank(lines[j]) {
				j++
			}
			more := j > i && j < len(lines) && (itemRE.MatchString(lines[j]) || strings.HasPrefix(lines[j], "  ") || strings.HasPrefix(lines[j], "\t"))
			i = j
			if !list || !more {
				break
			}
		}
		out = append(out, strings.Join(lines[start:i], ""))
	}
	return out
}

// closes says a line ends a fence opened with fence.
func closes(line, fence string) bool {
	t := strings.TrimLeft(line, " ")
	return len(line)-len(t) <= 3 && strings.HasPrefix(t, fence) && strings.Trim(t, fence[:1]+" \t\r\n") == ""
}

// lineCount is how many lines a piece of text holds.
func lineCount(s string) int {
	n := strings.Count(s, "\n")
	if s != "" && !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// span is the lines of bs[from:to], without the blank lines after the last
// block, from line first.
func span(first int, bs []string) [2]int {
	end := first
	for _, b := range bs {
		end += lineCount(b)
	}
	if len(bs) > 0 {
		ls := strings.SplitAfter(bs[len(bs)-1], "\n")
		for k := len(ls) - 1; k >= 0 && end > first; k-- {
			if ls[k] == "" {
				continue
			}
			if !blank(ls[k]) {
				break
			}
			end--
		}
	}
	return [2]int{first, end}
}

func linesOf(bs []string) int {
	n := 0
	for _, b := range bs {
		n += lineCount(b)
	}
	return n
}

// mergeBlocks joins two edits of base by blocks. Changes to different
// blocks both stay, and a block neither side touched comes out byte for
// byte. Where both changed one block, the lines merge as merge3 does; when
// they cannot, the block is a conflict and keeps ours.
func mergeBlocks(base, ours, theirs string) model.Merge {
	b := blocks(base)
	sides := [2][]string{blocks(ours), blocks(theirs)}
	type sided struct {
		change
		side int
	}
	var cs []sided
	for s, bs := range sides {
		for _, c := range changes(linediff.Lines(b, bs)) {
			cs = append(cs, sided{c, s})
		}
	}
	// By start; at one start an insertion goes before a replacement.
	slices.SortStableFunc(cs, func(x, y sided) int {
		if x.from != y.from {
			return x.from - y.from
		}
		return (x.to - x.from) - (y.to - y.from)
	})
	m := model.Merge{Clean: true}
	var out strings.Builder
	written := 0
	write := func(bs []string) {
		s := strings.Join(bs, "")
		out.WriteString(s)
		written += lineCount(s)
	}
	// shift is where each side stands against the base: its block index
	// minus the base's.
	var shift [2]int
	at := 0
	for k := 0; k < len(cs); {
		cf, ct := cs[k].from, cs[k].to
		group := []sided{cs[k]}
		for k++; k < len(cs); k++ {
			c := cs[k]
			if c.from >= ct && !(c.from == c.to && cf == ct && c.from == cf) {
				break
			}
			group = append(group, c)
			ct = max(ct, c.to)
		}
		write(b[at:cf])
		at = ct
		var region [2][]string
		var has [2]bool
		for s := range sides {
			i := cf
			for _, g := range group {
				if g.side == s {
					region[s] = append(region[s], b[i:g.from]...)
					region[s] = append(region[s], g.lines...)
					i, has[s] = g.to, true
				}
			}
			region[s] = append(region[s], b[i:ct]...)
		}
		starts := [2]int{cf + shift[0], cf + shift[1]}
		for s := range sides {
			shift[s] += len(region[s]) - (ct - cf)
		}
		switch {
		case !has[1] || slices.Equal(region[0], region[1]):
			write(region[0])
		case !has[0]:
			write(region[1])
		default:
			if merged, ok := merge3(strings.Join(b[cf:ct], ""), strings.Join(region[0], ""), strings.Join(region[1], "")); ok {
				write([]string{merged})
				continue
			}
			m.Clean = false
			m.Conflicts = append(m.Conflicts, model.Conflict{
				Base:   span(linesOf(b[:cf]), b[cf:ct]),
				Ours:   span(linesOf(sides[0][:starts[0]]), region[0]),
				Theirs: span(linesOf(sides[1][:starts[1]]), region[1]),
				Merged: span(written, region[0]),
			})
			write(region[0])
		}
	}
	write(b[at:])
	m.Merged = out.String()
	return m
}
