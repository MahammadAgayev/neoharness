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
