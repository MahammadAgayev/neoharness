// Package prompts builds what the worker and the reviewer are told, and owns
// the evaluator persona.
//
// The evaluator is embedded rather than read from disk. The Python version
// resolved it relative to the script, through a symlink, which meant the
// binary's location was load-bearing; a compiled program has no such excuse.
package prompts

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"
)

//go:embed evaluator.md
var evaluatorMD string

// Evaluator is the reviewer's system prompt and the model it pins.
//
// The frontmatter carries a model because the reviewer's judgement is the only
// check on a run, and it is worth pinning that to a known-good model rather
// than inheriting whatever the worker happened to be using.
type Evaluator struct {
	SystemPrompt string
	Model        string
}

var (
	frontmatterRe = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n`)
	modelRe       = regexp.MustCompile(`(?m)^model:\s*(\S+)`)
)

// LoadEvaluator parses the embedded evaluator definition.
func LoadEvaluator() Evaluator {
	m := frontmatterRe.FindStringSubmatch(evaluatorMD)
	if m == nil {
		return Evaluator{SystemPrompt: evaluatorMD}
	}
	ev := Evaluator{SystemPrompt: strings.TrimPrefix(evaluatorMD, m[0])}
	if mm := modelRe.FindStringSubmatch(m[1]); mm != nil {
		ev.Model = mm[1]
	}
	return ev
}

// WorkerInput is everything the per-iteration instruction depends on.
type WorkerInput struct {
	Name      string
	Iteration int // the one about to run, 1-based
	// MaxIterations is the ceiling. Stated in the prompt so the agent can tell
	// "I have plenty of room to do this properly" from "this is the last turn
	// before the run gives up".
	MaxIterations int
	Task          string
	RunDir        string
	NotesFile     string
	LogFile       string
	// Verdict and Findings describe the previous iteration, if there was one.
	Verdict  string
	Findings string
}

// Worker builds the per-iteration instruction.
//
// The task is re-stated in full every time. The session is cold each iteration,
// so this is the only place the agent sees it — and it is re-read from disk
// first, which is what makes editing task.md mid-run the way to change course.
//
// Nothing is pasted in that the agent could open itself. It is told where the
// run folder is and what is in it; reading log.md is step one. Injecting the
// file would mean a size cap, a truncation policy, and a rule for which end to
// keep — three decisions bought with tokens spent every iteration, to save a
// single tool call.
func Worker(in WorkerInput) string {
	var b strings.Builder
	p := func(format string, args ...any) {
		fmt.Fprintf(&b, format+"\n", args...)
	}

	p("[HARNESS · iteration %d of run %q]", in.Iteration, in.Name)
	p("")
	// What the harness is, stated before the task.
	//
	// Without this the agent knows where its files are but not what it is
	// inside: that a reviewer will judge the work, that a PASS is what ends
	// the run, and that there will be a next iteration with no memory of this
	// one. An agent that does not know it is in a loop writes notes for
	// nobody, treats the reviewer's findings as an interruption, and tries to
	// finish everything in one turn.
	p("## What this is")
	p("")
	p("You are being run by harness, unattended. Nobody is watching this turn.")
	p("")
	p("The loop is: you work → an independent reviewer judges the result →")
	p("either it passes and the run ends, or you get another iteration with the")
	p("reviewer's findings. You are in iteration %d of at most %d.", in.Iteration, in.MaxIterations)
	p("")
	p("Three things follow from that, and they are the whole reason this works:")
	p("")
	p("- **You do not decide when the run is done.** The reviewer does. Do one")
	p("  solid increment and stop; do not rush the whole task into this turn.")
	p("- **The next iteration is a stranger with your notes.** It gets a fresh")
	p("  session and remembers nothing. log.md is the only thing you can hand it.")
	p("- **The reviewer did not watch you work.** It reads the diff and runs the")
	p("  build itself. Anything you did not actually verify, it will find.")
	p("")

	p("## Task")
	p("")
	p("%s", strings.TrimSpace(in.Task))
	p("")
	p("## Where things stand")
	p("")
	p("You have no memory of earlier iterations — each one is a fresh session.")
	p("What you know about this run is what you wrote down.")
	p("")
	p("- Run folder: %s", in.RunDir)
	p("  - task.md     the contract, quoted above")
	p("  - log.md      your own notes, iteration by iteration")
	p("  - notes.md    what the user sees while you work (see below)")
	p("  - reviews/    what the reviewer said each iteration (NNN.md)")
	p("- Completed iterations: %d", in.Iteration-1)
	p("")

	switch in.Verdict {
	case "NEEDS_WORK":
		p("## Reviewer findings")
		p("")
		p("An independent reviewer that did not watch you write this judged the work")
		p("incomplete. It is the only check on this run:")
		p("")
		p("%s", in.Findings)
		p("")
		p("Address this. Do not argue with it by making the check weaker.")
		p("")
	case "ERROR":
		p("## The reviewer could not be run")
		p("")
		p("Nothing was judged last iteration, so this is not a verdict on your work.")
		p("Carry on; the run gives up by itself if it keeps happening.")
		p("")
	}

	// The notes section is deliberately blunt and deliberately early. An
	// earlier design gave the agent a note channel and mentioned it in passing;
	// agents ignored it almost entirely, and a run that is working looks
	// identical to a hung one when its only voice is silent.
	p("## Reporting progress — read this before you start")
	p("")
	p("%s is the only thing the user sees while you work.", in.NotesFile)
	p("They are not watching your tool calls. They are not reading log.md. If you")
	p("do not write here, a working run and a stuck run look exactly the same to")
	p("them.")
	p("")
	p("Append one line to it — do not rewrite the file — whenever any of these")
	p("happen:")
	p("")
	p("- you work out what is actually wrong")
	p("- you decide on an approach, or change your mind about one")
	p("- something fails in a way that matters")
	p("- you finish a piece of the work")
	p("")
	p("Write intent, not activity. \"Auth test fails because the clock is mocked")
	p("after the token is minted\" is worth reading; \"editing auth_test.go\" is not —")
	p("the harness already shows that.")
	p("")
	p("Aim for a handful of lines across the iteration. Silence for ten minutes is")
	p("a bug.")
	p("")

	p("## What to do now")
	p("")
	p("Advance the task by one meaningful increment:")
	p("")
	p("1. Read %s. It is what earlier iterations left you and the only", in.LogFile)
	p("   memory you have of them.")
	p("2. Establish where things stand — build and test the project the way the task")
	p("   says to, or the way the project itself implies.")
	p("3. Pick the single most valuable thing you can finish this iteration.")
	p("4. Do it, and verify it the same way.")
	p("5. Append what happened to %s — what you tried, what the result was,", in.LogFile)
	p("   and anything the next iteration needs. It starts with no memory of this")
	p("   one and has only what you write here: what you leave out is lost. Dead")
	p("   ends are worth more than successes — they stop the work being redone.")
	p("")
	p("Finish your turn with one or two sentences on what you did.")
	p("")
	p("Work only within the task. Do not refactor code it did not ask about. Stop when")
	p("the increment is done — the reviewer decides when the run is finished, not you.")

	return b.String()
}

// ReviewInput is what the reviewer needs to locate and judge the work.
type ReviewInput struct {
	Iteration int
	Task      string
	Cwd       string
	LogFile   string
	NotesFile string
}

// Review builds the reviewer's instruction, on top of the evaluator persona.
//
// The absolute paths at the top are not decoration. Without them a reviewer
// goes looking for the run's files: one real run spent 257 seconds on
// `find / -iname log.md` before being killed with nothing measured. With them,
// the same review took 11.7 seconds and two tool calls.
func Review(in ReviewInput) string {
	var b strings.Builder
	p := func(format string, args ...any) {
		fmt.Fprintf(&b, format+"\n", args...)
	}

	p("Judge whether the work in this repository genuinely satisfies its task.")
	p("")
	p("- Repository under review: %s", in.Cwd)
	p("- The agent's own notes: %s", in.LogFile)
	p("- What it reported as it worked: %s", in.NotesFile)
	p("")
	p("Those paths are exact. Do not search the filesystem for them.")
	p("")
	p("## The task")
	p("")
	p("%s", strings.TrimSpace(in.Task))
	p("")
	p("This is iteration %d of an unattended run. Your verdict alone", in.Iteration)
	p("decides whether it ends, and a PASS ships the work as finished.")

	return b.String()
}
