package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/internal/model"
)

// fakeClient is a hub that records what the UI asked of it.
type fakeClient struct {
	projects []model.ProjectView
	threads  map[string][]model.ThreadView
	events   map[string][]model.Event
	sent     []string
	answers  []string
	opened   []string
	resolved []string
}

func (f *fakeClient) Projects() ([]model.ProjectView, error) { return f.projects, nil }
func (f *fakeClient) CreateProject(name, pin string) error {
	f.projects = append(f.projects, model.ProjectView{Name: name, Pin: pin})
	f.threads[name] = []model.ThreadView{{Project: name, Name: model.Coordinator, Role: model.RoleCoordinator, State: model.StateIdle}}
	return nil
}
func (f *fakeClient) Threads(p string) ([]model.ThreadView, error) { return f.threads[p], nil }
func (f *fakeClient) OpenThread(p, name, brief, loc string) error {
	f.opened = append(f.opened, name+"@"+loc)
	return nil
}
func (f *fakeClient) Events(p, t string, tail int) ([]model.Event, error) { return f.events[t], nil }
func (f *fakeClient) Send(p, t, text string) error {
	f.sent = append(f.sent, t+": "+text)
	return nil
}
func (f *fakeClient) Resolve(p, t string) error {
	f.resolved = append(f.resolved, t)
	return nil
}
func (f *fakeClient) Reopen(p, t string) error { return nil }
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
		projects: []model.ProjectView{{Name: "demo", Pin: "/src/demo", Counts: map[model.State]int{model.StateRunning: 1}, Activity: []int{0, 1, 3}}},
		threads: map[string][]model.ThreadView{"demo": {
			{Project: "demo", Name: model.Coordinator, Role: model.RoleCoordinator, State: model.StateIdle, CreatedAt: now},
			{Project: "demo", Name: "auth-refactor", Role: model.RoleThread, State: model.StateRunning, Progress: "moving store", CreatedAt: now, Since: now},
			{Project: "demo", Name: "api-tests", Role: model.RoleThread, State: model.StateIdle, CreatedAt: now},
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
	// The cursor starts on the first project row.
	h.press("<CR>")
	if h.a.project != "demo" {
		h.t.Fatalf("project = %q, want demo", h.a.project)
	}
}

func TestProjectsScreenAndOpen(t *testing.T) {
	h := start(t)
	h.see("PROJECTS")
	h.see("demo")
	h.see("/src/demo")
	h.see("● 1")
	h.openDemo()
	if wins := h.a.root.windows(); len(wins) != 1 || h.a.cur.Buf.Thread != model.Coordinator {
		t.Fatalf("a project should open as the coordinator alone; got %d windows", len(wins))
	}
	h.see("split the session store out")
	h.see("Opened auth-refactor and api-tests.")
	// The bottom line counts what runs: auth-refactor.
	h.see("● 1")
}

func TestNewProjectStartsReadyToWrite(t *testing.T) {
	h := start(t)
	h.press(":new solo /tmp", "<CR>")
	if n := len(h.a.root.windows()); n != 1 || h.a.cur.Buf.Thread != model.Coordinator {
		t.Fatalf("a new project should be the coordinator alone; got %d windows", n)
	}
	if h.a.mode != modeInsert {
		t.Fatal("a new project should start ready to write")
	}
	h.press("<Esc>")
	v := model.ThreadView{Project: "solo", Name: "first", Role: model.RoleThread, State: model.StateRunning}
	h.a.apply(model.Update{Kind: model.UpdateThread, Project: "solo", Thread: v.Name, View: &v})
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

func TestThreadsListSwitchesOrSplits(t *testing.T) {
	h := start(t)
	h.openDemo()
	// Rows: coordinator, auth-refactor, api-tests.
	h.press(":threads", "<CR>", "j", "j", "<CR>")
	if len(h.a.root.windows()) != 1 || h.a.cur.Buf.Thread != "api-tests" {
		t.Fatalf("<CR> should switch this window to api-tests; got %q", h.a.cur.Buf.Name)
	}
	h.press(":threads", "<CR>", "j", "<C-v>")
	wins := h.a.root.windows()
	if len(wins) != 2 || wins[0] != h.a.cur || h.a.cur.Buf.Thread != "auth-refactor" || wins[1].Buf.Kind != KindThreads {
		t.Fatalf("<C-v> should open auth-refactor in a split on the left; got %d windows", len(wins))
	}
}

func TestPickerOpensInPlaceOrSplit(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press("<Space>", "t", "api", "<C-x>")
	wins := h.a.root.windows()
	if len(wins) != 2 || wins[0] != h.a.cur || h.a.cur.Buf.Thread != "api-tests" || wins[1].Buf.Thread != model.Coordinator {
		t.Fatalf("<C-x> in the picker should split api-tests above the coordinator; got %d windows", len(wins))
	}
	h.press("<Space>", "t", "auth", "<CR>")
	if len(h.a.root.windows()) != 2 || h.a.cur != wins[0] || h.a.cur.Buf.Thread != "auth-refactor" {
		t.Fatal("<CR> in the picker should switch the current window")
	}
}

func TestEventsAppendLiveAndFold(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.press(":b auth-refactor", "<CR>")
	h.a.apply(model.Update{Kind: model.UpdateEvent, Project: "demo", Thread: "auth-refactor",
		Event: &model.Event{Kind: model.EventTool, Tool: "Edit", Detail: "session.go"}})
	h.a.draw()
	h.see("Edit  session.go")
	h.see("3 tool calls")
	h.a.apply(model.Update{Kind: model.UpdateEvent, Project: "demo", Thread: "auth-refactor",
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
	h.a.apply(model.Update{Kind: model.UpdateThread, Project: "demo", Thread: v.Name, View: &v})
	h.a.draw()
	h.see("auth-refactor needs permission")
	h.see("◆ 1")

	// <leader>w goes to it and shows the card; <leader>y allows it.
	h.press("<Space>", "w")
	if h.a.cur.Buf.Thread != "auth-refactor" {
		t.Fatalf("<leader>w focused %q", h.a.cur.Buf.Thread)
	}
	h.see("permission")
	h.see("rm -rf ./tmp/cache")
	h.press("<Space>", "y")
	if len(h.hub.answers) != 1 || h.hub.answers[0] != "auth-refactor p1 allow " {
		t.Fatalf("answers = %q", h.hub.answers)
	}
	h.press(":deny not now", "<CR>")
	if h.hub.answers[1] != "auth-refactor p1 deny not now" {
		t.Fatalf("answers = %q", h.hub.answers)
	}
}

func TestNewProjectFolder(t *testing.T) {
	tmp := t.TempDir()
	for _, d := range []string{"alpha", "beta/inner", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(tmp)
	t.Setenv("HOME", tmp)
	h := start(t)
	last := func() model.ProjectView { return h.hub.projects[len(h.hub.projects)-1] }

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
		t.Fatalf("the first choice should create an unpinned project; got %+v", p)
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
	if len(wins) != 2 || wins[0] != h.a.cur || wins[1] != first {
		t.Fatalf("<C-w>v should open a focused window on the left; got %d windows", len(wins))
	}
	h.press("<C-w>", "l")
	if h.a.cur != first {
		t.Fatal("<C-w>l should move to the right window")
	}
	h.press("<C-w>", "h")
	if h.a.cur != wins[0] {
		t.Fatal("<C-w>h should move to the left window")
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
	h.press("<Space>", "t")
	if h.a.mode != modePicker {
		t.Fatal("<leader>t should open the threads picker")
	}
	h.see("threads")
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
	h.hub.projects[0].Counts = nil
	h.a.loadProjects()
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
