// Package theme holds the shared visual vocabulary of the two TUIs, so a
// verdict is the same colour whichever one you are looking at.
//
// The palette is Rosé Pine Moon, with the same semantic assignments as the pi
// theme of that name (accent→foam, error→love, warning→gold, success→the muted
// green). Copied as literals rather than read from pi's theme file: this binary
// does not depend on pi being installed, and a run watched over ssh should not
// change colour because a config file moved.
package theme

import "github.com/charmbracelet/lipgloss"

// The Rosé Pine Moon palette.
const (
	Base          = "#232136"
	Text          = "#e0def4"
	Subtle        = "#908caa"
	Muted         = "#6e6a86"
	Love          = "#eb6f92" // error
	Gold          = "#f6c177" // warning
	Rose          = "#ea9a97"
	Pine          = "#3e8fb0"
	Foam          = "#9ccfd8" // accent
	Iris          = "#c4a7e7"
	HighlightHigh = "#56526e"
	Success       = "#95b1ac"
)

var (
	colText    = lipgloss.Color(Text)
	colSubtle  = lipgloss.Color(Subtle)
	colMuted   = lipgloss.Color(Muted)
	colFoam    = lipgloss.Color(Foam)
	colGold    = lipgloss.Color(Gold)
	colLove    = lipgloss.Color(Love)
	colIris    = lipgloss.Color(Iris)
	colSuccess = lipgloss.Color(Success)
)

var (
	Title    = lipgloss.NewStyle().Bold(true).Foreground(colText)
	Label    = lipgloss.NewStyle().Foreground(colMuted)
	Value    = lipgloss.NewStyle().Foreground(colText)
	Dim      = lipgloss.NewStyle().Foreground(colMuted)
	Activity = lipgloss.NewStyle().Foreground(colSubtle)
	NoteText = lipgloss.NewStyle().Foreground(colText)
	Help     = lipgloss.NewStyle().Foreground(colMuted)
	Rule     = lipgloss.NewStyle().Foreground(colMuted)
	Accent   = lipgloss.NewStyle().Foreground(colFoam)
	Selected = lipgloss.NewStyle().Bold(true).Foreground(colIris)
)

// Verdict colours a verdict string. Kept in one place because the mapping is
// the main thing a glance at either TUI is reading.
func Verdict(v string) lipgloss.Style {
	switch v {
	case "PASS":
		return lipgloss.NewStyle().Bold(true).Foreground(colSuccess)
	case "NEEDS_WORK":
		return lipgloss.NewStyle().Foreground(colGold)
	case "ERROR":
		return lipgloss.NewStyle().Foreground(colLove)
	}
	return lipgloss.NewStyle().Foreground(colMuted)
}

// Status colours a run status.
//
// The live flag matters for exactly one case: a run recorded as running with no
// tmux session behind it died without unwinding. That is a fault, not progress,
// and colouring it like a healthy run is how it goes unnoticed for a day.
func Status(s string, live bool) lipgloss.Style {
	switch {
	case s == "running" && live:
		return lipgloss.NewStyle().Bold(true).Foreground(colFoam)
	case s == "running" && !live:
		return lipgloss.NewStyle().Foreground(colLove)
	case s == "done":
		return lipgloss.NewStyle().Bold(true).Foreground(colSuccess)
	case s == "failed":
		return lipgloss.NewStyle().Foreground(colLove)
	case s == "stopped":
		return lipgloss.NewStyle().Foreground(colGold)
	}
	return lipgloss.NewStyle().Foreground(colMuted)
}
