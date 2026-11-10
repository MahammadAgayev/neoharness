// Package agent is the boundary between the hub and whichever coding CLI
// actually does the work.
//
// The hub must be testable without spawning a model, and adding a second CLI
// must not touch the hub at all. Both fall out of one rule: nothing in this
// package knows what a "claude" is. Adapters live in subpackages and are
// handed to the hub as an Agent.
//
// The Event set is deliberately the intersection of what the supported CLIs can
// report, not the union. A field that only one of them can fill would grow a
// "does this agent support X" question at every call site, and the answer would
// be load-bearing for the display. Anything an adapter cannot express it simply
// never emits.
package agent

import "context"

// Kind classifies an Event. Small and closed on purpose: these are the things
// worth showing a human watching a thread, and every adapter can produce all
// of them from its own stream.
type Kind int

const (
	// KindToolStart is one tool invocation beginning. The bulk of the stream.
	KindToolStart Kind = iota
	// KindAssistantText is prose the model emitted — a completed message, not a
	// token delta. Adapters drop the per-token events; they are 90% of the bytes
	// and nobody reads a reply one token at a time.
	KindAssistantText
	// KindTurnEnd is one model turn finished (a response plus its tool calls).
	KindTurnEnd
	// KindError is the agent reporting a failure inside an otherwise live stream.
	// A process that dies is a Result error instead.
	KindError
	// KindToolFailed is a tool call that failed or was denied: ID is the
	// KindToolStart's, Text the first line of why.
	KindToolFailed
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
	case KindToolFailed:
		return "tool failed"
	}
	return "unknown"
}

// Event is one thing that happened during a turn, already normalised.
type Event struct {
	Kind Kind
	// Tool is the tool name, for KindToolStart.
	Tool string
	// ID ties a KindToolFailed to its KindToolStart.
	ID string
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

// Request is one agent invocation: one turn of a thread.
type Request struct {
	// Prompt is the user-role instruction for this turn.
	Prompt string
	// SystemPrompt is added to the CLI's own system prompt: who this session
	// is in the workspace.
	SystemPrompt string
	// Cwd is the directory the agent operates on.
	Cwd string
	// Conversation is a session the CLI owns and keys by this id. With Resume
	// false the turn creates it; with Resume true the turn continues it, so a
	// thread is one long conversation across turns and restarts.
	Conversation string
	Resume       bool
	// Harness is the neoharness MCP server for this thread. The adapter adds
	// it to the CLI's servers and asks it before a tool call the CLI may not
	// make on its own, so a person can answer without a terminal.
	Harness Harness
	// Model and Effort choose the model and effort level; empty leaves the
	// CLI's default.
	Model  string
	Effort string
	// OnEvent receives events as they arrive. Called from the adapter's read
	// goroutine, one at a time, never concurrently. May be nil.
	OnEvent func(Event)
}

// Harness is an MCP server over HTTP, reached with a bearer token.
type Harness struct {
	URL   string
	Token string
}

// Permission is a question an agent asks before a tool call, in terms the hub
// can show and remember. The adapter fills it from its own tool schema.
type Permission struct {
	Tool string
	// Detail is the interesting argument, on one line.
	Detail string
	// Prefix is what an "allow always" remembers, as a command's first word;
	// empty covers the whole tool.
	Prefix string
	// Compound is a call that does more than one thing, as a chained shell
	// command. A rule never covers it.
	Compound bool
}

// Efforter is an Agent that names the effort levels it takes. The hub checks
// /effort against them; without it, any level goes to the agent.
type Efforter interface {
	Efforts() []string
}

// Result is what one invocation produced, and what it cost.
type Result struct {
	// Text is the agent's final prose.
	Text string
	// Tools is the number of tool calls made.
	Tools int
	// Tokens is the highest cumulative token count seen.
	Tokens int
	// ExitCode is the process exit status; non-zero means the invocation failed
	// and Text must not be trusted.
	ExitCode int
	// Stderr is captured standard error, for reporting a failure. Trimmed by the
	// adapter to something loggable.
	Stderr string
	// Commands is the slash commands the CLI offers in this session, without
	// the `/`. Empty when the CLI did not say.
	Commands []string
	// Model is the model the session runs, as the CLI names it.
	Model string
	// Context is how many tokens the last model call carried, out of
	// ContextWindow: how full the conversation is.
	Context       int
	ContextWindow int
	// Cost is what this invocation cost in US dollars.
	Cost float64
	// Lost says the agent has no record of the conversation the request
	// resumed. The caller starts a new conversation and sends the prompt again.
	Lost bool
}

// Agent runs one turn and reports what happened.
//
// Implementations must be safe to call sequentially with the same value.
type Agent interface {
	// Run executes one turn. It returns an error only when the invocation could
	// not be carried out at all (the binary is missing, the context was
	// cancelled). A model that ran and failed is a Result with a non-zero
	// ExitCode, not an error.
	Run(ctx context.Context, req Request) (Result, error)
}
