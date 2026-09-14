package prompts

import (
	"strings"
	"testing"
)

func TestLoadEvaluatorStripsFrontmatter(t *testing.T) {
	ev := LoadEvaluator()
	if strings.HasPrefix(ev.SystemPrompt, "---") {
		t.Error("frontmatter should not reach the model as part of the system prompt")
	}
	if !strings.Contains(ev.SystemPrompt, "You decide whether a piece of work is genuinely finished") {
		t.Errorf("system prompt looks wrong:\n%.200s", ev.SystemPrompt)
	}
}

func TestLoadEvaluatorPinsAModel(t *testing.T) {
	// The reviewer is the only check on a run, so it is pinned rather than
	// inheriting whatever the worker happened to be using.
	if got := LoadEvaluator().Model; got == "" {
		t.Error("the evaluator should pin a model in its frontmatter")
	}
}

// TestEvaluatorKeepsItsCriticalInstructions guards the parts of the persona
// that make the verdict trustworthy. These are easy to lose in an edit and
// their absence is invisible until a run ships unfinished work.
func TestEvaluatorKeepsItsCriticalInstructions(t *testing.T) {
	body := LoadEvaluator().SystemPrompt
	for _, want := range []string{
		"NEEDS_WORK", // the pessimistic default
		"Never edit", // a reviewer that fixes things is not independent
		"PASS",       // the terminating verdict
	} {
		if !strings.Contains(body, want) {
			t.Errorf("evaluator persona no longer mentions %q", want)
		}
	}
}

func workerInput() WorkerInput {
	return WorkerInput{
		Name:          "demo",
		Iteration:     3,
		Task:          "# Task\n\nMake the build green.\n",
		MaxIterations: 40,
		RunDir:        "/home/u/.harness/demo",
		NotesFile:     "/home/u/.harness/demo/notes.md",
		LogFile:       "/home/u/.harness/demo/log.md",
	}
}

func TestWorkerStatesTheTaskAndPaths(t *testing.T) {
	got := Worker(workerInput())
	for _, want := range []string{
		"iteration 3",
		"Make the build green.",
		"/home/u/.harness/demo",
		"/home/u/.harness/demo/log.md",
		"/home/u/.harness/demo/notes.md",
		"Completed iterations: 2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("worker prompt missing %q", want)
		}
	}
}

func TestWorkerPushesTheNotesChannelHard(t *testing.T) {
	// An earlier design mentioned the note channel in passing and agents
	// ignored it almost entirely, which left working runs indistinguishable
	// from hung ones. The blunt framing is the fix and must not be softened.
	//
	// The reason changed when the TUI went away — tool calls are now printed,
	// so notes are the record of reasoning rather than the only sign of life —
	// but the instruction still has to be unmissable.
	got := Worker(workerInput())
	for _, want := range []string{
		"explain this run to whoever reads it later",
		"that reasoning is gone",
		"Write intent, not activity",
		"Silence for ten minutes is",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the notes instruction lost %q — agents will stop reporting", want)
		}
	}
}

// TestWorkerDoesNotClaimNobodySeesToolCalls guards against the old wording
// coming back. The harness prints every tool call now; telling the agent
// otherwise is a lie it will reason from.
func TestWorkerDoesNotClaimNobodySeesToolCalls(t *testing.T) {
	got := Worker(workerInput())
	for _, stale := range []string{
		"only thing the user sees",
		"not watching your tool calls",
	} {
		if strings.Contains(got, stale) {
			t.Errorf("the prompt still claims %q, but tool calls are printed", stale)
		}
	}
}

func TestWorkerIncludesFindingsOnNeedsWork(t *testing.T) {
	in := workerInput()
	in.Verdict = "NEEDS_WORK"
	in.Findings = "the retry branch in client.go:88 is never exercised"
	got := Worker(in)
	if !strings.Contains(got, in.Findings) {
		t.Error("reviewer findings must be handed to the next iteration verbatim")
	}
	// Without this, an agent under pressure weakens the test rather than the
	// code — the exact failure the reviewer exists to catch.
	if !strings.Contains(got, "Do not argue with it by making the check weaker") {
		t.Error("missing the instruction against weakening the check")
	}
}

func TestWorkerExplainsAnErrorVerdictIsNotAJudgement(t *testing.T) {
	in := workerInput()
	in.Verdict = "ERROR"
	in.Findings = "the reviewer could not be dispatched"
	got := Worker(in)
	if !strings.Contains(got, "not a verdict on your work") {
		t.Error("an ERROR must be explained as a dispatch failure, not criticism")
	}
	if strings.Contains(got, "Reviewer findings") {
		t.Error("an ERROR must not be presented as findings to address")
	}
}

func TestWorkerOmitsFindingsWhenThereAreNone(t *testing.T) {
	got := Worker(workerInput()) // no verdict: the first iteration
	if strings.Contains(got, "Reviewer findings") {
		t.Error("a first iteration has nothing to address")
	}
	if strings.Contains(got, "could not be run") {
		t.Error("a first iteration had no failed review")
	}
}

func TestWorkerTellsTheAgentItHasNoMemory(t *testing.T) {
	// Sessions are cold every iteration. An agent that assumes continuity
	// re-explores instead of reading its own notes.
	got := Worker(workerInput())
	if !strings.Contains(got, "no memory of earlier iterations") {
		t.Error("the worker must be told its session is cold")
	}
	// Wrapped across two lines in the prompt, so match the distinctive half.
	if !strings.Contains(got, "ends are worth more than successes") {
		t.Error("the log instruction should ask for dead ends, which stop work being redone")
	}
}

func TestWorkerFirstIterationCountsFromZero(t *testing.T) {
	in := workerInput()
	in.Iteration = 1
	got := Worker(in)
	if !strings.Contains(got, "Completed iterations: 0") {
		t.Error("the first iteration has none completed before it")
	}
}

func TestReviewLeadsWithAbsolutePaths(t *testing.T) {
	// Without exact paths a reviewer goes looking: one real run spent 257
	// seconds on `find / -iname log.md` and was killed with nothing measured.
	got := Review(ReviewInput{
		Iteration: 2,
		Task:      "Make the build green.",
		Cwd:       "/home/u/project",
		LogFile:   "/home/u/.harness/demo/log.md",
		NotesFile: "/home/u/.harness/demo/notes.md",
	})
	for _, want := range []string{
		"/home/u/project",
		"/home/u/.harness/demo/log.md",
		"Do not search the filesystem for them",
		"Make the build green.",
		"iteration 2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("review prompt missing %q", want)
		}
	}
	// The paths must come before the task, or a model starts working before it
	// reads them.
	if strings.Index(got, "/home/u/project") > strings.Index(got, "## The task") {
		t.Error("paths must be stated above the task")
	}
}

func TestReviewSaysTheVerdictIsFinal(t *testing.T) {
	got := Review(ReviewInput{Iteration: 1, Task: "x", Cwd: "/p"})
	if !strings.Contains(got, "PASS ships the work as finished") {
		t.Error("the reviewer must be told its verdict alone ends the run")
	}
}

// TestWorkerExplainsTheHarnessItself pins the agent's self-awareness.
//
// Without this section the agent knows where its files are but not what it is
// inside. An agent that does not know a reviewer is coming writes notes for
// nobody, treats findings as an interruption rather than the point, and tries
// to cram the whole task into one turn because it does not know another
// iteration exists.
func TestWorkerExplainsTheHarnessItself(t *testing.T) {
	got := Worker(workerInput())
	for _, want := range []string{
		"run by harness, unattended",   // what is running it
		"Nobody is watching this turn", // why notes matter
		"independent reviewer judges",  // what happens next
		"You do not decide when the run is done",
		"The next iteration is a stranger with your notes",
		"The reviewer did not watch you work",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the agent is no longer told %q", want)
		}
	}
	// The context must come before the task, or it is read as an afterthought.
	if strings.Index(got, "## What this is") > strings.Index(got, "## Task") {
		t.Error("the harness explanation must precede the task")
	}
}

func TestWorkerStatesWhereItIsInTheRun(t *testing.T) {
	in := workerInput()
	in.Iteration, in.MaxIterations = 3, 40
	// "iteration 3 of at most 40" is the difference between "I have room to do
	// this properly" and "this is my last turn".
	if !strings.Contains(Worker(in), "iteration 3 of at most 40") {
		t.Error("the agent should be told its position in the run")
	}
}
