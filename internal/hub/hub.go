// Package hub owns every workspace and thread while neoharness runs: it queues
// messages, runs one turn at a time per thread, routes messages between
// threads, holds permission requests until the user answers, and pushes every
// change to subscribers.
//
// The hub is the only writer of workspace state. It knows agents only through
// agent.Agent and a factory, and knows nothing of MCP, HTTP, ssh, or the TUI;
// those are thin adapters around the methods here. That is what lets its tests
// drive it with a scripted fake in milliseconds.
package hub

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MahammadAgayev/neoharness/internal/agent"
	"github.com/MahammadAgayev/neoharness/internal/model"
	"github.com/MahammadAgayev/neoharness/internal/prompts"
	"github.com/MahammadAgayev/neoharness/internal/workspace"
)

// Defaults.
const (
	// MaxAgentTurns stops two threads from messaging each other forever. After
	// this many turns in a row that the user did not start, a thread waits
	// for the user.
	MaxAgentTurns = 20
	// TurnTimeout catches a wedged turn, not a slow one.
	TurnTimeout = 4 * time.Hour
	// filesTTL is how long a folder's file list is reused. Listing a remote
	// folder costs an ssh round trip, and the prompt asks on every Insert.
	filesTTL = 30 * time.Second
	// MaxReadTail is the most events read_thread returns at once.
	MaxReadTail = 200
)

// AgentFactory builds the agent that runs a thread's turns. name is the
// agent the thread's role runs in its workspace.
type AgentFactory func(name string, p workspace.Workspace, t workspace.Thread) agent.Agent

// FileLister lists the files in a location, relative to its folder: all of
// them, or with a query only those whose path holds it. many says there are
// too many to list, and the folder must be searched.
type FileLister func(ctx context.Context, l model.Location, query string) (paths []string, many bool, err error)

// BranchReader reads the git branch of a location; empty outside a
// repository.
type BranchReader func(ctx context.Context, l model.Location) (string, error)

// ShellRunner runs a command line in a location's folder, in the user's
// shell, and returns stdout and stderr together with the exit code.
type ShellRunner func(ctx context.Context, l model.Location, command string) (out string, exit int, err error)

// Defaults are an agent's settings for a session that chose none of its own.
type Defaults struct {
	// Model and Effort are for a session with no /model or /effort of its
	// own. Empty leaves the agent's default.
	Model, Effort string
	// ContextWindow, when not 0, is the most of a model's window that
	// counts: the context percentage is of this, so it warns in time to
	// /compact.
	ContextWindow int
}

// Config is how the hub is wired.
type Config struct {
	Factory AgentFactory
	// MCPPort is where the MCP server listens, on 127.0.0.1 here and, through
	// the ssh tunnel, on every remote host.
	MCPPort int
	// Agents are the agents of the config, by name. Coordinator names the
	// coordinator's agent, and Threads the threads'; empty is the
	// coordinator's. A workspace can choose others with SetLocal.
	Agents               map[string]Defaults
	Coordinator, Threads string
	MaxAgentTurns        int
	TurnTimeout          time.Duration
	// Files lists a thread's folder for `@` completion. Nil lists nothing.
	Files FileLister
	// Branch reads a thread's git branch for the status line. Nil reads
	// nothing.
	Branch BranchReader
	// Shell runs `!` commands. Nil refuses them.
	Shell ShellRunner
	// Notify makes the coordinator the one that notifies the person: thread
	// turns wake it, and it decides with notify what reaches them.
	Notify bool
}

// Hub is the running state of every workspace.
type Hub struct {
	cfg Config

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu         sync.Mutex
	workspaces map[string]*space
	tokens     map[string]ident
	subs       map[int]chan model.Update
	nextSub    int
	nextPerm   int

	filesMu sync.Mutex
	files   map[model.Location]fileList
}

type fileList struct {
	at    time.Time
	paths []string
	many  bool
}

type space struct {
	p       workspace.Workspace
	threads map[string]*thread
	// ended holds the threads that ended a turn since the coordinator's last
	// turn began. A thread's report does not wake the coordinator, so its
	// next turn starts with a note about them.
	ended []string
	// reports wakes the coordinator about ended, when Config.Notify is on.
	reports *time.Timer
}

type thread struct {
	t       *workspace.Thread
	busy    bool
	started bool // the current turn produced output, so its session exists
	cancel  context.CancelFunc
	// interrupted marks a turn that SendNow or Stop stopped; dropped says
	// Stop did, so the messages of the turn do not go again.
	interrupted, dropped bool
	// wake is the timer of the thread's wake-up.
	wake  *time.Timer
	perms []*pending
	state model.State
	// reply is the final text of its last turn, for the coordinator's note.
	reply string
	// deleted stops a turn that is still unwinding from writing the thread
	// back to disk.
	deleted bool
	branch  string
}

type pending struct {
	model.Permission
	prefix string
	done   chan model.Decision
	msg    string
}

type ident struct {
	workspace string
	thread    string
}

// Identity is who an MCP token belongs to.
type Identity struct {
	Workspace string
	Thread    string
	Role      model.Role
}

// New loads every workspace from disk and resumes any queued work.
func New(cfg Config) (*Hub, error) {
	if cfg.MaxAgentTurns == 0 {
		cfg.MaxAgentTurns = MaxAgentTurns
	}
	if cfg.TurnTimeout == 0 {
		cfg.TurnTimeout = TurnTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &Hub{
		cfg:        cfg,
		ctx:        ctx,
		cancel:     cancel,
		workspaces: map[string]*space{},
		tokens:     map[string]ident{},
		subs:       map[int]chan model.Update{},
		files:      map[model.Location]fileList{},
	}
	ps, err := workspace.List()
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range ps {
		if p.Deleted {
			continue
		}
		if _, err := h.load(p); err != nil {
			return nil, err
		}
	}
	for _, ws := range h.workspaces {
		h.start(ws)
	}
	return h, nil
}

// load reads a workspace and its threads into the hub. Callers hold h.mu.
func (h *Hub) load(p workspace.Workspace) (*space, error) {
	ts, err := workspace.Threads(p.Name)
	if err != nil {
		return nil, err
	}
	ws := &space{p: p, threads: map[string]*thread{}}
	h.workspaces[p.Name] = ws
	for _, t := range ts {
		th := &thread{t: t}
		ws.threads[t.Name] = th
		h.tokens[t.Token] = ident{p.Name, t.Name}
		if len(t.InFlight) > 0 {
			// The hub died with this turn running, and its claude process
			// died with it. Say so instead of pretending it finished.
			t.InFlight = nil
			h.event(ws, th, model.Event{Kind: model.EventError,
				Text: "The hub stopped during this turn, so it was lost. Send the message again to retry."})
		}
		th.state = th.derive()
		_ = workspace.SaveThread(p.Name, t)
	}
	return ws, nil
}

// start runs a loaded workspace's queued messages and reads its branches.
// Callers hold h.mu.
func (h *Hub) start(ws *space) {
	for _, th := range ws.threads {
		h.armWake(ws, th)
		h.kick(ws, th)
		h.readBranch(ws, th)
	}
}

// closeWait is how long Close waits for turns to unwind. An agent that does
// not stop must not keep the program from quitting.
var closeWait = 5 * time.Second

// Close cancels every running turn and waits for them to unwind, for
// closeWait at most.
func (h *Hub) Close() {
	h.cancel()
	done := make(chan struct{})
	go func() { h.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(closeWait):
	}
}

// --- workspaces ---

// CreateWorkspace creates a workspace and its coordinator. pin is an optional
// local folder.
func (h *Hub) CreateWorkspace(name, pin string) error {
	if pin != "" {
		abs, err := filepath.Abs(expandHome(pin))
		if err != nil {
			return err
		}
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			return fmt.Errorf("%s is not a folder", abs)
		}
		pin = abs
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.workspaces[name]; ok {
		return fmt.Errorf("workspace %q already exists", name)
	}
	if _, err := os.Stat(workspace.Dir(name)); err == nil {
		return fmt.Errorf("a deleted workspace %q still keeps its data in %s; choose another name", name, workspace.Dir(name))
	}
	p := workspace.Workspace{Name: name, Pin: pin, CreatedAt: time.Now().UTC()}
	if err := workspace.Create(p); err != nil {
		return err
	}
	ws := &space{p: p, threads: map[string]*thread{}}
	h.workspaces[name] = ws

	dir := pin
	if dir == "" {
		// An unpinned workspace still needs somewhere for its coordinator to
		// stand; its own folder is the one place that certainly exists.
		dir = filepath.Join(workspace.Dir(name), "workspace")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if _, err := h.addThread(ws, model.Coordinator, model.RoleCoordinator, model.Location{Dir: dir}); err != nil {
		return err
	}
	h.publishWorkspace(ws)
	return nil
}

// DeletedWorkspaces names the deleted workspaces whose data is still there.
func (h *Hub) DeletedWorkspaces() ([]string, error) {
	ps, err := workspace.List()
	var out []string
	for _, p := range ps {
		if p.Deleted {
			out = append(out, p.Name)
		}
	}
	return out, err
}

// RestoreWorkspace brings a deleted workspace back, with its threads.
func (h *Hub) RestoreWorkspace(name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.workspaces[name]; ok {
		return fmt.Errorf("workspace %q is not deleted", name)
	}
	ps, err := workspace.List()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(ps, func(p workspace.Workspace) bool { return p.Name == name && p.Deleted })
	if i < 0 {
		return fmt.Errorf("no deleted workspace %q", name)
	}
	p := ps[i]
	p.Deleted = false
	ws, err := h.load(p)
	if err != nil {
		return err
	}
	if err := workspace.Save(p); err != nil {
		delete(h.workspaces, name)
		return err
	}
	h.start(ws)
	h.publishWorkspace(ws)
	return nil
}

// SetFolder changes the folder a workspace is pinned to. The coordinator
// moves there and keeps its conversation: Claude finds a session from any
// folder. Open threads stay where they are; new ones open in the new folder.
func (h *Hub) SetFolder(name, dir string) error {
	abs, err := filepath.Abs(expandHome(dir))
	if err != nil {
		return err
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a folder", abs)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, err := h.workspace(name)
	if err != nil {
		return err
	}
	p := ws.p
	p.Pin = abs
	if err := workspace.Save(p); err != nil {
		return err
	}
	ws.p = p
	if th, ok := ws.threads[model.Coordinator]; ok {
		th.t.Location = model.Location{Dir: abs}
		h.event(ws, th, model.Event{Kind: model.EventNote, Text: "Folder changed to " + abs + ". New threads open there; open threads stay where they are."})
		h.changed(ws, th)
		h.readBranch(ws, th)
	}
	h.publishWorkspace(ws)
	return nil
}

// SetLocal sets which agent the coordinator or the threads of a workspace
// run, from their next turn. An empty agent goes back to the config's.
func (h *Hub) SetLocal(name, option, agentName string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, err := h.workspace(name)
	if err != nil {
		return err
	}
	if _, ok := h.cfg.Agents[agentName]; agentName != "" && !ok {
		return fmt.Errorf("no agent %q; the agents are %s", agentName, strings.Join(slices.Sorted(maps.Keys(h.cfg.Agents)), ", "))
	}
	p := ws.p
	var who string
	switch option {
	case "coordinator":
		p.Coordinator, who = agentName, "The coordinator runs"
	case "threads":
		p.Threads, who = agentName, "Threads run"
	default:
		return fmt.Errorf("no option %q; the options are coordinator and threads", option)
	}
	if err := workspace.Save(p); err != nil {
		return err
	}
	ws.p = p
	if th, ok := ws.threads[model.Coordinator]; ok {
		co := h.agentOf(ws, th.t)
		threads := h.agentOf(ws, &workspace.Thread{Role: model.RoleThread})
		text := fmt.Sprintf("%s %s from the next turn.", who, co)
		if option == "threads" {
			text = fmt.Sprintf("%s %s from their next turn.", who, threads)
		}
		h.event(ws, th, model.Event{Kind: model.EventNote, Text: text})
		h.changed(ws, th)
	}
	h.publishWorkspace(ws)
	return nil
}

// DeleteWorkspace hides a workspace and stops its turns. Its data stays on
// disk, marked deleted.
func (h *Hub) DeleteWorkspace(name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, err := h.workspace(name)
	if err != nil {
		return err
	}
	p := ws.p
	p.Deleted = true
	if err := workspace.Save(p); err != nil {
		return err
	}
	for _, th := range ws.threads {
		if len(th.t.InFlight) > 0 {
			th.t.InFlight = nil
			h.event(ws, th, model.Event{Kind: model.EventError,
				Text: "The workspace was deleted during this turn, so it was lost. Send the message again to retry."})
			_ = workspace.SaveThread(name, th.t)
		}
		// Stops a turn that is still unwinding from writing to a workspace
		// that is gone.
		th.deleted = true
		if th.cancel != nil {
			th.cancel()
		}
		h.denyAll(th, "the workspace was deleted")
		delete(h.tokens, th.t.Token)
	}
	delete(h.workspaces, name)
	h.publish(model.Update{Kind: model.UpdateDeleted, Workspace: name})
	return nil
}

// Workspaces lists every workspace with a summary.
func (h *Hub) Workspaces() []model.WorkspaceView {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]model.WorkspaceView, 0, len(h.workspaces))
	for _, ws := range h.workspaces {
		out = append(out, h.summary(ws))
	}
	slices.SortFunc(out, func(a, b model.WorkspaceView) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out
}

// Threads lists a workspace's threads: the coordinator first, then by age.
func (h *Hub) Threads(workspaceName string) ([]model.ThreadView, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, err := h.workspace(workspaceName)
	if err != nil {
		return nil, err
	}
	return h.views(ws), nil
}

// Events returns the last n transcript events of a thread.
func (h *Hub) Events(workspaceName, threadName string, n int) ([]model.Event, error) {
	h.mu.Lock()
	_, _, err := h.lookup(workspaceName, threadName)
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return workspace.Events(workspaceName, threadName, n)
}

// Completions lists what the prompt of a thread can complete. The commands
// are the ones the thread's last turn reported; before its first turn, those
// of another thread in the same folder, else on the same host, because a
// different machine has different plugins and skills.
// Completions is what a thread's prompt completes. In a folder with too many
// files to send, Files are only those whose path holds query.
func (h *Hub) Completions(workspaceName, threadName, query string) (model.Completions, error) {
	h.mu.Lock()
	ws, th, err := h.lookup(workspaceName, threadName)
	if err != nil {
		h.mu.Unlock()
		return model.Completions{}, err
	}
	c := model.Completions{Commands: th.t.Commands, Threads: []string{}}
	loc := th.t.Location
	if len(c.Commands) == 0 {
		c.Commands = h.commandsNear(loc)
	}
	for _, v := range h.views(ws) {
		if v.Name != threadName {
			c.Threads = append(c.Threads, v.Name)
		}
	}
	h.mu.Unlock()
	if c.Files, c.Many = h.listFiles(loc); c.Many {
		c.Files, c.Query = h.searchFiles(loc, query), query
	}
	if c.Commands == nil {
		c.Commands = []string{}
	}
	return c, nil
}

func (h *Hub) commandsNear(loc model.Location) []string {
	var sameHost []string
	for _, ws := range h.workspaces {
		for _, th := range ws.threads {
			t := th.t
			switch {
			case len(t.Commands) == 0 || t.Location.Host != loc.Host:
			case t.Location == loc:
				return t.Commands
			case sameHost == nil:
				sameHost = t.Commands
			}
		}
	}
	return sameHost
}

// listFiles returns a location's files, cached for filesTTL. A failure is
// cached too and shows as no files: one unreachable host must not cost an ssh
// timeout on every Insert.
func (h *Hub) listFiles(loc model.Location) ([]string, bool) {
	if h.cfg.Files == nil {
		return []string{}, false
	}
	h.filesMu.Lock()
	defer h.filesMu.Unlock()
	if f, ok := h.files[loc]; ok && time.Since(f.at) < filesTTL {
		return f.paths, f.many
	}
	ctx, cancel := context.WithTimeout(h.ctx, 10*time.Second)
	defer cancel()
	paths, many, err := h.cfg.Files(ctx, loc, "")
	if err != nil || paths == nil {
		paths = []string{}
	}
	h.files[loc] = fileList{at: time.Now(), paths: paths, many: many && err == nil}
	return paths, many && err == nil
}

// searchFiles returns the files of a location whose path holds query.
func (h *Hub) searchFiles(loc model.Location, query string) []string {
	if query == "" {
		return []string{}
	}
	ctx, cancel := context.WithTimeout(h.ctx, 10*time.Second)
	defer cancel()
	paths, _, err := h.cfg.Files(ctx, loc, query)
	if err != nil || paths == nil {
		return []string{}
	}
	return paths
}

// ReadThread returns the last tail events of a thread, only those that
// contain query when it is not empty. The caller must say how much it wants:
// a whole transcript can be larger than its context.
func (h *Hub) ReadThread(workspaceName, name string, tail int, query string) ([]model.Event, error) {
	if tail < 1 || tail > MaxReadTail {
		return nil, fmt.Errorf("tail must be from 1 to %d", MaxReadTail)
	}
	if query == "" {
		return h.Events(workspaceName, name, tail)
	}
	evs, err := h.Events(workspaceName, name, 0)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	evs = slices.DeleteFunc(evs, func(e model.Event) bool {
		return !strings.Contains(strings.ToLower(e.From+" "+e.Tool+" "+e.Detail+" "+e.Text), q)
	})
	return evs[max(len(evs)-tail, 0):], nil
}

// --- threads ---

// OpenThread creates a thread and, if brief is not empty, sends it as the
// first message. by is the user or the coordinator. loc is "host:dir", a
// local folder, or empty for the workspace folder.
func (h *Hub) OpenThread(workspaceName, by, name, brief, loc string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, err := h.workspace(workspaceName)
	if err != nil {
		return err
	}
	if err := model.ValidName(name); err != nil {
		return err
	}
	if name == model.Coordinator || name == model.User {
		return fmt.Errorf("%q is reserved", name)
	}
	if _, ok := ws.threads[name]; ok {
		return fmt.Errorf("thread %q already exists in %s; choose another name", name, workspaceName)
	}
	var l model.Location
	if strings.TrimSpace(loc) == "" {
		if ws.p.Pin == "" {
			return fmt.Errorf("workspace %s is not pinned to a folder; give a location (a folder or host:dir)", workspaceName)
		}
		l = model.Location{Dir: ws.p.Pin}
	} else if l, err = model.ParseLocation(loc); err != nil {
		return err
	}
	if !l.Remote() {
		abs, err := filepath.Abs(expandHome(l.Dir))
		if err != nil {
			return err
		}
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			return fmt.Errorf("%s is not a folder", abs)
		}
		l.Dir = abs
	}
	th, err := h.addThread(ws, name, model.RoleThread, l)
	if err != nil {
		return err
	}
	h.event(ws, th, model.Event{Kind: model.EventNote, Text: "Opened by " + by + " in " + l.String()})
	if strings.TrimSpace(brief) != "" {
		h.enqueue(ws, th, by, brief)
	}
	return nil
}

func (h *Hub) addThread(ws *space, name string, role model.Role, l model.Location) (*thread, error) {
	now := time.Now().UTC()
	t := &workspace.Thread{
		Name:      name,
		Role:      role,
		Location:  l,
		SessionID: workspace.NewID(),
		Token:     workspace.NewToken(),
		Queue:     []workspace.Message{},
		Since:     now,
		CreatedAt: now,
	}
	if err := workspace.SaveThread(ws.p.Name, t); err != nil {
		return nil, err
	}
	th := &thread{t: t, state: model.StateIdle}
	ws.threads[name] = th
	h.tokens[t.Token] = ident{ws.p.Name, name}
	h.changed(ws, th)
	h.readBranch(ws, th)
	return th, nil
}

// Send queues a message for a thread. from is model.User, the coordinator, or
// another thread's name. A message from the user also answers any question the
// thread asked and lifts a loop-guard pause.
func (h *Hub) Send(workspaceName, from, to, text string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.send(workspaceName, from, to, text)
	return err
}

// SendNow sends a message from the user and stops the running turn, so the
// message starts the next turn at once. With no text, the messages already
// queued go now. Claude keeps the work of the stopped turn in its session.
func (h *Hub) SendNow(workspaceName, to, text string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var th *thread
	var err error
	if strings.TrimSpace(text) == "" {
		var ws *space
		ws, th, err = h.lookup(workspaceName, to)
		if err == nil && len(th.t.Queue) == 0 {
			err = errors.New("nothing is queued")
		}
		// Stopped with messages queued: nothing runs, so they start now.
		if err == nil && th.t.Paused {
			th.t.Paused = false
			h.changed(ws, th)
			h.kick(ws, th)
			return nil
		}
	} else {
		th, err = h.send(workspaceName, model.User, to, text)
	}
	if err != nil {
		return err
	}
	if th.busy && th.cancel != nil {
		th.interrupted = true
		th.cancel()
	}
	return nil
}

// Stop stops a thread's running turn. Messages Claude did not see yet are
// dropped, not sent again, and those queued behind it wait for the person.
func (h *Hub) Stop(workspaceName, name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, th, err := h.lookup(workspaceName, name)
	if err != nil {
		return err
	}
	if !th.busy || th.cancel == nil {
		return errors.New("nothing runs")
	}
	th.interrupted, th.dropped = true, true
	th.cancel()
	return nil
}

// Unqueue takes back a queued message that no turn has read yet. at is when
// it was queued.
func (h *Hub) Unqueue(workspaceName, name string, at time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, th, err := h.lookup(workspaceName, name)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(th.t.Queue, func(m workspace.Message) bool { return !m.Shown && m.At.Equal(at) })
	if i < 0 {
		return errors.New("that message is no longer queued")
	}
	th.t.Queue = slices.Delete(th.t.Queue, i, i+1)
	if len(th.t.Queue) == 0 {
		th.t.Paused = false
	}
	h.changed(ws, th)
	return nil
}

func (h *Hub) send(workspaceName, from, to, text string) (*thread, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("empty message")
	}
	ws, th, err := h.lookup(workspaceName, to)
	if err != nil {
		return nil, err
	}
	if from == to {
		return nil, errors.New("a thread cannot message itself")
	}
	switch {
	case th.t.Resolved && from == model.User:
		// Writing to it says the person wants it back.
		h.reopen(ws, th)
	case th.t.Resolved:
		return nil, fmt.Errorf("thread %q is resolved; reopen it first", to)
	}
	if from == model.User {
		if done, err := h.command(ws, th, text); done {
			return th, err
		}
	}
	// The thread asked and ended its reply; what still runs (background
	// tasks) must not hold the answer back. Checked before enqueue, which may
	// start the answer's own turn.
	if from == model.User && th.t.Asking != "" && th.busy && th.cancel != nil {
		th.interrupted = true
		th.cancel()
	}
	h.enqueue(ws, th, from, text)
	return th, nil
}

// command runs the slash commands that do not work under --print: each turn
// is a new process, so /model and /effort last one turn, and /clear and
// /compact do nothing. It reports whether text was one of them.
func (h *Hub) command(ws *space, th *thread, text string) (bool, error) {
	f := strings.Fields(text)
	if len(f) == 0 {
		return false, nil
	}
	t := th.t
	switch {
	case f[0] == "/clear" && len(f) == 1:
		return true, h.clear(ws, th)
	case f[0] == "/model" && len(f) == 2:
		t.ModelChoice = f[1]
		if f[1] == "default" {
			t.ModelChoice = ""
		}
		h.event(ws, th, model.Event{Kind: model.EventNote, Text: "Model " + f[1] + " from the next turn."})
	case f[0] == "/effort" && len(f) == 2:
		if e, ok := h.cfg.Factory(h.agentOf(ws, t), ws.p, *t).(agent.Efforter); ok && len(e.Efforts()) > 0 && f[1] != "default" && !slices.Contains(e.Efforts(), f[1]) {
			return true, fmt.Errorf("effort is one of %s, or default", strings.Join(e.Efforts(), ", "))
		}
		t.Effort = f[1]
		if f[1] == "default" {
			t.Effort = ""
		}
		h.event(ws, th, model.Event{Kind: model.EventNote, Text: "Effort " + f[1] + " from the next turn."})
	case strings.HasPrefix(text, "!"):
		return true, h.shell(ws, th, strings.TrimSpace(text[1:]))
	case f[0] == "/compact":
		if !t.Started {
			return true, errors.New("nothing to compact yet")
		}
		focus := strings.Join(f[1:], " ")
		t.AgentTurns, t.Paused, t.Asking = 0, false, ""
		t.Queue = append(t.Queue, workspace.Message{From: model.User, Text: focus, At: time.Now().UTC(), Compact: true, Shown: true})
		h.event(ws, th, model.Event{Kind: model.EventMessage, From: model.User, Text: strings.TrimSpace(text)})
		h.changed(ws, th)
		h.kick(ws, th)
		return true, nil
	default:
		return false, nil
	}
	h.changed(ws, th)
	return true, nil
}

// shellTimeout stops a `!` command that runs too long.
const shellTimeout = 10 * time.Minute

// shellLimit is how much of a `!` command's output is kept, from the end.
const shellLimit = 30000

// shell runs a `!` command, like Claude Code's shell mode: the output shows
// in the transcript and goes to Claude with the next message. It starts no
// turn. Claude's own `!` does not work under --print.
func (h *Hub) shell(ws *space, th *thread, command string) error {
	if command == "" {
		return errors.New("write a command after !")
	}
	if h.cfg.Shell == nil {
		return errors.New("! commands are not available")
	}
	id := strconv.FormatInt(time.Now().UnixNano(), 36)
	h.event(ws, th, model.Event{Kind: model.EventShell, ID: id, Detail: command})
	loc := th.t.Location
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		ctx, cancel := context.WithTimeout(h.ctx, shellTimeout)
		defer cancel()
		out, exit, err := h.cfg.Shell(ctx, loc, command)
		if err != nil {
			out, exit = strings.TrimSpace(out+"\n"+err.Error()), -1
		}
		if len(out) > shellLimit {
			out = "…" + out[len(out)-shellLimit:]
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if th.deleted {
			return
		}
		h.event(ws, th, model.Event{Kind: model.EventShell, ID: id, Detail: command, Text: out, Exit: exit})
		th.t.Shell = append(th.t.Shell, prompts.Shell(command, out, exit))
		h.changed(ws, th)
	}()
	return nil
}

// clear gives a thread a new Claude session. Claude's own /clear does nothing
// under --print: the next turn resumes the same session, so the hub does it.
func (h *Hub) clear(ws *space, th *thread) error {
	if th.busy {
		return errors.New("a turn is running; /clear when it ends")
	}
	t := th.t
	t.SessionID, t.Started = workspace.NewID(), false
	t.Context, t.Summary = 0, ""
	t.Queue = []workspace.Message{}
	t.Asking, t.Paused, t.AgentTurns = "", false, 0
	h.event(ws, th, model.Event{Kind: model.EventClear, Text: "Cleared. The next message starts a new conversation."})
	h.changed(ws, th)
	return nil
}

func (h *Hub) enqueue(ws *space, th *thread, from, text string) {
	t := th.t
	if from == model.User {
		t.AgentTurns = 0
		t.Paused = false
		t.Asking = ""
	}
	// The message goes into the transcript when its turn starts (kick), so
	// the transcript shows what the agent read in the order it read it.
	t.Queue = append(t.Queue, workspace.Message{From: from, Text: text, At: time.Now().UTC()})
	h.changed(ws, th)
	h.kick(ws, th)
}

// Resolve closes a thread. A running turn is cancelled and queued messages
// are dropped. The coordinator cannot be resolved.
func (h *Hub) Resolve(workspaceName, by, name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, th, err := h.lookup(workspaceName, name)
	if err != nil {
		return err
	}
	if th.t.Role == model.RoleCoordinator {
		return errors.New("the coordinator cannot be resolved")
	}
	if th.t.Resolved {
		return nil
	}
	t := th.t
	t.Resolved = true
	t.Queue = []workspace.Message{}
	t.Asking = ""
	t.Paused = false
	t.WakeAt, t.WakeNote = time.Time{}, ""
	h.armWake(ws, th)
	if th.cancel != nil {
		th.cancel()
	}
	h.denyAll(th, "the thread was resolved")
	h.event(ws, th, model.Event{Kind: model.EventNote, Text: "Resolved by " + by})
	h.changed(ws, th)
	return nil
}

// Delete removes a thread and its transcript for good. A running turn is
// stopped. The coordinator cannot be deleted.
func (h *Hub) Delete(workspaceName, name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, th, err := h.lookup(workspaceName, name)
	if err != nil {
		return err
	}
	if th.t.Role == model.RoleCoordinator {
		return errors.New("the coordinator cannot be deleted")
	}
	th.deleted = true
	th.t.Queue = nil
	if th.wake != nil {
		th.wake.Stop()
	}
	if th.cancel != nil {
		th.cancel()
	}
	h.denyAll(th, "the thread was deleted")
	delete(ws.threads, name)
	delete(h.tokens, th.t.Token)
	if err := workspace.DeleteThread(workspaceName, name); err != nil {
		return err
	}
	h.publish(model.Update{Kind: model.UpdateDeleted, Workspace: workspaceName, Thread: name})
	h.publishWorkspace(ws)
	return nil
}

// Reopen brings a resolved thread back. It keeps its Claude session, so it
// remembers everything before it was resolved.
func (h *Hub) Reopen(workspaceName, name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, th, err := h.lookup(workspaceName, name)
	if err != nil {
		return err
	}
	if th.t.Resolved {
		h.reopen(ws, th)
	}
	return nil
}

func (h *Hub) reopen(ws *space, th *thread) {
	th.t.Resolved = false
	th.t.AgentTurns = 0
	h.event(ws, th, model.Event{Kind: model.EventNote, Text: "Reopened"})
	h.changed(ws, th)
}

// ReportProgress sets a thread's progress line.
// artifactLimit is the largest artifact, in bytes. It travels in one MCP call,
// over the ssh tunnel for a remote thread.
const artifactLimit = 1 << 20

// WriteArtifact creates or replaces a workspace's artifact. Its title is the
// first heading, else the name.
func (h *Hub) WriteArtifact(workspaceName, by, name, text string) (model.Artifact, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, th, err := h.lookup(workspaceName, by)
	if err != nil {
		return model.Artifact{}, err
	}
	if err := model.ValidName(name); err != nil {
		return model.Artifact{}, err
	}
	if strings.TrimSpace(text) == "" {
		return model.Artifact{}, errors.New("the artifact is empty")
	}
	if len(text) > artifactLimit {
		return model.Artifact{}, fmt.Errorf("the artifact is %d KB; the limit is %d KB. Split it into several artifacts", len(text)>>10, artifactLimit>>10)
	}
	now := time.Now().UTC()
	a, old, err := workspace.Artifact(workspaceName, name)
	if errors.Is(err, os.ErrNotExist) {
		a = model.Artifact{Name: name, CreatedAt: now}
	} else if err != nil {
		return model.Artifact{}, err
	}
	// Writing over an edit the writer has not seen would drop it without
	// anyone knowing. The refusal carries the edit instead.
	if wt, ok := ws.threads[a.Thread]; ok {
		if e, unseen := wt.t.Edits[name]; unseen {
			delete(wt.t.Edits, name)
			h.changed(ws, wt)
			return model.Artifact{}, errors.New(editNote(name, e, old, a.Version) + "\n\nNothing was saved. Write the whole document again with their changes in it.")
		}
	}
	a.Title, a.Thread, a.UpdatedAt, a.UserEdit = title(text, name), by, now, false
	a.Version++
	if err := workspace.SaveArtifact(workspaceName, a, text); err != nil {
		return model.Artifact{}, err
	}
	h.event(ws, th, model.Event{Kind: model.EventArtifact, Detail: name, Text: a.Title, Version: a.Version})
	h.publish(model.Update{Kind: model.UpdateArtifact, Workspace: workspaceName, Thread: by, Artifact: &a})
	return a, nil
}

// EditArtifact saves the person's edit of an artifact as a new version and
// returns the text saved. base and baseText are the version the edit started
// from: if the artifact moved on since, the edit is merged into it, or
// refused when both changed the same lines, unless force. The writer gets
// the diff with its next message; edits until then add up to one diff.
func (h *Hub) EditArtifact(workspaceName, name, text, baseText string, base int, force bool) (model.Artifact, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, err := h.workspace(workspaceName)
	if err != nil {
		return model.Artifact{}, "", err
	}
	a, old, err := workspace.Artifact(workspaceName, name)
	if errors.Is(err, os.ErrNotExist) {
		return a, "", fmt.Errorf("no artifact %q in %s", name, workspaceName)
	} else if err != nil {
		return a, "", err
	}
	if strings.TrimSpace(text) == "" {
		return a, "", errors.New("the artifact is empty")
	}
	if len(text) > artifactLimit {
		return a, "", fmt.Errorf("the artifact is %d KB; the limit is %d KB", len(text)>>10, artifactLimit>>10)
	}
	if a.Version != base && !force {
		merged, ok := merge3(baseText, text, old)
		if !ok {
			return a, "", fmt.Errorf("v%d changed the lines you edited · :w! saves over it, :e! takes it", a.Version)
		}
		text = merged
	}
	if text == old {
		return a, text, nil
	}
	th, ok := ws.threads[a.Thread]
	if ok {
		if _, unseen := th.t.Edits[name]; !unseen {
			if th.t.Edits == nil {
				th.t.Edits = map[string]workspace.Edit{}
			}
			th.t.Edits[name] = workspace.Edit{Version: a.Version, Text: old}
		}
	}
	a.Title, a.UpdatedAt, a.UserEdit = title(text, name), time.Now().UTC(), true
	a.Version++
	if err := workspace.SaveArtifact(workspaceName, a, text); err != nil {
		return model.Artifact{}, "", err
	}
	if ok {
		h.event(ws, th, model.Event{Kind: model.EventArtifact, From: model.User, Detail: name, Text: a.Title, Version: a.Version})
		h.changed(ws, th)
	}
	h.publish(model.Update{Kind: model.UpdateArtifact, Workspace: workspaceName, Thread: a.Thread, Artifact: &a})
	return a, text, nil
}

// editNotes tells a thread what the person changed in the artifacts it
// wrote, one note each, and forgets the edits. Callers hold h.mu.
func (h *Hub) editNotes(ws *space, t *workspace.Thread) []string {
	var notes []string
	for _, name := range slices.Sorted(maps.Keys(t.Edits)) {
		a, text, err := workspace.Artifact(ws.p.Name, name)
		if err != nil || text == t.Edits[name].Text {
			continue
		}
		notes = append(notes, editNote(name, t.Edits[name], text, a.Version))
	}
	t.Edits = nil
	return notes
}

// editNote is the diff from what a thread saw to the artifact now.
func editNote(name string, seen workspace.Edit, now string, version int) string {
	diff, sections, added, removed := unifiedDiff(seen.Text, now)
	if len(diff) > diffLimit {
		diff = ""
	}
	return prompts.ArtifactEdited(name, seen.Version, version, diff, sections, added, removed)
}

// ReadArtifact returns an artifact and its text.
func (h *Hub) ReadArtifact(workspaceName, name string) (model.Artifact, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, err := h.workspace(workspaceName); err != nil {
		return model.Artifact{}, "", err
	}
	a, text, err := workspace.Artifact(workspaceName, name)
	if errors.Is(err, os.ErrNotExist) {
		return a, "", fmt.Errorf("no artifact %q in %s", name, workspaceName)
	}
	return a, text, err
}

// ReadArtifactAs is read_artifact for a session. When the session wrote the
// artifact and the person edited it since, the text comes after the edit's
// diff, and the edit counts as seen: no note next turn, no refused write.
func (h *Hub) ReadArtifactAs(workspaceName, by, name string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, err := h.workspace(workspaceName)
	if err != nil {
		return "", err
	}
	a, text, err := workspace.Artifact(workspaceName, name)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("no artifact %q in %s", name, workspaceName)
	} else if err != nil {
		return "", err
	}
	if th, ok := ws.threads[by]; ok && a.Thread == by {
		if e, unseen := th.t.Edits[name]; unseen {
			delete(th.t.Edits, name)
			h.changed(ws, th)
			return editNote(name, e, text, a.Version) + "\n\n---\n\n" + text, nil
		}
	}
	return text, nil
}

// Artifacts lists a workspace's artifacts, newest first.
func (h *Hub) Artifacts(workspaceName string) ([]model.Artifact, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, err := h.workspace(workspaceName); err != nil {
		return nil, err
	}
	return workspace.Artifacts(workspaceName)
}

// title is a document's first heading, without its marks.
func title(text, fallback string) string {
	for _, l := range strings.Split(text, "\n") {
		if h, ok := strings.CutPrefix(strings.TrimSpace(l), "#"); ok {
			if t := strings.TrimSpace(strings.TrimLeft(h, "#")); t != "" {
				return t
			}
		}
	}
	return fallback
}

func (h *Hub) ReportProgress(workspaceName, name, summary string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, th, err := h.lookup(workspaceName, name)
	if err != nil {
		return err
	}
	summary = strings.TrimSpace(summary)
	th.t.Progress = summary
	th.t.ProgressAt = time.Now().UTC()
	h.event(ws, th, model.Event{Kind: model.EventProgress, Text: summary})
	h.changed(ws, th)
	return nil
}

// MaxWake is the furthest ahead a thread can ask to be woken.
const MaxWake = 24 * time.Hour

// wakeFrom is the sender of a wake-up message.
const wakeFrom = "neoharness"

// WakeMe wakes a thread after d with a message that holds note, so it comes
// back to work that outlives its turn: nothing else wakes it. A new call
// replaces the old one; 0 cancels it.
func (h *Hub) WakeMe(workspaceName, name string, d time.Duration, note string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, th, err := h.lookup(workspaceName, name)
	if err != nil {
		return err
	}
	if d < 0 || d > MaxWake {
		return fmt.Errorf("a wake-up is at most %s ahead", MaxWake)
	}
	t := th.t
	t.WakeAt, t.WakeNote = time.Time{}, ""
	if d > 0 {
		t.WakeAt, t.WakeNote = time.Now().UTC().Add(d), strings.TrimSpace(note)
	}
	h.armWake(ws, th)
	h.changed(ws, th)
	return nil
}

// armWake sets the timer of a thread's wake-up, or stops it when there is
// none. A wake-up whose time passed while the hub was down fires at once.
func (h *Hub) armWake(ws *space, th *thread) {
	if th.wake != nil {
		th.wake.Stop()
		th.wake = nil
	}
	at, note := th.t.WakeAt, th.t.WakeNote
	if at.IsZero() {
		return
	}
	th.wake = time.AfterFunc(time.Until(at), func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		t := th.t
		if h.ctx.Err() != nil || th.deleted || t.Resolved || !t.WakeAt.Equal(at) {
			return
		}
		t.WakeAt, t.WakeNote, th.wake = time.Time{}, "", nil
		h.enqueue(ws, th, wakeFrom, prompts.Wake(note))
	})
}

// AskUser posts a question to the user. The thread waits until the user
// writes to it.
func (h *Hub) AskUser(workspaceName, name, question string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, th, err := h.lookup(workspaceName, name)
	if err != nil {
		return err
	}
	th.t.Asking = strings.TrimSpace(question)
	h.event(ws, th, model.Event{Kind: model.EventAsk, Text: th.t.Asking})
	h.changed(ws, th)
	return nil
}

// reportDelay is how long the coordinator waits after a thread's turn ends
// before it hears of it, so the threads that end close together make one
// coordinator turn.
var reportDelay = 15 * time.Second

// reportFrom is the sender of the message that wakes the coordinator about
// the threads that ended a turn.
const reportFrom = "threads"

// report wakes the coordinator about the threads that ended a turn, after
// reportDelay, when Config.Notify is on. A busy coordinator hears of them in
// its next turn; when that turn ends, report runs again. Callers hold h.mu.
func (h *Hub) report(ws *space) {
	if !h.cfg.Notify || ws.reports != nil {
		return
	}
	ws.reports = time.AfterFunc(reportDelay, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		ws.reports = nil
		co, ok := ws.threads[model.Coordinator]
		if h.ctx.Err() != nil || !ok || co.deleted || co.busy || len(ws.ended) == 0 {
			return
		}
		if slices.ContainsFunc(co.t.Queue, func(m workspace.Message) bool { return m.From == reportFrom }) {
			return
		}
		h.enqueue(ws, co, reportFrom, prompts.Reports(ws.ended))
	})
}

// Notifies says the coordinator is the one that notifies the person.
func (h *Hub) Notifies() bool { return h.cfg.Notify }

// Notify shows the person a notice from the coordinator. needsYou keeps it
// on screen until they look.
func (h *Hub) Notify(workspaceName, name, text string, needsYou bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, th, err := h.lookup(workspaceName, name)
	if err != nil {
		return err
	}
	if th.t.Role != model.RoleCoordinator {
		return errors.New("only the coordinator notifies the person")
	}
	if text = strings.TrimSpace(text); text == "" {
		return errors.New("empty notice")
	}
	h.event(ws, th, model.Event{Kind: model.EventNotice, Text: text, NeedsYou: needsYou})
	return nil
}

// Identify maps an MCP token to the thread it belongs to.
func (h *Hub) Identify(token string) (Identity, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id, ok := h.tokens[token]
	if !ok {
		return Identity{}, false
	}
	ws := h.workspaces[id.workspace]
	th := ws.threads[id.thread]
	return Identity{Workspace: id.workspace, Thread: id.thread, Role: th.t.Role}, true
}

// Whoami describes a thread to itself.
func (h *Hub) Whoami(workspaceName, name string) (model.ThreadView, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, th, err := h.lookup(workspaceName, name)
	if err != nil {
		return model.ThreadView{}, err
	}
	return h.view(ws, th), nil
}

// --- permissions ---

// Permission blocks until the user answers a permission request, or a rule
// answers it, or the turn ends. It returns whether the call may go ahead and,
// for a denial, the user's reason.
func (h *Hub) Permission(ctx context.Context, workspaceName, name string, q agent.Permission) (bool, string) {
	h.mu.Lock()
	ws, th, err := h.lookup(workspaceName, name)
	if err != nil {
		h.mu.Unlock()
		return false, err.Error()
	}
	tool, detail, prefix := q.Tool, q.Detail, q.Prefix
	for _, r := range th.t.Rules {
		if r.Tool == tool && !q.Compound && (r.Prefix == "" || r.Prefix == prefix) {
			h.mu.Unlock()
			return true, ""
		}
	}
	h.nextPerm++
	p := &pending{
		Permission: model.Permission{ID: fmt.Sprintf("p%d", h.nextPerm), Tool: tool, Detail: detail},
		prefix:     prefix,
		done:       make(chan model.Decision, 1),
	}
	th.perms = append(th.perms, p)
	h.event(ws, th, model.Event{Kind: model.EventPermission, ID: p.ID, Tool: tool, Detail: detail})
	h.changed(ws, th)
	h.mu.Unlock()

	var d model.Decision
	select {
	case d = <-p.done:
	case <-ctx.Done():
		d = model.Deny
		h.mu.Lock()
		h.dropPerm(ws, th, p.ID)
		h.mu.Unlock()
	case <-h.ctx.Done():
		return false, "neoharness is shutting down"
	}
	return d != model.Deny, p.msg
}

// Answer resolves a pending permission request.
func (h *Hub) Answer(workspaceName, name, id string, d model.Decision, msg string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, th, err := h.lookup(workspaceName, name)
	if err != nil {
		return err
	}
	if id == "" && len(th.perms) > 0 {
		id = th.perms[0].ID
	}
	var p *pending
	for _, q := range th.perms {
		if q.ID == id {
			p = q
		}
	}
	if p == nil {
		return fmt.Errorf("no pending permission %q on %s", id, name)
	}
	if d == model.Always {
		th.t.Rules = append(th.t.Rules, workspace.Rule{Tool: p.Tool, Prefix: p.prefix})
	}
	p.msg = msg
	text := string(d)
	if msg != "" {
		text += ": " + msg
	}
	h.event(ws, th, model.Event{Kind: model.EventAnswer, ID: p.ID, Tool: p.Tool, Text: text})
	h.dropPerm(ws, th, p.ID)
	p.done <- d
	return nil
}

func (h *Hub) dropPerm(ws *space, th *thread, id string) {
	th.perms = slices.DeleteFunc(th.perms, func(p *pending) bool { return p.ID == id })
	h.changed(ws, th)
}

func (h *Hub) denyAll(th *thread, why string) {
	for _, p := range th.perms {
		p.msg = why
		p.done <- model.Deny
	}
	th.perms = nil
}

// --- turns ---

// kick starts a turn if the thread has work and nothing stops it.
func (h *Hub) kick(ws *space, th *thread) {
	t := th.t
	if th.busy || t.Resolved || t.Paused || len(t.Queue) == 0 {
		return
	}
	fromUser := slices.ContainsFunc(t.Queue, func(m workspace.Message) bool { return m.From == model.User })
	// Reports to the coordinator do not count: threads can loop only by
	// messages, and those count on both sides.
	counts := slices.ContainsFunc(t.Queue, func(m workspace.Message) bool { return m.From != reportFrom })
	if !fromUser && counts && t.AgentTurns >= h.cfg.MaxAgentTurns {
		t.Paused = true
		h.event(ws, th, model.Event{Kind: model.EventNote,
			Text: fmt.Sprintf("Paused after %d turns without your input. Write to continue.", t.AgentTurns)})
		h.changed(ws, th)
		return
	}
	if !fromUser && counts {
		t.AgentTurns++
	}
	// A /compact request is a turn of its own.
	n := len(t.Queue)
	if t.Queue[0].Compact {
		n = 1
	} else if i := slices.IndexFunc(t.Queue, func(m workspace.Message) bool { return m.Compact }); i > 0 {
		n = i
	}
	batch := t.Queue[:n:n]
	t.Queue = append([]workspace.Message{}, t.Queue[n:]...)
	for i, m := range batch {
		if !m.Shown {
			h.event(ws, th, model.Event{Kind: model.EventMessage, From: m.From, Text: m.Text})
			batch[i].Shown = true
		}
	}
	t.InFlight = batch
	now := time.Now().UTC()
	t.Turns = append(slices.DeleteFunc(t.Turns, func(at time.Time) bool { return now.Sub(at) > 24*time.Hour }), now)

	ctx, cancel := context.WithTimeout(h.ctx, h.cfg.TurnTimeout)
	th.busy = true
	th.started = false
	th.interrupted, th.dropped = false, false
	th.cancel = cancel
	h.changed(ws, th)

	prompt := compose(batch, ws.threads)
	switch {
	case batch[0].Compact:
		prompt = prompts.Compact(batch[0].Text)
	case t.Summary != "":
		prompt = prompts.Compacted(t.Summary) + "\n\n---\n\n" + prompt
	}
	if len(t.Shell) > 0 && !batch[0].Compact {
		prompt = strings.Join(t.Shell, "\n\n") + "\n\n---\n\n" + prompt
		t.Shell = nil
	}
	if len(t.Edits) > 0 && !batch[0].Compact {
		if notes := h.editNotes(ws, t); len(notes) > 0 {
			prompt = strings.Join(notes, "\n\n") + "\n\n---\n\n" + prompt
		}
	}
	if t.Role == model.RoleCoordinator && !batch[0].Compact {
		if b := h.board(ws, batch); b != "" {
			prompt = b + "\n\n---\n\n" + prompt
		}
	}
	req := agent.Request{
		Prompt:       prompt,
		Model:        cmp.Or(t.ModelChoice, h.defaults(ws, t).Model),
		Effort:       cmp.Or(t.Effort, h.defaults(ws, t).Effort),
		SystemPrompt: h.systemPrompt(ws, th),
		Conversation: t.SessionID,
		Resume:       t.Started,
		Harness:      agent.Harness{URL: fmt.Sprintf("http://127.0.0.1:%d/mcp", h.cfg.MCPPort), Token: t.Token},
		HarnessOnly:  t.Role == model.RoleCoordinator,
	}
	if !t.Location.Remote() {
		req.Cwd = t.Location.Dir
	}
	req.OnEvent = func(e agent.Event) { h.onAgentEvent(ws, th, e) }
	ag := h.cfg.Factory(h.agentOf(ws, t), ws.p, *t)

	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer cancel()
		res, err := ag.Run(ctx, req)
		h.endTurn(ws, th, ctx, res, err)
	}()
}

func (h *Hub) endTurn(ws *space, th *thread, ctx context.Context, res agent.Result, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t := th.t
	compact := len(t.InFlight) > 0 && t.InFlight[0].Compact
	if t.Role != model.RoleCoordinator && !compact {
		if !slices.Contains(ws.ended, t.Name) {
			ws.ended = append(ws.ended, t.Name)
		}
		th.reply = ""
		if err == nil && res.ExitCode == 0 {
			th.reply = res.Text
		}
	}
	batch := t.InFlight
	th.busy = false
	th.cancel = nil
	t.InFlight = nil
	h.denyAll(th, "the turn ended")
	if th.started {
		t.Started = true
	}
	if len(res.Commands) > 0 {
		t.Commands = res.Commands
	}
	if res.Model != "" {
		t.Model = res.Model
	}
	if res.Context > 0 {
		t.Context, t.ContextWindow = res.Context, res.ContextWindow
		if c := h.defaults(ws, t).ContextWindow; c > 0 && c < t.ContextWindow {
			t.ContextWindow = c
		}
	}
	// A failed turn was still paid for.
	t.Cost += res.Cost
	h.readBranch(ws, th)
	switch {
	case h.ctx.Err() != nil:
		// Shutting down. The next start reports nothing lost, because nothing
		// is in flight on disk any more; say it here while we still can.
		h.event(ws, th, model.Event{Kind: model.EventError, Text: "neoharness stopped during this turn"})
	case t.Resolved:
		// Cancelled by Resolve; the note already says so.
	case th.interrupted:
		h.event(ws, th, model.Event{Kind: model.EventNote, Text: "Interrupted."})
		// Claude never saw these messages, so they go again with the new one.
		if !th.started && !th.dropped {
			t.Queue = append(batch, t.Queue...)
		}
		// A stop means stop: what the person queued behind the turn waits
		// for them.
		if th.dropped && len(t.Queue) > 0 {
			t.Paused = true
		}
	case ctx.Err() == context.DeadlineExceeded:
		h.event(ws, th, model.Event{Kind: model.EventError, Text: fmt.Sprintf("the turn ran past %s and was stopped", h.cfg.TurnTimeout)})
	case err != nil:
		h.event(ws, th, model.Event{Kind: model.EventError, Text: err.Error()})
	case res.Lost:
		// The agent never saw these messages.
		t.SessionID, t.Started = workspace.NewID(), false
		t.Queue = append(batch, t.Queue...)
		h.event(ws, th, model.Event{Kind: model.EventNote, Text: "The agent has no record of this conversation, so a new one starts."})
	case res.ExitCode != 0:
		h.event(ws, th, model.Event{Kind: model.EventError, Text: "the agent failed: " + lastLine(res.Stderr, res.Text)})
	default:
		t.Started = true
		h.event(ws, th, model.Event{Kind: model.EventTurnEnd, Tools: res.Tools, Tokens: res.Tokens})
		switch {
		case compact && strings.TrimSpace(res.Text) == "":
			h.event(ws, th, model.Event{Kind: model.EventError, Text: "/compact got no summary; the conversation is unchanged"})
		case compact:
			t.Summary = strings.TrimSpace(res.Text)
			t.SessionID, t.Started, t.Context = workspace.NewID(), false, 0
			h.event(ws, th, model.Event{Kind: model.EventNote, Text: "Compacted. The next message starts a new conversation from the summary above."})
		default:
			t.Summary = ""
		}
	}
	h.changed(ws, th)
	if h.ctx.Err() == nil {
		h.kick(ws, th)
		if len(ws.ended) > 0 {
			h.report(ws)
		}
	}
}

func (h *Hub) onAgentEvent(ws *space, th *thread, e agent.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	th.started = true
	// The context grows during a turn; the status line follows it in steps of
	// 1% of the window, and the turn's end sets the exact number. The window
	// is known only after the first turn.
	if step := th.t.ContextWindow / 100; step > 0 && e.Tokens >= th.t.Context+step {
		th.t.Context = e.Tokens
		h.changed(ws, th)
	}
	switch e.Kind {
	case agent.KindToolStart:
		h.event(ws, th, model.Event{Kind: model.EventTool, Tool: e.Tool, Detail: e.Detail, ID: e.ID})
	case agent.KindToolFailed:
		h.event(ws, th, model.Event{Kind: model.EventTool, ID: e.ID, Exit: 1, Text: e.Text})
	case agent.KindAssistantText:
		h.event(ws, th, model.Event{Kind: model.EventText, Text: e.Text})
	case agent.KindError:
		h.event(ws, th, model.Event{Kind: model.EventError, Text: e.Text})
	}
}

// readBranch reads a thread's git branch in the background: over ssh it is a
// round trip, and callers hold h.mu.
func (h *Hub) readBranch(ws *space, th *thread) {
	if h.cfg.Branch == nil {
		return
	}
	loc := th.t.Location
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		ctx, cancel := context.WithTimeout(h.ctx, 10*time.Second)
		defer cancel()
		b, err := h.cfg.Branch(ctx, loc)
		if err != nil {
			b = ""
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if th.branch != b {
			th.branch = b
			h.changed(ws, th)
		}
	}()
}

// agentOf names the agent of the thread's role: the workspace's choice, then
// the config's. Threads that have none of their own run the coordinator's.
// A name that is no longer in the config does not count.
func (h *Hub) agentOf(ws *space, t *workspace.Thread) string {
	names := []string{ws.p.Coordinator, h.cfg.Coordinator}
	if t.Role != model.RoleCoordinator {
		names = []string{ws.p.Threads, h.cfg.Threads, ws.p.Coordinator, h.cfg.Coordinator}
	}
	for _, n := range names {
		if _, ok := h.cfg.Agents[n]; ok {
			return n
		}
	}
	return ""
}

func (h *Hub) defaults(ws *space, t *workspace.Thread) Defaults {
	return h.cfg.Agents[h.agentOf(ws, t)]
}

func (h *Hub) systemPrompt(ws *space, th *thread) string {
	in := prompts.SessionInput{Workspace: ws.p.Name, Name: th.t.Name, Location: th.t.Location.String(), Notify: h.cfg.Notify}
	if th.t.Role == model.RoleCoordinator {
		return prompts.Coordinator(in)
	}
	return prompts.Thread(in)
}

// compose turns a batch of queued messages into one prompt. Messages from
// anyone but the user are labelled, so the agent knows who to answer. An
// `@name` of a thread gets a note that says how to read it, not the thread's
// history: the agent decides how much of it it needs.
func compose(batch []workspace.Message, threads map[string]*thread) string {
	parts := make([]string, 0, len(batch))
	var named []string
	for _, m := range batch {
		if m.From == model.User {
			parts = append(parts, m.Text)
		} else {
			parts = append(parts, "Message from "+m.From+":\n"+m.Text)
		}
		for _, w := range strings.Fields(m.Text) {
			// `@api,` at the end of a clause still names api; `@api.go` is a
			// file, because the whole word must be a thread name.
			n, ok := strings.CutPrefix(strings.TrimRight(w, ".,;:!?)"), "@")
			if _, thread := threads[n]; ok && thread && !slices.Contains(named, n) {
				named = append(named, n)
			}
		}
	}
	prompt := strings.Join(parts, "\n\n---\n\n")
	for _, n := range named {
		prompt += "\n\n@" + n + " is the thread " + n + " in this workspace. Read its transcript with read_thread if you need it."
	}
	return prompt
}

// board is every open thread, for the start of a coordinator turn, so the
// coordinator manages without asking for the status. A thread that ended a
// turn since its last turn adds the start of its reply, unless its message
// is in the batch and says it there. Callers hold h.mu.
func (h *Hub) board(ws *space, batch []workspace.Message) string {
	var lines []string
	for _, n := range slices.Sorted(maps.Keys(ws.threads)) {
		th := ws.threads[n]
		if n == model.Coordinator || th.deleted || th.t.Resolved {
			continue
		}
		ended := slices.Contains(ws.ended, n) &&
			!slices.ContainsFunc(batch, func(m workspace.Message) bool { return m.From == n })
		lines = append(lines, prompts.BoardLine(n, string(th.state), th.t.Progress, th.reply, ended))
	}
	ws.ended = nil
	if len(lines) == 0 {
		return ""
	}
	return prompts.Board(lines)
}

// --- state and updates ---

// derive computes the visible state. A pending permission outranks a running
// turn, because it is the reason the turn is not moving.
func (th *thread) derive() model.State {
	t := th.t
	switch {
	case t.Resolved:
		return model.StateResolved
	// A question waits for the person even while its turn still runs: claude
	// -p stays up until its background tasks end.
	case len(th.perms) > 0 || t.Asking != "":
		return model.StateWaiting
	case th.busy:
		return model.StateRunning
	case t.Paused:
		return model.StateWaiting
	case len(t.Queue) > 0:
		return model.StateRunning
	}
	return model.StateIdle
}

// changed saves a thread and tells subscribers. Callers hold h.mu.
func (h *Hub) changed(ws *space, th *thread) {
	if th.deleted {
		return
	}
	if s := th.derive(); s != th.state {
		th.state = s
		th.t.Since = time.Now().UTC()
	}
	_ = workspace.SaveThread(ws.p.Name, th.t)
	v := h.view(ws, th)
	h.publish(model.Update{Kind: model.UpdateThread, Workspace: ws.p.Name, Thread: th.t.Name, View: &v})
	h.publishWorkspace(ws)
}

func (h *Hub) publishWorkspace(ws *space) {
	s := h.summary(ws)
	h.publish(model.Update{Kind: model.UpdateWorkspace, Workspace: ws.p.Name, Summary: &s})
}

// event appends to a transcript and tells subscribers. Callers hold h.mu.
func (h *Hub) event(ws *space, th *thread, e model.Event) {
	if th.deleted {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	_ = workspace.AppendEvent(ws.p.Name, th.t.Name, e)
	h.publish(model.Update{Kind: model.UpdateEvent, Workspace: ws.p.Name, Thread: th.t.Name, Event: &e})
}

// Subscribe returns a channel of every update from now on. A subscriber that
// falls too far behind has its channel closed, so it can reconnect and read
// state afresh instead of showing a transcript with holes in it.
func (h *Hub) Subscribe() (<-chan model.Update, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.nextSub
	h.nextSub++
	ch := make(chan model.Update, 4096)
	h.subs[id] = ch
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if c, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(c)
		}
	}
}

func (h *Hub) publish(u model.Update) {
	for id, ch := range h.subs {
		select {
		case ch <- u:
		default:
			delete(h.subs, id)
			close(ch)
		}
	}
}

// unshown are the queued messages that are not in the transcript yet.
func unshown(q []workspace.Message) []model.Event {
	var out []model.Event
	for _, m := range q {
		if !m.Shown {
			out = append(out, model.Event{At: m.At, Kind: model.EventMessage, From: m.From, Text: m.Text})
		}
	}
	return out
}

func (h *Hub) view(ws *space, th *thread) model.ThreadView {
	t := th.t
	v := model.ThreadView{
		Workspace:     ws.p.Name,
		Name:          t.Name,
		Role:          t.Role,
		Location:      t.Location,
		State:         th.state,
		Queued:        len(t.Queue),
		Pending:       unshown(t.Queue),
		Progress:      t.Progress,
		WakeAt:        t.WakeAt,
		Model:         t.Model,
		Context:       t.Context,
		ContextWindow: t.ContextWindow,
		Cost:          t.Cost,
		Branch:        th.branch,
		Quiet:         h.cfg.Notify,
		Since:         t.Since,
		UpdatedAt:     t.UpdatedAt,
		CreatedAt:     t.CreatedAt,
	}
	// A report from before the turn that runs now is old news: "blocked,
	// asking the user" reads wrong once the answer came.
	if th.state == model.StateRunning && t.ProgressAt.Before(t.Since) {
		v.Progress = ""
	}
	switch {
	case len(th.perms) > 0:
		p := th.perms[0].Permission
		v.Permission = &p
		v.Waiting = "permission"
	case t.Asking != "":
		v.Waiting = t.Asking
	case t.Paused:
		v.Waiting = "paused"
	}
	return v
}

func (h *Hub) views(ws *space) []model.ThreadView {
	out := make([]model.ThreadView, 0, len(ws.threads))
	for _, th := range ws.threads {
		out = append(out, h.view(ws, th))
	}
	slices.SortFunc(out, func(a, b model.ThreadView) int {
		if (a.Role == model.RoleCoordinator) != (b.Role == model.RoleCoordinator) {
			if a.Role == model.RoleCoordinator {
				return -1
			}
			return 1
		}
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return out
}

func (h *Hub) summary(ws *space) model.WorkspaceView {
	v := model.WorkspaceView{
		Name:             ws.p.Name,
		Pin:              ws.p.Pin,
		CreatedAt:        ws.p.CreatedAt,
		Counts:           map[model.State]int{},
		Activity:         make([]int, 24),
		CoordinatorAgent: h.agentOf(ws, &workspace.Thread{Role: model.RoleCoordinator}),
		ThreadsAgent:     h.agentOf(ws, &workspace.Thread{Role: model.RoleThread}),
		AgentNames:       slices.Sorted(maps.Keys(h.cfg.Agents)),
	}
	now := time.Now()
	for _, th := range ws.threads {
		if th.t.Role == model.RoleThread {
			v.Counts[th.state]++
		} else {
			v.Coordinator = th.state
		}
		for _, at := range th.t.Turns {
			if age := int(now.Sub(at).Hours()); age >= 0 && age < 24 {
				v.Activity[23-age]++
			}
		}
	}
	return v
}

func (h *Hub) workspace(name string) (*space, error) {
	ws, ok := h.workspaces[name]
	if !ok {
		return nil, fmt.Errorf("no workspace %q", name)
	}
	return ws, nil
}

func (h *Hub) lookup(workspaceName, name string) (*space, *thread, error) {
	ws, err := h.workspace(workspaceName)
	if err != nil {
		return nil, nil, err
	}
	th, ok := ws.threads[name]
	if !ok {
		return nil, nil, fmt.Errorf("no thread %q in %s", name, workspaceName)
	}
	return ws, th, nil
}

// --- helpers ---

func lastLine(texts ...string) string {
	for _, s := range texts {
		lines := strings.Split(strings.TrimSpace(s), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if l := strings.TrimSpace(lines[i]); l != "" {
				return l
			}
		}
	}
	return "no output"
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
