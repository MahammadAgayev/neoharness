package tui

import (
	"strings"
	"testing"

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

func TestEachPaneHasAWinbarAndFocusShowsAsAnAccentRow(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press(":b auth-refactor", "<CR>", "<C-w>v", ":b coordinator", "<CR>")
	left := h.a.cur
	right := h.a.windowOf("auth-refactor")
	if left == right || right == nil {
		t.Fatal("two chats should show")
	}
	top := h.line(0)
	if !strings.Contains(top, "coordinator") || !strings.Contains(top, "auth-refactor") || !strings.Contains(top, "threads") {
		t.Fatalf("the top row should name each pane:\n%s", h.screen())
	}
	if !strings.Contains(top, " running") {
		t.Fatalf("a winbar should show its thread's state:\n%s", top)
	}
	cells, w, _ := h.s.GetContents()
	under := func(win *Window) (string, bool) {
		c := cells[w+win.rect.X+1]
		return string(c.Runes), fgOf(c.Style) == fgOf(h.a.th.Accent)
	}
	if r, accent := under(left); r != "▔" || !accent {
		t.Fatalf("the focused pane should have an accent ▔ row under its winbar, got %q", r)
	}
	if r, _ := under(right); r == "▔" {
		t.Fatal("a pane without focus should have no focus row")
	}
	if fg := fgOf(cells[left.rect.X+1].Style); fg != fgOf(h.a.th.Fg) {
		t.Fatal("the focused pane's name should be in fg")
	}
	if fg := fgOf(cells[right.rect.X+1].Style); fg != fgOf(h.a.th.Fg3) {
		t.Fatal("a pane without focus should have its winbar in fg-3")
	}
	h.a.cur = right
	h.a.draw()
	cells, _, _ = h.s.GetContents()
	if r, _ := under(right); r != "▔" {
		t.Fatal("the focus row should move with the focus")
	}
	if r, _ := under(left); r == "▔" {
		t.Fatal("the focus row should leave the pane that lost focus")
	}
}

func TestOneGlobalStatusLineNamesTheWorkspaceOnce(t *testing.T) {
	h := start(t)
	h.openDemo()
	v := h.a.threads[model.Coordinator]
	v.Model, v.Context, v.ContextWindow = "claude-opus-5", 100, 1000
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.press("<C-w>v")
	_, H := h.s.Size()
	if n := strings.Count(h.screen(), "demo"); n != 1 {
		t.Fatalf("the workspace should show once, got %d:\n%s", n, h.screen())
	}
	status := h.line(H - 2)
	for _, want := range []string{"demo", "opus-5", "ctx 10%", "● 1 running"} {
		if !strings.Contains(status, want) {
			t.Fatalf("the global status line should show %q:\n%s", want, status)
		}
	}
	if fg := h.fgAt("ctx 10%"); fg != fgOf(h.a.th.Fg3) {
		t.Fatal("the context should be meta, in fg-3, below its limit")
	}
	h.press(":")
	if !strings.HasPrefix(strings.TrimSpace(h.line(H-1)), ":") {
		t.Fatal("the command line should be the last row, under the status line")
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
	if prompt != H-4 || h.inPane(w, H-3) != "" {
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
	recap, todo, prompt := h.rowOf("recap  Cache added"), h.rowOf("0 of 2 · Write its test"), h.rowOf("› write to")
	if recap < 0 || todo != recap+1 || prompt != todo+1 {
		t.Fatalf("the dock should be the recap, the todo line, the prompt, one row each; rows %d %d %d:\n%s", recap, todo, prompt, h.screen())
	}
	if w := h.a.cur; h.inPane(w, recap-1) != "" || h.inPane(w, prompt+1) != "" {
		t.Fatalf("one blank row above the dock and one below the prompt:\n%s", h.screen())
	}
	if strings.Contains(h.screen(), "╭") {
		t.Fatalf("the recap is a line with a bar, not a box:\n%s", h.screen())
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
