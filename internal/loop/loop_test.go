package loop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MahammadAgayev/harness/internal/agent"
	"github.com/MahammadAgayev/harness/internal/run"
)

// turn is one scripted agent reply.
type turn struct {
	result agent.Result
	err    error
	// before runs just before the reply is returned, for simulating something
	// happening mid-iteration (a stop request, a task edit).
	before func()
}

// fakeAgent replays a script. Worker and reviewer calls are told apart by the
// presence of a system prompt, which is how the real reviewer is dispatched.
type fakeAgent struct {
	name     string
	worker   []turn
	reviewer []turn

	workerCalls   []agent.Request
	reviewerCalls []agent.Request
}

func (f *fakeAgent) Name() string { return f.name }

func (f *fakeAgent) Run(ctx context.Context, req agent.Request) (agent.Result, error) {
	isReview := req.SystemPrompt != ""
	script := &f.worker
	calls := &f.workerCalls
	if isReview {
		script = &f.reviewer
		calls = &f.reviewerCalls
	}
	*calls = append(*calls, req)
	if len(*script) == 0 {
		return agent.Result{}, fmt.Errorf("fake agent ran out of scripted turns (review=%v)", isReview)
	}
	t := (*script)[0]
	*script = (*script)[1:]
	if t.before != nil {
		t.before()
	}
	if req.OnEvent != nil {
		req.OnEvent(agent.Event{Kind: agent.KindToolStart, Tool: "bash", Detail: "go test ./..."})
	}
	return t.result, t.err
}

func ok(text string) turn {
	return turn{result: agent.Result{Text: text, Tools: 2, Tokens: 100, Duration: time.Second}}
}

// newRun creates a runnable run folder in a temp HARNESS_HOME.
func newRun(t *testing.T) run.Dir {
	t.Helper()
	t.Setenv("HARNESS_HOME", t.TempDir())
	d := run.Open("demo")
	if err := d.Create(t.TempDir(), "fake", "", "Make the build green."); err != nil {
		t.Fatal(err)
	}
	return d
}

func cfg(d run.Dir, a agent.Agent) Config {
	return Config{Dir: d, Agent: a}
}

func TestPassEndsTheRun(t *testing.T) {
	d := newRun(t)
	a := &fakeAgent{
		worker:   []turn{ok("did the work")},
		reviewer: []turn{ok("Ran the tests, they pass.\n\nPASS")},
	}
	status, ending, err := Run(context.Background(), cfg(d, a))
	if err != nil {
		t.Fatal(err)
	}
	if status != run.StatusDone {
		t.Errorf("status = %q, want done (%s)", status, ending)
	}
	if len(a.workerCalls) != 1 {
		t.Errorf("expected exactly one iteration, got %d", len(a.workerCalls))
	}
	st, _ := d.ReadState()
	if st.Iteration != 1 || st.Verdict != run.VerdictPass {
		t.Errorf("state = iteration %d verdict %s", st.Iteration, st.Verdict)
	}
	// A pid left behind is how a reader tells a killed run from a finished one.
	if st.PID != 0 {
		t.Errorf("a cleanly finished run must clear its pid, got %d", st.PID)
	}
	if st.Ending == "" {
		t.Error("state should record why the run ended")
	}
}

func TestNeedsWorkFeedsFindingsIntoTheNextIteration(t *testing.T) {
	d := newRun(t)
	findings := "the retry branch in client.go:88 is never exercised"
	a := &fakeAgent{
		worker:   []turn{ok("first attempt"), ok("second attempt")},
		reviewer: []turn{ok(findings + "\n\nNEEDS_WORK"), ok("fixed now\n\nPASS")},
	}
	if _, _, err := Run(context.Background(), cfg(d, a)); err != nil {
		t.Fatal(err)
	}
	if len(a.workerCalls) != 2 {
		t.Fatalf("expected 2 worker calls, got %d", len(a.workerCalls))
	}
	// The findings are the entire value of a NEEDS_WORK: without them the next
	// iteration repeats the same mistake.
	second := a.workerCalls[1].Prompt
	if !strings.Contains(second, findings) {
		t.Error("the second iteration was not told what the reviewer found")
	}
	if !strings.Contains(second, "iteration 2") {
		t.Error("the second prompt should say which iteration it is")
	}
}

func TestThreeConsecutiveReviewErrorsEndTheRun(t *testing.T) {
	d := newRun(t)
	// A reviewer that crashed is not a reviewer that said no: nothing was
	// measured, so iterating on it produces no signal and burns tokens.
	a := &fakeAgent{
		worker: []turn{ok("a"), ok("b"), ok("c"), ok("d")},
		reviewer: []turn{
			{err: errors.New("dispatch failed")},
			{err: errors.New("dispatch failed")},
			{err: errors.New("dispatch failed")},
			ok("PASS"),
		},
	}
	status, ending, err := Run(context.Background(), cfg(d, a))
	if err != nil {
		t.Fatal(err)
	}
	if status != run.StatusFailed {
		t.Errorf("status = %q, want failed", status)
	}
	if !strings.Contains(ending, "3 times in a row") {
		t.Errorf("ending = %q", ending)
	}
	if len(a.workerCalls) != 3 {
		t.Errorf("should have stopped after 3 iterations, ran %d", len(a.workerCalls))
	}
}

func TestReviewErrorCounterResets(t *testing.T) {
	d := newRun(t)
	// Two failures then a success must not leave the run one failure from
	// death: the budget is for *consecutive* failures.
	a := &fakeAgent{
		worker: []turn{ok("a"), ok("b"), ok("c"), ok("d"), ok("e")},
		reviewer: []turn{
			{err: errors.New("boom")},
			{err: errors.New("boom")},
			ok("NEEDS_WORK"),
			{err: errors.New("boom")},
			ok("PASS"),
		},
	}
	status, _, err := Run(context.Background(), cfg(d, a))
	if err != nil {
		t.Fatal(err)
	}
	if status != run.StatusDone {
		t.Errorf("status = %q, want done — the error counter should have reset", status)
	}
}

func TestEmptyReviewIsAnErrorNotAVerdict(t *testing.T) {
	d := newRun(t)
	// A reviewer that exits cleanly having said nothing measured nothing.
	// Parsing that into a verdict is how a run silently stops being reviewed.
	a := &fakeAgent{
		worker: []turn{ok("a"), ok("b")},
		reviewer: []turn{
			{result: agent.Result{Text: "", ExitCode: 0}},
			ok("PASS"),
		},
	}
	if _, _, err := Run(context.Background(), cfg(d, a)); err != nil {
		t.Fatal(err)
	}
	st, _ := d.ReadState()
	if len(st.History) < 1 || st.History[0].Verdict != run.VerdictError {
		t.Fatalf("first verdict should be ERROR, history = %+v", st.History)
	}
	// And the worker must be told it was a dispatch failure, not criticism.
	if !strings.Contains(a.workerCalls[1].Prompt, "not a verdict on your work") {
		t.Error("the next iteration should be told the review did not happen")
	}
}

func TestStopBeforeTheFirstIteration(t *testing.T) {
	d := newRun(t)
	if err := d.RequestStop(); err != nil {
		t.Fatal(err)
	}
	a := &fakeAgent{worker: []turn{ok("should never run")}}
	status, ending, err := Run(context.Background(), cfg(d, a))
	if err != nil {
		t.Fatal(err)
	}
	if status != run.StatusStopped {
		t.Errorf("status = %q, want stopped (%s)", status, ending)
	}
	if len(a.workerCalls) != 0 {
		t.Error("a stop before the first iteration must not spend a turn")
	}
}

func TestStopDuringAnIterationSkipsTheReview(t *testing.T) {
	d := newRun(t)
	// A stop that lands mid-iteration should not then spend an hour on a review
	// the user is no longer waiting for.
	a := &fakeAgent{
		worker: []turn{{
			result: agent.Result{Text: "worked", Duration: time.Second},
			before: func() { _ = d.RequestStop() },
		}},
		reviewer: []turn{ok("PASS")},
	}
	status, _, err := Run(context.Background(), cfg(d, a))
	if err != nil {
		t.Fatal(err)
	}
	if status != run.StatusStopped {
		t.Errorf("status = %q, want stopped", status)
	}
	if len(a.reviewerCalls) != 0 {
		t.Error("the review should have been skipped")
	}
}

func TestIterationCeiling(t *testing.T) {
	d := newRun(t)
	var w, r []turn
	for i := 0; i < 5; i++ {
		w = append(w, ok("work"))
		r = append(r, ok("NEEDS_WORK"))
	}
	c := cfg(d, &fakeAgent{worker: w, reviewer: r})
	c.MaxIterations = 3
	status, ending, err := Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if status != run.StatusFailed {
		t.Errorf("status = %q, want failed", status)
	}
	if !strings.Contains(ending, "3-iteration ceiling") {
		t.Errorf("ending = %q", ending)
	}
	st, _ := d.ReadState()
	if st.Iteration != 3 {
		t.Errorf("ran %d iterations, want 3", st.Iteration)
	}
}

func TestWorkerProcessFailureEndsTheRun(t *testing.T) {
	d := newRun(t)
	// Unlike a reviewer failure, a broken worker has no budget: it will not fix
	// itself, and every retry costs a full iteration.
	a := &fakeAgent{
		worker: []turn{{result: agent.Result{ExitCode: 1, Stderr: "API key auth failed"}}},
	}
	status, ending, err := Run(context.Background(), cfg(d, a))
	if err != nil {
		t.Fatal(err)
	}
	if status != run.StatusFailed {
		t.Errorf("status = %q, want failed", status)
	}
	if !strings.Contains(ending, "agent process failed") {
		t.Errorf("ending = %q", ending)
	}
	logged, _ := os.ReadFile(d.LogFile())
	if !strings.Contains(string(logged), "API key auth failed") {
		t.Error("the stderr that explains the failure should reach log.md")
	}
}

func TestWorkerDispatchErrorEndsTheRun(t *testing.T) {
	d := newRun(t)
	a := &fakeAgent{worker: []turn{{err: errors.New("pi is not on PATH")}}}
	status, ending, err := Run(context.Background(), cfg(d, a))
	if err != nil {
		t.Fatal(err)
	}
	if status != run.StatusFailed {
		t.Errorf("status = %q, want failed", status)
	}
	if !strings.Contains(ending, "not on PATH") {
		t.Errorf("ending should name the cause, got %q", ending)
	}
}

func TestTaskIsRereadEveryIteration(t *testing.T) {
	d := newRun(t)
	// Editing task.md mid-run is the documented way to change a run's course.
	// If the task were captured once, that would silently not work.
	a := &fakeAgent{
		worker: []turn{
			{
				result: agent.Result{Text: "first", Duration: time.Second},
				before: func() {
					_ = os.WriteFile(d.TaskFile(), []byte("# Task\n\nActually, make the linter clean.\n"), 0o644)
				},
			},
			ok("second"),
		},
		reviewer: []turn{ok("NEEDS_WORK"), ok("PASS")},
	}
	if _, _, err := Run(context.Background(), cfg(d, a)); err != nil {
		t.Fatal(err)
	}
	if len(a.workerCalls) != 2 {
		t.Fatalf("expected 2 iterations, got %d", len(a.workerCalls))
	}
	if !strings.Contains(a.workerCalls[1].Prompt, "make the linter clean") {
		t.Error("the second iteration did not pick up the edited task")
	}
	if !strings.Contains(a.reviewerCalls[1].Prompt, "make the linter clean") {
		t.Error("the reviewer should judge against the current task")
	}
}

func TestUnrunnableTaskIsRefused(t *testing.T) {
	t.Setenv("HARNESS_HOME", t.TempDir())
	d := run.Open("demo")
	// An untouched template: not blank, but not a task either.
	if err := d.Create(t.TempDir(), "fake", "", ""); err != nil {
		t.Fatal(err)
	}
	a := &fakeAgent{worker: []turn{ok("should never run")}}
	_, _, err := Run(context.Background(), cfg(d, a))
	if err == nil {
		t.Fatal("expected a refusal to start")
	}
	if len(a.workerCalls) != 0 {
		t.Error("an unfilled template must not be handed to an agent")
	}
}

func TestSessionIdsAreDistinctPerIterationAndRole(t *testing.T) {
	d := newRun(t)
	a := &fakeAgent{
		worker:   []turn{ok("a"), ok("b")},
		reviewer: []turn{ok("NEEDS_WORK"), ok("PASS")},
	}
	if _, _, err := Run(context.Background(), cfg(d, a)); err != nil {
		t.Fatal(err)
	}
	// Cold sessions per iteration are the design; colliding ids would silently
	// resume the previous one and reintroduce the memory this avoids.
	seen := map[string]bool{}
	for _, r := range append(append([]agent.Request{}, a.workerCalls...), a.reviewerCalls...) {
		if r.SessionID == "" {
			t.Error("every invocation should have a session id")
		}
		if seen[r.SessionID] {
			t.Errorf("duplicate session id %q", r.SessionID)
		}
		seen[r.SessionID] = true
	}
	if a.workerCalls[0].SessionID != "demo-001" {
		t.Errorf("worker session id = %q, want demo-001", a.workerCalls[0].SessionID)
	}
	if a.reviewerCalls[0].SessionID != "demo-review-001" {
		t.Errorf("review session id = %q", a.reviewerCalls[0].SessionID)
	}
}

func TestReviewerGetsTheEvaluatorPersonaAndReadOnlyTools(t *testing.T) {
	d := newRun(t)
	a := &fakeAgent{worker: []turn{ok("a")}, reviewer: []turn{ok("PASS")}}
	if _, _, err := Run(context.Background(), cfg(d, a)); err != nil {
		t.Fatal(err)
	}
	r := a.reviewerCalls[0]
	if r.SystemPrompt == "" {
		t.Error("the reviewer must run under the evaluator persona")
	}
	// A reviewer that can edit is not an independent reviewer.
	for _, tool := range r.Tools {
		if tool == "edit" || tool == "write" {
			t.Errorf("the reviewer must not have %q", tool)
		}
	}
	if r.Timeout < time.Minute {
		t.Error("the reviewer needs a budget covering a cold build")
	}
}

func TestSeparateReviewerAgentIsUsed(t *testing.T) {
	d := newRun(t)
	worker := &fakeAgent{name: "worker", worker: []turn{ok("did it")}}
	reviewer := &fakeAgent{name: "reviewer", reviewer: []turn{ok("PASS")}}
	c := cfg(d, worker)
	c.Reviewer = reviewer
	if _, _, err := Run(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(reviewer.reviewerCalls) != 1 {
		t.Error("the configured reviewer should have been used")
	}
	if len(worker.reviewerCalls) != 0 {
		t.Error("the worker agent should not have been asked to review")
	}
}

func TestContextCancellationStopsTheRun(t *testing.T) {
	d := newRun(t)
	ctx, cancel := context.WithCancel(context.Background())
	a := &fakeAgent{
		worker: []turn{{
			result: agent.Result{Text: "worked", Duration: time.Second},
			before: cancel,
		}},
		reviewer: []turn{ok("NEEDS_WORK")},
	}
	status, _, err := Run(ctx, cfg(d, a))
	if err != nil {
		t.Fatal(err)
	}
	if status != run.StatusStopped {
		t.Errorf("status = %q, want stopped", status)
	}
}

func TestObserverSeesThePhasesAndVerdict(t *testing.T) {
	d := newRun(t)
	var phases []string
	var verdicts []run.Verdict
	var activity int
	var endedWith run.Status
	c := cfg(d, &fakeAgent{
		worker:   []turn{ok("a")},
		reviewer: []turn{ok("PASS")},
	})
	c.Observer = Observer{
		OnPhase:    func(p Phase, i int) { phases = append(phases, fmt.Sprintf("%s/%d", p, i)) },
		OnActivity: func(agent.Event) { activity++ },
		OnVerdict:  func(_ int, v run.Verdict, _ string) { verdicts = append(verdicts, v) },
		OnEnded:    func(s run.Status, _ string) { endedWith = s },
	}
	if _, _, err := Run(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	want := []string{"working/1", "reviewing/1"}
	if len(phases) != 2 || phases[0] != want[0] || phases[1] != want[1] {
		t.Errorf("phases = %v, want %v", phases, want)
	}
	if len(verdicts) != 1 || verdicts[0] != run.VerdictPass {
		t.Errorf("verdicts = %v", verdicts)
	}
	if activity != 2 {
		t.Errorf("activity events = %d, want 2 (one per invocation)", activity)
	}
	if endedWith != run.StatusDone {
		t.Errorf("ended with %q", endedWith)
	}
}

func TestNilObserverIsSafe(t *testing.T) {
	d := newRun(t)
	a := &fakeAgent{worker: []turn{ok("a")}, reviewer: []turn{ok("PASS")}}
	// Config.Observer zero value has all-nil callbacks; the loop must not care.
	if _, _, err := Run(context.Background(), cfg(d, a)); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactsAreWritten(t *testing.T) {
	d := newRun(t)
	a := &fakeAgent{
		worker:   []turn{ok("a"), ok("b")},
		reviewer: []turn{ok("not yet\n\nNEEDS_WORK"), ok("good\n\nPASS")},
	}
	if _, _, err := Run(context.Background(), cfg(d, a)); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		data, err := os.ReadFile(d.ReviewFile(i))
		if err != nil {
			t.Fatalf("review %d missing: %v", i, err)
		}
		if !strings.Contains(string(data), "verdict:") {
			t.Errorf("review %d should record the verdict in its header", i)
		}
	}
	logged, _ := os.ReadFile(d.LogFile())
	body := string(logged)
	for _, want := range []string{"Run started.", "Iteration 1: NEEDS_WORK", "Iteration 2: PASS", "Run ended"} {
		if !strings.Contains(body, want) {
			t.Errorf("log.md missing %q", want)
		}
	}
	st, _ := d.ReadState()
	if len(st.History) != 2 {
		t.Fatalf("history should have 2 entries, got %d", len(st.History))
	}
	if st.History[0].Verdict != run.VerdictNeedsWork || st.History[1].Verdict != run.VerdictPass {
		t.Errorf("history verdicts = %+v", st.History)
	}
	if st.History[0].WorkerTokens != 100 || st.History[0].ReviewTools != 2 {
		t.Errorf("history should record cost: %+v", st.History[0])
	}
}

func TestVerdictOf(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want run.Verdict
	}{
		{"bare pass", "PASS", run.VerdictPass},
		{"bare needs work", "NEEDS_WORK", run.VerdictNeedsWork},
		{"pass on the last line after prose", "I checked everything.\n\nPASS", run.VerdictPass},
		{"needs work on the last line", "The retry path is untested.\n\nNEEDS_WORK", run.VerdictNeedsWork},
		{
			// Models routinely weigh both words before committing, which is why
			// the scan runs backwards from the end rather than forwards.
			"discusses pass then concludes needs work",
			"I considered whether this is a PASS, but the tests were weakened.\n\nNEEDS_WORK",
			run.VerdictNeedsWork,
		},
		{
			"discusses needs work then concludes pass",
			"This looked like NEEDS_WORK at first.\n\nPASS",
			run.VerdictPass,
		},
		{"lowercase", "pass", run.VerdictPass},
		{"trailing whitespace", "PASS\n\n  \n", run.VerdictPass},
		// Everything below is unreadable, and must fail safe toward more work.
		// A false PASS ships unfinished work; a false NEEDS_WORK costs one
		// iteration. These are not symmetric.
		{"empty", "", run.VerdictNeedsWork},
		{"no verdict at all", "I could not determine the state of this.", run.VerdictNeedsWork},
		{"both words on one line", "PASS or NEEDS_WORK?", run.VerdictNeedsWork},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := VerdictOf(tc.text); got != tc.want {
				t.Errorf("VerdictOf(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

func TestLastLine(t *testing.T) {
	if got := lastLine("startup noise\nwarning: x\nthe real error"); got != "the real error" {
		t.Errorf("lastLine = %q", got)
	}
	if got := lastLine("boom\n\n\n"); got != "boom" {
		t.Errorf("lastLine should skip trailing blanks, got %q", got)
	}
	if got := lastLine("   "); got != "no output" {
		t.Errorf("lastLine = %q, want a placeholder", got)
	}
}

// TestCancelDuringReviewIsNotAnError reproduces a real interruption: the user
// stopped a run while its reviewer was mid-flight.
//
// Nothing was measured, so recording ERROR is wrong twice over — it spends a
// third of the error budget, and the next iteration is told its reviewer is
// broken when the work was actually complete. This is the same bug the earlier
// pi harness had.
func TestCancelDuringReviewIsNotAnError(t *testing.T) {
	d := newRun(t)
	ctx, cancel := context.WithCancel(context.Background())
	a := &fakeAgent{
		worker: []turn{ok("did the work")},
		reviewer: []turn{{
			// The kill makes the reviewer exit non-zero with no text, exactly
			// as a killed subprocess does.
			result: agent.Result{ExitCode: 1, Stderr: "signal: killed"},
			before: cancel,
		}},
	}
	status, ending, err := Run(ctx, cfg(d, a))
	if err != nil {
		t.Fatal(err)
	}
	if status != run.StatusStopped {
		t.Errorf("status = %q, want stopped — a cancelled review is not a failure", status)
	}
	if ending != "cancelled" {
		t.Errorf("ending = %q, want cancelled", ending)
	}
	st, _ := d.ReadState()
	if st.Verdict == run.VerdictError {
		t.Error("a cancelled review must not be recorded as a reviewer error")
	}
	if len(st.History) != 0 {
		t.Errorf("an unmeasured iteration must not enter the history: %+v", st.History)
	}
}

// TestCancelDuringWorkIsNotAFailure is the same point for the worker: killing
// it makes it exit non-zero, and that must not be reported as "the agent
// process failed".
func TestCancelDuringWorkIsNotAFailure(t *testing.T) {
	d := newRun(t)
	ctx, cancel := context.WithCancel(context.Background())
	a := &fakeAgent{
		worker: []turn{{
			result: agent.Result{ExitCode: 1, Stderr: "signal: killed"},
			before: cancel,
		}},
	}
	status, ending, err := Run(ctx, cfg(d, a))
	if err != nil {
		t.Fatal(err)
	}
	if status != run.StatusStopped {
		t.Errorf("status = %q, want stopped", status)
	}
	if strings.Contains(ending, "failed") {
		t.Errorf("ending = %q — a cancelled worker is not a fault", ending)
	}
}

// TestCancelledRunStillUnwinds: whatever else happens, an interrupted run must
// leave a truthful record. A stale pid or a "running" status is what makes a
// listing lie about a machine that went to sleep.
func TestCancelledRunStillUnwinds(t *testing.T) {
	d := newRun(t)
	ctx, cancel := context.WithCancel(context.Background())
	a := &fakeAgent{
		worker: []turn{{result: agent.Result{Text: "x", Duration: time.Second}, before: cancel}},
	}
	if _, _, err := Run(ctx, cfg(d, a)); err != nil {
		t.Fatal(err)
	}
	st, _ := d.ReadState()
	if st.PID != 0 {
		t.Errorf("pid = %d, want cleared", st.PID)
	}
	if st.Status == run.StatusRunning {
		t.Error("a cancelled run must not be left recorded as running")
	}
	if st.Ending == "" {
		t.Error("state should say why the run ended")
	}
}
