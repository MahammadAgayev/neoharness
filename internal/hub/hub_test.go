package hub

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MahammadAgayev/neoharness/internal/agent"
	"github.com/MahammadAgayev/neoharness/internal/model"
	"github.com/MahammadAgayev/neoharness/internal/project"
)

// fake is a scripted agent. Each turn calls script, which may block, emit
// events, or call back into the hub the way an MCP tool call would.
type fake struct {
	mu     sync.Mutex
	reqs   []agent.Request
	script func(ctx context.Context, req agent.Request) agent.Result
}

func (f *fake) Name() string { return "fake" }

func (f *fake) Run(ctx context.Context, req agent.Request) (agent.Result, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	script := f.script
	f.mu.Unlock()
	if script == nil {
		if req.OnEvent != nil {
			req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: "ok"})
		}
		return agent.Result{Text: "ok"}, nil
	}
	return script(ctx, req), nil
}

func (f *fake) requests() []agent.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agent.Request(nil), f.reqs...)
}

func newHub(t *testing.T, f *fake) *Hub {
	t.Helper()
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	h, err := New(Config{Factory: func(project.Project, project.Thread) agent.Agent { return f }, MCPPort: 4242})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

func pinned(t *testing.T, h *Hub, name string) {
	t.Helper()
	if err := h.CreateProject(name, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func view(t *testing.T, h *Hub, p, name string) model.ThreadView {
	t.Helper()
	vs, err := h.Threads(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("no thread %s", name)
	return model.ThreadView{}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func stateIs(t *testing.T, h *Hub, p, name string, s model.State) {
	t.Helper()
	eventually(t, name+" to be "+string(s), func() bool { return view(t, h, p, name).State == s })
}

func TestProjectGetsACoordinator(t *testing.T) {
	h := newHub(t, &fake{})
	pinned(t, h, "demo")
	vs, _ := h.Threads("demo")
	if len(vs) != 1 || vs[0].Name != model.Coordinator || vs[0].Role != model.RoleCoordinator {
		t.Fatalf("threads = %+v", vs)
	}
	if err := h.CreateProject("demo", ""); err == nil {
		t.Fatal("duplicate project should fail")
	}
	// Unpinned projects still get a coordinator, standing in their own folder.
	if err := h.CreateProject("loose", ""); err != nil {
		t.Fatal(err)
	}
	if err := h.OpenThread("loose", model.User, "x", "", ""); err == nil {
		t.Fatal("a thread in an unpinned project needs a location")
	}
}

func TestTurnLifecycleAndResume(t *testing.T) {
	gate := make(chan struct{})
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		<-gate
		req.OnEvent(agent.Event{Kind: agent.KindToolStart, Tool: "Read", Detail: "a.go"})
		req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: "done"})
		return agent.Result{Text: "done", Tools: 1}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")

	if err := h.Send("demo", model.User, model.Coordinator, "hello"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateRunning)
	gate <- struct{}{}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)

	if err := h.Send("demo", model.User, model.Coordinator, "again"); err != nil {
		t.Fatal(err)
	}
	gate <- struct{}{}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)

	reqs := f.requests()
	if len(reqs) != 2 {
		t.Fatalf("%d turns, want 2", len(reqs))
	}
	if reqs[0].Resume || !reqs[1].Resume || reqs[0].Conversation != reqs[1].Conversation {
		t.Errorf("first turn must create the session and the second resume it: %+v / %+v", reqs[0], reqs[1])
	}
	if reqs[0].PermissionPromptTool != PermissionTool || !strings.Contains(reqs[0].MCPConfig, "127.0.0.1:4242") {
		t.Errorf("turn not wired to the MCP server: %+v", reqs[0])
	}
	if !strings.Contains(reqs[0].SystemPrompt, "coordinator") {
		t.Error("coordinator should get the coordinator prompt")
	}

	evs, _ := h.Events("demo", model.Coordinator, 0)
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, string(e.Kind))
	}
	want := "message tool text turn message tool text turn"
	if strings.Join(kinds, " ") != want {
		t.Errorf("events = %v, want %s", kinds, want)
	}
}

func TestMessagesQueueWhileBusyAndBatch(t *testing.T) {
	gate := make(chan struct{})
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		<-gate
		return agent.Result{Text: "ok"}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "api-tests", "write tests", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "api-tests", model.StateRunning)
	_ = h.Send("demo", "auth-refactor", "api-tests", "touching session.go?")
	_ = h.Send("demo", model.User, "api-tests", "also add a benchmark")
	if v := view(t, h, "demo", "api-tests"); v.Queued != 2 {
		t.Fatalf("queued = %d, want 2", v.Queued)
	}
	gate <- struct{}{}
	gate <- struct{}{}
	stateIs(t, h, "demo", "api-tests", model.StateIdle)

	reqs := f.requests()
	if len(reqs) != 2 {
		t.Fatalf("%d turns, want 2 (queued messages go in one turn)", len(reqs))
	}
	if !strings.HasPrefix(reqs[0].Prompt, "Message from coordinator:\nwrite tests") {
		t.Errorf("first prompt = %q", reqs[0].Prompt)
	}
	p := reqs[1].Prompt
	if !strings.Contains(p, "Message from auth-refactor:\ntouching session.go?") || !strings.Contains(p, "also add a benchmark") {
		t.Errorf("batched prompt = %q", p)
	}
	if strings.Contains(p, "Message from user") {
		t.Error("the user's own words are not labelled")
	}
}

func TestNamesAreChecked(t *testing.T) {
	h := newHub(t, &fake{})
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "api-tests", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"api-tests", "coordinator", "Bad Name", "user"} {
		if err := h.OpenThread("demo", model.Coordinator, name, "", ""); err == nil {
			t.Errorf("OpenThread(%q) should fail", name)
		}
	}
	if err := h.Send("demo", model.User, "nobody", "hi"); err == nil {
		t.Error("sending to a missing thread should fail")
	}
	if err := h.Send("demo", "api-tests", "api-tests", "hi"); err == nil {
		t.Error("a thread messaging itself should fail")
	}
}

func TestPermissionWaitsForTheUser(t *testing.T) {
	type outcome struct {
		ok  bool
		msg string
	}
	results := make(chan outcome, 4)
	f := &fake{}
	h := newHub(t, f)
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		for _, cmd := range []string{"go test ./...", "go vet ./...", "go test ./... && rm -rf /"} {
			in, _ := json.Marshal(map[string]string{"command": cmd})
			ok, msg := h.Permission(ctx, "demo", model.Coordinator, "Bash", in)
			results <- outcome{ok, msg}
		}
		return agent.Result{Text: "ok"}
	}
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "run tests")

	stateIs(t, h, "demo", model.Coordinator, model.StateWaiting)
	v := view(t, h, "demo", model.Coordinator)
	if v.Permission == nil || v.Permission.Tool != "Bash" || v.Permission.Detail != "go test ./..." {
		t.Fatalf("permission = %+v", v.Permission)
	}
	if err := h.Answer("demo", model.Coordinator, v.Permission.ID, model.Always, ""); err != nil {
		t.Fatal(err)
	}
	if r := <-results; !r.ok {
		t.Fatal("allowed call was denied")
	}
	// The rule allows the next `go` command without asking.
	if r := <-results; !r.ok {
		t.Fatal("always-allow rule did not apply")
	}
	// But not a chained command that starts with the same word.
	stateIs(t, h, "demo", model.Coordinator, model.StateWaiting)
	if err := h.Answer("demo", model.Coordinator, "", model.Deny, "too risky"); err != nil {
		t.Fatal(err)
	}
	if r := <-results; r.ok || r.msg != "too risky" {
		t.Fatalf("deny = %+v", r)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
}

func TestPendingPermissionIsDeniedWhenTheTurnEnds(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	done := make(chan bool)
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		go func() {
			ok, _ := h.Permission(context.Background(), "demo", model.Coordinator, "Write", json.RawMessage(`{"file_path":"x"}`))
			done <- ok
		}()
		eventually(t, "permission to be pending", func() bool { return view(t, h, "demo", model.Coordinator).Permission != nil })
		return agent.Result{Text: "ok"}
	}
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "go")
	if <-done {
		t.Fatal("a permission outliving its turn must be denied")
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
}

func TestAskUserWaitsAndUserReplyClears(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		if !strings.Contains(req.Prompt, "postgres") {
			_ = h.AskUser("demo", model.Coordinator, "which database?")
		}
		return agent.Result{Text: "ok"}
	}
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "set up storage")
	stateIs(t, h, "demo", model.Coordinator, model.StateWaiting)
	if v := view(t, h, "demo", model.Coordinator); v.Waiting != "which database?" {
		t.Fatalf("waiting = %q", v.Waiting)
	}
	_ = h.Send("demo", model.User, model.Coordinator, "postgres")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
}

func TestResolveRefusesMessagesAndReopenKeepsSession(t *testing.T) {
	gate := make(chan struct{})
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		select {
		case <-gate:
		case <-ctx.Done():
		}
		req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: "partial"})
		return agent.Result{Text: "ok"}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	_ = h.OpenThread("demo", model.Coordinator, "docs", "write docs", "")
	stateIs(t, h, "demo", "docs", model.StateRunning)

	// Resolving a running thread cancels its turn.
	if err := h.Resolve("demo", model.Coordinator, "docs"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "docs", model.StateResolved)
	if err := h.Send("demo", model.User, "docs", "more"); err == nil {
		t.Fatal("a resolved thread must refuse messages")
	}
	if err := h.Resolve("demo", model.User, model.Coordinator); err == nil {
		t.Fatal("the coordinator cannot be resolved")
	}

	if err := h.Reopen("demo", "docs"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "docs", model.StateIdle)
	_ = h.Send("demo", model.User, "docs", "continue")
	gate <- struct{}{}
	stateIs(t, h, "demo", "docs", model.StateIdle)
	reqs := f.requests()
	last := reqs[len(reqs)-1]
	if !last.Resume || last.Conversation != reqs[0].Conversation {
		t.Errorf("reopened thread must resume its session: %+v", last)
	}
}

func TestLoopGuardPausesAgentPingPong(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	h.cfg.MaxAgentTurns = 3
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		return agent.Result{Text: "ok"}
	}
	pinned(t, h, "demo")
	_ = h.OpenThread("demo", model.User, "a", "", "")
	for i := range 4 {
		_ = h.Send("demo", "coordinator", "a", "ping")
		if i < 3 {
			stateIs(t, h, "demo", "a", model.StateIdle)
		}
	}
	stateIs(t, h, "demo", "a", model.StateWaiting)
	if v := view(t, h, "demo", "a"); v.Waiting != "paused" || v.Queued != 1 {
		t.Fatalf("view = %+v", v)
	}
	if n := len(f.requests()); n != 3 {
		t.Fatalf("%d turns ran, want 3", n)
	}
	// The user writing lifts the pause and the queued message goes too.
	_ = h.Send("demo", model.User, "a", "carry on")
	stateIs(t, h, "demo", "a", model.StateIdle)
	if n := len(f.requests()); n != 4 {
		t.Fatalf("%d turns ran, want 4", n)
	}
}

func TestRestartReportsTheLostTurn(t *testing.T) {
	block := make(chan struct{})
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return agent.Result{Text: "ok"}
	}
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	factory := func(project.Project, project.Thread) agent.Agent { return f }
	h, err := New(Config{Factory: factory})
	if err != nil {
		t.Fatal(err)
	}
	_ = h.CreateProject("demo", t.TempDir())
	_ = h.Send("demo", model.User, model.Coordinator, "work")
	eventually(t, "turn to start", func() bool { return len(f.requests()) == 1 })

	// Simulate a crash: the turn is on disk as in flight.
	ths, _ := project.Threads("demo")
	if len(ths[0].InFlight) != 1 {
		t.Fatalf("in-flight not persisted: %+v", ths[0])
	}
	h2, err := New(Config{Factory: factory})
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()
	evs, _ := h2.Events("demo", model.Coordinator, 1)
	if len(evs) != 1 || evs[0].Kind != model.EventError || !strings.Contains(evs[0].Text, "lost") {
		t.Fatalf("last event = %+v", evs)
	}
	if v := view(t, h2, "demo", model.Coordinator); v.State != model.StateIdle {
		t.Fatalf("state after restart = %s", v.State)
	}
	close(block)
	h.Close()
}

func TestSubscribersSeeUpdates(t *testing.T) {
	h := newHub(t, &fake{})
	ch, stop := h.Subscribe()
	defer stop()
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "hi")
	var sawEvent, sawThread bool
	timeout := time.After(2 * time.Second)
	for !sawEvent || !sawThread {
		select {
		case u := <-ch:
			sawEvent = sawEvent || (u.Kind == model.UpdateEvent && u.Event.Kind == model.EventText)
			sawThread = sawThread || u.Kind == model.UpdateThread
		case <-timeout:
			t.Fatal("no updates")
		}
	}
}

func TestHarnessToolsNeverAsk(t *testing.T) {
	h := newHub(t, &fake{})
	pinned(t, h, "demo")
	ok, _ := h.Permission(context.Background(), "demo", model.Coordinator, "mcp__neoharness__whoami", json.RawMessage(`{}`))
	if !ok {
		t.Fatal("the hub's own tools must be allowed without asking")
	}
	if v := view(t, h, "demo", model.Coordinator); v.Permission != nil {
		t.Fatal("no permission request should be left pending")
	}
}
