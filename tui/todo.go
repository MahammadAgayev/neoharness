package tui

import (
	"fmt"

	"github.com/MahammadAgayev/neoharness/api/model"
)

func todoName(workspace, thread string) string {
	return "neoharness://" + workspace + "/todo/" + thread
}

// showTodo shows a thread's todo list in the window: the named thread, the
// one the window shows, or the coordinator.
func (a *App) showTodo(thread string) {
	if thread == "" {
		thread = model.Coordinator
		if a.cur.Buf.Kind == KindChat {
			thread = a.cur.Buf.Thread
		}
	}
	v, ok := a.threads[thread]
	if !ok {
		a.notify("no thread "+thread, a.th.Alert)
		return
	}
	name := todoName(a.workspace, thread)
	b, ok := a.bufs[name]
	if !ok {
		b = newBuffer(name, KindTodo)
		b.Workspace, b.Thread = a.workspace, thread
		a.bufs[name] = b
	}
	b.SetLines(a.todoLines(v.Todos))
	a.cur.show(b)
	a.cur.Follow = false
}

// refreshTodo follows a thread's todo list while it is open.
func (a *App) refreshTodo(v model.ThreadView) {
	if b, ok := a.bufs[todoName(a.workspace, v.Name)]; ok {
		b.SetLines(a.todoLines(v.Todos))
	}
}

func (a *App) todoLines(todos []model.Todo) []Line {
	th := a.th
	in := []Span{{" ", th.Fg}}
	if len(todos) == 0 {
		return []Line{{Prefix: in, Spans: []Span{{"no todo list yet", th.Fg3}}}}
	}
	done := 0
	var out []Line
	for _, t := range todos {
		mark, ms, ts := "○ ", th.Fg, th.Fg
		switch t.State {
		case model.TodoDone:
			mark, ms, ts = "✓ ", th.Fg3, th.Fg3.StrikeThrough(true)
			done++
		case model.TodoActive:
			mark, ms = "● ", th.Run
		}
		out = append(out, Line{Prefix: in, Spans: []Span{{mark, ms}, {t.Text, ts}}})
	}
	return append(out, Line{}, Line{Prefix: in, Spans: []Span{{fmt.Sprintf("%d of %d done", done, len(todos)), th.Fg2}}})
}
