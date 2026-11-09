// Package tui is the neoharness workspace: a terminal UI with Neovim's
// fundamentals. Every view is a buffer, buffers are shown in windows, windows
// split, and keys are modal: Normal to move and act, Insert to write to an
// agent, Command-line for `:` commands, and <Space> as the leader for
// pickers.
//
// The TUI holds no state the hub does not also hold. It talks to the hub
// only through hubapi, so it can be closed and reopened at any time.
package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/internal/hubapi"
	"github.com/MahammadAgayev/neoharness/internal/model"
)

// Client is the part of hubapi the TUI uses. An interface so tests can drive
// the UI without a hub.
type Client interface {
	Workspaces() ([]model.WorkspaceView, error)
	CreateWorkspace(name, pin string) error
	SetFolder(name, pin string) error
	DeleteWorkspace(name string) error
	Threads(workspace string) ([]model.ThreadView, error)
	OpenThread(workspace, name, brief, location string) error
	Events(workspace, thread string, tail int) ([]model.Event, error)
	Completions(workspace, thread string) (model.Completions, error)
	Send(workspace, thread, text string) error
	SendNow(workspace, thread, text string) error
	Resolve(workspace, thread string) error
	Delete(workspace, thread string) error
	Reopen(workspace, thread string) error
	Answer(workspace, thread, id string, d model.Decision, msg string) error
	Updates(ctx context.Context) (<-chan model.Update, error)
}

var _ Client = (*hubapi.Client)(nil)

// historyTail is how much of each transcript is loaded on open.
const historyTail = 2000

type mode int

const (
	modeNormal mode = iota
	modeInsert
	modeVisual
	modeCommand
	modePicker
)

// Custom screen events.
type (
	evUpdate     struct{ u model.Update }
	evResync     struct{}
	evDisconnect struct{}
	evTick       struct{}
	evKeyTimeout struct{ seq int }
	evComplete   struct {
		buf string
		c   model.Completions
	}
)

func (evUpdate) When() time.Time     { return time.Now() }
func (evResync) When() time.Time     { return time.Now() }
func (evDisconnect) When() time.Time { return time.Now() }
func (evTick) When() time.Time       { return time.Now() }
func (evKeyTimeout) When() time.Time { return time.Now() }
func (evComplete) When() time.Time   { return time.Now() }

// App is the whole UI.
type App struct {
	s  tcell.Screen
	c  Client
	th *Theme

	root *node
	cur  *Window
	bufs map[string]*Buffer

	workspaces []model.WorkspaceView
	workspace  string
	threads    map[string]model.ThreadView
	order      []string

	workspacesBuf *Buffer
	threadsBuf    *Buffer
	// pane is the window on the right that <leader>t opens. It shows the
	// threads list, and the threads opened from it.
	pane         *Window
	showResolved bool
	messagesBuf  *Buffer
	messages     []Line

	mode     mode
	binds    map[string]func(int)
	pending  []string
	pendSeq  int
	count    int
	cmd      *cmdline
	picker   *picker
	menu     *menu
	toasts   []*toast
	menuShut int // the word whose menu <Esc> closed, or -1
	ctrlX    bool
	// confirm runs on y; any other key drops it.
	confirm  func()
	comps    map[string]model.Completions
	search   string
	notice   string
	noticeSt tcell.Style
	offline  bool

	cursorX int
	cursorY int
	spin    int
	ticking bool
	quit    bool
	pasting bool
	restart func() error
}

// Run opens the workspace on a terminal and blocks until the user quits.
// restart starts the hub again when it stops answering.
func Run(c Client, restart func() error) error {
	s, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	if err := s.Init(); err != nil {
		return err
	}
	defer s.Fini()
	s.EnablePaste()
	a := newApp(s, c)
	a.restart = restart
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.listen(ctx)
	a.loop()
	return nil
}

func newApp(s tcell.Screen, c Client) *App {
	a := &App{
		s:        s,
		c:        c,
		th:       newTheme(),
		bufs:     map[string]*Buffer{},
		threads:  map[string]model.ThreadView{},
		comps:    map[string]model.Completions{},
		menuShut: -1,
	}
	a.workspacesBuf = newBuffer("neoharness://workspaces", KindWorkspaces)
	a.messagesBuf = newBuffer("neoharness://messages", KindMessages)
	a.bufs[a.workspacesBuf.Name] = a.workspacesBuf
	a.bufs[a.messagesBuf.Name] = a.messagesBuf
	a.cmd = &cmdline{}
	a.binds = a.normalBindings()
	a.loadWorkspaces()
	a.showWorkspaces()
	return a
}

func (a *App) loop() {
	for !a.quit {
		a.draw()
		a.handle(a.s.PollEvent())
		// Drain whatever else is queued before drawing again, so a burst of
		// hub events costs one frame, not one each.
		for !a.quit && a.s.HasPendingEvent() {
			a.handle(a.s.PollEvent())
		}
	}
}

func (a *App) handle(ev tcell.Event) {
	switch ev := ev.(type) {
	case nil:
		a.quit = true
	case *tcell.EventKey:
		a.key(ev)
	case *tcell.EventPaste:
		a.pasting = ev.Start()
	case *tcell.EventResize:
		a.s.Sync()
	case evUpdate:
		a.apply(ev.u)
	case evResync:
		if a.offline {
			a.notify("reconnected to the hub", a.th.Foam)
		}
		a.offline = false
		a.resync()
	case evDisconnect:
		if !a.offline {
			a.offline = true
			a.notify("lost the hub; reconnecting…", a.th.Love)
		}
	case evRedraw:
	case evTick:
		a.ticking = false
		a.spin++
	case evKeyTimeout:
		if ev.seq == a.pendSeq && len(a.pending) > 0 {
			a.flushPending()
		}
	case evComplete:
		a.comps[ev.buf] = ev.c
		if a.mode == modeInsert && a.cur.Buf.Name == ev.buf {
			a.updateMenu()
		}
	}
}

// listen streams hub updates into the event loop, reconnecting forever.
func (a *App) listen(ctx context.Context) {
	for ctx.Err() == nil {
		ch, err := a.c.Updates(ctx)
		if err != nil {
			_ = a.s.PostEvent(evDisconnect{})
			if a.restart != nil {
				_ = a.restart()
			}
			time.Sleep(time.Second)
			continue
		}
		_ = a.s.PostEvent(evResync{})
		for u := range ch {
			_ = a.s.PostEvent(evUpdate{u})
		}
		_ = a.s.PostEvent(evDisconnect{})
		time.Sleep(500 * time.Millisecond)
	}
}

// --- state from the hub ---

func (a *App) loadWorkspaces() {
	ps, err := a.c.Workspaces()
	if err != nil {
		a.notify(err.Error(), a.th.Love)
		return
	}
	a.workspaces = ps
	a.renderWorkspaces()
}

func (a *App) createWorkspace(name, pin string) {
	// The hub runs in another folder, so a relative path must become absolute
	// here, where the user typed it.
	if pin != "" && !strings.HasPrefix(pin, "~") {
		if abs, err := filepath.Abs(pin); err == nil {
			pin = abs
		}
	}
	if err := a.c.CreateWorkspace(name, pin); err != nil {
		a.notify(err.Error(), a.th.Love)
		return
	}
	a.loadWorkspaces()
	a.openWorkspace(name)
	a.insert()
}

// resync reloads everything after a (re)connect, keeping windows on the
// buffers they showed.
func (a *App) resync() {
	a.loadWorkspaces()
	if a.workspace == "" {
		return
	}
	vs, err := a.c.Threads(a.workspace)
	if err != nil {
		a.notify(err.Error(), a.th.Love)
		return
	}
	a.threads = map[string]model.ThreadView{}
	a.order = nil
	for _, v := range vs {
		a.threads[v.Name] = v
		a.order = append(a.order, v.Name)
		a.loadHistory(v)
	}
	a.renderThreads()
}

func (a *App) loadHistory(v model.ThreadView) {
	b := a.chat(v.Name)
	evs, err := a.c.Events(a.workspace, v.Name, historyTail)
	if err != nil {
		a.notify(err.Error(), a.th.Love)
		return
	}
	b.blocks = nil
	for _, e := range evs {
		b.AddEvent(e)
	}
	b.settle(v.State == model.StateRunning)
}

func (a *App) apply(u model.Update) {
	switch u.Kind {
	case model.UpdateWorkspace:
		if u.Summary == nil {
			return
		}
		i := slices.IndexFunc(a.workspaces, func(p model.WorkspaceView) bool { return p.Name == u.Workspace })
		if i < 0 {
			a.workspaces = append(a.workspaces, *u.Summary)
		} else {
			a.workspaces[i] = *u.Summary
		}
		a.renderWorkspaces()
	case model.UpdateThread:
		if u.Workspace == a.workspace && u.View != nil {
			a.onView(*u.View)
		}
	case model.UpdateEvent:
		if u.Workspace == a.workspace && u.Event != nil {
			a.chat(u.Thread).AddEvent(*u.Event)
			if u.Event.Kind == model.EventError {
				a.toast(u.Thread, Span{"✗", a.th.Love}, u.Event.Text, a.th.Love, 8*time.Second)
			}
		}
	case model.UpdateDeleted:
		switch {
		case u.Thread == "":
			a.dropWorkspace(u.Workspace)
		case u.Workspace == a.workspace:
			a.dropThread(u.Thread)
		}
	}
}

// dropThread forgets a deleted thread. Windows that showed it show the
// coordinator instead.
func (a *App) dropThread(name string) {
	if _, ok := a.threads[name]; !ok {
		return
	}
	delete(a.threads, name)
	a.order = slices.DeleteFunc(a.order, func(n string) bool { return n == name })
	b := a.chat(name)
	delete(a.bufs, b.Name)
	delete(a.comps, b.Name)
	for _, w := range a.root.windows() {
		if w.Buf == b {
			w.show(a.chat(model.Coordinator))
		}
		if w.Alt == b {
			w.Alt = nil
		}
	}
	a.renderThreads()
	a.notify(name+" deleted", a.th.Muted)
}

// setFolder changes the open workspace's folder. A relative path is made
// absolute here, where the user typed it, as createWorkspace does.
func (a *App) setFolder(dir string) {
	if !strings.HasPrefix(dir, "~") {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
	}
	if err := a.c.SetFolder(a.workspace, dir); err != nil {
		a.notify(err.Error(), a.th.Love)
		return
	}
	a.notify(a.workspace+" now works in "+shortHome(dir), a.th.Pine)
}

// dropWorkspace forgets a deleted workspace, and leaves it for the
// workspaces screen if it was open.
func (a *App) dropWorkspace(name string) {
	a.workspaces = slices.DeleteFunc(a.workspaces, func(p model.WorkspaceView) bool { return p.Name == name })
	a.renderWorkspaces()
	if a.workspace == name {
		a.workspace = ""
		a.threads = map[string]model.ThreadView{}
		a.order = nil
		a.threadsBuf = nil
		a.showWorkspaces()
	}
	a.notify(name+" deleted", a.th.Muted)
}

// askDeleteWorkspace asks before hiding a workspace and stopping its turns.
func (a *App) askDeleteWorkspace(name string) {
	a.notice, a.noticeSt = "delete the workspace "+name+" and stop its threads? y/n", a.th.Gold
	a.confirm = func() {
		if err := a.c.DeleteWorkspace(name); err != nil {
			a.notify(err.Error(), a.th.Love)
		}
	}
}

// askDelete asks before deleting a thread: it cannot be undone.
func (a *App) askDelete(name string) {
	if name == model.Coordinator {
		a.notify("the coordinator cannot be deleted", a.th.Muted)
		return
	}
	a.notice, a.noticeSt = "delete "+name+" and its transcript? y/n", a.th.Gold
	a.confirm = func() {
		if err := a.c.Delete(a.workspace, name); err != nil {
			a.notify(err.Error(), a.th.Love)
		}
	}
}

func (a *App) onView(v model.ThreadView) {
	old, had := a.threads[v.Name]
	a.threads[v.Name] = v
	if !had {
		a.order = append(a.order, v.Name)
		a.chat(v.Name)
	}
	// A turn that ended may have reported the thread's first command list.
	if had && old.State == model.StateRunning && v.State != model.StateRunning {
		if b := a.chat(v.Name); a.mode == modeInsert && a.cur.Buf == b {
			a.fetchCompletions(b)
		}
	}
	// A stopped turn sends no turn end, so its tool calls would spin forever.
	if v.State == model.StateIdle || v.State == model.StateResolved {
		a.chat(v.Name).settle(false)
	}
	a.toastView(old, v, had)
	a.renderThreads()
}

func (a *App) focusedOn(thread string) bool {
	return a.cur != nil && a.cur.Buf.Kind == KindChat && a.cur.Buf.Thread == thread
}

// chat returns the chat buffer of a thread in the open workspace, creating it.
func (a *App) chat(thread string) *Buffer {
	name := chatName(a.workspace, thread)
	if b, ok := a.bufs[name]; ok {
		return b
	}
	b := newChat(a.workspace, thread)
	a.bufs[name] = b
	return b
}

// --- screens ---

func (a *App) showWorkspaces() {
	w := newWindow(a.workspacesBuf)
	w.Follow = false
	w.Cur = 4
	a.root = leaf(w, 1)
	a.cur = w
	a.mode = modeNormal
}

func (a *App) openWorkspace(name string) {
	if !slices.ContainsFunc(a.workspaces, func(p model.WorkspaceView) bool { return p.Name == name }) {
		a.notify("no workspace "+name, a.th.Love)
		return
	}
	a.workspace = name
	a.threadsBuf = newBuffer("neoharness://"+name+"/threads", KindThreads)
	a.bufs[a.threadsBuf.Name] = a.threadsBuf
	a.resync()

	coord := newWindow(a.chat(model.Coordinator))
	a.root = leaf(coord, 1)
	a.cur = coord
	a.pane = nil
	a.mode = modeNormal
}

func (a *App) windowOf(thread string) *Window {
	for _, w := range a.root.windows() {
		if w.Buf.Kind == KindChat && w.Buf.Thread == thread {
			return w
		}
	}
	return nil
}

func (a *App) knownThread(name string) bool {
	if a.workspace == "" {
		return false
	}
	if _, ok := a.threads[name]; !ok {
		a.notify("no thread "+name, a.th.Love)
		return false
	}
	return true
}

// toPane shows a buffer in the pane on the right and focuses it. The first
// call opens the pane at full height.
func (a *App) toPane(b *Buffer) {
	if a.pane == nil || a.root.find(a.pane) == nil {
		a.pane = newWindow(b)
		a.root = split(true, a.root, leaf(a.pane, 1))
	} else {
		a.pane.show(b)
	}
	a.cur = a.pane
}

// showList opens the threads list in the pane.
func (a *App) showList() {
	if a.workspace == "" {
		a.pickWorkspaces()
		return
	}
	a.toPane(a.threadsBuf)
	a.cur.Follow = false
}

// paneThread opens a thread in the pane.
func (a *App) paneThread(name string) {
	if a.knownThread(name) {
		a.toPane(a.chat(name))
	}
}

// splitThread opens a thread in a new split above the current window.
func (a *App) splitThread(name string) {
	if a.knownThread(name) {
		a.splitCur(false, a.chat(name))
	}
}

// showThread focuses the window that shows a thread, or shows the thread in
// the current window, as :b does.
func (a *App) showThread(name string) {
	if !a.knownThread(name) {
		return
	}
	if w := a.windowOf(name); w != nil {
		a.cur = w
		return
	}
	a.cur.show(a.chat(name))
}

// --- rendering lists ---

func (a *App) renderWorkspaces() {
	th := a.th
	lines := []Line{
		{},
		{Prefix: []Span{{"  ", th.Text}}, Spans: []Span{{"neoharness", th.Iris.Bold(true)}}},
		{},
		{Prefix: []Span{{"  ", th.Text}}, Spans: []Span{{"workspaces", th.Muted}}},
	}
	nameW, pinW := 8, 10
	for _, p := range a.workspaces {
		nameW = max(nameW, width(p.Name))
		pinW = max(pinW, width(displayPin(p.Pin)))
	}
	pinW = min(pinW, 40)
	for _, p := range a.workspaces {
		spans := []Span{
			{pad(p.Name, nameW+3), th.Text},
			{pad(truncate(displayPin(p.Pin), pinW), pinW+3), th.Muted},
		}
		spans = append(spans, countSpans(p.Counts, th)...)
		lines = append(lines, Line{Prefix: []Span{{"  ", th.Text}}, Spans: spans, Item: p.Name})
	}
	if len(a.workspaces) == 0 {
		lines = append(lines, Line{Prefix: []Span{{"  ", th.Text}}, Spans: []Span{{":new <name> [folder]", th.Muted}}})
	}
	a.workspacesBuf.SetLines(lines)
}

func displayPin(pin string) string {
	if pin == "" {
		return "not pinned"
	}
	return shortHome(pin)
}

func countSpans(c map[model.State]int, th *Theme) []Span {
	var out []Span
	for _, s := range []model.State{model.StateRunning, model.StateWaiting, model.StateResolved} {
		if n := c[s]; n > 0 {
			out = append(out, Span{fmt.Sprintf("%s %d  ", stateGlyph(s), n), th.state(s)})
		}
	}
	if len(out) == 0 {
		idle := c[model.StateIdle]
		label := "no threads"
		if idle > 0 {
			label = "all idle"
		}
		out = append(out, Span{"○ " + label, th.Muted})
	}
	return out
}

// resolvedRow is the Item of the row that folds the resolved threads. A
// thread name cannot hold a NUL.
const resolvedRow = "\x00resolved"

func (a *App) renderThreads() {
	if a.threadsBuf == nil {
		return
	}
	th := a.th
	// The coordinator is not a thread; <leader>pc goes to it.
	names := a.listOrder()
	nameW := 0
	for _, n := range names {
		nameW = max(nameW, width(n))
	}
	shown := map[string]bool{}
	if a.root != nil {
		for _, w := range a.root.windows() {
			if w.Buf.Kind == KindChat {
				shown[w.Buf.Thread] = true
			}
		}
	}
	row := func(n string) Line {
		v := a.threads[n]
		nameSt, info, infoSt := th.Subtle, v.Progress, th.Muted
		if shown[n] {
			nameSt = th.Text.Bold(true)
		}
		switch v.State {
		case model.StateResolved:
			nameSt = th.Muted
		case model.StateWaiting:
			infoSt = th.Gold
			switch {
			case v.Permission != nil:
				info = "permission · " + v.Permission.Tool
			case v.Waiting == "paused":
				info = "paused"
			default:
				info = "question"
			}
		}
		if v.Queued > 0 {
			info = fmt.Sprintf("+%d queued  %s", v.Queued, info)
		}
		return Line{
			Prefix: []Span{{" ", th.Text}},
			Spans: []Span{
				{stateGlyph(v.State) + " ", th.state(v.State)},
				{pad(n, nameW+2), nameSt},
				{flatten(info), infoSt},
			},
			Item: n,
		}
	}
	groups := map[model.State][]string{}
	for _, n := range names {
		state := a.threads[n].State
		groups[state] = append(groups[state], n)
	}
	var lines []Line
	for _, s := range []model.State{model.StateWaiting, model.StateRunning, model.StateIdle} {
		if len(groups[s]) == 0 {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, Line{})
		}
		lines = append(lines, Line{Prefix: []Span{{" ", th.Text}}, Spans: []Span{{stateLabel(s), th.Muted}}})
		for _, n := range groups[s] {
			lines = append(lines, row(n))
		}
	}
	if done := groups[model.StateResolved]; len(done) > 0 {
		fold := "▸"
		if a.showResolved {
			fold = "▾"
		}
		if len(lines) > 0 {
			lines = append(lines, Line{})
		}
		lines = append(lines, Line{
			Prefix: []Span{{" ", th.Text}},
			Spans:  []Span{{fmt.Sprintf("%s %d resolved", fold, len(done)), th.Muted}},
			Item:   resolvedRow,
		})
		if a.showResolved {
			for _, n := range done {
				lines = append(lines, row(n))
			}
		}
	}
	a.threadsBuf.SetLines(lines)
}

// listOrder is the threads in display order: open ones by age, resolved last.
// The coordinator is left out; callers that want it put it first.
func (a *App) listOrder() []string {
	var open, done []string
	for _, n := range a.order {
		if n == model.Coordinator {
			continue
		}
		if a.threads[n].State == model.StateResolved {
			done = append(done, n)
		} else {
			open = append(open, n)
		}
	}
	return append(open, done...)
}

// --- notices ---

func (a *App) notify(text string, st tcell.Style) {
	a.notice, a.noticeSt = text, st
	a.logMessage(text, st)
}

// logMessage adds a line to :messages without showing it on the bottom line.
func (a *App) logMessage(text string, st tcell.Style) {
	a.messages = append(a.messages, Line{
		Prefix: []Span{{" " + time.Now().Format("15:04:05") + "  ", a.th.Muted}},
		Spans:  []Span{{text, st}},
	})
	a.messagesBuf.SetLines(a.messages)
}

func pad(s string, n int) string {
	if w := width(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}
