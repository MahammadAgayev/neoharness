package tui

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// The frame scheduler spends the person's attention, and bytes over ssh, on
// purpose: a key draws at once; hub events draw at most frameEvery; a timer
// wakes the loop only when a cell will change: the live tool row's spinner,
// the running glyph's pulse, or a time that turns a minute, or a board
// row's time that turns its unit.
const (
	frameEvery = time.Second / 8
	spinEvery  = 250 * time.Millisecond
	pulseEvery = 800 * time.Millisecond
)

// phase counts the periods of every since the Unix epoch, so each
// animation turns on the same clock.
func phase(t time.Time, every time.Duration) int {
	return int(t.UnixNano() / int64(every))
}

func nextPhase(t time.Time, every time.Duration) time.Time {
	return time.Unix(0, int64(phase(t, every)+1)*int64(every))
}

// glyph is a state's glyph; a running one pulses between ● and ◉.
func (a *App) glyph(s model.State) string {
	if s == model.StateRunning && phase(a.now(), pulseEvery)%2 == 1 {
		return "◉"
	}
	return stateGlyph(s)
}

// take handles an event; true when it must draw at once: everything the
// person does.
func (a *App) take(ev tcell.Event) bool {
	a.handle(ev)
	switch ev.(type) {
	case evUpdate, evTick, evRedraw:
		return false
	}
	return true
}

// step handles one event and draws as the loop would.
func (a *App) step(ev tcell.Event) { a.frame(a.take(ev)) }

// frame draws now, later within the budget, or not at all.
func (a *App) frame(urgent bool) {
	if a.quit || !urgent && !a.dirty {
		return
	}
	if due := a.lastFrame.Add(frameEvery); !urgent && a.now().Before(due) {
		a.wake(due)
		return
	}
	a.draw()
}

// wake asks for a frame at t.
func (a *App) wake(t time.Time) {
	now := a.now()
	if !a.wakeAt.IsZero() && a.wakeAt.After(now) && !t.Before(a.wakeAt) {
		return
	}
	a.wakeAt = t
	a.after(t.Sub(now), evTick{t})
}

// scheduleNext sets the timer for the next cell that will change on its
// own. An idle screen sets none.
func (a *App) scheduleNext(now time.Time, live bool) {
	var next time.Time
	consider := func(t time.Time) {
		if next.IsZero() || t.Before(next) {
			next = t
		}
	}
	if live {
		consider(nextPhase(now, spinEvery))
	}
	board := a.boardWin() != nil
	for _, v := range a.threads {
		if v.State == model.StateRunning {
			consider(nextPhase(now, pulseEvery))
		}
		if !v.Since.IsZero() && v.State != model.StateIdle {
			d := now.Sub(v.Since)
			consider(v.Since.Add((d/time.Minute + 1) * time.Minute))
		}
		if !v.Since.IsZero() && board {
			consider(agoTurns(v.Since, now))
		}
	}
	for k, t := range a.seenFrom {
		if l := a.threads[k].Level; !a.seenSent[k] && (l == model.LevelAmbient || l == model.LevelNotice) {
			consider(t.Add(model.SeenDwell))
		}
	}
	for _, t := range a.settleUntil {
		if t.After(now) {
			consider(t)
		}
	}
	if !next.IsZero() {
		a.wake(next)
	}
}
