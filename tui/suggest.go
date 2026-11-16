package tui

import (
	"strings"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// openSuggestion opens the focus in diff mode beside the edit the attention
// agent suggests, as :w does beside a newer version: do takes a change, and
// :w saves the focus as it is then. The edit itself is never saved.
func (a *App) openSuggestion() {
	a.openDoc(model.HubScope, model.FocusDoc)
	w := a.cur
	e := w.Buf.Edit
	if w.Buf.Workspace != model.HubScope || e == nil {
		return
	}
	s := e.art.Suggestion
	if s == nil {
		a.notify("no edit of the focus waits", a.th.Fg3)
		return
	}
	if e.diff != nil {
		return
	}
	tb := newBuffer("neoharness://"+model.HubScope+"/"+model.FocusDoc+"@suggested", KindDiff)
	tb.diffOf = e
	tw := newWindow(tb)
	tw.Follow = false
	e.diff = &diffMode{theirs: strings.Split(s.Text, "\n"), version: e.art.Version, thread: "attention", buf: tb, win: tw}
	e.cacheW = 0
	a.root.splitWindow(w, tw, true)
	a.equalAlways(true)
	a.cur = w
	a.toWindow(w)
	a.notify("attention suggests: "+flatten(s.Why)+" · do takes a change, ]c [c move, :w saves", a.th.Wait)
}
