package hub

import (
	"strings"
	"testing"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
	"github.com/MahammadAgayev/neoharness/hub/agent"
	"github.com/MahammadAgayev/neoharness/hub/workspace"
)

// TestTheFocusIsADocumentOfTheHub: the focus is in no workspace. Before the
// person writes it, it reads as the template at version 0; their first :w
// makes version 1. Nothing else lives in the hub's scope.
func TestTheFocusIsADocumentOfTheHub(t *testing.T) {
	h := newHub(t, &fake{})
	ch, stop := h.Subscribe()
	defer stop()
	a, text, err := h.ReadArtifact(model.HubScope, model.FocusDoc)
	if err != nil || a.Version != 0 || text != model.FocusTemplate || a.Name != model.FocusDoc {
		t.Fatalf("a new focus = %+v %q %v", a, text, err)
	}
	doc := strings.Replace(model.FocusTemplate, "## Now\n", "## Now\n- ship the refactor\n", 1)
	a, _, err = h.EditArtifact(model.HubScope, model.FocusDoc, doc, model.FocusTemplate, 0, false)
	if err != nil || a.Version != 1 || !a.UserEdit {
		t.Fatalf("first save = %+v %v", a, err)
	}
	for u := range ch {
		if u.Kind == model.UpdateArtifact {
			if u.Workspace != model.HubScope || u.Artifact.Name != model.FocusDoc {
				t.Fatalf("update = %+v", u)
			}
			break
		}
	}
	if _, text, _ := h.ReadArtifact(model.HubScope, model.FocusDoc); text != doc {
		t.Fatalf("read back %q", text)
	}
	if _, _, err := h.ReadArtifact(model.HubScope, "plan"); err == nil {
		t.Fatal("the hub has only the focus")
	}
	if _, _, err := h.EditArtifact(model.HubScope, "plan", doc, "", 0, false); err == nil {
		t.Fatal("wrote another document to the hub")
	}
	if _, err := h.Artifacts(model.HubScope); err == nil {
		t.Fatal("the hub's scope is no workspace")
	}
	if ws := h.Workspaces(); len(ws) != 0 {
		t.Fatalf("the focus made a workspace: %+v", ws)
	}
}

// TestFocusOffSetsItAsideAcrossARestart: :focus off keeps the text and says
// so on the focus, until :focus on.
func TestFocusOffSetsItAsideAcrossARestart(t *testing.T) {
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	cfg := Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return &fake{} }}
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.SetFocusOff(true); err != nil {
		t.Fatal(err)
	}
	if a, _, _ := h.ReadArtifact(model.HubScope, model.FocusDoc); !a.Off {
		t.Fatal("not set aside")
	}
	h.Close()
	h2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h2.Close)
	if a, _, _ := h2.ReadArtifact(model.HubScope, model.FocusDoc); !a.Off {
		t.Fatal("a restart brought it back")
	}
	_ = h2.SetFocusOff(false)
	if a, _, _ := h2.ReadArtifact(model.HubScope, model.FocusDoc); a.Off {
		t.Fatal(":focus on did not bring it back")
	}
	_ = time.Now
}
