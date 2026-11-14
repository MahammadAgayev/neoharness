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

func TestTheBoardShowsTheStartOfAReplyWhenTheThreadEndedATurn(t *testing.T) {
	if got, want := BoardLine("cache", "idle", Wait{}, "All tests pass.", "", true), "- cache: idle, ended a turn. Reply: All tests pass."; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if got := BoardLine("cache", "idle", Wait{}, "All tests pass.", "", false); got != "- cache: idle." {
		t.Errorf("a reply comes once: %q", got)
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
		if got := BoardLine("api", c.state, c.w, "", c.fail, false); got != c.want {
			t.Errorf("got  %q\nwant %q", got, c.want)
		}
	}
}
