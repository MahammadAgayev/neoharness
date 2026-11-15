package tui

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/api/model"
)

type timer struct {
	at time.Time
	ev tcell.Event
	id int
}

// clocked runs the app on a clock of its own: timers wait in a list, and run
// fires them and the hub events in time order, as the loop would.
type clocked struct {
	h      *neoharness
	now    time.Time
	timers []timer
	ids    int
}

func startClocked(t *testing.T) *clocked {
	h := start(t)
	c := &clocked{h: h, now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}
	h.a.now = func() time.Time { return c.now }
	h.a.after = func(d time.Duration, ev tcell.Event) {
		c.ids++
		c.timers = append(c.timers, timer{c.now.Add(d), ev, -c.ids})
	}
	return c
}

// run moves the clock d on, with the events at their times.
func (c *clocked) run(d time.Duration, events []timer) {
	end := c.now.Add(d)
	for {
		all := append(append([]timer{}, events...), c.timers...)
		sort.SliceStable(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
		if len(all) == 0 || all[0].at.After(end) {
			break
		}
		next := all[0]
		if i := indexOf(events, next); i >= 0 {
			events = append(events[:i], events[i+1:]...)
		} else {
			i := indexOf(c.timers, next)
			c.timers = append(c.timers[:i], c.timers[i+1:]...)
		}
		c.now = next.at
		c.h.a.step(next.ev)
	}
	c.now = end
}

func indexOf(ts []timer, t timer) int {
	for i := range ts {
		if ts[i].id == t.id {
			return i
		}
	}
	return -1
}

func TestALiveTurnDrawsAtMostEightFramesASecond(t *testing.T) {
	c := startClocked(t)
	c.h.openDemo()
	c.h.press(":b auth-refactor", "<CR>")
	if !c.h.a.cur.Buf.Live() {
		t.Fatal("the running thread should have a live tool row")
	}
	// 30 hub events a second for 10 seconds, as a busy turn sends.
	var events []timer
	for i := 0; i < 300; i++ {
		e := model.Event{Kind: model.EventText, Text: "more"}
		events = append(events, timer{c.now.Add(time.Duration(i) * time.Second / 30), evUpdate{model.Update{Kind: model.UpdateEvent, Workspace: "demo", Thread: "auth-refactor", Event: &e}}, i + 1})
	}
	c.h.a.frames = 0
	c.run(10*time.Second, events)
	if n := c.h.a.frames; n > 80 || n < 20 {
		t.Fatalf("10 s of a live turn drew %d frames, want 20 to 80", n)
	}
	c.h.see("more")
}

func TestAKeyDrawsAtOnceEvenInsideTheBudget(t *testing.T) {
	c := startClocked(t)
	c.h.openDemo()
	c.h.a.step(evUpdate{})
	before := c.h.a.frames
	c.h.a.step(tcell.NewEventKey(tcell.KeyRune, 'k', 0))
	if c.h.a.frames != before+1 {
		t.Fatal("a key should draw in the same frame")
	}
}

func TestAnIdleWorkspaceSchedulesOnlyTheBoardsTimes(t *testing.T) {
	c := startClocked(t)
	c.h.openDemo()
	v := c.h.a.threads["auth-refactor"]
	v.State = model.StateIdle
	c.h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	b := c.h.a.chat("auth-refactor")
	for _, bl := range b.blocks {
		bl.live = false
	}
	c.h.a.toasts = nil
	c.h.a.settleUntil = map[string]time.Time{}
	c.timers = nil
	c.h.a.wakeAt = time.Time{}
	c.now = c.now.Add(time.Second)
	c.h.a.step(evUpdate{})
	// Only the board's times turn, at most once a minute.
	if len(c.timers) > 1 || len(c.timers) == 1 && c.timers[0].at.Sub(c.now) < 59*time.Second {
		t.Fatalf("nothing else moves on an idle screen, yet timers wait: %+v", c.timers)
	}
	c.h.press("<C-w>o")
	c.timers = nil
	c.h.a.wakeAt = time.Time{}
	c.now = c.now.Add(time.Second)
	c.h.a.step(evUpdate{})
	if len(c.timers) != 0 {
		t.Fatalf("with the board closed nothing moves, yet %d timers wait", len(c.timers))
	}
}

func TestTheRunningGlyphPulsesSlowlyAndTimesTickByTheMinute(t *testing.T) {
	c := startClocked(t)
	c.h.openDemo()
	v := c.h.a.threads["auth-refactor"]
	v.Since = c.now
	c.h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	c.h.a.step(evUpdate{})
	c.h.see("● auth-refactor")
	c.h.see("now")
	c.run(800*time.Millisecond, nil)
	c.h.see("◉ auth-refactor")
	c.run(800*time.Millisecond, nil)
	c.h.see("● auth-refactor")
	c.run(time.Minute, nil)
	c.h.see("1m")
	if s := c.h.screen(); strings.Contains(s, "1m0") {
		t.Fatalf("times should not show seconds:\n%s", s)
	}
}

func TestTheCursorIsABlockInNormalAndALineInInsert(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press("k")
	cv := h.a.s
	if cv.bar {
		t.Fatal("Normal mode should have a block cursor")
	}
	cx, cy := cv.cx, cv.cy
	cells, w, _ := h.s.GetContents()
	if _, _, attrs := cells[cy*w+cx].Style.Decompose(); attrs&tcell.AttrReverse == 0 {
		t.Fatal("the block should be drawn on its cell")
	}
	if _, _, vis := h.s.GetCursor(); vis {
		t.Fatal("in Normal mode the terminal's own cursor stays hidden")
	}
	h.press("i")
	if !cv.bar {
		t.Fatal("Insert should have a line cursor")
	}
	x, y, vis := h.s.GetCursor()
	if !vis || y != h.rowOf("› write to coordinator") || x != h.a.cursorX {
		t.Fatalf("in Insert the terminal's cursor, a steady bar, should be at the prompt; at %d,%d visible %v", x, y, vis)
	}
	cells, w, _ = h.s.GetContents()
	if _, _, attrs := cells[y*w+x].Style.Decompose(); attrs&tcell.AttrReverse != 0 {
		t.Fatal("a line cursor draws no block")
	}
	h.press("<Esc>")
	if cv.bar {
		t.Fatal("Esc should give the block back")
	}
}
