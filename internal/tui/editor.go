package tui

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Editor holds the markdown source of an artifact while the person edits
// it. The cursor is a source line and a rune offset in it; the window shows
// the lines soft-wrapped and keeps its own row and column, so motions work
// as in any buffer. Edits read the window's position first and set it after.
type Editor struct {
	Name string
	Src  [][]rune
	// Base is the version the edit started from, or the last one saved.
	Base      int
	Line, Off int
	Dirty     bool
	// fromDoc says Insert started on the document: <Esc> shows it again.
	fromDoc bool
	// saved is the text as last loaded or saved: undo back to it is clean.
	saved string

	undo, redo []snapshot
	// reg is what the last delete or yank took, for p and P. lines says it
	// took whole lines.
	reg   string
	lines bool

	rev    int
	cache  []Line
	cacheW int
	cacheR int
}

type snapshot struct {
	src       [][]rune
	line, off int
}

// metaRows are the rows above the source: a blank, the meta line, a blank.
// The cursor does not stop on them.
const metaRows = 3

// pos is a place in the source: a line and a rune offset in it.
type pos struct{ line, off int }

func (p pos) before(q pos) bool { return p.line < q.line || p.line == q.line && p.off < q.off }

func newEditor(name, text string, base int) *Editor {
	e := &Editor{Name: name, Base: base, rev: 1, saved: text}
	for _, l := range strings.Split(text, "\n") {
		e.Src = append(e.Src, []rune(l))
	}
	return e
}

func (e *Editor) Text() string {
	parts := make([]string, len(e.Src))
	for i, l := range e.Src {
		parts[i] = string(l)
	}
	return strings.Join(parts, "\n")
}

func (e *Editor) cursor() pos { return pos{e.Line, e.Off} }

// save remembers the source before a change, for u.
func (e *Editor) save() {
	src := make([][]rune, len(e.Src))
	for i, l := range e.Src {
		src[i] = slices.Clone(l)
	}
	e.undo = append(e.undo, snapshot{src, e.Line, e.Off})
	e.redo = nil
}

func (e *Editor) changed() {
	e.Dirty = true
	e.rev++
}

// step undoes (from undo to redo) or redoes (the other way).
func (e *Editor) step(from, to *[]snapshot) bool {
	if len(*from) == 0 {
		return false
	}
	s := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	*to = append(*to, snapshot{e.Src, e.Line, e.Off})
	e.Src, e.Line, e.Off = s.src, s.line, s.off
	e.changed()
	e.Dirty = e.Text() != e.saved
	return true
}

// text is the source from a up to b.
func (e *Editor) text(a, b pos) string {
	if a.line == b.line {
		return string(e.Src[a.line][a.off:b.off])
	}
	parts := []string{string(e.Src[a.line][a.off:])}
	for l := a.line + 1; l < b.line; l++ {
		parts = append(parts, string(e.Src[l]))
	}
	return strings.Join(append(parts, string(e.Src[b.line][:b.off])), "\n")
}

// remove takes the source from a up to b out, leaves the cursor at a, and
// returns what it took.
func (e *Editor) remove(a, b pos) string {
	if !a.before(b) {
		return ""
	}
	took := e.text(a, b)
	joined := append(slices.Clone(e.Src[a.line][:a.off]), e.Src[b.line][b.off:]...)
	e.Src = slices.Replace(e.Src, a.line, b.line+1, joined)
	e.Line, e.Off = a.line, a.off
	e.changed()
	return took
}

// insertText writes s at the cursor and leaves the cursor after it.
func (e *Editor) insertText(s string) {
	parts := strings.Split(s, "\n")
	l := e.Src[e.Line]
	head, tail := slices.Clone(l[:e.Off]), slices.Clone(l[e.Off:])
	rows := [][]rune{append(head, []rune(parts[0])...)}
	for _, p := range parts[1:] {
		rows = append(rows, []rune(p))
	}
	n := len(rows) - 1
	off := len(rows[n])
	rows[n] = append(rows[n], tail...)
	e.Src = slices.Replace(e.Src, e.Line, e.Line+1, rows...)
	e.Line, e.Off = e.Line+n, off
	e.changed()
}

// listItem is the start of a markdown list item: indent, marker, task box.
var listItem = regexp.MustCompile(`^(\s*)([-*+]|(\d+)([.)]))( \[[ xX]\])? `)

// newLine is <CR> in Insert. After the marker of a list item it starts the
// next item; on an empty item it ends the list, as Markdown editors do.
func (e *Editor) newLine() {
	l := string(e.Src[e.Line])
	m := listItem.FindStringSubmatchIndex(l)
	if m == nil || e.Off < len([]rune(l[:m[1]])) || rule(l) {
		e.insertText("\n")
		return
	}
	if strings.TrimSpace(l[m[1]:]) == "" {
		e.remove(pos{e.Line, 0}, pos{e.Line, len(e.Src[e.Line])})
		return
	}
	marker := l[m[4]:m[5]]
	if m[6] >= 0 {
		n, _ := strconv.Atoi(l[m[6]:m[7]])
		marker = strconv.Itoa(n+1) + l[m[8]:m[9]]
	}
	box := ""
	if m[10] >= 0 {
		box = " [ ]"
	}
	e.insertText("\n" + l[m[2]:m[3]] + marker + box + " ")
}

// rule reports whether l is a thematic break written with list markers, as
// "- - -" or "* * *".
func rule(l string) bool {
	t := strings.ReplaceAll(strings.TrimSpace(l), " ", "")
	return len(t) >= 3 && strings.Count(t, t[:1]) == len(t)
}

func (e *Editor) backspace() {
	switch {
	case e.Off > 0:
		e.remove(pos{e.Line, e.Off - 1}, e.cursor())
	case e.Line > 0:
		e.remove(pos{e.Line - 1, len(e.Src[e.Line-1])}, e.cursor())
	}
}

func (e *Editor) del() {
	switch {
	case e.Off < len(e.Src[e.Line]):
		e.remove(e.cursor(), pos{e.Line, e.Off + 1})
	case e.Line < len(e.Src)-1:
		e.remove(e.cursor(), pos{e.Line + 1, 0})
	}
}

// cut removes runes from..to of the cursor line into the register.
func (e *Editor) cut(from, to int) {
	to = min(to, len(e.Src[e.Line]))
	if from < to {
		e.reg, e.lines = e.remove(pos{e.Line, from}, pos{e.Line, to}), false
	}
}

// cutLines removes lines a..b into the register.
func (e *Editor) cutLines(a, b int) {
	b = min(b, len(e.Src)-1)
	e.yankLines(a, b)
	e.Src = slices.Delete(e.Src, a, b+1)
	if len(e.Src) == 0 {
		e.Src = [][]rune{{}}
	}
	e.Line = min(a, len(e.Src)-1)
	e.Off = firstNonBlank(e.Src[e.Line])
	e.changed()
}

func (e *Editor) yankLines(a, b int) {
	parts := make([]string, 0, b-a+1)
	for _, l := range e.Src[a : b+1] {
		parts = append(parts, string(l))
	}
	e.reg, e.lines = strings.Join(parts, "\n"), true
}

// put pastes the register after the cursor, or before it.
func (e *Editor) put(after bool) {
	if e.reg == "" && !e.lines {
		return
	}
	if e.lines {
		at := e.Line
		if after {
			at++
		}
		var rows [][]rune
		for _, l := range strings.Split(e.reg, "\n") {
			rows = append(rows, []rune(l))
		}
		e.Src = slices.Insert(e.Src, at, rows...)
		e.Line, e.Off = at, firstNonBlank(e.Src[at])
		e.changed()
		return
	}
	if after && len(e.Src[e.Line]) > 0 {
		e.Off++
	}
	e.insertText(e.reg)
	e.Off = max(e.Off-1, 0)
}

// join joins the cursor line with the next, as J.
func (e *Editor) join() {
	if e.Line >= len(e.Src)-1 {
		return
	}
	l := e.Src[e.Line]
	next := strings.TrimLeft(string(e.Src[e.Line+1]), " \t")
	sep := " "
	if len(l) == 0 || l[len(l)-1] == ' ' || next == "" {
		sep = ""
	}
	at := len(l)
	e.Src[e.Line] = append(slices.Clone(l), []rune(sep+next)...)
	e.Src = slices.Delete(e.Src, e.Line+1, e.Line+2)
	e.Off = at
	e.changed()
}

func firstNonBlank(l []rune) int {
	for i, r := range l {
		if !unicode.IsSpace(r) {
			return i
		}
	}
	return 0
}

// --- words, as Vim counts them: letters and digits, or other signs ---

func runeClass(r rune) int {
	switch {
	case unicode.IsSpace(r):
		return 0
	case r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
		return 1
	}
	return 2
}

// wordFwd is w: the start of the next word, or the end of the line.
func wordFwd(rs []rune, i int) int {
	if i >= len(rs) {
		return len(rs)
	}
	if c := runeClass(rs[i]); c != 0 {
		for i < len(rs) && runeClass(rs[i]) == c {
			i++
		}
	}
	for i < len(rs) && runeClass(rs[i]) == 0 {
		i++
	}
	return i
}

// wordBack is b: the start of this word, or of the one before.
func wordBack(rs []rune, i int) int {
	i = min(i, len(rs))
	if i > 0 {
		i--
	}
	for i > 0 && runeClass(rs[i]) == 0 {
		i--
	}
	if i < len(rs) {
		c := runeClass(rs[i])
		for i > 0 && runeClass(rs[i-1]) == c {
			i--
		}
	}
	return i
}

// wordEnd is e: the last rune of this word, or of the next.
func wordEnd(rs []rune, i int) int {
	if i+1 >= len(rs) {
		return max(len(rs)-1, 0)
	}
	i++
	for i < len(rs)-1 && runeClass(rs[i]) == 0 {
		i++
	}
	c := runeClass(rs[i])
	for i+1 < len(rs) && runeClass(rs[i+1]) == c {
		i++
	}
	return i
}

// innerWord is iw: the run of one class around i.
func innerWord(rs []rune, i int) (int, int) {
	if len(rs) == 0 {
		return 0, 0
	}
	i = min(i, len(rs)-1)
	c := runeClass(rs[i])
	a, b := i, i+1
	for a > 0 && runeClass(rs[a-1]) == c {
		a--
	}
	for b < len(rs) && runeClass(rs[b]) == c {
		b++
	}
	return a, b
}

// aroundWord is aw: the word and the spaces after it, else before it.
func aroundWord(rs []rune, i int) (int, int) {
	a, b := innerWord(rs, i)
	if len(rs) == 0 {
		return a, b
	}
	if runeClass(rs[min(i, len(rs)-1)]) == 0 {
		if b < len(rs) {
			c := runeClass(rs[b])
			for b < len(rs) && runeClass(rs[b]) == c {
				b++
			}
		}
		return a, b
	}
	if end := b; end < len(rs) && runeClass(rs[end]) == 0 {
		for b < len(rs) && runeClass(rs[b]) == 0 {
			b++
		}
		return a, b
	}
	for a > 0 && runeClass(rs[a-1]) == 0 {
		a--
	}
	return a, b
}

// span is where an operator's motion reaches on the cursor line, from..to
// with to not included.
func (e *Editor) span(op byte, motion string, n int) (int, int, bool) {
	l, i := e.Src[e.Line], e.Off
	switch motion {
	case "w":
		// cw stops at the end of the word, as Vim does.
		if op == 'c' && i < len(l) && runeClass(l[i]) != 0 {
			_, end := innerWord(l, i)
			for range n - 1 {
				end = wordEnd(l, end-1) + 1
			}
			return i, end, true
		}
		end := i
		for range n {
			end = wordFwd(l, end)
		}
		return i, end, true
	case "e":
		end := i
		for range n {
			end = wordEnd(l, end)
		}
		return i, min(end+1, len(l)), true
	case "b":
		from := i
		for range n {
			from = wordBack(l, from)
		}
		return from, i, true
	case "$":
		return i, len(l), true
	case "0":
		return 0, i, true
	case "^":
		f := firstNonBlank(l)
		return min(f, i), max(f, i), true
	case "iw":
		a, b := innerWord(l, i)
		return a, b, true
	case "aw":
		a, b := aroundWord(l, i)
		return a, b, true
	}
	if motion == "%" {
		j, ok := matchBracket(l, i)
		if !ok {
			return 0, 0, false
		}
		return min(i, j), max(i, j) + 1, true
	}
	if rs := []rune(motion); len(rs) == 2 && strings.ContainsRune("fFtT", rs[0]) {
		j, ok := findRune(l, i, byte(rs[0]), rs[1], max(n, 1), false)
		switch {
		case !ok:
			return 0, 0, false
		case rs[0] == 'f' || rs[0] == 't':
			return i, j + 1, true
		}
		return j, i, true
	}
	if len(motion) == 2 && (motion[0] == 'i' || motion[0] == 'a') {
		if pair, ok := textObjects[motion[1]]; ok {
			var a, b int
			if pair[0] == pair[1] {
				a, b, ok = quoted(l, i, pair[0])
			} else {
				a, b, ok = bracketed(l, i, pair[0], pair[1])
			}
			if !ok {
				return 0, 0, false
			}
			if motion[0] == 'i' {
				return a + 1, b, true
			}
			return a, b + 1, true
		}
	}
	return 0, 0, false
}

// paragraph is ip (around false) or ap over n lines from line i: the run of
// lines that are blank or not, like line i; ap adds the blank lines after
// it, or before it when none follow.
func paragraph(n, i int, around bool, blank func(int) bool) (int, int) {
	run := func(a, b int, want bool) (int, int) {
		for a > 0 && blank(a-1) == want {
			a--
		}
		for b+1 < n && blank(b+1) == want {
			b++
		}
		return a, b
	}
	from, to := run(i, i, blank(i))
	if !around {
		return from, to
	}
	if to+1 < n {
		_, end := run(to+1, to+1, !blank(i))
		return from, end
	}
	if from > 0 {
		start, _ := run(from-1, from-1, !blank(i))
		return start, to
	}
	return from, to
}

// bracketLines is a bracket object whose brackets are on other lines, as
// di{ around a block. Inner of a block whose brackets end and start their
// lines is the whole lines between them, as in Vim.
func (a *App) bracketLines(e *Editor, op byte, motion string) bool {
	pair, ok := textObjects[motion[len(motion)-1]]
	if len(motion) != 2 || motion[0] != 'i' && motion[0] != 'a' || !ok || pair[0] == pair[1] {
		return false
	}
	open, close := pair[0], pair[1]
	var o, c pos
	found := false
	for l, i, depth := e.Line, e.Off, 0; l >= 0 && !found; l-- {
		rs := e.Src[l]
		if l != e.Line {
			i = len(rs) - 1
		} else if i < len(rs) && rs[i] == close {
			i--
		}
		for ; i >= 0; i-- {
			if i >= len(rs) {
				continue
			}
			if rs[i] == close {
				depth++
			} else if rs[i] == open {
				if depth == 0 {
					o, found = pos{l, i}, true
					break
				}
				depth--
			}
		}
	}
	if !found {
		return false
	}
	found = false
	for l, i, depth := o.line, o.off+1, 0; l < len(e.Src) && !found; l, i = l+1, 0 {
		for rs := e.Src[l]; i < len(rs); i++ {
			if rs[i] == open {
				depth++
			} else if rs[i] == close {
				if depth == 0 {
					c, found = pos{l, i}, true
					break
				}
				depth--
			}
		}
	}
	if !found || c.line == o.line {
		return false
	}
	from, to := pos{o.line, o.off + 1}, c
	if motion[0] == 'a' {
		from, to = o, pos{c.line, c.off + 1}
	} else if from.off == len(e.Src[o.line]) && strings.TrimSpace(string(e.Src[c.line][:c.off])) == "" {
		first, last := o.line+1, c.line-1
		switch {
		case op == 'y':
			if first <= last {
				e.yankLines(first, last)
				a.clip(e.reg, last-first+1)
			}
		case first > last:
			return false
		case op == 'd':
			e.save()
			e.cutLines(first, last)
		default:
			e.save()
			e.yankLines(first, last)
			e.Src = slices.Replace(e.Src, first, last+1, []rune{})
			e.Line, e.Off = first, 0
			e.changed()
		}
		return true
	}
	if op == 'y' {
		e.reg, e.lines = e.text(from, to), false
		a.clip(e.reg, to.line-from.line+1)
		return true
	}
	e.save()
	e.reg, e.lines = e.remove(from, to), false
	return true
}

// textObjects are the quote and bracket objects after i or a: the open and
// close rune of each.
var textObjects = map[byte][2]rune{
	'"': {'"', '"'}, '\'': {'\'', '\''}, '`': {'`', '`'},
	'(': {'(', ')'}, ')': {'(', ')'}, 'b': {'(', ')'},
	'[': {'[', ']'}, ']': {'[', ']'},
	'{': {'{', '}'}, '}': {'{', '}'}, 'B': {'{', '}'},
	'<': {'<', '>'}, '>': {'<', '>'},
}

// quoted finds the quotes around i on the line, as Vim pairs them from the
// line start; else the first pair after i.
func quoted(l []rune, i int, q rune) (int, int, bool) {
	var at []int
	for k, r := range l {
		if r == q {
			at = append(at, k)
		}
	}
	for k := 0; k+1 < len(at); k += 2 {
		if i <= at[k+1] {
			return at[k], at[k+1], true
		}
	}
	return 0, 0, false
}

// bracketed finds the open and close bracket around i on the line.
func bracketed(l []rune, i int, open, close rune) (int, int, bool) {
	if len(l) == 0 {
		return 0, 0, false
	}
	i = min(i, len(l)-1)
	a := -1
	if l[i] == close {
		i--
	}
	for depth := 0; i >= 0; i-- {
		if l[i] == close {
			depth++
		} else if l[i] == open {
			if depth == 0 {
				a = i
				break
			}
			depth--
		}
	}
	if a < 0 {
		return 0, 0, false
	}
	for depth, k := 0, a+1; k < len(l); k++ {
		if l[k] == open {
			depth++
		} else if l[k] == close {
			if depth == 0 {
				return a, k, true
			}
			depth--
		}
	}
	return 0, 0, false
}

// render shows the source soft-wrapped to w cells, headings in their colors
// and marked for the outline rail. A meta line on top keeps the text where
// the document had it.
func (e *Editor) render(w int, th *Theme) []Line {
	if e.cacheW == w && e.cacheR == e.rev {
		return e.cache
	}
	margin := []Span{{"  ", th.Text}}
	avail := max(w-width(spansText(margin)), 4)
	meta := fmt.Sprintf("◇ %s · editing v%d", e.Name, e.Base)
	out := []Line{{}, {Prefix: margin, Spans: []Span{{meta, th.Muted}}}, {}}
	fence := false
	for i, l := range e.Src {
		st, head := th.Text, 0
		s := string(l)
		switch {
		case strings.HasPrefix(s, "```"):
			fence = !fence
			st = th.Muted
		case !fence && headingLevel(s) > 0:
			head = headingLevel(s)
			st = headingStyle(th, head)
		}
		starts := wrapRunes(l, avail)
		for j, at := range starts {
			end := len(l)
			if j+1 < len(starts) {
				end = starts[j+1]
			}
			ln := Line{Prefix: margin, Src: i + 1, Off: at}
			if end > at {
				ln.Spans = []Span{{strings.ReplaceAll(string(l[at:end]), "\t", " "), st}}
			}
			if j == 0 {
				ln.Head = head
			}
			out = append(out, ln)
		}
	}
	e.cache, e.cacheW, e.cacheR = out, w, e.rev
	return out
}

// wrapRunes gives the start of each row of l wrapped to w cells, breaking
// after a space where it can. Every rune stays, so offsets map both ways.
func wrapRunes(l []rune, w int) []int {
	starts := []int{0}
	col, start, space := 0, 0, -1
	for i, r := range l {
		cw := runeCells(r)
		if col+cw > w && i > start {
			brk := i
			if space >= start && space+1 < i {
				brk = space + 1
			}
			starts = append(starts, brk)
			start, col, space = brk, 0, -1
			for k := brk; k < i; k++ {
				col += runeCells(l[k])
				if l[k] == ' ' {
					space = k
				}
			}
		}
		if r == ' ' {
			space = i
		}
		col += cw
	}
	return starts
}

func runeCells(r rune) int {
	if r == '\t' {
		return 1
	}
	return max(width(string(r)), 1)
}

// --- the editor in a window ---

// posAt is the source place under a row and column of the window. The meta
// rows on top count as the start of the source.
func (a *App) posAt(w *Window, row, col int) pos {
	e := w.Buf.Edit
	lines := e.render(a.textWidth(w), a.th)
	if row >= len(lines) || lines[row].Src == 0 {
		return pos{}
	}
	l := lines[row]
	src := e.Src[l.Src-1]
	end := len(src)
	if row+1 < len(lines) && lines[row+1].Src == l.Src {
		end = lines[row+1].Off
	}
	off, col := l.Off, col-l.prefixWidth()
	for off < end && col > 0 {
		col -= runeCells(src[off])
		off++
	}
	return pos{l.Src - 1, off}
}

// fromWindow moves the editor's cursor to the window's.
func (a *App) fromWindow(w *Window) {
	p := a.posAt(w, w.Cur, w.Col)
	w.Buf.Edit.Line, w.Buf.Edit.Off = p.line, p.off
}

// toWindow moves the window's cursor to the editor's.
func (a *App) toWindow(w *Window) {
	e := w.Buf.Edit
	e.Line = max(min(e.Line, len(e.Src)-1), 0)
	e.Off = max(min(e.Off, len(e.Src[e.Line])), 0)
	lines := e.render(a.textWidth(w), a.th)
	for i, l := range lines {
		if l.Src != e.Line+1 || l.Off > e.Off {
			continue
		}
		if i+1 < len(lines) && lines[i+1].Src == l.Src && lines[i+1].Off <= e.Off {
			continue
		}
		col := l.prefixWidth()
		for _, r := range e.Src[e.Line][l.Off:e.Off] {
			col += runeCells(r)
		}
		w.Cur, w.Col, w.Follow = i, col, false
		return
	}
}

// startEdit shows the source of the artifact in the window, at the line the
// cursor was reading, and runs an insert key there: i a I A o O, or 0 for
// none (:e).
func (a *App) startEdit(k rune) {
	w, b := a.cur, a.cur.Buf
	if b.Edit == nil {
		bl := b.blocks
		if len(bl) == 0 || bl[0].kind != bDoc {
			return
		}
		lines := b.Lines(a.textWidth(w), a.th, a.spin)
		src := 0
		for i := min(w.Cur, len(lines)-1); i >= 0 && src == 0; i-- {
			src = lines[i].Src
		}
		if b.Held != nil {
			b.Edit, b.Held = b.Held, nil
		} else {
			b.Edit = newEditor(b.Artifact, bl[0].text, bl[0].art.Version)
		}
		b.Edit.Line = min(max(src-1, 0), len(b.Edit.Src)-1)
		b.Edit.fromDoc = k != 0
		b.dirty = true
		// The rendered column says nothing about the source one.
		if k == 'a' {
			k = 'i'
		}
	} else {
		a.fromWindow(w)
	}
	e := b.Edit
	if k == 0 {
		a.toWindow(w)
		return
	}
	e.save()
	switch k {
	case 'a':
		e.Off = min(e.Off+1, len(e.Src[e.Line]))
	case 'I':
		e.Off = firstNonBlank(e.Src[e.Line])
	case 'A':
		e.Off = len(e.Src[e.Line])
	case 'o', 'O':
		if k == 'o' {
			e.Line++
		}
		e.Src = slices.Insert(e.Src, e.Line, []rune{})
		e.Off = 0
		e.changed()
	}
	a.toInsert(w)
}

// toInsert starts Insert mode in the source at the editor's cursor.
func (a *App) toInsert(w *Window) {
	a.mode = modeInsert
	w.vmode = 0
	a.toWindow(w)
}

// stopEdit shows the document again, at the section the cursor was in. An
// unsaved edit is held and shows in the document.
func (a *App) stopEdit(w *Window) {
	b := w.Buf
	target := b.Edit.Line + 1
	if b.Edit.Dirty {
		b.Held = b.Edit
	}
	b.Edit = nil
	b.dirty = true
	a.mode = modeNormal
	best, bestSrc := 0, 0
	for i, l := range b.Lines(a.textWidth(w), a.th, a.spin) {
		if l.Src > bestSrc && l.Src <= target {
			best, bestSrc = i, l.Src
		}
	}
	w.Cur, w.Col = best, 0
}

// dropEdit is :q!: the saved document again, without the edit.
func (a *App) dropEdit() {
	b := a.cur.Buf
	if b.Edit != nil {
		b.Edit.Dirty = false
		a.stopEdit(a.cur)
	}
	b.Held = nil
	b.dirty = true
}

// writeHeld is :w on the document: it saves the held edit and shows the
// document again, with the edit still held if the save failed.
func (a *App) writeHeld(force bool) {
	a.startEdit(0)
	a.saveEdit(force)
	a.stopEdit(a.cur)
}

// yankMotion is y with a motion or text object where there is no source
// (a document, a chat): it yanks from the screen row, and ip ap whole rows.
func (a *App) yankMotion(motion string, n int) {
	w := a.cur
	if motion == "ip" || motion == "ap" {
		lines := w.Buf.Lines(a.textWidth(w), a.th, a.spin)
		if len(lines) == 0 {
			return
		}
		blank := func(i int) bool { return strings.TrimSpace(lines[i].Text()) == "" }
		from, to := paragraph(len(lines), w.Cur, motion == "ap", blank)
		a.yankLines(from, to)
		return
	}
	l := a.line(w)
	text := []rune(l.Text())
	row := &Editor{Src: [][]rune{text}, Off: runeAt(text, max(w.Col-l.prefixWidth(), 0))}
	from, to, ok := row.span('y', motion, max(n, 1))
	if !ok || to <= from {
		return
	}
	a.clip(string(row.Src[0][from:to]), 1)
}

// saveEdit sends the source to the hub as the person's new version.
func (a *App) saveEdit(force bool) bool {
	b := a.cur.Buf
	e := b.Edit
	art, text, err := a.c.EditArtifact(b.Workspace, b.Artifact, e.Text(), e.saved, e.Base, force)
	if err != nil {
		a.notify(err.Error(), a.th.Love)
		return false
	}
	saved := art.Version != e.Base
	merged := text != e.Text()
	if merged {
		// The newer version's changes come in; u goes back to the edit alone.
		e.save()
		e.Src = nil
		for _, l := range strings.Split(text, "\n") {
			e.Src = append(e.Src, []rune(l))
		}
		e.rev++
		a.toWindow(a.cur)
	}
	e.Base, e.Dirty, e.saved = art.Version, false, text
	// The meta line shows the new version; no rev, it is not a change.
	e.cacheW = 0
	a.loadArtifact(b)
	if !saved {
		a.notify("no changes", a.th.Muted)
		return true
	}
	what := "saved"
	if merged {
		what = "merged and saved"
	}
	a.notify(fmt.Sprintf("%s %s v%d · %s sees it with your next message", what, b.Artifact, art.Version, art.Thread), a.th.Subtle)
	return true
}

// editNormal runs a Normal key that changes the source: x D dd p P u <C-r>
// J s S C cc r, and an operator (d c y) with a motion (w e b $ 0 ^ iw aw).
func (a *App) editNormal(k string, n int) {
	w := a.cur
	e := w.Buf.Edit
	if e == nil {
		if k[0] == 'y' {
			a.yankMotion(k[1:], n)
			return
		}
		if w.Buf.Kind == KindArtifact {
			a.notify("i or :e edits the markdown", a.th.Muted)
		}
		return
	}
	a.fromWindow(w)
	n = max(n, 1)
	insert := false
	switch k {
	case "x":
		e.save()
		e.cut(e.Off, e.Off+n)
	case "s":
		e.save()
		e.cut(e.Off, e.Off+n)
		insert = true
	case "D", "C":
		e.save()
		e.cut(e.Off, len(e.Src[e.Line]))
		insert = k == "C"
	case "dd":
		e.save()
		e.cutLines(e.Line, e.Line+n-1)
	case "cc", "S":
		e.save()
		last := min(e.Line+n-1, len(e.Src)-1)
		e.yankLines(e.Line, last)
		e.remove(pos{e.Line, 0}, pos{last, len(e.Src[last])})
		insert = true
	case "yy":
		last := min(e.Line+n-1, len(e.Src)-1)
		e.yankLines(e.Line, last)
		a.clip(e.reg, last-e.Line+1)
	case "dip", "dap", "yip", "yap", "cip", "cap":
		blank := func(i int) bool { return strings.TrimSpace(string(e.Src[i])) == "" }
		from, to := paragraph(len(e.Src), e.Line, k[1] == 'a', blank)
		switch k[0] {
		case 'y':
			e.yankLines(from, to)
			a.clip(e.reg, to-from+1)
			e.Line = from
		case 'd':
			e.save()
			e.cutLines(from, to)
		case 'c':
			e.save()
			e.yankLines(from, to)
			e.Src = slices.Replace(e.Src, from, to+1, []rune{})
			e.Line, e.Off = from, 0
			e.changed()
			insert = true
		}
	case "p", "P":
		e.save()
		for range n {
			e.put(k == "p")
		}
	case "J":
		e.save()
		for range max(n-1, 1) {
			e.join()
		}
	case "u":
		a.noRepeat = true
		if !e.step(&e.undo, &e.redo) {
			a.notify("already at oldest change", a.th.Muted)
		}
	case "<C-r>":
		a.noRepeat = true
		if !e.step(&e.redo, &e.undo) {
			a.notify("already at newest change", a.th.Muted)
		}
	default:
		op, motion := k[0], k[1:]
		from, to, ok := e.span(op, motion, n)
		if !ok {
			if a.bracketLines(e, op, motion) {
				insert = op == 'c'
				break
			}
			return
		}
		switch op {
		case 'y':
			e.reg, e.lines = string(e.Src[e.Line][from:to]), false
			e.Off = from
			a.clip(e.reg, 1)
		case 'd', 'c':
			e.save()
			e.cut(from, to)
			insert = op == 'c'
		}
	}
	if insert {
		a.toInsert(w)
		return
	}
	if len(e.Src[e.Line]) > 0 {
		e.Off = min(e.Off, len(e.Src[e.Line])-1)
	}
	a.toWindow(w)
}

// replace is r: the next n runes become c.
func (a *App) replace(c rune, n int) {
	e := a.cur.Buf.Edit
	a.fromWindow(a.cur)
	l := e.Src[e.Line]
	if e.Off+max(n, 1) > len(l) {
		return
	}
	e.save()
	from := e.Off
	e.remove(pos{e.Line, from}, pos{e.Line, from + max(n, 1)})
	e.insertText(strings.Repeat(string(c), max(n, 1)))
	e.Off = from + max(n, 1) - 1
	a.toWindow(a.cur)
}

// editVisual runs d, x, c or y on the Visual selection in the source.
func (a *App) editVisual(k string) {
	w := a.cur
	e := w.Buf.Edit
	p1, p2 := a.posAt(w, w.vLine, w.vCol), a.posAt(w, w.Cur, w.Col)
	if p2.before(p1) {
		p1, p2 = p2, p1
	}
	linewise := w.vmode == 'V'
	w.vmode = 0
	a.mode = modeNormal
	if linewise {
		e.Line, e.Off = p1.line, 0
		switch k {
		case "y":
			e.yankLines(p1.line, p2.line)
			a.clip(e.reg, p2.line-p1.line+1)
		case "c":
			e.save()
			e.yankLines(p1.line, p2.line)
			e.remove(pos{p1.line, 0}, pos{p2.line, len(e.Src[p2.line])})
			a.toInsert(w)
			return
		default:
			e.save()
			e.cutLines(p1.line, p2.line)
		}
		a.toWindow(w)
		return
	}
	// The selection takes the rune under its end too.
	end := pos{p2.line, p2.off + 1}
	if p2.off >= len(e.Src[p2.line]) {
		end = pos{p2.line, len(e.Src[p2.line])}
		if p2.line+1 < len(e.Src) {
			end = pos{p2.line + 1, 0}
		}
	}
	if k == "y" {
		e.reg, e.lines = e.text(p1, end), false
		e.Line, e.Off = p1.line, p1.off
		a.clip(e.reg, p2.line-p1.line+1)
		a.toWindow(w)
		return
	}
	e.save()
	e.reg, e.lines = e.remove(p1, end), false
	if k == "c" {
		a.toInsert(w)
		return
	}
	if len(e.Src[e.Line]) > 0 {
		e.Off = min(e.Off, len(e.Src[e.Line])-1)
	}
	a.toWindow(w)
}

// editInsert is a key in Insert mode in the source.
func (a *App) editInsert(k string, r rune, isRune bool) {
	w := a.cur
	e := w.Buf.Edit
	switch k {
	case "<Esc>":
		if e.fromDoc {
			a.stopEdit(w)
			return
		}
		a.mode = modeNormal
		e.Off = max(e.Off-1, 0)
	case "<CR>", "<S-CR>", "<C-j>", "<M-\r>":
		if a.pasting {
			e.insertText("\n")
		} else {
			e.newLine()
		}
	case "<BS>":
		e.backspace()
	case "<Del>":
		e.del()
	case "<Left>":
		e.Off = max(e.Off-1, 0)
	case "<Right>":
		e.Off = min(e.Off+1, len(e.Src[e.Line]))
	case "<Up>":
		e.Line = max(e.Line-1, 0)
	case "<Down>":
		e.Line = min(e.Line+1, len(e.Src)-1)
	case "<Home>":
		e.Off = 0
	case "<End>":
		e.Off = len(e.Src[e.Line])
	case "<C-u>":
		e.remove(pos{e.Line, 0}, e.cursor())
	case "<C-w>":
		l := e.Src[e.Line]
		i := e.Off
		for i > 0 && unicode.IsSpace(l[i-1]) {
			i--
		}
		for i > 0 && !unicode.IsSpace(l[i-1]) {
			i--
		}
		e.remove(pos{e.Line, i}, e.cursor())
	case "<Tab>":
		e.insertText("  ")
	default:
		if isRune {
			e.insertText(string(r))
		}
	}
	// A paste comes as one key per rune; the cursor follows once it ends.
	if !a.pasting {
		a.toWindow(w)
	}
}
