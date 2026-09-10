// Package runview is the display inside a run's own tmux session.
//
// It answers "what is this run doing, and should I care" without printing
// every tool call. The notes the agent writes are the main pane; the tool
// stream is compressed to one line that is replaced rather than appended.
// The raw stream still goes to log.md for when something needs digging into.
package runview

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MahammadAgayev/harness/internal/agent"
	"github.com/MahammadAgayev/harness/internal/loop"
	"github.com/MahammadAgayev/harness/internal/notes"
	"github.com/MahammadAgayev/harness/internal/run"
	"github.com/MahammadAgayev/harness/internal/tui/format"
	"github.com/MahammadAgayev/harness/internal/tui/theme"
)

// notePollInterval is how often the notes file is checked. Two seconds is well
// inside human patience for a process whose steps take minutes, and cheap
// enough that it does not matter.
const notePollInterval = 2 * time.Second

// tickInterval drives the elapsed clock. One second, because a clock that
// updates less often looks frozen, which is exactly the impression this view
// exists to prevent.
const tickInterval = time.Second

// maxNotes bounds what is kept in memory. The full history is on disk; this is
// a live view, and a run of 40 iterations should not grow without limit.
const maxNotes = 500

// Messages published into the model. Every mutation of the view arrives as one
// of these, which is what makes Update testable without a terminal.
type (
	NoteMsg  struct{ Note notes.Note }
	PhaseMsg struct {
		Phase     loop.Phase
		Iteration int
	}
	ActivityMsg struct{ Event agent.Event }
	VerdictMsg  struct {
		Iteration int
		Verdict   run.Verdict
	}
	EndedMsg struct {
		Status run.Status
		Ending string
	}
	tickMsg     time.Time
	pollNoteMsg time.Time
)

type verdictRow struct {
	iteration int
	verdict   run.Verdict
	at        time.Time
}

// Model is the run view's state.
type Model struct {
	name      string
	agentName string
	cwd       string
	notesFile string

	started   time.Time
	phase     loop.Phase
	iteration int
	// phaseAt is when the current phase began, so the header can show how long
	// this iteration has been going rather than only the total.
	phaseAt time.Time

	activity  string
	toolCount int
	tokens    int

	notes    []notes.Note
	verdicts []verdictRow

	ended  bool
	status run.Status
	ending string

	tailer *notes.Tailer
	width  int
	height int
}

// New builds the view for a run.
func New(st *run.State, notesFile string) *Model {
	return &Model{
		name:      st.Name,
		agentName: st.Agent,
		cwd:       st.Cwd,
		notesFile: notesFile,
		started:   time.Now(),
		phaseAt:   time.Now(),
		// Start from the end: a restarted view should show what happens next,
		// not replay the whole of a previous run's notes.
		tailer: notes.NewTailerFromEnd(notesFile),
		width:  100,
		height: 30,
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(tick(), pollNotes())
}

func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func pollNotes() tea.Cmd {
	return tea.Tick(notePollInterval, func(t time.Time) tea.Msg { return pollNoteMsg(t) })
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			// Quitting the view does not stop the run: the loop owns its own
			// lifetime, and detaching from the display is the normal way to
			// leave a run working. Stopping is `harness stop`.
			return m, tea.Quit
		}

	case tickMsg:
		return m, tick()

	case pollNoteMsg:
		var cmds []tea.Cmd
		for _, n := range m.tailer.Poll() {
			m.appendNote(n)
		}
		cmds = append(cmds, pollNotes())
		return m, tea.Batch(cmds...)

	case NoteMsg:
		m.appendNote(msg.Note)

	case PhaseMsg:
		m.phase, m.iteration, m.phaseAt = msg.Phase, msg.Iteration, time.Now()
		// Activity belongs to a phase; carrying the last tool call of the
		// worker into the review would misreport what is happening now.
		m.activity = ""

	case ActivityMsg:
		e := msg.Event
		if e.Tokens > m.tokens {
			m.tokens = e.Tokens
		}
		if e.Kind == agent.KindToolStart {
			m.toolCount++
			m.activity = strings.TrimSpace(e.Tool + " " + format.OneLine(e.Detail))
		}

	case VerdictMsg:
		m.verdicts = append(m.verdicts, verdictRow{msg.Iteration, msg.Verdict, time.Now()})

	case EndedMsg:
		m.ended, m.status, m.ending = true, msg.Status, msg.Ending
		m.phase, m.activity = loop.PhaseDone, ""
	}
	return m, nil
}

func (m *Model) appendNote(n notes.Note) {
	m.notes = append(m.notes, n)
	if len(m.notes) > maxNotes {
		m.notes = m.notes[len(m.notes)-maxNotes:]
	}
}

func (m *Model) View() string {
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	b.WriteString(m.notesPane())
	b.WriteString("\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m *Model) header() string {
	title := theme.Title.Render("harness · " + m.name)
	meta := theme.Dim.Render(fmt.Sprintf("%s · %s", m.agentName, format.Truncate(m.cwd, 50)))

	var state string
	if m.ended {
		state = theme.Status(string(m.status), false).Render(strings.ToUpper(string(m.status)))
	} else {
		state = theme.Status("running", true).Render(
			fmt.Sprintf("iteration %d · %s %s", m.iteration, m.phase, format.Duration(time.Since(m.phaseAt))))
	}

	stats := theme.Dim.Render(fmt.Sprintf("%s elapsed · %d tools · %s tokens",
		format.Duration(time.Since(m.started)), m.toolCount, format.Tokens(m.tokens)))

	lines := []string{
		title + "  " + meta,
		state + "  " + stats,
	}
	if v := m.verdictLine(); v != "" {
		lines = append(lines, v)
	}
	lines = append(lines, theme.Rule.Render(strings.Repeat("─", max(1, m.width))))
	return strings.Join(lines, "\n")
}

// verdictLine is the run's history in one row: the shape of a run — three
// NEEDS_WORK then a PASS — is more informative than any single verdict.
func (m *Model) verdictLine() string {
	if len(m.verdicts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m.verdicts))
	for _, v := range m.verdicts {
		short := map[run.Verdict]string{
			run.VerdictPass:      "PASS",
			run.VerdictNeedsWork: "NEEDS",
			run.VerdictError:     "ERR",
		}[v.verdict]
		if short == "" {
			short = "?"
		}
		parts = append(parts, theme.Verdict(string(v.verdict)).Render(fmt.Sprintf("%d:%s", v.iteration, short)))
	}
	return theme.Label.Render("reviews ") + strings.Join(parts, theme.Dim.Render(" · "))
}

// notesPane is the main event. Everything above and below it is context.
func (m *Model) notesPane() string {
	// Leave room for the header (up to 4 lines) and footer (2).
	room := max(3, m.height-8)
	shown := m.notes
	if len(shown) > room {
		shown = shown[len(shown)-room:]
	}

	if len(shown) == 0 {
		hint := "waiting for the agent to report…"
		if m.ended {
			hint = "the run said nothing"
		}
		return theme.Dim.Render("  " + hint)
	}

	var b strings.Builder
	for i, n := range shown {
		stamp := theme.Dim.Render(n.At.Format("15:04:05"))
		text := format.Truncate(format.OneLine(n.Text), max(20, m.width-12))
		// The newest note is the one being read; the rest are context.
		style := theme.Activity
		if i == len(shown)-1 {
			style = theme.NoteText
		}
		b.WriteString(stamp + " " + style.Render(text) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) footer() string {
	rule := theme.Rule.Render(strings.Repeat("─", max(1, m.width)))

	var bottom string
	switch {
	case m.ended:
		bottom = theme.Status(string(m.status), false).Render("run ended") +
			theme.Dim.Render(" — "+m.ending)
	case m.activity != "":
		// One line, replaced each time. Accumulating these is the firehose
		// whole design exists to avoid.
		bottom = theme.Activity.Render("· " + format.Truncate(m.activity, max(20, m.width-4)))
	default:
		bottom = theme.Dim.Render("· thinking")
	}

	help := theme.Help.Render("q detach (the run keeps going) · harness stop " + m.name)
	return rule + "\n" + bottom + "\n" + help
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Program wires a Model into a bubbletea program and returns both, so the
// caller can publish messages into it from the loop's Observer.
func Program(m *Model) *tea.Program {
	// WithoutSignalHandler: the run process installs its own handling so that a
	// SIGINT stops the run cleanly rather than only closing the display.
	return tea.NewProgram(m, tea.WithoutSignalHandler())
}

// Observer adapts a running program to the loop's Observer interface.
//
// Everything the loop reports becomes a message. The loop never touches the
// view directly, which is what keeps it free of UI imports.
func Observer(p *tea.Program) loop.Observer {
	return loop.Observer{
		OnPhase: func(ph loop.Phase, it int) {
			p.Send(PhaseMsg{Phase: ph, Iteration: it})
		},
		OnActivity: func(e agent.Event) {
			p.Send(ActivityMsg{Event: e})
		},
		OnVerdict: func(it int, v run.Verdict, _ string) {
			p.Send(VerdictMsg{Iteration: it, Verdict: v})
		},
		OnEnded: func(s run.Status, ending string) {
			p.Send(EndedMsg{Status: s, Ending: ending})
		},
	}
}
