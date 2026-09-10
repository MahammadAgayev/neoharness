// Package dashboard is the cross-run view: one row per run, refreshed by
// polling, with Enter to jump into a run's tmux session.
//
// This is the "massively easy" surface. It exists so that the answer to "what
// are my runs doing, and does any of them need me" is one glance rather than
// four terminal tabs.
//
// It holds no connection to any run. Every refresh re-reads the run folders
// from disk and asks tmux which sessions are alive. That makes it stateless,
// crash-free in the sense that matters — killing it costs nothing — and
// immune to a run that wedges.
package dashboard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MahammadAgayev/harness/internal/notes"
	"github.com/MahammadAgayev/harness/internal/run"
	"github.com/MahammadAgayev/harness/internal/tmuxctl"
	"github.com/MahammadAgayev/harness/internal/tui/format"
	"github.com/MahammadAgayev/harness/internal/tui/theme"
)

// RefreshInterval is how often the run folders are re-read.
//
// Five seconds against iterations that take minutes: the lag is invisible, and
// polling avoids a filesystem watcher's lifecycle, platform differences, and
// dependency. This was a deliberate choice over fsnotify.
const RefreshInterval = 5 * time.Second

// Row is one run as the dashboard sees it.
type Row struct {
	State *run.State
	// Live is what tmux says, which outranks State.Status. A run recorded as
	// running with no session behind it died without unwinding — the machine
	// slept, or the session was killed — and showing it as running is how that
	// goes unnoticed.
	Live     bool
	LastNote string
	NoteAt   time.Time
}

// Status is what to display, reconciling the recorded status against tmux.
func (r Row) Status() string {
	if r.State.Status == run.StatusRunning && !r.Live {
		return "dead"
	}
	return string(r.State.Status)
}

// Attention reports whether this run wants a human.
//
// Deliberately narrow. If everything is flagged, nothing is: a finished run is
// news, a dead one is a fault, and a run that is working is not either.
func (r Row) Attention() bool {
	switch r.Status() {
	case "dead", "failed":
		return true
	case "done":
		return true
	}
	return false
}

// Loader reads the current state of the world. Injected so the dashboard can
// be tested against fixtures instead of a live tmux and a real home directory.
type Loader func() []Row

// DefaultLoader reads the real run folders and the real tmux.
func DefaultLoader() []Row {
	states, err := run.List()
	if err != nil {
		return nil
	}
	// One list-sessions for all runs rather than one has-session each: cheaper,
	// and it cannot disagree with itself halfway through a refresh.
	live := tmuxctl.LiveRuns()

	rows := make([]Row, 0, len(states))
	for _, st := range states {
		row := Row{State: st, Live: live[st.Name]}
		if ns := notes.ReadLast(run.Open(st.Name).NotesFile(), 1); len(ns) > 0 {
			row.LastNote = ns[0].Text
		}
		rows = append(rows, row)
	}
	sortRows(rows)
	return rows
}

// sortRows puts live runs first, then the ones wanting attention, then the
// rest by recency. The top of the list should be what to look at.
func sortRows(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Live != b.Live {
			return a.Live
		}
		if a.Attention() != b.Attention() {
			return a.Attention()
		}
		return a.State.UpdatedAt.After(b.State.UpdatedAt)
	})
}

type refreshMsg time.Time

// Action is something the dashboard asks the caller to do after it exits.
//
// Attaching has to happen after the TUI has released the terminal, and killing
// tmux from inside a bubbletea program running in that same tmux session is a
// good way to produce a confusing hang. So the model records an intent and the
// caller carries it out.
type Action struct {
	Attach string // run name to attach to
	Stop   string // run name to stop
}

// Model is the dashboard state.
type Model struct {
	load     Loader
	rows     []Row
	cursor   int
	width    int
	height   int
	action   Action
	lastLoad time.Time
	err      string
}

// New builds a dashboard over a loader.
func New(load Loader) *Model {
	return &Model{load: load, width: 100, height: 24}
}

// Action returns what the caller should do once the program has exited.
func (m *Model) Action() Action { return m.action }

func (m *Model) Init() tea.Cmd {
	m.refresh()
	return tick()
}

func tick() tea.Cmd {
	return tea.Tick(RefreshInterval, func(t time.Time) tea.Msg { return refreshMsg(t) })
}

func (m *Model) refresh() {
	// Keep the cursor on the same run across a refresh. Sorting can reorder
	// rows underneath it, and a cursor that jumps while you are reaching for
	// Enter is how you attach to the wrong run.
	var selected string
	if r, ok := m.current(); ok {
		selected = r.State.Name
	}
	m.rows = m.load()
	m.lastLoad = time.Now()
	if selected != "" {
		for i, r := range m.rows {
			if r.State.Name == selected {
				m.cursor = i
				break
			}
		}
	}
	if m.cursor >= len(m.rows) {
		m.cursor = max(0, len(m.rows)-1)
	}
}

func (m *Model) current() (Row, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return Row{}, false
	}
	return m.rows[m.cursor], true
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case refreshMsg:
		m.refresh()
		return m, tick()

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "j", "down":
			if m.cursor < len(m.rows)-1 {
				m.cursor++
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "g", "home":
			m.cursor = 0
		case "G", "end":
			m.cursor = max(0, len(m.rows)-1)
		case "r":
			m.refresh()
		case "enter":
			if r, ok := m.current(); ok {
				if !r.Live {
					// Attaching to a dead run would fail in the shell after the
					// TUI exits, which reads as the dashboard crashing.
					m.err = fmt.Sprintf("%s has no live session", r.State.Name)
					return m, nil
				}
				m.action = Action{Attach: r.State.Name}
				return m, tea.Quit
			}
		case "s":
			if r, ok := m.current(); ok && r.Live {
				m.action = Action{Stop: r.State.Name}
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

// Column widths. The name and the note get whatever is left, because they are
// what is actually read.
const (
	colStatus = 8
	colIter   = 5
	colVerdit = 10
	colAge    = 10
)

func (m *Model) View() string {
	var b strings.Builder

	b.WriteString(theme.Title.Render("harness") + "  " +
		theme.Dim.Render(fmt.Sprintf("%d runs · refreshed %s",
			len(m.rows), m.lastLoad.Format("15:04:05"))) + "\n")
	b.WriteString(theme.Rule.Render(strings.Repeat("─", max(1, m.width))) + "\n")

	if len(m.rows) == 0 {
		b.WriteString(theme.Dim.Render("  no runs yet — harness init <name>") + "\n")
		b.WriteString(m.help())
		return b.String()
	}

	nameW, noteW := m.columnWidths()
	b.WriteString(theme.Label.Render(
		"  "+format.Cell("RUN", nameW)+" "+
			format.Cell("STATUS", colStatus)+" "+
			format.Cell("ITER", colIter)+" "+
			format.Cell("VERDICT", colVerdit)+" "+
			format.Cell("AGE", colAge)+" "+
			"LAST NOTE") + "\n")

	room := max(1, m.height-6)
	for i, r := range m.visibleRows(room) {
		b.WriteString(m.renderRow(r, i+m.scrollOffset(room) == m.cursor, nameW, noteW) + "\n")
	}

	if m.err != "" {
		b.WriteString(theme.Verdict("ERROR").Render("  "+m.err) + "\n")
	}
	b.WriteString(m.help())
	return b.String()
}

func (m *Model) columnWidths() (nameW, noteW int) {
	fixed := 2 + colStatus + colIter + colVerdit + colAge + 5
	remaining := max(30, m.width-fixed)
	nameW = min(28, max(12, remaining/3))
	noteW = max(10, remaining-nameW)
	return nameW, noteW
}

// scrollOffset keeps the cursor on screen for a list longer than the window.
func (m *Model) scrollOffset(room int) int {
	if len(m.rows) <= room || m.cursor < room {
		return 0
	}
	return min(m.cursor-room+1, len(m.rows)-room)
}

func (m *Model) visibleRows(room int) []Row {
	off := m.scrollOffset(room)
	end := min(off+room, len(m.rows))
	return m.rows[off:end]
}

func (m *Model) renderRow(r Row, selected bool, nameW, noteW int) string {
	status := r.Status()

	marker := "  "
	if selected {
		marker = theme.Selected.Render("▌ ")
	}
	// A run wanting attention is marked in the text, not only by colour: this
	// has to survive a screenshot, a colourblind reader, and a terminal with a
	// hostile palette.
	name := format.Cell(r.State.Name, nameW)
	if r.Attention() {
		name = format.Cell("• "+r.State.Name, nameW)
	}

	verdict := string(r.State.Verdict)
	if verdict == string(run.VerdictUnknown) {
		verdict = "—"
	}

	note := r.LastNote
	if note == "" {
		note = theme.Dim.Render("(nothing reported)")
	} else {
		note = theme.Activity.Render(format.Cell(note, noteW))
	}

	nameStyle := theme.Value
	if selected {
		nameStyle = theme.Selected
	}

	return marker +
		nameStyle.Render(name) + " " +
		theme.Status(status, r.Live).Render(format.Cell(strings.ToUpper(status), colStatus)) + " " +
		theme.Dim.Render(format.Cell(fmt.Sprintf("%d", r.State.Iteration), colIter)) + " " +
		theme.Verdict(verdict).Render(format.Cell(verdict, colVerdit)) + " " +
		theme.Dim.Render(format.Cell(format.Ago(r.State.UpdatedAt), colAge)) + " " +
		note
}

func (m *Model) help() string {
	return theme.Rule.Render(strings.Repeat("─", max(1, m.width))) + "\n" +
		theme.Help.Render("j/k move · enter attach · s stop · r refresh · q quit")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
