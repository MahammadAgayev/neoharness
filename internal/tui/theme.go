package tui

import (
	"os"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/internal/model"
)

// Rose Pine Moon. https://rosepinetheme.com/palette
var (
	rpText    = tcell.NewHexColor(0xe0def4)
	rpSubtle  = tcell.NewHexColor(0x908caa)
	rpMuted   = tcell.NewHexColor(0x6e6a86)
	rpLove    = tcell.NewHexColor(0xeb6f92)
	rpGold    = tcell.NewHexColor(0xf6c177)
	rpRose    = tcell.NewHexColor(0xea9a97)
	rpPine    = tcell.NewHexColor(0x3e8fb0)
	rpFoam    = tcell.NewHexColor(0x9ccfd8)
	rpIris    = tcell.NewHexColor(0xc4a7e7)
	rpHLLow   = tcell.NewHexColor(0x2a283e)
	rpHLMed   = tcell.NewHexColor(0x44415a)
	rpOverlay = tcell.NewHexColor(0x393552)
)

// Theme maps meaning to style. Background is never set except on the cursor
// line and the visual selection, so a transparent terminal stays transparent.
type Theme struct {
	Base, Text, Subtle, Muted tcell.Style
	Iris, Rose, Foam, Gold    tcell.Style
	Pine, Love                tcell.Style
	CursorLine, Selection     tcell.Style
	Separator                 tcell.Style
	Bold, Italic, Code        tcell.Style
	NoColor                   bool
}

func newTheme() *Theme {
	base := tcell.StyleDefault
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return &Theme{
			Base: base, Text: base, Subtle: base, Muted: base.Dim(true),
			Iris: base.Bold(true), Rose: base, Foam: base.Bold(true), Gold: base.Bold(true),
			Pine: base, Love: base.Bold(true),
			CursorLine: base.Reverse(true), Selection: base.Reverse(true),
			Separator: base.Dim(true),
			Bold:      base.Bold(true), Italic: base.Italic(true), Code: base,
			NoColor: true,
		}
	}
	fg := func(c tcell.Color) tcell.Style { return base.Foreground(c) }
	return &Theme{
		Base:       base,
		Text:       fg(rpText),
		Subtle:     fg(rpSubtle),
		Muted:      fg(rpMuted),
		Iris:       fg(rpIris),
		Rose:       fg(rpRose),
		Foam:       fg(rpFoam),
		Gold:       fg(rpGold),
		Pine:       fg(rpPine),
		Love:       fg(rpLove),
		CursorLine: base.Background(rpHLLow),
		Selection:  base.Background(rpHLMed),
		Separator:  fg(rpOverlay),
		Bold:       fg(rpText).Bold(true),
		Italic:     fg(rpText).Italic(true),
		Code:       fg(rpRose),
	}
}

// overlay applies a background (cursor line, selection) on top of a style
// without losing its foreground.
func overlay(st, bg tcell.Style) tcell.Style {
	_, b, attrs := bg.Decompose()
	if attrs&tcell.AttrReverse != 0 {
		return st.Reverse(true)
	}
	return st.Background(b)
}

// stateGlyph is the one-character state marker used everywhere.
func stateGlyph(s model.State) string {
	switch s {
	case model.StateRunning:
		return "●"
	case model.StateWaiting:
		return "◆"
	case model.StateResolved:
		return "✓"
	}
	return "○"
}

func (t *Theme) state(s model.State) tcell.Style {
	switch s {
	case model.StateRunning:
		return t.Foam
	case model.StateWaiting:
		return t.Gold
	case model.StateResolved:
		return t.Pine
	}
	return t.Muted
}

func stateLabel(s model.State) string {
	switch s {
	case model.StateWaiting:
		return "waiting"
	case "":
		return "idle"
	}
	return string(s)
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
