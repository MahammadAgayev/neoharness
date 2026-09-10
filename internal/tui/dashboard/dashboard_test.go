package dashboard

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MahammadAgayev/harness/internal/run"
)

func row(name string, status run.Status, live bool, iter int, v run.Verdict, note string) Row {
	return Row{
		State: &run.State{
			Name:      name,
			Status:    status,
			Iteration: iter,
			Verdict:   v,
			UpdatedAt: time.Now(),
		},
		Live:     live,
		LastNote: note,
	}
}

func fixed(rows ...Row) Loader {
	return func() []Row {
		sortRows(rows)
		return rows
	}
}

func newModel(rows ...Row) *Model {
	m := New(fixed(rows...))
	m.Init()
	return m
}

func send(m *Model, msg tea.Msg) *Model {
	out, _ := m.Update(msg)
	return out.(*Model)
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// TestStaleRunningIsReportedDead is the reason tmux is the liveness oracle.
//
// state.json is written by the run's own process; a machine that slept or a
// session that was killed leaves "running" on disk forever. There are three
// such stale folders in the author's ~/.pi/harness right now.
func TestStaleRunningIsReportedDead(t *testing.T) {
	r := row("ghost", run.StatusRunning, false, 3, run.VerdictNeedsWork, "")
	if got := r.Status(); got != "dead" {
		t.Errorf("Status() = %q, want dead", got)
	}
	if !r.Attention() {
		t.Error("a dead run needs a human")
	}
}

func TestLiveRunningIsRunning(t *testing.T) {
	r := row("alive", run.StatusRunning, true, 1, run.VerdictUnknown, "")
	if got := r.Status(); got != "running" {
		t.Errorf("Status() = %q, want running", got)
	}
	if r.Attention() {
		t.Error("a working run does not need a human — flagging it dilutes the signal")
	}
}

func TestAttentionIsNarrow(t *testing.T) {
	// If everything is flagged, nothing is.
	for _, tc := range []struct {
		status run.Status
		live   bool
		want   bool
	}{
		{run.StatusDone, false, true},     // finished: news
		{run.StatusFailed, false, true},   // gave up: news
		{run.StatusRunning, false, true},  // died without unwinding: a fault
		{run.StatusRunning, true, false},  // working
		{run.StatusIdle, false, false},    // never started
		{run.StatusStopped, false, false}, // stopped on purpose
	} {
		r := row("x", tc.status, tc.live, 0, run.VerdictUnknown, "")
		if got := r.Attention(); got != tc.want {
			t.Errorf("Attention(%s, live=%v) = %v, want %v", tc.status, tc.live, got, tc.want)
		}
	}
}

func TestSortPutsLiveRunsFirst(t *testing.T) {
	rows := []Row{
		row("done", run.StatusDone, false, 5, run.VerdictPass, ""),
		row("live", run.StatusRunning, true, 2, run.VerdictNeedsWork, ""),
		row("idle", run.StatusIdle, false, 0, run.VerdictUnknown, ""),
	}
	sortRows(rows)
	if rows[0].State.Name != "live" {
		t.Errorf("live run should sort first, got %q", rows[0].State.Name)
	}
	// Then the one wanting attention, then the rest.
	if rows[1].State.Name != "done" {
		t.Errorf("attention run should sort second, got %q", rows[1].State.Name)
	}
}

func TestNavigation(t *testing.T) {
	m := newModel(
		row("a", run.StatusRunning, true, 1, run.VerdictUnknown, ""),
		row("b", run.StatusRunning, true, 1, run.VerdictUnknown, ""),
		row("c", run.StatusRunning, true, 1, run.VerdictUnknown, ""),
	)
	if m.cursor != 0 {
		t.Fatalf("cursor should start at 0, got %d", m.cursor)
	}
	m = send(m, key("j"))
	m = send(m, key("j"))
	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2", m.cursor)
	}
	// The cursor must not walk off the end.
	m = send(m, key("j"))
	if m.cursor != 2 {
		t.Errorf("cursor ran past the last row: %d", m.cursor)
	}
	m = send(m, key("k"))
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1", m.cursor)
	}
	m = send(m, key("g"))
	if m.cursor != 0 {
		t.Errorf("g should go to the top, got %d", m.cursor)
	}
	m = send(m, key("G"))
	if m.cursor != 2 {
		t.Errorf("G should go to the bottom, got %d", m.cursor)
	}
	// And not above the first.
	m.cursor = 0
	m = send(m, key("k"))
	if m.cursor != 0 {
		t.Errorf("cursor went negative: %d", m.cursor)
	}
}

func TestNavigationOnEmptyList(t *testing.T) {
	m := newModel()
	for _, k := range []string{"j", "k", "g", "G", "enter", "s"} {
		m = send(m, key(k))
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d on an empty list", m.cursor)
	}
	if m.action != (Action{}) {
		t.Errorf("no action should be possible with no runs, got %+v", m.action)
	}
	if !strings.Contains(m.View(), "no runs yet") {
		t.Error("an empty dashboard should say how to make a run")
	}
}

func TestEnterRequestsAttach(t *testing.T) {
	m := newModel(row("alpha", run.StatusRunning, true, 1, run.VerdictUnknown, ""))
	m = send(m, key("enter"))
	// The action is recorded rather than performed: attaching has to happen
	// after the TUI releases the terminal.
	if m.Action().Attach != "alpha" {
		t.Errorf("Action = %+v, want an attach to alpha", m.Action())
	}
}

func TestEnterOnADeadRunExplainsInstead(t *testing.T) {
	m := newModel(row("ghost", run.StatusRunning, false, 1, run.VerdictUnknown, ""))
	m = send(m, key("enter"))
	if m.Action().Attach != "" {
		t.Error("must not try to attach to a run with no session")
	}
	// Failing in the shell after the TUI exits reads as the dashboard crashing.
	if !strings.Contains(m.View(), "no live session") {
		t.Errorf("the reason should be shown in the dashboard:\n%s", m.View())
	}
}

func TestStopRequestsStop(t *testing.T) {
	m := newModel(row("alpha", run.StatusRunning, true, 1, run.VerdictUnknown, ""))
	m = send(m, key("s"))
	if m.Action().Stop != "alpha" {
		t.Errorf("Action = %+v, want a stop of alpha", m.Action())
	}
}

func TestStopIsIgnoredForADeadRun(t *testing.T) {
	m := newModel(row("ghost", run.StatusDone, false, 1, run.VerdictPass, ""))
	m = send(m, key("s"))
	if m.Action().Stop != "" {
		t.Error("stopping an already-finished run is meaningless")
	}
}

func TestCursorStaysOnTheSameRunAcrossARefresh(t *testing.T) {
	rows := []Row{
		row("alpha", run.StatusRunning, true, 1, run.VerdictUnknown, ""),
		row("beta", run.StatusRunning, true, 1, run.VerdictUnknown, ""),
	}
	// A loader that reverses the order on the second call, simulating a sort
	// changing underneath the cursor.
	calls := 0
	m := New(func() []Row {
		calls++
		if calls > 1 {
			return []Row{rows[1], rows[0]}
		}
		return rows
	})
	m.Init()
	m = send(m, key("j")) // now on beta
	if got, _ := m.current(); got.State.Name != "beta" {
		t.Fatalf("expected to be on beta, got %q", got.State.Name)
	}
	m = send(m, refreshMsg(time.Now()))
	// A cursor that jumps as you reach for Enter is how you attach to the
	// wrong run.
	if got, _ := m.current(); got.State.Name != "beta" {
		t.Errorf("cursor moved to %q across a refresh, want beta", got.State.Name)
	}
}

func TestCursorSurvivesARunDisappearing(t *testing.T) {
	all := []Row{
		row("a", run.StatusDone, false, 1, run.VerdictPass, ""),
		row("b", run.StatusDone, false, 1, run.VerdictPass, ""),
	}
	calls := 0
	m := New(func() []Row {
		calls++
		if calls > 1 {
			return all[:1] // b was deleted
		}
		return all
	})
	m.Init()
	m = send(m, key("j"))
	m = send(m, refreshMsg(time.Now()))
	if m.cursor >= len(m.rows) {
		t.Errorf("cursor %d is out of range for %d rows", m.cursor, len(m.rows))
	}
	if _, ok := m.current(); !ok {
		t.Error("current() should still be valid after a run disappears")
	}
}

func TestRowsRender(t *testing.T) {
	m := newModel(
		row("alpha", run.StatusRunning, true, 3, run.VerdictNeedsWork, "found the deadlock"),
		row("beta", run.StatusDone, false, 7, run.VerdictPass, "all tests green"),
	)
	view := m.View()
	for _, want := range []string{
		"alpha", "beta",
		"RUNNING", "DONE",
		"NEEDS_WORK", "PASS",
		"found the deadlock", "all tests green",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestAttentionIsMarkedInText(t *testing.T) {
	// Colour alone does not survive a screenshot, a colourblind reader, or a
	// hostile palette.
	m := newModel(row("finished", run.StatusDone, false, 2, run.VerdictPass, ""))
	if !strings.Contains(m.View(), "• finished") {
		t.Errorf("a run wanting attention should be marked in text:\n%s", m.View())
	}
}

func TestARunWithNoNotesSaysSo(t *testing.T) {
	m := newModel(row("quiet", run.StatusRunning, true, 1, run.VerdictUnknown, ""))
	// Silence is a reportable condition, not an empty cell.
	if !strings.Contains(m.View(), "nothing reported") {
		t.Errorf("expected the quiet run to be called out:\n%s", m.View())
	}
}

func TestUnknownVerdictRendersAsADash(t *testing.T) {
	m := newModel(row("fresh", run.StatusIdle, false, 0, run.VerdictUnknown, ""))
	if strings.Contains(m.View(), "UNKNOWN") {
		t.Error("an unreviewed run should show a dash, not the word UNKNOWN")
	}
}

func TestViewFitsTheWindow(t *testing.T) {
	var rows []Row
	for i := 0; i < 50; i++ {
		rows = append(rows, row(
			fmt.Sprintf("run-%02d", i), run.StatusRunning, true, i, run.VerdictNeedsWork,
			strings.Repeat("a long note ", 20),
		))
	}
	m := New(fixed(rows...))
	m.Init()
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 20})

	view := m.View()
	if n := strings.Count(view, "\n") + 1; n > 20 {
		t.Errorf("rendered %d rows into a 20-row window", n)
	}
	for _, line := range strings.Split(view, "\n") {
		if n := len([]rune(stripANSI(line))); n > 80 {
			t.Errorf("line of %d visible cells exceeds the 80-cell window:\n%q", n, stripANSI(line))
		}
	}
}

func TestScrollKeepsTheCursorVisible(t *testing.T) {
	var rows []Row
	for i := 0; i < 40; i++ {
		rows = append(rows, row(fmt.Sprintf("run-%02d", i), run.StatusRunning, true, 0, run.VerdictUnknown, ""))
	}
	m := New(fixed(rows...))
	m.Init()
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 15})
	for i := 0; i < 30; i++ {
		m = send(m, key("j"))
	}
	// The selected run must be on screen, or navigation is blind.
	sel, _ := m.current()
	if !strings.Contains(m.View(), sel.State.Name) {
		t.Errorf("selected run %q is not visible after scrolling", sel.State.Name)
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "esc"} {
		m := newModel(row("a", run.StatusRunning, true, 1, run.VerdictUnknown, ""))
		_, cmd := m.Update(key(k))
		if cmd == nil {
			t.Errorf("%q should quit", k)
		}
		if m.Action() != (Action{}) {
			t.Errorf("quitting should not request an action, got %+v", m.Action())
		}
	}
}

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
