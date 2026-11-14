package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/gdamore/tcell/v2"
	lua "github.com/yuin/gopher-lua"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// summaryThread and summaryArtifact start the Item of a card row that opens
// a thread or an artifact.
const (
	summaryThread   = "\x00thread:"
	summaryArtifact = "\x00artifact:"
)

// summarySubject is whose summary :summary opens from the window with focus:
// the thread's in its chat, else the workspace's.
func (a *App) summarySubject() string {
	if b := a.cur.Buf; b.Kind == KindChat && a.threads[b.Thread].Recap != nil {
		return b.Thread
	}
	return model.Coordinator
}

// toggleSummary is :summary: it opens the card, or closes it.
func (a *App) toggleSummary() {
	if a.summaryOf != "" {
		a.closeSummary()
		return
	}
	a.openSummary(a.summarySubject())
}

// openSummary shows the card of a subject and tells the hub the person saw
// it, so coming back does not show the same news again.
func (a *App) openSummary(subject string) {
	a.summaryOf, a.cardCur, a.cardTop, a.cardG = subject, 0, 0, false
	a.cardNews = map[string]bool{}
	for n, v := range a.threads {
		if v.State == model.StateResolved && v.RecapUnseen {
			a.cardNews[n] = true
		}
	}
	if err := a.c.SeenRecap(a.workspace, subject); err != nil {
		a.notify(err.Error(), a.th.Love)
	}
}

func (a *App) closeSummary() {
	a.summaryOf = ""
}

// cardKey moves in the card while it shows, as in a window: j k G gg <C-d>
// <C-u>, <CR> opens what the row holds, y yanks it, q and Esc close. Other
// keys go on, so : and <leader>ns still work. It reports that it took k.
func (a *App) cardKey(k string) bool {
	n := len(a.cardLines(a.cardWidth()))
	move := func(i int) { a.cardCur = max(0, min(i, n-1)) }
	g := a.cardG
	a.cardG = false
	switch k {
	case "j", "<Down>", "<C-n>":
		move(a.cardCur + 1)
	case "k", "<Up>", "<C-p>":
		move(a.cardCur - 1)
	case "G":
		move(n - 1)
	case "g":
		if g {
			move(0)
		} else {
			a.cardG = true
		}
	case "<C-d>":
		move(a.cardCur + 5)
	case "<C-u>":
		move(a.cardCur - 5)
	case "<CR>":
		a.openCardRow()
	case "y":
		if lines := a.cardLines(a.cardWidth()); a.cardCur < n {
			a.reg = register{text: strings.TrimSpace(lines[a.cardCur].Text()), lines: true}
			a.clip(a.reg.text, 1)
		}
	case "q", "<Esc>":
		a.closeSummary()
	default:
		return false
	}
	return true
}

// openCardRow opens what the card row under the cursor holds and closes
// the card.
func (a *App) openCardRow() {
	lines := a.cardLines(a.cardWidth())
	if a.cardCur >= len(lines) {
		return
	}
	item := lines[a.cardCur].Item
	if name, ok := strings.CutPrefix(item, summaryThread); ok {
		a.closeSummary()
		a.showThread(name)
	} else if name, ok := strings.CutPrefix(item, summaryArtifact); ok {
		a.closeSummary()
		a.openArtifact(name)
	}
}

// cardWindow is the window the card sits in: the chat of its subject, the
// one with focus first, or else the window with focus.
func (a *App) cardWindow() *Window {
	if w := a.cur; w.Buf.Kind == KindChat && w.Buf.Thread == a.summaryOf {
		return w
	}
	return cmp.Or(a.windowOf(a.summaryOf), a.cur)
}

func (a *App) cardWidth() int { return max(a.cardWindow().rect.W-4, 20) }

// cardLines are the rows of the card, w cells wide. The workspace's holds
// the counts and the coordinator's view, then each thread; a thread's holds
// only its own part, one row per item.
func (a *App) cardLines(w int) []Line {
	th := a.th
	if a.summaryOf != model.Coordinator {
		return a.threadPart(a.threads[a.summaryOf], w, true)
	}
	var out []Line
	v := a.workspaceView()
	r := v.Recap
	var counts []Span
	if n := v.Counts[model.StateWaiting]; n > 0 {
		verb := " wait for you"
		if n == 1 {
			verb = " waits for you"
		}
		counts = append(counts, Span{fmt.Sprintf("◆ %d", n), th.Gold}, Span{verb, th.Muted})
	}
	if r != nil && r.Failed > 0 {
		if len(counts) > 0 {
			counts = append(counts, Span{" · ", th.Muted})
		}
		counts = append(counts, Span{fmt.Sprintf("✗ %d", r.Failed), th.Love}, Span{" failed", th.Muted})
	}
	if len(counts) > 0 {
		out = append(out, Line{Spans: counts})
	}
	if r != nil {
		for _, l := range r.Lines {
			out = append(out, wrap([]Span{{l, th.Text}}, w, nil, nil)...)
		}
	}
	var shown []model.ThreadView
	for _, n := range a.listOrder() {
		if v := a.threads[n]; v.State != model.StateResolved || a.cardNews[n] {
			shown = append(shown, v)
		}
	}
	slices.SortStableFunc(shown, func(x, y model.ThreadView) int { return boxRank(x) - boxRank(y) })
	for _, t := range shown {
		out = append(out, Line{})
		out = append(out, a.threadPart(t, w, false)...)
	}
	if r == nil {
		return out
	}
	if len(r.Overlaps) > 0 {
		out = append(out, Line{})
	}
	for _, o := range r.Overlaps {
		names := o.Threads[0] + " and " + o.Threads[1] + " both"
		if n := len(o.Threads); n > 2 {
			names = strings.Join(o.Threads[:n-1], ", ") + " and " + o.Threads[n-1] + " all"
		}
		out = append(out, wrap([]Span{{names + " edited ", th.Subtle}, {o.Path, th.Text}}, w, nil, nil)...)
	}
	if !r.At.IsZero() && r.Newer > 0 {
		ended := "1 thread ended since"
		if r.Newer > 1 {
			ended = fmt.Sprintf("%d threads ended since", r.Newer)
		}
		out = append(out, Line{Spans: []Span{{"as of " + r.At.Local().Format("15:04") + " · " + ended, th.Muted}}})
	}
	return out
}

// threadPart is a thread in plain words: what it is for, then what needs
// the person, what failed, is done and in progress. In the workspace's card
// a heading names it, each list takes one row, and every row opens the
// thread; in its own card each item takes a row that opens what holds it.
func (a *App) threadPart(v model.ThreadView, w int, own bool) []Line {
	th := a.th
	r := v.Recap
	if r == nil {
		r = &model.Recap{}
	}
	var out []Line
	goal := cmp.Or(r.Goal, r.Asked)
	open := summaryThread + v.Name
	if own {
		open = ""
		if goal != "" {
			out = append(out, wrap([]Span{{goal, th.Subtle}}, w, nil, nil)...)
		}
	} else {
		spans := []Span{{v.Name, th.Text}}
		if goal != "" {
			spans = append(spans, Span{"  " + goal, th.Subtle})
		}
		out = append(out, wrap(spans, w, []Span{{stateMark(v) + " ", boxStyle(v, th)}}, []Span{{"  ", th.Text}})...)
	}
	done, doing, needs := r.Done, r.Doing, r.Needs
	// Until the thread writes its summary, its last turn stands in.
	if len(done)+len(doing)+len(needs) == 0 {
		if r.Outcome != "" {
			done = []model.RecapItem{{Text: r.Outcome}}
		}
		if r.Open != "" {
			needs = []model.RecapItem{{Text: r.Open}}
		}
	}
	switch {
	case v.State != model.StateWaiting:
	case v.Permission != nil:
		needs = append([]model.RecapItem{{Text: "permission · " + v.Permission.Tool + " " + flatten(v.Permission.Detail)}}, needs...)
	case v.Waiting == "paused":
		needs = append([]model.RecapItem{{Text: "paused"}}, needs...)
	case v.Waiting != "":
		needs = append([]model.RecapItem{{Text: "asks: " + flatten(v.Waiting)}}, needs...)
	}
	indent := "    "
	if own {
		indent = ""
	}
	list := func(label string, st tcell.Style, items []model.RecapItem) {
		if len(items) == 0 {
			return
		}
		head := []Span{{indent + pad(label, 9) + "  ", st}}
		rest := []Span{{indent + strings.Repeat(" ", 11), th.Text}}
		if !own {
			var spans []Span
			for i, it := range items {
				if i > 0 {
					spans = append(spans, Span{" · ", th.Muted})
				}
				spans = append(spans, itemSpans(it, " ", th)...)
			}
			for _, l := range wrap(spans, w, head, rest) {
				l.Item = open
				out = append(out, l)
			}
			return
		}
		for i, it := range items {
			p := rest
			if i == 0 {
				p = head
			}
			item := ""
			switch {
			case it.Thread != "":
				item = summaryThread + it.Thread
			case it.Artifact != "":
				item = summaryArtifact + it.Artifact
			}
			for j, l := range wrap(itemSpans(it, "  ", th), w, p, rest) {
				if j == 0 {
					l.Prefix = p
				}
				l.Item = item
				out = append(out, l)
			}
		}
	}
	list("needs you", th.Gold, needs)
	if r.Error != "" {
		list("failed", th.Love, []model.RecapItem{{Text: r.Error}})
	}
	list("done", th.Pine, done)
	list("in progress", th.Foam, doing)
	return out
}

// itemSpans are an item and what holds it, after sep.
func itemSpans(it model.RecapItem, sep string, th *Theme) []Span {
	spans := []Span{{it.Text, th.Text}}
	switch {
	case it.Thread != "":
		spans = append(spans, Span{sep + "→ " + it.Thread, th.Muted})
	case it.Artifact != "":
		spans = append(spans, Span{sep + "◇ ", th.Foam}, Span{it.Artifact, th.Subtle})
	}
	return spans
}

// stateMark is a thread's state in the card: it waits, failed, runs, came
// out, or nothing yet.
func stateMark(v model.ThreadView) string {
	switch {
	case v.State == model.StateWaiting:
		return "◆"
	case v.Recap != nil && v.Recap.Error != "":
		return "✗"
	case v.State == model.StateRunning:
		return "●"
	case v.Recap != nil:
		return "✓"
	}
	return stateGlyph(v.State)
}

// cameBack shows the card when the person comes back to news they have not
// seen: the thread's in the window, else the workspace's. It does not show
// while they type, nor over a card that waits for them, and not when
// neoharness.o.summary is false. The hub says what is news; this says only
// when.
func (a *App) cameBack() {
	if a.workspace == "" || a.opt("summary") == lua.LFalse || a.typing() || a.picker != nil || a.summaryOf != "" {
		return
	}
	if title, _, _ := a.card(a.cur); title != "" {
		return
	}
	if s := a.summarySubject(); s != model.Coordinator && a.threads[s].RecapUnseen {
		a.openSummary(s)
	} else if a.workspaceView().RecapUnseen {
		a.openSummary(model.Coordinator)
	}
}

// minTranscript is the fewest rows of a window the card leaves uncovered.
const minTranscript = 4

// drawCard draws the summary card on the prompt of its window, where the
// person looks, over the windows: a border in the color of what matters
// most, a background of its own so it stands out from the chat, and a
// cursor row. It scrolls when its rows do not fit.
func (a *App) drawCard() {
	a.cardRect = Rect{}
	if a.summaryOf == "" {
		return
	}
	w := a.cardWindow()
	r := w.rect
	if r.W < 30 || w.boxEnd-r.Y < minTranscript+3 {
		return
	}
	lines := a.cardLines(r.W - 4)
	var title []Span
	var border tcell.Style
	if a.summaryOf == model.Coordinator {
		v := a.workspaceView()
		title = []Span{{a.workspace, a.th.Text.Bold(true)}}
		if v.Recap != nil && v.Recap.Goal != "" {
			title = append(title, Span{" · " + v.Recap.Goal, a.th.Subtle})
		}
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
		v := a.threads[a.summaryOf]
		title = []Span{{v.Name, a.th.Text.Bold(true)}, {" · " + string(v.State), a.th.Subtle}}
		border = boxStyle(v, a.th)
	}
	if len(lines) == 0 {
		lines = []Line{{Spans: []Span{{"no summary yet · :summary! asks for one", a.th.Muted}}}}
	}
	a.cardCur = max(0, min(a.cardCur, len(lines)-1))
	rows := min(len(lines), w.boxEnd-r.Y-minTranscript-2)
	a.cardTop = max(0, min(a.cardTop, a.cardCur), a.cardCur-rows+1)
	h := rows + 2
	r.Y = w.boxEnd - h
	_, bg, _ := a.th.CursorLine.Decompose()
	_, sel, _ := a.th.Selection.Decompose()
	a.box(r.X, r.Y, r.W, h, "", border.Background(bg))
	for y := r.Y + 1; y < r.Y+h-1; y++ {
		a.fill(r.X+1, y, r.X+r.W-1, a.th.Base.Background(bg))
	}
	x := a.put(r.X+2, r.Y, r.X+r.W-4, " ", border.Background(bg))
	for _, sp := range title {
		x = a.put(x, r.Y, r.X+r.W-5, sp.Text, sp.Style.Background(bg))
	}
	a.put(x, r.Y, r.X+r.W-4, " ", border.Background(bg))
	a.put(r.X+r.W-4, r.Y, r.X+r.W-1, " × ", a.th.Subtle.Background(bg))
	for i := range rows {
		n := a.cardTop + i
		rbg := bg
		if n == a.cardCur {
			rbg = sel
			a.fill(r.X+1, r.Y+1+i, r.X+r.W-1, a.th.Base.Background(sel))
		}
		a.drawCut(r.X+2, r.Y+1+i, r.X+r.W-2, onBackground(lines[n], rbg))
	}
	a.cardRect = Rect{r.X, r.Y, r.W, h}
}

// onBackground is a line drawn on a background color.
func onBackground(l Line, bg tcell.Color) Line {
	set := func(spans []Span) []Span {
		out := make([]Span, len(spans))
		for i, s := range spans {
			out[i] = Span{s.Text, s.Style.Background(bg)}
		}
		return out
	}
	l.Prefix, l.Spans = set(l.Prefix), set(l.Spans)
	return l
}
