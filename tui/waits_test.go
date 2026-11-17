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

func TestTheCommandLineCountsTheWaitsOfEveryWorkspace(t *testing.T) {
	h := start(t)
	h.openDemo()
	_, H := h.s.Size()
	h.setWaits(append(demoWaits(), model.Wait{Workspace: "ops", Thread: "deploy", Kind: model.WaitFailed, ID: "failed", Text: "exit 1"})...)
	if !strings.Contains(h.line(H-1), "◆ 3 waiting") || h.fgAt("◆ 3 waiting") != fgOf(h.a.th.Wait) {
		t.Fatalf("the command line should count every wait, in wait ink:\n%s", h.line(H-1))
	}
	h.setWaits()
	if strings.Contains(h.line(H-1), "waiting") {
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

// A review is news, not a wait: it is on the ]q path after what blocks,
// but the command line does not count it, and x reads it.
func TestReviewsAreOnTheWaitsPathButNotCounted(t *testing.T) {
	h := start(t)
	h.openDemo()
	_, H := h.s.Size()
	review := model.Wait{Workspace: "demo", Thread: model.Coordinator, Kind: model.WaitReview, ID: model.ReviewID, Text: "setup page drafted", Since: time.Now().Add(-time.Hour)}
	h.setWaits(append(demoWaits(), review)...)
	if !strings.Contains(h.line(H-1), "◆ 2 waiting") {
		t.Fatalf("a review is not counted as waiting:\n%s", h.line(H-1))
	}
	h.press("]q", "]q", "]q")
	if h.a.cur.Buf.Thread != model.Coordinator {
		t.Fatalf("]q should reach the review after the blockers, got %q", h.a.cur.Buf.Thread)
	}
	h.press(":waits", "<CR>")
	h.see("✓ coordinator  review · setup page drafted")
	if h.fgAt("✓ coordinator") != fgOf(h.a.th.Done) {
		t.Fatal("a review is in done ink")
	}
	h.press("G", "x")
	if !slices.Equal(h.hub.dismissed, []string{"demo/coordinator/review"}) {
		t.Fatalf("x should read the review, got %v", h.hub.dismissed)
	}
	h.setWaits(review)
	if strings.Contains(h.line(H-1), "waiting") {
		t.Fatal("only reviews: nothing waits")
	}
}

// :waits shows the list in the hub's order, the order ]q walks; :waits!
// shows it oldest first.
func TestWaitsBangShowsTheListOldestFirst(t *testing.T) {
	h := start(t)
	h.openDemo()
	review := model.Wait{Workspace: "demo", Thread: model.Coordinator, Kind: model.WaitReview, ID: model.ReviewID, Text: "setup page drafted", Since: time.Now().Add(-time.Hour)}
	h.setWaits(append(demoWaits(), review)...)
	h.press(":waits", "<CR>")
	if h.rowOf("permission · ") > h.rowOf("review · ") {
		t.Fatal(":waits keeps the hub's order")
	}
	h.press(":waits!", "<CR>")
	if h.rowOf("review · ") > h.rowOf("permission · ") {
		t.Fatal(":waits! is oldest first")
	}
	h.press("gg", "<CR>")
	if h.a.cur.Buf.Thread != model.Coordinator {
		t.Fatalf("<CR> goes to the row's wait, got %q", h.a.cur.Buf.Thread)
	}
}

// With the attention agent, a stop carries why it is there and a brief: the
// jump says the why, and the card ends with the brief, muted, next to the
// prompt where the person answers.
func TestAStopsBriefEndsItsCard(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.setThread("api-tests", func(v *model.ThreadView) {
		v.State = model.StateWaiting
		v.Permission = &model.Permission{ID: "p1", Tool: "Bash", Detail: "go test ./..."}
	})
	ws := demoWaits()
	ws[0].Why, ws[0].Brief = "blocks the refactor", "It runs the tests; read-only. Suggest: allow."
	h.setWaits(ws...)
	h.press("]q")
	if h.a.cur.Buf.Thread != "api-tests" || !strings.Contains(h.a.notice, "(1 of 2) permission · blocks the refactor") {
		t.Fatalf("notice = %q", h.a.notice)
	}
	h.a.draw()
	h.see("It runs the tests; read-only. Suggest: allow.")
	if h.fgAt("It runs the tests") != fgOf(h.a.th.Fg3) {
		t.Fatal("a brief is muted")
	}
	if h.rowOf("┃ It runs the tests") <= h.rowOf("┃ go test ./...") {
		t.Fatal("the brief comes after the command")
	}
}

// A question card starts with the question itself, with no label that only
// repeats its kind, and the brief follows it.
func TestAQuestionCardStartsWithTheQuestion(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.setThread("api-tests", func(v *model.ThreadView) {
		v.State, v.Waiting, v.QuestionID = model.StateWaiting, "Bump every dependency now?", "q1"
	})
	ws := demoWaits()
	ws[0].Kind, ws[0].Why, ws[0].Brief = model.WaitQuestion, "outside the focus", "Touches every module. Suggest: defer."
	h.setWaits(ws...)
	h.press("]q")
	h.a.draw()
	if strings.Contains(h.screen(), "┃ question") {
		t.Fatalf("the card has no kind label:\n%s", h.screen())
	}
	if q, b := h.rowOf("┃ Bump every dependency now?"), h.rowOf("┃ Touches every module."); q < 0 || b <= q {
		t.Fatalf("the question comes first, then the brief:\n%s", h.screen())
	}
}

// A review has no card, so after ]q its brief shows alone above the prompt,
// muted. It stays while the person reads, after the hub drops the review,
// and goes when the thread runs again.
func TestAReviewsBriefShowsAboveThePrompt(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.setThread("api-tests", func(v *model.ThreadView) { v.State = model.StateIdle })
	h.setWaits(model.Wait{Workspace: "demo", Thread: "api-tests", Kind: model.WaitReview, ID: model.ReviewID, Text: "done", Since: time.Now(),
		Why: "in the focus", Brief: "added the health check · suggest: run the tests"})
	h.press("]q")
	h.a.draw()
	h.see("┃ added the health check")
	if h.fgAt("added the health check") != fgOf(h.a.th.Fg3) || h.fgAt("┃ added the health check") == fgOf(h.a.th.Wait) {
		t.Fatal("a review's brief is muted, bar too: it blocks nothing")
	}
	h.setWaits()
	h.a.draw()
	h.see("┃ added the health check")
	h.setThread("api-tests", func(v *model.ThreadView) { v.State = model.StateRunning })
	h.setThread("api-tests", func(v *model.ThreadView) { v.State = model.StateIdle })
	h.a.draw()
	if strings.Contains(h.screen(), "added the health check") {
		t.Fatalf("a new turn makes the brief old news:\n%s", h.screen())
	}
}

func TestCtrlOGoesBackToTheWorkspaceAJumpLeft(t *testing.T) {
	h := start(t)
	h.hub.CreateWorkspace("ops", "")
	h.hub.threads["ops"] = append(h.hub.threads["ops"], model.ThreadView{Workspace: "ops", Name: "deploy", Role: model.RoleThread, State: model.StateIdle})
	h.a.loadWorkspaces()
	h.openDemo()
	h.press("<Esc>", ":b api-tests", "<CR>")
	h.setWaits(model.Wait{Workspace: "ops", Thread: "deploy", Kind: model.WaitFailed, ID: "failed", Text: "exit 1"})
	h.press("]q")
	if h.a.workspace != "ops" || h.a.cur.Buf.Thread != "deploy" {
		t.Fatalf("]q should open ops · deploy, got %q · %q", h.a.workspace, h.a.cur.Buf.Thread)
	}
	h.press("<C-o>")
	if h.a.workspace != "demo" || h.a.cur.Buf.Thread != "api-tests" {
		t.Fatalf("<C-o> should go back to demo · api-tests, got %q · %q", h.a.workspace, h.a.cur.Buf.Thread)
	}
	h.press("<C-o>")
	if h.a.workspace != "demo" || h.a.cur.Buf.Thread != model.Coordinator {
		t.Fatalf("<C-o> again should go back in demo's window, got %q · %q", h.a.workspace, h.a.cur.Buf.Thread)
	}

	h.press("<Space>pt", "deploy", "<CR>", ":b coordinator", "<CR>", "<C-o>")
	if h.a.workspace != "ops" || h.a.cur.Buf.Thread != "deploy" {
		t.Fatalf("a jump inside ops goes back inside ops first, got %q · %q", h.a.workspace, h.a.cur.Buf.Thread)
	}
	h.press("<C-o>")
	if h.a.workspace != "demo" {
		t.Fatalf("then <C-o> leaves ops, got %q", h.a.workspace)
	}
}
