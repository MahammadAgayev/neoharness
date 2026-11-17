package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// The attention agent's edit of the focus is the last stop of ]q: it opens
// the focus in diff mode beside the edit, where do takes a change and :w
// saves. It is not counted as waiting, and <C-d> in :waits drops it.
func TestASuggestedEditOfTheFocusOpensInDiffMode(t *testing.T) {
	h := start(t)
	doc := "# Focus\n\n## Now\n\n- hub tests green\n"
	struck := "# Focus\n\n## Now\n\n- ~~hub tests green~~\n"
	s := &model.Suggestion{Text: struck, Why: "tests pass in api-tests", Base: 1, At: time.Now()}
	h.hub.arts = append(h.hub.arts, model.Artifact{Name: model.FocusDoc, Title: "Focus", Version: 1, Suggestion: s})
	h.hub.docs = map[string]string{model.FocusDoc: doc}
	h.openDemo()
	_, H := h.s.Size()
	h.setWaits(model.Wait{Workspace: model.HubScope, Thread: model.FocusDoc, Kind: model.WaitFocus, ID: model.SuggestionID, Text: s.Why, Since: s.At})
	if strings.Contains(h.line(H-1), "waiting") {
		t.Fatal("a suggestion is not counted as waiting")
	}
	h.press("]q")
	e := diffOf(h.a.cur)
	if e == nil || h.a.cur.Buf.Workspace != model.HubScope || !slices.Equal(e.diff.theirs, strings.Split(struck, "\n")) {
		t.Fatalf("]q should open the focus beside the edit, got %+v", h.a.cur.Buf)
	}
	if !strings.Contains(h.a.notice, "tests pass in api-tests") {
		t.Fatalf("notice = %q", h.a.notice)
	}
	h.press("G", "k", "do", ":w", "<CR>")
	if got := h.hub.docs[model.FocusDoc]; got != struck {
		t.Fatalf("do and :w should save the struck line, got %q", got)
	}

	h.press(":waits", "<CR>")
	h.see("focus · tests pass in api-tests")
	h.press("<C-d>")
	if !slices.Equal(h.hub.dismissed, []string{model.HubScope + "/" + model.FocusDoc + "/" + model.SuggestionID}) {
		t.Fatalf("<C-d> should drop the edit, got %v", h.hub.dismissed)
	}
}

// The focus says an edit waits, and :diffsplit opens it beside the focus.
func TestTheFocusSaysAnEditWaits(t *testing.T) {
	h := start(t)
	s := &model.Suggestion{Text: "# Focus\n\n- ~~done~~\n", Why: "done", Base: 1}
	h.hub.arts = append(h.hub.arts, model.Artifact{Name: model.FocusDoc, Title: "Focus", Version: 1, Suggestion: s})
	h.hub.docs = map[string]string{model.FocusDoc: "# Focus\n\n- done\n"}
	h.press(":focus", "<CR>")
	h.see("an edit waits · :diffsplit")
	h.press(":diffsplit", "<CR>")
	if diffOf(h.a.cur) == nil {
		t.Fatal(":diffsplit should open the edit")
	}
}
