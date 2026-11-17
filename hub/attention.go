package hub

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// level is the attention an event asks for. quiet says the coordinator
// decides what of the threads reaches the person.
func level(e model.Event, role model.Role, quiet bool) model.Level {
	switch e.Kind {
	case model.EventPermission, model.EventAsk, model.EventError:
		return model.LevelNeedsYou
	case model.EventNotice:
		if e.NeedsYou {
			return model.LevelNeedsYou
		}
		return model.LevelNotice
	case model.EventTurnEnd:
		// The work goes on; the turn that ends it notifies.
		if e.Background > 0 {
			return model.LevelQuiet
		}
		if quiet && role == model.RoleThread {
			return model.LevelAmbient
		}
		return model.LevelNotice
	case model.EventArtifact:
		return model.LevelAmbient
	}
	return model.LevelQuiet
}

// waitsOf is what waits on the person in one thread.
func waitsOf(ws *space, th *thread) []model.Wait {
	w, _, _ := needsOf(ws, th)
	return w
}

// rank is where a kind of wait goes in the list: what blocks a turn first,
// news that blocks nothing last.
func rank(k model.WaitKind) int {
	switch k {
	case model.WaitPermission, model.WaitQuestion:
		return 0
	case model.WaitReview:
		return 2
	case model.WaitFocus:
		return 3
	}
	return 1
}

// review is a thread's news the person has not read, when it rests: a turn
// end or a notice. Under notify = "coordinator" a thread's turn end is only
// ambient, so the coordinator's word is the review instead.
func review(ws *space, th *thread) (model.Wait, bool) {
	_, unseen, at := needsOf(ws, th)
	if unseen != model.LevelNotice || th.state != model.StateIdle {
		return model.Wait{}, false
	}
	text := th.t.Reply
	if text == "" {
		text = "finished a turn"
	}
	return model.Wait{Workspace: ws.p.Name, Thread: th.t.Name, Kind: model.WaitReview, ID: model.ReviewID, Text: text, Since: at}, true
}

// failedID names a thread's failure in the waits list; a thread has at
// most one.
const failedID = "failed"

// failed says the thread's last turn failed and nothing came after it.
func failed(th *thread) bool {
	return th.f.failure != nil && !th.t.Resolved && !th.busy && len(th.t.Queue) == 0
}

// failure is why the thread's last turn failed, while that is open.
func failure(th *thread) string {
	if th.f.failure == nil {
		return ""
	}
	return th.f.failure.Text
}

// threadLevel is the highest level still open on a thread.
func threadLevel(ws *space, th *thread) model.Level {
	w, unseen, _ := needsOf(ws, th)
	if len(w) > 0 {
		return model.LevelNeedsYou
	}
	return unseen
}

// Waits is what needs the person in every workspace, oldest first.
func (h *Hub) Waits() []model.Wait {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.waits()
}

// hubWaits is the waits list in the hub's own order, before the attention
// agent's. Callers hold h.mu.
func (h *Hub) hubWaits() []model.Wait {
	out := []model.Wait{}
	for _, ws := range h.workspaces {
		for _, th := range ws.threads {
			if th.deleted {
				continue
			}
			w := waitsOf(ws, th)
			if r, ok := review(ws, th); ok && len(w) == 0 {
				w = append(w, r)
			}
			out = append(out, w...)
		}
	}
	if s := h.focus.Suggestion; s != nil {
		out = append(out, model.Wait{Workspace: model.HubScope, Thread: model.FocusDoc, Kind: model.WaitFocus, ID: model.SuggestionID, Text: s.Why, Since: s.At})
	}
	slices.SortStableFunc(out, func(a, b model.Wait) int {
		if c := rank(a.Kind) - rank(b.Kind); c != 0 {
			return c
		}
		if c := a.Since.Compare(b.Since); c != 0 {
			return c
		}
		return strings.Compare(a.Workspace+"\x00"+a.Thread+"\x00"+a.ID, b.Workspace+"\x00"+b.Thread+"\x00"+b.ID)
	})
	return out
}

// publishWaits sends the waits list when it changed, and wakes the
// attention agent when what it reads changed. Callers hold h.mu.
func (h *Hub) publishWaits() {
	defer h.attentionSees()
	ws := h.waits()
	if slices.EqualFunc(ws, h.lastWaits, func(a, b model.Wait) bool {
		return a.Workspace == b.Workspace && a.Thread == b.Thread && a.Kind == b.Kind && a.ID == b.ID && a.Text == b.Text && a.Since.Equal(b.Since) &&
			a.Why == b.Why && a.Brief == b.Brief
	}) {
		return
	}
	h.lastWaits = ws
	h.publish(model.Update{Kind: model.UpdateWaits, Waits: ws})
}

// Seen says the person saw a thread at a time: what came before it at
// levels 1 and 2 is quiet now. What needs them stays until it is resolved.
func (h *Hub) Seen(workspaceName, name string, at time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, th, err := h.lookup(workspaceName, name)
	if err != nil {
		return err
	}
	at = at.UTC()
	if now := time.Now().UTC(); at.IsZero() || at.After(now) {
		at = now
	}
	if !at.After(th.f.seen.At) {
		return nil
	}
	h.note(ws, th, model.JournalEntry{At: at, Kind: model.JournalSeen})
	h.changed(ws, th)
	return nil
}

// Dismiss takes a notice, a failure or a review off the waits list. A permission or
// a question is answered instead.
func (h *Hub) Dismiss(workspaceName, name, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if workspaceName == model.HubScope {
		return h.dismissSuggestion(id)
	}
	ws, th, err := h.lookup(workspaceName, name)
	if err != nil {
		return err
	}
	_, unseen, _ := needsOf(ws, th)
	switch {
	case id == failedID && failed(th):
	case id == model.ReviewID && unseen > 0:
		// x on a review reads it.
	case slices.ContainsFunc(th.f.notices, func(n model.JournalEntry) bool { return n.ID == id }):
	case slices.ContainsFunc(th.perms, func(p *pending) bool { return p.ID == id }), id != "" && id == questionID(th):
		return errors.New("answer it instead: allow, deny, or skip")
	default:
		return fmt.Errorf("nothing waits as %q on %s", id, name)
	}
	h.note(ws, th, model.JournalEntry{Kind: model.JournalDismissed, ID: id})
	h.changed(ws, th)
	return nil
}
