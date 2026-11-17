package hub

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
	"github.com/MahammadAgayev/neoharness/hub/agent"
	"github.com/MahammadAgayev/neoharness/hub/workspace"
)

// TestNewsIsReadByTheCoordinatorAndThePersonApart: one journal, two
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

// TestNewsLeftOnAThreadMovesToTheJournal: news a thread kept from before the
// journal still reaches the coordinator.
func TestNewsLeftOnAThreadMovesToTheJournal(t *testing.T) {
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

// TestRotateJournalDropsWhatBothHaveReadAndIsOld: an item goes when the
// coordinator's board passed it, the person saw its thread or the thread is
// gone, and it is older than journalKeep; and the journal never holds more
// than journalMax.
func TestRotateJournalDropsWhatBothHaveReadAndIsOld(t *testing.T) {
	now := time.Now()
	old := now.Add(-journalKeep - time.Hour)
	seen := map[string]time.Time{"a": now, "b": old.Add(-time.Hour)}
	items := []workspace.Entry{
		{Seq: 1, At: old, Thread: "a", Kind: jRecap},    // read by both, old: goes
		{Seq: 2, At: old, Thread: "b", Kind: jRecap},    // the person has not seen it: stays
		{Seq: 3, At: old, Thread: "gone", Kind: jRecap}, // its thread is gone: goes
		{Seq: 9, At: old, Thread: "a", Kind: jRecap},    // the board has not passed it: stays
		{Seq: 4, At: now, Thread: "a", Kind: jArtifact}, // too new: stays
	}
	got := rotateJournal(items, 5, func(name string) (time.Time, bool) {
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
	if got := rotateJournal(items, 0, func(string) (time.Time, bool) { return time.Time{}, true }, now, 2); len(got) != 2 || got[0].Seq != 9 {
		t.Fatalf("the cap keeps the newest: %+v", got)
	}
}

// TestTheJournalKeepsWhatHappens: what happens to a session that matters
// outside its chat is in its workspace's journal, in order.
func TestTheJournalKeepsWhatHappens(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	asked := make(chan bool, 1)
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		if strings.Contains(req.Prompt, "run tests") {
			ok, _ := h.Permission(ctx, "demo", "api", agent.Permission{Tool: "Bash", Detail: "go test ./..."})
			asked <- ok
			return agent.Result{Text: "Tests pass.", Tools: 1}
		}
		return agent.Result{Text: "ok"}
	}
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "api", "run tests", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "api", model.StateWaiting)
	v := view(t, h, "demo", "api")
	if err := h.Answer("demo", "api", v.Permission.ID, model.Allow, ""); err != nil {
		t.Fatal(err)
	}
	<-asked
	stateIs(t, h, "demo", "api", model.StateIdle)
	_ = h.Notify("demo", model.Coordinator, "pick a database", true)
	_ = h.Seen("demo", "api", time.Now())
	if err := h.Resolve("demo", model.User, "api"); err != nil {
		t.Fatal(err)
	}
	es, err := workspace.Journal("demo")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range es {
		got = append(got, e.Thread+" "+e.Kind)
	}
	want := []string{"api opened", "api started", "api permission", "api decided", "api turn", "coordinator notice", "api seen", "api resolved"}
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Fatalf("journal = %v\nwant      %v", got, want)
	}
	if e := es[4]; e.Text != "Tests pass." || e.Tools != 1 || es[2].Text != "Bash: go test ./..." || !es[5].NeedsYou {
		t.Fatalf("entries = %+v", es)
	}
	for i := 1; i < len(es); i++ {
		if es[i].Seq <= es[i-1].Seq {
			t.Fatalf("seq does not rise: %+v", es)
		}
	}
}

// TestTheNewsLogBecomesTheJournal: a hub that kept a news log moves it
// into the journal once, and its board still shows what was not read.
func TestTheNewsLogBecomesTheJournal(t *testing.T) {
	h0 := newHub(t, &fake{})
	pinned(t, h0, "demo")
	if _, err := h0.addThread(h0.workspaces["demo"], "run", model.RoleThread, model.Location{Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	h0.Close()
	old := `{"seq":3,"at":"2026-11-17T15:00:04Z","thread":"run","kind":"reply","text":"Moved the store."}` + "\n"
	if err := os.WriteFile(filepath.Join(workspace.Dir("demo"), "news.jsonl"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fake{}
	h, err := New(Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if _, err := os.Stat(filepath.Join(workspace.Dir("demo"), "news.jsonl")); !os.IsNotExist(err) {
		t.Fatal("the news log is still there")
	}
	_ = h.Send("demo", model.User, model.Coordinator, "status?")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	rs := f.requests()
	if p := rs[len(rs)-1].Prompt; !strings.Contains(p, "- run: idle.\n  New: reply: Moved the store.") {
		t.Fatalf("board = %q", p)
	}
	_ = h.Send("demo", model.User, model.Coordinator, "and now?")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if es, _ := workspace.Journal("demo"); es[0].Seq != 3 || es[len(es)-1].Seq <= 3 {
		t.Fatalf("new entries must come after the old ones: %+v", es)
	}
}
