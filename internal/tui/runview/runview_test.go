package runview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MahammadAgayev/harness/internal/agent"
	"github.com/MahammadAgayev/harness/internal/loop"
	"github.com/MahammadAgayev/harness/internal/notes"
	"github.com/MahammadAgayev/harness/internal/run"
)

func newModel(t *testing.T) *Model {
	t.Helper()
	st := &run.State{Name: "demo", Agent: "pi", Cwd: "/some/tree"}
	m := New(st, filepath.Join(t.TempDir(), "notes.md"))
	m.width, m.height = 100, 30
	return m
}

// send pushes a message through Update, the way the runtime would.
func send(m *Model, msg tea.Msg) *Model {
	out, _ := m.Update(msg)
	return out.(*Model)
}

func TestNotesAccumulateAndRender(t *testing.T) {
	m := newModel(t)
	m = send(m, NoteMsg{Note: notes.Note{At: time.Now(), Text: "auth test fails because the clock is mocked"}})
	m = send(m, NoteMsg{Note: notes.Note{At: time.Now(), Text: "moved the mock after token mint"}})

	view := m.View()
	// Notes are the main pane: both must be visible, not just the latest.
	if !strings.Contains(view, "auth test fails") {
		t.Error("earlier notes should stay on screen")
	}
	if !strings.Contains(view, "moved the mock") {
		t.Error("the newest note should be shown")
	}
}

func TestActivityIsReplacedNotAccumulated(t *testing.T) {
	m := newModel(t)
	m = send(m, ActivityMsg{Event: agent.Event{Kind: agent.KindToolStart, Tool: "read", Detail: "/a/first.go"}})
	m = send(m, ActivityMsg{Event: agent.Event{Kind: agent.KindToolStart, Tool: "edit", Detail: "/a/second.go"}})

	// The whole point of this design: a tool call replaces the previous one.
	// Accumulating them is the firehose being avoided.
	if m.activity != "edit /a/second.go" {
		t.Errorf("activity = %q, want the latest call only", m.activity)
	}
	view := m.View()
	if strings.Contains(view, "first.go") {
		t.Error("a superseded tool call must not remain on screen")
	}
	if !strings.Contains(view, "second.go") {
		t.Error("the current tool call should be shown")
	}
	if m.toolCount != 2 {
		t.Errorf("toolCount = %d, want 2 — the count is kept even though the lines are not", m.toolCount)
	}
}

func TestActivityCollapsesMultilineDetail(t *testing.T) {
	m := newModel(t)
	m = send(m, ActivityMsg{Event: agent.Event{
		Kind: agent.KindToolStart, Tool: "bash", Detail: "cat <<EOF\nline one\nline two\nEOF",
	}})
	if strings.Contains(m.activity, "\n") {
		t.Errorf("a heredoc must not become multiple rows: %q", m.activity)
	}
}

func TestNonToolEventsDoNotCountAsActivity(t *testing.T) {
	m := newModel(t)
	m = send(m, ActivityMsg{Event: agent.Event{Kind: agent.KindAssistantText, Text: "some prose"}})
	if m.toolCount != 0 {
		t.Error("prose is not a tool call")
	}
	if m.activity != "" {
		t.Errorf("activity = %q, want empty", m.activity)
	}
}

func TestTokensAreMonotonic(t *testing.T) {
	m := newModel(t)
	m = send(m, ActivityMsg{Event: agent.Event{Kind: agent.KindToolStart, Tokens: 500}})
	m = send(m, ActivityMsg{Event: agent.Event{Kind: agent.KindToolStart, Tokens: 0}})
	if m.tokens != 500 {
		t.Errorf("tokens = %d, want 500 — a later zero must not walk the count back", m.tokens)
	}
}

func TestPhaseChangeClearsActivity(t *testing.T) {
	m := newModel(t)
	m = send(m, PhaseMsg{Phase: loop.PhaseWorking, Iteration: 1})
	m = send(m, ActivityMsg{Event: agent.Event{Kind: agent.KindToolStart, Tool: "edit", Detail: "/x.go"}})
	m = send(m, PhaseMsg{Phase: loop.PhaseReviewing, Iteration: 1})

	// Carrying the worker's last tool call into the review would misreport what
	// is happening now.
	if m.activity != "" {
		t.Errorf("activity = %q, want cleared on a phase change", m.activity)
	}
	if !strings.Contains(m.View(), "reviewing") {
		t.Error("the header should show the current phase")
	}
}

func TestVerdictHistoryIsShown(t *testing.T) {
	m := newModel(t)
	m = send(m, VerdictMsg{Iteration: 1, Verdict: run.VerdictNeedsWork})
	m = send(m, VerdictMsg{Iteration: 2, Verdict: run.VerdictError})
	m = send(m, VerdictMsg{Iteration: 3, Verdict: run.VerdictPass})

	view := m.View()
	// The shape of a run — two failures then a pass — says more than the last
	// verdict alone.
	for _, want := range []string{"1:NEEDS", "2:ERR", "3:PASS"} {
		if !strings.Contains(view, want) {
			t.Errorf("verdict line missing %q:\n%s", want, view)
		}
	}
}

func TestEndedStateReplacesTheActivityLine(t *testing.T) {
	m := newModel(t)
	m = send(m, ActivityMsg{Event: agent.Event{Kind: agent.KindToolStart, Tool: "bash", Detail: "go test"}})
	m = send(m, EndedMsg{Status: run.StatusDone, Ending: "the reviewer confirmed it is finished"})

	view := m.View()
	if !strings.Contains(view, "run ended") {
		t.Error("an ended run should say so")
	}
	if !strings.Contains(view, "the reviewer confirmed it is finished") {
		t.Error("the reason should be shown — it is the answer to 'what happened'")
	}
	if strings.Contains(view, "go test") {
		t.Error("a finished run must not still show an in-flight tool call")
	}
}

func TestEmptyStateExplainsItself(t *testing.T) {
	m := newModel(t)
	// A run that has not reported yet must not look broken.
	if !strings.Contains(m.View(), "waiting for the agent") {
		t.Error("an empty notes pane should explain that it is waiting")
	}

	m = send(m, EndedMsg{Status: run.StatusFailed, Ending: "the agent process failed"})
	// A finished run that never spoke is a different, and notable, condition.
	if !strings.Contains(m.View(), "said nothing") {
		t.Error("a finished run with no notes should say so")
	}
}

func TestNotesAreBounded(t *testing.T) {
	m := newModel(t)
	for i := 0; i < maxNotes+50; i++ {
		m = send(m, NoteMsg{Note: notes.Note{At: time.Now(), Text: "note"}})
	}
	// The full history is on disk; a 40-iteration run must not grow the view
	// without limit.
	if len(m.notes) != maxNotes {
		t.Errorf("kept %d notes, want a cap of %d", len(m.notes), maxNotes)
	}
}

func TestNotesPaneFitsTheWindow(t *testing.T) {
	m := newModel(t)
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 14})
	for i := 0; i < 100; i++ {
		m = send(m, NoteMsg{Note: notes.Note{At: time.Now(), Text: "a note"}})
	}
	got := strings.Count(m.notesPane(), "\n") + 1
	if got > 14 {
		t.Errorf("notes pane rendered %d rows into a 14-row window", got)
	}
}

func TestViewDoesNotExceedWidth(t *testing.T) {
	m := newModel(t)
	m = send(m, tea.WindowSizeMsg{Width: 60, Height: 20})
	m = send(m, NoteMsg{Note: notes.Note{
		At:   time.Now(),
		Text: strings.Repeat("a very long note that should be truncated ", 10),
	}})
	m = send(m, ActivityMsg{Event: agent.Event{
		Kind: agent.KindToolStart, Tool: "bash", Detail: strings.Repeat("x", 500),
	}})
	for _, line := range strings.Split(m.View(), "\n") {
		// Styling adds escape sequences, so measure the visible text only.
		if n := len([]rune(stripANSI(line))); n > 60 {
			t.Errorf("line of %d visible cells exceeds the 60-cell window: %q", n, stripANSI(line))
		}
	}
}

func TestWindowResizeIsHonoured(t *testing.T) {
	m := newModel(t)
	m = send(m, tea.WindowSizeMsg{Width: 42, Height: 12})
	if m.width != 42 || m.height != 12 {
		t.Errorf("size = %dx%d, want 42x12", m.width, m.height)
	}
}

func TestQuitDoesNotStopTheRun(t *testing.T) {
	m := newModel(t)
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q should quit the view")
	}
	// Detaching from the display is the normal way to leave a run working;
	// nothing here may mark the run as ended.
	if out.(*Model).ended {
		t.Error("quitting the view must not mark the run as ended")
	}
}

func TestNotesArePickedUpFromTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	st := &run.State{Name: "demo", Agent: "pi", Cwd: "/tree"}
	m := New(st, path)

	// The agent appends to the file; the view polls it. This is the whole
	// progress channel, so it is worth asserting end to end.
	if err := writeFile(path, "- found the deadlock in the worker pool\n"); err != nil {
		t.Fatal(err)
	}
	m = send(m, pollNoteMsg(time.Now()))
	if len(m.notes) != 1 || !strings.Contains(m.notes[0].Text, "deadlock") {
		t.Fatalf("notes = %+v", m.notes)
	}
	// Markdown bullets are stripped so the view does not render markdown.
	if strings.HasPrefix(m.notes[0].Text, "-") {
		t.Errorf("bullet should be stripped: %q", m.notes[0].Text)
	}
}

func TestObserverForwardsEverything(t *testing.T) {
	// The loop reports through this adapter and never touches the view, which
	// is what keeps the loop free of UI imports.
	obs := Observer(nil)
	if obs.OnPhase == nil || obs.OnActivity == nil || obs.OnVerdict == nil || obs.OnEnded == nil {
		t.Error("every loop callback should be wired to a message")
	}
	var _ loop.Observer = obs
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// stripANSI removes escape sequences so a rendered line can be measured.
func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEscape = true
		case inEscape && (r == 'm' || r == 'K' || r == 'H'):
			inEscape = false
		case !inEscape:
			b.WriteRune(r)
		}
	}
	return b.String()
}
