package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MahammadAgayev/neoharness/api/linediff"
)

// diffMode is Neovim's diff mode for an artifact whose :w met a newer
// version that changed the same lines: the source on the left, the newer
// version read-only on the right, both raw, line beside line.
type diffMode struct {
	theirs  []string
	version int
	thread  string
	buf     *Buffer
	win     *Window
	// w is the text width of both sides: the narrower window's.
	w     int
	hunks []hunk
	// starts are the rows where each hunk begins.
	starts []int
}

// hunk is a change between the source, lines mFrom..mTo, and the newer
// version, lines tFrom..tTo.
type hunk struct{ mFrom, mTo, tFrom, tTo int }

// renderDiff lays both sides out at avail cells: a pair of lines takes as
// many rows as the longer of the two, the shorter padded with filler rows,
// so the rows of the two windows stay side by side. The newer version's
// rows go to its buffer. A filler row has Off -1 and the Src of the source
// line above it, 0 on top.
func (e *Editor) renderDiff(avail int, th *Theme, margin []Span) []Line {
	d := e.diff
	mine := make([]string, len(e.Src))
	for i, l := range e.Src {
		mine[i] = string(l)
	}
	ops := linediff.Lines(mine, d.theirs)
	d.hunks, d.starts = nil, nil
	fill := Span{strings.Repeat("╱", avail), th.Separator}
	out := []Line{{}, {Prefix: margin, Spans: []Span{{e.meta(), th.Muted}}}, {}}
	theirs := []Line{{}, {Prefix: margin, Spans: []Span{{fmt.Sprintf("◇ v%d · by %s · read-only", d.version, d.thread), th.Muted}}}, {}}
	rows := func(s string, src, at int, st Span) []Line {
		var ls []Line
		r := []rune(strings.ReplaceAll(s, "\t", " "))
		starts := wrapRunes(r, avail)
		for j, from := range starts {
			end := len(r)
			if j+1 < len(starts) {
				end = starts[j+1]
			}
			l := Line{Prefix: margin, Src: src, Off: at + from}
			if end > from {
				l.Spans = []Span{{string(r[from:end]), st.Style}}
			}
			ls = append(ls, l)
		}
		return ls
	}
	last := -1
	pair := func(mi, ti int, changed bool) {
		mst, tst := Span{Style: th.Text}, Span{Style: th.Subtle}
		if changed {
			mst, tst = Span{Style: th.Gold}, Span{Style: th.Foam}
		}
		var m, t []Line
		if mi >= 0 {
			m = rows(mine[mi], mi+1, 0, mst)
			last = mi
		}
		if ti >= 0 {
			t = rows(d.theirs[ti], 0, 0, tst)
		}
		for len(m) < max(len(t), 1) {
			m = append(m, Line{Prefix: margin, Src: last + 1, Off: -1, Spans: []Span{fill}})
		}
		for len(t) < len(m) {
			t = append(t, Line{Prefix: margin, Spans: []Span{fill}})
		}
		for k := range t {
			t[k].Src, t[k].Off = m[k].Src, m[k].Off
		}
		out, theirs = append(out, m...), append(theirs, t...)
	}
	i, j := 0, 0
	for k := 0; k < len(ops); {
		if ops[k].Kind == ' ' {
			pair(i, j, false)
			i, j, k = i+1, j+1, k+1
			continue
		}
		h := hunk{mFrom: i, tFrom: j}
		for ; k < len(ops) && ops[k].Kind != ' '; k++ {
			if ops[k].Kind == '-' {
				i++
			} else {
				j++
			}
		}
		h.mTo, h.tTo = i, j
		d.hunks, d.starts = append(d.hunks, h), append(d.starts, len(out))
		for n := 0; n < max(h.mTo-h.mFrom, h.tTo-h.tFrom); n++ {
			mi, ti := -1, -1
			if h.mFrom+n < h.mTo {
				mi = h.mFrom + n
			}
			if h.tFrom+n < h.tTo {
				ti = h.tFrom + n
			}
			pair(mi, ti, true)
		}
	}
	d.buf.SetLines(theirs)
	return out
}

// openDiff starts diff mode after a :w that the newer version refused. The
// source keeps its text; its base becomes the newer version, so the next :w
// saves the result over it.
func (a *App) openDiff(w *Window, refused error) {
	b, e := w.Buf, w.Buf.Edit
	art, text, err := a.c.Artifact(b.Workspace, b.Artifact)
	if err != nil {
		a.notify(refused.Error(), a.th.Love)
		return
	}
	tb := newBuffer(fmt.Sprintf("neoharness://%s/%s@v%d", b.Workspace, b.Artifact, art.Version), KindDiff)
	tb.diffOf = e
	tw := newWindow(tb)
	tw.Follow = false
	e.diff = &diffMode{theirs: strings.Split(text, "\n"), version: art.Version, thread: art.Thread, buf: tb, win: tw}
	e.art, e.Base, e.saved = art, art.Version, text
	e.cacheW = 0
	a.root.splitWindow(w, tw, true)
	a.equalAlways(true)
	a.cur = w
	a.toWindow(w)
	a.notify(fmt.Sprintf("v%d changed the lines you edited · do takes its change, ]c [c move, :w saves yours", art.Version), a.th.Gold)
}

// closeDiff ends diff mode: the newer version's window closes.
func (a *App) closeDiff(e *Editor) {
	if e.diff == nil {
		return
	}
	tw := e.diff.win
	e.diff = nil
	e.cacheW = 0
	to, vert := a.root.closeWindow(tw)
	if to != nil {
		a.equalAlways(vert)
		if a.cur == tw {
			a.cur = to
		}
	}
	for _, w := range a.root.windows() {
		if w.Buf.Edit == e {
			a.toWindow(w)
		}
	}
}

// diffOf is the source in diff mode that a window shows, its own or the one
// beside it.
func diffOf(w *Window) *Editor {
	if e := w.Buf.Edit; e != nil && e.diff != nil {
		return e
	}
	if e := w.Buf.diffOf; e != nil && e.diff != nil {
		return e
	}
	return nil
}

// diffJump is ]c and [c: the next or previous change.
func (a *App) diffJump(d int) {
	w := a.cur
	e := diffOf(w)
	if e == nil {
		a.notify("not in a diff", a.th.Muted)
		return
	}
	a.lineCount(w)
	starts := e.diff.starts
	if d < 0 {
		starts = slices.Clone(starts)
		slices.Reverse(starts)
	}
	for _, row := range starts {
		if d > 0 && row > w.Cur || d < 0 && row < w.Cur {
			w.Cur, w.Follow = row, false
			if w.Buf.Edit != nil {
				a.fromWindow(w)
			}
			center(w)
			return
		}
	}
}

// diffTake is do in the source and dp in the newer version: the change at
// the cursor comes from the newer version into the source.
func (a *App) diffTake() {
	w := a.cur
	e := diffOf(w)
	if e == nil {
		a.notify("not in a diff", a.th.Muted)
		return
	}
	l := a.line(w)
	line, filler := l.Src-1, l.Off < 0
	at := -1
	for i, h := range e.diff.hunks {
		if h.mFrom == h.mTo && filler && line+1 == h.mFrom || line >= h.mFrom && line < h.mTo {
			at = i
			break
		}
	}
	if at < 0 {
		a.notify("no change here", a.th.Muted)
		return
	}
	h := e.diff.hunks[at]
	e.save()
	var lines [][]rune
	for _, s := range e.diff.theirs[h.tFrom:h.tTo] {
		lines = append(lines, []rune(s))
	}
	e.Src = slices.Concat(e.Src[:h.mFrom], lines, e.Src[h.mTo:])
	if len(e.Src) == 0 {
		e.Src = [][]rune{{}}
	}
	e.changed()
	e.Line, e.Off = min(h.mFrom, len(e.Src)-1), 0
	for _, ow := range a.root.windows() {
		if ow.Buf.Edit == e {
			a.toWindow(ow)
		}
	}
}

// bindDiff keeps the window beside a diff on the same rows as the focused
// one, as Neovim's scrollbind and cursorbind do, and both sides at the
// narrower width.
// It returns the windows that follow.
func (a *App) bindDiff() []*Window {
	var out []*Window
	for _, w := range a.root.windows() {
		e := w.Buf.Edit
		if e == nil || e.diff == nil || a.root.find(e.diff.win) == nil {
			continue
		}
		tw := e.diff.win
		if tw := min(a.textWidth(w), a.textWidth(tw)); tw != e.diff.w {
			e.diff.w, e.cacheW = tw, 0
		}
		lead, follow := w, tw
		if a.cur == tw {
			lead, follow = tw, w
		}
		follow.Top, follow.Cur = lead.Top, lead.Cur
		out = append(out, follow)
	}
	return out
}
