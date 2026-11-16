package tui

import (
	"strings"
	"testing"

	"github.com/MahammadAgayev/neoharness/api/model"
	"github.com/gdamore/tcell/v2"
)

func TestTodoShowsTheThreadsTodoListAndFollowsIt(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press(":b auth-refactor", "<CR>")
	chat := h.a.cur.Buf
	h.press(":todo", "<CR>")
	if b := h.a.cur.Buf; b.Kind != KindTodo || b.Thread != "auth-refactor" {
		t.Fatalf("buffer %v %q", b.Kind, b.Thread)
	}
	h.see("no todo list yet")

	v := h.a.threads["auth-refactor"]
	v.Todos = []model.Todo{
		{ID: "1", Text: "Split the session store", State: model.TodoDone},
		{ID: "2", Text: "Move the tests", State: model.TodoActive},
		{ID: "3", Text: "Update the docs", State: model.TodoPending},
	}
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.a.draw()
	for _, want := range []string{"✓ Split the session store", "● Move the tests", "○ Update the docs", "1 of 3 done"} {
		h.see(want)
	}
	if muted, _, _ := h.a.th.Fg3.Decompose(); h.fgAt("Split the session store") != muted {
		t.Error("a done item is muted")
	}
	if !h.struck("Split the session store") {
		t.Error("a done item is struck through")
	}

	h.press("<C-o>")
	if h.a.cur.Buf != chat {
		t.Fatalf("<C-o> goes back to the chat, not %q", h.a.cur.Buf.Name)
	}
}

func TestTodoOutsideAThreadNamesTheCoordinator(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press(":todo", "<CR>")
	if b := h.a.cur.Buf; b.Kind != KindTodo || b.Thread != model.Coordinator {
		t.Fatalf("buffer %v %q", b.Kind, b.Thread)
	}
	h.press(":todo api-tests", "<CR>")
	if b := h.a.cur.Buf; b.Thread != "api-tests" {
		t.Fatalf(":todo with a name shows that thread's list, not %q", b.Thread)
	}
	h.press(":todo nobody", "<CR>")
	if !strings.Contains(h.screen(), "no thread nobody") {
		t.Fatal("an unknown thread is an error")
	}
}

func TestTodoLineShowsAboveThePromptAndUnfolds(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press(":b auth-refactor", "<CR>")
	v := h.a.threads["auth-refactor"]
	v.Todos = []model.Todo{
		{ID: "1", Text: "Split the session store", State: model.TodoDone},
		{ID: "2", Text: "Move the tests", State: model.TodoActive},
		{ID: "3", Text: "Update the docs", State: model.TodoPending},
	}
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.a.draw()
	h.see("▸ 1 of 3 · Move the tests")
	if strings.Contains(h.screen(), "Update the docs") {
		t.Fatal("the todo line starts folded")
	}

	h.press("<Space>", "T")
	h.a.draw()
	for _, want := range []string{"▾ 1 of 3", "✓ Split the session store", "● Move the tests", "○ Update the docs"} {
		h.see(want)
	}
	if !h.struck("Split the session store") {
		t.Error("a done item is struck through")
	}

	// za on any of its rows folds it again, as on a fold of tool calls.
	h.press("G", "za")
	h.a.draw()
	h.see("▸ 1 of 3 · Move the tests")
	if strings.Contains(h.screen(), "Update the docs") {
		t.Fatal("za folds the todo list")
	}

	for i := range v.Todos {
		v.Todos[i].State = model.TodoDone
	}
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.a.draw()
	h.see("▸ 3 of 3")
}

func TestThreadsListShowsEachThreadsTodoProgress(t *testing.T) {
	h := start(t)
	h.openDemo()
	v := h.a.threads["auth-refactor"]
	v.Todos = []model.Todo{
		{ID: "1", Text: "Split the session store", State: model.TodoDone},
		{ID: "2", Text: "Move the tests", State: model.TodoActive},
		{ID: "3", Text: "Update the docs", State: model.TodoPending},
	}
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.a.draw()
	h.see("1/3 · Move the tests")

	for i := range v.Todos {
		v.Todos[i].State = model.TodoDone
	}
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.a.draw()
	h.see("3/3 done")
}

// struck says the first cell of want on screen is struck through.
func (h *neoharness) struck(want string) bool {
	h.t.Helper()
	cells, w, _ := h.s.GetContents()
	for i, line := range strings.Split(h.screen(), "\n") {
		if j := strings.Index(line, want); j >= 0 {
			_, _, attr := cells[i*w+len([]rune(line[:j]))].Style.Decompose()
			return attr&tcell.AttrStrikeThrough != 0
		}
	}
	h.t.Fatalf("%q is not on screen", want)
	return false
}

func TestDoneTasksAreStruckThroughInChatsAndArtifacts(t *testing.T) {
	th := newTheme(256)
	src := "- [x] ship the `fix`\n- [ ] write the docs\n"
	for name, lines := range map[string][]Line{
		"chat":     renderMarkdown(src, 80, th, nil),
		"artifact": docRows(model.Artifact{Name: "doc", Thread: "api", Version: 1}, src, 80, th),
	} {
		struck := map[string]bool{}
		for _, l := range lines {
			for _, sp := range l.Spans {
				_, _, attr := sp.Style.Decompose()
				struck[strings.TrimSpace(sp.Text)] = attr&tcell.AttrStrikeThrough != 0
			}
		}
		if !struck["ship the fix"] {
			t.Errorf("%s: a done task is struck through, code too: %v", name, struck)
		}
		if struck["write the docs"] {
			t.Errorf("%s: an open task is not struck through", name)
		}
	}
}
