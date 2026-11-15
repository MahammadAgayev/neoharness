package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
)

func (h *neoharness) board() *Window {
	for _, w := range h.a.root.windows() {
		if w.Buf.Kind == KindThreads {
			return w
		}
	}
	return nil
}

func TestNoticesDockInTheBoardsFootNeverOverItsRows(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.a.toast("api-tests", Span{"✓", h.a.th.Done}, "Moved the store.", h.a.th.Done, 4*time.Second)
	h.a.draw()
	b := h.board()
	row := h.rowOf("Moved the store.")
	if row != b.rect.Y+b.rect.H-1 {
		t.Fatalf("the notice should be the last row of the board, row %d:\n%s", row, h.screen())
	}
	line := h.line(row)
	if !strings.Contains(line[strings.Index(line, "│"):], "✓ api-tests  Moved the store.") {
		t.Fatalf("the notice should be in the board's column, as glyph, name, text:\n%s", line)
	}
	for _, want := range []string{"auth-refactor", "○ api-tests"} {
		if r := h.rowOf(want); r < 0 || r >= row {
			t.Fatalf("%q should stay visible above the notice:\n%s", want, h.screen())
		}
	}
	if strings.Contains(h.screen(), "╭") {
		t.Fatalf("a notice is a line, not a box:\n%s", h.screen())
	}
}

func TestNoticesShowInTheStatusLineWhenTheBoardIsClosed(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press("<C-w>o")
	if h.board() != nil {
		t.Fatal("<C-w>o should close the board")
	}
	h.a.toast("api-tests", Span{"✓", h.a.th.Done}, "Moved the store.", h.a.th.Done, 4*time.Second)
	h.a.draw()
	_, H := h.s.Size()
	if !strings.Contains(h.line(H-2), "✓ api-tests  Moved the store.") {
		t.Fatalf("with no board the notice goes in the status line:\n%s", h.screen())
	}
}

func TestANoticeWaitsWhileYouTypeAndNeedsYouDoesNot(t *testing.T) {
	c := startClocked(t)
	h := c.h
	h.openDemo()
	h.press("i")
	h.a.toast("api-tests", Span{"✓", h.a.th.Done}, "Moved the store.", h.a.th.Done, 4*time.Second)
	h.a.draw()
	if strings.Contains(h.screen(), "Moved the store.") {
		t.Fatal("a notice should wait while you are in Insert")
	}
	v := h.a.threads["auth-refactor"]
	v.State = model.StateWaiting
	h.a.threads[v.Name] = v
	h.a.toast("auth-refactor", Span{"◆", h.a.th.Wait}, "needs permission · Bash", h.a.th.Wait, 0)
	h.a.draw()
	h.see("needs permission · Bash")
	c.now = c.now.Add(10 * time.Second)
	h.press("<Esc>")
	h.see("Moved the store.")
	h.press(":")
	h.see("Moved the store.")
	c.now = c.now.Add(5 * time.Second)
	h.a.draw()
	if strings.Contains(h.screen(), "Moved the store.") {
		t.Fatal("a notice's 4 s should count from when it showed")
	}
}
