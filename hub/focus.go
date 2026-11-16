package hub

import (
	"errors"
	"fmt"
	"os"

	"github.com/MahammadAgayev/neoharness/api/model"
	"github.com/MahammadAgayev/neoharness/hub/workspace"
)

// The focus is the person's document of what they work on now, in every
// workspace. It is an artifact of the hub's scope, so the clients edit it as
// any artifact; the attention agent orders the waits list by it.

// hubDoc checks a name in the hub's scope: only the focus lives there.
func hubDoc(name string) error {
	if name != model.FocusDoc {
		return fmt.Errorf("no artifact %q in the hub; it has only %s", name, model.FocusDoc)
	}
	return nil
}

// readFocus is the focus and its state. Before the person writes one it is
// the template at version 0, so their first :w starts it. Callers hold h.mu.
func (h *Hub) readFocus() (model.Artifact, string, error) {
	a, text, err := workspace.Artifact(model.HubScope, model.FocusDoc)
	if errors.Is(err, os.ErrNotExist) {
		a, text, err = model.Artifact{Name: model.FocusDoc, Title: "Focus"}, model.FocusTemplate, nil
	}
	if err != nil {
		return a, "", err
	}
	a.Off, a.Suggestion = h.focus.Off, h.focus.Suggestion
	return a, text, nil
}

// SetFocusOff sets the focus aside, or brings it back. Its text stays.
func (h *Hub) SetFocusOff(off bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.focus.Off == off {
		return nil
	}
	h.focus.Off = off
	return h.focusChanged()
}

// focusChanged saves the focus state and tells the clients. Callers hold
// h.mu.
func (h *Hub) focusChanged() error {
	if err := workspace.SaveFocus(h.focus); err != nil {
		return err
	}
	a, _, err := h.readFocus()
	if err != nil {
		return err
	}
	h.publish(model.Update{Kind: model.UpdateArtifact, Workspace: model.HubScope, Artifact: &a})
	h.attentionSees()
	return nil
}

// docScope checks where a document is: a workspace, or the hub's scope.
// Callers hold h.mu.
func (h *Hub) docScope(workspaceName, name string) error {
	if workspaceName == model.HubScope {
		return hubDoc(name)
	}
	_, err := h.workspace(workspaceName)
	return err
}
