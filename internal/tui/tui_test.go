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
func (f *fakeClient) Threads(p string) ([]model.ThreadView, error) { return f.threads[p], nil }
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
	if wins := h.a.root.windows(); len(wins) != 1 || h.a.cur.Buf.Thread != model.Coordinator {
		t.Fatalf("a workspace should open as the coordinator alone; got %d windows", len(wins))
	}
	h.see("split the session store out")
	h.see("Opened auth-refactor and api-tests.")
	// The bottom line counts what runs: auth-refactor.
	h.see("● 1")
}

func TestNewWorkspaceStartsReadyToWrite(t *testing.T) {
	h := start(t)
	h.press(":new solo /tmp", "<CR>")
	if n := len(h.a.root.windows()); n != 1 || h.a.cur.Buf.Thread != model.Coordinator {
		t.Fatalf("a new workspace should be the coordinator alone; got %d windows", n)
	}
	if h.a.mode != modeInsert {
		t.Fatal("a new workspace should start ready to write")
	}
	h.press("<Esc>")
	v := model.ThreadView{Workspace: "solo", Name: "first", Role: model.RoleThread, State: model.StateRunning}
	h.a.apply(model.Update{Kind: model.UpdateThread, Workspace: "solo", Thread: v.Name, View: &v})
	if n := len(h.a.root.windows()); n != 1 || h.a.cur.Buf.Thread != model.Coordinator {
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
		t.Fatalf("<leader>t should open the list in a pane on the right; got %d windows", len(wins))
	}
	pane := wins[1]
	// Rows: running, auth-refactor, blank, idle, api-tests.
	h.press("G", "<CR>")
	if len(h.a.root.windows()) != 2 || h.a.cur != pane || pane.Buf.Thread != "api-tests" {
		t.Fatalf("<CR> should show api-tests in the pane; got %q", pane.Buf.Name)
	}
	h.press("<Space>", "t", "j", "<CR>")
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
	if h.a.cur != wins[0] || pane.Buf.Thread != "api-tests" {
		t.Fatal("<leader>pc should focus the coordinator and leave the pane")
	}
	h.press(":b auth-refactor", "<CR>", "<C-l>", "<Space>", "p", "c")
	if h.a.cur != wins[0] || wins[0].Buf.Thread != model.Coordinator || pane.Buf.Thread != "api-tests" {
		t.Fatalf("<leader>pc from the pane should show the coordinator on the left; cur %q", h.a.cur.Buf.Name)
	}
}

func TestPickerOpensInPlaceOrSplit(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press("<Space>", "<Space>", "api", "<C-x>")
	wins := h.a.root.windows()
	if len(wins) != 2 || wins[0] != h.a.cur || h.a.cur.Buf.Thread != "api-tests" || wins[1].Buf.Thread != model.Coordinator {
		t.Fatalf("<C-x> in a picker should split api-tests above the coordinator; got %d windows", len(wins))
	}
	h.press("<Space>", "<Space>", "auth", "<CR>")
	if len(h.a.root.windows()) != 2 || h.a.cur != wins[0] || h.a.cur.Buf.Thread != "auth-refactor" {
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
	if len(wins) != 2 || wins[1] != h.a.cur || wins[0] != first {
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
	if n := len(h.a.root.windows()); n != 1 {
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
	for _, l := range texts(renderMarkdown(src, 30, th, nil)) {
		if width(l) > 30 {
			t.Errorf("a wide table should shrink to the window, got %q (%d)", l, width(l))
		}
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
	h.see("demo deleted")
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
