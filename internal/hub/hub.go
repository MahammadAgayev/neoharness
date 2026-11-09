// Package hub owns every project and thread while neoharness runs: it queues
// messages, runs one turn at a time per thread, routes messages between
// threads, holds permission requests until the user answers, and pushes every
// change to subscribers.
//
// The hub is the only writer of project state. It knows agents only through
// agent.Agent and a factory, and knows nothing of MCP, HTTP, ssh, or the TUI;
// those are thin adapters around the methods here. That is what lets its tests
// drive it with a scripted fake in milliseconds.
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MahammadAgayev/neoharness/internal/agent"
	"github.com/MahammadAgayev/neoharness/internal/model"
	"github.com/MahammadAgayev/neoharness/internal/project"
	"github.com/MahammadAgayev/neoharness/internal/prompts"
)

// Defaults.
const (
	// MaxAgentTurns stops two threads from messaging each other forever. After
	// this many turns in a row that the user did not start, a thread waits
	// for the user.
	MaxAgentTurns = 20
	// TurnTimeout catches a wedged turn, not a slow one.
	TurnTimeout = 4 * time.Hour
	// PermissionTool is the MCP tool Claude asks before a tool call it may not
	// make on its own.
	PermissionTool = harnessTools + "permission_prompt"
	harnessTools   = "mcp__neoharness__"
	// mcpToolTimeout keeps Claude from giving up on a permission prompt
	// while the user is away from the keyboard.
	mcpToolTimeout = "MCP_TOOL_TIMEOUT=86400000"
)

// AgentFactory builds the agent that runs a thread's turns.
type AgentFactory func(p project.Project, t project.Thread) agent.Agent

// Config is how the hub is wired.
type Config struct {
	Factory AgentFactory
	// MCPPort is where the MCP server listens, on 127.0.0.1 here and, through
	// the ssh tunnel, on every remote host.
	MCPPort       int
	MaxAgentTurns int
	TurnTimeout   time.Duration
}

// Hub is the running state of every project.
type Hub struct {
	cfg Config

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	projects map[string]*proj
	tokens   map[string]ident
	subs     map[int]chan model.Update
	nextSub  int
	nextPerm int
}

type proj struct {
	p       project.Project
	threads map[string]*thread
}

type thread struct {
	t       *project.Thread
	busy    bool
	started bool // the current turn produced output, so its session exists
	cancel  context.CancelFunc
	perms   []*pending
	state   model.State
}

type pending struct {
	model.Permission
	prefix string
	done   chan model.Decision
	msg    string
}

type ident struct {
	project string
	thread  string
}

// Identity is who an MCP token belongs to.
type Identity struct {
	Project string
	Thread  string
	Role    model.Role
}

// New loads every project from disk and resumes any queued work.
func New(cfg Config) (*Hub, error) {
	if cfg.MaxAgentTurns == 0 {
		cfg.MaxAgentTurns = MaxAgentTurns
	}
	if cfg.TurnTimeout == 0 {
		cfg.TurnTimeout = TurnTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &Hub{
		cfg:      cfg,
		ctx:      ctx,
		cancel:   cancel,
		projects: map[string]*proj{},
		tokens:   map[string]ident{},
		subs:     map[int]chan model.Update{},
	}
	ps, err := project.List()
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range ps {
		pr := &proj{p: p, threads: map[string]*thread{}}
		h.projects[p.Name] = pr
		ts, err := project.Threads(p.Name)
		if err != nil {
			return nil, err
		}
		for _, t := range ts {
			th := &thread{t: t}
			pr.threads[t.Name] = th
			h.tokens[t.Token] = ident{p.Name, t.Name}
			if len(t.InFlight) > 0 {
				// The hub died with this turn running, and its claude process
				// died with it. Say so instead of pretending it finished.
				t.InFlight = nil
				h.event(pr, th, model.Event{Kind: model.EventError,
					Text: "The hub stopped during this turn, so it was lost. Send the message again to retry."})
			}
			th.state = th.derive()
			_ = project.SaveThread(p.Name, t)
		}
	}
	for _, pr := range h.projects {
		for _, th := range pr.threads {
			h.kick(pr, th)
		}
	}
	return h, nil
}

// Close cancels every running turn and waits for them to unwind.
func (h *Hub) Close() {
	h.cancel()
	h.wg.Wait()
}

// --- projects ---

// CreateProject creates a project and its coordinator. pin is an optional
// local folder.
func (h *Hub) CreateProject(name, pin string) error {
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
	if _, ok := h.projects[name]; ok {
		return fmt.Errorf("project %q already exists", name)
	}
	p := project.Project{Name: name, Pin: pin, CreatedAt: time.Now().UTC()}
	if err := project.Create(p); err != nil {
		return err
	}
	pr := &proj{p: p, threads: map[string]*thread{}}
	h.projects[name] = pr

	dir := pin
	if dir == "" {
		// An unpinned project still needs somewhere for its coordinator to
		// stand; its own folder is the one place that certainly exists.
		dir = filepath.Join(project.Dir(name), "workspace")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if _, err := h.addThread(pr, model.Coordinator, model.RoleCoordinator, model.Location{Dir: dir}); err != nil {
		return err
	}
	h.publishProject(pr)
	return nil
}

// Projects lists every project with a summary.
func (h *Hub) Projects() []model.ProjectView {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]model.ProjectView, 0, len(h.projects))
	for _, pr := range h.projects {
		out = append(out, h.summary(pr))
	}
	slices.SortFunc(out, func(a, b model.ProjectView) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out
}

// Threads lists a project's threads: the coordinator first, then by age.
func (h *Hub) Threads(projectName string) ([]model.ThreadView, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pr, err := h.project(projectName)
	if err != nil {
		return nil, err
	}
	return h.views(pr), nil
}

// Events returns the last n transcript events of a thread.
func (h *Hub) Events(projectName, threadName string, n int) ([]model.Event, error) {
	h.mu.Lock()
	_, _, err := h.lookup(projectName, threadName)
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return project.Events(projectName, threadName, n)
}

// --- threads ---

// OpenThread creates a thread and, if brief is not empty, sends it as the
// first message. by is the user or the coordinator. loc is "host:dir", a
// local folder, or empty for the project folder.
func (h *Hub) OpenThread(projectName, by, name, brief, loc string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	pr, err := h.project(projectName)
	if err != nil {
		return err
	}
	if err := model.ValidName(name); err != nil {
		return err
	}
	if name == model.Coordinator || name == model.User {
		return fmt.Errorf("%q is reserved", name)
	}
	if _, ok := pr.threads[name]; ok {
		return fmt.Errorf("thread %q already exists in %s; choose another name", name, projectName)
	}
	var l model.Location
	if strings.TrimSpace(loc) == "" {
		if pr.p.Pin == "" {
			return fmt.Errorf("project %s is not pinned to a folder; give a location (a folder or host:dir)", projectName)
		}
		l = model.Location{Dir: pr.p.Pin}
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
	th, err := h.addThread(pr, name, model.RoleThread, l)
	if err != nil {
		return err
	}
	h.event(pr, th, model.Event{Kind: model.EventNote, Text: "Opened by " + by + " in " + l.String()})
	if strings.TrimSpace(brief) != "" {
		h.enqueue(pr, th, by, brief)
	}
	return nil
}

func (h *Hub) addThread(pr *proj, name string, role model.Role, l model.Location) (*thread, error) {
	now := time.Now().UTC()
	t := &project.Thread{
		Name:      name,
		Role:      role,
		Location:  l,
		SessionID: project.NewID(),
		Token:     project.NewToken(),
		Queue:     []project.Message{},
		Since:     now,
		CreatedAt: now,
	}
	if err := project.SaveThread(pr.p.Name, t); err != nil {
		return nil, err
	}
	th := &thread{t: t, state: model.StateIdle}
	pr.threads[name] = th
	h.tokens[t.Token] = ident{pr.p.Name, name}
	h.changed(pr, th)
	return th, nil
}

// Send queues a message for a thread. from is model.User, the coordinator, or
// another thread's name. A message from the user also answers any question the
// thread asked and lifts a loop-guard pause.
func (h *Hub) Send(projectName, from, to, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("empty message")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	pr, th, err := h.lookup(projectName, to)
	if err != nil {
		return err
	}
	if from == to {
		return errors.New("a thread cannot message itself")
	}
	if th.t.Resolved {
		return fmt.Errorf("thread %q is resolved; reopen it first", to)
	}
	h.enqueue(pr, th, from, text)
	return nil
}

func (h *Hub) enqueue(pr *proj, th *thread, from, text string) {
	t := th.t
	if from == model.User {
		t.AgentTurns = 0
		t.Paused = false
		t.Asking = ""
	}
	t.Queue = append(t.Queue, project.Message{From: from, Text: text, At: time.Now().UTC()})
	h.event(pr, th, model.Event{Kind: model.EventMessage, From: from, Text: text})
	h.changed(pr, th)
	h.kick(pr, th)
}

// Resolve closes a thread. A running turn is cancelled and queued messages
// are dropped. The coordinator cannot be resolved.
func (h *Hub) Resolve(projectName, by, name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	pr, th, err := h.lookup(projectName, name)
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
	t.Queue = []project.Message{}
	t.Asking = ""
	t.Paused = false
	if th.cancel != nil {
		th.cancel()
	}
	h.denyAll(th, "the thread was resolved")
	h.event(pr, th, model.Event{Kind: model.EventNote, Text: "Resolved by " + by})
	h.changed(pr, th)
	return nil
}

// Reopen brings a resolved thread back. It keeps its Claude session, so it
// remembers everything before it was resolved.
func (h *Hub) Reopen(projectName, name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	pr, th, err := h.lookup(projectName, name)
	if err != nil {
		return err
	}
	if !th.t.Resolved {
		return nil
	}
	th.t.Resolved = false
	th.t.AgentTurns = 0
	h.event(pr, th, model.Event{Kind: model.EventNote, Text: "Reopened"})
	h.changed(pr, th)
	return nil
}

// ReportProgress sets a thread's progress line.
func (h *Hub) ReportProgress(projectName, name, summary string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	pr, th, err := h.lookup(projectName, name)
	if err != nil {
		return err
	}
	summary = strings.TrimSpace(summary)
	th.t.Progress = summary
	th.t.ProgressAt = time.Now().UTC()
	h.event(pr, th, model.Event{Kind: model.EventProgress, Text: summary})
	h.changed(pr, th)
	return nil
}

// AskUser posts a question to the user. The thread waits until the user
// writes to it.
func (h *Hub) AskUser(projectName, name, question string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	pr, th, err := h.lookup(projectName, name)
	if err != nil {
		return err
	}
	th.t.Asking = strings.TrimSpace(question)
	h.event(pr, th, model.Event{Kind: model.EventAsk, Text: th.t.Asking})
	h.changed(pr, th)
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
	pr := h.projects[id.project]
	th := pr.threads[id.thread]
	return Identity{Project: id.project, Thread: id.thread, Role: th.t.Role}, true
}

// Whoami describes a thread to itself.
func (h *Hub) Whoami(projectName, name string) (model.ThreadView, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pr, th, err := h.lookup(projectName, name)
	if err != nil {
		return model.ThreadView{}, err
	}
	return h.view(pr, th), nil
}

// --- permissions ---

// Permission blocks until the user answers a permission request, or a rule
// answers it, or the turn ends. It returns whether the call may go ahead and,
// for a denial, the user's reason.
func (h *Hub) Permission(ctx context.Context, projectName, name, tool string, input json.RawMessage) (bool, string) {
	h.mu.Lock()
	pr, th, err := h.lookup(projectName, name)
	if err != nil {
		h.mu.Unlock()
		return false, err.Error()
	}
	// The neoharness tools only reach this hub, and a session that had to ask
	// before reporting progress would wait on the user for nothing.
	if strings.HasPrefix(tool, harnessTools) {
		h.mu.Unlock()
		return true, ""
	}
	detail := inputDetail(tool, input)
	prefix, chained := rulePrefix(tool, input)
	for _, r := range th.t.Rules {
		if r.Tool == tool && !chained && (r.Prefix == "" || r.Prefix == prefix) {
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
	h.event(pr, th, model.Event{Kind: model.EventPermission, ID: p.ID, Tool: tool, Detail: detail})
	h.changed(pr, th)
	h.mu.Unlock()

	var d model.Decision
	select {
	case d = <-p.done:
	case <-ctx.Done():
		d = model.Deny
		h.mu.Lock()
		h.dropPerm(pr, th, p.ID)
		h.mu.Unlock()
	case <-h.ctx.Done():
		return false, "neoharness is shutting down"
	}
	return d != model.Deny, p.msg
}

// Answer resolves a pending permission request.
func (h *Hub) Answer(projectName, name, id string, d model.Decision, msg string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	pr, th, err := h.lookup(projectName, name)
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
		th.t.Rules = append(th.t.Rules, project.Rule{Tool: p.Tool, Prefix: p.prefix})
	}
	p.msg = msg
	text := string(d)
	if msg != "" {
		text += ": " + msg
	}
	h.event(pr, th, model.Event{Kind: model.EventAnswer, ID: p.ID, Tool: p.Tool, Text: text})
	h.dropPerm(pr, th, p.ID)
	p.done <- d
	return nil
}

func (h *Hub) dropPerm(pr *proj, th *thread, id string) {
	th.perms = slices.DeleteFunc(th.perms, func(p *pending) bool { return p.ID == id })
	h.changed(pr, th)
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
func (h *Hub) kick(pr *proj, th *thread) {
	t := th.t
	if th.busy || t.Resolved || t.Paused || len(t.Queue) == 0 {
		return
	}
	fromUser := slices.ContainsFunc(t.Queue, func(m project.Message) bool { return m.From == model.User })
	if !fromUser && t.AgentTurns >= h.cfg.MaxAgentTurns {
		t.Paused = true
		h.event(pr, th, model.Event{Kind: model.EventNote,
			Text: fmt.Sprintf("Paused after %d turns without your input. Write to continue.", t.AgentTurns)})
		h.changed(pr, th)
		return
	}
	if !fromUser {
		t.AgentTurns++
	}
	batch := t.Queue
	t.Queue = []project.Message{}
	t.InFlight = batch
	now := time.Now().UTC()
	t.Turns = append(slices.DeleteFunc(t.Turns, func(at time.Time) bool { return now.Sub(at) > 24*time.Hour }), now)

	ctx, cancel := context.WithTimeout(h.ctx, h.cfg.TurnTimeout)
	th.busy = true
	th.started = false
	th.cancel = cancel
	h.changed(pr, th)

	req := agent.Request{
		Prompt:               compose(batch),
		SystemPrompt:         h.systemPrompt(pr, th),
		Conversation:         t.SessionID,
		Resume:               t.Started,
		MCPConfig:            h.mcpConfig(t.Token),
		PermissionPromptTool: PermissionTool,
		Env:                  []string{mcpToolTimeout},
	}
	if !t.Location.Remote() {
		req.Cwd = t.Location.Dir
	}
	req.OnEvent = func(e agent.Event) { h.onAgentEvent(pr, th, e) }
	ag := h.cfg.Factory(pr.p, *t)

	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer cancel()
		res, err := ag.Run(ctx, req)
		h.endTurn(pr, th, ctx, res, err)
	}()
}

func (h *Hub) endTurn(pr *proj, th *thread, ctx context.Context, res agent.Result, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t := th.t
	th.busy = false
	th.cancel = nil
	t.InFlight = nil
	h.denyAll(th, "the turn ended")
	if th.started {
		t.Started = true
	}
	switch {
	case h.ctx.Err() != nil:
		// Shutting down. The next start reports nothing lost, because nothing
		// is in flight on disk any more; say it here while we still can.
		h.event(pr, th, model.Event{Kind: model.EventError, Text: "neoharness stopped during this turn"})
	case t.Resolved:
		// Cancelled by Resolve; the note already says so.
	case ctx.Err() == context.DeadlineExceeded:
		h.event(pr, th, model.Event{Kind: model.EventError, Text: fmt.Sprintf("the turn ran past %s and was stopped", h.cfg.TurnTimeout)})
	case err != nil:
		h.event(pr, th, model.Event{Kind: model.EventError, Text: err.Error()})
	case res.ExitCode != 0:
		h.event(pr, th, model.Event{Kind: model.EventError, Text: "claude failed: " + lastLine(res.Stderr, res.Text)})
	default:
		t.Started = true
		h.event(pr, th, model.Event{Kind: model.EventTurnEnd, Tools: res.Tools, Tokens: res.Tokens})
	}
	h.changed(pr, th)
	if h.ctx.Err() == nil {
		h.kick(pr, th)
	}
}

func (h *Hub) onAgentEvent(pr *proj, th *thread, e agent.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	th.started = true
	switch e.Kind {
	case agent.KindToolStart:
		h.event(pr, th, model.Event{Kind: model.EventTool, Tool: e.Tool, Detail: e.Detail})
	case agent.KindAssistantText:
		h.event(pr, th, model.Event{Kind: model.EventText, Text: e.Text})
	case agent.KindError:
		h.event(pr, th, model.Event{Kind: model.EventError, Text: e.Text})
	}
}

func (h *Hub) systemPrompt(pr *proj, th *thread) string {
	in := prompts.SessionInput{Project: pr.p.Name, Name: th.t.Name, Location: th.t.Location.String()}
	if th.t.Role == model.RoleCoordinator {
		return prompts.Coordinator(in)
	}
	return prompts.Thread(in)
}

func (h *Hub) mcpConfig(token string) string {
	cfg := map[string]any{"mcpServers": map[string]any{"neoharness": map[string]any{
		"type":    "http",
		"url":     fmt.Sprintf("http://127.0.0.1:%d/mcp", h.cfg.MCPPort),
		"headers": map[string]string{"Authorization": "Bearer " + token},
	}}}
	data, _ := json.Marshal(cfg)
	return string(data)
}

// compose turns a batch of queued messages into one prompt. Messages from
// anyone but the user are labelled, so the agent knows who to answer.
func compose(batch []project.Message) string {
	parts := make([]string, 0, len(batch))
	for _, m := range batch {
		if m.From == model.User {
			parts = append(parts, m.Text)
		} else {
			parts = append(parts, "Message from "+m.From+":\n"+m.Text)
		}
	}
	return strings.Join(parts, "\n\n---\n\n")
}

// --- state and updates ---

// derive computes the visible state. A pending permission outranks a running
// turn, because it is the reason the turn is not moving.
func (th *thread) derive() model.State {
	t := th.t
	switch {
	case t.Resolved:
		return model.StateResolved
	case len(th.perms) > 0:
		return model.StateWaiting
	case th.busy:
		return model.StateRunning
	case t.Asking != "" || t.Paused:
		return model.StateWaiting
	case len(t.Queue) > 0:
		return model.StateRunning
	}
	return model.StateIdle
}

// changed saves a thread and tells subscribers. Callers hold h.mu.
func (h *Hub) changed(pr *proj, th *thread) {
	if s := th.derive(); s != th.state {
		th.state = s
		th.t.Since = time.Now().UTC()
	}
	_ = project.SaveThread(pr.p.Name, th.t)
	v := h.view(pr, th)
	h.publish(model.Update{Kind: model.UpdateThread, Project: pr.p.Name, Thread: th.t.Name, View: &v})
	h.publishProject(pr)
}

func (h *Hub) publishProject(pr *proj) {
	s := h.summary(pr)
	h.publish(model.Update{Kind: model.UpdateProject, Project: pr.p.Name, Summary: &s})
}

// event appends to a transcript and tells subscribers. Callers hold h.mu.
func (h *Hub) event(pr *proj, th *thread, e model.Event) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	_ = project.AppendEvent(pr.p.Name, th.t.Name, e)
	h.publish(model.Update{Kind: model.UpdateEvent, Project: pr.p.Name, Thread: th.t.Name, Event: &e})
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

func (h *Hub) view(pr *proj, th *thread) model.ThreadView {
	t := th.t
	v := model.ThreadView{
		Project:   pr.p.Name,
		Name:      t.Name,
		Role:      t.Role,
		Location:  t.Location,
		State:     th.state,
		Queued:    len(t.Queue),
		Progress:  t.Progress,
		Since:     t.Since,
		UpdatedAt: t.UpdatedAt,
		CreatedAt: t.CreatedAt,
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

func (h *Hub) views(pr *proj) []model.ThreadView {
	out := make([]model.ThreadView, 0, len(pr.threads))
	for _, th := range pr.threads {
		out = append(out, h.view(pr, th))
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

func (h *Hub) summary(pr *proj) model.ProjectView {
	v := model.ProjectView{
		Name:      pr.p.Name,
		Pin:       pr.p.Pin,
		CreatedAt: pr.p.CreatedAt,
		Counts:    map[model.State]int{},
		Activity:  make([]int, 24),
	}
	now := time.Now()
	for _, th := range pr.threads {
		if th.t.Role == model.RoleThread {
			v.Counts[th.state]++
		}
		for _, at := range th.t.Turns {
			if age := int(now.Sub(at).Hours()); age >= 0 && age < 24 {
				v.Activity[23-age]++
			}
		}
	}
	return v
}

func (h *Hub) project(name string) (*proj, error) {
	pr, ok := h.projects[name]
	if !ok {
		return nil, fmt.Errorf("no project %q", name)
	}
	return pr, nil
}

func (h *Hub) lookup(projectName, name string) (*proj, *thread, error) {
	pr, err := h.project(projectName)
	if err != nil {
		return nil, nil, err
	}
	th, ok := pr.threads[name]
	if !ok {
		return nil, nil, fmt.Errorf("no thread %q in %s", name, projectName)
	}
	return pr, th, nil
}

// --- helpers ---

// inputDetail picks the part of a tool's input worth showing in a permission
// request: the command, the path, or else the first string argument.
func inputDetail(tool string, input json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	for _, k := range []string{"command", "file_path", "path", "url", "pattern"} {
		if s, ok := m[k].(string); ok && s != "" {
			return strings.Join(strings.Fields(s), " ")
		}
	}
	for _, v := range m {
		if s, ok := v.(string); ok && s != "" {
			return strings.Join(strings.Fields(s), " ")
		}
	}
	return ""
}

// rulePrefix is what an "allow always" remembers. For Bash it is the
// command's first word; for every other tool the whole tool is allowed.
//
// chained reports a Bash command that does more than one thing. Those are
// always asked about: an allowed `go` must not let `go test; rm -rf ~` through
// on the strength of its first word.
func rulePrefix(tool string, input json.RawMessage) (prefix string, chained bool) {
	if tool != "Bash" {
		return "", false
	}
	var m struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(input, &m)
	chained = strings.ContainsAny(m.Command, ";&|`<>\n") || strings.Contains(m.Command, "$(")
	if f := strings.Fields(m.Command); len(f) > 0 {
		return f[0], chained
	}
	return "", chained
}

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
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
