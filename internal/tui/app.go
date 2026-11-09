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
	Projects() ([]model.ProjectView, error)
	CreateProject(name, pin string) error
	Threads(project string) ([]model.ThreadView, error)
	OpenThread(project, name, brief, location string) error
	Events(project, thread string, tail int) ([]model.Event, error)
	Send(project, thread, text string) error
	Resolve(project, thread string) error
	Reopen(project, thread string) error
	Answer(project, thread, id string, d model.Decision, msg string) error
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
)

func (evUpdate) When() time.Time     { return time.Now() }
func (evResync) When() time.Time     { return time.Now() }
func (evDisconnect) When() time.Time { return time.Now() }
func (evTick) When() time.Time       { return time.Now() }
func (evKeyTimeout) When() time.Time { return time.Now() }

// App is the whole UI.
type App struct {
	s  tcell.Screen
	c  Client
	th *Theme

	root *node
	cur  *Window
	bufs map[string]*Buffer

	projects []model.ProjectView
	project  string
	threads  map[string]model.ThreadView
	order    []string

	projectsBuf *Buffer
	threadsBuf  *Buffer
	messagesBuf *Buffer
	messages    []Line

	mode     mode
	binds    map[string]func(int)
	pending  []string
	pendSeq  int
	count    int
	cmd      *cmdline
	picker   *picker
	search   string
	notice   string
	noticeSt tcell.Style
	offline  bool

	cursorX  int
	cursorY  int
	spin     int
	ticking  bool
	quit     bool
	pasting  bool
	waitNext int
	restart  func() error
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
		s:       s,
		c:       c,
		th:      newTheme(),
		bufs:    map[string]*Buffer{},
		threads: map[string]model.ThreadView{},
	}
	a.projectsBuf = newBuffer("neoharness://projects", KindProjects)
	a.messagesBuf = newBuffer("neoharness://messages", KindMessages)
	a.bufs[a.projectsBuf.Name] = a.projectsBuf
	a.bufs[a.messagesBuf.Name] = a.messagesBuf
	a.cmd = &cmdline{}
	a.binds = a.normalBindings()
	a.loadProjects()
	a.showProjects()
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
	case evTick:
		a.ticking = false
		a.spin++
	case evKeyTimeout:
		if ev.seq == a.pendSeq && len(a.pending) > 0 {
			a.flushPending()
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

func (a *App) loadProjects() {
	ps, err := a.c.Projects()
	if err != nil {
		a.notify(err.Error(), a.th.Love)
		return
	}
	a.projects = ps
	a.renderProjects()
}

func (a *App) createProject(name, pin string) {
	// The hub runs in another folder, so a relative path must become absolute
	// here, where the user typed it.
	if pin != "" && !strings.HasPrefix(pin, "~") {
		if abs, err := filepath.Abs(pin); err == nil {
			pin = abs
		}
	}
	if err := a.c.CreateProject(name, pin); err != nil {
		a.notify(err.Error(), a.th.Love)
		return
	}
	a.loadProjects()
	a.openProject(name)
	a.insert()
}

// resync reloads everything after a (re)connect, keeping windows on the
// buffers they showed.
func (a *App) resync() {
	a.loadProjects()
	if a.project == "" {
		return
	}
	vs, err := a.c.Threads(a.project)
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
	evs, err := a.c.Events(a.project, v.Name, historyTail)
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
	case model.UpdateProject:
		if u.Summary == nil {
			return
		}
		i := slices.IndexFunc(a.projects, func(p model.ProjectView) bool { return p.Name == u.Project })
		if i < 0 {
			a.projects = append(a.projects, *u.Summary)
		} else {
			a.projects[i] = *u.Summary
		}
		a.renderProjects()
	case model.UpdateThread:
		if u.Project == a.project && u.View != nil {
			a.onView(*u.View)
		}
	case model.UpdateEvent:
		if u.Project == a.project && u.Event != nil {
			a.chat(u.Thread).AddEvent(*u.Event)
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
	if v.State == model.StateWaiting && (!had || old.State != model.StateWaiting) && !a.focusedOn(v.Name) {
		what := "your input"
		switch {
		case v.Permission != nil:
			what = "permission"
		case v.Waiting == "paused":
			what = "you to continue"
		case v.Waiting != "":
			what = "an answer"
		}
		a.notify(fmt.Sprintf("%s needs %s", v.Name, what), a.th.Gold)
	}
	if v.State == model.StateResolved && had && old.State != model.StateResolved {
		a.notify(v.Name+" resolved", a.th.Pine)
	}
	a.renderThreads()
}

func (a *App) focusedOn(thread string) bool {
	return a.cur != nil && a.cur.Buf.Kind == KindChat && a.cur.Buf.Thread == thread
}

// chat returns the chat buffer of a thread in the open project, creating it.
func (a *App) chat(thread string) *Buffer {
	name := chatName(a.project, thread)
	if b, ok := a.bufs[name]; ok {
		return b
	}
	b := newChat(a.project, thread)
	a.bufs[name] = b
	return b
}

// --- screens ---

func (a *App) showProjects() {
	w := newWindow(a.projectsBuf)
	w.Follow = false
	w.Cur = 4
	a.root = leaf(w, 1)
	a.cur = w
	a.mode = modeNormal
}

func (a *App) openProject(name string) {
	if !slices.ContainsFunc(a.projects, func(p model.ProjectView) bool { return p.Name == name }) {
		a.notify("no project "+name, a.th.Love)
		return
	}
	a.project = name
	a.threadsBuf = newBuffer("neoharness://"+name+"/threads", KindThreads)
	a.bufs[a.threadsBuf.Name] = a.threadsBuf
	a.resync()

	coord := newWindow(a.chat(model.Coordinator))
	a.root = leaf(coord, 1)
	a.cur = coord
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
	if a.project == "" {
		return false
	}
	if _, ok := a.threads[name]; !ok {
		a.notify("no thread "+name, a.th.Love)
		return false
	}
	return true
}

// splitThread opens a thread in a new split of the current window.
func (a *App) splitThread(name string, vert bool) {
	if a.knownThread(name) {
		a.splitCur(vert, a.chat(name))
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

func (a *App) renderProjects() {
	th := a.th
	lines := []Line{
		{},
		{Prefix: []Span{{"  ", th.Text}}, Spans: []Span{{"neoharness", th.Iris.Bold(true)}}},
		{},
		{Prefix: []Span{{"  ", th.Text}}, Spans: []Span{{"PROJECTS", th.Muted}}},
	}
	nameW, pinW := 8, 10
	for _, p := range a.projects {
		nameW = max(nameW, width(p.Name))
		pinW = max(pinW, width(displayPin(p.Pin)))
	}
	pinW = min(pinW, 40)
	for _, p := range a.projects {
		spans := []Span{
			{pad(p.Name, nameW+3), th.Text},
			{pad(truncate(displayPin(p.Pin), pinW), pinW+3), th.Muted},
		}
		spans = append(spans, countSpans(p.Counts, th)...)
		lines = append(lines, Line{Prefix: []Span{{"  ", th.Text}}, Spans: spans, Item: p.Name})
	}
	if len(a.projects) == 0 {
		lines = append(lines, Line{Prefix: []Span{{"  ", th.Text}}, Spans: []Span{{":new <name> [folder]", th.Muted}}})
	}
	a.projectsBuf.SetLines(lines)
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

func (a *App) renderThreads() {
	if a.threadsBuf == nil {
		return
	}
	th := a.th
	names := a.listOrder()
	nameW := width(model.Coordinator)
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
	var lines []Line
	for _, n := range append([]string{model.Coordinator}, names...) {
		v := a.threads[n]
		nameSt, info, infoSt := th.Subtle, v.Progress, th.Muted
		if shown[n] {
			nameSt = th.Text.Bold(true)
		}
		switch v.State {
		case model.StateResolved:
			nameSt, info = th.Muted, "resolved"
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
		lines = append(lines, Line{
			Prefix: []Span{{" ", th.Text}},
			Spans: []Span{
				{stateGlyph(v.State) + " ", th.state(v.State)},
				{pad(n, nameW+2), nameSt},
				{flatten(info), infoSt},
			},
			Item: n,
		})
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
