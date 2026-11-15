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
	h.a.showNotice("ops", model.Event{Text: "needs you · Bash", NeedsYou: true})
	h.a.draw()
	h.see("needs you · Bash")
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

func (h *neoharness) setThread(name string, f func(v *model.ThreadView)) {
	v := h.a.threads[name]
	f(&v)
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.a.draw()
}

func TestNeedsYouRowsArePinnedOnTopWithWhatTheyNeed(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.setThread("api-tests", func(v *model.ThreadView) {
		v.State = model.StateWaiting
		v.Permission = &model.Permission{ID: "p1", Tool: "Bash", Detail: `psql -c "alter table"`}
	})
	head, row, detail, running := h.rowOf("waiting · 1"), h.rowOf("◆ api-tests"), h.rowOf("permission · Bash"), h.rowOf("running · 1")
	if head < 0 || row != head+1 || detail != row+1 || running < detail {
		t.Fatalf("the waiting thread should be on top, with what it needs under it; rows %d %d %d %d:\n%s", head, row, detail, running, h.screen())
	}
	h.see(`permission · Bash  psql -c "alter table"`)
	if h.fgAt("permission · Bash") != fgOf(h.a.th.Wait) || h.fgAt("psql -c") != fgOf(h.a.th.Fg) {
		t.Fatal("the label is in wait, the command in fg, to be read")
	}
}

func TestAFailedThreadShowsFailedNotIdle(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.setThread("api-tests", func(v *model.ThreadView) { v.State = stateFailed })
	head, row := h.rowOf("failed · 1"), h.rowOf("✗ api-tests")
	if head < 0 || row != head+1 || row > h.rowOf("running · 1") {
		t.Fatalf("a failed thread should be on top, in its own group:\n%s", h.screen())
	}
	if h.fgAt("✗ api-tests") != fgOf(h.a.th.Alert) {
		t.Fatal("failed is in alert")
	}
	if strings.Contains(h.screen(), "idle · 2") {
		t.Fatal("a failed thread is not idle")
	}
	h.press(":b api-tests", "<CR>", "<C-w>w")
	h.see("✗ failed")
	if h.fgAt("✗ failed") != fgOf(h.a.th.Alert) {
		t.Fatal("a failure keeps its color in a winbar without focus")
	}
}

func TestBoardRowsShowNoIdleSnippetAndKShowsIt(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.setThread("api-tests", func(v *model.ThreadView) { v.Reply = "Moved the store." })
	if strings.Contains(h.screen(), "Moved the store.") {
		t.Fatal("the board scans as names: no idle snippet")
	}
	h.press("<C-w>l")
	for h.a.line(h.a.cur).Item != "api-tests" {
		h.press("j")
	}
	h.press("K")
	_, H := h.s.Size()
	if !strings.Contains(h.line(H-1), "Moved the store.") {
		t.Fatalf("K should show the row's last reply, as Vim's hover:\n%s", h.screen())
	}
}

func TestABoardRowSettlesWithATintAndWaitsWhileYouAreOnTheBoard(t *testing.T) {
	c := startClocked(t)
	h := c.h
	h.openDemo()
	tinted := func(text string) bool {
		cells, w, ht := h.s.GetContents()
		x0 := h.board().rect.X
		for y := 0; y < ht; y++ {
			row := []rune(h.line(y))[x0:]
			if i := strings.Index(string(row), text); i >= 0 {
				x := x0 + len([]rune(string(row)[:i]))
				_, bg, _ := cells[y*w+x].Style.Decompose()
				_, want, _ := h.a.th.CursorLine.Decompose()
				return bg == want
			}
		}
		t.Fatalf("%q is not on the board", text)
		return false
	}
	h.setThread("api-tests", func(v *model.ThreadView) { v.State = model.StateRunning })
	if h.rowOf("api-tests") < h.rowOf("running · 2") || !tinted("api-tests") {
		t.Fatalf("a row that changes group moves there and settles with a tint:\n%s", h.screen())
	}
	c.run(2*time.Second, nil)
	if tinted("api-tests") {
		t.Fatal("the tint goes after 1.5 s")
	}

	h.press("<C-w>l")
	h.setThread("api-tests", func(v *model.ThreadView) { v.State = model.StateIdle })
	if h.rowOf("○ api-tests") > h.rowOf("idle · ") && h.rowOf("idle · ") >= 0 {
		t.Fatalf("rows do not move while the cursor is on the board:\n%s", h.screen())
	}
	h.press("<C-w>h")
	if h.rowOf("○ api-tests") < h.rowOf("idle · 1") || !tinted("api-tests") {
		t.Fatalf("the row moves when the cursor leaves the board:\n%s", h.screen())
	}
}

func TestBoardRowsShowTheirTimeOnTheRight(t *testing.T) {
	c := startClocked(t)
	h := c.h
	h.openDemo()
	h.setThread("api-tests", func(v *model.ThreadView) { v.Since = c.now.Add(-6 * 24 * time.Hour) })
	h.setThread("auth-refactor", func(v *model.ThreadView) { v.Since = c.now.Add(-4*time.Minute - 10*time.Second) })
	b := h.board()
	for name, want := range map[string]string{"○ api-tests": "6d", "◉ auth-refactor": "4m"} {
		if !strings.Contains(h.screen(), name) {
			name = "● auth-refactor"
		}
		line := []rune(h.line(h.rowOf(name)))
		end := b.rect.X + b.rect.W - 1
		if got := strings.TrimSpace(string(line[end-len(want) : end])); got != want {
			t.Fatalf("%s should end with %q one column before the edge, got %q:\n%s", name, want, got, h.screen())
		}
	}
	if h.fgAt("6d") != fgOf(h.a.th.Fg3) {
		t.Fatal("a row's time is in fg-3")
	}
	// The time of an idle row changes on its own, so the screen wakes for it.
	c.timers = nil
	h.a.wakeAt = time.Time{}
	h.setThread("auth-refactor", func(v *model.ThreadView) { v.State = model.StateIdle })
	h.setThread("api-tests", func(v *model.ThreadView) { v.Since = c.now.Add(-59*time.Minute - 30*time.Second) })
	h.a.step(evUpdate{})
	c.run(31*time.Second, nil)
	h.see("1h")
}

func TestNoFootNoticeForAWaitingThreadTheBoardShows(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.setThread("api-tests", func(v *model.ThreadView) {
		v.State = model.StateWaiting
		v.Permission = &model.Permission{ID: "p1", Tool: "Bash", Detail: "curl -X POST"}
	})
	if strings.Contains(h.screen(), "needs permission") {
		t.Fatalf("the board's row says it already:\n%s", h.screen())
	}
	h.press("<C-w>o")
	h.a.draw()
	h.see("needs permission · Bash")
}
