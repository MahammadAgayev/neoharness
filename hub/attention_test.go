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

func lastEvent(t *testing.T, h *Hub, p, name string, kind model.EventKind) model.Event {
	t.Helper()
	evs, err := h.Events(p, name, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Kind == kind {
			return evs[i]
		}
	}
	t.Fatalf("no %s event on %s", kind, name)
	return model.Event{}
}

// TestEventsCarryTheirAttentionLevel: the hub sets the level once, so the
// TUI and the app draw the same thing.
func TestEventsCarryTheirAttentionLevel(t *testing.T) {
	cases := []struct {
		e     model.Event
		role  model.Role
		quiet bool
		want  model.Level
	}{
		{model.Event{Kind: model.EventText}, model.RoleThread, false, model.LevelQuiet},
		{model.Event{Kind: model.EventTool}, model.RoleThread, false, model.LevelQuiet},
		{model.Event{Kind: model.EventMessage, From: model.User}, model.RoleThread, false, model.LevelQuiet},
		{model.Event{Kind: model.EventArtifact}, model.RoleThread, false, model.LevelAmbient},
		{model.Event{Kind: model.EventTurnEnd}, model.RoleThread, false, model.LevelNotice},
		// When the coordinator decides what reaches the person, a thread's
		// turn end is no notice.
		{model.Event{Kind: model.EventTurnEnd}, model.RoleThread, true, model.LevelAmbient},
		{model.Event{Kind: model.EventTurnEnd}, model.RoleCoordinator, true, model.LevelNotice},
		{model.Event{Kind: model.EventNotice}, model.RoleCoordinator, false, model.LevelNotice},
		{model.Event{Kind: model.EventNotice, NeedsYou: true}, model.RoleCoordinator, false, model.LevelNeedsYou},
		{model.Event{Kind: model.EventPermission}, model.RoleThread, false, model.LevelNeedsYou},
		{model.Event{Kind: model.EventAsk}, model.RoleThread, false, model.LevelNeedsYou},
		{model.Event{Kind: model.EventError}, model.RoleThread, false, model.LevelNeedsYou},
	}
	for _, c := range cases {
		if got := level(c.e, c.role, c.quiet); got != c.want {
			t.Errorf("level(%s needsYou=%v, %s, quiet=%v) = %d, want %d", c.e.Kind, c.e.NeedsYou, c.role, c.quiet, got, c.want)
		}
	}

	h := newHub(t, &fake{})
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "hi")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if e := lastEvent(t, h, "demo", model.Coordinator, model.EventTurnEnd); e.Level != model.LevelNotice {
		t.Fatalf("stored turn end level = %d", e.Level)
	}
}

// TestSeenQuietsNoticesButNotWhatNeedsYou: a thread shows its highest open
// level. Seen clears levels 1 and 2 that came before it; a level 3 item
// stays until it is resolved.
func TestSeenQuietsNoticesButNotWhatNeedsYou(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "hi")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if v := view(t, h, "demo", model.Coordinator); v.Level != model.LevelNotice {
		t.Fatalf("level after a turn = %d", v.Level)
	}
	// A seen time before the turn ended does not cover it.
	if err := h.Seen("demo", model.Coordinator, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if v := view(t, h, "demo", model.Coordinator); v.Level != model.LevelNotice {
		t.Fatalf("level after an old seen = %d", v.Level)
	}
	at := time.Now()
	if err := h.Seen("demo", model.Coordinator, at); err != nil {
		t.Fatal(err)
	}
	v := view(t, h, "demo", model.Coordinator)
	if v.Level != model.LevelQuiet || !v.SeenAt.Equal(at.UTC()) {
		t.Fatalf("after seen: level %d, seenAt %v", v.Level, v.SeenAt)
	}

	answered := make(chan bool, 1)
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		ok, _ := h.Permission(ctx, "demo", model.Coordinator, agent.Permission{Tool: "Bash", Detail: "ls"})
		answered <- ok
		return agent.Result{Text: "ok"}
	}
	_ = h.Send("demo", model.User, model.Coordinator, "list")
	stateIs(t, h, "demo", model.Coordinator, model.StateWaiting)
	_ = h.Seen("demo", model.Coordinator, time.Now())
	if v := view(t, h, "demo", model.Coordinator); v.Level != model.LevelNeedsYou {
		t.Fatalf("seen cleared a permission: level %d", v.Level)
	}
	if err := h.Answer("demo", model.Coordinator, "", model.Allow, ""); err != nil {
		t.Fatal(err)
	}
	<-answered
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if v := view(t, h, "demo", model.Coordinator); v.Level != model.LevelNotice {
		t.Fatalf("level after the answered turn = %d, want the turn end's", v.Level)
	}
	if err := h.Seen("demo", "nobody", time.Now()); err == nil {
		t.Fatal("seen of a thread that does not exist")
	}
}

func waitsUpdate(t *testing.T, ch <-chan model.Update, want int) []model.Wait {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case u := <-ch:
			if u.Kind == model.UpdateWaits && len(u.Waits) == want {
				return u.Waits
			}
		case <-timeout:
			t.Fatalf("no waits update with %d items", want)
			return nil
		}
	}
}

// TestTheWaitsListHoldsWhatNeedsYouAcrossWorkspaces: one list, oldest
// first, and an item leaves it only when it is resolved.
func TestTheWaitsListHoldsWhatNeedsYouAcrossWorkspaces(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	ch, stop := h.Subscribe()
	defer stop()
	pinned(t, h, "alpha")
	pinned(t, h, "beta")
	answered := make(chan bool, 1)
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		ok, _ := h.Permission(ctx, "alpha", model.Coordinator, agent.Permission{Tool: "Bash", Detail: "rm -rf build"})
		answered <- ok
		return agent.Result{Text: "ok"}
	}
	_ = h.Send("alpha", model.User, model.Coordinator, "clean")
	stateIs(t, h, "alpha", model.Coordinator, model.StateWaiting)
	if err := h.Notify("beta", model.Coordinator, "pick a database", true); err != nil {
		t.Fatal(err)
	}
	_ = h.Notify("beta", model.Coordinator, "fyi: tests pass", false)

	ws := waitsUpdate(t, ch, 2)
	if ws[0].Workspace != "alpha" || ws[0].Kind != model.WaitPermission || ws[0].Text != "Bash: rm -rf build" || ws[0].ID == "" {
		t.Fatalf("first wait = %+v", ws[0])
	}
	if ws[1].Workspace != "beta" || ws[1].Thread != model.Coordinator || ws[1].Kind != model.WaitNotice || ws[1].Text != "pick a database" {
		t.Fatalf("second wait = %+v", ws[1])
	}
	if got := h.Waits(); len(got) != 2 || got[0].ID != ws[0].ID {
		t.Fatalf("Waits() = %+v", got)
	}
	if !ws[0].Since.Before(ws[1].Since) {
		t.Fatal("not oldest first")
	}

	// Being seen resolves nothing.
	_ = h.Seen("beta", model.Coordinator, time.Now())
	if got := h.Waits(); len(got) != 2 {
		t.Fatalf("seen resolved a wait: %+v", got)
	}
	// A permission cannot be dismissed: it is answered.
	if err := h.Dismiss("alpha", model.Coordinator, ws[0].ID); err == nil {
		t.Fatal("dismissed a permission")
	}
	_ = h.Answer("alpha", model.Coordinator, ws[0].ID, model.Deny, "")
	<-answered
	if got := waitsUpdate(t, ch, 1); got[0].Kind != model.WaitNotice {
		t.Fatalf("after the answer = %+v", got)
	}
	if err := h.Dismiss("beta", model.Coordinator, ws[1].ID); err != nil {
		t.Fatal(err)
	}
	waitsUpdate(t, ch, 0)

	// The person's message to the coordinator answers its notices too.
	_ = h.Notify("beta", model.Coordinator, "approve the plan", true)
	waitsUpdate(t, ch, 1)
	f.script = nil
	_ = h.Send("beta", model.User, model.Coordinator, "approved")
	waitsUpdate(t, ch, 0)
}

// TestFailuresAndNoticesWaitAcrossARestart: the waits list is kept with
// the threads, so a restart loses none of it. A failure leaves when the
// thread runs again or the person dismisses it.
func TestFailuresAndNoticesWaitAcrossARestart(t *testing.T) {
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	f := &fake{script: func(ctx context.Context, req agent.Request) agent.Result {
		if strings.Contains(req.Prompt, "crash") {
			return agent.Result{ExitCode: 1, Stderr: "fake: crashed on purpose"}
		}
		return agent.Result{Text: "ok"}
	}}
	cfg := Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }}
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "flaky", "crash", ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the failure to wait", func() bool { return len(h.Waits()) == 1 })
	_ = h.Notify("demo", model.Coordinator, "look at flaky", true)
	before := h.Waits()
	h.Close()

	h2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h2.Close)
	ws := h2.Waits()
	if len(ws) != 2 || ws[0].Kind != model.WaitFailed || ws[0].Thread != "flaky" || !strings.Contains(ws[0].Text, "crashed on purpose") ||
		ws[1].Kind != model.WaitNotice || ws[1].ID != before[1].ID {
		t.Fatalf("waits after the restart = %+v", ws)
	}
	if v := view(t, h2, "demo", "flaky"); v.Level != model.LevelNeedsYou {
		t.Fatalf("failed thread level = %d", v.Level)
	}
	if err := h2.Dismiss("demo", "flaky", ws[0].ID); err != nil {
		t.Fatal(err)
	}
	if ws := h2.Waits(); len(ws) != 1 || ws[0].Kind != model.WaitNotice {
		t.Fatalf("after dismissing the failure = %+v", ws)
	}
	if err := h2.Dismiss("demo", model.Coordinator, "n-nothing"); err == nil {
		t.Fatal("dismissed a wait that does not exist")
	}

	// A failure also leaves when the thread runs again and ends well.
	_ = h2.Send("demo", model.User, "flaky", "crash again")
	eventually(t, "the second failure", func() bool { return len(h2.Waits()) == 2 })
	_ = h2.Send("demo", model.User, "flaky", "now work")
	eventually(t, "the failure to clear", func() bool { return len(h2.Waits()) == 1 })
}

// TestAFailedTurnLeavesTheThreadFailed: a thread whose last turn failed
// shows so, the coordinator too, until the person dismisses it or the
// thread runs again.
func TestAFailedTurnLeavesTheThreadFailed(t *testing.T) {
	f := &fake{script: func(ctx context.Context, req agent.Request) agent.Result {
		if strings.Contains(req.Prompt, "crash") {
			return agent.Result{ExitCode: 1, Stderr: "fake: crashed on purpose"}
		}
		return agent.Result{Text: "ok"}
	}}
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "flaky", "crash", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "flaky", model.StateFailed)
	if err := h.Dismiss("demo", "flaky", "failed"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "flaky", model.StateIdle)

	_ = h.Send("demo", model.User, "flaky", "crash again")
	stateIs(t, h, "demo", "flaky", model.StateFailed)
	if c := workspaceView(t, h, "demo").Counts[model.StateFailed]; c != 1 {
		t.Fatalf("failed count = %d", c)
	}
	_ = h.Send("demo", model.User, "flaky", "now work")
	stateIs(t, h, "demo", "flaky", model.StateIdle)

	_ = h.Send("demo", model.User, model.Coordinator, "crash")
	stateIs(t, h, "demo", model.Coordinator, model.StateFailed)
}
