package tui

import (
	"bytes"
	"time"

	"github.com/gdamore/tcell/v2"
)

// escTty keeps a typed Esc apart from a ] after it. tcell reads ESC ] as
// the start of an OSC string, which only BEL or ESC \ ends, so <Esc>]q,
// typed fast or sent at once, swallowed every key after it and the TUI
// looked frozen. Here the ] waits until tcell has taken the Esc as a key
// (it does after 50ms). neoharness asks the terminal for no OSC reply, and
// one would hold its end in the same read, so that one passes.
type escTty struct {
	tcell.Tty
	rest []byte
	hold time.Time // the ] after a lone Esc comes no sooner
}

// newScreen is tcell's screen on /dev/tty, read through escTty.
func newScreen() (tcell.Screen, error) {
	tty, err := tcell.NewDevTty()
	if err != nil {
		return tcell.NewScreen()
	}
	return tcell.NewTerminfoScreenFromTty(&escTty{Tty: tty})
}

// escHold is longer than tcell's own Esc timeout.
const escHold = 100 * time.Millisecond

func (t *escTty) Read(p []byte) (int, error) {
	if len(t.rest) == 0 {
		n, err := t.Tty.Read(p)
		if err != nil || n == 0 {
			return n, err
		}
		t.rest = append(t.rest[:0], p[:n]...)
	}
	if t.rest[0] == ']' {
		time.Sleep(time.Until(t.hold))
	}
	n := len(t.rest)
	if i := openOSC(t.rest); i >= 0 {
		n = i + 1
	}
	n = copy(p, t.rest[:n])
	t.rest = t.rest[n:]
	t.hold = time.Time{}
	if p[n-1] == 0x1b {
		t.hold = time.Now().Add(escHold)
	}
	return n, nil
}

// openOSC is where ESC ] starts in b with no BEL or ESC \ after it, or -1.
func openOSC(b []byte) int {
	i := bytes.Index(b, []byte("\x1b]"))
	if i < 0 {
		return -1
	}
	end := b[i+2:]
	if bytes.IndexByte(end, '\a') >= 0 || bytes.Contains(end, []byte("\x1b\\")) {
		return -1
	}
	return i
}
