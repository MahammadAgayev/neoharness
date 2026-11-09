package tui

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// Editor holds the markdown source of an artifact while the person edits
// it. The cursor is a source line and a rune offset in it; the window shows
// the lines soft-wrapped and keeps its own row and column, so motions work
// as in any buffer. Edits read the window's position first and set it after.
type Editor struct {
	Src [][]rune
	// Base is the version the edit started from, or the last one saved.
	Base      int
	Line, Off int
	Dirty     bool

	undo, redo []snapshot
	// reg is what x, D and dd took, for p and P. lines says dd took it.
	reg   []string
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

func newEditor(text string, base int) *Editor {
	e := &Editor{Base: base, rev: 1}
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
	return true
}

func (e *Editor) insert(rs ...rune) {
	l := e.Src[e.Line]
	e.Src[e.Line] = slices.Insert(slices.Clone(l), e.Off, rs...)
	e.Off += len(rs)
	e.changed()
}

func (e *Editor) newline() {
	l := e.Src[e.Line]
	head, tail := slices.Clone(l[:e.Off]), slices.Clone(l[e.Off:])
	e.Src[e.Line] = head
	e.Src = slices.Insert(e.Src, e.Line+1, tail)
	e.Line, e.Off = e.Line+1, 0
	e.changed()
}

func (e *Editor) backspace() {
	switch {
	case e.Off > 0:
		e.Src[e.Line] = slices.Delete(slices.Clone(e.Src[e.Line]), e.Off-1, e.Off)
		e.Off--
	case e.Line > 0:
		prev := e.Src[e.Line-1]
		e.Off = len(prev)
		e.Src[e.Line-1] = append(slices.Clone(prev), e.Src[e.Line]...)
		e.Src = slices.Delete(e.Src, e.Line, e.Line+1)
		e.Line--
	default:
		return
	}
	e.changed()
}

func (e *Editor) del() {
	l := e.Src[e.Line]
	switch {
	case e.Off < len(l):
		e.Src[e.Line] = slices.Delete(slices.Clone(l), e.Off, e.Off+1)
	case e.Line < len(e.Src)-1:
		e.Src[e.Line] = append(slices.Clone(l), e.Src[e.Line+1]...)
		e.Src = slices.Delete(e.Src, e.Line+1, e.Line+2)
	default:
		return
	}
	e.changed()
}

// cut removes runes from..to of the cursor line into the register.
func (e *Editor) cut(from, to int) {
	if l := e.Src[e.Line]; from < min(to, len(l)) {
		e.reg, e.lines = []string{string(l[from:min(to, len(l))])}, false
	}
	e.remove(from, to)
}

// remove removes runes from..to of the cursor line.
func (e *Editor) remove(from, to int) {
	l := e.Src[e.Line]
	to = min(to, len(l))
	if from >= to {
		return
	}
	e.Src[e.Line] = slices.Delete(slices.Clone(l), from, to)
	e.changed()
}

// cutLines removes n lines from the cursor into the register.
func (e *Editor) cutLines(n int) {
	end := min(e.Line+n, len(e.Src))
	e.reg, e.lines = nil, true
	for _, l := range e.Src[e.Line:end] {
		e.reg = append(e.reg, string(l))
	}
	e.Src = slices.Delete(e.Src, e.Line, end)
	if len(e.Src) == 0 {
		e.Src = [][]rune{{}}
	}
	e.Line = min(e.Line, len(e.Src)-1)
	e.Off = firstNonBlank(e.Src[e.Line])
	e.changed()
}

// put pastes the register after the cursor, or before it.
func (e *Editor) put(after bool) {
	if len(e.reg) == 0 {
		return
	}
	if e.lines {
		at := e.Line
		if after {
			at++
		}
		var rs [][]rune
		for _, l := range e.reg {
			rs = append(rs, []rune(l))
		}
		e.Src = slices.Insert(e.Src, at, rs...)
		e.Line, e.Off = at, firstNonBlank(e.Src[at])
		e.changed()
		return
	}
	if after && len(e.Src[e.Line]) > 0 {
		e.Off++
	}
	e.insert([]rune(e.reg[0])...)
	e.Off--
}

func firstNonBlank(l []rune) int {
	for i, r := range l {
		if !unicode.IsSpace(r) {
			return i
		}
	}
	return 0
}

// render shows the source soft-wrapped to w cells, headings in their colors
// and marked for the outline rail.
func (e *Editor) render(w int, th *Theme) []Line {
	if e.cacheW == w && e.cacheR == e.rev {
		return e.cache
	}
	margin := []Span{{"  ", th.Text}}
	avail := max(w-width(spansText(margin)), 4)
	var out []Line
	fence := false
	for i, l := range e.Src {
		st, head := th.Text, 0
		s := string(l)
		switch {
		case strings.HasPrefix(s, "```"):
			fence = !fence
			st = th.Muted
		case !fence && strings.HasPrefix(s, "#"):
			head = len(s) - len(strings.TrimLeft(s, "#"))
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

// fromWindow moves the editor's cursor to the window's.
func (a *App) fromWindow(w *Window) {
	e := w.Buf.Edit
	lines := e.render(a.textWidth(w), a.th)
	if w.Cur >= len(lines) {
		return
	}
	l := lines[w.Cur]
	src := e.Src[l.Src-1]
	end := len(src)
	if w.Cur+1 < len(lines) && lines[w.Cur+1].Src == l.Src {
		end = lines[w.Cur+1].Off
	}
	off, col := l.Off, w.Col-l.prefixWidth()
	for off < end && col > 0 {
		col -= runeCells(src[off])
		off++
	}
	e.Line, e.Off = l.Src-1, off
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
		b.Edit = newEditor(bl[0].text, bl[0].art.Version)
		b.Edit.Line = max(src-1, 0)
		b.dirty = true
		// The rendered column says nothing about the source one.
		if k == 'a' {
			k = 'i'
		}
	} else {
		a.fromWindow(w)
	}
	e := b.Edit
	switch k {
	case 0:
		a.toWindow(w)
		return
	case 'i':
	case 'a':
		e.Off = min(e.Off+1, len(e.Src[e.Line]))
	case 'I':
		e.Off = firstNonBlank(e.Src[e.Line])
	case 'A':
		e.Off = len(e.Src[e.Line])
	case 'o', 'O':
		e.save()
		if k == 'o' {
			e.Line++
		}
		e.Src = slices.Insert(e.Src, e.Line, []rune{})
		e.Off = 0
		e.changed()
	}
	if k != 'o' && k != 'O' {
		e.save()
	}
	a.mode = modeInsert
	a.cur.vmode = 0
	a.toWindow(w)
}

// stopEdit shows the document again, at the section the cursor was in.
func (a *App) stopEdit(w *Window) {
	b := w.Buf
	target := b.Edit.Line + 1
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

// leaveEdit is <Esc>, q and :q in the source: back to the document, unless
// that drops changes.
func (a *App) leaveEdit(force bool) {
	if a.cur.Buf.Edit.Dirty && !force {
		a.notify("unsaved changes · :w saves, :q! drops them", a.th.Gold)
		return
	}
	a.stopEdit(a.cur)
}

// saveEdit sends the source to the hub as the person's new version.
func (a *App) saveEdit(force bool) bool {
	b := a.cur.Buf
	e := b.Edit
	art, err := a.c.EditArtifact(b.Workspace, b.Artifact, e.Text(), e.Base, force)
	if err != nil {
		a.notify(err.Error(), a.th.Love)
		return false
	}
	saved := art.Version != e.Base
	e.Base, e.Dirty = art.Version, false
	a.loadArtifact(b)
	if !saved {
		a.notify("no changes", a.th.Muted)
		return true
	}
	a.notify(fmt.Sprintf("saved %s v%d · %s sees it with your next message", b.Artifact, art.Version, art.Thread), a.th.Subtle)
	return true
}

// editNormal runs a Normal key that changes the source.
func (a *App) editNormal(k string, n int) {
	w := a.cur
	e := w.Buf.Edit
	if e == nil {
		return
	}
	a.fromWindow(w)
	n = max(n, 1)
	switch k {
	case "x":
		e.save()
		e.cut(e.Off, e.Off+n)
	case "D":
		e.save()
		e.cut(e.Off, len(e.Src[e.Line]))
	case "dd":
		e.save()
		e.cutLines(n)
	case "p", "P":
		e.save()
		for range n {
			e.put(k == "p")
		}
	case "u":
		if !e.step(&e.undo, &e.redo) {
			a.notify("already at oldest change", a.th.Muted)
		}
	case "<C-r>":
		if !e.step(&e.redo, &e.undo) {
			a.notify("already at newest change", a.th.Muted)
		}
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
		a.mode = modeNormal
		e.Off = max(e.Off-1, 0)
	case "<CR>", "<S-CR>", "<C-j>", "<M-\r>":
		e.newline()
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
		e.remove(0, e.Off)
		e.Off = 0
	case "<C-w>":
		i := e.Off
		l := e.Src[e.Line]
		for i > 0 && unicode.IsSpace(l[i-1]) {
			i--
		}
		for i > 0 && !unicode.IsSpace(l[i-1]) {
			i--
		}
		e.remove(i, e.Off)
		e.Off = i
	case "<Tab>":
		e.insert(' ', ' ')
	default:
		if isRune {
			e.insert(r)
		}
	}
	// A paste comes as one key per rune; the cursor follows once it ends.
	if !a.pasting {
		a.toWindow(w)
	}
}
