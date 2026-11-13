package hub

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
	"github.com/MahammadAgayev/neoharness/hub/agent"
	"github.com/MahammadAgayev/neoharness/hub/workspace"
)

// fake is a scripted agent. Each turn calls script, which may block, emit
// events, or call back into the hub the way an MCP tool call would.
type fake struct {
	mu     sync.Mutex
	reqs   []agent.Request
	script func(ctx context.Context, req agent.Request) agent.Result
}

func (f *fake) Name() string { return "fake" }

func (f *fake) Efforts() []string { return []string{"low", "high"} }

func (f *fake) Models(context.Context) []agent.Model {
	return []agent.Model{{Name: "big", Label: "Big", Version: "model-a-1"}, {Name: "small"}}
}

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
	h, err := New(Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }, MCPPort: 4242})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

func pinned(t *testing.T, h *Hub, name string) {
	t.Helper()
	if err := h.CreateWorkspace(name, t.TempDir()); err != nil {
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

func TestWorkspaceGetsACoordinator(t *testing.T) {
	h := newHub(t, &fake{})
	pinned(t, h, "demo")
	vs, _ := h.Threads("demo")
	if len(vs) != 1 || vs[0].Name != model.Coordinator || vs[0].Role != model.RoleCoordinator {
		t.Fatalf("threads = %+v", vs)
	}
	if err := h.CreateWorkspace("demo", ""); err == nil {
		t.Fatal("duplicate workspace should fail")
	}
	// Unpinned workspaces still get a coordinator, standing in their own folder.
	if err := h.CreateWorkspace("loose", ""); err != nil {
		t.Fatal(err)
	}
	if err := h.OpenThread("loose", model.User, "x", "", ""); err == nil {
		t.Fatal("a thread in an unpinned workspace needs a location")
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
	if reqs[0].Harness.URL != "http://127.0.0.1:4242/mcp" || reqs[0].Harness.Token == "" {
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
	if reqs[0].Harness.URL == "" {
		t.Errorf("every turn reaches the harness: %+v", reqs[0])
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

func TestAQueuedMessageJoinsTheTranscriptWhenItsTurnStarts(t *testing.T) {
	gate := make(chan struct{})
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: "reply"})
		<-gate
		return agent.Result{Text: "reply"}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "first")
	stateIs(t, h, "demo", model.Coordinator, model.StateRunning)
	_ = h.Send("demo", model.User, model.Coordinator, "second")
	if v := view(t, h, "demo", model.Coordinator); len(v.Pending) != 1 || v.Pending[0].Text != "second" {
		t.Fatalf("pending = %+v, want the second message", v.Pending)
	}
	gate <- struct{}{}
	gate <- struct{}{}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	evs, _ := h.Events("demo", model.Coordinator, 0)
	var got []string
	for _, e := range evs {
		if e.Kind == model.EventMessage || e.Kind == model.EventText {
			got = append(got, e.Text)
		}
	}
	if want := "first reply second reply"; strings.Join(got, " ") != want {
		t.Errorf("transcript = %q, want %q: a queued message comes after the reply it waited for", strings.Join(got, " "), want)
	}
	if v := view(t, h, "demo", model.Coordinator); len(v.Pending) != 0 {
		t.Errorf("pending after the turn = %+v", v.Pending)
	}
}

// TestCoordinatorTurnsStartWithTheBoard: the coordinator sees every open
// thread at the start of each turn, so it manages without asking for the
// status. A thread that ended a turn since its last turn adds its reply.
func TestCoordinatorTurnsStartWithTheBoard(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	pinned(t, h, "demo")
	for _, n := range []string{"lint", "docs"} {
		if err := h.OpenThread("demo", model.Coordinator, n, "work", ""); err != nil {
			t.Fatal(err)
		}
		stateIs(t, h, "demo", n, model.StateIdle)
	}
	_ = h.ReportProgress("demo", "docs", "Done: usage written")
	_ = h.Send("demo", "lint", model.Coordinator, "lint done")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	_ = h.Send("demo", model.User, model.Coordinator, "status?")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)

	var coord []string
	for _, r := range f.requests() {
		if strings.Contains(r.Prompt, "lint done") || strings.Contains(r.Prompt, "status?") {
			coord = append(coord, r.Prompt)
		}
	}
	if len(coord) != 2 {
		t.Fatalf("%d coordinator turns, want 2", len(coord))
	}
	if want := "Threads:\n- docs: idle, ended a turn. Report: Done: usage written. Reply: ok\n- lint: idle.\n\n---\n\n"; !strings.HasPrefix(coord[0], want) {
		t.Errorf("first turn = %q, want it to start with %q", coord[0], want)
	}
	if want := "Threads:\n- docs: idle. Report: Done: usage written.\n- lint: idle.\n\n---\n\n"; !strings.HasPrefix(coord[1], want) {
		t.Errorf("second turn = %q, want it to start with %q: a reply comes once", coord[1], want)
	}
}

// TestOnlyTheCoordinatorIsHarnessOnly: the coordinator manages, so it gets
// only the harness tools; threads keep the agent's own.
func TestOnlyTheCoordinatorIsHarnessOnly(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "lint", "work", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "lint", model.StateIdle)
	_ = h.Send("demo", model.User, model.Coordinator, "hi")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	var coord, thread int
	for _, r := range f.requests() {
		switch coordinator := strings.Contains(r.SystemPrompt, "coordinator of the workspace"); {
		case coordinator && r.HarnessOnly:
			coord++
		case !coordinator && !r.HarnessOnly:
			thread++
		default:
			t.Errorf("coordinator %v, harness only %v", coordinator, r.HarnessOnly)
		}
	}
	if coord == 0 || thread == 0 {
		t.Fatalf("%d coordinator and %d thread turns", coord, thread)
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
		for _, q := range []agent.Permission{
			{Tool: "Bash", Detail: "go test ./...", Prefix: "go"},
			{Tool: "Bash", Detail: "go vet ./...", Prefix: "go"},
			{Tool: "Bash", Detail: "go test ./... && rm -rf /", Prefix: "go", Compound: true},
		} {
			ok, msg := h.Permission(ctx, "demo", model.Coordinator, q)
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
			ok, _ := h.Permission(context.Background(), "demo", model.Coordinator, agent.Permission{Tool: "Write", Detail: "x"})
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

func TestAnswerStopsATurnThatStillRunsAfterItsQuestion(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		if strings.Contains(req.Prompt, "postgres") {
			return agent.Result{Text: "ok"}
		}
		_ = h.AskUser("demo", model.Coordinator, "which database?")
		req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: "asked"})
		// claude -p waits here for a background task.
		<-ctx.Done()
		return agent.Result{ExitCode: -1}
	}
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "set up storage")
	stateIs(t, h, "demo", model.Coordinator, model.StateWaiting)
	if v := view(t, h, "demo", model.Coordinator); v.Waiting != "which database?" {
		t.Fatalf("waiting = %q", v.Waiting)
	}
	_ = h.Send("demo", model.User, model.Coordinator, "postgres")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if reqs := f.requests(); len(reqs) != 2 || !strings.Contains(reqs[1].Prompt, "postgres") {
		t.Fatalf("the answer should start the next turn at once; %d turns", len(reqs))
	}
}

func TestAnswerAfterTheTurnEndedInterruptsNothing(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	gate := make(chan struct{})
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		if strings.Contains(req.Prompt, "postgres") {
			select {
			case <-gate:
			case <-ctx.Done():
				return agent.Result{ExitCode: -1}
			}
			return agent.Result{Text: "ok"}
		}
		_ = h.AskUser("demo", model.Coordinator, "which database?")
		return agent.Result{Text: "asked"}
	}
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "set up storage")
	stateIs(t, h, "demo", model.Coordinator, model.StateWaiting)
	eventually(t, "the asking turn to end", func() bool {
		evs, _ := h.Events("demo", model.Coordinator, 50)
		return slices.ContainsFunc(evs, func(e model.Event) bool { return e.Kind == model.EventTurnEnd })
	})
	_ = h.Send("demo", model.User, model.Coordinator, "postgres")
	close(gate)
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	evs, _ := h.Events("demo", model.Coordinator, 50)
	for _, e := range evs {
		if e.Kind == model.EventNote && e.Text == "Interrupted." {
			t.Fatal("an answer must not stop the turn it starts")
		}
	}
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
	if err := h.Send("demo", model.Coordinator, "docs", "more"); err == nil {
		t.Fatal("a resolved thread must refuse the coordinator's messages")
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
	factory := func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }
	h, err := New(Config{Factory: factory})
	if err != nil {
		t.Fatal(err)
	}
	_ = h.CreateWorkspace("demo", t.TempDir())
	_ = h.Send("demo", model.User, model.Coordinator, "work")
	eventually(t, "turn to start", func() bool { return len(f.requests()) == 1 })

	// Simulate a crash: the turn is on disk as in flight.
	ths, _ := workspace.Threads("demo")
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

// TestCloseDoesNotWaitForeverOnAStuckTurn: a turn whose agent ignores the
// cancel must not keep the program from quitting.
func TestCloseDoesNotWaitForeverOnAStuckTurn(t *testing.T) {
	defer func(d time.Duration) { closeWait = d }(closeWait)
	closeWait = 100 * time.Millisecond
	block := make(chan struct{})
	defer close(block)
	f := &fake{script: func(ctx context.Context, req agent.Request) agent.Result {
		<-block
		return agent.Result{}
	}}
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	h, err := New(Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }})
	if err != nil {
		t.Fatal(err)
	}
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "work")
	stateIs(t, h, "demo", model.Coordinator, model.StateRunning)
	done := make(chan struct{})
	go func() { h.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close still waits for the stuck turn")
	}
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

func TestCompletionsCommandsFilesAndThreads(t *testing.T) {
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		return agent.Result{Text: "ok", Commands: []string{"simplify", "review"}}
	}
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	lists := 0
	h, err := New(Config{
		Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f },
		Files: func(ctx context.Context, l model.Location, query string) ([]string, bool, error) {
			lists++
			return []string{"main.go", "go.mod"}, false, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.User, "api", "", ""); err != nil {
		t.Fatal(err)
	}

	c, err := h.Completions("demo", model.Coordinator, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Commands) != 0 || strings.Join(c.Threads, " ") != "api" || len(c.Files) != 2 {
		t.Fatalf("before any turn: %+v", c)
	}

	if err := h.Send("demo", model.User, model.Coordinator, "hi"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the turn's commands", func() bool {
		c, _ := h.Completions("demo", model.Coordinator, "")
		return strings.Join(c.Commands, " ") == "simplify review"
	})
	// api has had no turn, but stands in the same folder.
	c, _ = h.Completions("demo", "api", "")
	if strings.Join(c.Commands, " ") != "simplify review" || strings.Join(c.Threads, " ") != model.Coordinator {
		t.Fatalf("api: %+v", c)
	}
	if lists != 1 {
		t.Errorf("listed the folder %d times, want 1 cached", lists)
	}
}

func TestComposeNotesThreadMentions(t *testing.T) {
	threads := map[string]*thread{"api": {}, model.Coordinator: {}}
	got := compose([]workspace.Message{{From: model.User, Text: "compare @api and @coordinator, not @api.go, @nope or a@api"}}, threads)
	if strings.Count(got, "read_thread") != 2 || !strings.Contains(got, "@api is the thread api") || !strings.Contains(got, "@coordinator is the thread") {
		t.Fatalf("prompt = %q", got)
	}
	if strings.Contains(got, "nope is the thread") {
		t.Fatal("an unknown name got a note")
	}
}

func TestReadThreadNeedsATailAndFilters(t *testing.T) {
	h := newHub(t, &fake{})
	pinned(t, h, "demo")
	for _, m := range []string{"first", "the token bug", "third"} {
		if err := h.Send("demo", model.User, model.Coordinator, m); err != nil {
			t.Fatal(err)
		}
	}
	// A queued message joins the transcript when its turn starts.
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if _, err := h.ReadThread("demo", model.Coordinator, 0, ""); err == nil {
		t.Fatal("tail 0 should fail")
	}
	if _, err := h.ReadThread("demo", model.Coordinator, MaxReadTail+1, ""); err == nil {
		t.Fatal("a tail over the limit should fail")
	}
	evs, err := h.ReadThread("demo", model.Coordinator, 5, "TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Text != "the token bug" {
		t.Fatalf("query: %+v", evs)
	}
}

func TestDeleteStopsTheTurnAndRemovesTheThread(t *testing.T) {
	ended := make(chan struct{})
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		<-ctx.Done()
		req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: "late"})
		close(ended)
		return agent.Result{ExitCode: 1}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.User, "api", "go", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "api", model.StateRunning)
	ch, stop := h.Subscribe()
	defer stop()

	if err := h.Delete("demo", model.Coordinator); err == nil {
		t.Fatal("the coordinator must not be deletable")
	}
	if err := h.Delete("demo", "api"); err != nil {
		t.Fatal(err)
	}
	<-ended
	h.Close()
	if _, err := os.Stat(workspace.ThreadDir("demo", "api")); !os.IsNotExist(err) {
		t.Fatalf("the thread folder is still there or came back: %v", err)
	}
	if vs, _ := h.Threads("demo"); len(vs) != 1 {
		t.Fatalf("threads = %+v", vs)
	}
	deleted := false
	for len(ch) > 0 {
		if u := <-ch; u.Kind == model.UpdateDeleted && u.Thread == "api" {
			deleted = true
		}
	}
	if !deleted {
		t.Error("subscribers were not told")
	}
}

func TestStatusUsageAddsCostAndReadsBranch(t *testing.T) {
	f := &fake{}
	turn := 0
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		turn++
		return agent.Result{Text: "ok", Model: "claude-opus-5[1m]", Context: 1000 * turn, ContextWindow: 1000000, Cost: 0.5}
	}
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	h, err := New(Config{
		Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f },
		Branch:  func(context.Context, model.Location) (string, error) { return "main", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	pinned(t, h, "demo")
	for _, m := range []string{"one", "two"} {
		if err := h.Send("demo", model.User, model.Coordinator, m); err != nil {
			t.Fatal(err)
		}
		stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	}
	eventually(t, "both turns and the branch", func() bool {
		v := view(t, h, "demo", model.Coordinator)
		return v.Cost == 1.0 && v.Context == 2000 && v.Branch == "main"
	})
	v := view(t, h, "demo", model.Coordinator)
	if v.Model != "claude-opus-5[1m]" || v.ContextWindow != 1000000 {
		t.Fatalf("view = %+v", v)
	}
}

func TestClearStartsANewSession(t *testing.T) {
	gate := make(chan struct{})
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		<-gate
		req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: "ok"})
		return agent.Result{Text: "ok", Context: 5000, ContextWindow: 100000}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	h.Send("demo", model.User, model.Coordinator, "remember pelican")
	stateIs(t, h, "demo", model.Coordinator, model.StateRunning)
	if err := h.Send("demo", model.User, model.Coordinator, "/clear"); err == nil {
		t.Fatal("/clear during a turn should fail")
	}
	gate <- struct{}{}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)

	if err := h.Send("demo", model.User, model.Coordinator, " /clear "); err != nil {
		t.Fatal(err)
	}
	if v := view(t, h, "demo", model.Coordinator); v.Context != 0 || v.Queued != 0 {
		t.Fatalf("after /clear: %+v", v)
	}
	h.Send("demo", model.User, model.Coordinator, "what word?")
	gate <- struct{}{}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)

	reqs := f.requests()
	if len(reqs) != 2 || reqs[1].Resume || reqs[1].Conversation == reqs[0].Conversation {
		t.Fatalf("the turn after /clear must start a new session: %+v", reqs)
	}
	if strings.Contains(reqs[1].Prompt, "/clear") {
		t.Fatal("/clear reached Claude")
	}
	evs, _ := h.Events("demo", model.Coordinator, 0)
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, string(e.Kind))
	}
	if !strings.Contains(strings.Join(kinds, " "), "turn clear message") {
		t.Fatalf("events = %v", kinds)
	}
}

func TestModelAndEffortLastAcrossTurns(t *testing.T) {
	h := newHub(t, &fake{})
	f := &fake{}
	h.cfg.Factory = func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }
	pinned(t, h, "demo")
	if err := h.Send("demo", model.User, model.Coordinator, "/effort huge"); err == nil {
		t.Fatal("an unknown effort should fail")
	}
	for _, m := range []string{"/model sonnet", "/effort high", "one", "two", "/model default", "three"} {
		if err := h.Send("demo", model.User, model.Coordinator, m); err != nil {
			t.Fatal(err)
		}
		stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	}
	reqs := f.requests()
	if len(reqs) != 3 {
		t.Fatalf("%d turns, want 3: the commands are not turns", len(reqs))
	}
	for i, want := range []string{"sonnet", "sonnet", ""} {
		if reqs[i].Model != want || reqs[i].Effort != "high" {
			t.Errorf("turn %d: model %q effort %q", i, reqs[i].Model, reqs[i].Effort)
		}
	}
}

func TestBareModelAndEffortSayTheChoiceInsteadOfGoingToTheAgent(t *testing.T) {
	h := newHub(t, &fake{})
	f := &fake{}
	h.cfg.Factory = func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }
	pinned(t, h, "demo")
	notes := func() []string {
		evs, _ := h.Events("demo", model.Coordinator, 50)
		var out []string
		for _, e := range evs {
			if e.Kind == model.EventNote {
				out = append(out, e.Text)
			}
		}
		return out
	}
	for _, m := range []string{"/model", "/effort"} {
		n := len(notes())
		if err := h.Send("demo", model.User, model.Coordinator, m); err != nil {
			t.Fatal(err)
		}
		eventually(t, m+" note", func() bool { return len(notes()) == n+1 })
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d turns, want 0: a bare /model is not a message to the agent", n)
	}
	got := notes()
	if !strings.Contains(got[0], "/model <name>") || !strings.Contains(got[0], "big (model-a-1), small") || !strings.Contains(got[1], "next turn: low, high") {
		t.Fatalf("notes = %q", got)
	}
}

func TestChoicesListTheAgentsModelsAndWhatTheThreadHas(t *testing.T) {
	h := newHub(t, &fake{})
	f := &fake{}
	h.cfg.Factory = func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }
	pinned(t, h, "demo")
	if err := h.Send("demo", model.User, model.Coordinator, "/model big"); err != nil {
		t.Fatal(err)
	}
	c, err := h.Choices("demo", model.Coordinator)
	if err != nil {
		t.Fatal(err)
	}
	if c.Model != "big" || len(c.Models) != 2 || c.Models[0].Version != "model-a-1" || len(c.Efforts) != 2 {
		t.Fatalf("choices = %+v", c)
	}
}

func TestCompactStartsANewSessionFromASummary(t *testing.T) {
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		text := "ok"
		if strings.Contains(req.Prompt, "Reply with only the summary") {
			text = "SUMMARY: pelican"
		}
		req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: text})
		return agent.Result{Text: text}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.Send("demo", model.User, model.Coordinator, "/compact"); err == nil {
		t.Fatal("/compact before any turn should fail")
	}
	for _, m := range []string{"remember pelican", "/compact the word", "what word?", "and now?"} {
		if err := h.Send("demo", model.User, model.Coordinator, m); err != nil {
			t.Fatal(err)
		}
		stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	}
	r := f.requests()
	if len(r) != 4 {
		t.Fatalf("%d turns", len(r))
	}
	if !strings.Contains(r[1].Prompt, "Focus on: the word") || !r[1].Resume || r[1].Conversation != r[0].Conversation {
		t.Errorf("the summary turn runs in the old session: %+v", r[1])
	}
	if r[2].Resume || r[2].Conversation == r[0].Conversation || !strings.HasPrefix(r[2].Prompt, "This conversation continues") ||
		!strings.Contains(r[2].Prompt, "SUMMARY: pelican") || !strings.HasSuffix(r[2].Prompt, "what word?") {
		t.Errorf("the turn after /compact: %+v", r[2])
	}
	if !r[3].Resume || strings.Contains(r[3].Prompt, "SUMMARY") {
		t.Errorf("the summary goes only into the first message: %+v", r[3])
	}
}

func TestSetFolderMovesTheCoordinator(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	pinned(t, h, "demo")
	h.Send("demo", model.User, model.Coordinator, "one")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)

	if err := h.SetFolder("demo", "/no/such/folder"); err == nil {
		t.Fatal("a missing folder should fail")
	}
	dir := t.TempDir()
	if err := h.SetFolder("demo", dir); err != nil {
		t.Fatal(err)
	}
	if ws := h.Workspaces(); ws[0].Pin != dir {
		t.Fatalf("pin = %q", ws[0].Pin)
	}
	h.Send("demo", model.User, model.Coordinator, "two")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	r := f.requests()
	if r[1].Cwd != dir || !r[1].Resume || r[1].Conversation != r[0].Conversation {
		t.Fatalf("the coordinator should run in the new folder and keep its session: %+v", r[1])
	}
	if err := h.OpenThread("demo", model.User, "api", "", ""); err != nil {
		t.Fatal(err)
	}
	if v := view(t, h, "demo", "api"); v.Location.Dir != dir {
		t.Fatalf("a new thread opened in %q", v.Location.Dir)
	}
}

func TestDeleteWorkspaceHidesItAndStopsTurns(t *testing.T) {
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		<-ctx.Done()
		return agent.Result{ExitCode: 1}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	h.Send("demo", model.User, model.Coordinator, "work")
	stateIs(t, h, "demo", model.Coordinator, model.StateRunning)
	if err := h.DeleteWorkspace("demo"); err != nil {
		t.Fatal(err)
	}
	if len(h.Workspaces()) != 0 {
		t.Fatal("a deleted workspace is still listed")
	}
	h.Close()
	ws, _ := workspace.List()
	if len(ws) != 1 || !ws[0].Deleted {
		t.Fatalf("the data should stay, marked deleted: %+v", ws)
	}
	h2, err := New(Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }})
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()
	if len(h2.Workspaces()) != 0 {
		t.Fatal("a restart brought the deleted workspace back")
	}
	if err := h2.CreateWorkspace("demo", ""); err == nil || !strings.Contains(err.Error(), "deleted") {
		t.Fatalf("reusing the name: %v", err)
	}
}

func TestSendNowInterruptsTheTurn(t *testing.T) {
	for _, c := range []struct{ started, queued bool }{{true, false}, {false, false}, {true, true}} {
		started, queued := c.started, c.queued
		t.Run(fmt.Sprintf("started=%v,queued=%v", started, queued), func(t *testing.T) {
			var err error
			f := &fake{}
			f.script = func(ctx context.Context, req agent.Request) agent.Result {
				if strings.Contains(req.Prompt, "now") {
					return agent.Result{Text: "ok"}
				}
				if started {
					req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: "working"})
				}
				<-ctx.Done()
				return agent.Result{ExitCode: -1}
			}
			h := newHub(t, f)
			pinned(t, h, "demo")
			if err := h.Send("demo", model.User, model.Coordinator, "long task"); err != nil {
				t.Fatal(err)
			}
			stateIs(t, h, "demo", model.Coordinator, model.StateRunning)
			if queued {
				if err := h.Send("demo", model.User, model.Coordinator, "stop, do this now"); err != nil {
					t.Fatal(err)
				}
				err = h.SendNow("demo", model.Coordinator, "")
			} else {
				err = h.SendNow("demo", model.Coordinator, "stop, do this now")
			}
			if err != nil {
				t.Fatal(err)
			}
			stateIs(t, h, "demo", model.Coordinator, model.StateIdle)

			reqs := f.requests()
			if len(reqs) != 2 {
				t.Fatalf("%d turns, want 2", len(reqs))
			}
			p := reqs[1].Prompt
			if !strings.Contains(p, "stop, do this now") {
				t.Fatalf("second prompt = %q", p)
			}
			if strings.Contains(p, "long task") != !started {
				t.Errorf("second prompt = %q; the stopped message goes again only if Claude never saw it", p)
			}
			evs, _ := h.Events("demo", model.Coordinator, 50)
			var kinds []string
			for _, e := range evs {
				if e.Kind == model.EventError {
					t.Errorf("an interrupt is not an error: %q", e.Text)
				}
				if e.Kind == model.EventNote {
					kinds = append(kinds, e.Text)
				}
			}
			if !slices.Contains(kinds, "Interrupted.") {
				t.Errorf("notes = %q", kinds)
			}
		})
	}
}

func TestShellCommandRunsWithoutATurnAndReachesTheNextPrompt(t *testing.T) {
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	f := &fake{}
	var ran []string
	var mu sync.Mutex
	h, err := New(Config{
		Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f },
		MCPPort: 4242,
		Shell: func(ctx context.Context, l model.Location, command string) (string, int, error) {
			mu.Lock()
			ran = append(ran, l.Dir+" $ "+command)
			mu.Unlock()
			return "no such file\n", 2, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	dir := t.TempDir()
	if err := h.CreateWorkspace("demo", dir); err != nil {
		t.Fatal(err)
	}
	if err := h.Send("demo", model.User, model.Coordinator, "!"); err == nil {
		t.Error("! alone should ask for a command")
	}
	if err := h.Send("demo", model.User, model.Coordinator, "!ls nope"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the command to end", func() bool {
		evs, _ := h.Events("demo", model.Coordinator, 10)
		return len(evs) > 0 && evs[len(evs)-1].Kind == model.EventShell && evs[len(evs)-1].Text != ""
	})
	mu.Lock()
	if len(ran) != 1 || ran[0] != dir+" $ ls nope" {
		t.Fatalf("ran = %q", ran)
	}
	mu.Unlock()
	if n := len(f.requests()); n != 0 {
		t.Fatalf("! started %d turns", n)
	}
	evs, _ := h.Events("demo", model.Coordinator, 10)
	last := evs[len(evs)-1]
	if last.Exit != 2 || last.Detail != "ls nope" || evs[len(evs)-2].ID != last.ID {
		t.Fatalf("events = %+v", evs)
	}

	if err := h.Send("demo", model.User, model.Coordinator, "why did it fail?"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	_ = h.Send("demo", model.User, model.Coordinator, "and now?")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	reqs := f.requests()
	want := "<bash-input>ls nope</bash-input>\n<bash-stdout>no such file\n</bash-stdout>\n<bash-exit-code>2</bash-exit-code>\n\n---\n\nwhy did it fail?"
	if len(reqs) != 2 || reqs[0].Prompt != want {
		t.Fatalf("first prompt = %q", reqs[0].Prompt)
	}
	if strings.Contains(reqs[1].Prompt, "bash-input") {
		t.Error("the command went to Claude twice")
	}
}

func TestWriteArtifactVersionsAndLimits(t *testing.T) {
	h := newHub(t, &fake{})
	pinned(t, h, "demo")
	ch, stop := h.Subscribe()
	defer stop()
	a, err := h.WriteArtifact("demo", model.Coordinator, "plan", "intro\n\n## The plan\n")
	if err != nil || a.Version != 1 || a.Title != "The plan" {
		t.Fatalf("first write = %+v, %v", a, err)
	}
	if a, _ = h.WriteArtifact("demo", model.Coordinator, "plan", "no heading"); a.Version != 2 || a.Title != "plan" {
		t.Fatalf("second write = %+v", a)
	}
	for _, bad := range []struct{ name, text string }{
		{"../x", "# x"}, {"plan", "  "}, {"big", strings.Repeat("x", artifactLimit+1)},
	} {
		if _, err := h.WriteArtifact("demo", model.Coordinator, bad.name, bad.text); err == nil {
			t.Errorf("WriteArtifact(%q, %d bytes) should fail", bad.name, len(bad.text))
		}
	}
	if _, _, err := h.ReadArtifact("demo", "nope"); err == nil {
		t.Error("a missing artifact should be an error")
	}
	as, _ := h.Artifacts("demo")
	if len(as) != 1 || as[0].Name != "plan" {
		t.Fatalf("artifacts = %+v", as)
	}
	evs, _ := h.Events("demo", model.Coordinator, 10)
	if e := evs[len(evs)-1]; e.Kind != model.EventArtifact || e.Detail != "plan" || e.Version != 2 {
		t.Errorf("last event = %+v", e)
	}
	seen := false
	for len(ch) > 0 {
		if u := <-ch; u.Kind == model.UpdateArtifact && u.Artifact.Version == 2 {
			seen = true
		}
	}
	if !seen {
		t.Error("subscribers were not told")
	}
}

func TestPersonEditsReachTheWriterAsOneDiff(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	pinned(t, h, "demo")
	doc := "# Plan\n\n## Store\n\none\ntwo\nthree\n\n## Tunnel\n\nssh\n"
	if _, err := h.WriteArtifact("demo", model.Coordinator, "plan", doc); err != nil {
		t.Fatal(err)
	}
	a, _, err := h.EditArtifact("demo", "plan", strings.Replace(doc, "two", "TWO", 1), doc, 1, false)
	if err != nil || a.Version != 2 || !a.UserEdit {
		t.Fatalf("first edit = %+v, %v", a, err)
	}
	if _, _, err := h.EditArtifact("demo", "plan", strings.Replace(doc, "two", "deux", 1), doc, 1, false); err == nil {
		t.Error("an edit of the same line from an old version should be refused")
	}
	if a, _, err = h.EditArtifact("demo", "plan", doc+"more\n", doc, 1, false); err != nil || a.Version != 3 {
		t.Fatalf("second edit = %+v, %v", a, err)
	}
	if len(f.requests()) != 0 {
		t.Fatal("an edit should not start a turn")
	}
	evs, _ := h.Events("demo", model.Coordinator, 10)
	if e := evs[len(evs)-1]; e.Kind != model.EventArtifact || e.From != model.User || e.Version != 3 {
		t.Errorf("the writer's transcript should show the edit, got %+v", e)
	}

	_ = h.Send("demo", model.User, model.Coordinator, "go on")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	_ = h.Send("demo", model.User, model.Coordinator, "and now?")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	reqs := f.requests()
	want := "The person edited the artifact plan (v1 → v3). The change, as a unified diff:\n\n```diff\n" +
		"@@ -5,3 +5,3 @@ ## Store\n one\n-two\n+TWO\n three\n" +
		"@@ -11,2 +11,3 @@ ## Tunnel\n ssh\n+more\n \n```\n\nKeep their changes when you write it again.\n\n---\n\ngo on"
	if len(reqs) != 2 || reqs[0].Prompt != want {
		t.Fatalf("first prompt = %q", reqs[0].Prompt)
	}
	if strings.Contains(reqs[1].Prompt, "edited") {
		t.Error("the edit went to Claude twice")
	}
}

func TestPersonStartsADocTheCoordinatorWorksFrom(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	pinned(t, h, "demo")
	doc := "# Launch\n\n- [ ] write the API\n- [ ] ship it\n"
	a, text, err := h.EditArtifact("demo", "launch", doc, "", 0, false)
	if err != nil || a.Version != 1 || a.Thread != model.Coordinator || !a.UserEdit || text != doc {
		t.Fatalf("start = %+v, %q, %v", a, text, err)
	}
	if _, _, err := h.EditArtifact("demo", "other", doc, "", 3, false); err == nil {
		t.Error("an edit of a missing artifact from a version should be refused")
	}
	if _, _, err := h.EditArtifact("demo", "Bad Name", doc, "", 0, false); err == nil {
		t.Error("a bad name should be refused")
	}
	if len(f.requests()) != 0 {
		t.Fatal("starting a doc should not start a turn")
	}
	_ = h.Send("demo", model.User, model.Coordinator, "go")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	reqs := f.requests()
	if len(reqs) != 1 || !strings.HasPrefix(reqs[0].Prompt, "The person started the artifact launch.") || !strings.Contains(reqs[0].Prompt, "- [ ] ship it") {
		t.Fatalf("prompt = %q", reqs[0].Prompt)
	}
}

func TestWriteOverAnUnseenEditIsRefused(t *testing.T) {
	h := newHub(t, &fake{})
	pinned(t, h, "demo")
	_, _ = h.WriteArtifact("demo", model.Coordinator, "plan", "# Plan\n\nold\n")
	_, _, _ = h.EditArtifact("demo", "plan", "# Plan\n\nmine\n", "# Plan\n\nold\n", 1, false)
	_, err := h.WriteArtifact("demo", model.Coordinator, "plan", "# Plan\n\nnew\n")
	if err == nil || !strings.Contains(err.Error(), "+mine") || !strings.Contains(err.Error(), "Nothing was saved") {
		t.Fatalf("err = %v", err)
	}
	if _, text, _ := h.ReadArtifact("demo", "plan"); text != "# Plan\n\nmine\n" {
		t.Fatalf("the person's edit was lost: %q", text)
	}
	if a, err := h.WriteArtifact("demo", model.Coordinator, "plan", "# Plan\n\nmine and new\n"); err != nil || a.Version != 3 || a.UserEdit {
		t.Fatalf("a write after the refusal = %+v, %v", a, err)
	}
}

func TestLargeEditsAreToldAsCounts(t *testing.T) {
	old := "# Plan\n\n## Store\n\n" + strings.Repeat("a line\n", 10)
	now := "# Plan\n\n## Store\n\n" + strings.Repeat("a much longer line that changed\n", 400)
	note := editNote("plan", workspace.Edit{Version: 1, Text: old}, now, 2)
	if strings.Contains(note, "```") || !strings.Contains(note, "400 lines added, 10 removed") || !strings.Contains(note, "## Store") {
		t.Fatalf("note = %q", note)
	}
}

func TestWritersReadSeesTheEdit(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	pinned(t, h, "demo")
	_, _ = h.WriteArtifact("demo", model.Coordinator, "plan", "# Plan\n\nold\n")
	_, _, _ = h.EditArtifact("demo", "plan", "# Plan\n\nmine\n", "# Plan\n\nold\n", 1, false)
	if text, _ := h.ReadArtifactAs("demo", "nobody", "plan"); text != "# Plan\n\nmine\n" {
		t.Fatalf("another reader should get the text alone: %q", text)
	}
	text, err := h.ReadArtifactAs("demo", model.Coordinator, "plan")
	if err != nil || !strings.HasPrefix(text, "The person edited the artifact plan (v1 → v2)") || !strings.HasSuffix(text, "---\n\n# Plan\n\nmine\n") {
		t.Fatalf("the writer's read = %q, %v", text, err)
	}
	if a, err := h.WriteArtifact("demo", model.Coordinator, "plan", "# Plan\n\nmine, and more\n"); err != nil || a.Version != 3 {
		t.Fatalf("a write after the read should go through: %+v, %v", a, err)
	}
	_ = h.Send("demo", model.User, model.Coordinator, "go on")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if reqs := f.requests(); len(reqs) != 1 || reqs[0].Prompt != "go on" {
		t.Fatalf("the edit was already seen; prompt = %q", reqs[0].Prompt)
	}
}

func TestMerge3(t *testing.T) {
	base := "# P\n\n## A\none\n\n## B\ntwo\n"
	for _, c := range []struct {
		mine, theirs, want string
		ok                 bool
	}{
		{"# P\n\n## A\nONE\n\n## B\ntwo\n", "# P\n\n## A\none\n\n## B\nTWO\n", "# P\n\n## A\nONE\n\n## B\nTWO\n", true},
		{"# P\n\n## A\nONE\n\n## B\ntwo\n", "# P\n\n## Aye\none\n\n## B\ntwo\n", "# P\n\n## Aye\nONE\n\n## B\ntwo\n", true},
		{"# P\n\n## A\nONE\n\n## B\ntwo\n", "# P\n\n## A\nUno\n\n## B\ntwo\n", "", false},
		{"# P\n\n## A\nONE\n\n## B\ntwo\n", "# P\n\n## A\nONE\n\n## B\ntwo\n", "# P\n\n## A\nONE\n\n## B\ntwo\n", true},
		{base + "mine\n", base + "theirs\n", "", false},
		{"# P\n\n## A\none\nmine\n\n## B\ntwo\n", base + "\n## C\nthree\n", "# P\n\n## A\none\nmine\n\n## B\ntwo\n\n## C\nthree\n", true},
	} {
		got, ok := merge3(base, c.mine, c.theirs)
		if ok != c.ok || got != c.want {
			t.Errorf("merge3(%q, %q) = %q, %v; want %q, %v", c.mine, c.theirs, got, ok, c.want, c.ok)
		}
	}
}

func TestFailedToolCallIsRecordedOnItsID(t *testing.T) {
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		req.OnEvent(agent.Event{Kind: agent.KindToolStart, Tool: "Bash", Detail: "git push", ID: "tu1"})
		req.OnEvent(agent.Event{Kind: agent.KindToolFailed, ID: "tu1", Text: "denied"})
		return agent.Result{Text: "done", Tools: 1}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.Send("demo", model.User, model.Coordinator, "push"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	evs, _ := h.Events("demo", model.Coordinator, 0)
	var tools []model.Event
	for _, e := range evs {
		if e.Kind == model.EventTool {
			tools = append(tools, e)
		}
	}
	if len(tools) != 2 || tools[0].ID != "tu1" || tools[1].ID != "tu1" || tools[1].Exit != 1 || tools[1].Text != "denied" {
		t.Fatalf("tool events = %+v", tools)
	}
}

func TestContextFollowsTheTurn(t *testing.T) {
	gate, seen := make(chan struct{}), make(chan struct{})
	f := &fake{}
	turns := 0
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		turns++
		// The first turn tells the window; only then does the context
		// follow a turn.
		req.OnEvent(agent.Event{Kind: agent.KindToolStart, Tool: "Read", Tokens: 5000})
		if turns == 1 {
			return agent.Result{Text: "done", Context: 1000, ContextWindow: 200000}
		}
		seen <- struct{}{}
		<-gate
		return agent.Result{Text: "done", Context: 6000, ContextWindow: 200000}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.Send("demo", model.User, model.Coordinator, "go"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if err := h.Send("demo", model.User, model.Coordinator, "again"); err != nil {
		t.Fatal(err)
	}
	<-seen
	coordinator := func() model.ThreadView {
		vs, _ := h.Threads("demo")
		for _, v := range vs {
			if v.Name == model.Coordinator {
				return v
			}
		}
		return model.ThreadView{}
	}
	if c := coordinator().Context; c != 5000 {
		t.Fatalf("context during the turn = %d, want 5000", c)
	}
	gate <- struct{}{}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if c := coordinator().Context; c != 6000 {
		t.Fatalf("context after the turn = %d, want 6000", c)
	}
}

func TestRestoreWorkspaceBringsItsThreadsBack(t *testing.T) {
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result { return agent.Result{Text: "ok"} }
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.Send("demo", model.User, model.Coordinator, "remember this"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if err := h.DeleteWorkspace("demo"); err != nil {
		t.Fatal(err)
	}
	if names, _ := h.DeletedWorkspaces(); strings.Join(names, ",") != "demo" {
		t.Fatalf("deleted = %q", names)
	}
	if err := h.RestoreWorkspace("nope"); err == nil {
		t.Fatal("restoring a name that was never deleted should fail")
	}
	if err := h.RestoreWorkspace("demo"); err != nil {
		t.Fatal(err)
	}
	if len(h.Workspaces()) != 1 {
		t.Fatal("the restored workspace should be listed")
	}
	evs, err := h.Events("demo", model.Coordinator, 0)
	if err != nil || len(evs) == 0 || evs[0].Text != "remember this" {
		t.Fatalf("the transcript should come back: %v %+v", err, evs)
	}
	if err := h.RestoreWorkspace("demo"); err == nil {
		t.Fatal("restoring a live workspace should fail")
	}
	if names, _ := h.DeletedWorkspaces(); len(names) != 0 {
		t.Fatalf("nothing is deleted now, got %q", names)
	}
}

func TestALostConversationStartsANewOne(t *testing.T) {
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		if req.Resume {
			return agent.Result{ExitCode: 1, Lost: true}
		}
		req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: "ok"})
		return agent.Result{Text: "ok"}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	for _, msg := range []string{"first", "second"} {
		if err := h.Send("demo", model.User, model.Coordinator, msg); err != nil {
			t.Fatal(err)
		}
		stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	}
	reqs := f.requests()
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want 3", len(reqs))
	}
	last := reqs[2]
	if last.Resume || last.Conversation == reqs[0].Conversation || !strings.Contains(last.Prompt, "second") {
		t.Fatalf("the message should go again in a new conversation: %+v", last)
	}
}

func TestConfigModelUnlessTheThreadChoseOne(t *testing.T) {
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	f := &fake{}
	h, err := New(Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }, MCPPort: 4242,
		Agents: map[string]Defaults{"big": {Model: "opus"}, "small": {Model: "haiku"}}, Coordinator: "big", Threads: "small"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	pinned(t, h, "demo")
	for _, msg := range []string{"one", "/model sonnet", "two", "/model default", "three"} {
		if err := h.Send("demo", model.User, model.Coordinator, msg); err != nil {
			t.Fatal(err)
		}
		stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	}
	var got []string
	for _, r := range f.requests() {
		got = append(got, r.Model)
	}
	if !slices.Equal(got, []string{"opus", "sonnet", "opus"}) {
		t.Fatalf("models = %q", got)
	}
	if err := h.OpenThread("demo", model.Coordinator, "lint", "work", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "lint", model.StateIdle)
	if reqs := f.requests(); reqs[len(reqs)-1].Model != "haiku" {
		t.Fatalf("thread model = %q, want the threads' setting", reqs[len(reqs)-1].Model)
	}
}

// TestThreadsRunTheCoordinatorsAgentUnlessSet: an agent for the threads is
// optional, in the config and in a workspace.
func TestThreadsRunTheCoordinatorsAgentUnlessSet(t *testing.T) {
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	var names []string
	var mu sync.Mutex
	f := &fake{}
	h, err := New(Config{MCPPort: 4242, Coordinator: "big",
		Agents: map[string]Defaults{"big": {Model: "opus"}, "small": {Model: "haiku"}},
		Factory: func(name string, _ workspace.Workspace, _ workspace.Thread) agent.Agent {
			mu.Lock()
			defer mu.Unlock()
			names = append(names, name)
			return f
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	pinned(t, h, "demo")
	last := func() string {
		mu.Lock()
		defer mu.Unlock()
		return names[len(names)-1]
	}
	if err := h.OpenThread("demo", model.Coordinator, "lint", "work", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "lint", model.StateIdle)
	if got := last(); got != "big" {
		t.Fatalf("thread ran %q, want the coordinator's agent", got)
	}

	// The workspace's coordinator agent reaches its threads too.
	if err := h.SetLocal("demo", "coordinator", "small"); err != nil {
		t.Fatal(err)
	}
	_ = h.Send("demo", model.User, "lint", "again")
	stateIs(t, h, "demo", "lint", model.StateIdle)
	if got := last(); got != "small" {
		t.Fatalf("thread ran %q after :setlocal coordinator=small", got)
	}
	if err := h.SetLocal("demo", "threads", "big"); err != nil {
		t.Fatal(err)
	}
	_ = h.Send("demo", model.User, model.Coordinator, "hi")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if got := last(); got != "small" {
		t.Fatalf("coordinator ran %q, want small", got)
	}
	_ = h.Send("demo", model.User, "lint", "more")
	stateIs(t, h, "demo", "lint", model.StateIdle)
	if got := last(); got != "big" {
		t.Fatalf("thread ran %q after :setlocal threads=big", got)
	}
	if reqs := f.requests(); reqs[len(reqs)-1].Model != "opus" {
		t.Fatalf("model %q, want big's", reqs[len(reqs)-1].Model)
	}

	if err := h.SetLocal("demo", "threads", "nope"); err == nil {
		t.Error("an agent not in the config should be refused")
	}
	if err := h.SetLocal("demo", "coordinator", ""); err != nil {
		t.Fatal(err)
	}
	if v := h.Workspaces()[0]; v.CoordinatorAgent != "big" || v.ThreadsAgent != "big" || !slices.Equal(v.AgentNames, []string{"big", "small"}) {
		t.Fatalf("view = %+v", v)
	}
}

func TestContextWindowSettingCapsTheModelsWindow(t *testing.T) {
	for _, tc := range []struct{ model, setting, want int }{
		{1000000, 400000, 400000},
		{200000, 400000, 200000},
		{1000000, 0, 1000000},
	} {
		t.Setenv("NEOHARNESS_HOME", t.TempDir())
		f := &fake{script: func(ctx context.Context, req agent.Request) agent.Result {
			return agent.Result{Text: "ok", Context: 1000, ContextWindow: tc.model}
		}}
		h, err := New(Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }, MCPPort: 4242,
			Agents: map[string]Defaults{"claude": {ContextWindow: tc.setting}}, Coordinator: "claude"})
		if err != nil {
			t.Fatal(err)
		}
		pinned(t, h, "demo")
		_ = h.Send("demo", model.User, model.Coordinator, "hi")
		stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
		if got := view(t, h, "demo", model.Coordinator).ContextWindow; got != tc.want {
			t.Errorf("model %d, setting %d: window %d, want %d", tc.model, tc.setting, got, tc.want)
		}
		h.Close()
	}
}

func TestRestoreSaysATurnWasStoppedByTheDelete(t *testing.T) {
	seen := make(chan struct{}, 1)
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		seen <- struct{}{}
		<-ctx.Done()
		return agent.Result{ExitCode: -1}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.Send("demo", model.User, model.Coordinator, "go"); err != nil {
		t.Fatal(err)
	}
	<-seen
	if err := h.DeleteWorkspace("demo"); err != nil {
		t.Fatal(err)
	}
	if err := h.RestoreWorkspace("demo"); err != nil {
		t.Fatal(err)
	}
	evs, _ := h.Events("demo", model.Coordinator, 0)
	last := evs[len(evs)-1]
	if last.Kind != model.EventError || !strings.Contains(last.Text, "workspace was deleted") {
		t.Fatalf("last event = %+v", last)
	}
}

func TestTheSummarySaysTheCoordinatorsState(t *testing.T) {
	h := newHub(t, &fake{})
	pinned(t, h, "demo")
	if err := h.AskUser("demo", model.Coordinator, "Which phase first?"); err != nil {
		t.Fatal(err)
	}
	for _, s := range h.Workspaces() {
		if s.Name == "demo" && (s.Coordinator != model.StateWaiting || s.Counts[model.StateWaiting] != 0) {
			t.Fatalf("summary = %+v; the coordinator waits, and it is not a thread", s)
		}
	}
}

func TestCompletionsSearchAFolderWithManyFiles(t *testing.T) {
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	var queries []string
	h, err := New(Config{
		Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return &fake{} },
		Files: func(ctx context.Context, l model.Location, query string) ([]string, bool, error) {
			queries = append(queries, query)
			if query == "" {
				return nil, true, nil
			}
			return []string{"internal/tui/mouse.go"}, false, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	pinned(t, h, "demo")
	c, _ := h.Completions("demo", model.Coordinator, "")
	if !c.Many || len(c.Files) != 0 {
		t.Fatalf("with no query: %+v", c)
	}
	c, _ = h.Completions("demo", model.Coordinator, "mouse")
	if !c.Many || c.Query != "mouse" || strings.Join(c.Files, " ") != "internal/tui/mouse.go" {
		t.Fatalf("with a query: %+v", c)
	}
	if strings.Join(queries, ",") != ",mouse" {
		t.Errorf("queries = %q; the list is asked once, then each search", queries)
	}
}

func TestAnOldReportDoesNotShowWhileANewTurnRuns(t *testing.T) {
	f := &fake{}
	gate := make(chan struct{})
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		<-gate
		return agent.Result{Text: "ok"}
	}
	h := newHub(t, f)
	t.Cleanup(func() { close(gate) }) // a failed check must not leave the turn hanging
	pinned(t, h, "demo")
	progress := func() string { return view(t, h, "demo", model.Coordinator).Progress }

	_ = h.Send("demo", model.User, model.Coordinator, "find the tracker")
	stateIs(t, h, "demo", model.Coordinator, model.StateRunning)
	_ = h.ReportProgress("demo", model.Coordinator, "Blocked: asking which file")
	if p := progress(); p != "Blocked: asking which file" {
		t.Fatalf("a report of the running turn shows; got %q", p)
	}
	gate <- struct{}{}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if p := progress(); p != "Blocked: asking which file" {
		t.Fatalf("an idle thread shows its last report; got %q", p)
	}

	_ = h.Send("demo", model.User, model.Coordinator, "the second one")
	stateIs(t, h, "demo", model.Coordinator, model.StateRunning)
	if p := progress(); p != "" {
		t.Fatalf("a new turn runs; the old report shows %q", p)
	}
}

func TestUnqueueAndStop(t *testing.T) {
	f := &fake{}
	gate := make(chan struct{})
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		select {
		case <-gate:
			return agent.Result{Text: "ok"}
		case <-ctx.Done():
			return agent.Result{ExitCode: -1}
		}
	}
	h := newHub(t, f)
	t.Cleanup(func() { close(gate) })
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "long task")
	stateIs(t, h, "demo", model.Coordinator, model.StateRunning)
	_ = h.Send("demo", model.User, model.Coordinator, "never mind")
	pending := view(t, h, "demo", model.Coordinator).Pending
	if len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	if err := h.Unqueue("demo", model.Coordinator, pending[0].At); err != nil {
		t.Fatal(err)
	}
	if v := view(t, h, "demo", model.Coordinator); len(v.Pending) != 0 || v.Queued != 0 {
		t.Fatalf("after unqueue: pending %+v, queued %d", v.Pending, v.Queued)
	}

	if err := h.Stop("demo", model.Coordinator); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if n := len(f.requests()); n != 1 {
		t.Fatalf("%d turns; a stopped turn's message must not go again", n)
	}
	if err := h.Stop("demo", model.Coordinator); err == nil {
		t.Error("Stop with nothing running should say so")
	}
}

// A stop means stop: what you queued behind the turn waits for you.
func TestStopHoldsTheQueue(t *testing.T) {
	f := &fake{}
	gate := make(chan struct{})
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		select {
		case <-gate:
			return agent.Result{Text: "ok"}
		case <-ctx.Done():
			return agent.Result{ExitCode: -1}
		}
	}
	h := newHub(t, f)
	t.Cleanup(func() { close(gate) })
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "long task")
	stateIs(t, h, "demo", model.Coordinator, model.StateRunning)
	_ = h.Send("demo", model.User, model.Coordinator, "next one")
	if err := h.Stop("demo", model.Coordinator); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateWaiting)
	if v := view(t, h, "demo", model.Coordinator); v.Waiting != "paused" || len(v.Pending) != 1 {
		t.Fatalf("after stop: waiting %q, pending %+v", v.Waiting, v.Pending)
	}
	if n := len(f.requests()); n != 1 {
		t.Fatalf("%d turns; the queued message must wait", n)
	}

	// A thread's message does not start it again; <CR> on the queued one does.
	_ = h.Send("demo", "docs", model.Coordinator, "fyi")
	stateIs(t, h, "demo", model.Coordinator, model.StateWaiting)
	if err := h.SendNow("demo", model.Coordinator, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the queued message to go", func() bool { return len(f.requests()) == 2 })
	if p := f.requests()[1].Prompt; !strings.Contains(p, "next one") || !strings.Contains(p, "fyi") {
		t.Fatalf("prompt = %q", p)
	}
}

// Taking back the last queued message leaves nothing to wait for.
func TestUnqueueAfterStopEndsThePause(t *testing.T) {
	f := &fake{}
	gate := make(chan struct{})
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		select {
		case <-gate:
			return agent.Result{Text: "ok"}
		case <-ctx.Done():
			return agent.Result{ExitCode: -1}
		}
	}
	h := newHub(t, f)
	t.Cleanup(func() { close(gate) })
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "long task")
	stateIs(t, h, "demo", model.Coordinator, model.StateRunning)
	_ = h.Send("demo", model.User, model.Coordinator, "next one")
	_ = h.Stop("demo", model.Coordinator)
	stateIs(t, h, "demo", model.Coordinator, model.StateWaiting)
	if err := h.Unqueue("demo", model.Coordinator, view(t, h, "demo", model.Coordinator).Pending[0].At); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
}

func TestWakeMeStartsATurn(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.WakeMe("demo", model.Coordinator, time.Hour, "first"); err != nil {
		t.Fatal(err)
	}
	if err := h.WakeMe("demo", model.Coordinator, 0, ""); err != nil {
		t.Fatal(err)
	}
	if v := view(t, h, "demo", model.Coordinator); !v.WakeAt.IsZero() {
		t.Fatalf("0 should cancel the wake-up; WakeAt %v", v.WakeAt)
	}
	if err := h.WakeMe("demo", model.Coordinator, 2*MaxWake, ""); err == nil {
		t.Error("a wake-up past MaxWake should fail")
	}

	_ = h.WakeMe("demo", model.Coordinator, time.Hour, "old note")
	_ = h.WakeMe("demo", model.Coordinator, 50*time.Millisecond, "check the benchmark")
	if v := view(t, h, "demo", model.Coordinator); v.WakeAt.IsZero() {
		t.Fatal("the view should say when the thread wakes")
	}
	eventually(t, "the wake-up turn", func() bool { return len(f.requests()) == 1 })
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	p := f.requests()[0].Prompt
	if !strings.Contains(p, "Message from neoharness:\nYou asked to be woken now: check the benchmark") || strings.Contains(p, "old note") {
		t.Fatalf("wake-up prompt = %q", p)
	}
	if v := view(t, h, "demo", model.Coordinator); !v.WakeAt.IsZero() {
		t.Error("a wake-up that came should be gone")
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(f.requests()); n != 1 {
		t.Errorf("%d turns; the replaced wake-up must not fire", n)
	}
}

func TestAWakeUpOutlivesARestart(t *testing.T) {
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	f := &fake{}
	cfg := Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }}
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pinned(t, h, "demo")
	_ = h.WakeMe("demo", model.Coordinator, 100*time.Millisecond, "after the restart")
	h.Close()
	time.Sleep(150 * time.Millisecond)

	h2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h2.Close)
	eventually(t, "the wake-up after the restart", func() bool {
		rs := f.requests()
		return len(rs) == 1 && strings.Contains(rs[0].Prompt, "after the restart")
	})
}

// TestThreadTurnsWakeTheCoordinator: with Notify on, threads that end a turn
// close together wake the coordinator once, and those turns do not count
// toward the pause.
func TestThreadTurnsWakeTheCoordinator(t *testing.T) {
	defer func(d time.Duration) { reportDelay = d }(reportDelay)
	reportDelay = 50 * time.Millisecond
	f := &fake{}
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	h, err := New(Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }, Notify: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	pinned(t, h, "demo")
	for _, n := range []string{"tests", "json"} {
		if err := h.OpenThread("demo", model.Coordinator, n, "work", ""); err != nil {
			t.Fatal(err)
		}
	}
	reports := func() []agent.Request {
		var out []agent.Request
		for _, r := range f.requests() {
			if strings.Contains(r.Prompt, "Message from threads") {
				out = append(out, r)
			}
		}
		return out
	}
	eventually(t, "the coordinator to hear of the threads", func() bool { return len(reports()) > 0 })
	time.Sleep(4 * reportDelay)
	rs := reports()
	if len(rs) != 1 || !strings.Contains(rs[0].Prompt, "tests") || !strings.Contains(rs[0].Prompt, "json") {
		t.Fatalf("report prompts = %d, want one about both threads: %+v", len(rs), rs)
	}
	if !strings.Contains(rs[0].SystemPrompt, "notify(") {
		t.Fatal("the coordinator's prompt should tell it to notify")
	}
	h.mu.Lock()
	turns := h.workspaces["demo"].threads[model.Coordinator].t.AgentTurns
	h.mu.Unlock()
	if turns != 0 {
		t.Fatalf("agent turns = %d; reports must not count toward the pause", turns)
	}
}

func TestThreadTurnsDoNotWakeTheCoordinatorWithoutNotify(t *testing.T) {
	defer func(d time.Duration) { reportDelay = d }(reportDelay)
	reportDelay = 10 * time.Millisecond
	f := &fake{}
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "tests", "work", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "tests", model.StateIdle)
	time.Sleep(10 * reportDelay)
	for _, r := range f.requests() {
		if strings.Contains(r.Prompt, "Message from threads") {
			t.Fatal("without Notify a thread's turn must not wake the coordinator")
		}
	}
	if err := h.Notify("demo", model.Coordinator, "done", false); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyTheCoordinatorNotifies(t *testing.T) {
	h := newHub(t, &fake{})
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "tests", "work", ""); err != nil {
		t.Fatal(err)
	}
	if err := h.Notify("demo", "tests", "hi", false); err == nil {
		t.Fatal("a thread must not notify the person")
	}
	if err := h.Notify("demo", model.Coordinator, "tests are done", true); err != nil {
		t.Fatal(err)
	}
	evs, _ := h.Events("demo", model.Coordinator, 1)
	if len(evs) != 1 || evs[0].Kind != model.EventNotice || !evs[0].NeedsYou || evs[0].Text != "tests are done" {
		t.Fatalf("last event = %+v", evs)
	}
}

// TestYourMessageReopensAResolvedThread: writing to a resolved thread means
// you want it back; a thread or the coordinator still has to reopen it.
func TestYourMessageReopensAResolvedThread(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "docs", "work", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "docs", model.StateIdle)
	if err := h.Resolve("demo", model.Coordinator, "docs"); err != nil {
		t.Fatal(err)
	}
	if err := h.Send("demo", model.Coordinator, "docs", "more"); err == nil {
		t.Fatal("the coordinator's message should not reopen it")
	}
	if err := h.Send("demo", model.User, "docs", "one more thing"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "docs", model.StateIdle)
	if reqs := f.requests(); !strings.Contains(reqs[len(reqs)-1].Prompt, "one more thing") {
		t.Fatalf("last prompt = %q, want your message to run", reqs[len(reqs)-1].Prompt)
	}
}

func TestCostAddsUpTurnsByThreadModelDayAndWorkspace(t *testing.T) {
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		switch req.Prompt {
		case "big":
			return agent.Result{Text: "ok", Model: "model-a", Cost: 2}
		case "fails":
			// A failed turn was still paid for.
			return agent.Result{Text: "boom", Model: "model-b", Cost: 0.25, ExitCode: 1}
		}
		return agent.Result{Text: "ok", Model: "model-b", Cost: 0.5}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	pinned(t, h, "other")
	for _, s := range []struct{ ws, prompt string }{{"demo", "big"}, {"demo", "small"}, {"demo", "fails"}, {"other", "small"}} {
		if err := h.Send(s.ws, model.User, model.Coordinator, s.prompt); err != nil {
			t.Fatal(err)
		}
		stateIs(t, h, s.ws, model.Coordinator, model.StateIdle)
	}
	sp, err := h.Cost("demo")
	if err != nil {
		t.Fatal(err)
	}
	if sp.Total != 2.75 || sp.Today != 2.75 || len(sp.Days) != 14 || sp.Days[13].Turns != 3 {
		t.Fatalf("total %v today %v days %+v", sp.Total, sp.Today, sp.Days)
	}
	if len(sp.Threads) != 1 || sp.Threads[0].Name != model.Coordinator || sp.Threads[0].Turns != 3 {
		t.Fatalf("threads = %+v", sp.Threads)
	}
	if len(sp.Models) != 2 || sp.Models[0].Name != "model-a" || sp.Models[1].Cost != 0.75 {
		t.Fatalf("models = %+v", sp.Models)
	}
	if len(sp.Top) != 3 || sp.Top[0].Cost != 2 || sp.Top[0].Workspace != "demo" || len(sp.Workspaces) != 0 {
		t.Fatalf("top = %+v, workspaces = %+v", sp.Top, sp.Workspaces)
	}
	if ws := h.Workspaces(); ws[0].Cost != 2.75 || ws[1].Cost != 0.5 {
		t.Fatalf("workspace costs = %v, %v", ws[0].Cost, ws[1].Cost)
	}
	all, _ := h.Cost("")
	if all.Total != 3.25 || len(all.Workspaces) != 2 || all.Workspaces[0].Name != "demo" || all.Workspaces[1].Cost != 0.5 {
		t.Fatalf("all = %+v", all)
	}
	if _, err := h.Cost("nope"); err == nil {
		t.Fatal("an unknown workspace should fail")
	}
}
