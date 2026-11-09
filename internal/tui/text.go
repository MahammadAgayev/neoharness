package tui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// Span is a run of text in one style.
type Span struct {
	Text  string
	Style tcell.Style
}

// Line is one screen line of a buffer.
type Line struct {
	// Prefix is decoration drawn before the text: a gutter bar, a list
	// bullet, indentation. It is skipped by yank and search.
	Prefix []Span
	Spans  []Span
	// Msg marks the first line of a message, for ]m and [m.
	Msg bool
	// Head is the level of a document heading, for the outline rail.
	Head int
	// Src is the 1-based source line of a document line, 0 when none. Off
	// is the rune offset in it where an edited line's row starts.
	Src, Off int
	// Block is the index of the block the line came from, for folding.
	Block int
	// Item is what <CR> on this line opens: a workspace or thread name.
	Item string
}

// Text is the line's content without its prefix.
func (l Line) Text() string {
	var b strings.Builder
	for _, s := range l.Spans {
		b.WriteString(s.Text)
	}
	return b.String()
}

func (l Line) prefixWidth() int {
	w := 0
	for _, s := range l.Prefix {
		w += width(s.Text)
	}
	return w
}

func width(s string) int { return uniseg.StringWidth(s) }

// truncate cuts s to at most n cells, marking the cut with an ellipsis.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if width(s) <= n {
		return s
	}
	var b strings.Builder
	w := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		gw := g.Width()
		if w+gw > n-1 {
			break
		}
		b.WriteString(g.Str())
		w += gw
	}
	return b.String() + "…"
}

func flatten(s string) string { return strings.Join(strings.Fields(s), " ") }

// token is a word, a run of spaces, or a line break. A word keeps its
// pieces apart because one word can carry several styles: markdown splits
// "report_progress" at the underscore, and the halves must not be broken
// across lines.
type token struct {
	pieces []Span
	space  bool
	br     bool
}

func (t token) width() int {
	n := 0
	for _, p := range t.pieces {
		n += width(p.Text)
	}
	return n
}

func tokenize(spans []Span) []token {
	var out []token
	var word []Span
	flushWord := func() {
		if len(word) > 0 {
			out = append(out, token{pieces: word})
			word = nil
		}
	}
	for _, sp := range spans {
		var cur strings.Builder
		curSpace := false
		flush := func() {
			if cur.Len() == 0 {
				return
			}
			if curSpace {
				flushWord()
				out = append(out, token{pieces: []Span{{cur.String(), sp.Style}}, space: true})
			} else {
				word = append(word, Span{cur.String(), sp.Style})
			}
			cur.Reset()
		}
		for _, r := range sp.Text {
			if r == '\n' {
				flush()
				flushWord()
				out = append(out, token{br: true})
				continue
			}
			isSpace := r == ' ' || r == '\t'
			if cur.Len() > 0 && isSpace != curSpace {
				flush()
			}
			curSpace = isSpace
			if r == '\t' {
				r = ' '
			}
			cur.WriteRune(r)
		}
		flush()
	}
	flushWord()
	return out
}

// wrap word-wraps spans to width. first prefixes the first line, rest every
// following line; both count against the width.
func wrap(spans []Span, w int, first, rest []Span) []Line {
	if w < 8 {
		w = 8
	}
	pw := func(p []Span) int {
		n := 0
		for _, s := range p {
			n += width(s.Text)
		}
		return n
	}
	var lines []Line
	cur := Line{Prefix: first}
	curW := pw(first)
	var pendingSpace Span
	hasPending := false
	newline := func() {
		lines = append(lines, cur)
		cur = Line{Prefix: rest}
		curW = pw(rest)
		hasPending = false
	}
	add := func(text string, st tcell.Style) {
		if n := len(cur.Spans); n > 0 && cur.Spans[n-1].Style == st {
			cur.Spans[n-1].Text += text
		} else {
			cur.Spans = append(cur.Spans, Span{text, st})
		}
		curW += width(text)
	}
	for _, t := range tokenize(spans) {
		switch {
		case t.br:
			newline()
		case t.space:
			if len(cur.Spans) > 0 {
				pendingSpace, hasPending = t.pieces[0], true
			}
		default:
			tw := t.width()
			sw := 0
			if hasPending {
				sw = width(pendingSpace.Text)
			}
			if len(cur.Spans) > 0 && curW+sw+tw > w {
				newline()
				sw = 0
			}
			if hasPending && sw > 0 {
				add(pendingSpace.Text, pendingSpace.Style)
			}
			hasPending = false
			if tw <= w-curW {
				for _, p := range t.pieces {
					add(p.Text, p.Style)
				}
				continue
			}
			// A word longer than a whole line is split where it overflows.
			for _, p := range t.pieces {
				text := p.Text
				for text != "" && width(text) > w-curW {
					head, tail := splitAt(text, w-curW)
					if head == "" {
						if len(cur.Spans) > 0 {
							newline()
							continue
						}
						head, tail = firstGrapheme(text)
					}
					add(head, p.Style)
					text = tail
					newline()
				}
				if text != "" {
					add(text, p.Style)
				}
			}
		}
	}
	lines = append(lines, cur)
	return lines
}

// splitAt cuts s after at most n cells.
func splitAt(s string, n int) (string, string) {
	w, i := 0, 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		if w+g.Width() > n {
			break
		}
		w += g.Width()
		_, i = g.Positions()
	}
	return s[:i], s[i:]
}

func firstGrapheme(s string) (string, string) {
	g := uniseg.NewGraphemes(s)
	if !g.Next() {
		return s, ""
	}
	_, end := g.Positions()
	return s[:end], s[end:]
}
