package tui

import "github.com/gdamore/tcell/v2"

// canvas keeps a frame of its own in front of tcell. tcell's Clear marks every
// cell dirty, so a frame that clears and draws the same text again sent the
// whole screen: each spinner frame was more than a kilobyte. canvas collects
// a frame and passes on only the cells that changed since the last one.
//
// It also draws the cursor itself, in reverse video, and keeps the terminal's
// cursor hidden. tcell hides, moves, shows and restyles the cursor on every
// frame; without synchronized output (tmux without the sync feature) the
// terminal shows each step, and the cursor shivers while a spinner runs.
type canvas struct {
	tcell.Screen
	w, h       int
	next, last []cell
	cx, cy     int
}

type cell struct {
	r    rune
	comb string
	st   tcell.Style
}

func newCanvas(s tcell.Screen) *canvas {
	s.HideCursor()
	return &canvas{Screen: s, cx: -1, cy: -1}
}

func (c *canvas) Clear() {
	w, h := c.Screen.Size()
	if w != c.w || h != c.h {
		c.w, c.h = w, h
		c.next, c.last = make([]cell, w*h), nil
	}
	for i := range c.next {
		c.next[i] = cell{r: ' '}
	}
	c.cx, c.cy = -1, -1
}

func (c *canvas) SetContent(x, y int, r rune, comb []rune, st tcell.Style) {
	if x >= 0 && y >= 0 && x < c.w && y < c.h {
		c.next[y*c.w+x] = cell{r, string(comb), st}
	}
}

func (c *canvas) ShowCursor(x, y int) { c.cx, c.cy = x, y }

func (c *canvas) HideCursor() { c.cx, c.cy = -1, -1 }

func (c *canvas) Show() {
	if c.cx >= 0 && c.cy >= 0 && c.cx < c.w && c.cy < c.h {
		i := c.cy*c.w + c.cx
		c.next[i].st = c.next[i].st.Reverse(true)
	}
	for i, n := range c.next {
		if c.last != nil && c.last[i] == n {
			continue
		}
		c.Screen.SetContent(i%c.w, i/c.w, n.r, []rune(n.comb), n.st)
	}
	c.last = append(c.last[:0], c.next...)
	c.Screen.Show()
}

// Sync draws the whole screen again, after a resize or when the terminal
// lost what was on it.
func (c *canvas) Sync() {
	c.last = nil
	c.Screen.Sync()
}
