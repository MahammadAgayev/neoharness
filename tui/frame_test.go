package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// rowOf is the screen row a text first shows on; -1 when it does not.
func (h *neoharness) rowOf(want string) int {
	for i, line := range strings.Split(h.screen(), "\n") {
		if strings.Contains(line, want) {
			return i
		}
	}
	return -1
}

func (h *neoharness) line(y int) string { return strings.Split(h.screen(), "\n")[y] }

func TestEachPaneHasAStatusLineAtItsFootAndFocusShowsInIt(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press(":b auth-refactor", "<CR>", "<C-w>v", ":b coordinator", "<CR>")
	// left has focus, right does not.
	left := h.a.cur
	right := h.a.windowOf("auth-refactor")
	if left == right || right == nil {
		t.Fatal("two chats should show")
	}
	if top := strings.TrimSpace(h.line(0)); top != "" {
		t.Fatalf("no bar on top: the top row is blank:\n%s", h.screen())
	}
	foot := left.rect.Y + left.rect.H - 1
	if !strings.Contains(h.inPane(left, foot), "coordinator") || !strings.Contains(h.inPane(right, foot), "auth-refactor") || !strings.Contains(h.inPane(right, foot), "running") {
		t.Fatalf("each pane's last row names it and its state:\n%s", h.screen())
	}
	cells, w, _ := h.s.GetContents()
	name := func(win *Window) tcell.Style { return cells[foot*w+win.rect.X+1].Style }
	if fgOf(name(left)) != fgOf(h.a.th.Fg) || !boldOf(name(left)) {
		t.Fatal("the focused pane's name should be in bold fg")
	}
	if fgOf(name(right)) != fgOf(h.a.th.Fg2) || boldOf(name(right)) {
		t.Fatal("a pane without focus should have its name in fg-2")
	}
	h.a.cur = right
	h.a.draw()
	cells, _, _ = h.s.GetContents()
	if !boldOf(name(right)) || boldOf(name(left)) {
		t.Fatal("the bold name should move with the focus")
	}
}

func TestTheCommandLineIsTheLastRowAndNamesTheWorkspaceOnce(t *testing.T) {
	h := start(t)
	h.openDemo()
	v := h.a.threads[model.Coordinator]
	v.Model, v.Context, v.ContextWindow = "claude-opus-5", 100, 1000
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.a.draw()
	w := h.a.cur
	if foot := h.inPane(w, w.rect.Y+w.rect.H-1); !strings.Contains(foot, "opus-5 · ctx 10%") {
		t.Fatalf("a chat's status line shows its model and context:\n%s", foot)
	}
	h.press("<C-w>v")
	_, H := h.s.Size()
	if n := strings.Count(h.screen(), "demo"); n != 1 {
		t.Fatalf("the workspace should show once, got %d:\n%s", n, h.screen())
	}
	last := h.line(H - 1)
	for _, want := range []string{"demo", "● 1 running"} {
		if !strings.Contains(last, want) {
			t.Fatalf("the command line should show %q on its right:\n%s", want, last)
		}
	}
	h.press(":")
	if !strings.HasPrefix(strings.TrimSpace(h.line(H-1)), ":") {
		t.Fatal("the command line should be the last row")
	}
}

// inPane is a row of the screen within a window's columns.
func (h *neoharness) inPane(w *Window, y int) string {
	return strings.TrimSpace(string([]rune(h.line(y))[w.rect.X : w.rect.X+w.rect.W]))
}

func TestThePromptHasOneBlankRowAboveAndBelow(t *testing.T) {
	h := start(t)
	h.openDemo()
	_, H := h.s.Size()
	w := h.a.cur
	prompt := h.rowOf("› write to coordinator")
	if prompt != H-4 || h.inPane(w, H-3) != "" || !strings.HasPrefix(h.inPane(w, H-2), "coordinator") {
		t.Fatalf("one blank row parts the prompt from the status line; prompt row %d:\n%s", prompt, h.screen())
	}
	h.press("i")
	for i := 0; i < 40; i++ {
		e := model.Event{Kind: model.EventText, Text: "line"}
		h.a.apply(model.Update{Kind: model.UpdateEvent, Workspace: "demo", Thread: model.Coordinator, Event: &e})
	}
	h.a.draw()
	if h.inPane(w, H-5) != "" || h.inPane(w, H-6) == "" {
		t.Fatalf("one blank row, not two, parts the stream from the prompt:\n%s", h.screen())
	}
}

func TestThePromptDockIsRecapThenTodoThenPrompt(t *testing.T) {
	h := start(t)
	h.openDemo()
	v := h.a.threads[model.Coordinator]
	v.Recap, v.RecapAt = "Cache added; review next.", h.a.now()
	v.Todos = []model.Todo{{Text: "Write its test", State: model.TodoActive}, {Text: "Ship", State: model.TodoPending}}
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.press(":recap", "<CR>")
	recap, todo, prompt := h.rowOf("※ recap: Cache added"), h.rowOf("0 of 2 · Write its test"), h.rowOf("› write to")
	if recap < 0 || todo != recap+1 || prompt != todo+2 {
		t.Fatalf("the dock should be the recap, the todo line, a blank row, the prompt; rows %d %d %d:\n%s", recap, todo, prompt, h.screen())
	}
	if w := h.a.cur; h.inPane(w, recap-1) != "" || h.inPane(w, prompt+1) != "" {
		t.Fatalf("one blank row above the dock and one below the prompt:\n%s", h.screen())
	}
	if strings.Contains(h.screen(), "╭") || strings.Contains(h.screen(), "▍") || strings.Contains(h.screen(), "×") {
		t.Fatalf("the recap is dim text, with no box, bar or ×:\n%s", h.screen())
	}
	if fg := h.fgAt("Cache added"); fg != fgOf(h.a.th.Fg3) {
		t.Fatal("the recap should be dim, in fg-3")
	}
}

func TestEveryPaneHasTheSameInsetFromItsSeparator(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press("<C-w>v")
	cells, w, _ := h.s.GetContents()
	for _, win := range h.a.root.windows()[1:] {
		x := win.rect.X
		if sep := string(cells[2*w+x-2].Runes); sep != "│" {
			t.Fatalf("a pane should start two columns after its separator, found %q", sep)
		}
		if gap := string(cells[2*w+x-1].Runes); gap != " " && gap != "" {
			t.Fatalf("the column after a separator should be blank, found %q", gap)
		}
	}
}

func boldOf(st tcell.Style) bool {
	_, _, attr := st.Decompose()
	return attr&tcell.AttrBold != 0
}

// A question is what the person answers: the card shows all of it while the
// window has room, and a blank row parts it from the prompt.
func TestTheQuestionCardShowsTheWholeQuestionAboveABlankRow(t *testing.T) {
	h := start(t)
	h.openDemo()
	var q []string
	for i := 1; i <= 20; i++ {
		q = append(q, fmt.Sprintf("- line %d of the question", i))
	}
	v := h.a.threads[model.Coordinator]
	v.State, v.Waiting, v.QuestionID = model.StateWaiting, strings.Join(q, "\n"), "q1"
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.a.draw()
	last, prompt := h.rowOf("line 20 of the question"), h.rowOf("› write to coordinator")
	if h.rowOf("line 1 of the question") < 0 || last < 0 {
		t.Fatalf("the whole question should show:\n%s", h.screen())
	}
	if prompt != last+2 || h.inPane(h.a.cur, last+1) != "" {
		t.Fatalf("one blank row parts the card from the prompt; rows %d %d:\n%s", last, prompt, h.screen())
	}
}

func bgOf(st tcell.Style) tcell.Color {
	_, bg, _ := st.Decompose()
	return bg
}

// The status line is a bar at each pane's foot, as Vim's StatusLine and
// StatusLineNC, so every pane, the coordinator's too, shows where it ends.
// The focused one has its colors: branch, model and context.
func TestTheStatusLineIsABarWithColorsInTheFocusedPane(t *testing.T) {
	h := start(t)
	h.openDemo()
	v := h.a.threads[model.Coordinator]
	v.Branch, v.Model, v.Context, v.ContextWindow = "main", "claude-opus-5", 100, 1000
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.press("<Esc>")
	coord, board := h.a.cur, h.board()
	cells, sw, _ := h.s.GetContents()
	at := func(w *Window, x int) tcell.Style { return cells[(w.rect.Y+w.rect.H-1)*sw+x].Style }
	for x := coord.rect.X; x < coord.rect.X+coord.rect.W; x++ {
		if bgOf(at(coord, x)) != bgOf(h.a.th.StatusLine) {
			t.Fatalf("the focused pane's status line should be a StatusLine bar, col %d", x)
		}
	}
	if bgOf(at(board, board.rect.X)) != bgOf(h.a.th.StatusLineNC) || bgOf(h.a.th.StatusLineNC) == bgOf(h.a.th.StatusLine) {
		t.Fatal("a pane without focus should have a StatusLineNC bar")
	}
	for text, want := range map[string]tcell.Style{"main": h.a.th.Run, "opus-5": h.a.th.Peer, "ctx 10%": h.a.th.Done} {
		if fg := h.fgAt(text); fg != fgOf(want) {
			t.Errorf("%q should have its color in the focused pane", text)
		}
	}
	if fg := fgOf(at(board, board.rect.X+1)); fg != fgOf(h.a.th.Fg2) {
		t.Fatal("a pane without focus should have its name in fg-2")
	}
	h.press("<C-w>l")
	if fg := h.fgAt("ctx 10%"); fg != fgOf(h.a.th.Fg3) {
		t.Fatal("a pane without focus should have its meta in fg-3")
	}
}

// The workspaces list counts only open threads, most urgent first; a
// workspace with none says when it was last used.
func TestTheWorkspacesListCountsOpenThreadsMostUrgentFirst(t *testing.T) {
	h := start(t)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	// An even pulse phase draws ● and the next one ◉.
	now = time.Unix(0, int64(phase(now, pulseEvery)+2)*int64(pulseEvery))
	h.a.now = func() time.Time { return now }
	h.a.workspaces = []model.WorkspaceView{
		{Name: "busy", Coordinator: model.StateWaiting, Counts: map[model.State]int{
			model.StateWaiting: 1, model.StateFailed: 1, model.StateRunning: 2, model.StateIdle: 1, model.StateResolved: 5}},
		{Name: "noticed", Coordinator: model.StateIdle, Notices: 2, Counts: map[model.State]int{model.StateResolved: 3}},
		{Name: "quiet", Coordinator: model.StateIdle, LastUsed: now.Add(-2 * time.Hour), Counts: map[model.State]int{model.StateResolved: 5}},
		{Name: "fresh", Coordinator: model.StateIdle, LastUsed: now.Add(-5 * time.Minute), Counts: map[model.State]int{}},
	}
	h.a.renderWorkspaces()
	h.a.draw()
	row := func(name string) (int, string) {
		for y, l := range strings.Split(h.screen(), "\n") {
			if strings.Contains(l, " "+name+" ") {
				return y, strings.TrimSpace(l)
			}
		}
		t.Fatalf("no row for %s:\n%s", name, h.screen())
		return 0, ""
	}
	want := map[string]string{
		"busy":    "◆ 2 waiting · ✗ 1 failed · ● 2 running · ○ 1 idle",
		"noticed": "◆ 1 waiting",
		"quiet":   "2h ago",
		"fresh":   "5m ago",
	}
	for name, w := range want {
		if _, l := row(name); !strings.HasSuffix(l, w) {
			t.Fatalf("%s row should end with %q: %q", name, w, l)
		}
	}
	if strings.Contains(h.screen(), "✓") {
		t.Fatalf("resolved threads should not be counted:\n%s", h.screen())
	}

	cells, w, _ := h.s.GetContents()
	styleOf := func(name, glyph string) tcell.Style {
		y, _ := row(name)
		for x := 0; x < w; x++ {
			if string(cells[y*w+x].Runes) == glyph {
				return cells[y*w+x].Style
			}
		}
		t.Fatalf("no %s in the %s row", glyph, name)
		return tcell.StyleDefault
	}
	th := h.a.th
	if fgOf(styleOf("busy", "◆")) != fgOf(th.Wait) || fgOf(styleOf("busy", "✗")) != fgOf(th.Alert) ||
		fgOf(styleOf("busy", "●")) != fgOf(th.Fg3) || fgOf(styleOf("busy", "○")) != fgOf(th.Fg3) {
		t.Fatal("waiting is gold, failed is love, running and idle are muted")
	}

	now = now.Add(pulseEvery)
	h.a.draw()
	if _, l := row("busy"); !strings.Contains(l, "◉ 2 running") {
		t.Fatalf("the running glyph should pulse: %q", l)
	}
}
