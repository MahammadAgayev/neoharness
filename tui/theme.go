package tui

import (
	"os"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// rgb256 is a theme color with its xterm-256 fallback. tcell's own nearest
// match puts Love, Gold and Rose all near pink-orange, so error, waiting and
// another session's words look alike; the fallbacks keep them apart and each
// at its contrast.
type rgb256 struct {
	rgb   int32
	cterm int
}

// Rose Pine Moon. https://rosepinetheme.com/palette
// Text, Subtle, Muted and Pine are brighter than the palette's, the values
// the desktop app computes (desktop/src/renderer/lib/themes.ts), so every
// level reads at its contrast: text 13:1, subtle 7:1, muted 5:1.
var (
	rpText    = rgb256{0xebe9f8, 255}
	rpSubtle  = rgb256{0xafacc2, 145}
	rpMuted   = rgb256{0x9491a9, 246}
	rpLove    = rgb256{0xeb6f92, 204}
	rpGold    = rgb256{0xf6c177, 179}
	rpRose    = rgb256{0xea9a97, 181}
	rpPine    = rgb256{0x559cb9, 74}
	rpFoam    = rgb256{0x9ccfd8, 116}
	rpIris    = rgb256{0xc4a7e7, 183}
	rpHLLow   = rgb256{0x2a283e, 236}
	rpHLMed   = rgb256{0x44415a, 238}
	rpOverlay = rgb256{0x393552, 237}
	rpBase    = rgb256{0x232136, 235}
	// rpGold at 20% over rpBase, as the Rose Pine Neovim theme blends Search.
	rpGoldLow = rgb256{0x4d4143, 94}
)

func (c rgb256) in(truecolor bool) tcell.Color {
	if truecolor {
		return tcell.NewHexColor(c.rgb)
	}
	return tcell.PaletteColor(c.cterm)
}

// Theme maps a role to a style; the roles have the app's names (fg, fg-2,
// fg-3, accent, wait, alert, run, done, peer). Background is never set
// except on the cursor line, the visual selection and search matches, so a
// transparent terminal stays transparent.
type Theme struct {
	Base, Fg, Fg2, Fg3    tcell.Style
	Accent, Wait, Alert   tcell.Style
	Run, Done, Peer       tcell.Style
	CursorLine, Selection tcell.Style
	Search, CurSearch     tcell.Style
	Separator             tcell.Style
	Bold, Italic, Code    tcell.Style
	NoColor               bool
	// TrueColor is false on a 256-color terminal; colors then come from the
	// cterm values.
	TrueColor bool
}

// newTheme takes the terminal's color count, tcell's Screen.Colors.
func newTheme(colors int) *Theme {
	base := tcell.StyleDefault
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return &Theme{
			Base: base, Fg: base, Fg2: base, Fg3: base.Dim(true),
			Accent: base.Bold(true), Peer: base, Run: base.Bold(true), Wait: base.Bold(true),
			Done: base, Alert: base.Bold(true),
			CursorLine: base.Reverse(true), Selection: base.Reverse(true),
			Search: base.Reverse(true), CurSearch: base.Reverse(true),
			Separator: base.Dim(true),
			Bold:      base.Bold(true), Italic: base.Italic(true), Code: base,
			NoColor: true,
		}
	}
	tc := colors >= 1<<24
	fg := func(c rgb256) tcell.Style { return base.Foreground(c.in(tc)) }
	return &Theme{
		Base:       base,
		Fg:         fg(rpText),
		Fg2:        fg(rpSubtle),
		Fg3:        fg(rpMuted),
		Accent:     fg(rpIris),
		Wait:       fg(rpGold),
		Alert:      fg(rpLove),
		Run:        fg(rpFoam),
		Done:       fg(rpPine),
		Peer:       fg(rpRose),
		CursorLine: base.Background(rpHLLow.in(tc)),
		Selection:  base.Background(rpHLMed.in(tc)),
		Search:     base.Background(rpGoldLow.in(tc)),
		CurSearch:  base.Background(rpGold.in(tc)).Foreground(rpBase.in(tc)),
		Separator:  fg(rpOverlay),
		Bold:       fg(rpText).Bold(true),
		Italic:     fg(rpText).Italic(true),
		Code:       fg(rpRose),
		TrueColor:  tc,
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

// stateFailed is a thread whose turn ended in an error, with nothing after
// it. The hub lands it as a State once every client draws it.
const stateFailed model.State = "failed"

// stateGlyph is the one-character state marker used everywhere.
func stateGlyph(s model.State) string {
	switch s {
	case stateFailed:
		return "✗"
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
	case stateFailed:
		return t.Alert
	case model.StateRunning:
		return t.Run
	case model.StateWaiting:
		return t.Wait
	case model.StateResolved:
		return t.Done
	}
	return t.Fg3
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
