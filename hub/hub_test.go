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
	"github.com/MahammadAgayev/neoharness/hub/prompts"
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
	if !strings.Contains(reqs[0].SystemPrompt, "You coordinate the") {
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
// status. A thread that ended a turn since its last turn adds the start of
// its reply.
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
	_ = h.Send("demo", model.User, model.Coordinator, "lint done")
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
	// Turns that only talked are no news.
	for _, c := range coord {
		if want := "Threads:\n- docs: idle.\n- lint: idle.\n\n---\n\n"; !strings.HasPrefix(c, want) {
			t.Errorf("turn = %q, want it to start with %q", c, want)
		}
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
		switch coordinator := strings.Contains(r.SystemPrompt, "You coordinate the"); {
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
			{Tool: "Bash", Detail: "go test ./...", Scope: "go test"},
			{Tool: "Bash", Detail: "go test ./hub", Scope: "go test"},
			{Tool: "Bash", Detail: "go vet ./...", Scope: "go vet"},
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
	if v.Permission == nil || v.Permission.Tool != "Bash" || v.Permission.Detail != "go test ./..." || v.Permission.Scope != "go test" {
		t.Fatalf("permission = %+v", v.Permission)
	}
	if err := h.Answer("demo", model.Coordinator, v.Permission.ID, model.Always, ""); err != nil {
		t.Fatal(err)
	}
	if r := <-results; !r.ok {
		t.Fatal("allowed call was denied")
	}
	// The rule allows the next `go test` without asking.
	if r := <-results; !r.ok {
		t.Fatal("always-allow rule did not apply")
	}
	// But not another go command: the rule is its scope, not its first word.
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

// TestAQuestionEndsTheTurn: ask does not wait. The turn ends, the thread
// waits for the person, and the question is a card and a wait until they
// answer by writing; their message goes first, with what waited behind it.
func TestAQuestionEndsTheTurn(t *testing.T) {
	h, f := askThenHold(t)
	stateIs(t, h, "demo", model.Coordinator, model.StateWaiting)
	if v := view(t, h, "demo", model.Coordinator); v.Waiting != "which database?" || v.QuestionID == "" {
		t.Fatalf("waiting = %q, question %q", v.Waiting, v.QuestionID)
	}
	if w := h.Waits(); len(w) != 1 || w[0].Kind != model.WaitQuestion || w[0].ID != view(t, h, "demo", model.Coordinator).QuestionID {
		t.Fatalf("waits = %+v", w)
	}
	if err := h.Send("demo", model.User, model.Coordinator, "postgres", pngImage); err != nil {
		t.Fatalf("an answer may carry a picture: %v", err)
	}
	eventually(t, "the answer's turn", func() bool { return len(f.requests()) == 2 })
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if w := h.Waits(); slices.ContainsFunc(w, func(w model.Wait) bool { return w.Kind == model.WaitQuestion }) {
		t.Fatalf("the answer closes the question: %+v", w)
	}
}

// TestASkippedQuestionTellsTheThread: a skip is the person's word too, so
// the thread hears it and goes on without the answer.
func TestASkippedQuestionTellsTheThread(t *testing.T) {
	h, f := askThenHold(t)
	id := view(t, h, "demo", model.Coordinator).QuestionID
	if err := h.Answer("demo", model.Coordinator, id, model.Allow, ""); err == nil {
		t.Fatal("a question is answered by writing, not allowed")
	}
	if err := h.Answer("demo", model.Coordinator, id, model.Deny, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the skip's turn", func() bool { return len(f.requests()) == 2 })
	if p := f.requests()[1].Prompt; !strings.Contains(p, "skipped your question") || !strings.Contains(p, "fyi") {
		t.Fatalf("prompt = %q", p)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if w := h.Waits(); slices.ContainsFunc(w, func(w model.Wait) bool { return w.Kind == model.WaitQuestion }) {
		t.Fatalf("waits = %+v", w)
	}
}

// askThenHold has the coordinator ask a question, which ends its turn,
// then another thread write to it while the question waits.
func askThenHold(t *testing.T) (*Hub, *fake) {
	t.Helper()
	f := &fake{}
	h := newHub(t, f)
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		if strings.Contains(req.Prompt, "set up storage") {
			if err := h.Ask("demo", model.Coordinator, "which database?", nil); err != nil {
				t.Error(err)
			}
			return agent.Result{Text: "asked"}
		}
		return agent.Result{Text: "ok"}
	}
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "set up storage")
	eventually(t, "the asking turn to end", func() bool {
		evs, _ := h.Events("demo", model.Coordinator, 50)
		return slices.ContainsFunc(evs, func(e model.Event) bool { return e.Kind == model.EventTurnEnd })
	})
	hubMessage(h, wakeFrom, "fyi")
	return h, f
}

func TestQuestionHoldsOtherMessagesUntilTheAnswer(t *testing.T) {
	h, f := askThenHold(t)
	time.Sleep(50 * time.Millisecond)
	if n := len(f.requests()); n != 1 {
		t.Fatalf("%d turns; a thread's message must wait behind the question", n)
	}
	if v := view(t, h, "demo", model.Coordinator); v.Waiting != "which database?" || v.Queued != 1 {
		t.Fatalf("waiting %q, queued %d", v.Waiting, v.Queued)
	}
	_ = h.Send("demo", model.User, model.Coordinator, "postgres")
	eventually(t, "the answer's turn", func() bool { return len(f.requests()) == 2 })
	p := f.requests()[1].Prompt
	if a, b := strings.Index(p, "postgres"), strings.Index(p, "fyi"); a < 0 || b < 0 || a > b {
		t.Fatalf("the answer goes first, with the held message: %q", p)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
}

func TestSendNowReleasesHeldMessagesAndClosesTheQuestion(t *testing.T) {
	h, f := askThenHold(t)
	if err := h.SendNow("demo", model.Coordinator, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the held message to go", func() bool { return len(f.requests()) == 2 })
	if p := f.requests()[1].Prompt; !strings.Contains(p, "fyi") {
		t.Fatalf("prompt = %q", p)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	evs, _ := h.Events("demo", model.Coordinator, 50)
	if !slices.ContainsFunc(evs, func(e model.Event) bool { return e.Kind == model.EventNote && strings.Contains(e.Text, "not answered") }) {
		t.Fatal("the transcript must say the question was not answered")
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
	if err := h.Send("demo", "api", "docs", "more"); err == nil {
		t.Fatal("a resolved thread must refuse another thread's messages")
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
	got := compose([]workspace.Message{{From: model.User, Text: "compare @api and @coordinator, not @api.go, @nope or a@api"}}, threads, false)
	if strings.Count(got, "read_thread") != 2 || !strings.Contains(got, "@api is the thread api") || !strings.Contains(got, "@coordinator is the thread") {
		t.Fatalf("prompt = %q", got)
	}
	if strings.Contains(got, "nope is the thread") {
		t.Fatal("an unknown name got a note")
	}
	// The coordinator has no read_thread: it asks the thread.
	if got := compose([]workspace.Message{{From: model.User, Text: "what did @api find?"}}, threads, true); strings.Contains(got, "read_thread") || !strings.Contains(got, "send_message") {
		t.Fatalf("coordinator prompt = %q", got)
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
		sp, _ := h.Cost("demo")
		return sp.Total == 1.0 && v.Context == 2000 && v.Branch == "main"
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
	if err := h.Ask("demo", model.Coordinator, "Which phase first?", nil); err != nil {
		t.Fatal(err)
	}
	for _, s := range h.Workspaces() {
		if s.Name == "demo" && (s.Coordinator != model.StateWaiting || s.Counts[model.StateWaiting] != 0) {
			t.Fatalf("summary = %+v; the coordinator waits, and it is not a thread", s)
		}
	}
}

func TestTheSummaryCountsNoticesAndSaysWhenTheWorkspaceWasLastUsed(t *testing.T) {
	h := newHub(t, &fake{})
	pinned(t, h, "demo")
	before := time.Now().UTC()
	if err := h.Notify("demo", model.Coordinator, "pick a phase", true); err != nil {
		t.Fatal(err)
	}
	for _, s := range h.Workspaces() {
		if s.Name == "demo" && (s.Notices != 1 || s.LastUsed.Before(before)) {
			t.Fatalf("summary = %+v; one notice waits, and the workspace was used just now", s)
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
// takeBackRun starts a turn on "long task" that runs until stopped; started
// says whether the agent gets to work on it first.
func takeBackRun(t *testing.T, started bool) (*Hub, *fake) {
	t.Helper()
	f := &fake{}
	h := newHub(t, f)
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		if !strings.Contains(req.Prompt, "long task") {
			return agent.Result{Text: "ok"}
		}
		if started {
			req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: "on it"})
		}
		<-ctx.Done()
		return agent.Result{ExitCode: -1}
	}
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "long task")
	stateIs(t, h, "demo", model.Coordinator, model.StateRunning)
	if started {
		eventually(t, "the agent to start", func() bool {
			evs, _ := h.Events("demo", model.Coordinator, 50)
			return slices.ContainsFunc(evs, func(e model.Event) bool { return e.Kind == model.EventText })
		})
	}
	if err := h.TakeBack("demo", model.Coordinator); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	evs, _ := h.Events("demo", model.Coordinator, 50)
	if !slices.ContainsFunc(evs, func(e model.Event) bool { return e.Kind == model.EventTakeBack && e.Text == "long task" }) {
		t.Fatalf("the transcript must say the message was taken back: %+v", evs)
	}
	return h, f
}

func TestTakeBackBeforeTheAgentStartedTellsItNothing(t *testing.T) {
	h, f := takeBackRun(t, false)
	_ = h.Send("demo", model.User, model.Coordinator, "short task")
	eventually(t, "the next turn", func() bool { return len(f.requests()) == 2 })
	if p := f.requests()[1].Prompt; strings.Contains(p, "long task") {
		t.Fatalf("the agent never saw it, so nothing to take back: %q", p)
	}
}

func TestTakeBackAfterTheAgentStartedTellsItWithTheNextMessage(t *testing.T) {
	h, f := takeBackRun(t, true)
	_ = h.Send("demo", model.User, model.Coordinator, "short task")
	eventually(t, "the next turn", func() bool { return len(f.requests()) == 2 })
	if p := f.requests()[1].Prompt; !strings.Contains(p, "took back") || !strings.Contains(p, "long task") || !strings.Contains(p, "short task") {
		t.Fatalf("prompt = %q", p)
	}
}

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

	// A wake-up does not start it again; <CR> on the queued one does.
	hubMessage(h, wakeFrom, "fyi")
	stateIs(t, h, "demo", model.Coordinator, model.StateWaiting)
	if err := h.SendNow("demo", model.Coordinator, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the queued message to go", func() bool { return len(f.requests()) == 2 })
	if p := f.requests()[1].Prompt; !strings.Contains(p, "next one") || !strings.Contains(p, "fyi") {
		t.Fatalf("prompt = %q", p)
	}
}

// A stop of the turn that asked closes the question: the person said stop,
// so nothing waits for them, and their next message starts a new turn.
func TestStopClosesAWaitingQuestion(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		if !strings.Contains(req.Prompt, "set up storage") {
			return agent.Result{Text: "ok"}
		}
		// It asks and works on, until the stop.
		_ = h.Ask("demo", model.Coordinator, "which database?", nil)
		<-ctx.Done()
		return agent.Result{ExitCode: -1}
	}
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "set up storage")
	stateIs(t, h, "demo", model.Coordinator, model.StateWaiting)
	if err := h.Stop("demo", model.Coordinator); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if v := view(t, h, "demo", model.Coordinator); v.Waiting != "" || v.QuestionID != "" {
		t.Fatalf("after stop: waiting %q, question %q", v.Waiting, v.QuestionID)
	}
	if w := needs(h.Waits()); len(w) != 0 {
		t.Fatalf("waits = %+v", w)
	}
	evs, _ := h.Events("demo", model.Coordinator, 50)
	if !slices.ContainsFunc(evs, func(e model.Event) bool { return e.Kind == model.EventNote && e.Text == "Interrupted." }) {
		t.Fatalf("the chat must say the turn was interrupted: %+v", evs)
	}
	_ = h.Send("demo", model.User, model.Coordinator, "next")
	eventually(t, "the next turn", func() bool { return len(f.requests()) == 2 })
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
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

// TestAWakeUpComesAfterTheComputerSleeps: Go timers stop while macOS sleeps,
// but the wall clock goes on, so a wake-up whose time passed during sleep
// must come soon after, not as late as the sleep was long.
func TestAWakeUpComesAfterTheComputerSleeps(t *testing.T) {
	defer func(d time.Duration, c func() time.Time) { wakeCheck, wallClock = d, c }(wakeCheck, wallClock)
	wakeCheck = 20 * time.Millisecond
	f := &fake{}
	h := newHub(t, f)
	pinned(t, h, "demo")
	_ = h.WakeMe("demo", model.Coordinator, time.Hour, "after the sleep")
	h.mu.Lock()
	wallClock = func() time.Time { return time.Now().Add(time.Hour) }
	h.mu.Unlock()
	eventually(t, "the wake-up after the sleep", func() bool {
		rs := f.requests()
		return len(rs) == 1 && strings.Contains(rs[0].Prompt, "after the sleep")
	})
	if v := view(t, h, "demo", model.Coordinator); !v.WakeAt.IsZero() {
		t.Error("a wake-up that came should be gone")
	}
}

// TestARestartedSessionGetsTheCurrentPrompt: a workspace lives for long, so
// after a restart its coordinator continues its conversation with the prompt
// as it is now, here changed by the notify setting.
func TestARestartedSessionGetsTheCurrentPrompt(t *testing.T) {
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	f := &fake{}
	cfg := Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }}
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "hello")
	eventually(t, "the first turn", func() bool { return len(f.requests()) == 1 })
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	h.Close()

	cfg.Notify = true
	h2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h2.Close)
	_ = h2.Send("demo", model.User, model.Coordinator, "again")
	eventually(t, "the turn after the restart", func() bool { return len(f.requests()) == 2 })
	first, second := f.requests()[0], f.requests()[1]
	if !second.Resume || second.Conversation != first.Conversation {
		t.Fatalf("the restarted coordinator must continue its conversation: %+v / %+v", first, second)
	}
	if second.SystemPrompt == first.SystemPrompt || !strings.Contains(second.SystemPrompt, "when threads end a turn") {
		t.Errorf("the restarted coordinator got the old prompt: %q", second.SystemPrompt)
	}
}

// TestThreadTurnsWakeTheCoordinator: with Notify on, threads whose turns
// bring news close together wake the coordinator once, and those turns do
// not count toward the pause.
func TestThreadTurnsWakeTheCoordinator(t *testing.T) {
	defer func(d time.Duration) { reportDelay = d }(reportDelay)
	reportDelay = 50 * time.Millisecond
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		return agent.Result{Text: "Ran the tests.", Tools: 1}
	}
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
// you want it back; the coordinator's message brings it back too, a
// thread's does not.
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
	if err := h.Send("demo", model.User, "docs", "one more thing"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "docs", model.StateIdle)
	if reqs := f.requests(); !strings.Contains(reqs[len(reqs)-1].Prompt, "one more thing") {
		t.Fatalf("last prompt = %q, want your message to run", reqs[len(reqs)-1].Prompt)
	}

	if err := h.Resolve("demo", model.Coordinator, "docs"); err != nil {
		t.Fatal(err)
	}
	if err := h.Send("demo", model.Coordinator, "docs", "and the tests"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "docs", model.StateIdle)
	if reqs := f.requests(); !strings.Contains(reqs[len(reqs)-1].Prompt, "and the tests") {
		t.Fatalf("last prompt = %q, want the coordinator's message to run", reqs[len(reqs)-1].Prompt)
	}
	if evs, _ := h.Events("demo", "docs", 0); !slices.ContainsFunc(evs, func(e model.Event) bool { return e.Text == "Reopened by coordinator" }) {
		t.Fatal("the thread should say the coordinator reopened it")
	}
}

// fullThread opens docs in demo, fills its context to 90% and resolves it.
// The fake answers a summary request with a summary.
func fullThread(t *testing.T, mode string) (*Hub, *fake) {
	t.Helper()
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		if strings.Contains(req.Prompt, "Reply with only the summary") {
			return agent.Result{Text: "SUMMARY", Context: 100, ContextWindow: 1000}
		}
		return agent.Result{Text: "ok", Context: 900, ContextWindow: 1000}
	}
	h := newHub(t, f)
	h.cfg.CompactWarn, h.cfg.ReopenCompact = 80, mode
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "docs", "work", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "docs", model.StateIdle)
	if err := h.Resolve("demo", model.Coordinator, "docs"); err != nil {
		t.Fatal(err)
	}
	return h, f
}

func TestReopenedFullThreadCompactsFirst(t *testing.T) {
	h, f := fullThread(t, ReopenCompact)
	if err := h.Send("demo", model.Coordinator, "docs", "more work"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "docs", model.StateIdle)
	r := f.requests()
	if len(r) != 3 || !strings.Contains(r[1].Prompt, "Reply with only the summary") {
		t.Fatalf("want the summary turn before the message: %+v", r)
	}
	if !strings.Contains(r[2].Prompt, "SUMMARY") || !strings.HasSuffix(r[2].Prompt, "more work") {
		t.Fatalf("the message should start the compacted session: %q", r[2].Prompt)
	}
}

func TestReopenedFullThreadAsksFirst(t *testing.T) {
	h, f := fullThread(t, "")
	if err := h.Send("demo", model.Coordinator, "docs", "more work"); err != nil {
		t.Fatal(err)
	}
	v := view(t, h, "demo", "docs")
	if v.State != model.StateWaiting || !strings.Contains(v.Waiting, "90% full") || v.Queued != 1 {
		t.Fatalf("want the message held and a question: %+v", v)
	}
	if len(f.requests()) != 1 {
		t.Fatal("no turn should run before the person chooses")
	}
	// /compact: the summary first, then the held message.
	if err := h.Send("demo", model.User, "docs", "/compact"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "docs", model.StateIdle)
	r := f.requests()
	if len(r) != 3 || !strings.Contains(r[1].Prompt, "Reply with only the summary") || !strings.HasSuffix(r[2].Prompt, "more work") {
		t.Fatalf("turns = %+v", r)
	}

	// Or send it as it is, and auto compact does not compact instead.
	h, f = fullThread(t, "")
	h.cfg.AutoCompact = 80
	_ = h.Send("demo", model.Coordinator, "docs", "more work")
	if err := h.SendNow("demo", "docs", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "docs", model.StateIdle)
	if r := f.requests(); len(r) != 2 || !r[1].Resume || !strings.HasSuffix(r[1].Prompt, "more work") {
		t.Fatalf("turns = %+v", r)
	}
	if v := view(t, h, "demo", "docs"); v.Waiting != "" {
		t.Fatalf("the question should go: %+v", v)
	}
}

func TestReopenCompactOffAndSetlocal(t *testing.T) {
	h, f := fullThread(t, ReopenCompact)
	if err := h.SetLocal("demo", "reopen_compact", "never"); err == nil {
		t.Fatal("reopen_compact takes compact, ask or off")
	}
	if err := h.SetLocal("demo", "reopen_compact", ReopenOff); err != nil {
		t.Fatal(err)
	}
	ws := h.Workspaces()
	if ws[0].ReopenCompact != ReopenOff {
		t.Fatalf("workspace view = %+v", ws[0])
	}
	_ = h.Send("demo", model.Coordinator, "docs", "more work")
	stateIs(t, h, "demo", "docs", model.StateIdle)
	if r := f.requests(); len(r) != 2 || !strings.HasSuffix(r[1].Prompt, "more work") {
		t.Fatalf("off sends the message as it is: %+v", r)
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
		want := model.StateIdle
		if s.prompt == "fails" {
			want = model.StateFailed
		}
		stateIs(t, h, s.ws, model.Coordinator, want)
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
	all, _ := h.Cost("")
	if all.Total != 3.25 || len(all.Workspaces) != 2 || all.Workspaces[0].Name != "demo" || all.Workspaces[1].Cost != 0.5 {
		t.Fatalf("all = %+v", all)
	}
	if _, err := h.Cost("nope"); err == nil {
		t.Fatal("an unknown workspace should fail")
	}
}

func TestReadArtifactStaysInItsWorkspace(t *testing.T) {
	h := newHub(t, &fake{})
	pinned(t, h, "demo")
	pinned(t, h, "other")
	if _, err := h.WriteArtifact("other", model.Coordinator, "secret", "# Secret\n"); err != nil {
		t.Fatal(err)
	}
	name := "../../other/artifacts/secret"
	if text, err := h.ReadArtifactAs("demo", model.Coordinator, name); err == nil {
		t.Errorf("ReadArtifactAs(%q) read another workspace: %q", name, text)
	}
	if _, text, err := h.ReadArtifact("demo", name); err == nil {
		t.Errorf("ReadArtifact(%q) read another workspace: %q", name, text)
	}
}

// "Always" on a call with no scope, such as a chained command, allows it once
// but stores no rule: a rule would let `rm -rf ~` through after
// `rm -rf build && make`.
func TestAlwaysWithNoScopeStoresNoRule(t *testing.T) {
	results := make(chan bool, 2)
	f := &fake{}
	h := newHub(t, f)
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		for _, q := range []agent.Permission{
			{Tool: "Bash", Detail: "rm -rf build && make"},
			{Tool: "Bash", Detail: "rm -rf ~"},
		} {
			ok, _ := h.Permission(ctx, "demo", model.Coordinator, q)
			results <- ok
		}
		return agent.Result{Text: "ok"}
	}
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "rebuild")

	stateIs(t, h, "demo", model.Coordinator, model.StateWaiting)
	if err := h.Answer("demo", model.Coordinator, "", model.Always, ""); err != nil {
		t.Fatal(err)
	}
	if !<-results {
		t.Fatal("the chained command was denied")
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateWaiting)
	if v := view(t, h, "demo", model.Coordinator); v.Permission == nil || v.Permission.Detail != "rm -rf ~" {
		t.Fatalf("permission = %+v, want a card for rm -rf ~", v.Permission)
	}
	if err := h.Answer("demo", model.Coordinator, "", model.Deny, ""); err != nil {
		t.Fatal(err)
	}
	<-results
}

// TestAFailedTurnShowsOnTheBoardUntilTheThreadRunsAgain: the coordinator
// cannot read transcripts, so the board says why a thread's last turn failed
// for as long as the thread needs someone.
func TestAFailedTurnShowsOnTheBoardUntilTheThreadRunsAgain(t *testing.T) {
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		if strings.Contains(req.Prompt, "try") {
			return agent.Result{ExitCode: 1, Stderr: "panic\nfake: crashed on purpose"}
		}
		return agent.Result{Text: "ok"}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "flaky", "try", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "flaky", model.StateFailed)
	for _, msg := range []string{"one", "two"} {
		_ = h.Send("demo", model.User, model.Coordinator, msg)
		stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	}
	var coord []string
	for _, r := range f.requests() {
		if strings.HasPrefix(r.Prompt, "Threads:") {
			coord = append(coord, r.Prompt)
		}
	}
	failed := "- flaky: failed. Last turn failed: the agent failed: fake: crashed on purpose.\n"
	if len(coord) != 2 || !strings.Contains(coord[0], failed) || !strings.Contains(coord[1], failed) {
		t.Fatalf("coordinator turns = %q, want the failure on both", coord)
	}
	if v := view(t, h, "demo", "flaky"); v.Reply != "" {
		t.Fatalf("reply of a failed turn = %q", v.Reply)
	}
}

// TestTheThreadsPaneShowsTheStartOfTheLastReply: with no progress lines, a
// thread's row says what its last turn came to.
func TestTheThreadsPaneShowsTheStartOfTheLastReply(t *testing.T) {
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		return agent.Result{Text: "All tests pass.\n\nDetails below."}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "cache", "add a cache", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "cache", model.StateIdle)
	if v := view(t, h, "demo", "cache"); v.Reply != "All tests pass. Details below." {
		t.Fatalf("reply = %q", v.Reply)
	}
}

func workspaceView(t *testing.T, h *Hub, name string) model.WorkspaceView {
	t.Helper()
	for _, v := range h.Workspaces() {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("no workspace %s", name)
	return model.WorkspaceView{}
}

// hubMessage queues a message from the hub itself for the coordinator of
// "demo", as a wake-up or a report does.
func hubMessage(h *Hub, from, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws := h.workspaces["demo"]
	h.enqueue(ws, ws.threads[model.Coordinator], from, text)
}

// TestThreadsDoNotMessageTheCoordinator: the coordinator's chat is the
// person's and the coordinator's; its board shows how threads stand.
func TestThreadsDoNotMessageTheCoordinator(t *testing.T) {
	h := newHub(t, &fake{})
	pinned(t, h, "demo")
	for _, n := range []string{"api", "docs"} {
		if err := h.OpenThread("demo", model.Coordinator, n, "work", ""); err != nil {
			t.Fatal(err)
		}
		stateIs(t, h, "demo", n, model.StateIdle)
	}
	if err := h.Send("demo", "api", model.Coordinator, "done"); err == nil || !strings.Contains(err.Error(), "board") {
		t.Fatalf("err = %v, want a refusal that points to the board", err)
	}
	if err := h.Send("demo", "api", "docs", "I change README.md"); err != nil {
		t.Fatalf("a thread messages a thread: %v", err)
	}
}

// TestTurnsAboutThreadsStayOutOfTheCoordinatorChat: the hub wakes the
// coordinator to manage; that turn says nothing in its chat, but what it
// shows the person on purpose, as a notice, still shows.
func TestTurnsAboutThreadsStayOutOfTheCoordinatorChat(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		if strings.Contains(req.Prompt, "has news") {
			_ = h.Notify("demo", model.Coordinator, "tests are done", false)
		}
		return agent.Result{Text: "Nothing to tell."}
	}
	pinned(t, h, "demo")
	hubMessage(h, reportFrom, prompts.Reports([]string{"tests"}))
	eventually(t, "the report turn", func() bool { return len(f.requests()) == 1 })
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	evs, _ := h.Events("demo", model.Coordinator, 50)
	if len(evs) != 1 || evs[0].Kind != model.EventNotice {
		t.Fatalf("events = %+v, want only the notice", evs)
	}
	// The person's own turn shows as before.
	_ = h.Send("demo", model.User, model.Coordinator, "status?")
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	evs, _ = h.Events("demo", model.Coordinator, 50)
	if !slices.ContainsFunc(evs, func(e model.Event) bool { return e.Kind == model.EventTurnEnd }) {
		t.Fatalf("events = %+v, want the person's turn", evs)
	}
}

var (
	pngImage = []byte("\x89PNG\r\n\x1a\none")
	gifImage = []byte("GIF89atwo")
)

// TestImagesGoWithTheMessage: each message numbers its own images; a turn
// that reads two of them must not show the agent two `[image 1]`s.
func TestImagesGoWithTheMessage(t *testing.T) {
	gate := make(chan struct{}, 2)
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		<-gate
		return agent.Result{Text: "ok"}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.Send("demo", model.User, model.Coordinator, "start"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateRunning)
	if err := h.Send("demo", model.User, model.Coordinator, "see [image 1]", pngImage); err != nil {
		t.Fatal(err)
	}
	if err := h.Send("demo", model.User, model.Coordinator, "and [image 1]", gifImage); err != nil {
		t.Fatal(err)
	}
	if p := view(t, h, "demo", model.Coordinator).Pending; len(p) != 2 || p[0].Text != "see [image 1]" {
		t.Fatalf("pending = %+v, want the messages as the person wrote them", p)
	}
	gate <- struct{}{}
	gate <- struct{}{}
	eventually(t, "the second turn", func() bool { return len(f.requests()) == 2 })
	req := f.requests()[1]
	if len(req.Images) != 2 || req.Images[0].Type != "image/png" || string(req.Images[1].Data) != string(gifImage) {
		t.Fatalf("images = %+v, want the png then the gif", req.Images)
	}
	if !strings.Contains(req.Prompt, "see [image 1]") || !strings.Contains(req.Prompt, "and [image 2]") {
		t.Errorf("prompt = %q, want the second message's image as [image 2]", req.Prompt)
	}
}

func TestImagesAreChecked(t *testing.T) {
	h := newHub(t, &fake{})
	pinned(t, h, "demo")
	for what, send := range map[string]func() error{
		"not an image": func() error { return h.Send("demo", model.User, model.Coordinator, "x", []byte("<svg/>")) },
		"too many": func() error {
			return h.Send("demo", model.User, model.Coordinator, "x", slices.Repeat([][]byte{pngImage}, model.MaxImages+1)...)
		},
		"with a command": func() error { return h.Send("demo", model.User, model.Coordinator, "/model big", pngImage) },
		"from an agent":  func() error { return h.Send("demo", model.Coordinator, "api", "x", pngImage) },
	} {
		if err := send(); err == nil {
			t.Errorf("%s: want an error", what)
		}
	}
	if err := h.Send("demo", model.User, model.Coordinator, "", pngImage); err != nil {
		t.Errorf("an image alone is a message: %v", err)
	}
}

func TestAutoCompactBeforeTheNextTurn(t *testing.T) {
	f := &fake{}
	summary := "SUMMARY"
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		if strings.Contains(req.Prompt, "Reply with only the summary") {
			return agent.Result{Text: summary, Context: 100, ContextWindow: 1000}
		}
		return agent.Result{Text: "ok", Context: 950, ContextWindow: 1000}
	}
	h := newHub(t, f)
	h.cfg.AutoCompact = 90
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "docs", "work", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "docs", model.StateIdle)
	if n := len(f.requests()); n != 1 {
		t.Fatalf("a thread with nothing to do should not compact: %d turns", n)
	}
	if err := h.Send("demo", model.Coordinator, "docs", "more work"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "docs", model.StateIdle)
	r := f.requests()
	if len(r) != 3 || !strings.Contains(r[1].Prompt, "Reply with only the summary") {
		t.Fatalf("want the summary turn before the message: %d turns", len(r))
	}
	if !strings.Contains(r[2].Prompt, "SUMMARY") || !strings.HasSuffix(r[2].Prompt, "more work") {
		t.Fatalf("the message should start the compacted session: %q", r[2].Prompt)
	}
	if evs, _ := h.Events("demo", "docs", 0); !slices.ContainsFunc(evs, func(e model.Event) bool {
		return e.Kind == model.EventNote && strings.Contains(e.Text, "95% full")
	}) {
		t.Fatal("a note should say the context was compacted")
	}

	// A summary that fails is tried once, not before every message.
	summary = ""
	for _, text := range []string{"again", "and again"} {
		if err := h.Send("demo", model.Coordinator, "docs", text); err != nil {
			t.Fatal(err)
		}
		stateIs(t, h, "demo", "docs", model.StateIdle)
	}
	if n := len(f.requests()); n != 6 {
		t.Fatalf("turns = %d, want one failed summary and two messages", n)
	}

	// :setlocal auto_compact=0 turns it off here.
	if err := h.SetLocal("demo", "auto_compact", "0"); err != nil {
		t.Fatal(err)
	}
	if err := h.SetLocal("demo", "auto_compact", "101"); err == nil {
		t.Error("auto_compact past 100 should fail")
	}
	summary = "SUMMARY"
	_ = h.Send("demo", model.User, "docs", "/compact")
	stateIs(t, h, "demo", "docs", model.StateIdle)
	_ = h.Send("demo", model.Coordinator, "docs", "one")
	stateIs(t, h, "demo", "docs", model.StateIdle)
	_ = h.Send("demo", model.Coordinator, "docs", "two")
	stateIs(t, h, "demo", "docs", model.StateIdle)
	if n := len(f.requests()); n != 9 {
		t.Fatalf("turns = %d, want no compact with auto_compact=0", n)
	}
}

func TestTextwidthComesFromTheConfigAndSetlocal(t *testing.T) {
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	h, err := New(Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return &fake{} }, TextWidth: 100})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	pinned(t, h, "demo")
	tw := func() int { return h.Workspaces()[0].TextWidth }
	if tw() != 100 {
		t.Fatalf("textwidth = %d, want the config's 100", tw())
	}
	if err := h.SetLocal("demo", "textwidth", "0"); err != nil {
		t.Fatal(err)
	}
	if tw() != 0 {
		t.Fatalf("textwidth = %d, want 0 after :setlocal textwidth=0", tw())
	}
	if err := h.SetLocal("demo", "textwidth", "-1"); err == nil {
		t.Error("a negative textwidth should fail")
	}
	if err := h.SetLocal("demo", "textwidth", ""); err != nil || tw() != 100 {
		t.Fatalf("textwidth& = %d, %v; want the config's 100", tw(), err)
	}
}

// TestARecapStaysWithTheThread: a session's recap shows in its view and
// a restart keeps it, so the person reads it when they come back.
func TestARecapStaysWithTheThread(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.Recap("demo", model.Coordinator, "  Cache added;\n tests pass.  "); err != nil {
		t.Fatal(err)
	}
	v := view(t, h, "demo", model.Coordinator)
	if v.Recap != "Cache added; tests pass." || v.RecapAt.IsZero() {
		t.Fatalf("recap = %q at %v", v.Recap, v.RecapAt)
	}
	h2, err := New(Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }, MCPPort: 4242})
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()
	if v := view(t, h2, "demo", model.Coordinator); v.Recap != "Cache added; tests pass." {
		t.Fatalf("recap after restart = %q", v.Recap)
	}
}

func TestTodoEventsKeepTheThreadsTodoList(t *testing.T) {
	turns := [][]agent.Event{
		{
			{Kind: agent.KindTodo, ID: "1", Text: "Write the parser", State: agent.TodoPending},
			{Kind: agent.KindTodo, ID: "2", Text: "Add tests", State: agent.TodoPending},
			{Kind: agent.KindTodo, ID: "1", State: agent.TodoActive},
		},
		{
			{Kind: agent.KindTodo, ID: "1", Text: "Write the lexer", State: agent.TodoDone},
			{Kind: agent.KindTodo, ID: "2", State: agent.TodoRemoved},
			{Kind: agent.KindTodo, ID: "3", Text: "Ship it", State: agent.TodoPending},
			{Kind: agent.KindTodo, ID: "7", State: agent.TodoDone},
		},
		{
			{Kind: agent.KindTodo, ID: "3", State: agent.TodoDone},
		},
		{
			{Kind: agent.KindTodo, ID: "8", Text: "Plan the release", State: agent.TodoPending},
		},
	}
	f := &fake{}
	n := 0
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		for _, e := range turns[n] {
			req.OnEvent(e)
		}
		n++
		return agent.Result{Text: "ok"}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	todos := func() []model.Todo { return view(t, h, "demo", model.Coordinator).Todos }

	if err := h.Send("demo", model.User, model.Coordinator, "plan"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	want := []model.Todo{{ID: "1", Text: "Write the parser", State: model.TodoActive}, {ID: "2", Text: "Add tests", State: model.TodoPending}}
	if got := todos(); !slices.Equal(got, want) {
		t.Fatalf("todos = %+v, want %+v", got, want)
	}

	if err := h.Send("demo", model.User, model.Coordinator, "go on"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	want = []model.Todo{{ID: "1", Text: "Write the lexer", State: model.TodoDone}, {ID: "3", Text: "Ship it", State: model.TodoPending}}
	if got := todos(); !slices.Equal(got, want) {
		t.Fatalf("an update to an item it never saw must not add it: todos = %+v, want %+v", got, want)
	}

	if err := h.Send("demo", model.User, model.Coordinator, "finish"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	want = []model.Todo{{ID: "1", Text: "Write the lexer", State: model.TodoDone}, {ID: "3", Text: "Ship it", State: model.TodoDone}}
	if got := todos(); !slices.Equal(got, want) {
		t.Fatalf("a done list stays: todos = %+v, want %+v", got, want)
	}

	if err := h.Send("demo", model.User, model.Coordinator, "next"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	want = []model.Todo{{ID: "8", Text: "Plan the release", State: model.TodoPending}}
	if got := todos(); !slices.Equal(got, want) {
		t.Fatalf("a new item after a done list starts a new list: todos = %+v, want %+v", got, want)
	}

	if err := h.Send("demo", model.User, model.Coordinator, "/clear"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the todo list to go with the conversation", func() bool { return len(todos()) == 0 })
}

func TestTodosOffKeepsNoTodoList(t *testing.T) {
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		req.OnEvent(agent.Event{Kind: agent.KindTodo, ID: "1", Text: "Write the parser", State: agent.TodoPending})
		return agent.Result{Text: "ok"}
	}
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	h, err := New(Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }, NoTodos: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	pinned(t, h, "demo")
	if err := h.Send("demo", model.User, model.Coordinator, "plan"); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if got := view(t, h, "demo", model.Coordinator).Todos; len(got) != 0 {
		t.Fatalf("todos = %+v, want none", got)
	}
}

// newsHub is a hub whose coordinator hears of the threads' news at once.
func newsHub(t *testing.T, f *fake, notify bool) *Hub {
	t.Helper()
	defer func(d time.Duration) { t.Cleanup(func() { reportDelay = d }) }(reportDelay)
	reportDelay = 30 * time.Millisecond
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	h, err := New(Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f }, Notify: notify})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

// newsScript is a thread that does what its message asks: talk, recap,
// finish a todo item, write an artifact, fail, or run tools without a recap.
func newsScript(h **Hub) func(ctx context.Context, req agent.Request) agent.Result {
	return func(ctx context.Context, req agent.Request) agent.Result {
		name := ""
		if _, rest, ok := strings.Cut(req.Prompt, "as "); ok {
			name = strings.Fields(rest)[0]
		}
		switch {
		case strings.Contains(req.Prompt, "please recap"):
			_ = (*h).Recap("demo", name, "Cache added")
			return agent.Result{Text: "Done.", Tools: 2}
		case strings.Contains(req.Prompt, "please finish"):
			req.OnEvent(agent.Event{Kind: agent.KindTodo, ID: "1", Text: "Add the route", State: agent.TodoActive})
			req.OnEvent(agent.Event{Kind: agent.KindTodo, ID: "1", State: agent.TodoDone})
		case strings.Contains(req.Prompt, "please write"):
			_, _ = (*h).WriteArtifact("demo", name, "plan", "# Plan\n")
		case strings.Contains(req.Prompt, "please fail"):
			return agent.Result{ExitCode: 1, Stderr: "fake: crashed"}
		case strings.Contains(req.Prompt, "please run"):
			return agent.Result{Text: "Moved the store.", Tools: 3}
		}
		return agent.Result{Text: "Sure."}
	}
}

func reportTurns(f *fake) []agent.Request {
	var out []agent.Request
	for _, r := range f.requests() {
		if strings.Contains(r.Prompt, "Message from threads") {
			out = append(out, r)
		}
	}
	return out
}

// TestOnlyNewsWakesTheCoordinator: a turn that only talks is no progress; a
// recap, a done todo item, an artifact or a failure is, and wakes the
// coordinator with it on the board.
func TestOnlyNewsWakesTheCoordinator(t *testing.T) {
	f := &fake{}
	var h *Hub
	f.script = newsScript(&h)
	h = newsHub(t, f, true)
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "chat", "just talk as chat", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "chat", model.StateIdle)
	time.Sleep(10 * reportDelay)
	if rs := reportTurns(f); len(rs) != 0 {
		t.Fatalf("a turn that only talked woke the coordinator: %q", rs[0].Prompt)
	}
	for _, c := range []struct{ name, brief, want string }{
		{"rec", "please recap as rec", "- rec: idle. Recap: Cache added.\n  New: recap."},
		{"todo", "please finish as todo", "- todo: idle. Tasks: 1/1 done.\n  New: done: Add the route."},
		{"art", "please write as art", "- art: idle.\n  New: wrote plan."},
		{"bad", "please fail as bad", "- bad: failed. Last turn failed: the agent failed: fake: crashed."},
	} {
		n := len(reportTurns(f))
		if err := h.OpenThread("demo", model.Coordinator, c.name, c.brief, ""); err != nil {
			t.Fatal(err)
		}
		eventually(t, c.name+" to wake the coordinator", func() bool { return len(reportTurns(f)) == n+1 })
		if p := reportTurns(f)[n].Prompt; !strings.Contains(p, c.want) || !strings.Contains(p, c.name+" has news") {
			t.Errorf("report = %q, want %q", p, c.want)
		}
		stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	}
}

// TestNewsShowsOnceAndARunWithoutARecapShowsItsReply: each piece of news is
// on one board; a turn that ran tools and wrote no recap brings the start
// of its reply, and one with a recap does not.
func TestNewsShowsOnceAndARunWithoutARecapShowsItsReply(t *testing.T) {
	f := &fake{}
	var h *Hub
	f.script = newsScript(&h)
	h = newsHub(t, f, false)
	pinned(t, h, "demo")
	for _, c := range [][2]string{{"run", "please run as run"}, {"rec", "please recap as rec"}} {
		if err := h.OpenThread("demo", model.Coordinator, c[0], c[1], ""); err != nil {
			t.Fatal(err)
		}
		stateIs(t, h, "demo", c[0], model.StateIdle)
	}
	time.Sleep(5 * reportDelay)
	if rs := reportTurns(f); len(rs) != 0 {
		t.Fatal("without Notify news must not wake the coordinator")
	}
	board := func(msg string) string {
		_ = h.Send("demo", model.User, model.Coordinator, msg)
		stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
		rs := f.requests()
		return rs[len(rs)-1].Prompt
	}
	first := board("status?")
	if !strings.Contains(first, "- run: idle.\n  New: reply: Moved the store.") || !strings.Contains(first, "- rec: idle. Recap: Cache added.\n  New: recap.\n") {
		t.Fatalf("first board = %q", first)
	}
	if second := board("and now?"); strings.Contains(second, "New:") {
		t.Fatalf("news came twice: %q", second)
	}
}

// TestNewsOutlivesARestart: news the coordinator has not seen is saved with
// the thread, and wakes the coordinator when the hub starts again.
func TestNewsOutlivesARestart(t *testing.T) {
	f := &fake{}
	var h *Hub
	f.script = newsScript(&h)
	h = newsHub(t, f, false)
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "rec", "please recap as rec", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "rec", model.StateIdle)
	h.Close()
	f2 := &fake{}
	h2, err := New(Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return f2 }, Notify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()
	eventually(t, "the news to wake the coordinator", func() bool { return len(reportTurns(f2)) == 1 })
	if p := reportTurns(f2)[0].Prompt; !strings.Contains(p, "New: recap.") {
		t.Fatalf("report after restart = %q", p)
	}
}

// bgFake starts background work in each turn. A follow waits for done, or
// for its stop, and then ends the turn the agent starts by itself.
func bgFake(done chan struct{}) *fake {
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		if !req.Follow {
			req.OnEvent(agent.Event{Kind: agent.KindToolStart, Tool: "Bash", Detail: "go test ./..."})
			req.OnEvent(agent.Event{Kind: agent.KindBackground, Background: 1})
			req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: "started"})
			return agent.Result{Text: "started", Tools: 1, Background: 1}
		}
		select {
		case <-ctx.Done():
			return agent.Result{Background: 1}
		case <-done:
		}
		req.OnEvent(agent.Event{Kind: agent.KindBackground, Background: 0})
		req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: "the tests passed"})
		return agent.Result{Text: "the tests passed"}
	}
	return f
}

// TestBackgroundWorkOutlivesTheTurn: a turn that leaves work running is not
// done. Its turn end does not notify; the turn the agent starts when the
// work ends does.
func TestBackgroundWorkOutlivesTheTurn(t *testing.T) {
	done := make(chan struct{})
	f := bgFake(done)
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "api", "run the tests", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "api", model.StateBackground)
	if v := view(t, h, "demo", "api"); v.Background != 1 || v.Level != model.LevelQuiet {
		t.Fatalf("view %+v, want 1 in the background and nothing to notify", v)
	}
	if s := h.Workspaces()[0]; s.Counts[model.StateBackground] != 1 {
		t.Errorf("workspace counts %v", s.Counts)
	}
	close(done)
	stateIs(t, h, "demo", "api", model.StateIdle)
	v := view(t, h, "demo", "api")
	if v.Background != 0 || v.Level != model.LevelNotice || v.Reply != "the tests passed" {
		t.Fatalf("view %+v, want a finished turn to notify", v)
	}
	evs, _ := h.Events("demo", "api", 0)
	var got []string
	for _, e := range evs {
		if e.Kind == model.EventTurnEnd || e.Kind == model.EventText {
			got = append(got, fmt.Sprintf("%s:%s:%d", e.Kind, e.Text, e.Level))
		}
	}
	want := []string{"text:started:0", "turn::0", "text:the tests passed:0", "turn::2"}
	if !slices.Equal(got, want) {
		t.Errorf("events %v, want %v", got, want)
	}
	if reqs := f.requests(); len(reqs) != 2 || !reqs[1].Follow || reqs[1].Conversation != reqs[0].Conversation || !reqs[1].Resume {
		t.Errorf("requests %+v, want the turn and a follow of its conversation", reqs)
	}
}

// TestAMessageDuringBackgroundWorkRunsAtOnce: the agent takes a message
// while its work runs, so the message does not wait for the work.
func TestAMessageDuringBackgroundWorkRunsAtOnce(t *testing.T) {
	f := bgFake(make(chan struct{}))
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "api", "run the tests", ""); err != nil {
		t.Fatal(err)
	}
	stateIs(t, h, "demo", "api", model.StateBackground)
	if err := h.Send("demo", model.User, "api", "how is it going?"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the message to run", func() bool {
		reqs := f.requests()
		return len(reqs) >= 3 && strings.Contains(reqs[2].Prompt, "how is it going?")
	})
	stateIs(t, h, "demo", "api", model.StateBackground)
	if reqs := f.requests(); len(reqs) != 4 || !reqs[1].Follow || reqs[2].Follow || !reqs[3].Follow {
		t.Errorf("requests %+v, want turn, follow, turn, follow", reqs)
	}
}

// TestTheEndOfBackgroundWorkIsNotIdle: between the end of the work and the
// turn the agent starts to read it, the thread is not done.
func TestTheEndOfBackgroundWorkIsNotIdle(t *testing.T) {
	ended, turn := make(chan struct{}), make(chan struct{})
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		if !req.Follow {
			req.OnEvent(agent.Event{Kind: agent.KindBackground, Background: 1})
			return agent.Result{Text: "started", Background: 1}
		}
		req.OnEvent(agent.Event{Kind: agent.KindBackground, Background: 0})
		close(ended)
		<-turn
		req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: "done"})
		return agent.Result{Text: "done"}
	}
	h := newHub(t, f)
	pinned(t, h, "demo")
	if err := h.OpenThread("demo", model.Coordinator, "api", "run the tests", ""); err != nil {
		t.Fatal(err)
	}
	<-ended
	if s := view(t, h, "demo", "api").State; s != model.StateBackground {
		t.Errorf("state %s once the work ended, want background until the agent's turn starts", s)
	}
	close(turn)
	stateIs(t, h, "demo", "api", model.StateIdle)
}

// TestAQuestionComesAtTheEndOfItsTurnWithItsOptions: the reply explains,
// the question and its options come after it, and the number of an option
// answers it.
func TestAQuestionComesAtTheEndOfItsTurnWithItsOptions(t *testing.T) {
	f := &fake{}
	h := newHub(t, f)
	opts := []string{"suggest only: no one is notified", "assign"}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		if strings.Contains(req.Prompt, "find reviewers") {
			if err := h.Ask("demo", model.Coordinator, "May I assign reviewers?", opts); err != nil {
				t.Error(err)
			}
			req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: "Found three owners."})
			return agent.Result{Text: "Found three owners."}
		}
		return agent.Result{Text: "ok"}
	}
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "find reviewers")
	eventually(t, "the asking turn to end", func() bool {
		evs, _ := h.Events("demo", model.Coordinator, 50)
		return slices.ContainsFunc(evs, func(e model.Event) bool { return e.Kind == model.EventAsk })
	})
	evs, _ := h.Events("demo", model.Coordinator, 50)
	var kinds []model.EventKind
	for _, e := range evs {
		if e.Kind == model.EventText || e.Kind == model.EventTurnEnd || e.Kind == model.EventAsk {
			kinds = append(kinds, e.Kind)
		}
	}
	if want := []model.EventKind{model.EventText, model.EventTurnEnd, model.EventAsk}; !slices.Equal(kinds, want) {
		t.Fatalf("events %v, want %v", kinds, want)
	}
	if e := evs[len(evs)-1]; e.Kind != model.EventAsk || !slices.Equal(e.Options, opts) {
		t.Fatalf("last event = %+v", e)
	}
	if v := view(t, h, "demo", model.Coordinator); v.Waiting != "May I assign reviewers?" || !slices.Equal(v.Options, opts) {
		t.Fatalf("view waits on %q with %+v", v.Waiting, v.Options)
	}
	_ = h.Send("demo", model.User, model.Coordinator, " 2 ")
	eventually(t, "the answer's turn", func() bool { return len(f.requests()) == 2 })
	if p := f.requests()[1].Prompt; !strings.Contains(p, "2 · assign") {
		t.Fatalf("the number names its option: %q", p)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	if v := view(t, h, "demo", model.Coordinator); len(v.Options) != 0 {
		t.Fatalf("options outlive the question: %+v", v.Options)
	}
}

// TestAskTakesTwoToFourOptions: a choice of one is no choice, and more
// than four is too many to read in a card.
func TestAskTakesTwoToFourOptions(t *testing.T) {
	h := newHub(t, &fake{})
	pinned(t, h, "demo")
	o := "a"
	for _, bad := range [][]string{{o}, {o, o, o, o, o}, {o, " "}} {
		if err := h.Ask("demo", model.Coordinator, "which?", bad); err == nil {
			t.Fatalf("%d options were taken: %+v", len(bad), bad)
		}
	}
	if err := h.Ask("demo", model.Coordinator, "which?", nil); err != nil {
		t.Fatal(err)
	}
}
