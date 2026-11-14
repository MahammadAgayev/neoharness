package tui

import (
	"cmp"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// recapRows are a thread's recap under its row in the threads pane: what came
// out, what the hub saw, and what is open, one row each, so a long outcome
// does not push the facts off a narrow pane. A running thread shows its live
// report instead.
func recapRows(v model.ThreadView, th *Theme) []Line {
	r := v.Recap
	if r == nil || v.State == model.StateRunning || v.State == model.StateResolved {
		return nil
	}
	pre := []Span{{"   ", th.Text}}
	var out []Line
	switch {
	case r.Error != "":
		out = append(out, Line{Prefix: pre, Spans: []Span{{"✗ " + r.Error, th.Love}}, Item: v.Name})
	case r.Outcome != "":
		st := th.Muted
		if r.FromReply {
			st = st.Italic(true)
		}
		out = append(out, Line{Prefix: pre, Spans: []Span{{cmp.Or(r.Headline, r.Outcome), st}}, Item: v.Name})
	}
	var facts []Span
	for i, s := range evidence(r, th) {
		if i > 0 {
			facts = append(facts, Span{" · ", th.Muted})
		}
		facts = append(facts, s...)
	}
	out = append(out, Line{Prefix: pre, Spans: facts, Item: v.Name})
	if r.Open != "" {
		out = append(out, Line{Prefix: pre, Spans: []Span{{"open: ", th.Muted}, {r.Open, th.Subtle}}, Item: v.Name})
	}
	return out
}

// evidence is what the hub saw the turn do, in short. "no commands" stays:
// it tells a claim that nothing checked.
func evidence(r *model.Recap, th *Theme) [][]Span {
	ran := []Span{{counted(r.Ran, "command"), th.Muted}}
	if r.Ran == 0 {
		ran[0].Text = "no commands"
	}
	if r.Failed > 0 {
		ran = append(ran, Span{fmt.Sprintf(", %d failed", r.Failed), th.Subtle})
	}
	out := [][]Span{ran}
	if n := len(r.Edited); n > 0 {
		out = append(out, []Span{{counted(n, "file") + " edited", th.Muted}})
	}
	for _, a := range r.Artifacts {
		out = append(out, []Span{{"◇ ", th.Foam}, {a, th.Subtle}})
	}
	return out
}

func counted(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// boxThreads is how many threads the workspace's box lists before it counts
// the rest.
const boxThreads = 8

// boxRank orders the threads in the workspace's box: what needs the person,
// what failed, what came out that they have not seen, what still runs, and
// the rest.
func boxRank(v model.ThreadView) int {
	switch {
	case v.State == model.StateWaiting:
		return 0
	case v.Recap != nil && v.Recap.Error != "":
		return 1
	case v.State == model.StateIdle && v.RecapUnseen:
		return 2
	case v.State == model.StateRunning:
		return 3
	}
	return 4
}

// boxRow is one thread in the workspace's box: its state and its headline.
func boxRow(v model.ThreadView, th *Theme, nameW int) Line {
	news := th.Muted
	if v.RecapUnseen {
		news = th.Text
	}
	glyph, detail := stateGlyph(v.State), []Span(nil)
	switch r := v.Recap; {
	case v.State == model.StateWaiting:
		switch {
		case v.Permission != nil:
			detail = []Span{{"permission · " + v.Permission.Tool + " ", th.Gold}, {flatten(v.Permission.Detail), th.Text}}
		case v.Waiting == "paused":
			detail = []Span{{"paused", th.Gold}}
		default:
			detail = []Span{{"asks: ", th.Gold}, {flatten(v.Waiting), th.Text}}
		}
	case r != nil && r.Error != "":
		glyph, detail = "✗", []Span{{r.Error, news}}
	case v.State == model.StateRunning:
		// The last recap is old news while a new turn runs.
	case r != nil:
		glyph, detail = "✓", []Span{{cmp.Or(r.Headline, r.Outcome), news}}
	}
	name := th.Subtle
	if v.RecapUnseen || v.State == model.StateWaiting {
		name = th.Text
	}
	return Line{Spans: append([]Span{{glyph + " ", boxStyle(v, th)}, {pad(truncate(v.Name, nameW), nameW+2), name}}, detail...)}
}

// boxStyle is the color of a thread's state in the box: Gold waits for the
// person, Love failed, Foam runs, Pine came out.
func boxStyle(v model.ThreadView, th *Theme) tcell.Style {
	switch {
	case v.State == model.StateWaiting:
		return th.Gold
	case v.Recap != nil && v.Recap.Error != "":
		return th.Love
	case v.State == model.StateRunning:
		return th.Foam
	case v.Recap != nil:
		return th.Pine
	}
	return th.Muted
}

func (a *App) typing() bool {
	switch a.mode {
	case modeNormal:
		return false
	case modeInsert:
		b := a.cur.Buf
		return b.Edit != nil || b.Prompt != nil && len(b.Prompt.Text) > 0
	}
	return true
}

// focus, and tmux may report focus once and never again.
func (a *App) keyAfterAway() bool {
	now := a.now()
	back := !a.lastKey.IsZero() && now.Sub(a.lastKey) >= model.RecapAway
	a.lastKey = now
	return back
}

// focus is the terminal gaining or losing focus.
func (a *App) focus(in bool) {
	if !in {
		a.blurAt = a.now()
		return
	}
	if !a.blurAt.IsZero() && a.now().Sub(a.blurAt) >= model.RecapAway {
		a.cameBack()
	}
	// The person is here now; their next key is not a second return.
	a.blurAt, a.lastKey = time.Time{}, a.now()
}
