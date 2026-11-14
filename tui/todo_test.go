package tui

import (
	"strings"
	"testing"

	"github.com/MahammadAgayev/neoharness/api/model"
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
	if muted, _, _ := h.a.th.Muted.Decompose(); h.fgAt("Split the session store") != muted {
		t.Error("a done item is muted")
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
