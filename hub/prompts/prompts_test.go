package prompts

import (
	"strconv"
	"strings"
	"testing"
)

func TestSessionPromptsFillEveryPlaceholder(t *testing.T) {
	in := SessionInput{Workspace: "demo", Name: "api-tests", Location: "gpu1:~/proj"}
	for name, got := range map[string]string{"coordinator": Coordinator(in), "thread": Thread(in)} {
		if strings.Contains(got, "{{") {
			t.Errorf("%s prompt has an unfilled placeholder", name)
		}
		if !strings.Contains(got, "demo") || !strings.Contains(got, "gpu1:~/proj") {
			t.Errorf("%s prompt is missing the workspace or location", name)
		}
	}
	if !strings.Contains(Thread(in), `"api-tests"`) {
		t.Error("thread prompt must name the thread")
	}
}

func TestThreadPromptAsksForATaskListOnlyWhenTodosAreOn(t *testing.T) {
	in := SessionInput{Workspace: "demo", Name: "api", Location: "/src"}
	if strings.Contains(Thread(in), "task list") {
		t.Error("todos off: the prompt does not ask for a task list")
	}
	in.Todos = true
	if !strings.Contains(Thread(in), "task list") {
		t.Error("todos on: the prompt asks for a task list")
	}
}

func TestCompactPrompt(t *testing.T) {
	if got := Compact(""); strings.Contains(got, "{{") || strings.Contains(got, "Focus on") {
		t.Errorf("no focus: %q", got)
	}
	if got := Compact(" the auth bug "); !strings.Contains(got, "Focus on: the auth bug\n") {
		t.Errorf("focus: %q", got)
	}
}

func TestTheBoardShowsTheRecapTasksAndNews(t *testing.T) {
	got := BoardLine(BoardThread{
		Name: "api", State: "idle", Recap: "Added /healthz; tests pass", Done: 3, Total: 5, Now: "Write the docs",
		News: News{Recap: true, Done: []string{"Add the route", "Add a test"}, Artifacts: []string{"health-plan"}},
	})
	want := "- api: idle. Recap: Added /healthz; tests pass. Tasks: 3/5 done, now: Write the docs.\n  New: recap; done: Add the route, Add a test; wrote health-plan."
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if got := BoardLine(BoardThread{Name: "api", State: "idle", News: News{Reply: "Moved the store."}}); got != "- api: idle.\n  New: reply: Moved the store." {
		t.Errorf("reply: %q", got)
	}
	if got := BoardLine(BoardThread{Name: "api", State: "idle", Recap: "Done"}); got != "- api: idle. Recap: Done." {
		t.Errorf("no news: %q", got)
	}
	many := News{}
	for i := range 12 {
		many.Done = append(many.Done, strconv.Itoa(i))
	}
	if got := BoardLine(BoardThread{Name: "api", State: "idle", News: many}); !strings.HasSuffix(got, "done: 0, 1, 2, 3, 4, 5, 6, 7, 8, 9 and 2 more.") {
		t.Errorf("many done: %q", got)
	}
}

func TestTheBoardSaysWhatAThreadWaitsForAndThatItsLastTurnFailed(t *testing.T) {
	for _, c := range []struct {
		state string
		w     Wait
		fail  string
		want  string
	}{
		{"waiting", Wait{Permission: "Bash: rm -rf build"}, "", "- api: waiting. Waits for permission: Bash: rm -rf build."},
		{"waiting", Wait{Question: "Which port?"}, "", "- api: waiting. Asks the person: Which port?"},
		{"waiting", Wait{Paused: true}, "", "- api: waiting. Paused by the person."},
		{"idle", Wait{}, "the agent failed: crashed", "- api: idle. Last turn failed: the agent failed: crashed."},
	} {
		if got := BoardLine(BoardThread{Name: "api", State: c.state, Wait: c.w, Failed: c.fail}); got != c.want {
			t.Errorf("got  %q\nwant %q", got, c.want)
		}
	}
}
