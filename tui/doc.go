package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// decorate renders an artifact's markdown line by line, as
// render-markdown.nvim does in Neovim: every source line keeps its own rows,
// so the cursor is always on a real line of the source. Markers are hidden
// and blocks are drawn: headings in color with a rule, bullets and task
// boxes, quotes, tables in columns, code highlighted. rows[i] are the rows
// of source line i, at most w cells wide. memo keeps the rows of plain lines
// between calls.
func decorate(src [][]rune, w int, th *Theme, memo map[string][]Line) [][]Line {
	r := &mdRenderer{th: th, w: w, doc: true}
	margin := []Span{{"  ", th.Text}}
	lines := make([]string, len(src))
	fence := false
	for i, l := range src {
		lines[i] = strings.ReplaceAll(string(l), "\t", "    ")
		if fenceOpen.MatchString(lines[i]) {
			fence = !fence
		} else if !fence && refURL.MatchString(lines[i]) {
			r.refs += "\n" + lines[i]
		}
	}
	rows := make([][]Line, len(lines))
	numW := numberWidths(lines)
	alert := ""
	for i := 0; i < len(lines); i++ {
		if f := fenceOpen.FindStringSubmatch(lines[i]); f != nil {
			i = r.fenced(lines, i, f[1], f[2], margin, rows)
			continue
		}
		if i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|") && tableDelim.MatchString(lines[i+1]) {
			i = r.tableLines(lines, i, margin, rows)
			continue
		}
		key := fmt.Sprintf("%d\x00%d\x00%s\x00%s\x00%s", w, numW[i], alert, lines[i], r.refs)
		if m, ok := memo[key]; ok {
			rows[i] = m
			alert = r.quoteAlert(lines[i], alert)
			continue
		}
		rows[i] = r.docLine(lines[i], margin, &alert, numW[i])
		memo[key] = rows[i]
	}
	return rows
}

var (
	fenceOpen  = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})\\s*([^`\\s]*)")
	tableDelim = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$`)
	quoteLine  = regexp.MustCompile(`^ {0,3}((?:>\s?)+)(.*)$`)
	alertLine  = regexp.MustCompile(`^\[!(\w+)\]\s*$`)
	headLine   = regexp.MustCompile(`^ {0,3}(#{1,6})(?:\s+(.*?))?(?:\s+#+)?\s*$`)
	ruleLine   = regexp.MustCompile(`^ {0,3}([-*_])(?:\s*[-*_]){2,}\s*$`)
	itemLine   = regexp.MustCompile(`^(\s*)([-*+]|\d{1,9}[.)])(?:\s+(.*))?$`)
)

// numberWidths is, for each item of a numbered list, the width of the
// list's widest number, so the numbers line up on the dot: " 9." above
// "10.".
func numberWidths(lines []string) []int {
	out := make([]int, len(lines))
	numbered := func(i int) []string {
		m := itemLine.FindStringSubmatch(lines[i])
		if m == nil || !strings.ContainsAny(m[2][len(m[2])-1:], ".)") {
			return nil
		}
		return m
	}
	for i := 0; i < len(lines); {
		m := numbered(i)
		if m == nil {
			i++
			continue
		}
		j, widest := i, 0
		for ; j < len(lines); j++ {
			n := numbered(j)
			if n == nil || n[1] != m[1] {
				break
			}
			widest = max(widest, len(n[2]))
		}
		for k := i; k < j; k++ {
			out[k] = widest
		}
		i = j
	}
	return out
}

// quoteAlert is the alert a quote line opens or goes on with, as
// > [!NOTE]; "" once the quote ends.
func (r *mdRenderer) quoteAlert(s, alert string) string {
	m := quoteLine.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	if a := alertLine.FindStringSubmatch(m[2]); a != nil && alerts[a[1]] != "" {
		return a[1]
	}
	return alert
}

// docLine renders one line that is not in a code block or a table.
func (r *mdRenderer) docLine(s string, margin []Span, alert *string, numW int) []Line {
	if strings.TrimSpace(s) == "" {
		*alert = ""
		return []Line{{Prefix: margin}}
	}
	m := quoteLine.FindStringSubmatch(s)
	if m == nil {
		*alert = ""
		return r.body(s, margin, margin, numW)
	}
	*alert = r.quoteAlert(s, *alert)
	st := r.th.Muted
	if *alert != "" {
		st = r.alertStyle(*alert)
	}
	bars := clone(margin)
	for range strings.Count(m[1], ">") {
		bars = append(bars, Span{"▎ ", st})
	}
	if alertLine.MatchString(m[2]) && *alert != "" {
		return []Line{{Prefix: bars, Spans: []Span{{alerts[*alert], st.Bold(true)}}}}
	}
	return r.body(m[2], bars, bars, 0)
}

// body renders a line's content after its quote bars: a heading, a rule, a
// list item or a paragraph line. numW is the width of a numbered item's
// number.
func (r *mdRenderer) body(s string, first, rest []Span, numW int) []Line {
	if h := headLine.FindStringSubmatch(s); h != nil {
		level := len(h[1])
		out := r.wrapped(r.inlineOf(h[2], headingStyle(r.th, level)), first, rest)
		out[0].Msg, out[0].Head = true, level
		if level <= 2 {
			rule := "─"
			if level == 1 {
				rule = "━"
			}
			// Off -1: the rule is not text of the line.
			out = append(out, Line{Prefix: rest, Off: -1, Spans: []Span{{strings.Repeat(rule, max(r.w-width(spansText(rest)), 1)), r.th.Muted}}})
		}
		return out
	}
	if refURL.MatchString(s) {
		return r.wrapped([]Span{{s, r.th.Muted}}, first, rest)
	}
	if ruleLine.MatchString(s) {
		return []Line{{Prefix: first, Spans: []Span{{strings.Repeat("─", max(r.w-width(spansText(first)), 1)), r.th.Muted}}}}
	}
	if m := itemLine.FindStringSubmatch(s); m != nil {
		indent := Span{m[1], r.th.Text}
		bullet, content := fmt.Sprintf("%*s ", numW, m[2]), m[3]
		if !strings.ContainsAny(m[2][len(m[2])-1:], ".)") {
			bullet = []string{"• ", "◦ ", "▪ "}[len(m[1])/2%3]
		}
		box := Span{}
		switch {
		case strings.HasPrefix(content, "[ ] "):
			box, content = Span{"☐ ", r.th.Muted}, content[4:]
		case strings.HasPrefix(content, "[x] "), strings.HasPrefix(content, "[X] "):
			box, content = Span{"☑ ", r.th.Pine}, content[4:]
		}
		// A task's box is its bullet, as on GitHub.
		lead := Span{bullet, r.th.Subtle}
		if box.Text != "" {
			lead = box
		}
		return r.wrapped(r.inlineOf(content, r.th.Text),
			append(clone(first), indent, lead),
			append(clone(rest), indent, Span{strings.Repeat(" ", width(lead.Text)), r.th.Text}))
	}
	trimmed := strings.TrimLeft(s, " ")
	if lead := s[:len(s)-len(trimmed)]; lead != "" {
		first = append(clone(first), Span{lead, r.th.Text})
		rest = append(clone(rest), Span{lead, r.th.Text})
	}
	return r.wrapped(r.inlineOf(trimmed, r.th.Text), first, rest)
}

// wrapped wraps spans to the document's width, as one row at least.
func (r *mdRenderer) wrapped(spans []Span, first, rest []Span) []Line {
	if out := wrap(spans, r.w, first, rest); len(out) > 0 {
		return out
	}
	return []Line{{Prefix: first}}
}

// inlineOf renders the inline markdown of one line's content: emphasis,
// code, links. Content that does not read as one paragraph shows as it is.
func (r *mdRenderer) inlineOf(s string, st tcell.Style) []Span {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	src := []byte(s + "\n" + r.refs)
	p := md.Parser().Parse(text.NewReader(src)).FirstChild()
	next := p.NextSibling()
	for ; next != nil; next = next.NextSibling() {
		if _, ok := next.(*ast.LinkReferenceDefinition); !ok {
			break
		}
	}
	if _, ok := p.(*ast.Paragraph); !ok || next != nil {
		return []Span{{s, st}}
	}
	saved := r.src
	r.src = src
	defer func() { r.src = saved }()
	return r.inline(p, st)
}

// fenced renders a code block from its opening fence at i: the language on
// the fence's row, the code highlighted, a blank row for the closing fence.
// It returns the index of the block's last line.
func (r *mdRenderer) fenced(lines []string, i int, marker, lang string, margin []Span, rows [][]Line) int {
	end := len(lines)
	for j := i + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if strings.HasPrefix(t, marker) && strings.Trim(t, marker[:1]) == "" {
			end = j
			break
		}
	}
	body := lines[i+1 : end]
	r.out = nil
	r.code(body, lang, margin, margin)
	code := r.out
	r.out = nil
	rows[i] = []Line{{Prefix: margin}}
	if lang != "" && len(code) > 0 {
		rows[i], code = code[:1], code[1:]
	}
	gutter := append(clone(margin), Span{"│ ", r.th.Muted})
	for k := range body {
		if k < len(code) {
			rows[i+1+k] = code[k : k+1]
		} else {
			rows[i+1+k] = []Line{{Prefix: gutter}}
		}
	}
	if end < len(lines) {
		rows[end] = []Line{{Prefix: margin}}
		return end
	}
	return len(lines) - 1
}

// tableLines renders a table from its header at i: cells in columns, the
// line under the header for the delimiter row. It returns the index of the
// table's last line.
func (r *mdRenderer) tableLines(lines []string, i int, margin []Span, rows [][]Line) int {
	end := i + 2
	for end < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[end]), "|") {
		end++
	}
	var aligns []east.Alignment
	for _, c := range tableCells(lines[i+1]) {
		left, right := strings.HasPrefix(c, ":"), strings.HasSuffix(c, ":")
		switch {
		case left && right:
			aligns = append(aligns, east.AlignCenter)
		case right:
			aligns = append(aligns, east.AlignRight)
		case left:
			aligns = append(aligns, east.AlignLeft)
		default:
			aligns = append(aligns, east.AlignNone)
		}
	}
	cells := map[int][][]Span{}
	widths := make([]int, len(aligns))
	for j := i; j < end; j++ {
		if j == i+1 {
			continue
		}
		st := r.th.Text
		if j == i {
			st = r.th.Bold
		}
		for k, c := range tableCells(lines[j]) {
			spans := r.inlineOf(c, st)
			cells[j] = append(cells[j], spans)
			if k < len(widths) {
				widths[k] = max(widths[k], width(spansText(spans)))
			}
		}
	}
	fitColumns(widths, r.w-width(spansText(margin)))
	for j := i; j < end; j++ {
		if j == i+1 {
			rows[j] = []Line{tableRule(r.th, widths, margin)}
			continue
		}
		rows[j] = r.tableRow(cells[j], widths, aligns, margin, margin)
	}
	return end - 1
}

// tableCells splits a table row into its cells, without the outer bars.
func tableCells(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "|")
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, `\|`) {
		s = s[:len(s)-1]
	}
	var out []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == '|':
			cur.WriteByte('|')
			i++
		case s[i] == '|':
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(s[i])
		}
	}
	return append(out, strings.TrimSpace(cur.String()))
}
