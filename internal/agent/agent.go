// Package agent is the boundary between the harness loop and whichever coding
// CLI actually does the work.
//
// The loop must be testable without spawning a model, and adding a second CLI
// must not touch the loop at all. Both fall out of one rule: nothing in this
// package knows what a "pi" or a "claude" is. Adapters live in subpackages and
// are handed to the loop as an Agent.
//
// The Event set is deliberately the intersection of what the supported CLIs can
// report, not the union. A field that only one of them can fill would grow a
// "does this agent support X" question at every call site, and the answer would
// be load-bearing for the display. Anything an adapter cannot express it simply
// never emits.
package agent

import (
	"context"
	"time"
)

// Kind classifies an Event. Small and closed on purpose: these are the things
// worth showing a human watching a run, and every adapter can produce all of
// them from its own stream.
type Kind int

const (
	// KindToolStart is one tool invocation beginning. The bulk of the stream.
	KindToolStart Kind = iota
	// KindAssistantText is prose the model emitted — a completed message, not a
	// token delta. Adapters drop the per-token events; a run that is watched
	// through a 5-second poll has no use for them and they are 90% of the bytes.
	KindAssistantText
	// KindTurnEnd is one model turn finished (a response plus its tool calls).
	KindTurnEnd
	// KindError is the agent reporting a failure inside an otherwise live stream.
	// A process that dies is a Result error instead.
	KindError
)

func (k Kind) String() string {
	switch k {
	case KindToolStart:
		return "tool"
	case KindAssistantText:
		return "text"
	case KindTurnEnd:
		return "turn"
	case KindError:
		return "error"
	}
	return "unknown"
}

// Event is one thing that happened during a run, already normalised.
type Event struct {
	Kind Kind
	// Tool is the tool name, for KindToolStart.
	Tool string
	// Detail is the interesting argument to that tool — a path, a command, a
	// pattern — flattened to a single line. Which argument that is varies by
	// tool and by CLI, so picking it is the adapter's job, not the display's.
	Detail string
	// Text is prose, for KindAssistantText and KindError.
	Text string
	// Tokens is cumulative total tokens as last reported. Providers report this
	// at unpredictable points and some only at the end, so it is monotonic
	// rather than incremental: see Result.Tokens.
	Tokens int
}

// Request is one agent invocation. It is a full description of the turn because
// the harness starts a cold session every iteration — there is no carried state
// for an adapter to lean on.
type Request struct {
	// Prompt is the user-role instruction for this turn.
	Prompt string
	// SystemPrompt overrides the CLI's own system prompt. Used for the reviewer,
	// which is a different persona from the worker; empty for the worker.
	SystemPrompt string
	// Model pins a specific model. Empty means the CLI's default.
	Model string
	// Cwd is the directory the agent operates on — the tree under work, which is
	// not where the run folder lives.
	Cwd string
	// SessionID and SessionDir control where the CLI writes its transcript.
	// Kept even though every session is cold: the transcript of the iteration
	// that went wrong is the thing worth reading afterwards.
	SessionID  string
	SessionDir string
	// Tools is the allowlist of tool names. Nothing that blocks on a human,
	// since by definition nobody is watching.
	Tools []string
	// Timeout bounds the whole invocation. Zero means no limit.
	Timeout time.Duration
	// OnEvent receives events as they arrive. Called from the adapter's read
	// goroutine, one at a time, never concurrently. May be nil.
	OnEvent func(Event)
}

// Result is what one invocation produced, and what it cost.
type Result struct {
	// Text is the agent's final prose. For the reviewer this carries the
	// verdict, which makes it the single most consequential field here.
	Text string
	// Tools is the number of tool calls made.
	Tools int
	// Tokens is the highest cumulative token count seen.
	Tokens int
	// Duration is wall-clock time for the invocation.
	Duration time.Duration
	// ExitCode is the process exit status; non-zero means the invocation failed
	// and Text must not be trusted.
	ExitCode int
	// Stderr is captured standard error, for reporting a failure. Trimmed by the
	// adapter to something loggable.
	Stderr string
	// TimedOut reports that the invocation was killed by Request.Timeout rather
	// than exiting on its own.
	TimedOut bool
}

// OK reports whether the invocation produced a usable answer.
//
// Empty text counts as failure even on a clean exit. An agent that said nothing
// measured nothing, and for the reviewer in particular an empty reply must
// never be parsed into a verdict — verdict parsing defaults to "keep working",
// so silence would otherwise read as a real judgement.
func (r Result) OK() bool {
	return r.ExitCode == 0 && !r.TimedOut && r.Text != ""
}

// Agent runs one turn and reports what happened.
//
// Implementations must be safe to call sequentially with the same value; the
// loop reuses one Agent for every iteration of a run.
type Agent interface {
	// Run executes one turn. It returns an error only when the invocation could
	// not be carried out at all (the binary is missing, the context was
	// cancelled). A model that ran and failed is a Result with a non-zero
	// ExitCode, not an error — the loop treats those differently.
	Run(ctx context.Context, req Request) (Result, error)
	// Name identifies the adapter, for state.json and the display.
	Name() string
}
