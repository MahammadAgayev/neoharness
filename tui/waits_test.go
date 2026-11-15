package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/api/model"
)

func (h *neoharness) setWaits(ws ...model.Wait) {
	h.hub.waits = ws
	h.a.apply(model.Update{Kind: model.UpdateWaits, Waits: ws})
	h.a.draw()
}

func demoWaits() []model.Wait {
	at := time.Now().Add(-time.Minute)
	return []model.Wait{
		{Workspace: "demo", Thread: "api-tests", Kind: model.WaitPermission, ID: "p1", Text: "Bash: go test ./...", Since: at},
		{Workspace: "demo", Thread: "auth-refactor", Kind: model.WaitQuestion, ID: "q1", Text: "Which branch?", Since: at.Add(time.Second)},
	}
}

func TestTheStatusLineCountsTheWaitsOfEveryWorkspace(t *testing.T) {
	h := start(t)
	h.openDemo()
	_, H := h.s.Size()
	h.setWaits(append(demoWaits(), model.Wait{Workspace: "ops", Thread: "deploy", Kind: model.WaitFailed, ID: "failed", Text: "exit 1"})...)
	if !strings.Contains(h.line(H-2), "◆ 3 waiting") || h.fgAt("◆ 3 waiting") != fgOf(h.a.th.Wait) {
		t.Fatalf("the status line should count every wait, in wait ink:\n%s", h.line(H-2))
	}
	h.setWaits()
	if strings.Contains(h.line(H-2), "waiting") {
		t.Fatal("no waits, no count")
	}
}

func TestBracketQWalksTheWaitsListOldestFirst(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.setWaits(demoWaits()...)
	h.press("]q")
	if h.a.cur.Buf.Thread != "api-tests" || !h.a.cur.Follow {
		t.Fatalf("]q should open the oldest wait at its card, got %q", h.a.cur.Buf.Thread)
	}
	h.press("]q")
	if h.a.cur.Buf.Thread != "auth-refactor" {
		t.Fatalf("]q again should go to the next, got %q", h.a.cur.Buf.Thread)
	}
	h.press("]q")
	if h.a.cur.Buf.Thread != "auth-refactor" || !strings.Contains(h.a.notice, "no more waits") {
		t.Fatalf("past the last, ]q stays and says so: %q", h.a.notice)
	}
	h.press("[q")
	if h.a.cur.Buf.Thread != "api-tests" {
		t.Fatal("[q should go back")
	}
	h.press(":cn", "<CR>")
	if h.a.cur.Buf.Thread != "auth-refactor" {
		t.Fatal(":cn is ]q")
	}
	h.press(":cp", "<CR>")
	if h.a.cur.Buf.Thread != "api-tests" {
		t.Fatal(":cp is [q")
	}
	h.setWaits()
	h.press("]q")
	if !strings.Contains(h.a.notice, "nothing waits") {
		t.Fatalf("an empty list says so: %q", h.a.notice)
	}
}

func TestWaitsOpensTheListWhereEnterJumpsAndXDismisses(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.setWaits(append(demoWaits(), model.Wait{Workspace: "demo", Thread: "api-tests", Kind: model.WaitFailed, ID: "failed", Text: "exit status 1", Since: time.Now()})...)
	for _, cmd := range []string{":copen", ":waits"} {
		h.press(cmd, "<CR>")
		if h.a.cur.Buf.Kind != KindWaits {
			t.Fatalf("%s should open the waits list", cmd)
		}
	}
	h.see("◆ api-tests  permission · Bash: go test ./...")
	h.see("◆ auth-refactor  question · Which branch?")
	h.see("✗ api-tests  failed · exit status 1")
	if h.fgAt("✗ api-tests") != fgOf(h.a.th.Alert) {
		t.Fatal("a failure is in alert")
	}
	h.press("gg", "j", "<CR>")
	if h.a.cur.Buf.Thread != "auth-refactor" {
		t.Fatalf("<CR> should jump to the wait on the row, got %q", h.a.cur.Buf.Thread)
	}
	h.press(":waits", "<CR>", "G", "x")
	if !slices.Equal(h.hub.dismissed, []string{"demo/api-tests/failed"}) {
		t.Fatalf("x should dismiss the failure, got %v", h.hub.dismissed)
	}
	h.press("gg", "x")
	if len(h.hub.dismissed) != 1 || !strings.Contains(h.a.notice, "answer") {
		t.Fatalf("a permission is answered, not dismissed: %v %q", h.hub.dismissed, h.a.notice)
	}
}

func TestAThreadOnScreenForTwoSecondsIsSeen(t *testing.T) {
	c := startClocked(t)
	h := c.h
	h.openDemo()
	h.setThread("api-tests", func(v *model.ThreadView) { v.Level = model.LevelNotice })
	h.press(":b api-tests", "<CR>")
	c.run(time.Second, nil)
	if len(h.hub.seen) != 0 {
		t.Fatal("a glance is not seen")
	}
	c.run(1500*time.Millisecond, nil)
	if !slices.Equal(h.hub.seen, []string{"demo/api-tests"}) {
		t.Fatalf("2 s on screen should say seen once, got %v", h.hub.seen)
	}

	h.setThread("auth-refactor", func(v *model.ThreadView) { v.Level = model.LevelNotice })
	h.a.handle(tcell.NewEventFocus(false))
	h.press(":b auth-refactor", "<CR>")
	c.run(3*time.Second, nil)
	if len(h.hub.seen) != 1 {
		t.Fatal("a terminal without focus sees nothing")
	}

	h.a.handle(tcell.NewEventFocus(true))
	h.setThread("auth-refactor", func(v *model.ThreadView) { v.Level = model.LevelNeedsYou })
	c.run(3*time.Second, nil)
	if len(h.hub.seen) != 1 {
		t.Fatal("seen is for levels 1 and 2; what needs you stays until it is resolved")
	}
}

func TestTheWaitsListReadsAsTheBoard(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.setWaits(demoWaits()...)
	h.press(":waits", "<CR>")
	w := h.a.cur
	if w.Buf.Kind != KindWaits {
		t.Fatal(":waits should open the list")
	}
	if h.fgAt("permission · ") != fgOf(h.a.th.Wait) || h.fgAt("question · ") != fgOf(h.a.th.Wait) {
		t.Fatal("the kind is in wait ink, as on the board")
	}
	row := []rune(h.line(h.rowOf("permission · ")))
	end := w.rect.X + w.rect.W - 1
	if got := string(row[end-2 : end]); got != "1m" {
		t.Fatalf("the time is right-aligned one column before the edge, got %q:\n%s", got, string(row))
	}
}
