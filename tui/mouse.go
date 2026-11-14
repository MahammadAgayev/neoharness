package tui

import (
	"time"

	"github.com/gdamore/tcell/v2"
)

// doubleClick is the most time between two clicks on one cell that make a
// double click; the terminal sends only presses and releases.
const doubleClick = 400 * time.Millisecond

// wheelLines is how far one wheel step moves.
const wheelLines = 3

// mouse handles a click or a wheel step. A click focuses the window under it
// and puts the cursor there; a double click is <CR> there. The wheel moves
// the window under the pointer.
func (a *App) mouse(ev *tcell.EventMouse) {
	btn := ev.Buttons()
	pressed := btn&tcell.Button1 != 0 && !a.mouseDown
	a.mouseDown = btn&tcell.Button1 != 0
	if a.picker != nil || a.mode == modeCommand {
		return
	}
	x, y := ev.Position()
	w := a.windowAt(x, y)
	if w == nil {
		return
	}
	switch {
	case btn&tcell.WheelUp != 0:
		a.wheel(w, -wheelLines)
	case btn&tcell.WheelDown != 0:
		a.wheel(w, wheelLines)
	case pressed:
		a.click(w, x, y, ev.When())
	}
}

func (a *App) windowAt(x, y int) *Window {
	for _, w := range a.root.windows() {
		r := w.rect
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			return w
		}
	}
	return nil
}

// normal leaves Insert or Visual in the window with focus, as <Esc> does.
func (a *App) normal() {
	if a.mode != modeNormal {
		a.key(keyEvent("<Esc>"))
	}
}

func (a *App) wheel(w *Window, d int) {
	if w == a.cur {
		a.normal()
	}
	a.leavePrompt(w)
	a.move(w, d)
}

func (a *App) click(w *Window, x, y int, when time.Time) {
	double := x == a.lastClickX && y == a.lastClickY && when.Sub(a.lastClick) < doubleClick
	a.lastClick, a.lastClickX, a.lastClickY = when, x, y
	a.normal()
	a.cur = w
	r := w.rect
	row := y - r.Y
	switch {
	case y == r.Y+r.H-1:
		// The statusline only focuses.
	case row < w.textH:
		if w.Top+row >= w.linesN {
			return
		}
		a.leavePrompt(w)
		a.moveTo(w, w.Top+row)
		w.Col = x - r.X - railWidth(w)
		a.clampCol(w)
		if double {
			a.enter()
		}
	case w.Buf.Prompt != nil:
		a.insert('i')
	}
}
