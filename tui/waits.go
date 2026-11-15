package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// The waits list is Vim's quickfix list for the person's attention: what
// needs them in every workspace, oldest first, kept by the hub. ]q and [q,
// :cn and :cp walk it; :waits and :copen show it.

func waitKey(w model.Wait) string { return w.Workspace + "\x00" + w.Thread + "\x00" + w.ID }

func (a *App) loadWaits() {
	ws, err := a.c.Waits()
	if err != nil {
		a.notify(err.Error(), a.th.Alert)
		return
	}
	a.setWaits(ws)
}

func (a *App) setWaits(ws []model.Wait) {
	a.waits = ws
	a.renderWaits()
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
func fmtWait(i, n int, w model.Wait) string {
	return fmt.Sprintf("(%d of %d) %s · %s", i+1, n, w.Kind, flatten(w.Text))
}

// dismissWait drops a notice or a failure from the list; a permission or a
// question is answered.
func (a *App) dismissWait(key string) {
	i := a.waitIndex(key)
	if i < 0 {
		return
	}
	w := a.waits[i]
	if w.Kind != model.WaitNotice && w.Kind != model.WaitFailed {
		a.notify("answer it in its thread: <CR> goes there", a.th.Fg3)
		return
	}
	if err := a.c.Dismiss(w.Workspace, w.Thread, w.ID); err != nil {
		a.notify(err.Error(), a.th.Alert)
	}
}

func (a *App) renderWaits() {
	th := a.th
	if len(a.waits) == 0 {
		a.waitsBuf.SetLines([]Line{{Prefix: []Span{{" ", th.Fg}}, Spans: []Span{{"nothing waits for you", th.Fg3}}}})
		return
	}
	tw := 80
	if a.root != nil {
		for _, w := range a.root.windows() {
			if w.Buf == a.waitsBuf {
				tw = a.textWidth(w)
			}
		}
	}
	var lines []Line
	for _, w := range a.waits {
		glyph, ink := Span{"◆ ", th.Wait}, th.Wait
		switch w.Kind {
		case model.WaitFailed:
			glyph, ink = Span{"✗ ", th.Alert}, th.Alert
		case model.WaitNotice:
			glyph = Span{"↗ ", th.Wait}
		}
		name := w.Thread
		if w.Workspace != a.workspace {
			name = w.Workspace + " · " + name
		}
		spans := []Span{glyph, {name + "  ", th.Fg2}, {string(w.Kind) + " · ", ink}}
		at := ago(a.now().Sub(w.Since))
		used := 0
		for _, s := range spans {
			used += width(s.Text)
		}
		// As on the board: the time at the right, one column from the edge.
		text := truncate(flatten(w.Text), tw-1-used-2-width(at))
		spans = append(spans, Span{text, th.Fg}, Span{strings.Repeat(" ", max(tw-1-used-width(text)-width(at), 2)) + at, th.Fg3})
		lines = append(lines, Line{
			Prefix: []Span{{" ", th.Fg}},
			Spans:  spans,
			Item:   waitKey(w),
		})
	}
	a.waitsBuf.SetLines(lines)
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
