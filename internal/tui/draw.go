package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"

	"github.com/MahammadAgayev/neoharness/internal/model"
)

// scrollOff keeps this many lines visible around the cursor.
const scrollOff = 2

// maxPromptLines is how tall the prompt grows before it scrolls.
const maxPromptLines = 6

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

	a.root.layout(Rect{0, 0, W, H - 1}, func(x, y, h int) {
		for i := 0; i < h; i++ {
			s.SetContent(x, y+i, '│', nil, a.th.Separator)
		}
	})
	live := false
	for _, w := range a.root.windows() {
		a.drawWindow(w, w == a.cur)
		live = live || w.Buf.Live()
	}
	if a.picker != nil {
		a.drawPicker(W, H)
	}
	a.drawGlobal(W, H)

	if a.cursorX >= 0 {
		s.ShowCursor(a.cursorX, a.cursorY)
	} else {
		s.HideCursor()
	}
	if a.mode == modeInsert || a.mode == modeCommand || a.mode == modePicker {
		s.SetCursorStyle(tcell.CursorStyleSteadyBar)
	} else {
		s.SetCursorStyle(tcell.CursorStyleSteadyBlock)
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

	showCursor := focused && (a.mode == modeNormal || a.mode == modeVisual)
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
		x := r.X
		col := 0
		for _, sp := range append(clone(l.Prefix), l.Spans...) {
			g := uniseg.NewGraphemes(sp.Text)
			for g.Next() {
				gw := g.Width()
				if gw == 0 {
					continue
				}
				if x+gw > right {
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
		if showCursor && li == w.Cur {
			a.cursorX, a.cursorY = r.X+min(w.Col, r.W-1), y
		}
	}
	if showCursor && n == 0 {
		a.cursorX, a.cursorY = r.X, top
	}

	if b.Prompt != nil {
		for i, l := range promptLines {
			a.drawLine(r.X, promptTop+i, right, l)
		}
	}
	if b.Kind == KindChat {
		a.drawFloats(w, top, textBottom)
	}
	a.drawStatus(w, status, focused)
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
	if len(p.Text) == 0 {
		if !writing {
			return []Line{{Prefix: first}}
		}
		a.cursorX, a.cursorY = w.rect.X+3, w.rect.Y+w.rect.H-2
		return []Line{{Prefix: first, Spans: []Span{{"Message " + w.Buf.Thread + "…", a.th.Muted}}}}
	}
	all := wrap([]Span{{string(p.Text), a.th.Text}}, a.textWidth(w), first, rest)
	hidden := max(len(all)-maxPromptLines, 0)
	lines := all[hidden:]
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
		statusY := w.rect.Y + w.rect.H - 1
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
	// Other modes show themselves: the bar cursor and the caret in Insert, the
	// command line, the picker box. Visual needs a word to tell v from V.
	if focused && a.mode == modeVisual {
		label := "VISUAL"
		if w.vmode == 'V' {
			label = "V-LINE"
		}
		x = a.put(x, y, right, label, dim) + 2
	}
	name, nameSt := bufTitle(b), a.th.Subtle
	if focused {
		nameSt = a.th.Text
	}
	x = a.put(x, y, right, name, nameSt) + 2

	var rightText []Span
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
		if v.Queued > 0 {
			rightText = append(rightText, Span{fmt.Sprintf("+%d queued", v.Queued), dim})
		}
		if v.Location.Remote() {
			rightText = append(rightText, Span{"ssh:" + v.Location.Host, dim})
		}
	}
	total := 0
	for i, sp := range rightText {
		total += width(sp.Text)
		if i > 0 {
			total += 2
		}
	}
	rx := right - 1 - total
	if rx > x+2 {
		for i, sp := range rightText {
			if i > 0 {
				rx += 2
			}
			rx = a.put(rx, y, right, sp.Text, sp.Style)
		}
	}
}

func bufTitle(b *Buffer) string {
	switch b.Kind {
	case KindChat:
		return b.Thread
	case KindThreads:
		return "threads"
	case KindProjects:
		return "projects"
	case KindMessages:
		return "messages"
	case KindHelp:
		return "help"
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
			{{"␣y", th.Iris}, {" allow   ", th.Muted}, {"␣Y", th.Iris}, {" always   ", th.Muted}, {"␣n", th.Iris}, {" deny", th.Muted}},
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
	labelW := 0
	for _, it := range p.items {
		labelW = max(labelW, width(it.label))
	}
	labelW = min(labelW, bw/3)
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
	// Right side: how many threads of the project run or wait for you, then
	// keys typed so far, as Vim's showcmd.
	var right []Span
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
