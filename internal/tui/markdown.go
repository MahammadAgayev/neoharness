package tui

import (
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/gdamore/tcell/v2"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

var codeStyle = styles.Get("rose-pine-moon")

// renderMarkdown turns markdown into wrapped lines. prefix is drawn before
// every line (a message's gutter bar).
func renderMarkdown(src string, w int, th *Theme, prefix []Span) []Line {
	source := []byte(src)
	doc := md.Parser().Parse(text.NewReader(source))
	r := &mdRenderer{src: source, th: th, w: w}
	r.blocks(doc, prefix, prefix)
	if len(r.out) == 0 {
		return []Line{{Prefix: prefix}}
	}
	return r.out
}

type mdRenderer struct {
	src []byte
	th  *Theme
	w   int
	out []Line
}

func (r *mdRenderer) blank(prefix []Span) {
	if len(r.out) > 0 {
		r.out = append(r.out, Line{Prefix: prefix})
	}
}

// blocks renders the block children of n. first prefixes the first line of
// the first child, rest every other line.
func (r *mdRenderer) blocks(n ast.Node, first, rest []Span) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if c != n.FirstChild() {
			if _, tight := n.(*ast.ListItem); !tight {
				r.blank(rest)
			}
			first = rest
		}
		r.block(c, first, rest)
	}
}

func (r *mdRenderer) block(n ast.Node, first, rest []Span) {
	switch n := n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		r.out = append(r.out, wrap(r.inline(n, r.th.Text), r.w, first, rest)...)
	case *ast.Heading:
		r.out = append(r.out, wrap(r.inline(n, r.th.Bold), r.w, first, rest)...)
	case *ast.FencedCodeBlock:
		lang := string(n.Language(r.src))
		r.code(r.lines(n), lang, first, rest)
	case *ast.CodeBlock:
		r.code(r.lines(n), "", first, rest)
	case *ast.Blockquote:
		bar := []Span{{"▎ ", r.th.Muted}}
		r.blocks(n, append(clone(first), bar...), append(clone(rest), bar...))
	case *ast.List:
		i := n.Start
		if i == 0 {
			i = 1
		}
		for item := n.FirstChild(); item != nil; item = item.NextSibling() {
			bullet := "• "
			if n.IsOrdered() {
				bullet = strconv.Itoa(i) + ". "
				i++
			}
			indent := strings.Repeat(" ", width(bullet))
			if item != n.FirstChild() {
				first = rest
				if !n.IsTight {
					r.blank(rest)
				}
			}
			r.blocks(item,
				append(clone(first), Span{bullet, r.th.Subtle}),
				append(clone(rest), Span{indent, r.th.Text}))
		}
	case *ast.ThematicBreak:
		r.out = append(r.out, Line{Prefix: first, Spans: []Span{{strings.Repeat("─", min(r.w-width(spansText(first)), 40)), r.th.Muted}}})
	case *east.Table:
		r.table(n, first, rest)
	case *ast.HTMLBlock:
		for _, l := range r.lines(n) {
			r.out = append(r.out, Line{Prefix: first, Spans: []Span{{l, r.th.Subtle}}})
			first = rest
		}
	default:
		if n.HasChildren() {
			r.blocks(n, first, rest)
		}
	}
}

// table lines the cells up in columns. When the table is wider than the
// window, the widest column shrinks first and its cells end in "…".
func (r *mdRenderer) table(n ast.Node, first, rest []Span) {
	var rows [][][]Span
	var aligns []east.Alignment
	for row := n.FirstChild(); row != nil; row = row.NextSibling() {
		_, head := row.(*east.TableHeader)
		var cells [][]Span
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			st := r.th.Text
			if head {
				st = r.th.Bold
			}
			cells = append(cells, r.inline(cell, st))
			if len(rows) == 0 {
				aligns = append(aligns, cell.(*east.TableCell).Alignment)
			}
		}
		rows = append(rows, cells)
	}
	widths := make([]int, len(aligns))
	for _, cells := range rows {
		for i, c := range cells {
			if i < len(widths) {
				widths[i] = max(widths[i], width(spansText(c)))
			}
		}
	}
	avail := r.w - width(spansText(first)) - 3*(len(widths)-1)
	for {
		total, widest := 0, 0
		for i, w := range widths {
			total += w
			if w > widths[widest] {
				widest = i
			}
		}
		if total <= avail || widths[widest] <= 3 {
			break
		}
		widths[widest]--
	}
	for ri, cells := range rows {
		var spans []Span
		for i, w := range widths {
			if i > 0 {
				spans = append(spans, Span{" │ ", r.th.Muted})
			}
			var c []Span
			if i < len(cells) {
				c = fitSpans(cells[i], w)
			}
			gap := Span{strings.Repeat(" ", w-width(spansText(c))), r.th.Text}
			switch {
			case aligns[i] == east.AlignRight:
				spans = append(append(spans, gap), c...)
			case i == len(widths)-1:
				spans = append(spans, c...)
			default:
				spans = append(append(spans, c...), gap)
			}
		}
		r.out = append(r.out, Line{Prefix: first, Spans: spans})
		first = rest
		if ri == 0 {
			rule := make([]string, len(widths))
			for i, w := range widths {
				rule[i] = strings.Repeat("─", w)
			}
			r.out = append(r.out, Line{Prefix: first, Spans: []Span{{strings.Join(rule, "─┼─"), r.th.Muted}}})
		}
	}
}

// fitSpans cuts spans to w cells, ending in "…" when it cuts.
func fitSpans(spans []Span, w int) []Span {
	if width(spansText(spans)) <= w {
		return spans
	}
	var out []Span
	left := w - 1
	for _, sp := range spans {
		head, _ := splitAt(sp.Text, left)
		out = append(out, Span{head, sp.Style})
		left -= width(head)
		if left <= 0 || head != sp.Text {
			out = append(out, Span{"…", sp.Style})
			break
		}
	}
	return out
}

func (r *mdRenderer) lines(n ast.Node) []string {
	var out []string
	segs := n.Lines()
	for i := 0; i < segs.Len(); i++ {
		s := segs.At(i)
		out = append(out, strings.TrimRight(string(s.Value(r.src)), "\n"))
	}
	return out
}

// code renders a code block with chroma highlighting. Lines are not wrapped:
// code read with its line breaks moved is harder to read than code cut off
// at the window edge.
func (r *mdRenderer) code(lines []string, lang string, first, rest []Span) {
	src := strings.Join(lines, "\n")
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Analyse(src)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	it, err := lexer.Tokenise(nil, src)
	indent := Span{"  ", r.th.Text}
	cur := Line{Prefix: append(clone(first), indent)}
	if err != nil {
		for _, l := range lines {
			r.out = append(r.out, Line{Prefix: append(clone(first), indent), Spans: []Span{{l, r.th.Subtle}}})
			first = rest
		}
		return
	}
	for tok := it(); tok != chroma.EOF; tok = it() {
		st := r.tokenStyle(tok.Type)
		parts := strings.Split(tok.Value, "\n")
		for i, p := range parts {
			if i > 0 {
				r.out = append(r.out, cur)
				first = rest
				cur = Line{Prefix: append(clone(first), indent)}
			}
			if p != "" {
				cur.Spans = append(cur.Spans, Span{strings.ReplaceAll(p, "\t", "    "), st})
			}
		}
	}
	if len(cur.Spans) > 0 {
		r.out = append(r.out, cur)
	}
}

func (r *mdRenderer) tokenStyle(t chroma.TokenType) tcell.Style {
	if r.th.NoColor {
		return r.th.Base
	}
	e := codeStyle.Get(t)
	st := r.th.Text
	if e.Colour.IsSet() {
		st = r.th.Base.Foreground(tcell.NewRGBColor(int32(e.Colour.Red()), int32(e.Colour.Green()), int32(e.Colour.Blue())))
	}
	if e.Bold == chroma.Yes {
		st = st.Bold(true)
	}
	if e.Italic == chroma.Yes {
		st = st.Italic(true)
	}
	return st
}

// inline flattens the inline children of n into spans.
func (r *mdRenderer) inline(n ast.Node, base tcell.Style) []Span {
	var out []Span
	var walk func(n ast.Node, st tcell.Style)
	walk = func(n ast.Node, st tcell.Style) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			switch c := c.(type) {
			case *ast.Text:
				out = append(out, Span{string(c.Segment.Value(r.src)), st})
				if c.HardLineBreak() {
					out = append(out, Span{"\n", st})
				} else if c.SoftLineBreak() {
					out = append(out, Span{" ", st})
				}
			case *ast.String:
				out = append(out, Span{string(c.Value), st})
			case *ast.CodeSpan:
				out = append(out, Span{string(c.Text(r.src)), r.th.Code})
			case *ast.Emphasis:
				if c.Level >= 2 {
					walk(c, st.Bold(true))
				} else {
					walk(c, st.Italic(true))
				}
			case *east.Strikethrough:
				walk(c, st.StrikeThrough(true))
			case *ast.Link:
				walk(c, r.th.Foam.Underline(true))
			case *ast.AutoLink:
				out = append(out, Span{string(c.URL(r.src)), r.th.Foam.Underline(true)})
			case *ast.Image:
				walk(c, r.th.Subtle)
			case *ast.RawHTML:
				segs := c.Segments
				for i := 0; i < segs.Len(); i++ {
					seg := segs.At(i)
					out = append(out, Span{string(seg.Value(r.src)), r.th.Subtle})
				}
			default:
				walk(c, st)
			}
		}
	}
	walk(n, base)
	return out
}

func clone(s []Span) []Span { return append([]Span(nil), s...) }

func spansText(s []Span) string {
	var b strings.Builder
	for _, sp := range s {
		b.WriteString(sp.Text)
	}
	return b.String()
}
