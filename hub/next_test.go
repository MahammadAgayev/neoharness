package hub

import (
	"context"
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
