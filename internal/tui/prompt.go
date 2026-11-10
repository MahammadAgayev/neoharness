package tui

// The prompt is the end of a chat buffer, as in Neovim chat plugins: in
// Normal mode the cursor goes into it with j or G and out with k or gg, and
// the motions and changes of an artifact's source work on it. While the
// cursor is there, Window.Cur and Col are a prompt row and column, and
// chatCur, chatCol keep the place in the transcript.

// promptRows are the prompt's screen rows: each line of the message wrapped
// to the window. Src is the line, 1-based, and Off the rune where the row
// starts, as in an artifact's rows. A wrapped row keeps its trailing space,
// so a column maps to one rune.
func (a *App) promptRows(w *Window) []Line {
	p := w.Buf.Prompt
	caret := a.th.Muted
	if w == a.cur && a.mode == modeInsert {
		caret = a.th.Iris
	}
	first := []Span{{" › ", caret}}
	rest := []Span{{"   ", a.th.Text}}
	tw := max(a.textWidth(w)-width(" › "), 8)
	var rows []Line
	for li, l := range splitRunes(p.Text) {
		starts := wrapRunes(l, tw)
		for si, s := range starts {
			end := len(l)
			if si+1 < len(starts) {
				end = starts[si+1]
			}
			pre := rest
			if len(rows) == 0 {
				pre = first
			}
			rows = append(rows, Line{Prefix: pre, Spans: []Span{{string(l[s:end]), a.th.Text}}, Src: li + 1, Off: s})
		}
	}
	return rows
}

// splitRunes splits a message into its lines.
func splitRunes(text []rune) [][]rune {
	out := [][]rune{{}}
	for _, r := range text {
		if r == '\n' {
			out = append(out, []rune{})
			continue
		}
		out[len(out)-1] = append(out[len(out)-1], r)
	}
	return out
}

// lineOff is the line and rune of index i in a message; runeIndex is the
// other way.
func lineOff(text []rune, i int) (int, int) {
	line, start := 0, 0
	for k, r := range text[:min(i, len(text))] {
		if r == '\n' {
			line, start = line+1, k+1
		}
	}
	return line, min(i, len(text)) - start
}

func runeIndex(text []rune, line, off int) int {
	i := 0
	for l, row := range splitRunes(text) {
		if l == line {
			return i + min(off, len(row))
		}
		i += len(row) + 1
	}
	return len(text)
}

// promptPos is the line and rune at a prompt row and column; promptAt is the
// row and column of a line and rune.
func promptPos(rows []Line, row, col int) (int, int) {
	r := rows[max(min(row, len(rows)-1), 0)]
	return r.Src - 1, r.Off + runeAt([]rune(r.Text()), max(col-r.prefixWidth(), 0))
}

func promptAt(rows []Line, line, off int) (int, int) {
	row := 0
	for i, r := range rows {
		if r.Src == line+1 && r.Off <= off {
			row = i
		}
	}
	r := rows[row]
	text := []rune(r.Text())
	return row, r.prefixWidth() + width(string(text[:min(off-r.Off, len(text))]))
}

// enterPrompt moves the Normal cursor into the prompt, at the rune the
// prompt's own cursor is on.
func (a *App) enterPrompt(w *Window) {
	p := w.Buf.Prompt
	if p == nil || w.inPrompt || w.Buf.Edit != nil {
		return
	}
	w.chatCur, w.chatCol = w.Cur, w.Col
	w.inPrompt = true
	line, off := lineOff(p.Text, p.Cur)
	w.Cur, w.Col = promptAt(a.promptRows(w), line, off)
	a.clampCol(w)
}

// leavePrompt puts the Normal cursor back in the transcript.
func (a *App) leavePrompt(w *Window) {
	if !w.inPrompt {
		return
	}
	p := w.Buf.Prompt
	line, off := promptPos(a.promptRows(w), w.Cur, w.Col)
	p.Cur = runeIndex(p.Text, line, off)
	w.outOfPrompt()
}

// promptEditor is the editor the prompt's changes run in. Text typed in
// Insert since its last change is one change of its own, so u takes it back.
func (a *App) promptEditor(p *Prompt) *Editor {
	// It starts empty, as a message does, so u can take all of it back.
	if p.ed == nil {
		p.ed = newEditor("", "", 0)
	}
	e := p.ed
	e.reg = &a.reg
	if text := string(p.Text); e.Text() != text {
		e.save()
		e.Src = splitRunes(p.Text)
		e.changed()
	}
	return e
}

// onPrompt runs a change on the prompt at the cursor, then puts the cursor
// where it ends, in Insert when the change asks for it.
func (a *App) onPrompt(change func(e *Editor) (insert, ok bool)) {
	w := a.cur
	p := w.Buf.Prompt
	e := a.promptEditor(p)
	e.Line, e.Off = promptPos(a.promptRows(w), w.Cur, w.Col)
	insert, ok := change(e)
	if !ok {
		return
	}
	p.Text = []rune(e.Text())
	if insert {
		p.Cur = runeIndex(p.Text, e.Line, e.Off)
		a.mode = modeInsert
		w.Follow = true
		return
	}
	if len(e.Src[e.Line]) > 0 {
		e.Off = min(e.Off, len(e.Src[e.Line])-1)
	}
	w.Cur, w.Col = promptAt(a.promptRows(w), e.Line, e.Off)
	a.clampCol(w)
}

// insertInPrompt is i a I A o O with the cursor in the prompt: Insert starts
// at the cursor, after it, at the line's start or end, or on a new line.
func (a *App) insertInPrompt(k rune) {
	w := a.cur
	p := w.Buf.Prompt
	line, off := promptPos(a.promptRows(w), w.Cur, w.Col)
	l := splitRunes(p.Text)[line]
	switch k {
	case 'a':
		off = min(off+1, len(l))
	case 'I':
		off = firstNonBlank(l)
	case 'A', 'o':
		off = len(l)
	case 'O':
		off = 0
	}
	p.Cur = runeIndex(p.Text, line, off)
	if k == 'o' || k == 'O' {
		p.insert('\n')
		if k == 'O' {
			p.Cur--
		}
	}
}

// transcriptKeys leave the prompt first: they read or move in the transcript.
var transcriptKeys = map[string]bool{
	"/": true, "?": true, "n": true, "N": true, "]m": true, "[m": true, "gO": true, "gg": true,
	"za": true, "zt": true, "zz": true, "zb": true, "<C-d>": true, "<C-u>": true, "<C-f>": true,
	"<C-b>": true, "<PageUp>": true, "<PageDown>": true, "m": true, "'": true, "`": true,
	"%": true, "v": true, "V": true, "gx": true, "gf": true,
}
