package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// The waits list is Vim's quickfix list for the person's attention: what
// needs them in every workspace, in the hub's order, then reviews of news
// they have not read. ]q and [q, :cn and :cp walk it; :waits, :copen and
// <leader>pq show it as the focus list, :waits! oldest first.

func waitKey(w model.Wait) string { return w.Workspace + "\x00" + w.Thread + "\x00" + w.ID }

func (a *App) loadWaits() {
	ws, err := a.c.Waits()
	if err != nil {
		a.notify(err.Error(), a.th.Alert)
		return
	}
	a.setWaits(ws)
	// A hub older than the status has none to give; that is no error.
	if s, err := a.c.Status(); err == nil {
		a.status = s
	}
}

func (a *App) setWaits(ws []model.Wait) {
	a.waits = ws
	a.followWaits()
}

// waiting counts what blocks on the person: a review only waits to be
// read, a suggested edit of the focus to be taken or left, and a place to
// go to be gone to.
func (a *App) waiting() int {
	n := 0
	for _, w := range a.waits {
		if blocks(w) {
			n++
		}
	}
	return n
}

func blocks(w model.Wait) bool {
	return w.Kind != model.WaitReview && w.Kind != model.WaitFocus && w.Kind != model.WaitNext
}

func (a *App) waitIndex(key string) int {
	return slices.IndexFunc(a.waits, func(w model.Wait) bool { return waitKey(w) == key })
}

// nextWait goes n waits on from the last one ]q went to, and opens it.
func (a *App) nextWait(n int) {
	if len(a.waits) == 0 {
		a.notify("nothing waits for you", a.th.Fg3)
		return
	}
	i := a.waitIndex(a.waitAt)
	switch {
	case i < 0 && n > 0:
		i = n - 1
	case i < 0:
		i = len(a.waits) + n
	default:
		i += n
	}
	if i < 0 || i >= len(a.waits) {
		a.notify("no more waits", a.th.Fg3)
		return
	}
	a.jumpWait(i)
}

// jumpWait opens the thread of a wait, at its end, where its card is.
func (a *App) jumpWait(i int) {
	w := a.waits[i]
	a.waitAt = waitKey(w)
	a.landed.workspace, a.landed.thread, a.landed.brief = w.Workspace, w.Thread, ""
	if w.Kind != model.WaitPermission && w.Kind != model.WaitQuestion {
		a.landed.brief = w.Brief
	}
	if w.Workspace == model.HubScope {
		a.openSuggestion()
		return
	}
	a.goThread(w.Workspace, w.Thread)
	if a.workspace != w.Workspace || a.cur.Buf.Kind != KindChat || a.cur.Buf.Thread != w.Thread {
		return
	}
	a.leavePrompt(a.cur)
	a.cur.Follow = true
	a.notify(fmtWait(i, len(a.waits), w), a.th.Fg3)
}

// fmtWait is a wait as the command line says it after a jump, as Vim's
// (2 of 5).
// The attention agent's why says more than the text, which the card
// shows.
func fmtWait(i, n int, w model.Wait) string {
	about := w.Text
	if w.Why != "" {
		about = w.Why
	}
	return fmt.Sprintf("(%d of %d) %s · %s", i+1, n, w.Kind, flatten(about))
}

// brief is the attention agent's brief of what a thread of the open
// workspace waits for, for its card; empty without the agent.
func (a *App) brief(thread string) string {
	for _, w := range a.waits {
		if w.Workspace == a.workspace && w.Thread == thread && (w.Kind == model.WaitPermission || w.Kind == model.WaitQuestion) {
			return w.Brief
		}
	}
	return ""
}

// dismissWait drops a notice or a failure from the list, and reads a
// review; a permission or a question is answered.
func (a *App) dismissWait(key string) {
	i := a.waitIndex(key)
	if i < 0 {
		return
	}
	w := a.waits[i]
	if w.Kind == model.WaitPermission || w.Kind == model.WaitQuestion {
		a.notify("answer it in its thread: <CR> goes there", a.th.Fg3)
		return
	}
	if err := a.c.Dismiss(w.Workspace, w.Thread, w.ID); err != nil {
		a.notify(err.Error(), a.th.Alert)
	}
}

// waitGlyph is a wait's glyph and the ink of its kind.
func waitGlyph(th *Theme, k model.WaitKind) (Span, tcell.Style) {
	switch k {
	case model.WaitFailed:
		return Span{"✗ ", th.Alert}, th.Alert
	case model.WaitNotice:
		return Span{"↗ ", th.Wait}, th.Wait
	case model.WaitReview:
		return Span{"✓ ", th.Done}, th.Done
	case model.WaitFocus:
		return Span{"◇ ", th.Wait}, th.Wait
	case model.WaitNext:
		return Span{"→ ", th.Fg2}, th.Fg2
	}
	return Span{"◆ ", th.Wait}, th.Wait
}

// waitName is the name a wait goes by from the open workspace.
func waitName(open string, w model.Wait) string {
	switch {
	case w.Workspace == model.HubScope:
		return model.FocusDoc
	case w.Workspace != open:
		return w.Workspace + " · " + w.Thread
	}
	return w.Thread
}

// openWaits opens the focus list: the waits list as a picker, in the order
// ]q walks it, or oldest first. It follows
// the hub while it is open: a row moves only when a wait comes, goes or
// changes there, and one that goes stays, muted, until the list closes.
func (a *App) openWaits(byAge bool) {
	if len(a.waits) == 0 {
		a.notify("nothing waits for you", a.th.Fg3)
		return
	}
	a.openPicker("next", func(q string) []pickItem { return fuzzy(a.waitItems(byAge), q) })
	a.picker.waits = true
}

func (a *App) waitItems(byAge bool) []pickItem {
	ws := a.waits
	if byAge {
		ws = slices.Clone(ws)
		slices.SortStableFunc(ws, func(x, y model.Wait) int { return x.Since.Compare(y.Since) })
	}
	var items []pickItem
	for _, w := range ws {
		glyph, _ := waitGlyph(a.th, w.Kind)
		glyph.Text = strings.TrimSpace(glyph.Text)
		about := w.Text
		if w.Brief != "" {
			about = w.Brief
		}
		detail := ago(a.now().Sub(w.Since)) + "  " + string(w.Kind) + " · " + flatten(about)
		if w.Kind == model.WaitNext {
			// It waited for no one, and its glyph says what it is.
			detail = flatten(about)
		}
		key := waitKey(w)
		items = append(items, pickItem{
			glyph:  glyph,
			label:  waitName(a.workspace, w),
			detail: detail,
			wait:   key,
			run: func() {
				switch i := a.waitIndex(key); {
				case i >= 0:
					a.jumpWait(i)
				case w.Workspace == model.HubScope:
					a.openDoc(model.HubScope, model.FocusDoc)
				default:
					a.goThread(w.Workspace, w.Thread)
				}
			},
		})
	}
	return items
}

// followWaits brings the open focus list up to the hub's waits.
// The cursor stays on its row.
func (a *App) followWaits() {
	p := a.picker
	if p == nil || !p.waits {
		return
	}
	at := ""
	if p.sel < len(p.items) {
		at = p.items[p.sel].wait
	}
	old := p.items
	p.items = p.source(string(p.query))
	for i, it := range old {
		if !slices.ContainsFunc(p.items, func(n pickItem) bool { return n.wait == it.wait }) {
			it.done = true
			p.items = slices.Insert(p.items, min(i, len(p.items)), it)
		}
	}
	p.all = max(p.all, len(p.items))
	if i := slices.IndexFunc(p.items, func(it pickItem) bool { return it.wait == at }); i >= 0 {
		p.sel = i
	}
	p.sel = min(p.sel, max(len(p.items)-1, 0))
}

// see tells the hub which chats the person saw: on screen, in a terminal
// with focus, for model.SeenDwell without a break. It quiets what came
// since at levels 1 and 2; what needs the person stays until resolved.
func (a *App) see(now time.Time) {
	on := map[string]bool{}
	if a.blurAt.IsZero() && a.root != nil {
		for _, w := range a.root.windows() {
			if w.Buf.Kind == KindChat && w.Buf.Workspace == a.workspace {
				on[w.Buf.Thread] = true
			}
		}
	}
	for k := range a.seenFrom {
		if !on[k] {
			delete(a.seenFrom, k)
			delete(a.seenSent, k)
		}
	}
	for t := range on {
		v := a.threads[t]
		if v.Level != model.LevelAmbient && v.Level != model.LevelNotice {
			delete(a.seenSent, t)
			if v.Level == model.LevelQuiet {
				a.seenFrom[t] = now
			}
			continue
		}
		since, ok := a.seenFrom[t]
		if !ok {
			a.seenFrom[t] = now
			continue
		}
		if !a.seenSent[t] && now.Sub(since) >= model.SeenDwell {
			a.seenSent[t] = true
			if err := a.c.Seen(a.workspace, t, now); err != nil {
				a.notify(err.Error(), a.th.Alert)
			}
		}
	}
}

// statusWhere is the widest the where column of the status gets.
const statusWhere = 20

// statusLines is how all the work stands, as columns: where, then its parts
// joined by ·. A workspace where something waits on the person is gold.
func (a *App) statusLines() [][]Span {
	th := a.th
	w := 0
	for _, l := range a.status.Lines {
		w = max(w, min(width(l.Where), statusWhere))
	}
	var out [][]Span
	for _, l := range a.status.Lines {
		var line []Span
		if w > 0 {
			st := th.Fg3
			if slices.ContainsFunc(a.waits, func(x model.Wait) bool {
				return x.Workspace == l.Where && blocks(x)
			}) {
				st = th.Wait
			}
			line = append(line, Span{pad(truncate(l.Where, w), w+2), st})
		}
		out = append(out, append(line, Span{strings.Join(l.Parts, " · "), th.Fg2}))
	}
	return out
}

// backLines is the card the person sees when they come back: how all the
// work stands, in the attention agent's words or else as counts, and the
// next things, the top of the waits list. Empty when there is nothing to
// say.
func (a *App) backLines() [][]Span {
	th := a.th
	lines := a.statusLines()
	if len(lines) == 0 {
		var parts []string
		count := func(n int, what string) {
			if n > 0 {
				parts = append(parts, fmt.Sprintf(what, n))
			}
		}
		read, failed := 0, 0
		for _, w := range a.waits {
			switch w.Kind {
			case model.WaitReview:
				read++
			case model.WaitFailed:
				failed++
			}
		}
		count(a.waiting()-failed, "◆ %d waiting")
		count(failed, "✗ %d failed")
		count(read, "✓ %d to read")
		if len(parts) > 0 {
			lines = append(lines, []Span{{strings.Join(parts, " · "), th.Fg2}})
		}
	}
	next := a.waits[:min(len(a.waits), backNext)]
	col := 0
	for _, w := range next {
		col = max(col, min(width(waitName(a.workspace, w)), statusWhere))
	}
	for _, w := range next {
		glyph, _ := waitGlyph(th, w.Kind)
		about := w.Text
		if w.Brief != "" {
			about = w.Brief
		} else if w.Why != "" {
			about = w.Why
		}
		lines = append(lines, []Span{glyph, {pad(truncate(waitName(a.workspace, w), col), col+2), th.Fg}, {flatten(about), th.Fg2}})
	}
	return lines
}

// backNext is how many next things the come-back card names.
const backNext = 3

// drawBack draws the card of backLines at the top right, where toasts go,
// and says how many rows it took.
func (a *App) drawBack(W int) int {
	lines := a.backLines()
	if !a.backShown || len(lines) == 0 {
		return 0
	}
	tw := min(backWidth, W-2*margin)
	if tw < 20 {
		return 0
	}
	x0, h := W-tw-margin, len(lines)+2
	a.box(x0, 1, tw, h, "", a.th.Fg3)
	for i, l := range lines {
		x := x0 + 2
		for _, sp := range l {
			x = a.put(x, 2+i, x0+tw-2, truncate(sp.Text, max(x0+tw-2-x, 0)), sp.Style)
		}
	}
	return h
}

// backWidth fits a status line: its where column, at most statusWhere,
// and parts the agent keeps under 60 columns.
const backWidth = 86
