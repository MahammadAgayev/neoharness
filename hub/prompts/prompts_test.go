package prompts

import (
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

func TestCompactPrompt(t *testing.T) {
	if got := Compact(""); strings.Contains(got, "{{") || strings.Contains(got, "Focus on") {
		t.Errorf("no focus: %q", got)
	}
	if got := Compact(" the auth bug "); !strings.Contains(got, "Focus on: the auth bug\n") {
		t.Errorf("focus: %q", got)
	}
}

func TestTheBoardShowsAThreadsRecapWhenItEndedATurn(t *testing.T) {
	r := &Recap{
		Asked: "Fix the lint issues", Outcome: "Fixed two", Open: "Choose: change the API or ignore one",
		Ran: 3, Failed: []string{"golangci-lint run"},
		Edited: []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go"}, Artifacts: []string{"plan"},
	}
	want := "- lint: idle, ended a turn. Asked: Fix the lint issues. Outcome: Fixed two. Open: Choose: change the API or ignore one. " +
		"Seen by neoharness: 3 commands, 1 failed: golangci-lint run; edited a.go, b.go, c.go, d.go, e.go and 1 more; wrote the artifact plan."
	if got := BoardLine("lint", "idle", r, true); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if got := BoardLine("lint", "idle", r, false); got != "- lint: idle." {
		t.Errorf("a recap comes once: %q", got)
	}
	reply := &Recap{Asked: "Add a cache", Outcome: "All tests pass.", FromReply: true}
	if got, want := BoardLine("cache", "idle", reply, true), "- cache: idle, ended a turn. Asked: Add a cache. Reply: All tests pass. Seen by neoharness: no commands."; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	failed := &Recap{Asked: "try", Error: "the agent failed: crashed"}
	if got, want := BoardLine("flaky", "idle", failed, true), "- flaky: idle, ended a turn. Asked: try. Failed: the agent failed: crashed. Seen by neoharness: no commands."; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
