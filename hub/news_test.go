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

// TestNewsIsReadByTheCoordinatorAndThePersonApart: one news log, two
// readers. The coordinator's board takes a thread's news once; the
// attention agent reads what the person has not seen, until they see the
// thread.
func TestNewsIsReadByTheCoordinatorAndThePersonApart(t *testing.T) {
	f := &fake{}
	var h *Hub
	f.script = newsScript(&h)
	attn := &fake{script: func(context.Context, agent.Request) agent.Result { return agent.Result{Text: `{"next":[]}`} }}
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	h, err := New(Config{
		Factory: func(name string, _ workspace.Workspace, _ workspace.Thread) agent.Agent {
			if name == "attention" {
				return attn
			}
			return f
		},
		Attention:      "attention",
		AttentionDelay: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "rec", "please recap as rec", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "rec", model.StateIdle)
	_ = h.Send("demo", model.User, model.Coordinator, "status?")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)

	last := func() string {
		rs := attn.requests()
		return rs[len(rs)-1].Prompt
	}
	h.mu.Lock()
	h.wakeAttention()
	h.mu.Unlock()
	eventually(t, "the agent to read rec's news", func() bool {
		return len(attn.requests()) > 0 && strings.Contains(last(), "- demo · rec: idle. Recap: Cache added.\n  New: recap.")
	})

	_ = h.Seen("demo", "rec", time.Now())
	n := len(attn.requests())
	h.mu.Lock()
	h.wakeAttention()
	h.mu.Unlock()
	eventually(t, "another turn", func() bool { return len(attn.requests()) > n })
	if p := last(); !strings.Contains(p, "- demo · rec: idle. Recap: Cache added.") || strings.Contains(p, "New: recap") {
		t.Fatalf("news the person saw is still new: %s", p)
	}
}

// TestNewsLeftOnAThreadMovesToTheLog: news a thread kept from before the
// log still reaches the coordinator.
func TestNewsLeftOnAThreadMovesToTheLog(t *testing.T) {
	h0 := newHub(t, &fake{})
	pinned(t, h0, "demo")
	h0.Close()
	old := &workspace.Thread{Name: "old", Role: model.RoleThread, News: &workspace.News{Recap: true, Done: []string{"Add the route"}}}
	if err := workspace.SaveThread("demo", old); err != nil {
		t.Fatal(err)
	}
	f := &fake{}
	h, err := New(Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	_ = h.Send("demo", model.User, model.Coordinator, "status?")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	rs := f.requests()
	if p := rs[len(rs)-1].Prompt; !strings.Contains(p, "New: recap; done: Add the route.") {
		t.Fatalf("board = %q", p)
	}
	ts, _ := workspace.Threads("demo")
	for _, t2 := range ts {
		if t2.News != nil {
			t.Fatalf("%s still keeps its news on the thread", t2.Name)
		}
	}
}

// TestRotateNewsDropsWhatBothHaveReadAndIsOld: an item goes when the
// coordinator's board passed it, the person saw its thread or the thread is
// gone, and it is older than newsKeep; and the log never holds more than
// newsMax.
func TestRotateNewsDropsWhatBothHaveReadAndIsOld(t *testing.T) {
	now := time.Now()
	old := now.Add(-newsKeep - time.Hour)
	seen := map[string]time.Time{"a": now, "b": old.Add(-time.Hour)}
	items := []workspace.NewsItem{
		{Seq: 1, At: old, Thread: "a", Kind: newsRecap},    // read by both, old: goes
		{Seq: 2, At: old, Thread: "b", Kind: newsRecap},    // the person has not seen it: stays
		{Seq: 3, At: old, Thread: "gone", Kind: newsRecap}, // its thread is gone: goes
		{Seq: 9, At: old, Thread: "a", Kind: newsRecap},    // the board has not passed it: stays
		{Seq: 4, At: now, Thread: "a", Kind: newsArtifact}, // too new: stays
	}
	got := rotateNews(items, 5, func(name string) (time.Time, bool) {
		at, ok := seen[name]
		return at, ok
	}, now, 10)
	var seqs []int64
	for _, it := range got {
		seqs = append(seqs, it.Seq)
	}
	if len(seqs) != 3 || seqs[0] != 2 || seqs[1] != 9 || seqs[2] != 4 {
		t.Fatalf("kept %v", seqs)
	}
	if got := rotateNews(items, 0, func(string) (time.Time, bool) { return time.Time{}, true }, now, 2); len(got) != 2 || got[0].Seq != 9 {
		t.Fatalf("the cap keeps the newest: %+v", got)
	}
}
