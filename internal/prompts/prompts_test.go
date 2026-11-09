package prompts

import (
	"strings"
	"testing"
)

func TestSessionPromptsFillEveryPlaceholder(t *testing.T) {
	in := SessionInput{Project: "demo", Name: "api-tests", Location: "gpu1:~/proj"}
	for name, got := range map[string]string{"coordinator": Coordinator(in), "thread": Thread(in)} {
		if strings.Contains(got, "{{") {
			t.Errorf("%s prompt has an unfilled placeholder", name)
		}
		if !strings.Contains(got, "demo") || !strings.Contains(got, "gpu1:~/proj") {
			t.Errorf("%s prompt is missing the project or location", name)
		}
	}
	if !strings.Contains(Thread(in), `"api-tests"`) {
		t.Error("thread prompt must name the thread")
	}
}
