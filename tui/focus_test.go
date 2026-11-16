package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// The focus is an artifact of the hub's scope: :focus and <leader>f open it
// in the window, from any workspace or none, and it edits as any artifact.
func TestFocusOpensTheHubsFocusDoc(t *testing.T) {
	h := start(t)
	h.hub.arts = append(h.hub.arts, model.Artifact{Name: model.FocusDoc, Title: "Focus"})
	h.hub.docs = map[string]string{model.FocusDoc: model.FocusTemplate}
	h.press(":focus", "<CR>")
	b := h.a.cur.Buf
	if b.Kind != KindArtifact || b.Workspace != model.HubScope || b.Artifact != model.FocusDoc {
		t.Fatalf(":focus opened %+v", b)
	}
	h.see("Keep moving")
	h.press("<C-o>")
	h.openDemo()
	h.press(" f")
	if b := h.a.cur.Buf; b.Workspace != model.HubScope || b.Artifact != model.FocusDoc {
		t.Fatalf("<leader>f opened %+v", b)
	}
}

// :focus off sets the focus aside and :focus on brings it back; the doc
// says so at its top while it is aside.
func TestFocusOffAndOn(t *testing.T) {
	h := start(t)
	h.hub.arts = append(h.hub.arts, model.Artifact{Name: model.FocusDoc, Title: "Focus", Version: 2})
	h.hub.docs = map[string]string{model.FocusDoc: model.FocusTemplate}
	h.press(":focus off", "<CR>", ":focus on", "<CR>")
	if !slices.Equal(h.hub.focusOff, []bool{true, false}) {
		t.Fatalf("calls = %v", h.hub.focusOff)
	}
	h.press(":focus", "<CR>")
	off := model.Artifact{Name: model.FocusDoc, Title: "Focus", Version: 2, Off: true}
	h.hub.arts[len(h.hub.arts)-1] = off
	h.a.apply(model.Update{Kind: model.UpdateArtifact, Workspace: model.HubScope, Artifact: &off})
	h.a.draw()
	h.see("set aside · :focus on")
	if h.fgAt("set aside") != fgOf(h.a.th.Fg3) {
		t.Fatal("set aside is muted")
	}
	h.press(":focus maybe", "<CR>")
	if !strings.Contains(h.a.notice, "on or off") {
		t.Fatalf("a wrong argument says so: %q", h.a.notice)
	}
}
