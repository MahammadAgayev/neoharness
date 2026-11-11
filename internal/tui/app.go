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
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	lua "github.com/yuin/gopher-lua"

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
	DeletedWorkspaces() ([]string, error)
	RestoreWorkspace(name string) error
	Threads(workspace string) ([]model.ThreadView, error)
	Artifacts(workspace string) ([]model.Artifact, error)
	Artifact(workspace, name string) (model.Artifact, string, error)
	EditArtifact(workspace, name, text, baseText string, base int, force bool) (model.Artifact, string, error)
	OpenThread(workspace, name, brief, location string) error
	Events(workspace, thread string, tail int) ([]model.Event, error)
	Completions(workspace, thread, query string) (model.Completions, error)
	Send(workspace, thread, text string) error
	SendNow(workspace, thread, text string) error
	Stop(workspace, thread string) error
	Unqueue(workspace, thread string, at time.Time) error
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
		ok  bool
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
	// openURL runs the system opener on a URL; tests replace it.
	openURL func(open, url string) error

	root *node
	cur  *Window
	bufs map[string]*Buffer

	workspaces []model.WorkspaceView
	workspace  string
	threads    map[string]model.ThreadView
	order      []string
	artifacts  []model.Artifact

	workspacesBuf *Buffer
	threadsBuf    *Buffer
	// pane is the window on the right that <leader>t opens. It shows the
	// threads list, and the threads opened from it.
	pane         *Window
	showResolved bool
	// layouts are the windows of the workspaces left this session, so
	// opening one again shows its splits as they were.
	layouts     map[string]layout
	messagesBuf *Buffer
	messages    []Line

	mode  mode
	binds map[string]func(int)
	// builtin are the Go bindings: Vim's keys and the <Plug> actions.
	// baseBinds add defaults.lua's maps; a map that is not remap reaches
	// only those, and noremap counts such maps that run now. lua is the
	// configs' state, which their maps call.
	builtin     map[string]func(int)
	baseBinds   map[string]func(int)
	noremap     int
	lua         *lua.LState
	defaultMaps []mapOp
	userMaps    []mapOp
	cfgName     string
	cfgDefaults bool
	cfgLoading  bool
	cfgCmds     []string
	// imaps are the Insert maps; iheld and ikeys are the keys that wait for
	// one, iflush is set while they go in as typed.
	imaps    map[string]func(int)
	iheld    []*tcell.EventKey
	ikeys    []string
	iflush   bool
	pending  []string
	pendSeq  int
	count    int
	cmd      *cmdline
	picker   *picker
	menu     *menu
	toasts   []*toast
	menuShut int // the word whose menu <Esc> closed, or -1
	// mouseDown is the left button's state, as the terminal sends a press
	// and a release; lastClick is the last press, for double clicks.
	mouseDown              bool
	lastClick              time.Time
	lastClickX, lastClickY int
	// replacing is set by r: the next key replaces replaceN runes.
	replacing bool
	// finding is f F t T, after an operator if one came first: the next key
	// is the rune to find. lastFind is the last one, for ; and ,.
	finding, lastFind string
	findN             int
	// selecting is i or a typed in Visual: the next key is a text object.
	selecting string
	// marking is m, ' or `: the next key names the mark.
	marking  string
	replaceN int
	// rec holds the keys of the change being typed in an artifact's source;
	// lastChange the last finished one, for `.`.
	rec        []*tcell.EventKey
	recRev     int
	lastChange []*tcell.EventKey
	replaying  bool
	noRepeat   bool
	// macroKey is q or @ waiting for the register it names. recordInto is
	// the register q is recording; macros holds each register's keys.
	macroKey   string
	macroN     int
	recordInto rune
	macros     map[rune][]*tcell.EventKey
	lastMacro  rune
	playing    int
	// confirm runs on y; any other key drops it.
	confirm func()
	comps   map[string]model.Completions
	// compBusy holds the buffers whose completions are being fetched.
	compBusy map[string]bool
	search   string
	// searchBack says the last search was ?, so n goes up.
	searchBack bool
	notice     string
	noticeSt   tcell.Style
	offline    bool

	cursorX int
	cursorY int
	spin    int
	ticking bool
	quit    bool
	pasting bool
	restart func() error
	// reg is the unnamed register: every yank and delete, in any window.
	// ctrlR is <C-r> in Insert waiting for the register it names.
	reg   register
	ctrlR bool
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
	// Grapheme cluster mode (2027): the terminal measures emoji and joined
	// characters as one cluster, as uniseg does here, so columns line up. A
	// terminal without it ignores the request. tcell does not send it.
	if tty, ok := s.Tty(); ok {
		_, _ = tty.Write([]byte("\x1b[?2027h"))
		defer func() { _, _ = tty.Write([]byte("\x1b[?2027l")) }()
	}
	s.EnablePaste()
	// Clicks and the wheel, no drags: the terminal still selects text with
	// Shift (or Option) held.
	s.EnableMouse(tcell.MouseButtonEvents)
	a := newApp(s, c)
	a.restart = restart
	a.loadConfig(filepath.Join(filepath.Dir(hubapi.SocketPath()), "init.lua"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.listen(ctx)
	a.loop()
	return nil
}

func newApp(s tcell.Screen, c Client) *App {
	a := &App{
		s:        newCanvas(s),
		c:        c,
		th:       newTheme(),
		openURL:  func(open, url string) error { return exec.Command(open, url).Start() },
		bufs:     map[string]*Buffer{},
		threads:  map[string]model.ThreadView{},
		comps:    map[string]model.Completions{},
		compBusy: map[string]bool{},
		menuShut: -1,
	}
	a.workspacesBuf = newBuffer("neoharness://workspaces", KindWorkspaces)
	a.messagesBuf = newBuffer("neoharness://messages", KindMessages)
	a.messagesBuf.SetLines([]Line{{Prefix: []Span{{" ", a.th.Text}}, Spans: []Span{{"no messages yet", a.th.Muted}}}})
	a.bufs[a.workspacesBuf.Name] = a.workspacesBuf
	a.bufs[a.messagesBuf.Name] = a.messagesBuf
	a.cmd = &cmdline{}
	a.builtin = a.normalBindings()
	a.runConfig(defaultsLua, "defaults.lua", true)
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
	case *tcell.EventMouse:
		a.mouse(ev)
	case *tcell.EventPaste:
		a.pasting = ev.Start()
		if !a.pasting && a.mode == modeInsert && a.cur.Buf.Edit != nil {
			a.toWindow(a.cur)
			a.settle(a.cur)
		}
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
		if ev.seq == a.pendSeq && len(a.ikeys) > 0 {
			a.flushInsert(true)
		} else if ev.seq == a.pendSeq && len(a.pending) > 0 {
			a.flushPending()
		}
	case evComplete:
		delete(a.compBusy, ev.buf)
		if !ev.ok {
			break
		}
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
	a.insert('i')
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
	if a.artifacts, err = a.c.Artifacts(a.workspace); err != nil {
		a.notify(err.Error(), a.th.Love)
	}
	for _, b := range a.bufs {
		if b.Kind == KindArtifact && b.Workspace == a.workspace {
			a.loadArtifact(b)
		}
	}
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
	b.SetWorking(v.State == model.StateRunning)
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
	case model.UpdateArtifact:
		if u.Workspace != a.workspace || u.Artifact == nil {
			return
		}
		a.artifacts = slices.DeleteFunc(a.artifacts, func(x model.Artifact) bool { return x.Name == u.Artifact.Name })
		a.artifacts = append([]model.Artifact{*u.Artifact}, a.artifacts...)
		if b, ok := a.bufs[artifactName(a.workspace, u.Artifact.Name)]; ok {
			a.loadArtifact(b)
			if e := b.Edit; e != nil && e.Dirty && u.Artifact.Version != e.Base {
				a.notify(fmt.Sprintf("%s wrote v%d while you edit · :w merges it, :e! takes it", u.Artifact.Thread, u.Artifact.Version), a.th.Gold)
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
		w.forget(b)
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
	delete(a.layouts, name)
	a.renderWorkspaces()
	if a.workspace == name {
		a.workspace = ""
		a.threads = map[string]model.ThreadView{}
		a.order = nil
		a.threadsBuf = nil
		a.showWorkspaces()
	}
	a.notify(name+" deleted · :restore "+name+" brings it back", a.th.Muted)
}

// restoreWorkspace is :restore. Without a name it lists what can come back.
func (a *App) restoreWorkspace(name string) {
	if name == "" {
		names, err := a.c.DeletedWorkspaces()
		switch {
		case err != nil:
			a.notify(err.Error(), a.th.Love)
		case len(names) == 0:
			a.notify("no deleted workspaces", a.th.Muted)
		default:
			a.notify("deleted: "+strings.Join(names, ", ")+" · :restore <name>", a.th.Muted)
		}
		return
	}
	if err := a.c.RestoreWorkspace(name); err != nil {
		a.notify(err.Error(), a.th.Love)
		return
	}
	a.loadWorkspaces()
	a.notify(name+" restored", a.th.Subtle)
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
	}
	a.chat(v.Name).SetPending(v.Pending)
	a.chat(v.Name).SetWorking(v.State == model.StateRunning)
	// A turn that ended may have reported the thread's first command list.
	if had && old.State == model.StateRunning && v.State != model.StateRunning {
		if b := a.chat(v.Name); a.mode == modeInsert && a.cur.Buf == b {
			a.fetchCompletions(b, "")
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

// layout is a workspace's windows: the tree, the focus, the pane, and the
// threads list the pane shows.
type layout struct {
	root      *node
	cur, pane *Window
	threads   *Buffer
}

// saveLayout keeps the open workspace's windows before another screen takes
// their place.
func (a *App) saveLayout() {
	if a.workspace == "" || a.threadsBuf == nil {
		return
	}
	root, cur := a.root, a.cur
	if root == nil || cur == nil || cur.Buf == a.workspacesBuf {
		return
	}
	if a.layouts == nil {
		a.layouts = map[string]layout{}
	}
	a.layouts[a.workspace] = layout{root, cur, a.pane, a.threadsBuf}
}

func (a *App) showWorkspaces() {
	a.saveLayout()
	w := newWindow(a.workspacesBuf)
	w.Follow = false
	w.Cur = 4
	// The cursor starts on the workspace you came from.
	for i, l := range a.workspacesBuf.Lines(80, a.th, a.spin) {
		if a.workspace != "" && l.Item == a.workspace {
			w.Cur = i
		}
	}
	a.root = leaf(w, 1)
	a.cur = w
	a.mode = modeNormal
}

func (a *App) openWorkspace(name string) {
	if !slices.ContainsFunc(a.workspaces, func(p model.WorkspaceView) bool { return p.Name == name }) {
		a.notify("no workspace "+name, a.th.Love)
		return
	}
	a.saveLayout()
	a.workspace = name
	if l, ok := a.layouts[name]; ok {
		a.threadsBuf = l.threads
		a.bufs[a.threadsBuf.Name] = a.threadsBuf
		a.resync()
		a.root, a.cur, a.pane = l.root, l.cur, l.pane
		for _, w := range a.root.windows() {
			if _, known := a.threads[w.Buf.Thread]; w.Buf.Kind == KindChat && !known {
				w.show(a.chat(model.Coordinator))
			}
		}
		if !slices.Contains(a.root.windows(), a.cur) {
			a.cur = a.root.windows()[0]
		}
		a.mode = modeNormal
		return
	}
	a.threadsBuf = newBuffer("neoharness://"+name+"/threads", KindThreads)
	a.bufs[a.threadsBuf.Name] = a.threadsBuf
	a.resync()

	coord := newWindow(a.chat(model.Coordinator))
	a.pane = newWindow(a.threadsBuf)
	a.pane.Follow = false
	a.root = split(true, leaf(coord, 1), leaf(a.pane, 1))
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
	if a.workspace == "" {
		return false
	}
	if _, ok := a.threads[name]; !ok {
		a.notify("no thread "+name, a.th.Love)
		return false
	}
	return true
}

// toPane shows a buffer in the threads pane on the right and focuses it. If
// the pane was closed, it opens again at full height.
func (a *App) toPane(b *Buffer) {
	switch {
	case a.pane == nil:
		a.pane = newWindow(b)
	case a.paneOnRight():
		a.pane.show(b)
		a.cur = a.pane
		return
	default:
		// Splits moved it; it goes back to a full-height column on the right.
		if a.root.find(a.pane) != nil {
			a.root.closeWindow(a.pane)
		}
		a.pane.show(b)
	}
	a.root.weight = 1
	a.root = split(true, a.root, leaf(a.pane, 1))
	a.equalAlways(true)
	a.cur = a.pane
}

// paneOnRight reports whether the pane is the rightmost column at full
// height, or the only window.
func (a *App) paneOnRight() bool {
	if a.root.win == a.pane {
		return true
	}
	return a.root.vert && a.root.kids[len(a.root.kids)-1].win == a.pane
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

// splitThread opens a thread in a new split of the current window: right
// of it (vert) or above it.
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

// openArtifact shows an artifact in the current window, as gf opens a file;
// <C-o> goes back.
func (a *App) openArtifact(name string) {
	b, ok := a.bufs[artifactName(a.workspace, name)]
	if !ok {
		b = newArtifact(a.workspace, name)
	}
	if _, ok := a.loadArtifact(b); !ok {
		return
	}
	a.bufs[b.Name] = b
	a.showBuf(a.cur, b)
	a.mode = modeNormal
}

// showBuf shows a buffer in a window. An artifact's cursor is its place in
// the source, so the window takes it from there.
func (a *App) showBuf(w *Window, b *Buffer) {
	w.show(b)
	if b.Edit != nil {
		a.toWindow(w)
	}
}

// mention is an artifact name in a text, from byte start to end.
type mention struct {
	name       string
	start, end int
}

// mentions finds the artifact names in s, as whole words. A name that is
// one plain word (plan, notes) is a link only in code, where an agent quotes
// it; in prose it is too often just the word.
func (a *App) mentions(s string, code bool) []mention {
	var out []mention
	for _, art := range a.artifacts {
		if !code && !strings.ContainsAny(art.Name, "-_0123456789") {
			continue
		}
		for i := 0; ; {
			j := strings.Index(s[i:], art.Name)
			if j < 0 {
				break
			}
			start, end := i+j, i+j+len(art.Name)
			if (start == 0 || !nameByte(s[start-1])) && (end == len(s) || !nameByte(s[end])) {
				out = append(out, mention{art.Name, start, end})
			}
			i = end
		}
	}
	slices.SortFunc(out, func(x, y mention) int { return x.start - y.start })
	return out
}

func nameByte(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' }

// linkify marks the artifact names in a chat line as links.
func (a *App) linkify(spans []Span) []Span {
	if len(a.artifacts) == 0 {
		return spans
	}
	var out []Span
	for _, sp := range spans {
		at := 0
		for _, m := range a.mentions(sp.Text, sp.Style == a.th.Code) {
			if m.start < at {
				continue
			}
			out = append(out, Span{sp.Text[at:m.start], sp.Style}, Span{sp.Text[m.start:m.end], a.th.Foam.Underline(true)})
			at = m.end
		}
		out = append(out, Span{sp.Text[at:], sp.Style})
	}
	return out
}

// artifactAt is the artifact named in a line under column col, else the
// first one named in it.
func (a *App) artifactAt(l Line, col int) string {
	text := l.Text()
	var ms []mention
	at := 0
	for _, sp := range l.Spans {
		for _, m := range a.mentions(sp.Text, sp.Style == a.th.Code) {
			m.start, m.end = m.start+at, m.end+at
			ms = append(ms, m)
		}
		at += len(sp.Text)
	}
	if len(ms) == 0 {
		return ""
	}
	col -= l.prefixWidth()
	for _, m := range ms {
		if c := width(text[:m.start]); col >= c && col < c+width(m.name) {
			return m.name
		}
	}
	return ms[0].name
}

// mdLink finds markdown links and bare URLs: [label](url), <url>, url.
var mdLink = regexp.MustCompile(`\[([^\]]*)\]\(<?([^)\s>]+)>?[^)]*\)|<?(https?://[^\s>)\]]+)`)

// mdRefLink finds reference links, [label][id] and [label][], and refURL
// the definitions they point to, [id]: url.
var (
	mdRefLink = regexp.MustCompile(`\[([^\]]+)\]\[([^\]]*)\]`)
	refURL    = regexp.MustCompile(`^ {0,3}\[([^\]]+)\]:\s*<?([^\s>]+)`)
)

// refURLs are an artifact's reference definitions by their lower-case id.
func refURLs(src [][]rune) map[string]string {
	out := map[string]string{}
	for _, l := range src {
		if m := refURL.FindStringSubmatch(string(l)); m != nil {
			out[strings.ToLower(m[1])] = m[2]
		}
	}
	return out
}

// refTarget is the URL of a reference link match in s, or "".
func refTarget(s string, m []int, refs map[string]string) string {
	id := s[m[4]:m[5]]
	if id == "" {
		id = s[m[2]:m[3]]
	}
	return refs[strings.ToLower(id)]
}

// linkAt is the URL of the link under the window's cursor, or "". The text
// shows no URL, so it comes from the source line, matched by the link
// style, which carries it.
func (a *App) linkAt(w *Window) string {
	l := a.line(w)
	col := w.Col - l.prefixWidth()
	var under Span
	for _, sp := range l.Spans {
		if col < width(sp.Text) {
			under = sp
			break
		}
		col -= width(sp.Text)
	}
	src := l.Text()
	b := w.Buf
	switch {
	case b.Edit != nil && l.Src > 0:
		src = string(b.Edit.Src[l.Src-1])
		// A raw line shows the link itself: the one around the cursor.
		if b.Edit.isRaw(l.Src - 1) {
			off := len(string([]rune(src)[:a.posAt(w, w.Cur, w.Col).off]))
			for _, m := range mdLink.FindAllStringSubmatchIndex(src, -1) {
				if off < m[0] || off >= m[1] {
					continue
				}
				if m[6] >= 0 {
					return src[m[6]:m[7]]
				}
				return src[m[4]:m[5]]
			}
			for _, m := range mdRefLink.FindAllStringSubmatchIndex(src, -1) {
				if off >= m[0] && off < m[1] {
					return refTarget(src, m, refURLs(b.Edit.Src))
				}
			}
			return ""
		}
	case l.Block < len(b.blocks) && b.blocks[l.Block].text != "":
		src = b.blocks[l.Block].text
	}
	var urls, labels []string
	for _, m := range mdLink.FindAllStringSubmatch(src, -1) {
		if m[3] != "" {
			m[1], m[2] = m[3], m[3]
		}
		if linkStyle(a.th, m[2]) == under.Style {
			return m[2]
		}
		urls, labels = append(urls, m[2]), append(labels, m[1])
	}

	for i, label := range labels {
		if t := strings.TrimSpace(under.Text); t != "" && strings.Contains(label, t) {
			return urls[i]
		}
	}
	if len(urls) == 1 {
		return urls[0]
	}
	return ""
}

// openLink is gx: the link under the cursor opens in the browser.
func (a *App) openLink() {
	url := a.linkAt(a.cur)
	if url == "" {
		a.notify("no link under the cursor", a.th.Muted)
		return
	}
	open := "xdg-open"
	switch runtime.GOOS {
	case "darwin":
		open = "open"
	case "windows":
		open = "explorer"
	}
	if err := a.openURL(open, url); err != nil {
		a.notify(err.Error(), a.th.Love)
		return
	}
	a.notify("opened "+url, a.th.Subtle)
}

// loadArtifact reads an artifact's text into its buffer.
func (a *App) loadArtifact(b *Buffer) (model.Artifact, bool) {
	art, text, err := a.c.Artifact(b.Workspace, b.Artifact)
	if err != nil {
		a.notify(err.Error(), a.th.Love)
		return art, false
	}
	b.SetDoc(art, text)
	return art, true
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

// wakeLabel says when an idle thread wakes, if it asked to be woken.
func wakeLabel(v model.ThreadView) string {
	if v.State != model.StateIdle || v.WakeAt.IsZero() {
		return ""
	}
	return "wakes at " + v.WakeAt.Local().Format("15:04")
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

// queuedRow starts the Item of a queued message's rows, then when it was
// queued: <CR> on one sends the queue now, x or dd takes it back.
const queuedRow = "\x00queued"

func (a *App) renderThreads() {
	if a.threadsBuf == nil {
		return
	}
	th := a.th
	// The coordinator is not a thread; <leader>c goes to it.
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
		if v.State == model.StateRunning && !v.Since.IsZero() {
			info = strings.TrimSuffix(elapsed(time.Since(v.Since))+" · "+info, " · ")
		}
		if w := wakeLabel(v); w != "" {
			info = strings.TrimSuffix(w+" · "+info, " · ")
		}
		if v.Queued > 0 {
			info = fmt.Sprintf("+%d queued  %s", v.Queued, info)
		}
		return Line{
			Prefix: []Span{{" ", th.Text}},
			Spans: []Span{
				{stateGlyph(v.State) + " ", th.state(v.State)},
				{pad(n, nameW+2), nameSt},
				{plain(info), infoSt},
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
	if len(lines) == 0 {
		lines = []Line{{Prefix: []Span{{" ", th.Text}}, Spans: []Span{{"no threads yet", th.Muted}}}}
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
