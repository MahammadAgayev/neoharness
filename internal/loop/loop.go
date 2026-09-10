// Package loop drives a run: prompt an agent, have an independent reviewer
// judge the result, repeat until it passes or the run gives up.
//
// Nothing here imports an adapter, tmux, or a TUI. The loop takes an
// agent.Agent and a set of callbacks, which is what lets all of its behaviour
// be tested against a scripted fake in milliseconds. That split is the most
// valuable structural property in this codebase and there is a test asserting
// it stays true.
package loop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MahammadAgayev/harness/internal/agent"
	"github.com/MahammadAgayev/harness/internal/prompts"
	"github.com/MahammadAgayev/harness/internal/run"
)

// Defaults for a run. Exported so the CLI can show them and tests can shrink
// them.
const (
	// MaxIterations is where a run gives up without a PASS. High enough that
	// hitting it means the task was wrong, not that the budget was mean.
	MaxIterations = 40
	// MaxReviewErrors is how many consecutive reviewer dispatch failures end a
	// run. A reviewer that crashed is not a reviewer that said no: nothing was
	// measured, so iterating on it produces no signal at all.
	MaxReviewErrors = 3
	// ReviewTimeout has to cover a cold build of whatever it is pointed at,
	// because the evaluator is told to build the project and run its tests. Ten
	// minutes did not: one real run had a 2741s worker and a reviewer killed
	// mid-build with nothing measured.
	ReviewTimeout = time.Hour
	// WorkerTimeout is deliberately generous. A worker that is genuinely
	// working for hours is doing its job; the ceiling exists to catch a wedge,
	// not to hurry it.
	WorkerTimeout = 4 * time.Hour
)

// Tool allowlists. Fixed, and free of anything that blocks on a human, since by
// definition nobody is watching.
var (
	WorkerTools = []string{"read", "bash", "edit", "write", "grep", "find", "ls"}
	// The reviewer gets bash because it is told to build and test the project.
	// It does not get edit or write: a reviewer that fixes what it finds is no
	// longer an independent reviewer.
	ReviewTools = []string{"read", "grep", "find", "ls", "bash"}
)

// Phase is what the loop is doing, for the display.
type Phase string

const (
	PhaseWorking   Phase = "working"
	PhaseReviewing Phase = "reviewing"
	PhaseDone      Phase = "done"
)

// Observer receives everything worth showing a human. Every method may be nil.
//
// The loop reports rather than renders. A TUI implements this; so does a plain
// line printer; so does a test.
type Observer struct {
	// Phase changed.
	OnPhase func(p Phase, iteration int)
	// Activity is one thing the agent did — a tool call. High frequency and
	// low value individually: intended to be shown as a single replaced line,
	// never accumulated.
	OnActivity func(e agent.Event)
	// Verdict is a completed review.
	OnVerdict func(iteration int, v run.Verdict, findings string)
	// Ended is the run finishing, with the reason.
	OnEnded func(status run.Status, ending string)
}

func (o Observer) phase(p Phase, i int) {
	if o.OnPhase != nil {
		o.OnPhase(p, i)
	}
}

func (o Observer) activity(e agent.Event) {
	if o.OnActivity != nil {
		o.OnActivity(e)
	}
}

func (o Observer) verdict(i int, v run.Verdict, f string) {
	if o.OnVerdict != nil {
		o.OnVerdict(i, v, f)
	}
}

func (o Observer) ended(s run.Status, e string) {
	if o.OnEnded != nil {
		o.OnEnded(s, e)
	}
}

// Config is one run's worth of settings.
type Config struct {
	Dir   run.Dir
	Agent agent.Agent
	// Reviewer defaults to Agent when nil. Separate because the reviewer's
	// independence is the whole point, and running it on a different model —
	// or eventually a different CLI — is a reasonable thing to want.
	Reviewer        agent.Agent
	MaxIterations   int
	MaxReviewErrors int
	WorkerTimeout   time.Duration
	ReviewTimeout   time.Duration
	Observer        Observer
}

func (c *Config) withDefaults() {
	if c.Reviewer == nil {
		c.Reviewer = c.Agent
	}
	if c.MaxIterations == 0 {
		c.MaxIterations = MaxIterations
	}
	if c.MaxReviewErrors == 0 {
		c.MaxReviewErrors = MaxReviewErrors
	}
	if c.WorkerTimeout == 0 {
		c.WorkerTimeout = WorkerTimeout
	}
	if c.ReviewTimeout == 0 {
		c.ReviewTimeout = ReviewTimeout
	}
}

// Run drives the loop until it ends, and returns why.
//
// It returns an error only for conditions that stop a run before it starts — an
// unreadable task, a missing state file. A run that started and then failed
// ends with a status and an explanation, both recorded in state.json, because
// that is what the dashboard has to display afterwards.
func Run(ctx context.Context, cfg Config) (run.Status, string, error) {
	cfg.withDefaults()
	d := cfg.Dir

	task, err := d.Task()
	if err != nil {
		return run.StatusFailed, "", fmt.Errorf("reading task: %w", err)
	}
	if !run.Runnable(task) {
		return run.StatusFailed, "", fmt.Errorf("no runnable task at %s — write one first", d.TaskFile())
	}
	state, err := d.ReadState()
	if err != nil {
		return run.StatusFailed, "", err
	}

	ev := prompts.LoadEvaluator()
	reviewErrors := 0
	status, ending := run.StatusFailed, "the loop exited without saying why"

	_ = d.AppendLog("Run started.")
	state.Status = run.StatusRunning
	state.PID = processID()
	state.Ending = ""
	_ = d.WriteState(state)

	for {
		iteration := state.Iteration + 1

		// Checked before the work, so a stop lands promptly rather than after
		// another multi-minute iteration.
		if d.StopRequested() {
			status, ending = run.StatusStopped, "stopped on request"
			break
		}
		if ctx.Err() != nil {
			status, ending = run.StatusStopped, "cancelled"
			break
		}
		if iteration > cfg.MaxIterations {
			status = run.StatusFailed
			ending = fmt.Sprintf("hit the %d-iteration ceiling without a PASS", cfg.MaxIterations)
			break
		}

		// Re-read every iteration: editing task.md mid-run is how a run is
		// steered, and this is the line that makes that true.
		task, err = d.Task()
		if err != nil {
			status, ending = run.StatusFailed, "task.md became unreadable"
			break
		}

		cfg.Observer.phase(PhaseWorking, iteration)
		worker, err := cfg.Agent.Run(ctx, agent.Request{
			Prompt: prompts.Worker(prompts.WorkerInput{
				Name:      d.Name,
				Iteration: iteration,
				Task:      task,
				RunDir:    d.Path,
				NotesFile: d.NotesFile(),
				LogFile:   d.LogFile(),
				Verdict:   string(state.Verdict),
				Findings:  state.Findings,
			}),
			Cwd:        state.Cwd,
			SessionID:  fmt.Sprintf("%s-%03d", d.Name, iteration),
			SessionDir: d.SessionsDir(),
			Tools:      WorkerTools,
			Timeout:    cfg.WorkerTimeout,
			OnEvent:    cfg.Observer.activity,
		})
		if err != nil {
			_ = d.AppendLog("The agent could not be run: %v", err)
			status, ending = run.StatusFailed, "the agent could not be run: "+err.Error()
			break
		}
		if worker.ExitCode != 0 {
			_ = d.AppendLog("The agent process failed: %s", lastLine(worker.Stderr))
			status, ending = run.StatusFailed, "the agent process failed"
			break
		}
		// A stop that arrived during a long iteration should not be spent on a
		// review the user is no longer waiting for.
		if d.StopRequested() {
			status, ending = run.StatusStopped, "stopped on request"
			break
		}

		cfg.Observer.phase(PhaseReviewing, iteration)
		review, err := cfg.Reviewer.Run(ctx, agent.Request{
			Prompt: prompts.Review(prompts.ReviewInput{
				Iteration: iteration,
				Task:      task,
				Cwd:       state.Cwd,
				LogFile:   d.LogFile(),
				NotesFile: d.NotesFile(),
			}),
			SystemPrompt: ev.SystemPrompt,
			Model:        ev.Model,
			Cwd:          state.Cwd,
			SessionID:    fmt.Sprintf("%s-review-%03d", d.Name, iteration),
			SessionDir:   d.SessionsDir(),
			Tools:        ReviewTools,
			Timeout:      cfg.ReviewTimeout,
			OnEvent:      cfg.Observer.activity,
		})

		var verdict run.Verdict
		var findings string
		switch {
		case err != nil:
			verdict = run.VerdictError
			findings = "The reviewer could not be run.\n\n" + err.Error()
			reviewErrors++
		case !review.OK():
			// Includes a clean exit with no text. A reviewer that said nothing
			// measured nothing and must not be parsed into a verdict.
			verdict = run.VerdictError
			findings = "The reviewer could not be run.\n\n" + fallback(review.Stderr, "no output")
			reviewErrors++
		default:
			verdict = VerdictOf(review.Text)
			findings = review.Text
			reviewErrors = 0
		}

		header := fmt.Sprintf("- verdict: %s\n- took: %ds\n- tool calls: %d\n- tokens: %d",
			verdict, int(review.Duration.Seconds()), review.Tools, review.Tokens)
		_ = d.WriteReview(iteration, header, findings)

		state.Iteration = iteration
		state.Verdict = verdict
		state.Findings = findings
		state.History = append(state.History, run.Iteration{
			N:             iteration,
			Verdict:       verdict,
			At:            time.Now().UTC(),
			WorkerSeconds: int(worker.Duration.Seconds()),
			WorkerTokens:  worker.Tokens,
			WorkerTools:   worker.Tools,
			ReviewSeconds: int(review.Duration.Seconds()),
			ReviewTools:   review.Tools,
		})
		_ = d.WriteState(state)
		_ = d.AppendLog("Iteration %d: %s (%ds, %d tool calls)",
			iteration, verdict, int(review.Duration.Seconds()), review.Tools)
		cfg.Observer.verdict(iteration, verdict, findings)

		if verdict == run.VerdictPass {
			status, ending = run.StatusDone, "the reviewer confirmed it is finished"
			break
		}
		if reviewErrors >= cfg.MaxReviewErrors {
			status = run.StatusFailed
			ending = fmt.Sprintf("the reviewer could not be dispatched %d times in a row", reviewErrors)
			break
		}
	}

	_ = d.AppendLog("Run ended: %s", ending)
	state.Status = status
	state.Ending = ending
	// Clearing the pid is what makes the record trustworthy. A run that reaches
	// here unwound properly; one that was killed leaves its pid behind, which is
	// how a reader tells "stopped on purpose" from "the laptop lid closed".
	state.PID = 0
	_ = d.WriteState(state)
	cfg.Observer.ended(status, ending)
	return status, ending, nil
}

// VerdictOf reads a verdict off the reviewer's reply.
//
// Scanned from the end: the contract asks for the verdict on the last line, and
// models routinely discuss both words before committing to one.
//
// Anything unreadable is NEEDS_WORK. The failure mode here has to be "keeps
// working", never "stops early and claims success" — a false PASS ships
// unfinished work and ends the run, while a false NEEDS_WORK costs one
// iteration. These are not symmetric.
func VerdictOf(text string) run.Verdict {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		upper := strings.ToUpper(lines[i])
		// NEEDS_WORK is checked first because it contains no "PASS", but a line
		// mentioning both should be read as the cautious one.
		if strings.Contains(upper, "NEEDS_WORK") {
			return run.VerdictNeedsWork
		}
		if strings.Contains(upper, "PASS") {
			return run.VerdictPass
		}
	}
	return run.VerdictNeedsWork
}

func fallback(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}

// lastLine picks the most informative part of a stderr dump for a log line:
// the final non-empty line, where the actual error usually is.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return "no output"
}
