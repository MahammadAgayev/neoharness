package tui

import (
	"io"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/gdamore/tcell/v2/terminfo"
)

// fakeTty is a terminal whose keys the test types, one read per send.
type fakeTty struct {
	in   chan []byte
	done chan struct{}
}

func newFakeTty() *fakeTty {
	return &fakeTty{in: make(chan []byte, 16), done: make(chan struct{})}
}

func (t *fakeTty) Start() error                { return nil }
func (t *fakeTty) Stop() error                 { return nil }
func (t *fakeTty) Drain() error                { close(t.done); return nil }
func (t *fakeTty) NotifyResize(func())         {}
func (t *fakeTty) Write(p []byte) (int, error) { return len(p), nil }
func (t *fakeTty) Close() error                { return nil }
func (t *fakeTty) WindowSize() (tcell.WindowSize, error) {
	return tcell.WindowSize{Width: 80, Height: 24}, nil
}

func (t *fakeTty) Read(p []byte) (int, error) {
	select {
	case b := <-t.in:
		return copy(p, b), nil
	case <-t.done:
		return 0, io.EOF
	}
}

// keysOf starts a terminfo screen on tty, types each read, and returns the
// keys tcell makes of them.
func keysOf(t *testing.T, tty tcell.Tty, reads ...string) []string {
	t.Helper()
	ti, err := terminfo.LookupTerminfo("xterm-256color")
	if err != nil {
		t.Fatal(err)
	}
	s, err := tcell.NewTerminfoScreenFromTtyTerminfo(tty, ti)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	defer s.Fini()
	f := tty.(*escTty).Tty.(*fakeTty)
	for _, r := range reads {
		f.in <- []byte(r)
	}
	var keys []string
	deadline := time.After(2 * time.Second)
	for len(keys) < 3 {
		ch := make(chan tcell.Event, 1)
		go func() { ch <- s.PollEvent() }()
		select {
		case ev := <-ch:
			if k, ok := ev.(*tcell.EventKey); ok {
				if k.Key() == tcell.KeyRune {
					keys = append(keys, string(k.Rune()))
				} else {
					keys = append(keys, k.Name())
				}
			}
		case <-deadline:
			return keys
		}
	}
	return keys
}

// tcell reads ESC then ] as the start of an OSC string and swallows every
// key until it ends, so <Esc>]q, typed fast or sent at once, froze the TUI.
// The Esc stays a key of its own.
func TestEscThenBracketStaysKeys(t *testing.T) {
	for _, reads := range [][]string{{"\x1b]q"}, {"\x1b", "]q"}} {
		got := keysOf(t, &escTty{Tty: newFakeTty()}, reads...)
		if len(got) != 3 || got[0] != "Esc" || got[1] != "]" || got[2] != "q" {
			t.Fatalf("%q gave keys %q, want Esc ] q", reads, got)
		}
	}
	// A whole OSC string from the terminal still reaches tcell whole.
	got := keysOf(t, &escTty{Tty: newFakeTty()}, "\x1b]52;c;aGk=\x07abc")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("an OSC reply gave keys %q, want a b c", got)
	}
}
