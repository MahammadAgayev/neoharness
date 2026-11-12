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
	// Version is an artifact's version.
	Version  int  `json:"version,omitempty"`
	NeedsYou bool `json:"needsYou,omitempty"`
}

// Artifact is a markdown document an agent wrote for the person to read. Its
// text is stored apart from this summary.
type Artifact struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Thread  string `json:"thread"`
	Version int    `json:"version"`
	// UserEdit says the newest version is the person's edit.
	UserEdit  bool      `json:"userEdit,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Permission is a request waiting for the user.
type Permission struct {
	ID     string `json:"id"`
	Tool   string `json:"tool"`
	Detail string `json:"detail"`
}

// ThreadView is a thread as the API shows it.
type ThreadView struct {
	Workspace string   `json:"workspace"`
	Name      string   `json:"name"`
	Role      Role     `json:"role"`
	Location  Location `json:"location"`
	State     State    `json:"state"`
	Queued    int      `json:"queued"`
	Progress  string   `json:"progress,omitempty"`
	// WakeAt is when the thread asked to be woken; zero is never.
	WakeAt     time.Time   `json:"wakeAt,omitzero"`
	Waiting    string      `json:"waiting,omitempty"`
	Permission *Permission `json:"permission,omitempty"`
	Pending    []Event     `json:"pending,omitempty"` // queued, not yet in the transcript
	// Quiet says the coordinator decides what of this thread reaches the
	// person: only its questions and permission requests notify them.
	Quiet bool `json:"quiet,omitempty"`
	// Model, Context and ContextWindow are as of the last turn; Cost is the
	// total of every turn, in US dollars. Branch is the git branch of the
	// thread's folder, empty outside a repository.
	Model         string    `json:"model,omitempty"`
	Context       int       `json:"context,omitempty"`
	ContextWindow int       `json:"contextWindow,omitempty"`
	Cost          float64   `json:"cost,omitempty"`
	Branch        string    `json:"branch,omitempty"`
	Since         time.Time `json:"since"`
	UpdatedAt     time.Time `json:"updatedAt"`
	CreatedAt     time.Time `json:"createdAt"`
}

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

// WorkspaceView is a workspace with a summary of its threads.
type WorkspaceView struct {
	Name      string        `json:"name"`
	Pin       string        `json:"pin,omitempty"`
	CreatedAt time.Time     `json:"createdAt"`
	Counts    map[State]int `json:"counts"`
	// Coordinator is the coordinator's state; Counts are the threads'.
	Coordinator State `json:"coordinator,omitempty"`
	// Activity is turns per hour over the last 24 hours, oldest first.
	Activity []int `json:"activity"`
	// CoordinatorAgent and ThreadsAgent are the agents the roles run here,
	// and AgentNames every agent of the config, for :setlocal.
	CoordinatorAgent string   `json:"coordinatorAgent,omitempty"`
	ThreadsAgent     string   `json:"threadsAgent,omitempty"`
	AgentNames       []string `json:"agentNames,omitempty"`
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
}

// Decision is the user's answer to a permission request.
type Decision string

const (
	Allow  Decision = "allow"
	Always Decision = "always"
	Deny   Decision = "deny"
)
