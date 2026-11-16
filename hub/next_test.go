package hub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
	"github.com/MahammadAgayev/neoharness/hub/agent"
	"github.com/MahammadAgayev/neoharness/hub/workspace"
)

// attentionHub is a hub whose attention agent is attn, woken at once.
func attentionHub(t *testing.T, f, attn *fake) *Hub {
	t.Helper()
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	h, err := New(Config{
		Factory: func(name string, _ workspace.Workspace, _ workspace.Thread) agent.Agent {
			if name == "attention" {
				return attn
			}
			return f
		},
		MCPPort:        4242,
		Agents:         map[string]Defaults{"attention": {Model: "small"}},
		Attention:      "attention",
		AttentionDelay: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

// TestTheAttentionAgentOrdersTheWaitsAndBriefsThem: the agent reads what
// waits and the focus, and its order is the order ]q walks, with a why and
// a brief for each stop. A brief stays while its wait is the same.
func TestTheAttentionAgentOrdersTheWaitsAndBriefsThem(t *testing.T) {
	attn := &fake{}
	attn.script = func(ctx context.Context, req agent.Request) agent.Result {
		if !strings.Contains(req.Prompt, "pick a database") {
			return agent.Result{Text: `{"next":[]}`}
		}
		return agent.Result{Text: "Here:\n```json\n" + `{"next":[{"i":2,"why":"in the focus","brief":"It asks which database. Suggest: postgres."},{"i":1,"why":"outside the focus"}]}` + "\n```"}
	}
	h := attentionHub(t, &fake{}, attn)
	pinned(t, h, "alpha")
	pinned(t, h, "beta")
	_ = h.Notify("alpha", model.Coordinator, "approve the plan", true)
	_ = h.Notify("beta", model.Coordinator, "pick a database", true)

	eventually(t, "the agent's order", func() bool {
		ws := h.Waits()
		return len(ws) == 2 && ws[0].Workspace == "beta" && ws[0].Why == "in the focus"
	})
	ws := h.Waits()
	if ws[0].Brief != "It asks which database. Suggest: postgres." || ws[1].Why != "outside the focus" {
		t.Fatalf("waits = %+v", ws)
	}
	req := attn.requests()[len(attn.requests())-1]
	if !req.NoTools || req.Model != "small" || req.Resume || req.Conversation != "" {
		t.Fatalf("the attention turn = %+v", req)
	}
	if !strings.Contains(req.Prompt, "[1] alpha · coordinator · notice") || !strings.Contains(req.Prompt, "No focus") {
		t.Fatalf("prompt = %s", req.Prompt)
	}

	// Its own order does not wake it again.
	runs := len(attn.requests())
	time.Sleep(100 * time.Millisecond)
	if n := len(attn.requests()); n != runs {
		t.Fatalf("the agent woke itself: %d runs, then %d", runs, n)
	}

	// Saving the focus wakes it, and it reads the focus.
	doc := strings.Replace(model.FocusTemplate, "## Now\n", "## Now\n- choose the database\n", 1)
	if _, _, err := h.EditArtifact(model.HubScope, model.FocusDoc, doc, model.FocusTemplate, 0, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a wake for the focus", func() bool { return len(attn.requests()) > runs })
	req = attn.requests()[len(attn.requests())-1]
	if !strings.Contains(req.Prompt, "choose the database") {
		t.Fatalf("the focus is not in the prompt: %s", req.Prompt)
	}
	for _, l := range strings.Split(req.Prompt, "\n") {
		if strings.Contains(l, "pick a database") && strings.HasSuffix(l, "· new") {
			t.Fatalf("a briefed wait is not new: %s", l)
		}
	}

	// With the focus aside, the agent does not read it.
	_ = h.SetFocusOff(true)
	eventually(t, "a wake for :focus off", func() bool {
		return strings.Contains(attn.requests()[len(attn.requests())-1].Prompt, "No focus")
	})
}

// TestTheHubsOrderStandsWhenTheAgentFails: a failed or garbled turn keeps
// the order the hub had.
func TestTheHubsOrderStandsWhenTheAgentFails(t *testing.T) {
	attn := &fake{script: func(ctx context.Context, req agent.Request) agent.Result {
		return agent.Result{Text: "I cannot do that."}
	}}
	h := attentionHub(t, &fake{}, attn)
	pinned(t, h, "alpha")
	_ = h.Notify("alpha", model.Coordinator, "approve the plan", true)
	eventually(t, "a wake", func() bool { return len(attn.requests()) > 0 })
	time.Sleep(50 * time.Millisecond)
	if ws := h.Waits(); len(ws) != 1 || ws[0].Why != "" {
		t.Fatalf("waits = %+v", ws)
	}
}

// TestNoAttentionAgentNoWakes: with attention off, nothing runs.
func TestNoAttentionAgentNoWakes(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	pinned(t, h, "alpha")
	_ = h.Notify("alpha", model.Coordinator, "approve the plan", true)
	time.Sleep(50 * time.Millisecond)
	for _, r := range f.requests() {
		if r.NoTools {
			t.Fatal("an attention turn ran with attention off")
		}
	}
}

// TestTheAgentSuggestsAnEditOfTheFocus: with focus_edit = "ask" the edit
// waits for the person, as the last stop; their save or x ends it, and a
// dismissed edit is not suggested again. With "allow" it is saved at once,
// as the agent's version.
func TestTheAgentSuggestsAnEditOfTheFocus(t *testing.T) {
	doc := strings.Replace(model.FocusTemplate, "## Now\n", "## Now\n- hub tests green\n", 1)
	struck := strings.Replace(doc, "- hub tests green", "- ~~hub tests green~~", 1)
	attn := &fake{}
	attn.script = func(ctx context.Context, req agent.Request) agent.Result {
		if !strings.Contains(req.Prompt, "hub tests green") || strings.Contains(req.Prompt, "~~hub tests green~~") {
			return agent.Result{Text: `{"next":[]}`}
		}
		return agent.Result{Text: `{"next":[], "edit": {"text": ` + quote(struck) + `, "why": "go test ./hub/... passed in alpha"}}`}
	}
	h := attentionHub(t, &fake{}, attn)
	if _, _, err := h.EditArtifact(model.HubScope, model.FocusDoc, doc, model.FocusTemplate, 0, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the suggestion", func() bool {
		a, _, _ := h.ReadArtifact(model.HubScope, model.FocusDoc)
		return a.Suggestion != nil
	})
	a, text, _ := h.ReadArtifact(model.HubScope, model.FocusDoc)
	if text != doc || a.Version != 1 || a.Suggestion.Text != struck || a.Suggestion.Why != "go test ./hub/... passed in alpha" || a.Suggestion.Base != 1 {
		t.Fatalf("focus = %+v %q", a, text)
	}
	ws := h.Waits()
	if len(ws) != 1 || ws[0].Kind != model.WaitFocus || ws[0].Workspace != model.HubScope || ws[0].Thread != model.FocusDoc || ws[0].ID != model.SuggestionID || ws[0].Text != a.Suggestion.Why {
		t.Fatalf("waits = %+v", ws)
	}
	if !strings.Contains(attn.requests()[0].SystemPrompt, `"edit"`) {
		t.Fatal("the agent is not told it may suggest an edit")
	}

	// x drops it, and the same edit is not suggested again.
	if err := h.Dismiss(model.HubScope, "", model.SuggestionID); err != nil {
		t.Fatal(err)
	}
	if a, _, _ := h.ReadArtifact(model.HubScope, model.FocusDoc); a.Suggestion != nil || len(h.Waits()) != 0 {
		t.Fatal("x left the suggestion")
	}
	runs := len(attn.requests())
	_ = h.SetFocusOff(true)
	_ = h.SetFocusOff(false)
	eventually(t, "another turn", func() bool { return len(attn.requests()) > runs })
	time.Sleep(50 * time.Millisecond)
	if a, _, _ := h.ReadArtifact(model.HubScope, model.FocusDoc); a.Suggestion != nil {
		t.Fatal("a dismissed edit came back")
	}

	// The person's save ends a suggestion too.
	h.mu.Lock()
	h.focus.Dismissed = ""
	h.mu.Unlock()
	doc2 := doc + "\nmore\n"
	if _, _, err := h.EditArtifact(model.HubScope, model.FocusDoc, doc2, doc, 1, false); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.focus.Suggestion = &model.Suggestion{Text: struck, Base: 2}
	h.mu.Unlock()
	if _, _, err := h.EditArtifact(model.HubScope, model.FocusDoc, doc2+"x\n", doc2, 2, false); err != nil {
		t.Fatal(err)
	}
	if a, _, _ := h.ReadArtifact(model.HubScope, model.FocusDoc); a.Suggestion != nil {
		t.Fatal("a save left the suggestion")
	}
}

func TestFocusEditAllowSavesTheAgentsEdit(t *testing.T) {
	doc := strings.Replace(model.FocusTemplate, "## Now\n", "## Now\n- hub tests green\n", 1)
	struck := strings.Replace(doc, "- hub tests green", "- ~~hub tests green~~", 1)
	attn := &fake{script: func(ctx context.Context, req agent.Request) agent.Result {
		return agent.Result{Text: `{"next":[], "edit": {"text": ` + quote(struck) + `, "why": "tests pass"}}`}
	}}
	h := attentionHub(t, &fake{}, attn)
	h.cfg.FocusEdit = FocusEditAllow
	if _, _, err := h.EditArtifact(model.HubScope, model.FocusDoc, doc, model.FocusTemplate, 0, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the agent's version", func() bool {
		a, text, _ := h.ReadArtifact(model.HubScope, model.FocusDoc)
		return a.Version == 2 && text == struck && !a.UserEdit && a.Thread == "attention" && a.Suggestion == nil
	})
}

func TestFocusEditOffSuggestsNothing(t *testing.T) {
	attn := &fake{}
	h := attentionHub(t, &fake{}, attn)
	h.cfg.FocusEdit = FocusEditOff
	_ = h.SetFocusOff(true)
	eventually(t, "a turn", func() bool { return len(attn.requests()) > 0 })
	if strings.Contains(attn.requests()[0].SystemPrompt, `"edit"`) {
		t.Fatal("focus_edit = off still offers edits")
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
