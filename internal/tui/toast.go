package tui

import (
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/internal/model"
)

// toastsShown is how many toasts stack at once; older ones stay in
// :messages.
const toastsShown = 3

// toast is a small note in the top right corner about a thread that is not
// on screen, like snacks.nvim's notifier.
type toast struct {
	workspace, thread string
	glyph             Span
	body              string
	border            tcell.Style
	// until is when it goes away. Zero keeps it while the thread waits for
	// the person and is not on screen.
	until time.Time
	// notice is the coordinator's: with until zero it stays until the
	// coordinator is on screen.
	notice bool
}

type evRedraw struct{}

func (evRedraw) When() time.Time { return time.Now() }

// toast shows a note about a thread, unless the thread is on screen: then
// the person sees it already. life 0 keeps it while the thread waits.
func (a *App) toast(thread string, glyph Span, body string, border tcell.Style, life time.Duration) {
	if a.windowOf(thread) != nil {
		return
	}
	a.addToast(&toast{workspace: a.workspace, thread: thread, glyph: glyph, body: body, border: border}, life)
}

// showNotice shows what a coordinator told the person with notify, from any
// workspace. One the person must act on stays until they look.
func (a *App) showNotice(workspace string, e model.Event) {
	if workspace == a.workspace && a.windowOf(model.Coordinator) != nil {
		return
	}
	glyph, border, life := Span{"↗", a.th.Foam}, a.th.Foam, 8*time.Second
	if e.NeedsYou {
		glyph, border, life = Span{"↗", a.th.Gold}, a.th.Gold, 0
	}
	a.addToast(&toast{workspace: workspace, thread: model.Coordinator, glyph: glyph, body: e.Text, border: border, notice: true}, life)
}

func (a *App) addToast(t *toast, life time.Duration) {
	thread := t.thread
	if life > 0 {
		t.until = time.Now().Add(life)
		time.AfterFunc(life, func() { _ = a.s.PostEvent(evRedraw{}) })
	}
	// One toast per thread: the newest says where it is now.
	for i, o := range a.toasts {
		if o.workspace == t.workspace && o.thread == thread {
			a.toasts = append(a.toasts[:i], a.toasts[i+1:]...)
			break
		}
	}
	a.toasts = append(a.toasts, t)
	a.logMessage(t.workspace+" · "+thread+": "+t.body, t.glyph.Style)
}

// toastView turns a thread's change into a toast. Of a quiet thread only a
// question or a permission request does: the coordinator tells the rest.
func (a *App) toastView(old, v model.ThreadView, had bool) {
	switch {
	case v.Quiet && v.State != model.StateWaiting:
	case !had && v.Role == model.RoleThread:
		a.toast(v.Name, Span{"+", a.th.Iris}, "opened in "+shortHome(v.Location.String()), a.th.Iris, 4*time.Second)
	case v.State == model.StateWaiting && old.State != model.StateWaiting:
		body := "asks: " + plain(v.Waiting)
		switch {
		case v.Permission != nil:
			body = "needs permission · " + v.Permission.Tool
		case v.Waiting == "paused":
			body = "paused · write to continue"
		}
		a.toast(v.Name, Span{"◆", a.th.Gold}, body, a.th.Gold, 0)
	case v.State == model.StateResolved && old.State != model.StateResolved:
		a.toast(v.Name, Span{"✓", a.th.Muted}, "resolved", a.th.Muted, 4*time.Second)
	case old.State == model.StateRunning && v.State == model.StateIdle:
		body := plain(a.chat(v.Name).lastText())
		if body == "" {
			body = "finished a turn"
		}
		a.toast(v.Name, Span{"✓", a.th.Pine}, body, a.th.Pine, 4*time.Second)
	}
}

// pruneToasts drops toasts whose time is up, and waiting ones whose thread
// no longer waits or is now on screen.
func (a *App) pruneToasts() {
	now := time.Now()
	keep := a.toasts[:0]
	for _, t := range a.toasts {
		if t.until.IsZero() {
			if a.stillWaits(t) {
				keep = append(keep, t)
			}
		} else if now.Before(t.until) {
			keep = append(keep, t)
		}
	}
	a.toasts = keep
}

func (a *App) drawToasts(W, H int) {
	a.pruneToasts()
	tw := min(56, W-4)
	if tw < 20 {
		return
	}
	x0, y := W-tw-margin, 1
	shown := a.toasts[max(len(a.toasts)-toastsShown, 0):]
	for i := len(shown) - 1; i >= 0 && y+4 < H; i-- {
		t := shown[i]
		a.box(x0, y, tw, 4, "", t.border)
		x := a.put(x0+2, y+1, x0+tw-2, t.glyph.Text+" ", t.glyph.Style)
		x = a.put(x, y+1, x0+tw-2, t.workspace+" · ", a.th.Subtle)
		a.put(x, y+1, x0+tw-2, t.thread, a.th.Text.Bold(true))
		a.put(x0+4, y+2, x0+tw-2, truncate(flatten(t.body), tw-6), a.th.Subtle)
		y += 4
	}
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
