package tui

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"

	"github.com/MahammadAgayev/neoharness/internal/model"
)

// scrollOff keeps this many lines visible around the cursor.
const scrollOff = 8

// The prompt is at least minPromptLines tall, so it reads as a place to
// write, and grows to maxPromptLines before it scrolls.
const (
	minPromptLines = 3
	maxPromptLines = 12
)

func (a *App) draw() {
	s := a.s
	s.Clear()
	W, H := s.Size()
	if W < 20 || H < 6 {
		a.put(0, 0, W, "neoharness: window too small", a.th.Love)
		s.Show()
		return
	}
	a.cursorX, a.cursorY = -1, -1
	// Which threads are on screen changes with every window command, so the
	// list is redrawn here rather than after each of them.
	a.renderThreads()

	sep := func(x, y, h int) {
		for i := 0; i < h; i++ {
			s.SetContent(x, y+i, '│', nil, a.th.Separator)
		}
	}
	live := false
	area := Rect{0, 0, W, H - 1}
	a.root.layout(area, sep)
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
	s.Show()

	// Animate only while something is alive on screen; an idle workspace
	// costs nothing.
	if live && !a.ticking {
		a.ticking = true
		time.AfterFunc(90*time.Millisecond, func() { _ = a.s.PostEvent(evTick{}) })
	}
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

func (a *App) drawWindow(w *Window, focused bool) {
	r := w.rect
	if r.W < 4 || r.H < 2 {
		return
	}
	b := w.Buf
	right := r.X + r.W
	top := r.Y
	status := r.Y + r.H - 1

	// The prompt sits at the bottom of a chat window, above the statusline.
	var promptLines []Line
	promptTop := status
	if b.Prompt != nil {
		promptLines = a.promptLines(w, focused)
		promptTop = status - len(promptLines)
	}
	textBottom := promptTop
	if b.Prompt != nil {
		textBottom-- // a blank line between transcript and prompt
	}
	h := max(textBottom-top, 1)
	w.textH = h

	lines := b.Lines(a.textWidth(w), a.th, a.spin)
	n := len(lines)
	w.linesN = n
	rail := railWidth(w)
	if n == 0 {
		w.Cur = 0
	} else if w.Follow || w.Cur >= n {
		w.Cur = n - 1
	}
	off := min(scrollOff, h/2)
	if w.Cur < w.Top+off {
		w.Top = max(w.Cur-off, 0)
	}
	if w.Cur >= w.Top+h-off {
		w.Top = w.Cur - h + off + 1
	}
	w.Top = max(min(w.Top, n-h), 0)
	if w.Follow {
		w.Top = max(n-h, 0)
	}

	editing := b.Edit != nil && focused && a.mode == modeInsert
	showCursor := focused && (a.mode == modeNormal || a.mode == modeVisual) || editing
	vl1, vl2 := -1, -1
	if w.vmode != 0 && focused {
		vl1, vl2 = min(w.vLine, w.Cur), max(w.vLine, w.Cur)
	}
	for i := 0; i < h && w.Top+i < n; i++ {
		li := w.Top + i
		y := top + i
		l := lines[li]
		bg := tcell.Style{}
		hasBG := false
		if showCursor && li == w.Cur && w.vmode == 0 {
			bg, hasBG = a.th.CursorLine, true
		}
		x := r.X + rail
		col := 0
		cut := false
		spans := l.Spans
		if w.Buf.Kind == KindChat {
			spans = a.linkify(spans)
		}
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
			st := a.th.Muted
			if hasBG {
				st = overlay(st, bg)
			}
			a.put(right-1, y, right, "…", st)
		}
		if showCursor && li == w.Cur {
			a.cursorX, a.cursorY = r.X+rail+min(w.Col, r.W-rail-1), y
			// The caret of the prompt: you type here.
			if editing {
				a.put(r.X+rail, y, right, "›", overlay(a.th.Iris, a.th.CursorLine))
			}
		}
	}
	if showCursor && n == 0 {
		a.cursorX, a.cursorY = r.X+rail, top
	}
	if rail > 0 {
		a.drawRail(w, lines, top, h)
	}

	if b.Prompt != nil {
		for i, l := range promptLines {
			a.drawLine(r.X, promptTop+i, right, l)
		}
	}
	if b.Kind == KindChat {
		a.drawFloats(w, top, textBottom)
	}
	if focused && a.menu != nil {
		a.drawMenu(w, top, promptTop)
	}
	a.drawStatus(w, status, focused)
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
		dash, st := "─", a.th.Muted
		if i == cur {
			dash, st = "━", a.th.Subtle
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
	a.box(x0, y0, bw, rows+2, "", th.Iris)
	start := max(m.sel-rows+1, 0)
	for i := 0; i < rows && start+i < len(m.items); i++ {
		y := y0 + 1 + i
		it := m.items[start+i]
		glyphSt, st := it.glyph.Style, th.Subtle
		if start+i == m.sel {
			a.fill(x0+1, y, x0+bw-1, th.CursorLine)
			glyphSt, st = overlay(glyphSt, th.CursorLine), overlay(th.Text.Bold(true), th.CursorLine)
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

func (a *App) drawLine(x, y, maxX int, l Line) int {
	for _, sp := range l.Prefix {
		x = a.put(x, y, maxX, sp.Text, sp.Style)
	}
	for _, sp := range l.Spans {
		x = a.put(x, y, maxX, sp.Text, sp.Style)
	}
	return x
}

// promptLines renders the prompt, and places the cursor in it when writing.
func (a *App) promptLines(w *Window, focused bool) []Line {
	p := w.Buf.Prompt
	writing := focused && a.mode == modeInsert
	caret := a.th.Muted
	if writing {
		caret = a.th.Iris
	}
	first := []Span{{" › ", caret}}
	rest := []Span{{"   ", a.th.Text}}
	statusY := w.rect.Y + w.rect.H - 1
	pad := func(lines []Line) []Line {
		for len(lines) < minPromptLines {
			lines = append(lines, Line{Prefix: rest})
		}
		return lines
	}
	if len(p.Text) == 0 {
		if !writing {
			return pad([]Line{{Prefix: first}})
		}
		a.cursorX, a.cursorY = w.rect.X+3, statusY-minPromptLines
		return pad([]Line{{Prefix: first, Spans: []Span{{"Message " + w.Buf.Thread + "…", a.th.Muted}}}})
	}
	all := wrap([]Span{{string(p.Text), a.th.Text}}, a.textWidth(w), first, rest)
	hidden := max(len(all)-maxPromptLines, 0)
	lines := pad(all[hidden:])
	if writing {
		// Wrapping the text up to the cursor the same way puts the cursor at
		// the end of its last line.
		head := wrap([]Span{{string(p.Text[:p.Cur]), a.th.Text}}, a.textWidth(w), first, rest)
		last := head[len(head)-1]
		x := last.prefixWidth() + width(last.Text())
		// wrap drops a trailing space, but the cursor sits after it.
		if p.Cur > 0 && p.Text[p.Cur-1] == ' ' {
			x++
		}
		a.cursorX = w.rect.X + min(x, w.rect.W-1)
		a.cursorY = statusY - len(lines) + max(len(head)-1-hidden, 0)
	}
	return lines
}

func (a *App) drawStatus(w *Window, y int, focused bool) {
	r := w.rect
	right := r.X + r.W
	b := w.Buf
	x := r.X + 1
	dim := a.th.Muted
	// Other modes show themselves: the colored caret in Insert, the
	// command line, the picker box. Visual needs a word to tell v from V.
	if focused && a.mode == modeVisual {
		label := "visual"
		if w.vmode == 'V' {
			label = "v-line"
		}
		x = a.put(x, y, right, label, dim) + 2
	}
	name, nameSt := bufTitle(b), a.th.Subtle
	if e := b.Edit; e != nil && e.Dirty {
		name += " [+]"
	}
	if focused {
		nameSt = a.th.Text
	}
	x = a.put(x, y, right, name, nameSt) + 2

	// Right side, in muted color. When it does not fit, the first parts of
	// drop go first: context matters most, it says when to start fresh.
	type part struct {
		Span
		rank int // 0 stays
	}
	var rightText []part
	if b.Kind == KindChat {
		v := a.threads[b.Thread]
		st := a.th.state(v.State)
		if !focused {
			st = dim
		}
		x = a.put(x, y, right, stateGlyph(v.State)+" "+stateLabel(v.State), st)
		if !v.Since.IsZero() && v.State != model.StateIdle {
			x = a.put(x+2, y, right, elapsed(time.Since(v.Since)), dim)
		}
		add := func(text string, st tcell.Style, rank int) {
			if text != "" {
				rightText = append(rightText, part{Span{text, st}, rank})
			}
		}
		if v.Queued > 0 {
			add(fmt.Sprintf("+%d queued", v.Queued), dim, 0)
		}
		// Colored in the window with focus, like Claude Code's status line;
		// muted in the others.
		color := func(st tcell.Style) tcell.Style {
			if focused {
				return st
			}
			return dim
		}
		add(v.Branch, color(a.th.Foam), 1)
		add(shortModel(v.Model), color(a.th.Rose), 2)
		if v.ContextWindow > 0 {
			pct := v.Context * 100 / v.ContextWindow
			ctxSt := color(a.th.Gold)
			if pct >= 80 {
				// A nearly full context needs the person: time to /compact.
				ctxSt = a.th.Love
			}
			add(fmt.Sprintf("ctx %d%%", pct), ctxSt, 4)
		}
		if v.Cost > 0 {
			add(fmt.Sprintf("$%.2f", v.Cost), color(a.th.Pine), 3)
		}
		if v.Location.Remote() {
			add("ssh:"+v.Location.Host, dim, 0)
		}
	}
	const sep = " · "
	fits := func() bool {
		total := 0
		for i, sp := range rightText {
			total += width(sp.Text)
			if i > 0 {
				total += width(sep)
			}
		}
		return right-1-total > x+2
	}
	for rank := 1; !fits() && rank <= 4; rank++ {
		rightText = slices.DeleteFunc(rightText, func(p part) bool { return p.rank == rank })
	}
	if !fits() {
		return
	}
	total := 0
	for i, sp := range rightText {
		total += width(sp.Text)
		if i > 0 {
			total += width(sep)
		}
	}
	rx := right - 1 - total
	for i, sp := range rightText {
		if i > 0 {
			rx = a.put(rx, y, right, sep, dim)
		}
		rx = a.put(rx, y, right, sp.Text, sp.Style)
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
	case KindArtifact:
		return b.Artifact
	}
	return b.Name
}

// drawFloats draws the permission and question cards over a chat window.
func (a *App) drawFloats(w *Window, top, bottom int) {
	v, ok := a.threads[w.Buf.Thread]
	if !ok || v.State != model.StateWaiting {
		return
	}
	r := w.rect
	bw := min(r.W-4, 64)
	if bw < 20 {
		return
	}
	th := a.th
	var title string
	var body [][]Span
	switch {
	case v.Permission != nil:
		title = "permission"
		body = [][]Span{
			{{v.Permission.Tool, th.Text.Bold(true)}, {"  " + truncate(v.Permission.Detail, bw-8-width(v.Permission.Tool)), th.Subtle}},
			{{"␣a", th.Iris}, {" allow   ", th.Muted}, {"␣A", th.Iris}, {" always   ", th.Muted}, {"␣x", th.Iris}, {" deny", th.Muted}},
		}
	case v.Waiting == "paused":
		title = "paused"
		body = [][]Span{
			{{"i", th.Iris}, {" write to continue", th.Muted}},
		}
	case v.Waiting != "":
		title = "question"
		for i, l := range wrap([]Span{{v.Waiting, th.Text}}, bw-4, nil, nil) {
			if i == 3 {
				break
			}
			body = append(body, l.Spans)
		}
		body = append(body, []Span{{"i", th.Iris}, {" reply", th.Muted}})
	default:
		return
	}
	bh := len(body) + 2
	y0 := bottom - bh
	if y0 < top {
		return
	}
	x0 := r.X + 2
	a.box(x0, y0, bw, bh, title, th.Gold)
	for i, spans := range body {
		x := x0 + 2
		for _, sp := range spans {
			x = a.put(x, y0+1+i, x0+bw-2, sp.Text, sp.Style)
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
	x0, y0 := (W-bw)/2, (H-bh)/3
	th := a.th
	a.box(x0, y0, bw, bh, p.title, th.Iris)
	inner := x0 + bw - 2
	qx := a.put(x0+2, y0+1, inner, "› ", th.Iris)
	end := a.put(qx, y0+1, inner, string(p.query), th.Text)
	a.cursorX, a.cursorY = end, y0+1
	count := fmt.Sprintf("%d", len(p.items))
	a.put(inner-width(count), y0+1, inner, count, th.Muted)
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
		labelSt := th.Subtle
		if selected {
			labelSt = th.Text.Bold(true)
		}
		x = a.put(x, y, inner, pad(truncate(it.label, labelW), labelW+2), sel(labelSt))
		a.put(x, y, inner, truncate(flatten(it.detail), inner-x), sel(th.Muted))
	}
	if len(p.items) == 0 {
		a.put(x0+2, y0+3, inner, "no matches", th.Muted)
	}
}

func (a *App) drawGlobal(W, H int) {
	y := H - 1
	th := a.th
	if a.mode == modeCommand {
		x := a.put(0, y, W, string(a.cmd.prefix), th.Iris)
		a.put(x, y, W, string(a.cmd.text), th.Text)
		a.cursorX, a.cursorY = x+width(string(a.cmd.text[:a.cmd.cur])), y
		if c := a.cmd; c.comp != nil {
			hint := strings.Join(c.comp, "  ")
			a.put(max(W-width(hint)-1, x+len(c.text)+4), y, W, truncate(hint, W/2), th.Muted)
		}
		return
	}
	// Right side: how many threads of the workspace run or wait for you, then
	// keys typed so far, as Vim's showcmd.
	var right []Span
	if a.recordInto != 0 {
		right = append(right, Span{"recording @" + string(a.recordInto) + "  ", th.Muted})
	}
	if len(a.pending) > 0 || a.count > 0 {
		show := strings.Join(a.pending, "")
		if a.count > 0 {
			show = fmt.Sprintf("%d%s", a.count, show)
		}
		right = append(right, Span{show + "  ", th.Muted})
	}
	n := map[model.State]int{}
	for _, v := range a.threads {
		n[v.State]++
	}
	for _, s := range []model.State{model.StateRunning, model.StateWaiting} {
		if n[s] > 0 {
			right = append(right, Span{fmt.Sprintf(" %s %d", stateGlyph(s), n[s]), th.state(s)})
		}
	}
	rx := W - 1
	for _, sp := range right {
		rx -= width(sp.Text)
	}
	for x, i := rx, 0; i < len(right); i++ {
		x = a.put(x, y, W, right[i].Text, right[i].Style)
	}

	x := 1
	if a.offline {
		x = a.put(x, y, W, "● offline  ", th.Love)
	}
	a.put(x, y, rx-1, truncate(a.notice, rx-x-2), a.noticeSt)
}

func elapsed(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func shortHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
