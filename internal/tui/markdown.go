package tui

import (
	"fmt"
	"regexp"
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

// docWidth is the widest text column of an artifact: long lines are hard to
// read.
const docWidth = 96

type mdRenderer struct {
	src   []byte
	th    *Theme
	w     int
	out   []Line
	doc   bool
	depth int
	// strip is an alert marker, like [!NOTE], to drop from the next
	// paragraph.
	strip string
	// refs are a document's link reference definitions, [1]: url, given to
	// each line it parses alone.
	refs string
}

// alerts are GitHub's blockquote alerts: > [!NOTE] and the like.
var alerts = map[string]string{"NOTE": "note", "TIP": "tip", "IMPORTANT": "important", "WARNING": "warning", "CAUTION": "caution"}

func (r *mdRenderer) alertStyle(kind string) tcell.Style {
	switch kind {
	case "TIP":
		return r.th.Pine
	case "IMPORTANT":
		return r.th.Iris
	case "WARNING":
		return r.th.Gold
	case "CAUTION":
		return r.th.Love
	}
	return r.th.Foam
}

// headingStyle gives each level of a document its own color.
func headingStyle(th *Theme, level int) tcell.Style {
	switch level {
	case 1:
		return th.Iris.Bold(true)
	case 2:
		return th.Foam.Bold(true)
	case 3:
		return th.Rose.Bold(true)
	}
	return th.Subtle.Bold(true)
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
		spans := r.inline(n, r.th.Text)
		if r.strip != "" {
			spans = trimSpans(spans, r.strip)
			r.strip = ""
		}
		r.out = append(r.out, wrap(spans, r.w, first, rest)...)
	case *ast.Heading:
		r.out = append(r.out, wrap(r.inline(n, r.th.Bold), r.w, first, rest)...)
	case *ast.FencedCodeBlock:
		lang := string(n.Language(r.src))
		r.code(r.lines(n), lang, first, rest)
	case *ast.CodeBlock:
		r.code(r.lines(n), "", first, rest)
	case *ast.Blockquote:
		bar := []Span{{"▎ ", r.th.Muted}}
		if kind := r.alert(n); kind != "" {
			st := r.alertStyle(kind)
			bar = []Span{{"▎ ", st}}
			r.out = append(r.out, Line{Prefix: append(clone(first), bar...), Spans: []Span{{alerts[kind], st.Bold(true)}}})
			first = rest
			r.strip = "[!" + kind + "]"
		}
		r.blocks(n, append(clone(first), bar...), append(clone(rest), bar...))
	case *ast.List:
		i := n.Start
		if i == 0 {
			i = 1
		}
		// Numbers line up on the dot: " 9." above "10.".
		numW := len(strconv.Itoa(i + n.ChildCount() - 1))
		glyph := []string{"• ", "◦ ", "▪ "}[r.depth%3]
		r.depth++
		defer func() { r.depth-- }()
		for item := n.FirstChild(); item != nil; item = item.NextSibling() {
			bullet := glyph
			if n.IsOrdered() {
				bullet = fmt.Sprintf("%*d. ", numW, i)
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
		rule := min(r.w-width(spansText(first)), 40)
		r.out = append(r.out, Line{Prefix: first, Spans: []Span{{strings.Repeat("─", max(rule, 1)), r.th.Muted}}})
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
// window, the widest column shrinks first and its cells wrap.
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
	fitColumns(widths, r.w-width(spansText(first)))
	r.wrappedRows(rows, widths, aligns, first, rest)
}

// fitColumns shrinks the widest column until the table fits in avail cells
// with its separators.
func fitColumns(widths []int, avail int) {
	avail -= 3 * (len(widths) - 1)
	for {
		total, widest := 0, 0
		for i, w := range widths {
			total += w
			if w > widths[widest] {
				widest = i
			}
		}
		if total <= avail || widths[widest] <= 3 {
			return
		}
		widths[widest]--
	}
}

// wrappedRows draws table rows with long cells wrapped onto more lines, so
// every word shows: a chat cannot scroll sideways.
func (r *mdRenderer) wrappedRows(rows [][][]Span, widths []int, aligns []east.Alignment, first, rest []Span) {
	for ri, cells := range rows {
		r.out = append(r.out, r.tableRow(cells, widths, aligns, first, rest)...)
		first = rest
		if ri == 0 {
			r.out = append(r.out, tableRule(r.th, widths, rest))
		}
	}
}

// tableRow is one table row, its long cells wrapped onto more lines.
func (r *mdRenderer) tableRow(cells [][]Span, widths []int, aligns []east.Alignment, first, rest []Span) []Line {
	cols := make([][]Line, len(widths))
	height := 1
	for i, w := range widths {
		if i < len(cells) {
			cols[i] = wrap(cells[i], w, nil, nil)
		}
		height = max(height, len(cols[i]))
	}
	var out []Line
	for k := 0; k < height; k++ {
		var spans []Span
		for i, w := range widths {
			if i > 0 {
				spans = append(spans, Span{" │ ", r.th.Muted})
			}
			var c []Span
			if k < len(cols[i]) {
				c = cols[i][k].Spans
			}
			gap := Span{strings.Repeat(" ", max(w-width(spansText(c)), 0)), r.th.Text}
			switch {
			case aligns[i] == east.AlignRight:
				spans = append(append(spans, gap), c...)
			case i == len(widths)-1:
				spans = append(spans, c...)
			default:
				spans = append(append(spans, c...), gap)
			}
		}
		out = append(out, Line{Prefix: first, Spans: spans})
		first = rest
	}
	return out
}

// tableRule is the line under a table's header.
func tableRule(th *Theme, widths []int, prefix []Span) Line {
	rule := make([]string, len(widths))
	for i, w := range widths {
		rule[i] = strings.Repeat("─", w)
	}
	return Line{Prefix: prefix, Spans: []Span{{strings.Join(rule, "─┼─"), th.Muted}}}
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
	if r.doc {
		// No background is painted, so a gutter marks where code starts and
		// ends.
		indent = Span{"│ ", r.th.Muted}
		if lang != "" {
			r.out = append(r.out, Line{Prefix: first, Spans: []Span{{lang, r.th.Muted.Italic(true)}}})
			first = rest
		}
	}
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
				walk(c, linkStyle(r.th, string(c.Destination)))
			case *east.TaskCheckBox:
				if c.IsChecked {
					out = append(out, Span{"☑ ", r.th.Pine})
				} else {
					out = append(out, Span{"☐ ", r.th.Muted})
				}
			case *ast.AutoLink:
				out = append(out, Span{string(c.URL(r.src)), linkStyle(r.th, string(c.URL(r.src)))})
			case *ast.Image:
				out = append(out, Span{"image ", r.th.Muted})
				walk(c, linkStyle(r.th, string(c.Destination)))
			case *ast.RawHTML:
				segs := c.Segments
				for i := 0; i < segs.Len(); i++ {
					at := segs.At(i)
					seg := string(at.Value(r.src))
					// Agents break lines in table cells with <br>.
					if lineBreak.MatchString(seg) {
						out = append(out, Span{"\n", st})
						continue
					}
					out = append(out, Span{seg, r.th.Subtle})
				}
			default:
				walk(c, st)
			}
		}
	}
	walk(n, base)
	return out
}

// lineBreak is an HTML line break: <br>, <br/>, <br />.
var lineBreak = regexp.MustCompile(`(?i)^<br\s*/?>$`)

// linkStyle is the style of a link's text. It carries the URL, so the
// terminal opens it on a click and gx finds it.
func linkStyle(th *Theme, url string) tcell.Style {
	return th.Foam.Underline(true).Url(url)
}

// alert returns the kind of a GitHub alert quote, or "".
func (r *mdRenderer) alert(n *ast.Blockquote) string {
	p := n.FirstChild()
	if p == nil {
		return ""
	}
	if _, ok := p.(*ast.Paragraph); !ok {
		return ""
	}
	t := spansText(r.inline(p, r.th.Text))
	for kind := range alerts {
		if strings.HasPrefix(t, "[!"+kind+"]") {
			return kind
		}
	}
	return ""
}

// trimSpans drops prefix from the start of spans, and the spaces after it.
func trimSpans(spans []Span, prefix string) []Span {
	var out []Span
	for _, sp := range spans {
		if prefix != "" {
			n := min(len(prefix), len(sp.Text))
			if sp.Text[:n] != prefix[:n] {
				prefix = ""
			} else {
				sp.Text, prefix = sp.Text[n:], prefix[n:]
			}
		}
		if prefix == "" && len(out) == 0 {
			sp.Text = strings.TrimLeft(sp.Text, " \n")
		}
		if sp.Text != "" || len(out) > 0 {
			out = append(out, sp)
		}
	}
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
