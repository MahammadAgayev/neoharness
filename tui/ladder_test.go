package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

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
	h.setThread("api-tests", func(v *model.ThreadView) { v.State = model.StateFailed })
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

func TestThePermissionCardIsAWaitBarWithTheFullCommand(t *testing.T) {
	h := start(t)
	h.openDemo()
	long := "curl -X POST https://example.com/api/v1/deploy -H 'content-type: application/json' -d '{\"service\":\"api\",\"version\":\"1.2.3\",\"region\":\"eu\"}' --fail-with-body"
	h.setThread("auth-refactor", func(v *model.ThreadView) {
		v.State = model.StateWaiting
		v.Waiting = "permission"
		v.Permission = &model.Permission{ID: "p1", Tool: "Bash", Detail: long}
	})
	h.press(":b auth-refactor", "<CR>")
	w := h.a.cur
	if strings.ContainsAny(h.screen(), "╭╮╰╯") {
		t.Fatalf("the card is a bar, not a box:\n%s", h.screen())
	}
	tool := h.rowOf("┃ Bash")
	if tool < 0 {
		t.Fatalf("the card starts with its tool after a bar:\n%s", h.screen())
	}
	cells, sw, _ := h.s.GetContents()
	x := w.rect.X
	if fg, _, _ := cells[tool*sw+x].Style.Decompose(); fg != fgOf(h.a.th.Wait) {
		t.Fatal("the bar is in wait ink")
	}
	if _, _, attr := cells[tool*sw+x+2].Style.Decompose(); attr&tcell.AttrBold != 0 {
		t.Fatal("the tool is not bold: bold is for names with focus")
	}
	if fg, _, _ := cells[tool*sw+x+2].Style.Decompose(); fg != fgOf(h.a.th.Fg2) {
		t.Fatal("the tool is in fg-2")
	}
	in := func(y int) string { return strings.TrimSpace(string([]rune(h.line(y))[x : x+w.rect.W])) }
	var cmd string
	for y := tool + 1; strings.HasPrefix(in(y), "┃"); y++ {
		if fg, _, _ := cells[y*sw+x+2].Style.Decompose(); fg != fgOf(h.a.th.Fg) {
			t.Fatalf("row %d of the command is not in fg", y)
		}
		cmd += strings.TrimPrefix(in(y), "┃")
	}
	if strings.ReplaceAll(cmd, " ", "") != strings.ReplaceAll(long, " ", "") {
		t.Fatalf("the card should hold the full command, wrapped; got %q", cmd)
	}
}

func TestAPendingPermissionLineShowsItsCommandInFg(t *testing.T) {
	b := newChat("demo", "api")
	b.AddEvent(model.Event{Kind: model.EventPermission, Tool: "Bash", Detail: "curl -X POST"})
	th := newTheme(256)
	for _, l := range b.Lines(80, th, 0) {
		for _, s := range l.Spans {
			if strings.Contains(s.Text, "curl") && s.Style != th.Fg {
				t.Fatal("you judge the command, so it is in fg")
			}
		}
	}
}

func TestALongBriefFromTheCoordinatorFoldsToOneRow(t *testing.T) {
	b := newChat("demo", "api")
	brief := "Write health.go.\n\n- add GET /healthz\n- return 200 with the build\n- test it with go test\n\nTell me when gofmt and go vet pass."
	b.AddEvent(model.Event{Kind: model.EventMessage, From: model.Coordinator, Text: brief})
	b.AddEvent(model.Event{Kind: model.EventMessage, From: model.Coordinator, Text: "and keep it short"})
	th := newTheme(256)
	text := func() string {
		var out []string
		for _, l := range b.Lines(80, th, 0) {
			pre := ""
			for _, p := range l.Prefix {
				pre += p.Text
			}
			out = append(out, strings.TrimSpace(pre+l.Text()))
		}
		return strings.Join(out, "\n")
	}
	got := text()
	if !strings.Contains(got, "▎ from coordinator · brief ▸") || strings.Contains(got, "healthz") {
		t.Fatalf("a long brief should fold to one row:\n%s", got)
	}
	if !strings.Contains(got, "and keep it short") {
		t.Fatalf("a short message stays open:\n%s", got)
	}
	if !b.Toggle(0) {
		t.Fatal("za should open the brief")
	}
	if got := text(); !strings.Contains(got, "from coordinator · brief ▾") || !strings.Contains(got, "add GET /healthz") {
		t.Fatalf("an open brief shows all of it:\n%s", got)
	}
}

func TestOpeningFromTheBoardKeepsTheBoard(t *testing.T) {
	for _, keys := range [][]string{
		{":b api-tests", "<CR>"},
		{"<CR>"},
		{":open fresh", "<CR>", "<Esc>"},
	} {
		h := start(t)
		h.openDemo()
		h.press("<C-w>l")
		for keys[0] == "<CR>" && h.a.line(h.a.cur).Item != "api-tests" {
			h.press("j")
		}
		h.press(keys...)
		want := "api-tests"
		if keys[0] == ":open fresh" {
			want = "fresh"
		}
		if h.board() == nil {
			t.Fatalf("%q from the board closed it:\n%s", keys, h.screen())
		}
		if h.a.cur.Buf.Kind != KindChat || h.a.cur.Buf.Thread != want {
			t.Fatalf("%q should open %s in the chat pane, got %q", keys, want, h.a.cur.Buf.Thread)
		}
	}
}

// turnEnd sends the end of a thread's turn at the level the hub gave it.
func (h *neoharness) turnEnd(workspace, thread string, l model.Level) {
	h.a.apply(model.Update{Kind: model.UpdateEvent, Workspace: workspace, Thread: thread, Event: &model.Event{Kind: model.EventTurnEnd, Level: l}})
}

func TestATurnEndToastsAtTheHubsLevel(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.a.toasts = nil
	h.setThread("api-tests", func(v *model.ThreadView) { v.State = model.StateRunning })
	h.setThread("api-tests", func(v *model.ThreadView) { v.State = model.StateIdle })
	if len(h.a.toasts) != 0 {
		t.Fatal("a thread going idle is no toast by itself: the hub decides")
	}
	h.turnEnd("demo", "api-tests", model.LevelAmbient)
	if len(h.a.toasts) != 0 {
		t.Fatal("an ambient turn end changes in place, with no toast")
	}
	h.turnEnd("demo", "api-tests", model.LevelNotice)
	if len(h.a.toasts) != 1 || h.a.toasts[0].glyph.Text != "✓" {
		t.Fatalf("a notice-level turn end toasts: %+v", h.a.toasts)
	}
}

func TestAnAnsweredPermissionHasNoGlyphSinceArtifactsUseTheDiamond(t *testing.T) {
	b := newChat("demo", "api")
	b.AddEvent(model.Event{Kind: model.EventPermission, Tool: "Bash", Detail: "go test ./..."})
	b.AddEvent(model.Event{Kind: model.EventAnswer, Text: "allow"})
	for _, l := range b.Lines(80, newTheme(256), 0) {
		if !strings.Contains(l.Text(), "go test") {
			continue
		}
		for _, s := range l.Prefix {
			if strings.Contains(s.Text, "◇") {
				t.Fatalf("the answered permission %q has ◇, the artifact glyph", l.Text())
			}
		}
		return
	}
	t.Fatal("the answered permission is not in the chat")
}

func TestEnterFromTheBoardOpensOneThreadWindowBesideTheCoordinator(t *testing.T) {
	h := start(t)
	h.s.SetSize(200, 40)
	h.openDemo()
	coord := h.a.root.windows()[0]
	h.press("<C-w>l", "gg", "j", "<CR>")
	wins := h.a.root.windows()
	if len(wins) != 3 || wins[0] != coord || coord.Buf.Thread != model.Coordinator || wins[1] != h.a.cur || h.a.cur.Buf.Thread != "auth-refactor" || wins[2].Buf.Kind != KindThreads {
		t.Fatalf("<CR> should split the thread between the coordinator and the list; got %d windows:\n%s", len(wins), h.screen())
	}
	if wins[1].rect.W < minSplit || coord.rect.W < wins[1].rect.W-2 {
		t.Fatalf("the coordinator gives the thread half its width; widths %d %d %d", coord.rect.W, wins[1].rect.W, wins[2].rect.W)
	}
	h.press("<C-w>l", "G", "<CR>")
	wins = h.a.root.windows()
	if len(wins) != 3 || wins[1] != h.a.cur || h.a.cur.Buf.Thread != "api-tests" || coord.Buf.Thread != model.Coordinator {
		t.Fatalf("a second <CR> should reuse the thread window; got %d windows:\n%s", len(wins), h.screen())
	}
	h.press("<C-w>l", ":b auth-refactor", "<CR>")
	if len(h.a.root.windows()) != 3 || h.a.cur != wins[1] || h.a.cur.Buf.Thread != "auth-refactor" {
		t.Fatalf(":b from the list should follow <CR>; got %d windows", len(h.a.root.windows()))
	}
}

func TestEnterFromTheBoardOnANarrowScreenUsesTheCoordinatorWindow(t *testing.T) {
	h := start(t)
	h.s.SetSize(80, 30)
	h.openDemo()
	coord := h.a.root.windows()[0]
	h.press("<C-w>l", "G", "<CR>")
	if len(h.a.root.windows()) != 2 || h.a.cur != coord || coord.Buf.Thread != "api-tests" {
		t.Fatalf("on 80 columns <CR> should open in the coordinator's window; got %d windows", len(h.a.root.windows()))
	}
}
