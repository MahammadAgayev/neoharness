package tui

import (
	"math"
	"testing"

	"github.com/gdamore/tcell/v2"
)

type trueColorScreen struct{ tcell.SimulationScreen }

func (trueColorScreen) Colors() int { return 1 << 24 }

func luminance(c tcell.Color) float64 {
	r, g, b := c.RGB()
	lin := func(v int32) float64 {
		f := float64(v) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

func contrast(a, b tcell.Color) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

func distance(a, b tcell.Color) float64 {
	ar, ag, ab := a.RGB()
	br, bg, bb := b.RGB()
	return math.Sqrt(float64((ar-br)*(ar-br) + (ag-bg)*(ag-bg) + (ab-bb)*(ab-bb)))
}

func TestThemeRolesReadAtTheirContrastInTrueAnd256Colors(t *testing.T) {
	for _, colors := range []int{1 << 24, 256} {
		th := newTheme(colors)
		bg := rpBase.in(th.TrueColor)
		for name, want := range map[string]struct {
			st  tcell.Style
			min float64
		}{
			"fg": {th.Fg, 12}, "fg-2": {th.Fg2, 6.5}, "fg-3": {th.Fg3, 4.5},
			"accent": {th.Accent, 4.5}, "wait": {th.Wait, 4.5}, "alert": {th.Alert, 4.5},
			"run": {th.Run, 4.5}, "done": {th.Done, 4.5}, "peer": {th.Peer, 4.5},
		} {
			if got := contrast(fgOf(want.st), bg); got < want.min {
				t.Errorf("%d colors: %s is %.1f:1 on the background, want %.1f:1", colors, name, got, want.min)
			}
		}
		// Error, waiting and another session's words must not look alike.
		// tcell's own 256 match had Love and Rose 40 apart; the palette's
		// true colors are 43 apart.
		far := 40.0
		if !th.TrueColor {
			far = 60
		}
		roles := map[string]tcell.Style{"wait": th.Wait, "alert": th.Alert, "peer": th.Peer, "accent": th.Accent}
		for a, sa := range roles {
			for b, sb := range roles {
				if a < b && distance(fgOf(sa), fgOf(sb)) < far {
					t.Errorf("%d colors: %s and %s are too close", colors, a, b)
				}
			}
		}
	}
}

func TestDefaultsLuaColorsAreTheThemeInTrueAnd256Colors(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	for _, s := range []tcell.Screen{sim, trueColorScreen{sim}} {
		a := newApp(s, newFake())
		th := newTheme(s.Colors())
		for name, pair := range map[string][2]tcell.Style{
			"Fg": {a.th.Fg, th.Fg}, "Fg2": {a.th.Fg2, th.Fg2}, "Fg3": {a.th.Fg3, th.Fg3},
			"Accent": {a.th.Accent, th.Accent}, "Wait": {a.th.Wait, th.Wait}, "Alert": {a.th.Alert, th.Alert},
			"Run": {a.th.Run, th.Run}, "Done": {a.th.Done, th.Done}, "Peer": {a.th.Peer, th.Peer},
			"Code": {a.th.Code, th.Code}, "Separator": {a.th.Separator, th.Separator},
			"CursorLine": {a.th.CursorLine, th.CursorLine}, "Selection": {a.th.Selection, th.Selection},
			"Search": {a.th.Search, th.Search}, "CurSearch": {a.th.CurSearch, th.CurSearch},
		} {
			if pair[0] != pair[1] {
				t.Errorf("%d colors: defaults.lua's %s is not the theme's", s.Colors(), name)
			}
		}
	}
}
