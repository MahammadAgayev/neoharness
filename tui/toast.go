package tui

import (
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// toastsShown is how many toasts stack at once; older ones stay in
// :messages.
const toastsShown = 3

// toast is a one-line notice about a thread that is not on screen, in the
// foot of the board, or in the status line when the board is closed. It never
// covers the board's rows.
type toast struct {
	workspace, thread string
	glyph             Span
	body              string
	border            tcell.Style
	// life is how long it shows; 0 keeps it while the thread waits for the
	// person and is not on screen. A notice with a life waits while the
	// person types, and its life counts from when it shows, until then.
	life  time.Duration
	shown time.Time
	until time.Time
	// notice is the coordinator's: with until zero it stays until the
	// coordinator is on screen.
	notice bool
	at     time.Time
}

type evRedraw struct{}

func (evRedraw) When() time.Time { return time.Now() }

// toast shows a note about a thread, unless the thread is on screen: then
// the person sees it already. life 0 keeps it while the thread waits.
func (a *App) toast(thread string, glyph Span, body string, border tcell.Style, life time.Duration) {
	a.toastIn(a.workspace, thread, glyph, body, border, life)
}

// toastIn is toast for a thread of any workspace.
func (a *App) toastIn(workspace, thread string, glyph Span, body string, border tcell.Style, life time.Duration) {
	if workspace == a.workspace && a.windowOf(thread) != nil {
		return
	}
	a.addToast(&toast{workspace: workspace, thread: thread, glyph: glyph, body: body, border: border}, life)
}

// awayView toasts the change of a thread in a workspace that is not open.
// Its first view since the start is news only when it waits.
func (a *App) awayView(workspace string, v model.ThreadView) {
	if a.away == nil {
		a.away = map[string]model.ThreadView{}
	}
	key := workspace + "\x00" + v.Name
	old, had := a.away[key]
	a.away[key] = v
	if had || v.State == model.StateWaiting {
		a.toastView(workspace, old, v, true)
	}
}

func (a *App) awayReply(workspace, thread, text string) {
	if a.awayText == nil {
		a.awayText = map[string]string{}
	}
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	a.awayText[workspace+"\x00"+thread] = line
}

// showNotice shows what a coordinator told the person with notify, from any
// workspace. One the person must act on stays until they look.
func (a *App) showNotice(workspace string, e model.Event) {
	if workspace == a.workspace && a.windowOf(model.Coordinator) != nil {
		return
	}
	glyph, border, life := Span{"↗", a.th.Run}, a.th.Run, 8*time.Second
	if e.NeedsYou {
		glyph, border, life = Span{"↗", a.th.Wait}, a.th.Wait, 0
	}
	a.addToast(&toast{workspace: workspace, thread: model.Coordinator, glyph: glyph, body: e.Text, border: border, notice: true}, life)
}

func (a *App) addToast(t *toast, life time.Duration) {
	thread := t.thread
	t.life = life
	// One toast per thread: the newest says where it is now.
	for i, o := range a.toasts {
		if o.workspace == t.workspace && o.thread == thread {
			a.toasts = append(a.toasts[:i], a.toasts[i+1:]...)
			break
		}
	}
	a.toasts = append(a.toasts, t)
	t.at = time.Now()
	a.toasted = append(a.toasted, t)
	a.logMessage(t.workspace+" · "+thread+": "+t.body, t.glyph.Style)
}

// toastView turns a thread's change into a toast. Of a quiet thread only a
// question or a permission request does: the coordinator tells the rest.
func (a *App) toastView(workspace string, old, v model.ThreadView, had bool) {
	toast := func(glyph Span, body string, border tcell.Style, life time.Duration) {
		a.toastIn(workspace, v.Name, glyph, body, border, life)
	}
	switch {
	case v.Quiet && v.State != model.StateWaiting:
	case !had && v.Role == model.RoleThread:
		toast(Span{"+", a.th.Accent}, "opened in "+shortHome(v.Location.String()), a.th.Accent, 4*time.Second)
	case v.State == model.StateWaiting && old.State != model.StateWaiting:
		body := "asks: " + plain(v.Waiting)
		switch {
		case v.Permission != nil:
			body = "needs permission · " + v.Permission.Tool
		case v.Waiting == "paused":
			body = "paused · write to continue"
		}
		toast(Span{"◆", a.th.Wait}, body, a.th.Wait, 0)
	case v.State == model.StateResolved && old.State != model.StateResolved:
		toast(Span{"✓", a.th.Fg3}, "resolved", a.th.Fg3, 4*time.Second)
	case old.State == model.StateResolved && v.State != model.StateResolved:
		// The coordinator brings a thread back by writing to it.
		toast(Span{"↺", a.th.Run}, "reopened", a.th.Run, 4*time.Second)
	case old.State == model.StateRunning && v.State == model.StateIdle && a.showsError(workspace, v.Name):
		// The error came just before the end of its turn, and says more.
	case old.State == model.StateRunning && v.State == model.StateIdle:
		var body string
		life := 4 * time.Second
		if workspace == a.workspace {
			body = plain(a.chat(v.Name).lastText())
		} else {
			// The chat of a workspace that is not open is not loaded, and
			// you are less likely to be looking.
			key := workspace + "\x00" + v.Name
			body, life = plain(a.awayText[key]), 10*time.Second
			delete(a.awayText, key)
			if body == "" {
				body = plain(v.Reply)
			}
		}
		if body == "" {
			body = "finished a turn"
		}
		toast(Span{"✓", a.th.Done}, body, a.th.Done, life)
	}
}

func (a *App) showsError(workspace, thread string) bool {
	return slices.ContainsFunc(a.toasts, func(t *toast) bool {
		return t.workspace == workspace && t.thread == thread && t.glyph.Text == "✗"
	})
}

// pruneToasts drops toasts whose time is up, and waiting ones whose thread
// no longer waits or is now on screen.
func (a *App) pruneToasts() {
	now := a.now()
	keep := a.toasts[:0]
	for _, t := range a.toasts {
		switch {
		case t.life == 0:
			if a.stillWaits(t) {
				keep = append(keep, t)
			}
		case t.shown.IsZero() || now.Before(t.until):
			keep = append(keep, t)
		}
	}
	a.toasts = keep
}

// shownToasts are the toasts to draw now, the newest last. A notice that
// has not shown yet waits while the person types: in Insert or on the
// command line. What needs the person shows at once.
func (a *App) shownToasts() []*toast {
	a.pruneToasts()
	typing := a.mode == modeInsert || a.mode == modeCommand
	var out []*toast
	for _, t := range a.toasts {
		if t.life > 0 && t.shown.IsZero() {
			if typing {
				continue
			}
			t.shown, t.until = a.now(), a.now().Add(t.life)
			a.after(t.life, evRedraw{})
		}
		out = append(out, t)
	}
	return out[max(len(out)-toastsShown, 0):]
}

// toastLine is a toast as one line: glyph, name, text.
func (a *App) toastLine(t *toast) []Span {
	name := t.thread
	if t.workspace != a.workspace {
		name = t.workspace + " · " + name
	}
	return []Span{{t.glyph.Text + " ", t.glyph.Style}, {name + "  ", a.th.Fg2}, {flatten(t.body), a.th.Fg2}}
}

// stillWaits reports whether a waiting toast's thread still waits and is not
// on screen. Of another workspace only its summary is known: something there
// waits.
func (a *App) stillWaits(t *toast) bool {
	if t.notice {
		return t.workspace != a.workspace || a.windowOf(t.thread) == nil
	}
	if t.workspace == a.workspace {
		return a.threads[t.thread].State == model.StateWaiting && a.windowOf(t.thread) == nil
	}
	for _, p := range a.workspaces {
		if p.Name == t.workspace {
			return p.Counts[model.StateWaiting] > 0 || p.Coordinator == model.StateWaiting
		}
	}
	return false
}

// lastText is the first line of the newest reply in the buffer.
func (b *Buffer) lastText() string {
	for i := len(b.blocks) - 1; i >= 0; i-- {
		if bl := b.blocks[i]; bl.kind == bText {
			line, _, _ := strings.Cut(strings.TrimSpace(bl.text), "\n")
			return line
		}
	}
	return ""
}
