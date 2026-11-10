package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/internal/model"
)

// fakeClient is a hub that records what the UI asked of it.
type fakeClient struct {
	workspaces []model.WorkspaceView
	threads    map[string][]model.ThreadView
	events     map[string][]model.Event
	sent       []string
	answers    []string
	opened     []string
	resolved   []string
	deleted    []string
	folders    []string
	gone       []string
	docs       map[string]string
	arts       []model.Artifact
	// merged is what an edit from an old version merges to; empty refuses.
	merged string
}

func (f *fakeClient) Workspaces() ([]model.WorkspaceView, error) { return f.workspaces, nil }
func (f *fakeClient) CreateWorkspace(name, pin string) error {
	f.workspaces = append(f.workspaces, model.WorkspaceView{Name: name, Pin: pin})
	f.threads[name] = []model.ThreadView{{Workspace: name, Name: model.Coordinator, Role: model.RoleCoordinator, State: model.StateIdle}}
	return nil
}
func (f *fakeClient) SetFolder(name, pin string) error {
	f.folders = append(f.folders, name+"="+pin)
	return nil
}
func (f *fakeClient) DeleteWorkspace(name string) error {
	f.gone = append(f.gone, name)
	return nil
}
func (f *fakeClient) DeletedWorkspaces() ([]string, error) { return slices.Clone(f.gone), nil }
func (f *fakeClient) RestoreWorkspace(name string) error {
	if !slices.Contains(f.gone, name) {
		return fmt.Errorf("no deleted workspace %q", name)
	}
	f.gone = slices.DeleteFunc(f.gone, func(n string) bool { return n == name })
	f.workspaces = append(f.workspaces, model.WorkspaceView{Name: name})
	return nil
}
func (f *fakeClient) Threads(p string) ([]model.ThreadView, error) { return f.threads[p], nil }
func (f *fakeClient) Artifacts(p string) ([]model.Artifact, error) { return slices.Clone(f.arts), nil }
func (f *fakeClient) Artifact(p, name string) (model.Artifact, string, error) {
	for _, a := range f.arts {
		if a.Name == name {
			return a, f.docs[name], nil
		}
	}
	return model.Artifact{}, "", fmt.Errorf("no artifact %q", name)
}
func (f *fakeClient) EditArtifact(p, name, text, baseText string, base int, force bool) (model.Artifact, string, error) {
	i := slices.IndexFunc(f.arts, func(a model.Artifact) bool { return a.Name == name })
	if i < 0 {
		return model.Artifact{}, "", fmt.Errorf("no artifact %q", name)
	}
	if f.arts[i].Version != base && !force {
		if f.merged == "" {
			return f.arts[i], "", fmt.Errorf("v%d changed the lines you edited · :w! saves over it, :e! takes it", f.arts[i].Version)
		}
		text = f.merged
	}
	if f.docs[name] != text {
		f.arts[i].Version++
		f.arts[i].UserEdit = true
		f.docs[name] = text
	}
	return f.arts[i], text, nil
}
func (f *fakeClient) OpenThread(p, name, brief, loc string) error {
	f.opened = append(f.opened, name+"@"+loc)
	return nil
}
func (f *fakeClient) Events(p, t string, tail int) ([]model.Event, error) { return f.events[t], nil }
func (f *fakeClient) Completions(p, t string) (model.Completions, error) {
	return model.Completions{
		Commands: []string{"simplify", "code-review", "dev:pr-create"},
		Files:    []string{"main.go", "internal/auth/auth.go", "go.mod"},
		Threads:  []string{"auth-refactor", "api-tests"},
	}, nil
}
func (f *fakeClient) Send(p, t, text string) error {
	f.sent = append(f.sent, t+": "+text)
	return nil
}
func (f *fakeClient) SendNow(p, t, text string) error {
	f.sent = append(f.sent, "now "+t+": "+text)
	return nil
}
func (f *fakeClient) Resolve(p, t string) error {
	f.resolved = append(f.resolved, t)
	return nil
}
func (f *fakeClient) Reopen(p, t string) error { return nil }
func (f *fakeClient) Delete(p, t string) error {
	f.deleted = append(f.deleted, t)
	return nil
}
func (f *fakeClient) Answer(p, t, id string, d model.Decision, msg string) error {
	f.answers = append(f.answers, t+" "+id+" "+string(d)+" "+msg)
	return nil
}
func (f *fakeClient) Updates(ctx context.Context) (<-chan model.Update, error) {
	return make(chan model.Update), nil
}

func newFake() *fakeClient {
	now := time.Now()
	return &fakeClient{
		workspaces: []model.WorkspaceView{{Name: "demo", Pin: "/src/demo", Counts: map[model.State]int{model.StateRunning: 1}, Activity: []int{0, 1, 3}}},
		threads: map[string][]model.ThreadView{"demo": {
			{Workspace: "demo", Name: model.Coordinator, Role: model.RoleCoordinator, State: model.StateIdle, CreatedAt: now},
			{Workspace: "demo", Name: "auth-refactor", Role: model.RoleThread, State: model.StateRunning, Progress: "moving store", CreatedAt: now, Since: now},
			{Workspace: "demo", Name: "api-tests", Role: model.RoleThread, State: model.StateIdle, CreatedAt: now},
		}},
		events: map[string][]model.Event{
			model.Coordinator: {
				{Kind: model.EventMessage, From: model.User, Text: "split the session store out"},
				{Kind: model.EventText, Text: "Opened **auth-refactor** and `api-tests`."},
			},
			"auth-refactor": {
				{Kind: model.EventMessage, From: model.Coordinator, Text: "refactor auth"},
				{Kind: model.EventTool, Tool: "Read", Detail: "internal/auth/auth.go"},
				{Kind: model.EventTool, Tool: "Bash", Detail: "go test ./internal/auth/..."},
			},
		},
	}
}

type neoharness struct {
	t   *testing.T
	a   *App
	s   tcell.SimulationScreen
	hub *fakeClient
}

func start(t *testing.T) *neoharness {
	t.Helper()
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(120, 36)
	f := newFake()
	h := &neoharness{t: t, a: newApp(s, f), s: s, hub: f}
	h.a.draw()
	return h
}

// press types keys in Vim notation, drawing after each like the real loop.
func (h *neoharness) press(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		var ev *tcell.EventKey
		switch k {
		case "<CR>":
			ev = tcell.NewEventKey(tcell.KeyEnter, 0, 0)
		case "<Esc>":
			ev = tcell.NewEventKey(tcell.KeyEscape, 0, 0)
		case "<Tab>":
			ev = tcell.NewEventKey(tcell.KeyTab, 0, 0)
		case "<Space>":
			ev = tcell.NewEventKey(tcell.KeyRune, ' ', 0)
		case "<C-w>":
			ev = tcell.NewEventKey(tcell.KeyCtrlW, 0, 0)
		case "<C-j>":
			ev = tcell.NewEventKey(tcell.KeyCtrlJ, 0, 0)
		case "<C-v>":
			ev = tcell.NewEventKey(tcell.KeyCtrlV, 0, 0)
		case "<C-x>":
			ev = tcell.NewEventKey(tcell.KeyCtrlX, 0, 0)
		case "<C-d>":
			ev = tcell.NewEventKey(tcell.KeyCtrlD, 0, 0)
		case "<C-s>":
			ev = tcell.NewEventKey(tcell.KeyCtrlS, 0, 0)
		case "<C-^>":
			ev = tcell.NewEventKey(tcell.KeyCtrlCarat, 0, 0)
		case "<C-l>":
			ev = tcell.NewEventKey(tcell.KeyCtrlL, 0, 0)
		case "<C-n>":
			ev = tcell.NewEventKey(tcell.KeyCtrlN, 0, 0)
		case "<BS>":
			ev = tcell.NewEventKey(tcell.KeyBackspace2, 0, 0)
		case "<Up>":
			ev = tcell.NewEventKey(tcell.KeyUp, 0, 0)
		case "<Down>":
			ev = tcell.NewEventKey(tcell.KeyDown, 0, 0)
		case "<End>":
			ev = tcell.NewEventKey(tcell.KeyEnd, 0, 0)
		default:
			for _, r := range k {
				h.a.handle(tcell.NewEventKey(tcell.KeyRune, r, 0))
				h.a.draw()
			}
			continue
		}
		h.a.handle(ev)
		h.a.draw()
	}
}

// settle handles what background work posted, such as completions.
func (h *neoharness) settle() {
	h.t.Helper()
	deadline := time.Now().Add(time.Second)
	for len(h.a.comps) == 0 && time.Now().Before(deadline) {
		for h.s.HasPendingEvent() {
			h.a.handle(h.s.PollEvent())
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.a.draw()
}

func (h *neoharness) prompt() string { return string(h.a.cur.Buf.Prompt.Text) }

func (h *neoharness) screen() string {
	cells, w, ht := h.s.GetContents()
	var b strings.Builder
	for y := 0; y < ht; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) == 0 {
				b.WriteByte(' ')
			} else {
				b.WriteString(string(c.Runes))
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func (h *neoharness) see(want string) {
	h.t.Helper()
	if !strings.Contains(h.screen(), want) {
		h.t.Fatalf("screen does not show %q:\n%s", want, h.screen())
	}
}

func (h *neoharness) openDemo() {
	h.t.Helper()
	// The cursor starts on the first workspace row.
	h.press("<CR>")
	if h.a.workspace != "demo" {
		h.t.Fatalf("workspace = %q, want demo", h.a.workspace)
	}
}

func TestWorkspacesScreenAndOpen(t *testing.T) {
	h := start(t)
	h.see("workspaces")
	h.see("demo")
	h.see("/src/demo")
	h.see("● 1")
	h.openDemo()
	if wins := h.a.root.windows(); len(wins) != 2 || wins[0] != h.a.cur || h.a.cur.Buf.Thread != model.Coordinator || wins[1].Buf.Kind != KindThreads {
		t.Fatalf("a workspace should open with the coordinator on the left and threads on the right; got %d windows", len(wins))
	}
	h.see("split the session store out")
	h.see("Opened auth-refactor and api-tests.")
	// The bottom line counts what runs: auth-refactor.
	h.see("● 1")
}

func TestNewWorkspaceStartsReadyToWrite(t *testing.T) {
	h := start(t)
	h.press(":new solo /tmp", "<CR>")
	if n := len(h.a.root.windows()); n != 2 || h.a.cur.Buf.Thread != model.Coordinator {
		t.Fatalf("a new workspace should focus the coordinator beside the threads; got %d windows", n)
	}
	if h.a.mode != modeInsert {
		t.Fatal("a new workspace should start ready to write")
	}
	h.press("<Esc>")
	v := model.ThreadView{Workspace: "solo", Name: "first", Role: model.RoleThread, State: model.StateRunning}
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "solo", Thread: v.Name, View: &v})
	if n := len(h.a.root.windows()); n != 2 || h.a.cur.Buf.Thread != model.Coordinator {
		t.Fatal("a new thread should not open a window or take the focus")
	}
}

func TestInsertSendsToTheFocusedThread(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press("i", "hello there", "<C-j>", "second line", "<CR>")
	if len(h.hub.sent) != 1 || h.hub.sent[0] != "coordinator: hello there\nsecond line" {
		t.Fatalf("sent = %q", h.hub.sent)
	}
	if h.a.mode != modeInsert {
		t.Error("sending keeps Insert mode, so a conversation can continue")
	}
	h.press("<Esc>")
	if h.a.mode != modeNormal {
		t.Fatal("<Esc> should return to Normal")
	}
	// Read-only buffers refuse Insert.
	h.press(":threads", "<CR>", "i")
	if h.a.mode != modeNormal || h.a.cur.Buf.Kind != KindThreads {
		t.Fatalf("Insert in the threads list: mode %v buf %v", h.a.mode, h.a.cur.Buf.Kind)
	}
}

func TestThreadsPaneShowsTheListThenThreads(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press("<Space>", "t")
	wins := h.a.root.windows()
	if len(wins) != 2 || wins[1] != h.a.cur || h.a.cur.Buf.Kind != KindThreads || wins[0].Buf.Thread != model.Coordinator {
		t.Fatalf("<leader>t should focus the threads pane on the right; got %d windows", len(wins))
	}
	coord, pane := wins[0], wins[1]
	// Rows: running, auth-refactor, blank, idle, api-tests.
	h.press("G", "<CR>")
	if len(h.a.root.windows()) != 2 || h.a.cur != pane || pane.Buf.Thread != "api-tests" {
		t.Fatalf("<CR> should show api-tests in the pane; got %q", pane.Buf.Name)
	}
	h.press("<Space>", "t", "gg", "j", "<CR>")
	if len(h.a.root.windows()) != 2 || pane.Buf.Thread != "auth-refactor" {
		t.Fatalf("the next thread should open in the same pane; got %q", pane.Buf.Name)
	}
	h.press("<C-^>")
	if pane.Buf.Kind != KindThreads {
		t.Fatal("<C-^> should go back to the list")
	}
	h.press("<Space>", "<Space>", "api", "<C-v>")
	if len(h.a.root.windows()) != 2 || h.a.cur != pane || pane.Buf.Thread != "api-tests" {
		t.Fatalf("<C-v> in a picker should use the pane; got %d windows", len(h.a.root.windows()))
	}

	h.press("<Space>", "p", "c")
	if h.a.cur != coord || pane.Buf.Thread != "api-tests" {
		t.Fatal("<leader>pc should focus the coordinator and leave the pane")
	}
	h.press(":b auth-refactor", "<CR>", "<C-l>", "<Space>", "p", "c")
	if h.a.cur != coord || coord.Buf.Thread != model.Coordinator || pane.Buf.Thread != "api-tests" {
		t.Fatalf("<leader>pc from the pane should show the coordinator on the left; cur %q", h.a.cur.Buf.Name)
	}
}

func TestPickerOpensInPlaceOrSplit(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press("<Space>", "<Space>", "api", "<C-x>")
	wins := h.a.root.windows()
	if len(wins) != 3 || wins[0] != h.a.cur || h.a.cur.Buf.Thread != "api-tests" || wins[1].Buf.Thread != model.Coordinator {
		t.Fatalf("<C-x> in a picker should split api-tests above the coordinator; got %d windows", len(wins))
	}
	h.press("<Space>", "<Space>", "auth", "<CR>")
	if len(h.a.root.windows()) != 3 || h.a.cur != wins[0] || h.a.cur.Buf.Thread != "auth-refactor" {
		t.Fatal("<CR> in the picker should switch the current window")
	}
}

func TestEventsAppendLiveAndFold(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press(":b auth-refactor", "<CR>")
	h.a.apply(model.Update{Kind: model.UpdateEvent, Workspace: "demo", Thread: "auth-refactor",
		Event: &model.Event{Kind: model.EventTool, Tool: "Edit", Detail: "session.go"}})
	h.a.draw()
	h.see("Edit  session.go")
	h.see("3 tool calls")
	h.a.apply(model.Update{Kind: model.UpdateEvent, Workspace: "demo", Thread: "auth-refactor",
		Event: &model.Event{Kind: model.EventTurnEnd}})
	h.a.draw()
	h.see("▸ 3 tool calls")

	w := h.a.cur
	lines := w.Buf.Lines(h.a.textWidth(w), h.a.th, 0)
	for i, l := range lines {
		if strings.Contains(l.Text(), "3 tool calls") {
			h.a.moveTo(w, i)
		}
	}
	h.press("za")
	h.see("▾ 3 tool calls")
	h.see("Read  internal/auth")
}

func TestPermissionCardAndAnswer(t *testing.T) {
	h := start(t)
	h.openDemo()
	v := h.a.threads["auth-refactor"]
	v.State = model.StateWaiting
	v.Waiting = "permission"
	v.Permission = &model.Permission{ID: "p1", Tool: "Bash", Detail: "rm -rf ./tmp/cache"}
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.a.draw()
	h.see("needs permission · Bash")
	h.see("◆ 1")

	// The card shows in its window; <leader>a allows it.
	h.press(":b auth-refactor", "<CR>")
	h.see("permission")
	h.see("rm -rf ./tmp/cache")
	h.press("<Space>", "y")
	if len(h.hub.answers) != 0 {
		t.Fatal("<leader>y yanks; it must not answer a permission")
	}
	h.see("␣a allow")
	h.press("<Space>", "a")
	if len(h.hub.answers) != 1 || h.hub.answers[0] != "auth-refactor p1 allow " {
		t.Fatalf("answers = %q", h.hub.answers)
	}
	h.press(":deny not now", "<CR>")
	if h.hub.answers[1] != "auth-refactor p1 deny not now" {
		t.Fatalf("answers = %q", h.hub.answers)
	}
}

func TestNewWorkspaceFolder(t *testing.T) {
	tmp := t.TempDir()
	for _, d := range []string{"alpha", "beta/inner", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(tmp)
	t.Setenv("HOME", tmp)
	h := start(t)
	last := func() model.WorkspaceView { return h.hub.workspaces[len(h.hub.workspaces)-1] }

	h.press(":new x "+tmp+"/al", "<Tab>", "<CR>", "<Esc>")
	if p := last(); p.Name != "x" || p.Pin != filepath.Join(tmp, "alpha") {
		t.Fatalf("<Tab> should complete the folder; got %+v", p)
	}

	h.press(":new y alpha", "<CR>", "<Esc>")
	if p := last(); p.Name != "y" || p.Pin != filepath.Join(tmp, "alpha") {
		t.Fatalf("a relative folder should be made absolute here; got %+v", p)
	}

	h.press(":new z", "<CR>")
	if h.a.mode != modePicker {
		t.Fatalf(":new without a folder should open the folder picker; mode %v", h.a.mode)
	}
	h.see("not pinned")
	h.see("~/beta")
	if strings.Contains(h.screen(), ".hidden") {
		t.Fatal("the folder picker should hide dot folders")
	}
	h.press("inner")
	h.see("~/beta/inner")
	h.press("<BS>", "<BS>", "<BS>", "<BS>", "<BS>", "<CR>")
	if p := last(); p.Name != "z" || p.Pin != "" {
		t.Fatalf("the first choice should create an unpinned workspace; got %+v", p)
	}
}

func TestCommandsAndCompletion(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press(":b api", "<Tab>", "<CR>")
	if h.a.cur.Buf.Thread != "api-tests" {
		t.Fatalf(":b with completion opened %q", h.a.cur.Buf.Name)
	}
	h.press(":resolve", "<CR>")
	if len(h.hub.resolved) != 1 || h.hub.resolved[0] != "api-tests" {
		t.Fatalf("resolved = %q", h.hub.resolved)
	}
	h.press(":open docs gpu1:~/proj", "<CR>")
	if len(h.hub.opened) != 1 || h.hub.opened[0] != "docs@gpu1:~/proj" {
		t.Fatalf("opened = %q", h.hub.opened)
	}
	if h.a.cur.Buf.Thread != "docs" || h.a.mode != modeInsert {
		t.Fatalf(":open should show the new thread ready to write; buf %q mode %v", h.a.cur.Buf.Name, h.a.mode)
	}
	h.press("<Esc>", ":nonsense", "<CR>")
	h.see("not a command: nonsense")
}

func TestSplitsAndWindowMotion(t *testing.T) {
	h := start(t)
	h.openDemo()
	first := h.a.cur
	h.press("<C-w>", "v")
	wins := h.a.root.windows()
	if len(wins) != 3 || wins[1] != h.a.cur || wins[0] != first {
		t.Fatalf("<C-w>v should open a focused window on the right; got %d windows", len(wins))
	}
	h.press("<C-w>", "h")
	if h.a.cur != first {
		t.Fatal("<C-w>h should move to the left window")
	}
	h.press("<C-w>", "l")
	if h.a.cur != wins[1] {
		t.Fatal("<C-w>l should move to the right window")
	}
	h.press("<C-w>", "c")
	if n := len(h.a.root.windows()); n != 2 {
		t.Fatalf("%d windows after <C-w>c", n)
	}
	h.press("<C-w>", "s", "<C-w>", "o")
	if n := len(h.a.root.windows()); n != 1 {
		t.Fatalf("%d windows after <C-w>o", n)
	}
}

func TestPickerFiltersAndOpens(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press("<Space>", "<Space>")
	if h.a.mode != modePicker {
		t.Fatal("<leader><leader> should open the buffers picker")
	}
	h.see("buffers")
	h.press("apit")
	if len(h.a.picker.items) != 1 || h.a.picker.items[0].label != "api-tests" {
		t.Fatalf("items = %+v", h.a.picker.items)
	}
	h.press("<CR>")
	if h.a.cur.Buf.Thread != "api-tests" || h.a.mode != modeNormal {
		t.Fatalf("picker did not open api-tests: %q", h.a.cur.Buf.Name)
	}
}

func TestSearchAndYank(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press("gg", "/session", "<CR>")
	l := h.a.line(h.a.cur)
	if !strings.Contains(l.Text(), "session") {
		t.Fatalf("search landed on %q", l.Text())
	}
	h.press("yy")
	if got := string(h.s.GetClipboardData()); !strings.Contains(got, "split the session store out") {
		t.Fatalf("clipboard = %q", got)
	}
}

func TestQuitClosesLastWindow(t *testing.T) {
	h := start(t)
	h.hub.workspaces[0].Counts = nil
	h.a.loadWorkspaces()
	h.press(":q", "<CR>")
	if !h.a.quit {
		t.Fatal(":q on the last window should quit")
	}
}

// TestQuitRefusesWhileThreadsRun: the hub lives in this process, so quitting
// stops every running turn. Like unsaved changes in Vim, that needs a !.
func TestQuitRefusesWhileThreadsRun(t *testing.T) {
	h := start(t)
	for _, cmd := range []string{":q", ":qa"} {
		h.press(cmd, "<CR>")
		if h.a.quit {
			t.Fatalf("%s quit with a thread running", cmd)
		}
		h.see("1 running · :qa! stops them")
	}
	h.press(":qa!", "<CR>")
	if !h.a.quit {
		t.Fatal(":qa! should quit")
	}
}

func TestWrap(t *testing.T) {
	th := newTheme()
	lines := wrap([]Span{{"the quick brown fox jumps over the lazy dog", th.Text}}, 16, []Span{{"▎ ", th.Iris}}, []Span{{"▎ ", th.Iris}})
	for _, l := range lines {
		if w := l.prefixWidth() + width(l.Text()); w > 16 {
			t.Errorf("line %q is %d wide", l.Text(), w)
		}
	}
	if len(lines) != 4 || lines[0].Text() != "the quick" {
		t.Errorf("lines = %q", texts(lines))
	}
	long := wrap([]Span{{strings.Repeat("x", 30), th.Text}}, 10, nil, nil)
	if len(long) != 3 {
		t.Errorf("a long word should split across lines: %q", texts(long))
	}
	if got := wrap([]Span{{"a\nb", th.Text}}, 20, nil, nil); len(got) != 2 {
		t.Errorf("newline should break: %q", texts(got))
	}
}

func TestMarkdown(t *testing.T) {
	th := newTheme()
	src := "# Title\n\nSome **bold** and `code`.\n\n- one\n- two\n\n```go\nfunc main() {}\n```\n"
	got := texts(renderMarkdown(src, 60, th, nil))
	want := []string{"Title", "", "Some bold and code.", "", "one", "two", "", "func main() {}"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("markdown =\n%q\nwant\n%q", got, want)
	}
}

func TestMarkdownTableAlignsColumns(t *testing.T) {
	th := newTheme()
	src := "| PR | Saved | Result |\n|---|---:|---|\n| store-clients#62 | ~2,050 | Confirmed |\n| D. app-code#59746 | ~250 | Not proven |\n"
	got := texts(renderMarkdown(src, 80, th, nil))
	if len(got) != 4 {
		t.Fatalf("want header, rule, and two rows; got %q", got)
	}
	bar := strings.Index(got[0], "│")
	for _, l := range []string{got[2], got[3]} {
		if strings.Index(l, "│") != bar {
			t.Errorf("columns do not line up:\n%s", strings.Join(got, "\n"))
		}
	}
	if !strings.Contains(got[1], "─┼─") {
		t.Errorf("want a rule under the header, got %q", got[1])
	}
	if !strings.Contains(got[3], "│   ~250 │") {
		t.Errorf("a right-aligned column should pad on the left, got %q", got[3])
	}
	narrow := texts(renderMarkdown(src, 30, th, nil))
	for _, l := range narrow {
		if width(l) > 30 {
			t.Errorf("a wide table should shrink to the window, got %q (%d)", l, width(l))
		}
	}
	if all := strings.Join(narrow, " "); !strings.Contains(all, "proven") || strings.Contains(all, "…") {
		t.Errorf("a narrow table should wrap its cells, not cut them:\n%s", strings.Join(narrow, "\n"))
	}
}

func texts(lines []Line) []string {
	var out []string
	for _, l := range lines {
		out = append(out, l.Text())
	}
	return out
}

func TestWrapKeepsAStyledWordTogether(t *testing.T) {
	th := newTheme()
	got := texts(renderMarkdown("Call report_progress with the summary", 24, th, nil))
	for _, l := range got {
		if strings.HasSuffix(l, "report_") || strings.HasPrefix(l, "progress") {
			t.Fatalf("report_progress was split: %q", got)
		}
	}
}

func TestCompletionMenu(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press("i")
	h.settle()

	h.press("/rev")
	h.see("/code-review")
	h.press("<Tab>")
	if h.prompt() != "/code-review " || h.a.menu != nil {
		t.Fatalf("prompt = %q, menu %v", h.prompt(), h.a.menu)
	}

	// `/` is a command only at the start; later it is a path or plain text.
	h.press("and/rev")
	if h.a.menu != nil {
		t.Fatal("a `/` inside the text opened the menu")
	}

	// `@` offers threads, marked `#`, before files.
	h.press(" @a")
	h.see("#auth-refactor")
	h.see("#api-tests")
	if it := h.a.menu.items[0]; it.label != "auth-refactor" || it.glyph.Text != "#" {
		t.Fatalf("first row = %+v, want the thread auth-refactor", it)
	}
	h.press("<C-n>", "<CR>")
	if len(h.hub.sent) != 0 {
		t.Fatalf("<CR> on the menu sent the prompt: %q", h.hub.sent)
	}
	h.press("@auth.go")
	h.see("internal/auth/auth.go")
	h.press("<Tab>")
	want := "/code-review and/rev @api-tests @internal/auth/auth.go "
	if h.prompt() != want {
		t.Fatalf("prompt = %q, want %q", h.prompt(), want)
	}
	h.press("#a")
	if h.a.menu != nil {
		t.Fatal("`#` opened the menu")
	}

	// <Esc> closes the menu and stays in Insert; the word stays closed.
	h.press(" @", "<Esc>")
	if h.a.menu != nil || h.a.mode != modeInsert {
		t.Fatalf("menu %v mode %v after <Esc>", h.a.menu, h.a.mode)
	}
	h.press("a")
	if h.a.menu != nil {
		t.Fatal("the closed word opened again")
	}
	h.press("<Esc>")
	if h.a.mode != modeNormal {
		t.Fatal("a second <Esc> should leave Insert")
	}
}

func TestDeleteThreadFromThePicker(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press(":b auth-refactor", "<CR>")

	h.press("<Space>", "<Space>", "auth", "<C-d>")
	h.see("delete auth-refactor and its transcript? y/n")
	h.press("n")
	if len(h.hub.deleted) != 0 {
		t.Fatal("n deleted the thread")
	}
	h.press("<Space>", "<Space>", "coord", "<C-d>")
	h.see("the coordinator cannot be deleted")

	h.press("<Space>", "<Space>", "auth", "<C-d>", "y")
	if strings.Join(h.hub.deleted, " ") != "auth-refactor" {
		t.Fatalf("deleted = %q", h.hub.deleted)
	}
	h.a.apply(model.Update{Kind: model.UpdateDeleted, Workspace: "demo", Thread: "auth-refactor"})
	h.a.draw()
	if h.a.cur.Buf.Thread != model.Coordinator {
		t.Fatalf("the window still shows %q", h.a.cur.Buf.Thread)
	}
	if _, ok := h.a.threads["auth-refactor"]; ok || slices.Contains(h.a.order, "auth-refactor") {
		t.Fatal("the thread is still listed")
	}
	h.see("auth-refactor deleted")
}

func TestHalfPagesAndSearchRecenter(t *testing.T) {
	h := start(t)
	h.openDemo()
	var evs []model.Event
	for i := 0; i < 200; i++ {
		evs = append(evs, model.Event{Kind: model.EventNote, Text: fmt.Sprintf("line %d", i)})
	}
	evs = append(evs, model.Event{Kind: model.EventNote, Text: "needle"})
	for i := 0; i < 200; i++ {
		evs = append(evs, model.Event{Kind: model.EventNote, Text: fmt.Sprintf("more %d", i)})
	}
	for _, e := range evs {
		h.a.chat(model.Coordinator).AddEvent(e)
	}
	h.a.draw()
	w := h.a.cur
	centered := func(what string) {
		t.Helper()
		if mid := w.Top + w.textH/2; w.Cur != mid {
			t.Fatalf("%s: cursor line %d, middle %d", what, w.Cur, mid)
		}
	}
	h.press("gg", "<C-d>")
	centered("<C-d>")
	h.press("/needle", "<CR>", "n")
	centered("n")
	if !strings.Contains(h.a.line(w).Text(), "needle") {
		t.Fatalf("n landed on %q", h.a.line(w).Text())
	}
}

func TestLeaderYankYanksTheLine(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press("gg", "<Space>", "y")
	h.see("yanked 1 line")
	h.press("V", "j", "<Space>", "Y")
	h.see("yanked 2 lines")
}

func TestCompletionsReloadWhenTheTurnEnds(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press(":b auth-refactor", "<CR>", "i")
	h.settle()
	delete(h.a.comps, h.a.cur.Buf.Name)
	v := h.a.threads["auth-refactor"]
	v.State = model.StateIdle
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.settle()
	h.press("/rev")
	h.see("/code-review")
}

func TestStatusLineShowsUsage(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press("<C-w>", "o")
	v := h.a.threads[model.Coordinator]
	v.Model, v.Context, v.ContextWindow, v.Cost, v.Branch = "claude-opus-5[1m]", 81000, 1000000, 9.17, "neoharness-hub-tui"
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.a.draw()
	h.see("neoharness-hub-tui · opus-5 1m · ctx 8% · $9.17")

	// Narrow windows drop the branch and model first; context stays.
	h.press("<C-w>", "v", "<C-w>", "v", "<C-w>", "h", "<C-w>", "h")
	h.a.draw()
	scr := h.screen()
	if !strings.Contains(scr, "ctx 8%") || strings.Contains(scr, "neoharness-hub-tui ·") {
		t.Fatalf("narrow status line:\n%s", scr)
	}
}

func TestClearEmptiesTheChat(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.see("split the session store out")
	h.a.apply(model.Update{Kind: model.UpdateEvent, Workspace: "demo", Thread: model.Coordinator,
		Event: &model.Event{Kind: model.EventClear, Text: "Cleared. The next message starts a new conversation."}})
	h.a.draw()
	if strings.Contains(h.screen(), "split the session store out") {
		t.Fatal("the chat still shows what came before /clear")
	}
	h.see("Cleared. The next message starts a new conversation.")
}

// fgAt returns the foreground color of the first cell of want on screen.
func (h *neoharness) fgAt(want string) tcell.Color {
	h.t.Helper()
	cells, w, _ := h.s.GetContents()
	for i, line := range strings.Split(h.screen(), "\n") {
		if j := strings.Index(line, want); j >= 0 {
			fg, _, _ := cells[i*w+len([]rune(line[:j]))].Style.Decompose()
			return fg
		}
	}
	h.t.Fatalf("%q is not on screen", want)
	return 0
}

func TestStatusColorsAndPromptHeight(t *testing.T) {
	h := start(t)
	h.openDemo()
	v := h.a.threads[model.Coordinator]
	v.Model, v.Context, v.ContextWindow, v.Cost, v.Branch = "claude-opus-5[1m]", 100, 1000, 9.17, "trunk"
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.a.draw()
	for want, color := range map[string]tcell.Color{"trunk": rpFoam, "opus-5 1m": rpRose, "ctx 10%": rpGold, "$9.17": rpPine} {
		if got := h.fgAt(want); got != color {
			t.Errorf("%s: color %v, want %v", want, got, color)
		}
	}
	v.Context = 850
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.a.draw()
	if got := h.fgAt("ctx 85%"); got != rpLove {
		t.Errorf("a nearly full context should be red, got %v", got)
	}

	// The prompt takes three lines above the status line; the cursor is on
	// the first.
	h.press("i")
	lines := strings.Split(h.screen(), "\n")
	status := 0
	for i, l := range lines {
		if strings.Contains(l, "coordinator  ○ idle") {
			status = i
		}
	}
	if !strings.Contains(lines[status-3], "Message coordinator…") || h.a.cursorY != status-3 {
		t.Fatalf("prompt at %d, cursor at %d, status at %d:\n%s", status-3, h.a.cursorY, status, strings.Join(lines[status-5:status+1], "\n"))
	}
}

func TestChangeFolderAndDeleteWorkspace(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(tmp)
	h := start(t)
	h.openDemo()

	h.press(":folder other", "<CR>")
	h.press(":folder", "<CR>", "1/other", "<CR>")
	if len(h.hub.folders) != 2 || !strings.HasSuffix(h.hub.folders[0], "/other") || !strings.HasSuffix(h.hub.folders[1], "/other") {
		t.Fatalf("folders = %q, want twice .../other", h.hub.folders)
	}

	h.press("<Space>w", "demo", "<C-d>")
	h.see("delete the workspace demo and stop its threads? y/n")
	h.press("y")
	if strings.Join(h.hub.gone, " ") != "demo" {
		t.Fatalf("gone = %q", h.hub.gone)
	}
	h.a.apply(model.Update{Kind: model.UpdateDeleted, Workspace: "demo"})
	h.a.draw()
	if h.a.workspace != "" || len(h.a.workspaces) != 0 {
		t.Fatalf("still in %q, list %+v", h.a.workspace, h.a.workspaces)
	}
	h.see("workspaces")
	h.see("demo deleted · :restore demo brings it back")
}

func TestRestoreWorkspace(t *testing.T) {
	h := start(t)
	h.press(":restore", "<CR>")
	h.see("no deleted workspaces")
	h.hub.gone = []string{"old"}
	h.press(":restore", "<CR>")
	h.see("deleted: old · :restore <name>")
	h.press(":restore nope", "<CR>")
	h.see("no deleted workspace \"nope\"")
	h.press(":restore old", "<CR>")
	h.see("old restored")
	if !slices.ContainsFunc(h.a.workspaces, func(p model.WorkspaceView) bool { return p.Name == "old" }) {
		t.Fatal("the restored workspace should be in the list")
	}
}

func TestThreadsPaneGroupsByStateAndFoldsResolved(t *testing.T) {
	h := start(t)
	h.openDemo()
	states := map[string]model.State{"api-tests": model.StateResolved, "auth-refactor": model.StateWaiting, model.Coordinator: model.StateWaiting}
	for name, st := range states {
		v := h.a.threads[name]
		v.State = st
		h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: name, View: &v})
	}
	h.press("<Space>", "t")
	rows := func() []string {
		var out []string
		for _, l := range h.a.threadsBuf.Lines(80, h.a.th, 0) {
			if f := strings.Fields(l.Text()); len(f) > 0 {
				out = append(out, strings.Join(f, " "))
			}
		}
		return out
	}
	got := strings.Join(rows(), " | ")
	want := "waiting | ◆ auth-refactor question | ▸ 1 resolved"
	if got != want {
		t.Fatalf("rows = %q\nwant   %q", got, want)
	}
	h.press("G", "<CR>")
	if got := strings.Join(rows(), " | "); !strings.HasSuffix(got, "▾ 1 resolved | ✓ api-tests") {
		t.Fatalf("<CR> on the fold should unfold it: %q", got)
	}
	if h.a.cur.Buf.Kind != KindThreads {
		t.Fatal("unfolding should not leave the list")
	}
}

func TestSendNowWithCtrlXCtrlS(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press("i", "stop", "<C-x>", "<C-s>")
	h.press("x", "<C-x>", "y", "<CR>")
	if strings.Join(h.hub.sent, "|") != "now coordinator: stop|coordinator: xy" {
		t.Fatalf("sent = %q", h.hub.sent)
	}
}

func TestGoToWorkspacesAndCoordinator(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press(":b auth-refactor", "<CR>", "<Space>pc")
	if h.a.cur.Buf.Thread != model.Coordinator {
		t.Fatalf("<leader>pc showed %q", h.a.cur.Buf.Thread)
	}
	h.press("<Space>pv")
	h.see("workspaces")
}

func TestToastsForThreadsOffScreen(t *testing.T) {
	h := start(t)
	h.openDemo()
	v := h.a.threads["api-tests"]
	v.State = model.StateWaiting
	v.Waiting = "Which branch?"
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.a.draw()
	h.see("asks: Which branch?")

	// A thread on screen needs no toast.
	run := h.a.threads[model.Coordinator]
	run.State = model.StateRunning
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: run.Name, View: &run})
	run.State = model.StateIdle
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: run.Name, View: &run})
	if len(h.a.toasts) != 1 {
		t.Fatalf("toasts = %d, want only api-tests", len(h.a.toasts))
	}

	// A finished turn off screen shows the reply's first line.
	h.a.chat("auth-refactor").AddEvent(model.Event{Kind: model.EventText, Text: "Moved the store.\nDetails follow."})
	done := h.a.threads["auth-refactor"]
	done.State = model.StateIdle
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: done.Name, View: &done})
	h.a.draw()
	h.see("Moved the store.")

	// Going to the waiting thread clears its toast; <leader>nc clears the rest.
	h.press(":b api-tests", "<CR>")
	h.a.draw()
	for _, ts := range h.a.toasts {
		if ts.thread == "api-tests" {
			t.Fatal("the waiting toast stayed after going to the thread")
		}
	}
	h.press("<Space>nc")
	h.a.draw()
	if len(h.a.toasts) != 0 || strings.Contains(h.screen(), "Moved the store.") {
		t.Fatal("<leader>nc should dismiss every toast")
	}
	h.press("<Space>nh")
	h.see("auth-refactor: Moved the store.")
}

func TestStoppedTurnStopsSpinning(t *testing.T) {
	h := start(t)
	h.openDemo()
	v := h.a.threads[model.Coordinator]
	v.State = model.StateWaiting
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	for _, e := range []model.Event{
		{Kind: model.EventTool, Tool: "Write", Detail: "note.txt"},
		{Kind: model.EventPermission, Tool: "Write", Detail: "note.txt"},
		{Kind: model.EventMessage, From: model.User, Text: "forget it"},
		{Kind: model.EventNote, Text: "Interrupted."},
	} {
		h.a.apply(model.Update{Kind: model.UpdateEvent, Workspace: "demo", Thread: v.Name, Event: &e})
	}
	v.State = model.StateIdle
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	for _, bl := range h.a.chat(model.Coordinator).blocks {
		if bl.live {
			t.Fatal("a tool block of a stopped turn is still live")
		}
	}
}

func TestShellCommandShowsAndFolds(t *testing.T) {
	h := start(t)
	h.openDemo()
	var out []string
	for i := 1; i <= 12; i++ {
		out = append(out, fmt.Sprintf("line%d\t\x1b[31mred\x1b[0m", i))
	}
	for _, e := range []model.Event{
		{Kind: model.EventShell, ID: "x", Detail: "ls -la"},
		{Kind: model.EventShell, ID: "x", Detail: "ls -la", Text: strings.Join(out, "\n"), Exit: 1},
	} {
		h.a.apply(model.Update{Kind: model.UpdateEvent, Workspace: "demo", Thread: model.Coordinator, Event: &e})
	}
	h.a.draw()
	h.see("! ls -la")
	h.see("line1    red")
	h.see("… 2 more lines · za")
	h.see("exit 1")
	if strings.Contains(h.screen(), "line11") {
		t.Fatal("output past 10 lines shows while folded")
	}
	h.press("G")
	for !strings.Contains(h.a.line(h.a.cur).Text(), "more lines") {
		h.press("k")
	}
	h.press("za")
	h.see("line12")
	h.press("i", "!ls", "<CR>")
	if h.hub.sent[len(h.hub.sent)-1] != "coordinator: !ls" {
		t.Fatalf("sent = %q", h.hub.sent)
	}
}

func TestArtifactOpensInTheWindowAndCtrlOGoesBack(t *testing.T) {
	h := start(t)
	h.hub.arts = []model.Artifact{{Name: "design", Title: "Session store design", Thread: "auth-refactor", Version: 1}}
	h.hub.docs = map[string]string{"design": "# Session store design\n\nFirst draft."}
	h.openDemo()
	layout, coord := h.a.root, h.a.cur
	e := model.Event{Kind: model.EventArtifact, Detail: "design", Text: "Session store design", Version: 1}
	h.a.apply(model.Update{Kind: model.UpdateEvent, Workspace: "demo", Thread: model.Coordinator, Event: &e})
	h.a.draw()
	h.see("◇ Session store design  · artifact")

	h.press("G")
	for !strings.Contains(h.a.line(coord).Text(), "artifact") {
		h.press("k")
	}
	chatRow := coord.Cur
	h.press("<CR>")
	if h.a.root != layout || h.a.cur != coord || coord.Buf.Kind != KindArtifact {
		t.Fatalf("<CR> should show the artifact in the same window; cur %q", h.a.cur.Buf.Name)
	}
	h.see("by auth-refactor · v1")
	h.see("First draft.")

	h.hub.arts[0].Version = 2
	h.hub.docs["design"] = "# Session store design\n\nSecond draft."
	a := h.hub.arts[0]
	h.a.apply(model.Update{Kind: model.UpdateArtifact, Workspace: "demo", Thread: "auth-refactor", Artifact: &a})
	h.a.draw()
	h.see("Second draft.")

	h.press("<Esc>")
	if coord.Buf.Kind != KindArtifact {
		t.Fatal("<Esc> in Normal keeps the artifact")
	}
	h.press("<C-o>")
	if coord.Buf.Thread != model.Coordinator || coord.Cur != chatRow {
		t.Fatalf("<C-o> should go back to the chat, on its line: %q row %d, want %d", coord.Buf.Name, coord.Cur, chatRow)
	}
	h.press("<C-^>")
	if coord.Buf.Artifact != "design" {
		t.Fatal("<C-^> should show the artifact again")
	}
	h.press("V")
	// A fast <Esc><C-o> reaches the app as one Alt+<C-o>.
	h.a.handle(tcell.NewEventKey(tcell.KeyCtrlO, 0, tcell.ModAlt))
	if h.a.mode != modeNormal || coord.Buf.Thread != model.Coordinator {
		t.Fatalf("<Esc><C-o> should leave Visual and go back: mode %v, %q", h.a.mode, coord.Buf.Name)
	}
	h.press("<Space>", "p", "a", "<CR>")
	if h.a.cur.Buf.Artifact != "design" || h.a.root != layout {
		t.Fatal("<leader>pa should open the artifact in the window too")
	}
}

// docRows renders an artifact as its window shows it in Normal mode, with
// no line under the cursor.
func docRows(a model.Artifact, src string, w int, th *Theme) []Line {
	e := newEditor(a.Name, src, a.Version)
	e.art = a
	return e.render(w, th)
}

func TestDocumentRendering(t *testing.T) {
	th := newTheme()
	src := "# Title\n\nSee [the docs](https://x.dev).\n\n## Steps\n\n- [x] done\n- [ ] todo\n  - nested\n\n" +
		"1. a\n2. b\n3. c\n4. d\n5. e\n6. f\n7. g\n8. h\n9. i\n10. j\n\n> [!WARNING]\n> Careful here.\n\n```go\nfmt.Println(1)\n```\n\n" +
		"| Option | Cost |\n|---|---|\n| shared cache | one more service to run, and a network hop on every read of the hot path |\n"
	lines := docRows(model.Artifact{Name: "doc", Thread: "api", Version: 3}, src, 80, th)
	var text []string
	var heads []string
	for _, l := range lines {
		text = append(text, strings.TrimRight(spansText(l.Prefix)+l.Text(), " "))
		if l.Msg {
			heads = append(heads, l.Text())
		}
	}
	all := strings.Join(text, "\n")
	for _, want := range []string{
		"◇ doc · by api · v3",
		"  Title\n  ━━━━",
		"  Steps\n  ────",
		"See the docs.",
		"  ☑ done", "  ☐ todo", "    ◦ nested",
		"shared cache │ one more service to run, and a network hop on every read of the\n" + strings.Repeat(" ", 15) + "│ hot path",
		"   9. i", "  10. j",
		"▎ warning\n  ▎ Careful here.",
		"  go\n  │ fmt.Println(1)",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "[!WARNING]") {
		t.Error("the alert marker should not show")
	}
	for _, l := range lines {
		for _, sp := range l.Spans {
			if sp.Text == "the docs" && sp.Style != linkStyle(th, "https://x.dev") {
				t.Error("a link should carry its URL for the terminal, not show it")
			}
		}
	}
	if strings.Join(heads, "|") != "Title|Steps" {
		t.Errorf("headings for ]m = %q", heads)
	}
	for _, l := range lines {
		if w := width(spansText(l.Prefix) + l.Text()); w > 80 {
			t.Errorf("line wider than the window: %q", l.Text())
		}
	}
}

// counting counts the cells the app passes on to the terminal.
type counting struct {
	tcell.SimulationScreen
	n int
}

func (c *counting) SetContent(x, y int, r rune, comb []rune, st tcell.Style) {
	c.n++
	c.SimulationScreen.SetContent(x, y, r, comb, st)
}

func TestFramesSendOnlyWhatChangedAndTheCursorIsDrawn(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(120, 36)
	s := &counting{SimulationScreen: sim}
	h := &neoharness{t: t, a: newApp(s, newFake()), s: sim, hub: nil}
	h.a.draw()
	h.openDemo()
	h.press(":b auth-refactor", "<CR>")
	if !h.a.chat("auth-refactor").Live() {
		t.Fatal("the running thread should have a spinner")
	}
	s.n = 0
	h.a.draw()
	if s.n != 0 {
		t.Fatalf("an unchanged frame sent %d cells", s.n)
	}
	h.a.spin++
	h.a.draw()
	if s.n == 0 || s.n > 3 {
		t.Fatalf("a spinner frame sent %d cells, want the spinner only", s.n)
	}

	if _, _, visible := sim.GetCursor(); visible {
		t.Fatal("the terminal cursor should stay hidden")
	}
	h.press("i")
	cells, w, _ := sim.GetContents()
	c := cells[h.a.cursorY*w+h.a.cursorX]
	if _, _, attrs := c.Style.Decompose(); attrs&tcell.AttrReverse == 0 {
		t.Fatal("the cursor cell should be drawn in reverse")
	}
}

func TestOnlyTheNewestToolBlockSpins(t *testing.T) {
	b := newChat("demo", "api")
	for _, e := range []model.Event{
		{Kind: model.EventTool, Tool: "Write", Detail: "a"},
		{Kind: model.EventPermission, Tool: "Write", Detail: "a"},
		{Kind: model.EventTool, Tool: "Read", Detail: "b"},
	} {
		b.AddEvent(e)
	}
	live := 0
	for _, bl := range b.blocks {
		if bl.live {
			live++
		}
	}
	if live != 1 || !b.last().live {
		t.Fatalf("%d live blocks; only the last should spin", live)
	}
}

func TestArtifactNamesInAChatAreLinks(t *testing.T) {
	h := start(t)
	h.hub.arts = []model.Artifact{{Name: "cache-design", Title: "Cache", Thread: "auth-refactor", Version: 1}}
	h.hub.docs = map[string]string{"cache-design": "# Cache\n\nLRU."}
	h.openDemo()
	e := model.Event{Kind: model.EventText, Text: "auth-refactor wrote `cache-design`; see the cache-designer note too."}
	h.a.apply(model.Update{Kind: model.UpdateEvent, Workspace: "demo", Thread: model.Coordinator, Event: &e})
	h.a.draw()
	cells, w, _ := h.s.GetContents()
	screen := h.screen()
	i := strings.Index(screen, "cache-design;")
	line := strings.Count(screen[:i], "\n")
	col := len([]rune(screen[strings.LastIndex(screen[:i], "\n")+1 : i]))
	if _, _, attrs := cells[line*w+col].Style.Decompose(); attrs&tcell.AttrUnderline == 0 {
		t.Fatal("the artifact name should be drawn as a link")
	}
	j := strings.Index(screen, "cache-designer")
	jl, jc := strings.Count(screen[:j], "\n"), len([]rune(screen[strings.LastIndex(screen[:j], "\n")+1:j]))
	if _, _, attrs := cells[jl*w+jc].Style.Decompose(); attrs&tcell.AttrUnderline != 0 {
		t.Fatal("a longer word that starts with the name is not a link")
	}

	h.press("G")
	for !strings.Contains(h.a.line(h.a.cur).Text(), "cache-design") {
		h.press("k")
	}
	h.press("<CR>")
	if h.a.cur.Buf.Kind != KindArtifact || h.a.cur.Buf.Artifact != "cache-design" {
		t.Fatalf("<CR> on the line should open the artifact; cur %q", h.a.cur.Buf.Name)
	}
	h.press("<C-w>", "h", "<Space>", "p", "a")
	if h.a.picker == nil || h.a.picker.title != "artifacts" {
		t.Fatal("<leader>pa should open the artifacts picker")
	}
}

func TestTurnEndStopsEverySpinner(t *testing.T) {
	b := newChat("demo", "api")
	for _, e := range []model.Event{
		{Kind: model.EventTool, Tool: "mcp__neoharness__ask_user", Detail: "which release?"},
		{Kind: model.EventAsk, Text: "which release?"},
		{Kind: model.EventTurnEnd},
	} {
		b.AddEvent(e)
	}
	if b.Live() {
		t.Fatal("a turn that ended still spins")
	}
}

func TestArtifactRailFollowsTheCursor(t *testing.T) {
	h := start(t)
	body := "# Design\n\nIntro.\n\n## Store\n\n" + strings.Repeat("Text.\n\n", 30) + "## Tunnel\n\nEnd."
	h.hub.arts = []model.Artifact{{Name: "design", Title: "Design", Thread: "auth-refactor", Version: 1}}
	h.hub.docs = map[string]string{"design": body}
	h.openDemo()
	h.a.openArtifact("design")
	h.press("<C-w>", "o")
	h.a.draw()
	h.see("━━━")
	h.see("──")
	h.press("G")
	h.a.draw()
	if !strings.Contains(h.screen(), "───") || !strings.Contains(h.screen(), "━━") {
		t.Fatalf("at the end the last section should be the heavy dash:\n%s", h.screen())
	}
	w := h.a.cur
	if strings.Contains(h.screen(), "─ Tunnel") || strings.Contains(h.screen(), "─  Tunnel") {
		t.Fatal("the rail shows no names")
	}
	if lines := w.Buf.Lines(h.a.textWidth(w), h.a.th, 0); !strings.Contains(lines[len(lines)-1].Text(), "End.") || h.a.textWidth(w) != w.rect.W-1-railWidth(w) {
		t.Fatal("the text should start after the rail")
	}
}

func editDemo(t *testing.T, body string) *neoharness {
	h := start(t)
	h.hub.arts = []model.Artifact{{Name: "design", Title: "Design", Thread: "auth-refactor", Version: 1}}
	h.hub.docs = map[string]string{"design": body}
	h.openDemo()
	h.a.openArtifact("design")
	h.a.draw()
	return h
}

func TestOutlineJumpsToAHeading(t *testing.T) {
	h := editDemo(t, "# Design\n\nIntro.\n\n## Store\n\nKeep it in redis.\n\n## Auth\n\nTokens.\n")
	h.press("g", "O")
	if h.a.mode != modePicker || len(h.a.picker.items) != 3 {
		t.Fatalf("gO should list the 3 headings, mode %v", h.a.mode)
	}
	h.press("Auth", "<CR>")
	if got := h.a.line(h.a.cur).Text(); !strings.Contains(got, "Auth") {
		t.Fatalf("cursor on %q, want the Auth heading", got)
	}
}

func TestGfOpensTheArtifactUnderTheCursor(t *testing.T) {
	h := start(t)
	h.hub.arts = []model.Artifact{
		{Name: "design", Title: "Design", Thread: "auth-refactor", Version: 1},
		{Name: "doc-notes", Title: "Notes", Thread: "auth-refactor", Version: 1},
	}
	h.hub.docs = map[string]string{"design": "# D\n\nSee doc-notes here.\n", "doc-notes": "# Notes\n"}
	h.openDemo()
	h.a.openArtifact("design")
	h.a.draw()
	h.press("g", "f")
	if !strings.Contains(h.a.notice, "no artifact") {
		t.Fatalf("gf off a name should say so, notice %q", h.a.notice)
	}
	h.press("/doc-notes", "<CR>", "g", "f")
	if h.a.cur.Buf.Artifact != "doc-notes" {
		t.Fatal("gf should open the artifact named under the cursor")
	}
}

func TestMacrosRecordAndPlay(t *testing.T) {
	h, ed := srcEdit(t, "a1\na2\na3\na4\na5")
	h.press("q", "x", "A", "!", "<Esc>", "j", "q")
	if h.a.recordInto != 0 {
		t.Fatal("q should stop recording")
	}
	h.press("@", "x", "@", "@")
	if got, want := ed.Text(), "# D\n\na1!\na2!\na3!\na4\na5\n"; got != want {
		t.Fatalf("after @x @@: %q, want %q", got, want)
	}
	h.press("2", "@", "x")
	if got, want := ed.Text(), "# D\n\na1!\na2!\na3!\na4!\na5!\n"; got != want {
		t.Fatalf("after 2@x: %q, want %q", got, want)
	}
	h.press("q", "y")
	h.a.draw()
	h.see("recording @y")
	h.press("q")
	if h.a.recordInto != 0 || h.a.cur.Buf.Edit == nil {
		t.Fatal("q stops recording and keeps the artifact")
	}
}

func TestMotionsCountWideCharactersAsTwoColumns(t *testing.T) {
	h := editDemo(t, "# D\n\n中文 a(b) end\n")
	w := h.a.cur
	h.press("/中", "<CR>")
	pre := w.Col
	for _, c := range []struct {
		keys []string
		col  int
	}{
		{[]string{"f", "("}, 6},
		{[]string{"%"}, 8},
		{[]string{"w"}, 10},
		{[]string{"F", "文"}, 2},
		{[]string{"b"}, 0},
		{[]string{"e"}, 2},
	} {
		h.press(c.keys...)
		if w.Col-pre != c.col {
			t.Fatalf("%v: column %d, want %d", c.keys, w.Col-pre, c.col)
		}
	}
}

func TestArtifactEditsInPlaceAndSaves(t *testing.T) {
	h := editDemo(t, "# Design\n\nIntro.\n\n## Store\n\nKeep it in redis.\n")
	for !strings.Contains(h.a.line(h.a.cur).Text(), "Keep it") {
		h.press("j")
	}
	h.a.draw()
	if strings.Contains(h.screen(), "## Store") || !strings.Contains(h.screen(), "Store") {
		t.Fatal("Normal mode shows the headings rendered, away from the cursor")
	}
	h.press("A", " Or sqlite.")
	h.a.draw()
	if strings.Contains(h.screen(), "## Store") {
		t.Fatal("Insert shows only the cursor's line raw")
	}
	h.press("<Up>", "<Up>")
	h.a.draw()
	h.see("## Store")
	h.press("<Down>", "<Down>", "<End>", "<Esc>")
	b := h.a.cur.Buf
	if h.a.mode != modeNormal || !b.Edit.Dirty {
		t.Fatal("<Esc> goes back to Normal with the change in the buffer")
	}
	h.a.draw()
	h.see("Keep it in redis. Or sqlite.")
	h.see("design [+]")
	if strings.Contains(h.screen(), "## Store") {
		t.Fatal("Normal mode shows the document rendered again")
	}
	h.press(":qa", "<CR>")
	if h.a.quit {
		t.Fatal(":qa should not drop an unsaved change")
	}
	h.press(":w", "<CR>")
	if h.hub.docs["design"] != "# Design\n\nIntro.\n\n## Store\n\nKeep it in redis. Or sqlite.\n" || h.hub.arts[0].Version != 2 {
		t.Fatalf("saved %q v%d", h.hub.docs["design"], h.hub.arts[0].Version)
	}
	if b.Edit.Dirty || h.a.cur.Buf != b {
		t.Fatal(":w should save and keep the artifact in the window")
	}
	h.a.draw()
	if l := h.a.line(h.a.cur).Text(); !strings.Contains(l, "Keep it in redis. Or sqlite.") {
		t.Fatalf("the cursor should stay on the edited line, got %q", l)
	}
	h.see("v2 · edited by you")
}

const richDoc = "# Title\n\nSome **bold** text that is long enough to wrap when the window is narrow, " +
	"with more words after it.\n\n## List\n\n- one\n- [ ] two\n\n| a | b |\n|---|---|\n| x | y |\n\n```go\nfmt.Println(1)\n```\n\n> [!NOTE]\n> Careful.\n\nEnd.\n"

func TestJAndKVisitEverySourceLine(t *testing.T) {
	h := editDemo(t, richDoc)
	e := h.a.cur.Buf.Edit
	h.press("gg")
	seen := []int{e.Line}
	for range 60 {
		h.press("j")
		if e.Line != seen[len(seen)-1] {
			seen = append(seen, e.Line)
		}
	}
	for i := range len(e.Src) - 1 {
		if i >= len(seen) || seen[i] != i {
			t.Fatalf("j should visit each line once, in order: %v", seen)
		}
	}
	for range 60 {
		h.press("k")
	}
	if e.Line != 0 {
		t.Fatalf("k should go back to the first line, on %d", e.Line)
	}
}

func TestTheCursorLineShowsRaw(t *testing.T) {
	h := editDemo(t, richDoc)
	h.press("/List", "<CR>")
	h.a.draw()
	h.see("## List")
	if strings.Contains(h.screen(), "**bold**") || strings.Contains(h.screen(), "# Title") {
		t.Fatal("lines away from the cursor show rendered")
	}
	h.press("V")
	h.a.draw()
	if strings.Contains(h.screen(), "**bold**") {
		t.Fatal("Visual shows only the selected lines raw")
	}
	h.press("k", "k", "k")
	h.a.draw()
	h.see("**bold**")
	h.see("## List")
	if strings.Contains(h.screen(), "# Title") {
		t.Fatal("lines out of the selection show rendered")
	}
	h.press("<Esc>")
	h.a.draw()
	h.see("**bold**")
	if strings.Contains(h.screen(), "## List") {
		t.Fatal("after Visual only the cursor's line shows raw")
	}
	h.press("/bold", "<CR>")
	if e := h.a.cur.Buf.Edit; e.Line != 2 || e.Off != 7 {
		t.Fatalf("/ finds the place in the source, at %d:%d", e.Line, e.Off)
	}
	h.press("ma", "G", "`", "a")
	if e := h.a.cur.Buf.Edit; e.Line != 2 || e.Off != 7 {
		t.Fatalf("a mark keeps its place in the source, at %d:%d", e.Line, e.Off)
	}
}

func TestReferenceLinksRenderAndOpen(t *testing.T) {
	h := editDemo(t, "# D\n\nRead [the docs][1] and [Spec][].\n\n[1]: https://docs.example.com\n[spec]: https://spec.example.com\n")
	var opened string
	h.a.openURL = func(_, url string) error { opened = url; return nil }
	h.press("G")
	h.a.draw()
	h.see("Read the docs and Spec.")
	h.see("[1]: https://docs.example.com")
	h.press("gg", "j", "j", "f", "S", "gx")
	if opened != "https://spec.example.com" {
		t.Fatalf("gx on [Spec][] opened %q", opened)
	}
	h.press("0", "f", "t", "gx")
	if opened != "https://docs.example.com" {
		t.Fatalf("gx on [the docs][1] opened %q", opened)
	}
}

func TestRenderedColumnsMapToTheSource(t *testing.T) {
	src := "Some **bold** text, a [link](https://example.com). And `code` here, then more words to wrap the line twice over."
	lines := docRows(model.Artifact{Name: "d"}, src, 40, newTheme())
	rs := []rune(src)
	for _, word := range []string{"Some", "bold", "text", "link", ". And", "code", "here", "wrap", "over"} {
		want := strings.Index(src, word)
		found := false
		for row, l := range lines {
			if l.Src != 1 {
				continue
			}
			if i := strings.Index(l.Text(), word); i >= 0 {
				col := l.prefixWidth() + width(l.Text()[:i])
				if got := renderedOff(lines, row, col, rs); got != want {
					t.Errorf("%q maps to %d, want %d", word, got, want)
				}
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%q is not on a row", word)
		}
	}
	table := docRows(model.Artifact{Name: "d"}, "| Option | Cost |\n|---|---|\n| sqlite | none at all |", 60, newTheme())
	src2 := []rune("| sqlite | none at all |")
	for row, l := range table {
		if l.Src != 3 {
			continue
		}
		for _, word := range []string{"sqlite", "none", "all"} {
			i := strings.Index(l.Text(), word)
			col := l.prefixWidth() + width(l.Text()[:i])
			if got, want := renderedOff(table, row, col, src2), strings.Index(string(src2), word); got != want {
				t.Errorf("table %q maps to %d, want %d", word, got, want)
			}
		}
	}
}

func TestArtifactKeepsItsCursorInEveryWindow(t *testing.T) {
	h := editDemo(t, "# D\n\none\n\ntwo\n\nthree\n")
	e := h.a.cur.Buf.Edit
	h.press("/three", "<CR>")
	h.press(":b coordinator", "<CR>", ":b design", "<CR>")
	if e.Line != 6 || !strings.Contains(h.a.line(h.a.cur).Text(), "three") {
		t.Fatalf(":b should come back to the artifact's cursor, on line %d", e.Line)
	}
	h.press(":vsplit", "<CR>")
	if e.Line != 6 || !strings.Contains(h.a.line(h.a.cur).Text(), "three") {
		t.Fatalf(":vsplit should show the artifact at its cursor, on line %d", e.Line)
	}
}

func TestQuestionMarkSearchesUp(t *testing.T) {
	h := editDemo(t, "# D\n\nred one\n\nred two\n\nred three\n")
	e := h.a.cur.Buf.Edit
	h.press("G", "?red", "<CR>")
	if e.Line != 6 {
		t.Fatalf("? from the end should find the last red, on %d", e.Line)
	}
	h.press("n")
	if e.Line != 4 {
		t.Fatalf("n after ? goes up, on %d", e.Line)
	}
	h.press("N")
	if e.Line != 6 {
		t.Fatalf("N after ? goes down, on %d", e.Line)
	}
}

func TestInsertUpDownKeepTheColumn(t *testing.T) {
	h := editDemo(t, "# D\n\nabcdef\n\nabcdef\n")
	e := h.a.cur.Buf.Edit
	h.press("/cd", "<CR>", "a")
	h.press("<Down>")
	if e.Line != 3 || e.Off != 0 {
		t.Fatalf("on the blank line: %d:%d", e.Line, e.Off)
	}
	h.press("<Down>", "X")
	if got := string(e.Src[4]); got != "abcXdef" {
		t.Fatalf("<Down> over a blank line should keep the column: %q", got)
	}
}

func TestUnsavedChangesStayAndDrop(t *testing.T) {
	h := editDemo(t, "# D\n\none\n")
	h.press("G", "k", "A", "!", "<Esc>")
	b := h.a.cur.Buf
	h.press(":e", "<CR>")
	if !strings.Contains(h.a.notice, "no write since") {
		t.Fatalf(":e on a changed buffer should refuse, notice %q", h.a.notice)
	}
	h.press("<C-o>")
	if h.a.cur.Buf == b {
		t.Fatal("<C-o> goes back from the artifact")
	}
	h.a.openArtifact("design")
	h.a.draw()
	h.see("one!")
	h.see("design [+]")
	h.press("u")
	if b.Edit.Text() != "# D\n\none\n" || b.Edit.Dirty {
		t.Fatalf("u goes back to the saved text: %q", b.Edit.Text())
	}
	h.press("<C-r>", ":q!", "<CR>")
	if b.Edit.Dirty || b.Edit.Text() != "# D\n\none\n" || h.hub.docs["design"] != "# D\n\none\n" {
		t.Fatalf(":q! should drop the change: %q", b.Edit.Text())
	}
}

func TestArtifactEditNormalCommandsAndUndo(t *testing.T) {
	h := editDemo(t, "# D\n\none\ntwo\n")
	h.press(":e", "<CR>", "gg")
	ed := h.a.cur.Buf.Edit
	h.press("j", "j", "dd", "p")
	if got := ed.Text(); got != "# D\n\ntwo\none\n" {
		t.Fatalf("dd p = %q", got)
	}
	h.press("x", "o", "new", "<Esc>")
	if got := ed.Text(); got != "# D\n\ntwo\nne\nnew\n" {
		t.Fatalf("x o = %q", got)
	}
	h.press("u")
	if got := ed.Text(); got != "# D\n\ntwo\nne\n" {
		t.Fatalf("u = %q", got)
	}
	h.press("u", "u", "u")
	if got := ed.Text(); got != "# D\n\none\ntwo\n" || ed.Dirty {
		t.Fatalf("u back to the start = %q", got)
	}
	h.press("<C-r>")
	if got := ed.Text(); got != "# D\n\ntwo\n" {
		t.Fatalf("<C-r> = %q", got)
	}
	h.press(":q!", "<CR>")
	if h.a.cur.Buf.Edit != nil || h.hub.docs["design"] != "# D\n\none\ntwo\n" {
		t.Fatal(":q! should drop the changes and show the document")
	}
}

func TestArtifactEditWrapsLongLines(t *testing.T) {
	long := strings.Repeat("word ", 40)
	h := editDemo(t, "# D\n\n"+long+"end\n")
	h.press(":e", "<CR>", "G")
	w := h.a.cur
	if lines := w.Buf.Lines(h.a.textWidth(w), h.a.th, 0); len(lines) < 5 {
		t.Fatalf("a long line should wrap; %d rows", len(lines))
	}
	h.press("k", "$", "x")
	if got := w.Buf.Edit.Text(); !strings.HasSuffix(got, "word en\n") {
		t.Fatalf("x at the end of a wrapped line = %q", got[len(got)-12:])
	}
	h.press("k", "0", "i", "X", "<Esc>")
	l := w.Buf.Edit.Src[2]
	if strings.Count(string(l), "X") != 1 || l[0] == 'X' {
		t.Fatalf("i on a wrapped row should write in that row, got %q", string(l))
	}
}

func TestArtifactEditRefusesToSaveOverANewerVersion(t *testing.T) {
	h := editDemo(t, "# D\n\nmine\n")
	h.press(":e", "<CR>", "G", "k", "A", "!", "<Esc>")
	h.hub.arts[0].Version = 2
	h.hub.docs["design"] = "# D\n\ntheirs\n"
	a := h.hub.arts[0]
	h.a.apply(model.Update{Kind: model.UpdateArtifact, Workspace: "demo", Thread: "auth-refactor", Artifact: &a})
	h.a.draw()
	h.see("auth-refactor wrote v2 while you edit")
	h.press(":w", "<CR>")
	if h.hub.docs["design"] != "# D\n\ntheirs\n" {
		t.Fatal(":w should not save over a newer version")
	}
	h.press(":w!", "<CR>")
	if h.hub.docs["design"] != "# D\n\nmine!\n" || h.hub.arts[0].Version != 3 {
		t.Fatalf(":w! = %q v%d", h.hub.docs["design"], h.hub.arts[0].Version)
	}
}

func TestAConflictOpensADiff(t *testing.T) {
	h := editDemo(t, "# D\n\nkeep\nmine\nend\n")
	e := h.a.cur.Buf.Edit
	h.press("/mine", "<CR>", "A", "!", "<Esc>")
	h.hub.arts[0].Version = 2
	h.hub.docs["design"] = "# D\n\nkeep\ntheirs\nend\nadded\n"
	a := h.hub.arts[0]
	h.a.apply(model.Update{Kind: model.UpdateArtifact, Workspace: "demo", Thread: "auth-refactor", Artifact: &a})
	mine := h.a.cur
	h.press(":w", "<CR>")
	if e.diff == nil || h.a.cur != mine || h.a.root.find(e.diff.win) == nil || e.diff.win.Buf.Kind != KindDiff {
		t.Fatal("a refused :w should open the newer version beside the source")
	}
	h.a.draw()
	h.see("◇ v2 · by auth-refactor")
	h.see("theirs")
	h.see("mine!")
	h.press("gg", "]c", "do")
	if got := e.Text(); got != "# D\n\nkeep\ntheirs\nend\n" {
		t.Fatalf("do should take the change: %q", got)
	}
	h.press("]c")
	if l := h.a.line(mine); l.Off >= 0 || e.diff.win.Cur != mine.Cur {
		t.Fatalf("]c should go to the added line's filler row, beside it: %+v", l)
	}
	h.press("do")
	if got := e.Text(); got != h.hub.docs["design"] {
		t.Fatalf("do on an added line should add it: %q", got)
	}
	h.press("gg", "A", " v3", "<Esc>", ":w", "<CR>")
	if h.hub.docs["design"] != "# D v3\n\nkeep\ntheirs\nend\nadded\n" || h.hub.arts[0].Version != 3 {
		t.Fatalf(":w in the diff should save over the newer version: %q v%d", h.hub.docs["design"], h.hub.arts[0].Version)
	}
	if e.diff != nil || len(h.a.root.windows()) != 2 {
		t.Fatal(":w should end the diff")
	}

	h.press("A", "!", "<Esc>")
	h.hub.arts[0].Version = 4
	h.hub.docs["design"] = "# D v4\n"
	a = h.hub.arts[0]
	h.a.apply(model.Update{Kind: model.UpdateArtifact, Workspace: "demo", Thread: "auth-refactor", Artifact: &a})
	h.press(":w", "<CR>", "<C-w>", "l", ":q", "<CR>")
	if e.diff != nil || h.a.cur.Buf.Edit != e || len(h.a.root.windows()) != 2 {
		t.Fatal(":q in the newer version's window should end the diff")
	}
}

func TestANewVersionReloadsAnUnchangedBuffer(t *testing.T) {
	h := editDemo(t, "# D\n\nold\n")
	h.hub.arts[0].Version = 2
	h.hub.docs["design"] = "# D\n\nnew\n"
	a := h.hub.arts[0]
	h.a.apply(model.Update{Kind: model.UpdateArtifact, Workspace: "demo", Thread: "auth-refactor", Artifact: &a})
	e := h.a.cur.Buf.Edit
	if e.Text() != "# D\n\nnew\n" || e.Base != 2 || e.Dirty {
		t.Fatalf("an unchanged buffer should show the new version: %q v%d", e.Text(), e.Base)
	}
	h.a.draw()
	h.see("◇ design · by auth-refactor · v2")
}

// srcEdit opens the source of a one-line document on that line.
func srcEdit(t *testing.T, line string) (*neoharness, *Editor) {
	h := editDemo(t, "# D\n\n"+line+"\n")
	h.press(":e", "<CR>", "/"+strings.Fields(line)[0], "<CR>", "0")
	return h, h.a.cur.Buf.Edit
}

func TestArtifactEditOperators(t *testing.T) {
	for _, c := range []struct {
		line string
		keys []string
		want string
	}{
		{"alpha beta gamma", []string{"dw"}, "beta gamma"},
		{"alpha beta gamma", []string{"2dw"}, "gamma"},
		{"alpha beta gamma", []string{"cw", "one", "<Esc>"}, "one beta gamma"},
		{"alpha beta gamma", []string{"w", "de"}, "alpha  gamma"},
		{"alpha beta gamma", []string{"$", "db"}, "alpha beta a"},
		{"alpha beta gamma", []string{"w", "d$"}, "alpha "},
		{"alpha beta gamma", []string{"w", "D"}, "alpha "},
		{"alpha beta gamma", []string{"w", "d0"}, "beta gamma"},
		{"alpha beta gamma", []string{"w", "l", "diw"}, "alpha  gamma"},
		{"alpha beta gamma", []string{"w", "l", "daw"}, "alpha gamma"},
		{"alpha beta gamma", []string{"w", "ciw", "BETA", "<Esc>"}, "alpha BETA gamma"},
		{"see **bold** here", []string{"w", "w", "ciw", "x", "<Esc>"}, "see **x** here"},
		{"alpha beta gamma", []string{"w", "C", "end", "<Esc>"}, "alpha end"},
		{"alpha beta gamma", []string{"s", "A", "<Esc>"}, "Alpha beta gamma"},
		{"alpha beta gamma", []string{"cc", "new", "<Esc>"}, "new"},
		{"alpha beta gamma", []string{"3r", "x"}, "xxxha beta gamma"},
		{"alpha beta gamma", []string{"yiw", "$", "p"}, "alpha beta gammaalpha"},
		{"alpha beta gamma", []string{"x", "."}, "pha beta gamma"},
		{"alpha beta gamma", []string{"dw", "."}, "gamma"},
		{"alpha beta gamma", []string{"cw", "one", "<Esc>", "w", "."}, "one one gamma"},
		{"alpha beta gamma", []string{"v", "e", "d"}, " beta gamma"},
		{"alpha beta gamma", []string{"w", "v", "e", "c", "B", "<Esc>"}, "alpha B gamma"},
		{"see [docs](https://old.dev) now", []string{"f", "h", "ci(", "https://new.dev", "<Esc>"}, "see [docs](https://new.dev) now"},
		{"see [docs](https://old.dev) now", []string{"w", "w", "di["}, "see [](https://old.dev) now"},
		{"say \"hi there\" ok", []string{"ci\"", "yo", "<Esc>"}, "say \"yo\" ok"},
		{"say \"hi there\" ok", []string{"$", "da\""}, "say \"hi there\" ok"},
		{"f(a, g(b)) end", []string{"f", "b", "dib"}, "f(a, g()) end"},
		{"f(a, g(b)) end", []string{"l", "l", "da("}, "f end"},
		{"use `code` here", []string{"w", "w", "yi`", "$", "p"}, "use `code` herecode"},
		{"a, b, c, d", []string{"df,"}, " b, c, d"},
		{"a, b, c, d", []string{"2df,"}, " c, d"},
		{"a, b, c, d", []string{"dt,"}, ", b, c, d"},
		{"a, b, c, d", []string{"$", "dF,"}, "a, b, cd"},
		{"a, b, c, d", []string{"$", "dT,"}, "a, b, c,d"},
		{"f(x) end", []string{"ct)", "y", "<Esc>"}, "y) end"},
		{"a, b, c, d", []string{"f,", ";", "x"}, "a, b c, d"},
		{"a, b, c, d", []string{"f,", ";", ",", "x"}, "a b, c, d"},
		{"a, b, c, d", []string{"t,", ";", "x"}, "a, , c, d"},
		{"a, b, c, d", []string{"df,", "."}, " c, d"},
		{"f(a, g(b)) end", []string{"f", "(", "d%"}, "f end"},
		{"f(a, g(b)) end", []string{"d%"}, " end"},
		{"f(a, g(b)) end", []string{"f", "(", "%", "x"}, "f(a, g(b) end"},
		{"f(a, g(b)) end", []string{"f", "(", "%", "%", "x"}, "fa, g(b)) end"},
		{"alpha beta gamma", []string{"w", "v", "i", "w", "d"}, "alpha  gamma"},
		{"see [docs](https://x.dev) now", []string{"f", "x", "v", "i", "(", "c", "y", "<Esc>"}, "see [docs](y) now"},
		{"say \"hi there\" ok", []string{"f", "h", "v", "a", "\"", "d"}, "say  ok"},
	} {
		h, ed := srcEdit(t, c.line)
		h.press(c.keys...)
		if got := string(ed.Src[2]); got != c.want {
			t.Errorf("%q %v = %q, want %q", c.line, c.keys, got, c.want)
		}
	}
}

func TestArtifactEditLinesAndYank(t *testing.T) {
	h := editDemo(t, "# D\n\none\ntwo\nthree\n")
	h.press(":e", "<CR>", "/one", "<CR>")
	ed := h.a.cur.Buf.Edit
	h.press("yy", "j", "p")
	if got := ed.Text(); got != "# D\n\none\ntwo\none\nthree\n" {
		t.Fatalf("yy j p = %q", got)
	}
	h.press("k", "J")
	if got := ed.Text(); got != "# D\n\none\ntwo one\nthree\n" {
		t.Fatalf("J = %q", got)
	}
	h.press("V", "j", "d")
	if got := ed.Text(); got != "# D\n\none\n" {
		t.Fatalf("V j d = %q", got)
	}
	h.press("P")
	if got := ed.Text(); got != "# D\n\none\ntwo one\nthree\n" {
		t.Fatalf("P after V d = %q", got)
	}
	h.press("gg")
	if h.a.cur.Cur < metaRows {
		t.Fatal("gg should stop on the first source line, not the meta line")
	}
	h.a.draw()
	h.see("◇ design · by auth-refactor · v1")
}

func TestEditedArtifactShowsInTheWritersChat(t *testing.T) {
	h := start(t)
	h.openDemo()
	e := model.Event{Kind: model.EventArtifact, From: model.User, Detail: "design", Text: "Design", Version: 2}
	h.a.apply(model.Update{Kind: model.UpdateEvent, Workspace: "demo", Thread: model.Coordinator, Event: &e})
	h.a.draw()
	h.see("◇ Design  · artifact v2 · edited by you")
}

func TestArtifactEditMergesANewerVersion(t *testing.T) {
	h := editDemo(t, "# D\n\n## A\none\n")
	h.press(":e", "<CR>", "/one", "<CR>", "A", "!", "<Esc>")
	ed := h.a.cur.Buf.Edit
	h.hub.arts[0].Version = 2
	h.hub.docs["design"] = "# D\n\n## Aye\none\n"
	a := h.hub.arts[0]
	h.a.apply(model.Update{Kind: model.UpdateArtifact, Workspace: "demo", Thread: "auth-refactor", Artifact: &a})
	h.a.draw()
	h.see("auth-refactor wrote v2 while you edit · :w merges it")
	h.hub.merged = "# D\n\n## Aye\none!\n"
	h.press(":w", "<CR>")
	if ed.Text() != h.hub.merged || ed.Dirty || ed.Base != 3 {
		t.Fatalf("after the merge: %q dirty %v base %d", ed.Text(), ed.Dirty, ed.Base)
	}
	h.see("merged and saved design v3")
	h.press("u")
	if ed.Text() != "# D\n\n## A\none!\n" {
		t.Fatalf("u after a merge should give back the edit alone: %q", ed.Text())
	}
}

func TestHeadingLevel(t *testing.T) {
	for s, want := range map[string]int{"# A": 1, "### A": 3, "#": 1, "#tag": 0, "####### A": 0, "text": 0, "#\tA": 1} {
		if got := headingLevel(s); got != want {
			t.Errorf("headingLevel(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestRepeatOnTheDocument(t *testing.T) {
	h := editDemo(t, "# D\n\none\n\ntwo\n")
	h.press("/one", "<CR>", "A", "!", "<Esc>", "/two", "<CR>", ".")
	if got := h.a.cur.Buf.Edit.Text(); got != "# D\n\none!\n\ntwo!\n" {
		t.Fatalf(". on the document should type the change again: %q", got)
	}
}

func TestEnterContinuesAList(t *testing.T) {
	for _, c := range []struct{ line, typed, want string }{
		{"- one", "<CR>two", "- one\n- two"},
		{"  * one", "<CR>two", "  * one\n  * two"},
		{"3. one", "<CR>two", "3. one\n4. two"},
		{"- [x] done", "<CR>next", "- [x] done\n- [ ] next"},
		{"- one", "<CR><CR>after", "- one\nafter"},
		{"plain", "<CR>x", "plain\nx"},
		{"- - -", "<CR>x", "- - -\nx"},
		{"* * *", "<CR>x", "* * *\nx"},
	} {
		h, ed := srcEdit(t, c.line)
		h.press("A")
		for _, part := range strings.SplitAfter(c.typed, "<CR>") {
			text, cr := strings.CutSuffix(part, "<CR>")
			if text != "" {
				h.press(text)
			}
			if cr {
				h.press("<CR>")
			}
		}
		if got := strings.TrimSuffix(strings.TrimPrefix(ed.Text(), "# D\n\n"), "\n"); got != c.want {
			t.Errorf("%q + %q = %q, want %q", c.line, c.typed, got, c.want)
		}
	}
}

func TestSearchFindsTextAcrossAWrap(t *testing.T) {
	for n := 1; n < 160; n += 3 {
		h := editDemo(t, "# D\n\n"+strings.Repeat("x", n)+" Pick the store\n")
		w := h.a.cur
		split := false
		for _, l := range w.Buf.Lines(h.a.textWidth(w), h.a.th, 0) {
			split = split || strings.HasSuffix(l.Text(), "Pick") || strings.HasSuffix(l.Text(), "the")
		}
		if !split {
			continue
		}
		h.press("gg", "/Pick the store", "<CR>")
		if strings.Contains(h.a.notice, "not found") || !strings.Contains(h.a.line(w).Text(), "Pick") {
			t.Fatalf("search should match across a wrapped row: %q", h.a.notice)
		}
		return
	}
	t.Fatal("no filler length split the words")
}

func TestGxOpensTheLinkUnderTheCursor(t *testing.T) {
	h := editDemo(t, "# D\n\n| a | b |\n|---|---|\n| [one](https://one.dev) | [two](https://two.dev) |\n")
	var opened []string
	h.a.openURL = func(_, url string) error { opened = append(opened, url); return nil }
	h.press("/two", "<CR>", "gx", "/one", "<CR>", "gx", "gg", "gx")
	if strings.Join(opened, " ") != "https://two.dev https://one.dev" {
		t.Fatalf("gx opened %q", opened)
	}
	h.see("no link under the cursor")
}

func TestACutRowEndsWithAnEllipsis(t *testing.T) {
	h := start(t)
	h.openDemo()
	v := h.a.threads["auth-refactor"]
	v.Progress = strings.Repeat("long progress ", 20) + "END"
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.a.draw()
	for _, l := range strings.Split(h.screen(), "\n") {
		if strings.Contains(l, "auth-refactor") && strings.Contains(l, "long progress") {
			if strings.Contains(l, "END") || !strings.HasSuffix(strings.TrimRight(l, " "), "…") {
				t.Fatalf("a cut row should end with …: %q", l)
			}
			return
		}
	}
	t.Fatalf("no threads row:\n%s", h.screen())
}

func TestPlainDropsMarkdownMarks(t *testing.T) {
	for in, want := range map[string]string{
		"Done: the `notes` artifact is **written**.": "Done: the notes artifact is written.",
		"see [docs](https://x.dev)\nnext line":       "see docs next line",
		"- one\n- two":                               "• one • two",
	} {
		if got := plain(in); got != want {
			t.Errorf("plain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGxInAChat(t *testing.T) {
	h := start(t)
	h.openDemo()
	e := model.Event{Kind: model.EventText, Text: "See [the guide](https://guide.dev) and [other](https://other.dev)."}
	h.a.apply(model.Update{Kind: model.UpdateEvent, Workspace: "demo", Thread: model.Coordinator, Event: &e})
	var opened string
	h.a.openURL = func(_, url string) error { opened = url; return nil }
	h.press("<Esc>", "/other", "<CR>", "gx")
	if opened != "https://other.dev" {
		t.Fatalf("gx in a chat opened %q", opened)
	}
}

func TestJSONTextReadsAsKeysAndValues(t *testing.T) {
	src := `{"ok": false, "code": "usage", "reason": "Usage:\n  /setup --wizard\n  /setup --apply", "n": 3, "list": ["a", {"b": 1}]}`
	lines, ok := renderJSON(src, 60, newTheme(), nil)
	if !ok {
		t.Fatal("a JSON object should render as JSON")
	}
	want := "ok: false\ncode: usage\nreason:\n  Usage:\n    /setup --wizard\n    /setup --apply\nn: 3\nlist:\n  - a\n  -\n    b: 1"
	var got []string
	for _, l := range lines {
		got = append(got, strings.TrimRight(spansText(l.Prefix)+l.Text(), " "))
	}
	if got := strings.Join(got, "\n"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if _, ok := renderJSON("{not json", 60, newTheme(), nil); ok {
		t.Fatal("text that is not JSON should stay markdown")
	}
}

func TestMarks(t *testing.T) {
	h := editDemo(t, "# D\n\none\n\n"+strings.Repeat("filler\n\n", 20)+"two words\n")
	w := h.a.cur
	h.press("/one", "<CR>", "ma")
	one := w.Cur
	h.press("/words", "<CR>")
	col := w.Col
	h.press("mb", "'a")
	if w.Cur != one {
		t.Fatalf("'a should go to the line of mark a, at %d not %d", w.Cur, one)
	}
	h.press("`b")
	if w.Col != col {
		t.Fatalf("`b should go to the column of mark b, %d not %d", w.Col, col)
	}
	h.press("''")
	if w.Cur != one {
		t.Fatal("'' should go back to where the jump started")
	}
	h.press("'z")
	h.see("mark not set")
}

func TestParagraphObjects(t *testing.T) {
	for _, c := range []struct {
		keys []string
		want string
	}{
		{[]string{"dap"}, "# D\n\nthree\n"},
		{[]string{"dip"}, "# D\n\n\nthree\n"},
		{[]string{"cip", "new", "<Esc>"}, "# D\n\nnew\n\nthree\n"},
		{[]string{"yap", "G", "p"}, "# D\n\none\ntwo\n\nthree\n\none\ntwo\n"},
		{[]string{"V", "<Esc>", "v", "i", "p", "d"}, "# D\n\n\nthree\n"},
	} {
		h, ed := srcEdit(t, "one\ntwo\n\nthree")
		h.press(c.keys...)
		if got := ed.Text(); got != c.want {
			t.Errorf("%v = %q, want %q", c.keys, got, c.want)
		}
	}
}

func TestFastEscapeThenKeyIsTwoKeys(t *testing.T) {
	h := editDemo(t, "# D\n\none\n")
	h.press(":e", "<CR>", "G", "k", "A", "!")
	h.a.key(tcell.NewEventKey(tcell.KeyRune, ':', tcell.ModAlt))
	h.press("w", "<CR>")
	if h.hub.docs["design"] != "# D\n\none!\n" {
		t.Fatalf("<Esc>:w typed fast should save, saved %q", h.hub.docs["design"])
	}
	if h.a.mode != modeNormal {
		t.Fatal("<Esc> typed fast should leave Insert")
	}
}

func TestFastEscapeInNormalKeepsTheKey(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.a.key(tcell.NewEventKey(tcell.KeyRune, ':', tcell.ModAlt))
	if h.a.mode != modeCommand {
		t.Fatal("<Esc>: typed fast in Normal should start a command, as in Vim")
	}
}

func TestYankMotionsWithoutASource(t *testing.T) {
	h := editDemo(t, "# D\n\nKeep (the cache) warm, said Ann.\n\nNext paragraph.\n")
	clip := func() string { return string(h.s.GetClipboardData()) }
	h.press("/cache", "<CR>", "yiw")
	if clip() != "cache" {
		t.Fatalf("yiw on the document = %q", clip())
	}
	h.press("yi(")
	if clip() != "the cache" {
		t.Fatalf("yi( on the document = %q", clip())
	}
	// yi( leaves the cursor at the start of what it took, as in Vim.
	h.press("yt,")
	if clip() != "the cache) warm" {
		t.Fatalf("yt, on the document = %q", clip())
	}
	h.press("yip")
	if !strings.Contains(clip(), "Keep (the cache) warm, said Ann.") || strings.Contains(clip(), "Next") {
		t.Fatalf("yip on the document = %q", clip())
	}
}

func TestBracketObjectsOverLines(t *testing.T) {
	for _, c := range []struct {
		keys []string
		want string
	}{
		{[]string{"di{"}, "f {\n}\n"},
		{[]string{"da{"}, "f \n"},
		{[]string{"ci{", "x", "<Esc>"}, "f {\nx\n}\n"},
		{[]string{"yi{", "G", "p"}, "f {\n  a\n  b\n}\n\n  a\n  b"},
		// A find that fails on the line is not a bracket object.
		{[]string{"dt}"}, "f {\n  a\n  b\n}\n"},
		{[]string{"df}"}, "f {\n  a\n  b\n}\n"},
	} {
		h := editDemo(t, "f {\n  a\n  b\n}\n")
		h.press(":e", "<CR>", "/a", "<CR>")
		ed := h.a.cur.Buf.Edit
		h.press(c.keys...)
		if got := ed.Text(); got != c.want {
			t.Errorf("%v = %q, want %q", c.keys, got, c.want)
		}
	}
}

func TestHelpOpensAtTheTop(t *testing.T) {
	h := start(t)
	h.openDemo()
	coord := h.a.cur
	h.press(":help", "<CR>")
	h.a.draw()
	h.see("neoharness  keys")
	if h.a.cur == coord || coord.Buf.Thread != model.Coordinator {
		t.Fatal(":help should open in a split, not over the coordinator")
	}
	h.press(":help", "<CR>")
	if len(h.a.root.windows()) != 3 {
		t.Fatalf(":help again should focus the help window, %d windows", len(h.a.root.windows()))
	}
	h.press(":q", "<CR>")
	if h.a.cur.Buf.Kind == KindHelp || len(h.a.root.windows()) != 2 || h.a.root.find(coord) == nil {
		t.Fatal(":q in help should close only the help window")
	}
}

func TestEmptyMessagesSaySo(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press(":messages", "<CR>")
	h.a.draw()
	h.see("no messages yet")
}

func TestToolDetailsUseShortMCPNames(t *testing.T) {
	if got := shortTools("select:mcp__neoharness__workspace_status,mcp__slack__read"); got != "select:neoharness·workspace_status,slack·read" {
		t.Fatalf("shortTools = %q", got)
	}
}

func TestTranscriptSearchShowsTheMatch(t *testing.T) {
	h := start(t)
	h.openDemo()
	e := model.Event{Kind: model.EventText, Text: "Some opening words that come first, then the needle word."}
	h.a.apply(model.Update{Kind: model.UpdateEvent, Workspace: "demo", Thread: model.Coordinator, Event: &e})
	h.press("<Esc>", "<Space>", "/", "needle")
	items := h.a.picker.items
	if len(items) != 1 || !strings.HasPrefix(items[0].detail, "…") || !strings.Contains(items[0].detail, "needle") || strings.Contains(items[0].detail, "Some") {
		t.Fatalf("items = %+v", items)
	}
	if items[0].glyph.Text == "›" {
		t.Fatal("a result should show its thread's state, not the prompt glyph")
	}
}

func TestFailedToolCallsShowOnTheFold(t *testing.T) {
	h := start(t)
	h.openDemo()
	for _, e := range []model.Event{
		{Kind: model.EventTool, Tool: "Bash", Detail: "git push --force", ID: "tu1"},
		{Kind: model.EventTool, Tool: "Read", Detail: "a.go", ID: "tu2"},
		{Kind: model.EventTool, ID: "tu1", Exit: 1, Text: "Permission to use Bash was denied."},
		{Kind: model.EventTurnEnd},
	} {
		h.a.apply(model.Update{Kind: model.UpdateEvent, Workspace: "demo", Thread: model.Coordinator, Event: &e})
	}
	h.a.draw()
	h.see("2 tool calls · 1 failed")
	if got := h.fgAt("· 1 failed"); got != rpLove {
		t.Fatalf("the failed mark should be red, got %v", got)
	}
	h.press("<Esc>", "/2 tool calls", "<CR>", "za")
	h.a.draw()
	h.see("✗ Permission to use Bash was denied.")
}

func TestLineBreaksAndImages(t *testing.T) {
	th := newTheme()
	src := "one<br>two<BR/>three\n\n| a | b |\n|---|---|\n| x<br>y | z |\n\n![diagram](https://x.dev/a.png)\n"
	lines := docRows(model.Artifact{Name: "d"}, src, 80, th)
	all := strings.Join(texts(lines), "\n")
	for _, want := range []string{"one\n", "two\n", "three", "x", "y", "image diagram"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(strings.ToLower(all), "<br") {
		t.Errorf("<br> should break the line:\n%s", all)
	}
	for _, l := range texts(lines) {
		if strings.Contains(l, "│ z") && (!strings.Contains(l, "x") || strings.Contains(l, "y")) {
			t.Errorf("<br> in a cell should put y on the next row: %q", l)
		}
	}
	for _, l := range lines {
		for _, sp := range l.Spans {
			if sp.Text == "diagram" && sp.Style != linkStyle(th, "https://x.dev/a.png") {
				t.Error("an image should link to its file")
			}
		}
	}
}

func TestQuitGuardsTheCoordinatorAndPermissionWaits(t *testing.T) {
	h := start(t)
	h.openDemo()
	setState := func(name string, st model.State, perm *model.Permission) {
		v := h.a.threads[name]
		v.State, v.Permission = st, perm
		h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: name, View: &v})
	}
	for name := range h.a.threads {
		setState(name, model.StateIdle, nil)
	}
	h.hub.workspaces[0].Counts = nil
	h.a.loadWorkspaces()
	setState(model.Coordinator, model.StateRunning, nil)
	h.press(":qa", "<CR>")
	if h.a.quit {
		t.Fatal(":qa should not stop the coordinator's turn")
	}
	setState(model.Coordinator, model.StateIdle, nil)
	setState("auth-refactor", model.StateWaiting, &model.Permission{Tool: "Bash"})
	h.press(":qa", "<CR>")
	if h.a.quit {
		t.Fatal(":qa should not stop a turn that waits on a permission")
	}
	h.see("1 running · :qa! stops them")
	setState("auth-refactor", model.StateIdle, nil)
	h.press(":qa", "<CR>")
	if !h.a.quit {
		t.Fatal(":qa should quit when no turn is in flight")
	}
}

func TestThreadsListShowsHowLongAThreadRuns(t *testing.T) {
	h := start(t)
	h.openDemo()
	v := h.a.threads["auth-refactor"]
	v.State, v.Since, v.Progress = model.StateRunning, time.Now().Add(-95*time.Second), "reading files"
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "demo", Thread: v.Name, View: &v})
	h.a.draw()
	h.see("1m35s · reading files")
}

func TestWorkspaceKeepsItsSplits(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press("<C-w>", "v")
	n := len(h.a.root.windows())
	h.press("<Space>", "p", "v")
	if len(h.a.root.windows()) != 1 {
		t.Fatal("<leader>pv should show the workspaces screen")
	}
	if l := h.a.line(h.a.cur); l.Item != "demo" {
		t.Fatalf("the workspaces screen should start on the workspace you left, on %q", l.Item)
	}
	h.openDemo()
	if got := len(h.a.root.windows()); got != n {
		t.Fatalf("opening the workspace again should keep its %d windows, got %d", n, got)
	}
	h.a.draw()
}

func TestPlainWordArtifactsLinkOnlyInCode(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.a.artifacts = []model.Artifact{{Name: "plan"}, {Name: "doc-notes"}}
	link := h.a.th.Foam.Underline(true)
	linked := func(spans []Span) []string {
		var out []string
		for _, sp := range h.a.linkify(spans) {
			if sp.Style == link {
				out = append(out, sp.Text)
			}
		}
		return out
	}
	prose := []Span{{"the plan is in doc-notes", h.a.th.Text}}
	if got := linked(prose); strings.Join(got, ",") != "doc-notes" {
		t.Fatalf("prose links = %q, want only doc-notes", got)
	}
	code := []Span{{"see ", h.a.th.Text}, {"plan", h.a.th.Code}}
	if got := linked(code); strings.Join(got, ",") != "plan" {
		t.Fatalf("code links = %q, want plan", got)
	}
	if name := h.a.artifactAt(Line{Spans: prose}, 5); name != "doc-notes" {
		t.Fatalf("<CR> on the word plan in prose opened %q", name)
	}
}

func TestPopupMarginsAreBlank(t *testing.T) {
	h := editDemo(t, "# D\n\ntext\n")
	h.a.draw()
	W, H := h.s.Size()
	cells, _, _ := h.s.GetContents()
	for y := 2; y < H-2; y++ {
		for _, x := range []int{0, 1, W - 2, W - 1} {
			if r := cells[y*W+x].Runes; len(r) > 0 && r[0] != ' ' {
				t.Fatalf("margin cell (%d,%d) = %q, want blank", x, y, string(r))
			}
		}
	}
}
