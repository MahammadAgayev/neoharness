// Package model holds the plain types shared by the hub, its HTTP API, and
// the TUI: what a workspace, a thread, and a transcript event look like on the
// wire.
//
// It is a leaf with no behaviour beyond parsing and formatting. The TUI depends
// on it instead of on the hub or the storage package, so the UI cannot reach
// past the API into state it does not own.
package model

import (
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// State is where a thread is, as the user sees it.
type State string

const (
	StateIdle     State = "idle"
	StateRunning  State = "running"
	StateWaiting  State = "waiting"
	StateResolved State = "resolved"
	// StateFailed is a thread whose last turn failed, until the person
	// dismisses it or the thread runs again.
	StateFailed State = "failed"
	// StateBackground is a thread whose turn ended while its agent still
	// runs work in the background, such as a shell or a subagent.
	StateBackground State = "background"
)

// Role separates the one coordinator per workspace from its threads. The
// coordinator gets the tools that open and resolve threads; nothing else does.
type Role string

const (
	RoleCoordinator Role = "coordinator"
	RoleThread      Role = "thread"
)

// Coordinator is the coordinator's name and MCP address in every workspace.
const Coordinator = "coordinator"

// User is the sender name for messages typed in the TUI.
const User = "user"

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// ValidName checks a workspace or thread name. Names are file names, MCP
// addresses, and buffer names at once, so they are kept to a slug.
func ValidName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("name %q must be lowercase letters, digits and dashes, at most 40 characters", name)
	}
	return nil
}

// Location is where a thread runs: a local directory, or a directory on an
// ssh host.
type Location struct {
	Host string `json:"host,omitempty"`
	Dir  string `json:"dir"`
}

// Remote reports whether the location is on another machine.
func (l Location) Remote() bool { return l.Host != "" }

func (l Location) String() string {
	if l.Remote() {
		return l.Host + ":" + l.Dir
	}
	return l.Dir
}

// ParseLocation reads "host:dir" or a local path.
//
// A local path never contains a colon before its first slash, so "host:dir"
// is unambiguous for anything a user would type.
func ParseLocation(s string) (Location, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Location{}, errors.New("empty location")
	}
	if i := strings.Index(s, ":"); i > 0 && !strings.Contains(s[:i], "/") && !drivePath(s) {
		host, dir := s[:i], s[i+1:]
		// ssh reads a host that starts with a dash as an option, such as
		// -oProxyCommand, which runs a command here.
		if strings.HasPrefix(host, "-") {
			return Location{}, fmt.Errorf("host %q starts with a dash", host)
		}
		if dir == "" {
			dir = "~"
		}
		return Location{Host: host, Dir: dir}, nil
	}
	return Location{Dir: s}, nil
}

// drivePath reports a Windows path with a drive letter, such as C:\src or
// C:/src. Only on Windows: elsewhere "c:/src" is the folder /src on host c.
func drivePath(s string) bool {
	if runtime.GOOS != "windows" || len(s) < 2 || s[1] != ':' {
		return false
	}
	c := s[0] | 0x20
	return c >= 'a' && c <= 'z' && (len(s) == 2 || s[2] == '\\' || s[2] == '/')
}

// EventKind classifies a transcript entry.
type EventKind string

const (
	// EventMessage is input to a thread: from the user, the coordinator, or a
	// peer. From says who.
	EventMessage EventKind = "message"
	// EventText is prose the agent wrote.
	EventText EventKind = "text"
	// EventTool is one tool call the agent made.
	EventTool EventKind = "tool"
	// EventTurnEnd closes a turn; Tools and Tokens describe it.
	EventTurnEnd EventKind = "turn"
	// EventError is a failed turn or a lost one.
	EventError EventKind = "error"
	// EventPermission is a pending permission request; ID identifies it.
	EventPermission EventKind = "permission"
	// EventAnswer is the user's answer to a permission request.
	EventAnswer EventKind = "answer"
	// EventAsk is a question the agent asked the user.
	EventAsk EventKind = "ask"
	// EventProgress is a progress report.
	EventProgress EventKind = "progress"
	// EventNote is something the hub did to the thread: opened, resolved.
	EventNote EventKind = "note"
	// EventClear starts the conversation again: what came before it is no
	// longer on screen or in Claude's context.
	EventClear EventKind = "clear"
	// EventShell is a command the user ran with `!`: Detail is the command.
	// It comes twice with the same ID, when it starts and when it ends; the
	// second has the output in Text and the exit code.
	EventShell EventKind = "shell"
	// EventArtifact is a document the thread wrote: Detail is its name, Text
	// its title.
	EventArtifact EventKind = "artifact"
	// EventNotice is what the coordinator told the person with notify;
	// NeedsYou says the person must act.
	EventNotice EventKind = "notice"
	// EventTakeBack takes the person's last message, Text, off the screen:
	// they took it back to the prompt.
	EventTakeBack EventKind = "takeback"
)

// Event is one transcript entry.
type Event struct {
	At     time.Time `json:"at"`
	Kind   EventKind `json:"kind"`
	From   string    `json:"from,omitempty"`
	Text   string    `json:"text,omitempty"`
	Tool   string    `json:"tool,omitempty"`
	Detail string    `json:"detail,omitempty"`
	ID     string    `json:"id,omitempty"`
	Tools  int       `json:"tools,omitempty"`
	Tokens int       `json:"tokens,omitempty"`
	Exit   int       `json:"exit,omitempty"`
	// Background is how many background tasks a turn end left running.
	Background int `json:"background,omitempty"`
	// Version is an artifact's version.
	Version  int  `json:"version,omitempty"`
	NeedsYou bool `json:"needsYou,omitempty"`
	// Level is how much of the person's attention the event asks for. The
	// hub sets it once, so every client draws the same thing.
	Level Level `json:"level,omitempty"`
}

// Level is a step of the attention ladder. A client chooses how to draw a
// level, never which level an event has.
type Level int

const (
	// LevelQuiet is the work itself: text, tool calls, progress.
	LevelQuiet Level = iota
	// LevelAmbient changes in place: a new artifact, a thread's turn end
	// when the coordinator decides what reaches the person.
	LevelAmbient
	// LevelNotice may show a short notice, but never while the person types.
	LevelNotice
	// LevelNeedsYou waits for the person: a permission, a question, a
	// failure, a notice that needs them. Being seen does not clear it.
	LevelNeedsYou
)

// SeenDwell is how long a thread must be on screen, in a window in front,
// before a client says it was seen. A glance while moving past is not
// reading.
const SeenDwell = 2 * time.Second

// WaitKind says what a wait is and how it is resolved.
type WaitKind string

const (
	// WaitPermission is answered with allow or deny.
	WaitPermission WaitKind = "permission"
	// WaitQuestion is answered by writing to the thread, or skipped.
	WaitQuestion WaitKind = "question"
	// WaitFailed is a turn that failed; it goes when the thread runs again
	// or the person dismisses it.
	WaitFailed WaitKind = "failed"
	// WaitNotice is a notice that needs the person; it goes when they write
	// to the session or dismiss it.
	WaitNotice WaitKind = "notice"
	// WaitReview is news the person has not read: a turn that ended or a
	// notice. It blocks nothing, so it is not counted as waiting; it goes
	// when they read the thread or dismiss it.
	WaitReview WaitKind = "review"
	// WaitFocus is an edit of the focus the attention agent suggests. It is
	// in model.HubScope, its thread is model.FocusDoc, it blocks nothing, and goes when the
	// person saves the focus or dismisses it.
	WaitFocus WaitKind = "focus"
)

// ReviewID names a thread's review in the waits list; a thread has at most
// one. SuggestionID names the focus's suggested edit; there is at most one.
const (
	ReviewID     = "review"
	SuggestionID = "suggestion"
)

// Wait is one item of the waits list: something that needs the person, in
// any workspace. ID names it to Answer or Dismiss. The list is in the order
// to go through it: what blocks a turn, then failures and notices, then
// reviews, each oldest first.
type Wait struct {
	Workspace string    `json:"workspace"`
	Thread    string    `json:"thread"`
	Kind      WaitKind  `json:"kind"`
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Since     time.Time `json:"since"`
	// Why says in a few words why the attention agent put it where it is,
	// and Brief what it asks and what to do, in two short sentences. Empty
	// without the agent.
	Why   string `json:"why,omitempty"`
	Brief string `json:"brief,omitempty"`
}

// Status is how all the work stands, in every workspace, as the attention
// agent last wrote it: a few short lines, for the person to read when they
// come back.
type Status struct {
	Lines []StatusLine `json:"lines,omitempty"`
	At    time.Time    `json:"at,omitzero"`
}

// StatusLine is the work of one workspace, or of one goal across them:
// Where names it, and Parts say what moved, what is stuck and what runs.
type StatusLine struct {
	Where string   `json:"where,omitempty"`
	Parts []string `json:"parts"`
}

// JournalEntry is one fact of a workspace's journal: what happened to a
// session that matters outside its chat. Kind says what; ID names the
// permission, question or notice it is about; Tools counts a turn's tool
// calls; Level is the attention it asks for, as the hub set it once. Seq
// rises across the hub. Workspace is set when it leaves the hub.
type JournalEntry struct {
	Seq       int64     `json:"seq"`
	At        time.Time `json:"at"`
	Workspace string    `json:"workspace,omitempty"`
	Thread    string    `json:"thread"`
	Kind      string    `json:"kind"`
	ID        string    `json:"id,omitempty"`
	Text      string    `json:"text,omitempty"`
	Tools     int       `json:"tools,omitempty"`
	NeedsYou  bool      `json:"needsYou,omitempty"`
	Level     Level     `json:"level,omitempty"`
}

// The kinds of journal entry.
const (
	JournalOpened     = "opened"
	JournalStarted    = "started" // a turn starts
	JournalTurn       = "turn"    // a turn ended well: Text is its reply, Tools its tool calls
	JournalFailed     = "failed"
	JournalRecap      = "recap"
	JournalDone       = "done" // a todo item
	JournalArtifact   = "artifact"
	JournalAsked      = "asked"
	JournalPaused     = "paused"  // the queue waits for the person: Text says why
	JournalResumed    = "resumed" // the queue goes on, or is empty
	JournalPermission = "permission"
	JournalDecided    = "decided" // a permission's answer
	JournalNotice     = "notice"
	JournalWrote      = "wrote"     // the person wrote to the session
	JournalDismissed  = "dismissed" // the person skipped a question, or dismissed a notice, a failure or news
	JournalSeen       = "seen"
	JournalResolved   = "resolved"
	JournalReopened   = "reopened"
)

// HubScope is where a workspace name goes for a document of the hub, which
// is in no workspace. It is no valid workspace name. FocusDoc is its one
// document: what the person works on now, in every workspace.
const (
	HubScope = "~"
	FocusDoc = "focus"
)

// FocusTemplate is the focus before the person writes one.
const FocusTemplate = "# Focus\n\n## Now\n\n## Keep moving\n\n## Leave alone\n"

// Suggestion is an edit of the focus the attention agent suggests: the
// whole text as it would have it, on version Base, and why in one line.
type Suggestion struct {
	Text string    `json:"text"`
	Why  string    `json:"why"`
	Base int       `json:"base"`
	At   time.Time `json:"at"`
}

// Artifact is a markdown document an agent wrote for the person to read. Its
// text is stored apart from this summary.
type Artifact struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Thread  string `json:"thread"`
	Version int    `json:"version"`
	// UserEdit says the newest version is the person's edit.
	UserEdit bool `json:"userEdit,omitempty"`
	// Off says the person set the focus aside with :focus off, and
	// Suggestion is the attention agent's edit of it that waits for them.
	// Only the focus has them.
	Off        bool        `json:"off,omitempty"`
	Suggestion *Suggestion `json:"suggestion,omitempty"`
	CreatedAt  time.Time   `json:"createdAt"`
	UpdatedAt  time.Time   `json:"updatedAt"`
}

// Merge is what an edit of an artifact becomes when it is merged into the
// newest version, Version. Merged has every change that merged; where both
// sides changed the same lines it keeps the edit's side, and Conflicts says
// where. Clean says there are none.
type Merge struct {
	Merged    string     `json:"merged"`
	Clean     bool       `json:"clean"`
	Version   int        `json:"version"`
	Conflicts []Conflict `json:"conflicts,omitempty"`
}

// Conflict is where both sides changed the same block, as lines: [first,
// end) from 0, in the base, the edit (Ours), the newest version (Theirs) and
// the merged text. A range always holds whole blocks.
type Conflict struct {
	Base   [2]int `json:"base"`
	Ours   [2]int `json:"ours"`
	Theirs [2]int `json:"theirs"`
	Merged [2]int `json:"merged"`
}

// Permission is a request waiting for the user.
type Permission struct {
	ID     string `json:"id"`
	Tool   string `json:"tool"`
	Detail string `json:"detail"`
	// Scope is what "allow always" covers; empty when it is not offered.
	Scope string `json:"scope,omitempty"`
}

// RecapAway is how long the person must be away before the recap shows
// when they come back. A shorter break leaves the work fresh in mind.
const RecapAway = 2 * time.Minute

// ThreadView is a thread as the API shows it.
type ThreadView struct {
	Workspace string   `json:"workspace"`
	Name      string   `json:"name"`
	Role      Role     `json:"role"`
	Location  Location `json:"location"`
	State     State    `json:"state"`
	Queued    int      `json:"queued"`
	// Background is how many tasks the agent runs in the background.
	Background int `json:"background,omitempty"`
	// WakeAt is when the thread asked to be woken; zero is never.
	WakeAt  time.Time `json:"wakeAt,omitzero"`
	Waiting string    `json:"waiting,omitempty"`
	// QuestionID names the thread's open question, when Waiting is one: the
	// person answers it by writing, or skips it with this ID.
	QuestionID string      `json:"questionId,omitempty"`
	Permission *Permission `json:"permission,omitempty"`
	Pending    []Event     `json:"pending,omitempty"` // queued, not yet in the transcript
	// Reply is the start of the last turn's reply; empty while a turn runs,
	// as it is older news then.
	Reply string `json:"reply,omitempty"`
	// Recap is where the session's work stands, in its own words, and
	// RecapAt when it wrote it. It shows above the prompt when the person
	// comes back after RecapAway.
	Recap   string    `json:"recap,omitempty"`
	RecapAt time.Time `json:"recapAt,omitzero"`
	// Todos is the todo list the session keeps while it works, in the
	// order it made the items.
	Todos []Todo `json:"todos,omitempty"`
	// Level is the highest attention level still open on the thread:
	// what waits on the person, or what came since SeenAt.
	Level  Level     `json:"level,omitempty"`
	SeenAt time.Time `json:"seenAt,omitzero"`
	// Quiet says the coordinator decides what of this thread reaches the
	// person: only its questions and permission requests notify them.
	Quiet bool `json:"quiet,omitempty"`
	// Model, Context and ContextWindow are as of the last turn. Branch is
	// the git branch of the thread's folder, empty outside a repository.
	Model         string    `json:"model,omitempty"`
	Context       int       `json:"context,omitempty"`
	ContextWindow int       `json:"contextWindow,omitempty"`
	Branch        string    `json:"branch,omitempty"`
	Since         time.Time `json:"since"`
	UpdatedAt     time.Time `json:"updatedAt"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Todo is one item of a session's todo list.
type Todo struct {
	ID    string    `json:"id"`
	Text  string    `json:"text"`
	State TodoState `json:"state"`
}

// TodoState is where a todo item stands.
type TodoState string

const (
	TodoPending TodoState = "pending"
	TodoActive  TodoState = "active"
	TodoDone    TodoState = "done"
)

// Completions is what the prompt of a thread can complete: `/` commands,
// and after `@` the threads of its workspace and the files in its folder.
type Completions struct {
	Commands []string `json:"commands"`
	Files    []string `json:"files"`
	Threads  []string `json:"threads"`
	// Many says the folder has too many files to send: Files are only those
	// whose path holds Query, and the prompt asks again as its word changes.
	Many  bool   `json:"many,omitempty"`
	Query string `json:"query,omitempty"`
}

// TurnCost is what one turn cost. The cost log of a workspace keeps one per
// paid turn, failed ones too.
type TurnCost struct {
	At        time.Time `json:"at"`
	Workspace string    `json:"workspace,omitempty"`
	Thread    string    `json:"thread"`
	Model     string    `json:"model,omitempty"`
	Cost      float64   `json:"cost"`
	Tools     int       `json:"tools,omitempty"`
}

// CostReport is what a workspace, or every workspace, cost. Total and Threads
// count every turn ever; Days, Models and Top only the turns in the cost
// logs, which began later: Since is the first of them.
type CostReport struct {
	Total   float64    `json:"total"`
	Today   float64    `json:"today"`
	Since   time.Time  `json:"since"`
	Threads []CostPart `json:"threads"`
	// Workspaces is filled only for every workspace.
	Workspaces []CostPart `json:"workspaces"`
	Models     []CostPart `json:"models"`
	// Days are the last 14, oldest first, each as YYYY-MM-DD in local time.
	Days []CostPart `json:"days"`
	// Top are the turns that cost the most, the most first.
	Top []TurnCost `json:"top"`
}

// CostPart is the cost of one thread, workspace, model or day.
type CostPart struct {
	Workspace string  `json:"workspace,omitempty"`
	Name      string  `json:"name"`
	Cost      float64 `json:"cost"`
	Turns     int     `json:"turns,omitempty"`
}

// Choices are what /model and /effort of a thread take, and what it has now.
// Empty Model or Effort is the agent's default.
type Choices struct {
	Model   string        `json:"model"`
	Effort  string        `json:"effort"`
	Models  []ModelChoice `json:"models"`
	Efforts []string      `json:"efforts"`
}

// ModelChoice is one model of /model. Version is the model it runs now.
type ModelChoice struct {
	Name        string   `json:"name"`
	Label       string   `json:"label,omitempty"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Efforts     []string `json:"efforts,omitempty"`
}

// WorkspaceView is a workspace with a summary of its threads.
type WorkspaceView struct {
	Name      string        `json:"name"`
	Pin       string        `json:"pin,omitempty"`
	CreatedAt time.Time     `json:"createdAt"`
	Counts    map[State]int `json:"counts"`
	// Coordinator is the coordinator's state; Counts are the threads'.
	Coordinator State `json:"coordinator,omitempty"`
	// Notices counts the coordinator's notices that wait for the person.
	Notices int `json:"notices,omitempty"`
	// LastUsed is when a thread of the workspace last changed.
	LastUsed time.Time `json:"lastUsed,omitzero"`
	// Activity is turns per hour over the last 24 hours, oldest first.
	Activity []int `json:"activity"`
	// CoordinatorAgent and ThreadsAgent are the agents the roles run here,
	// and AgentNames every agent of the config, for :setlocal.
	CoordinatorAgent string   `json:"coordinatorAgent,omitempty"`
	ThreadsAgent     string   `json:"threadsAgent,omitempty"`
	AgentNames       []string `json:"agentNames,omitempty"`
	// ReopenCompact is what a resolved thread with a full context does when
	// the coordinator reopens it: compact, ask or off.
	ReopenCompact string `json:"reopenCompact,omitempty"`
	// AutoCompact is the context percent at which a session compacts
	// before its next turn; 0 never.
	AutoCompact int `json:"autoCompact"`
	// TextWidth is the widest column of reading text, chats and artifacts;
	// 0 is the full width.
	TextWidth int `json:"textWidth"`
}

// UpdateKind classifies a hub update.
type UpdateKind string

const (
	UpdateEvent     UpdateKind = "event"
	UpdateThread    UpdateKind = "thread"
	UpdateWorkspace UpdateKind = "workspace"
	// UpdateDeleted says Thread no longer exists, or with no Thread, that
	// the whole workspace is gone.
	UpdateDeleted UpdateKind = "deleted"
	// UpdateArtifact says an artifact was written.
	UpdateArtifact UpdateKind = "artifact"
	// UpdateStopped is the last update of a hub that was asked to stop. A
	// client must not start a new hub for it.
	UpdateStopped UpdateKind = "stopped"
	// UpdateWaits is the whole waits list, after it changed.
	UpdateWaits UpdateKind = "waits"
	// UpdateStatus is the attention agent's new Status.
	UpdateStatus UpdateKind = "status"
	// UpdateDelta is the reply a thread writes now: Text is the text so far.
	// It is never in the transcript; the text event that follows replaces
	// it, and an empty Text drops it.
	UpdateDelta UpdateKind = "delta"
)

// Update is one change pushed to subscribers.
type Update struct {
	Kind      UpdateKind     `json:"kind"`
	Workspace string         `json:"workspace"`
	Thread    string         `json:"thread,omitempty"`
	Event     *Event         `json:"event,omitempty"`
	View      *ThreadView    `json:"view,omitempty"`
	Summary   *WorkspaceView `json:"summary,omitempty"`
	Artifact  *Artifact      `json:"artifact,omitempty"`
	Waits     []Wait         `json:"waits,omitempty"`
	Status    *Status        `json:"status,omitempty"`
	Text      string         `json:"text,omitempty"`
}

// Decision is the user's answer to a permission request.
type Decision string

const (
	Allow  Decision = "allow"
	Always Decision = "always"
	Deny   Decision = "deny"
)
