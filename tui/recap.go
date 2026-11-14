package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	lua "github.com/yuin/gopher-lua"

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

// recapWords is how many rows each of the thread's words may take at the
// top of its window.
const recapWords = 3

// recapHead is a thread's recap at the top of a window w cells wide: the
// thread's words, wrapped, then what the hub saw, one row each.
func recapHead(r *model.Recap, th *Theme, w int) []Line {
	if r == nil {
		return nil
	}
	label := func(s string) []Span { return []Span{{"  " + pad(s, 9), th.Muted}} }
	row := func(name string, spans ...Span) Line {
		return Line{Prefix: label(name), Spans: spans}
	}
	var out []Line
	words := func(name, text string, st tcell.Style) {
		rows := wrap([]Span{{text, st}}, w, label(name), label(""))
		if len(rows) > recapWords {
			rows = rows[:recapWords]
			rows[recapWords-1].Spans = append(rows[recapWords-1].Spans, Span{" …", th.Muted})
		}
		out = append(out, rows...)
	}
	if r.Asked != "" {
		words("asked", r.Asked, th.Subtle)
	}
	if r.Outcome != "" {
		name := "outcome"
		if r.FromReply {
			name = "reply"
		}
		words(name, r.Outcome, th.Text)
	}
	if r.Error != "" {
		words("failed", r.Error, th.Love)
	}
	if r.Open != "" {
		words("open", r.Open, th.Text)
	}
	// The newest first: the failures and what checked the work as it ended,
	// and a long row is cut at its end.
	var ran []Span
	for i := len(r.Commands) - 1; i >= 0; i-- {
		c := r.Commands[i]
		if len(ran) > 0 {
			ran = append(ran, Span{" · ", th.Muted})
		}
		if c.Failed {
			ran = append(ran, Span{"✗ " + c.Text, th.Subtle})
		} else {
			ran = append(ran, Span{c.Text, th.Muted})
		}
	}
	if more := r.Ran - len(r.Commands); more > 0 {
		ran = append(ran, Span{fmt.Sprintf(" · +%d more", more), th.Muted})
	}
	if len(ran) == 0 {
		ran = []Span{{"no commands", th.Muted}}
	}
	out = append(out, row("ran", ran...))
	var changed []Span
	if len(r.Edited) > 0 {
		changed = append(changed, Span{strings.Join(r.Edited, ", "), th.Muted})
	}
	for _, a := range r.Artifacts {
		if len(changed) > 0 {
			changed = append(changed, Span{" · ", th.Muted})
		}
		changed = append(changed, Span{"◇ ", th.Foam}, Span{a, th.Subtle})
	}
	if len(changed) > 0 {
		out = append(out, row("changed", changed...))
	}
	return out
}

// workspaceLineRows is how many rows each of the coordinator's lines may take
// in the workspace's recap box.
const workspaceLineRows = 2

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

// workspaceBox is the workspace recap in its box, w cells wide: the counts,
// one row per open thread in boxRank order, then the coordinator's lines and
// the files two threads edited. What came since the person last closed the
// box is in Text, so news stands out from what they saw. The hub counts;
// this only lays out.
func workspaceBox(v model.WorkspaceView, threads []model.ThreadView, th *Theme, w int) []Line {
	r := v.Recap
	var failed, newer int
	if r != nil {
		failed, newer = r.Failed, r.Newer
	}
	waits, running := v.Counts[model.StateWaiting], v.Counts[model.StateRunning]
	idle := v.Counts[model.StateIdle] - failed
	if len(threads) == 0 && (r == nil || len(r.Lines) == 0) {
		return nil
	}
	slices.SortStableFunc(threads, func(x, y model.ThreadView) int { return boxRank(x) - boxRank(y) })

	// A strip of one cell per thread shows the state of the whole at a glance.
	var counts []Span
	for _, t := range threads {
		counts = append(counts, Span{"■", boxStyle(t, th)})
	}
	if len(counts) > 0 {
		counts = append(counts, Span{"  ", th.Muted})
	}
	n := len(counts)
	add := func(spans ...Span) {
		if len(counts) > n {
			counts = append(counts, Span{" · ", th.Muted})
		}
		counts = append(counts, spans...)
	}
	if waits > 0 {
		verb := " wait for you"
		if waits == 1 {
			verb = " waits for you"
		}
		add(Span{fmt.Sprintf("◆ %d", waits), th.state(model.StateWaiting)}, Span{verb, th.Muted})
	}
	if failed > 0 {
		add(Span{fmt.Sprintf("✗ %d", failed), th.Love}, Span{" failed", th.Muted})
	}
	if running > 0 {
		add(Span{fmt.Sprintf("● %d", running), th.state(model.StateRunning)}, Span{" running", th.Muted})
	}
	if idle > 0 {
		add(Span{fmt.Sprintf("%d idle", idle), th.Muted})
	}
	out := []Line{{Spans: counts}}

	if len(threads) > 0 {
		out = append(out, Line{})
	}
	nameW := 0
	for _, t := range threads {
		nameW = min(max(nameW, width(t.Name)), 18)
	}
	for i, t := range threads {
		if i == boxThreads {
			out = append(out, Line{Spans: []Span{{fmt.Sprintf("  +%d more in the threads list", len(threads)-i), th.Muted}}})
			break
		}
		out = append(out, boxRow(t, th, nameW))
	}

	if r == nil {
		return out
	}
	if len(r.Lines) > 0 || len(r.Overlaps) > 0 {
		out = append(out, Line{})
	}
	for _, l := range r.Lines {
		rows := wrap([]Span{{l, th.Text}}, w, nil, nil)
		if len(rows) > workspaceLineRows {
			rows = rows[:workspaceLineRows]
			rows[workspaceLineRows-1].Spans = append(rows[workspaceLineRows-1].Spans, Span{" …", th.Muted})
		}
		out = append(out, rows...)
	}
	for _, o := range r.Overlaps {
		names := o.Threads[0] + " and " + o.Threads[1] + " both"
		if n := len(o.Threads); n > 2 {
			names = strings.Join(o.Threads[:n-1], ", ") + " and " + o.Threads[n-1] + " all"
		}
		out = append(out, Line{Spans: []Span{{names + " edited ", th.Subtle}, {o.Path, th.Text}}})
	}
	if len(r.Lines) > 0 && newer > 0 {
		ended := "1 thread ended since"
		if newer > 1 {
			ended = fmt.Sprintf("%d threads ended since", newer)
		}
		out = append(out, Line{Spans: []Span{{"as of " + r.At.Local().Format("15:04") + " · " + ended, th.Muted}}})
	}
	return out
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
		detail = []Span{{plain(v.Progress), th.Muted}}
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

// cameBack shows the recap box when the person comes back to news they have
// not seen. It does not show while they type, nor over a card that waits for
// them, and not when neoharness.o.recap is false. The hub says what is news;
// this says only when.
func (a *App) cameBack() {
	if a.workspace == "" || a.opt("recap") == lua.LFalse || a.typing() || a.picker != nil {
		return
	}
	if title, _, _ := a.card(a.cur); title != "" {
		return
	}
	if s := a.boxSubject(); s != model.Coordinator && a.threads[s].RecapUnseen {
		a.recapBox = s
	} else if a.workspaceView().RecapUnseen {
		a.recapBox = model.Coordinator
	}
}

// typing says the person writes now: a command line, a selection, or a
// draft in Insert. Insert alone is not: the prompt often stays in Insert
// after a message, while the person is away.
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

// boxSubject is whose recap the box shows from the window with focus: the
// thread's in its window, else the workspace's.
func (a *App) boxSubject() string {
	if b := a.cur.Buf; b.Kind == KindChat && a.threads[b.Thread].Recap != nil {
		return b.Thread
	}
	return model.Coordinator
}

// keyAfterAway says a key comes after minutes with none: on a terminal that
// sends no focus events, that is the person coming back.
func (a *App) keyAfterAway() bool {
	now := a.now()
	back := !a.focusKnown && !a.lastKey.IsZero() && now.Sub(a.lastKey) >= model.RecapAway
	a.lastKey = now
	return back
}

// focus is the terminal gaining or losing focus.
func (a *App) focus(in bool) {
	a.focusKnown = true
	if !in {
		a.blurAt = a.now()
		return
	}
	if !a.blurAt.IsZero() && a.now().Sub(a.blurAt) >= model.RecapAway {
		a.cameBack()
	}
	a.blurAt = time.Time{}
}

// openBox shows a recap box: :recap and :recap! open it whether or not it
// holds news.
func (a *App) openBox(subject string) {
	if subject == model.Coordinator && a.workspaceLines(80) == nil || subject != model.Coordinator && a.threads[subject].Recap == nil {
		a.notify("no recap yet", a.th.Muted)
		return
	}
	a.recapBox = subject
}

// closeBox closes the recap box and tells the hub the person saw it, so the
// app does not show the same news again.
func (a *App) closeBox() {
	s := a.recapBox
	a.recapBox = ""
	if err := a.c.SeenRecap(a.workspace, s); err != nil {
		a.notify(err.Error(), a.th.Love)
	}
}

// workspaceLines are the workspace's box, w cells wide.
func (a *App) workspaceLines(w int) []Line {
	var open []model.ThreadView
	for _, n := range a.listOrder() {
		if v := a.threads[n]; v.State != model.StateResolved {
			open = append(open, v)
		}
	}
	return workspaceBox(a.workspaceView(), open, a.th, w)
}

// boxWidth is the widest the box grows, so its rows stay short enough to
// read at a glance.
const boxWidth = 100

// drawBox draws the recap box at the top of the screen, over the windows
// and the toasts: it holds what they say. Its border takes the color of
// what matters most in it.
func (a *App) drawBox(W, H int) {
	a.boxRect = Rect{}
	if a.recapBox == "" {
		return
	}
	bw := min(W-2*margin, boxWidth)
	r := Rect{(W - bw) / 2, 1, bw, H - 2}
	if r.W < 30 || r.H < minTranscript+4 {
		return
	}
	inner := r.W - 4
	var lines []Line
	var title []Span
	var border tcell.Style
	if a.recapBox == model.Coordinator {
		lines = a.workspaceLines(inner)
		title = []Span{{a.workspace, a.th.Text.Bold(true)}}
		if v := a.workspaceView(); v.Recap != nil && v.Recap.Goal != "" {
			title = append(title, Span{" · " + v.Recap.Goal, a.th.Subtle})
		}
		v := a.workspaceView()
		switch {
		case v.Counts[model.StateWaiting] > 0:
			border = a.th.Gold
		case v.Recap != nil && v.Recap.Failed > 0:
			border = a.th.Love
		case v.Counts[model.StateRunning] > 0:
			border = a.th.Foam
		default:
			border = a.th.Pine
		}
	} else {
		v := a.threads[a.recapBox]
		lines = recapHead(v.Recap, a.th, inner+2)
		for i := range lines {
			// recapHead indents its labels for a window; the border does that here.
			if len(lines[i].Prefix) > 0 {
				lines[i].Prefix[0].Text = strings.TrimPrefix(lines[i].Prefix[0].Text, "  ")
			}
		}
		title = []Span{{v.Name, a.th.Text.Bold(true)}, {" · " + string(v.State), a.th.Subtle}}
		border = boxStyle(v, a.th)
	}
	if len(lines) == 0 {
		return
	}
	lines = lines[:min(len(lines), r.H-minTranscript-2)]
	h := len(lines) + 2
	a.box(r.X, r.Y, r.W, h, "", border)
	x := a.put(r.X+2, r.Y, r.X+r.W-4, " ", border)
	for _, sp := range title {
		x = a.put(x, r.Y, r.X+r.W-5, sp.Text, sp.Style)
	}
	a.put(x, r.Y, r.X+r.W-4, " ", border)
	a.put(r.X+r.W-4, r.Y, r.X+r.W-1, " × ", a.th.Subtle)
	for i, l := range lines {
		a.drawCut(r.X+2, r.Y+1+i, r.X+r.W-2, l)
	}
	a.boxRect = Rect{r.X, r.Y, r.W, h}
}
