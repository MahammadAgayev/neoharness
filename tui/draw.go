package tui

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
	lua "github.com/yuin/gopher-lua"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// scrollOff keeps this many lines visible around the cursor.
const scrollOff = 8

// margin keeps the windows off the terminal's edges: this many columns at
// the left and right.
const margin = 2

// chromeRows are the rows on top of each pane, over its text: none, as in
// Vim. A pane's status line is its last row.
const chromeRows = 0

// The prompt grows to maxPromptLines before it scrolls.
const maxPromptLines = 12

func (a *App) draw() {
	s := a.s
	s.Clear()
	W, H := s.Size()
	if W < 20 || H < 6 {
		a.put(0, 0, W, "neoharness: window too small", a.th.Alert)
		s.Show()
		return
	}
	a.cursorX, a.cursorY = -1, -1
	if a.cur.Buf.Kind == KindChat {
		a.lastChat = a.cur
	}
	now := a.now()
	a.spin = phase(now, spinEvery)
	sep := func(x, y, h int) {
		for i := 0; i < h; i++ {
			s.SetContent(x, y+i, '│', nil, a.th.Separator)
		}
	}
	live := false
	// A blank row on top keeps the panes off the terminal's edge; the last
	// row is the command line.
	area := Rect{margin, 1, W - 2*margin, H - 2}
	a.root.layout(area, sep)
	// Which threads are on screen changes with every window command, so the
	// list is redrawn here rather than after each of them. Its rows fit the
	// width the layout gives.
	a.renderThreads()
	// Its running glyphs pulse and its times age.
	if a.workspacesShown() {
		a.renderWorkspaces()
	}
	if a.waitsBuf != nil {
		a.renderWaits()
	}
	a.bindDiff()
	for _, w := range a.root.windows() {
		a.drawWindow(w, w == a.cur)
		live = live || w.Buf.Live()
	}
	// The lead window may have scrolled while it was drawn.
	for _, w := range a.bindDiff() {
		a.drawWindow(w, false)
	}
	a.drawToasts(W, H)
	if a.picker != nil {
		a.drawPicker(W, H)
	}
	a.drawGlobal(W, H)

	if a.cursorX >= 0 {
		s.ShowCursor(a.cursorX, a.cursorY)
	} else {
		s.HideCursor()
	}
	// The cursor is a block in Normal and Visual, and a line in Insert, as
	// in Vim.
	s.bar = a.mode == modeInsert
	s.Show()
	a.frames++
	a.lastFrame, a.dirty = now, false
	a.see(now)
	a.scheduleNext(now, live)
}

// put draws text from x, clipped at maxX, and returns where it stopped.
func (a *App) put(x, y, maxX int, text string, st tcell.Style) int {
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		gw := g.Width()
		if gw == 0 {
			continue
		}
		if x+gw > maxX {
			break
		}
		r := g.Runes()
		a.s.SetContent(x, y, r[0], r[1:], st)
		x += gw
	}
	return x
}

func (a *App) fill(x, y, maxX int, st tcell.Style) {
	for ; x < maxX; x++ {
		a.s.SetContent(x, y, ' ', nil, st)
	}
}

// promptGap is the blank row between a chat's prompt and the status line
// under it, so the line you write is not pressed between two others.
const promptGap = 1

func (a *App) drawWindow(w *Window, focused bool) {
	r := w.rect
	if r.W < 4 || r.H < chromeRows+3 {
		return
	}
	b := w.Buf
	right := r.X + r.W
	top := r.Y + chromeRows
	bottom := r.Y + r.H - 1
	a.drawStatus(w, bottom, focused)

	// The prompt is the last rows of a chat window, under the stream.
	var promptLines []Line
	promptTop, promptCur, promptFirst := bottom, -1, 0
	if b.Prompt != nil {
		promptLines, promptCur, promptFirst = a.promptLines(w, focused)
		promptTop = bottom - promptGap - len(promptLines)
	}
	textBottom := promptTop
	// The recap is the top of the prompt dock, the end of the chat's lines.
	b.SetRecap(a.recapText(w))
	// The transcript ends above the card, so the card hides none of it. A
	// blank row parts the card from the chat above and the prompt below.
	cardBottom := textBottom - 1
	if title, body, _ := a.card(w); title != "" && textBottom-len(body)-2 > top {
		textBottom -= len(body) + 2
	} else if b.Prompt != nil {
		// A blank row parts the prompt from the chat, or from the recap and
		// the todo line, which bring their own blank row above them.
		textBottom--
	}
	w.recapShown = b.recap != ""
	h := max(textBottom-top, 1)
	w.textH = h

	lines := b.Lines(a.textWidth(w), a.th, a.spin)
	n := len(lines)
	w.linesN = n
	rail := railWidth(w)
	// With the cursor in the prompt, the transcript keeps its own place.
	cur := &w.Cur
	if w.inPrompt {
		cur = &w.chatCur
	}
	// A selection in the transcript holds it still: new lines would move its
	// end and scroll the window under the person.
	follow := w.Follow && (w.vmode == 0 || w.inPrompt)
	if n == 0 {
		*cur = 0
	} else if follow || *cur >= n {
		*cur = n - 1
	}
	off := min(scrollOff, h/2)
	if *cur < w.Top+off {
		w.Top = max(*cur-off, 0)
	}
	if *cur >= w.Top+h-off {
		w.Top = *cur - h + off + 1
	}
	w.Top = max(min(w.Top, n-h), 0)
	if follow {
		w.Top = max(n-h, 0)
	}
	// The recap and the todo line stay on the prompt, as one dock, when the
	// chat is too short to fill the window.
	w.dockFrom, w.dockGap = n, 0
	for i := n - 1; i >= 0 && (lines[i].Item == recapRow || lines[i].Item == todoRow); i-- {
		w.dockFrom = i
	}
	if rows := n - w.Top; rows < h && w.dockFrom < n {
		w.dockGap = h - rows
	}

	editing := b.Edit != nil && focused && a.mode == modeInsert
	showCursor := focused && !w.inPrompt && (a.mode == modeNormal || a.mode == modeVisual) || editing
	vl1, vl2 := -1, -1
	if w.vmode != 0 && focused {
		vl1, vl2 = min(w.vLine, w.Cur), max(w.vLine, w.Cur)
	}
	for i := 0; i < h && w.Top+i < n; i++ {
		li := w.Top + i
		y := top + i
		if li >= w.dockFrom {
			y += w.dockGap
		}
		l := lines[li]
		bg := tcell.Style{}
		hasBG := false
		if showCursor && li == w.Cur && w.vmode == 0 || l.Tint {
			bg, hasBG = a.th.CursorLine, true
		}
		x := r.X + rail
		col := 0
		cut := false
		spans := l.Spans
		if w.Buf.Kind == KindChat {
			spans = a.linkify(spans)
		}
		matches := a.matchCols(w, li, l)
		for _, sp := range append(clone(l.Prefix), spans...) {
			g := uniseg.NewGraphemes(sp.Text)
			for g.Next() {
				gw := g.Width()
				if gw == 0 {
					continue
				}
				if x+gw > right {
					cut = true
					break
				}
				st := sp.Style
				if hasBG {
					st = overlay(st, bg)
				}
				if li >= vl1 && li <= vl2 && a.selected(w, li, col, l) {
					st = overlay(st, a.th.Selection)
				}
				for _, m := range matches {
					switch {
					case col < m[0] || col >= m[1]:
					case li == w.Cur && m[0] == w.Col:
						st = a.th.CurSearch
					default:
						st = overlay(st, a.th.Search)
					}
				}
				rs := g.Runes()
				a.s.SetContent(x, y, rs[0], rs[1:], st)
				x += gw
				col += gw
			}
		}
		if hasBG {
			a.fill(x, y, right, a.th.CursorLine)
		} else if li >= vl1 && li <= vl2 && w.vmode == 'V' {
			a.fill(x, y, right, a.th.Selection)
		}
		// A row that does not fit says so, as Neovim's listchars extends.
		if cut {
			st := a.th.Fg3
			if hasBG {
				st = overlay(st, bg)
			}
			a.put(right-1, y, right, "…", st)
		}
		if showCursor && li == w.Cur {
			a.cursorX, a.cursorY = r.X+rail+min(w.Col, r.W-rail-1), y
			// The caret of the prompt: you type here.
			if editing {
				a.put(r.X+rail, y, right, "›", overlay(a.th.Accent, a.th.CursorLine))
			}
		}
	}
	if showCursor && n == 0 && b.Prompt == nil {
		a.cursorX, a.cursorY = r.X+rail, top
	}
	if rail > 0 {
		a.drawRail(w, lines, top, h)
	}

	if b.Prompt != nil {
		for i, l := range promptLines {
			if i == promptCur {
				a.fill(r.X, promptTop+i, right, a.th.CursorLine)
				l.Prefix, l.Spans = clone(l.Prefix), clone(l.Spans)
				for k := range l.Prefix {
					l.Prefix[k].Style = overlay(l.Prefix[k].Style, a.th.CursorLine)
				}
				for k := range l.Spans {
					l.Spans[k].Style = overlay(l.Spans[k].Style, a.th.CursorLine)
				}
			}
			if w.inPrompt && w.vmode != 0 && focused {
				row := promptFirst + i
				a.drawCells(r.X, promptTop+i, right, l, func(col int) bool {
					return row >= min(w.vLine, w.Cur) && row <= max(w.vLine, w.Cur) && a.selected(w, row, col, l)
				})
				continue
			}
			a.drawLine(r.X, promptTop+i, right, l)
		}
	}
	if b.Kind == KindChat {
		a.drawFloats(w, top, cardBottom)
	}
	if focused && a.menu != nil {
		a.drawMenu(w, top, promptTop)
	}
}

// recapText is the recap a chat's dock shows; "" when it does not show.
func (a *App) recapText(w *Window) string {
	v, ok := a.threads[w.Buf.Thread]
	if w.Buf.Kind != KindChat || !ok || !a.recapOpen[a.recapKey(v.Name)] || v.State == model.StateRunning {
		return ""
	}
	return v.Recap
}

// railWidth is the left gutter of an artifact window, where the outline rail
// goes. Narrow windows keep their columns for the text.
func railWidth(w *Window) int {
	if w.Buf.Kind != KindArtifact || w.rect.W < 60 || w.Buf.Edit != nil && w.Buf.Edit.diff != nil {
		return 0
	}
	return 5
}

// drawRail draws the outline of a document in its gutter: a dash per heading,
// longer for higher levels, the cursor's section heavier and brighter.
func (a *App) drawRail(w *Window, lines []Line, top, h int) {
	var heads []int
	cur := 0
	for i, l := range lines {
		if l.Head == 0 {
			continue
		}
		if i <= w.Cur {
			cur = len(heads)
		}
		heads = append(heads, i)
	}
	if len(heads) < 2 {
		return
	}
	rows := min(len(heads), h)
	first := max(min(cur-rows/2, len(heads)-rows), 0)
	y := top + (h-rows)/2
	for i := first; i < first+rows; i++ {
		dash, st := "─", a.th.Fg3
		if i == cur {
			dash, st = "━", a.th.Fg2
		}
		a.put(w.rect.X+1, y+i-first, w.rect.X+railWidth(w), strings.Repeat(dash, max(4-lines[heads[i]].Head, 1)), st)
	}
}

// drawMenu draws the completion popup just above the prompt.
func (a *App) drawMenu(w *Window, top, bottom int) {
	m := a.menu
	r := w.rect
	rows := min(len(m.items), menuRows, bottom-top-2)
	if rows < 1 {
		return
	}
	bw := 0
	for _, it := range m.items {
		bw = max(bw, width(it.glyph.Text+it.label))
	}
	bw = min(bw+4, r.W-4, 64)
	if bw < 12 {
		return
	}
	x0, y0 := r.X+2, bottom-rows-2
	th := a.th
	a.box(x0, y0, bw, rows+2, "", th.Accent)
	start := max(m.sel-rows+1, 0)
	for i := 0; i < rows && start+i < len(m.items); i++ {
		y := y0 + 1 + i
		it := m.items[start+i]
		glyphSt, st := it.glyph.Style, th.Fg2
		if start+i == m.sel {
			a.fill(x0+1, y, x0+bw-1, th.CursorLine)
			glyphSt, st = overlay(glyphSt, th.CursorLine), overlay(th.Fg.Bold(true), th.CursorLine)
		}
		x := a.put(x0+2, y, x0+bw-2, it.glyph.Text, glyphSt)
		a.put(x, y, x0+bw-2, truncate(it.label, x0+bw-2-x), st)
	}
}

func (a *App) selected(w *Window, li, col int, l Line) bool {
	if w.vmode == 'V' {
		return true
	}
	l1, c1, l2, c2 := w.vLine, w.vCol, w.Cur, w.Col
	if l2 < l1 || l2 == l1 && c2 < c1 {
		l1, c1, l2, c2 = l2, c2, l1, c1
	}
	if li == l1 && col < c1 {
		return false
	}
	if li == l2 && col > c2 {
		return false
	}
	return true
}

// matchCols is where the search being typed matches row li of w, as
// columns from the start of the row, its prefix included.
func (a *App) matchCols(w *Window, li int, l Line) [][2]int {
	if a.mode != modeCommand || a.inc == nil || a.inc.w != w || a.inc.re == nil {
		return nil
	}
	text, pw := l.Text(), l.prefixWidth()
	var cols [][2]int
	for _, m := range a.inc.re.FindAllStringIndex(text, -1) {
		if m[1] > m[0] {
			cols = append(cols, [2]int{pw + width(text[:m[0]]), pw + width(text[:m[1]])})
		}
	}
	return cols
}

func (a *App) drawLine(x, y, maxX int, l Line) int {
	for _, sp := range l.Prefix {
		x = a.put(x, y, maxX, sp.Text, sp.Style)
	}
	for _, sp := range l.Spans {
		x = a.put(x, y, maxX, sp.Text, sp.Style)
	}
	return x
}

// promptLines renders the prompt and places the cursor in it. It returns
// the row of the Normal cursor, -1 when there is none, and the prompt row
// the first line shows, as long prompts scroll.
func (a *App) promptLines(w *Window, focused bool) ([]Line, int, int) {
	p := w.Buf.Prompt
	writing := focused && a.mode == modeInsert
	bottom := w.rect.Y + w.rect.H - 1 - promptGap
	a.fitPrompt(w)
	rows := a.promptRows(w)
	if len(p.Text) == 0 {
		// An empty prompt says who you write to.
		rows[0].Spans = []Span{{"write to " + w.Buf.Thread, a.th.Fg3}}
	}
	// The cursor's row: where you type, or the Normal cursor in the prompt.
	row, col := -1, 0
	switch {
	case writing:
		line, off := lineOff(p.Text, p.Cur)
		row, col = promptAt(rows, line, off)
	case focused && w.inPrompt && (a.mode == modeNormal || a.mode == modeVisual):
		row, col = min(w.Cur, len(rows)-1), w.Col
	}
	hidden := max(len(rows)-maxPromptLines, 0)
	if row >= 0 && row < hidden {
		hidden = row
	}
	lines := rows[hidden:min(hidden+maxPromptLines, len(rows))]
	if row < 0 {
		return lines, -1, hidden
	}
	a.cursorX = w.rect.X + min(col, w.rect.W-1)
	a.cursorY = bottom - len(lines) + row - hidden
	if a.mode != modeNormal {
		return lines, -1, hidden
	}
	return lines, row - hidden, hidden
}

// drawCells draws a line cell by cell, with the selection color where sel
// says so.
func (a *App) drawCells(x, y, maxX int, l Line, sel func(col int) bool) {
	col := 0
	for _, sp := range append(clone(l.Prefix), l.Spans...) {
		g := uniseg.NewGraphemes(sp.Text)
		for g.Next() {
			gw := g.Width()
			if gw == 0 {
				continue
			}
			if x+gw > maxX {
				return
			}
			st := sp.Style
			if sel(col) {
				st = overlay(st, a.th.Selection)
			}
			rs := g.Runes()
			a.s.SetContent(x, y, rs[0], rs[1:], st)
			x += gw
			col += gw
		}
	}
}

// drawStatus draws a pane's status line, its last row, as a bar: its name
// and state on the left, the chat's queue, branch, model and context on the
// right. The pane with focus has a brighter bar, its name in bold and its
// meta in color, as Vim's StatusLine; the others are quieter, StatusLineNC.
func (a *App) drawStatus(w *Window, y int, focused bool) {
	r := w.rect
	right := r.X + r.W
	b := w.Buf
	th := a.th
	bar := th.StatusLineNC
	if focused {
		bar = th.StatusLine
	}
	a.fill(r.X, y, right, bar)
	put := func(x int, text string, st tcell.Style) int { return a.put(x, y, right, text, overlay(st, bar)) }
	dim := th.Fg3
	// color is a meta part's own color in the pane with focus.
	color := func(st tcell.Style) tcell.Style {
		if focused {
			return st
		}
		return dim
	}
	x := r.X + 1
	// The workspace shows only for a pane of another one; the command line
	// names this one.
	if b.Workspace != "" && b.Workspace != a.workspace {
		x = put(x, b.Workspace+" · ", th.Run)
	}
	name, nameSt := bufTitle(b), th.Fg2
	if e := b.Edit; e != nil && e.Dirty {
		name += " [+]"
	}
	if focused {
		nameSt = th.Fg.Bold(true)
	}
	x = put(x, name, nameSt) + 2
	if b.Kind != KindChat {
		return
	}
	v := a.threads[b.Thread]
	glyphSt := th.state(v.State)
	// What needs the person keeps its color in every pane.
	if !focused && v.State != model.StateWaiting && v.State != model.StateFailed {
		glyphSt = dim
	}
	x = put(x, a.glyph(v.State)+" ", glyphSt)
	x = put(x, stateLabel(v.State), color(th.Fg2))
	if !v.Since.IsZero() && v.State != model.StateIdle {
		x = put(x, " "+elapsed(a.now().Sub(v.Since)), dim)
	}
	if wl := wakeLabel(v); wl != "" {
		x = put(x, "  "+wl, dim)
	}

	// The right: when it does not fit, the parts with the lowest rank go
	// first; context matters most, it says when to start fresh.
	type part struct {
		Span
		rank int // 0 stays
	}
	var parts []part
	add := func(text string, st tcell.Style, rank int) {
		if text != "" {
			parts = append(parts, part{Span{text, st}, rank})
		}
	}
	if v.Queued > 0 {
		add(fmt.Sprintf("+%d queued", v.Queued), dim, 0)
	}
	add(v.Branch, color(th.Run), 1)
	add(shortModel(v.Model), color(th.Peer), 2)
	if v.ContextWindow > 0 {
		pct := v.Context * 100 / v.ContextWindow
		label, ctxSt := fmt.Sprintf("ctx %d%%", pct), color(th.Done)
		if limit, _ := a.opt("compactwarn").(lua.LNumber); limit > 0 && pct >= int(limit) {
			// Every turn sends the whole context again: time to /compact.
			label, ctxSt = label+" · /compact", th.Alert
		}
		add(label, ctxSt, 4)
	}
	if v.Location.Remote() {
		add("ssh:"+v.Location.Host, dim, 0)
	}
	const sep = " · "
	total := func() int {
		t := 0
		for i, sp := range parts {
			t += width(sp.Text)
			if i > 0 {
				t += width(sep)
			}
		}
		return t
	}
	for rank := 1; right-1-total() <= x+2 && rank <= 4; rank++ {
		parts = slices.DeleteFunc(parts, func(p part) bool { return p.rank == rank })
	}
	if right-1-total() <= x+2 {
		return
	}
	rx := right - 1 - total()
	for i, sp := range parts {
		if i > 0 {
			rx = put(rx, sep, dim)
		}
		rx = put(rx, sp.Text, sp.Style)
	}
}

// shortModel turns "claude-opus-5[1m]" into "opus-5 1m".
func shortModel(m string) string {
	m = strings.TrimPrefix(m, "claude-")
	m = strings.ReplaceAll(m, "[", " ")
	return strings.TrimSuffix(m, "]")
}

func bufTitle(b *Buffer) string {
	switch b.Kind {
	case KindChat:
		return b.Thread
	case KindThreads:
		return "threads"
	case KindWorkspaces:
		return "workspaces"
	case KindMessages:
		return "messages"
	case KindHelp:
		return "help"
	case KindCost:
		if b.Workspace == "" {
			return "cost of every workspace"
		}
		return "cost"
	case KindArtifact:
		return b.Artifact
	case KindTodo:
		return b.Thread + " todo"
	case KindWaits:
		return "waits"
	}
	return b.Name
}

// drawFloats draws the permission and question cards over a chat window.
// card is the box a waiting chat shows above its prompt: its title and
// rows, and its width. No title, no card.
func (a *App) card(w *Window) (title string, body [][]Span, bw int) {
	v, ok := a.threads[w.Buf.Thread]
	if w.Buf.Kind != KindChat || !ok || v.State != model.StateWaiting {
		return
	}
	bw = a.textWidth(w)
	if bw < 20 {
		return
	}
	th := a.th
	switch {
	case v.Permission != nil:
		title = "permission"
		body = [][]Span{{{v.Permission.Tool, th.Fg2}}}
		var rows [][]Span
		for _, l := range strings.Split(v.Permission.Detail, "\n") {
			for _, r := range wrap([]Span{{l, th.Fg}}, bw-2, nil, nil) {
				rows = append(rows, r.Spans)
			}
		}
		if len(rows) > commandRows {
			rows = rows[:commandRows]
			rows[commandRows-1] = append(clone(rows[commandRows-1]), Span{" …", th.Fg3})
		}
		body = append(body, rows...)
		// What "always" would allow is part of the decision, not a key hint.
		if sc := v.Permission.Scope; sc != "" {
			body = append(body, []Span{{"always: " + truncate(sc, bw-10), th.Fg3}})
		}
	case v.Waiting == "paused":
		title = "paused"
		body = [][]Span{{{"paused", th.Fg2}}, {{"write to continue", th.Fg3}}}
	case v.QuestionID != "":
		title = "question"
		body = [][]Span{{{"question", th.Fg2}}}
		rows := renderMarkdown(v.Waiting, bw-2, th, nil)
		// The question is what the person answers, so it is cut only where
		// the window ends, leaving the chat a few rows.
		if n := max(w.rect.H-questionRoom, 1); len(rows) > n {
			rows = rows[:n]
			rows[n-1].Spans = append(clone(rows[n-1].Spans), Span{" …", th.Fg3})
		}
		for _, l := range rows {
			body = append(body, append(clone(l.Prefix), l.Spans...))
		}
	}
	return title, body, bw
}

// commandRows is the most rows of a command the card shows.
const commandRows = 8

// questionRoom is the rows of a window a question card leaves to the rest:
// the status line, the prompt, the blank rows and a few rows of chat.
const questionRoom = 9

// drawFloats draws the card in the rows the transcript left for it, just
// above bottom.
func (a *App) drawFloats(w *Window, top, bottom int) {
	title, body, bw := a.card(w)
	if title == "" {
		return
	}
	// One blank row parts the card from the chat above it.
	y0 := bottom - len(body)
	if y0-1 <= top {
		return
	}
	x0 := w.rect.X
	for i, spans := range body {
		a.fill(x0, y0+i, x0+bw, a.th.Base)
		x := a.put(x0, y0+i, x0+bw, "┃ ", a.th.Wait)
		for _, sp := range spans {
			x = a.put(x, y0+i, x0+bw, sp.Text, sp.Style)
		}
	}
}

// box draws a rounded border and clears its inside to the terminal's own
// background, so floats stay transparent.
func (a *App) box(x, y, w, h int, title string, st tcell.Style) {
	for i := y; i < y+h; i++ {
		a.fill(x, i, x+w, a.th.Base)
	}
	for i := x + 1; i < x+w-1; i++ {
		a.s.SetContent(i, y, '─', nil, st)
		a.s.SetContent(i, y+h-1, '─', nil, st)
	}
	for i := y + 1; i < y+h-1; i++ {
		a.s.SetContent(x, i, '│', nil, st)
		a.s.SetContent(x+w-1, i, '│', nil, st)
	}
	a.s.SetContent(x, y, '╭', nil, st)
	a.s.SetContent(x+w-1, y, '╮', nil, st)
	a.s.SetContent(x, y+h-1, '╰', nil, st)
	a.s.SetContent(x+w-1, y+h-1, '╯', nil, st)
	if title != "" {
		a.put(x+2, y, x+w-2, " "+title+" ", st)
	}
}

func (a *App) drawPicker(W, H int) {
	p := a.picker
	bw := min(W-8, 84)
	bh := min(H-6, 22)
	if p.all > 0 {
		bh = min(bh, p.all+4)
	}
	x0, y0 := (W-bw)/2, (H-bh)/3
	th := a.th
	a.box(x0, y0, bw, bh, p.title, th.Accent)
	inner := x0 + bw - 2
	qx := a.put(x0+2, y0+1, inner, "› ", th.Accent)
	end := a.put(qx, y0+1, inner, string(p.query), th.Fg)
	a.cursorX, a.cursorY = end, y0+1
	count := fmt.Sprintf("%d", len(p.items))
	a.put(inner-width(count), y0+1, inner, count, th.Fg3)
	for i := x0 + 1; i < x0+bw-1; i++ {
		a.s.SetContent(i, y0+2, '─', nil, th.Separator)
	}
	rows := bh - 4
	start := 0
	if p.sel >= rows {
		start = p.sel - rows + 1
	}
	labelW, details := 0, false
	for _, it := range p.items {
		labelW = max(labelW, width(it.label))
		details = details || it.detail != ""
	}
	// Without details, as in an outline, the label has the whole row.
	if details {
		labelW = min(labelW, bw/3)
	} else {
		labelW = min(labelW, bw-6)
	}
	for i := 0; i < rows && start+i < len(p.items); i++ {
		it := p.items[start+i]
		y := y0 + 3 + i
		selected := start+i == p.sel
		sel := func(st tcell.Style) tcell.Style {
			if selected {
				return overlay(st, th.CursorLine)
			}
			return st
		}
		if selected {
			a.fill(x0+1, y, x0+bw-1, th.CursorLine)
		}
		x := a.put(x0+2, y, inner, it.glyph.Text+" ", sel(it.glyph.Style))
		labelSt := th.Fg2
		if selected {
			labelSt = th.Fg.Bold(true)
		}
		x = a.put(x, y, inner, pad(truncate(it.label, labelW), labelW+2), sel(labelSt))
		a.put(x, y, inner, truncate(flatten(it.detail), inner-x), sel(th.Fg3))
	}
	if len(p.items) == 0 {
		a.put(x0+2, y0+3, inner, "no matches", th.Fg3)
	}
}

func (a *App) drawGlobal(W, H int) {
	y := H - 1
	th := a.th
	if a.mode == modeCommand {
		x := a.put(0, y, W, string(a.cmd.prefix), th.Accent)
		a.put(x, y, W, string(a.cmd.text), th.Fg)
		a.cursorX, a.cursorY = x+width(string(a.cmd.text[:a.cmd.cur])), y
		if c := a.cmd; c.comp != nil {
			hint := strings.Join(c.comp, "  ")
			a.put(max(W-width(hint)-1, x+len(c.text)+4), y, W, truncate(hint, W/2), th.Fg3)
		}
		return
	}
	// Right side: keys typed so far, as Vim's showcmd, then the workspace
	// and what waits and runs: what waits counts every workspace, the one
	// place to look.
	var right []Span
	if a.recordInto != 0 {
		right = append(right, Span{"recording @" + string(a.recordInto) + "  ", th.Fg3})
	}
	if len(a.pending) > 0 || a.count > 0 {
		show := strings.Join(a.pending, "")
		if a.count > 0 {
			show = fmt.Sprintf("%d%s", a.count, show)
		}
		right = append(right, Span{show + "  ", th.Fg3})
	}
	if a.workspace != "" {
		right = append(right, Span{a.workspace, th.Fg2})
	}
	running := 0
	for _, v := range a.threads {
		if v.State == model.StateRunning {
			running++
		}
	}
	if len(a.waits) > 0 {
		right = append(right, Span{fmt.Sprintf("  ◆ %d waiting", len(a.waits)), th.Wait})
	}
	if running > 0 {
		right = append(right, Span{fmt.Sprintf("  ● %d running", running), th.Run})
	}
	rx := W - margin
	for _, sp := range right {
		rx -= width(sp.Text)
	}
	for x, i := rx, 0; i < len(right); i++ {
		x = a.put(x, y, W, right[i].Text, right[i].Style)
	}

	x := margin + 1
	if a.offline {
		x = a.put(x, y, W, "● offline  ", th.Alert)
	}
	// Other modes show themselves: the colored caret in Insert, the
	// command line, the picker box. Visual needs a word to tell v from V.
	if a.mode == modeVisual {
		label := "visual"
		if a.cur.vmode == 'V' {
			label = "v-line"
		}
		x = a.put(x, y, W, label, th.Fg3) + 2
	}
	a.put(x, y, rx-1, truncate(a.notice, rx-x-2), a.noticeSt)
}

// elapsed is a time in whole minutes, so it changes once a minute and no
// seconds tick at the edge of the eye.
func elapsed(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// ago is a board row's time: one unit, at most three columns, so the
// column stays even.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// agoTurns is when ago(now-since) next changes.
func agoTurns(since, now time.Time) time.Time {
	d, unit := now.Sub(since), time.Minute
	switch {
	case d >= 24*time.Hour:
		unit = 24 * time.Hour
	case d >= time.Hour:
		unit = time.Hour
	}
	return since.Add((d/unit + 1) * unit)
}

func shortHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
