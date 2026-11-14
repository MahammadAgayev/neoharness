package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	lua "github.com/yuin/gopher-lua"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// summaryBuf is the window that says how a thread's work, or the
// workspace's, stands: what is done, in progress and needs the person.
const summaryBuf = "neoharness://summary"

// summaryThread and summaryArtifact start the Item of a summary line that
// opens a thread or an artifact.
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

// toggleSummary is :summary: it opens the summary, or goes back from it.
func (a *App) toggleSummary() {
	if a.cur.Buf.Kind == KindSummary {
		a.jumpBack()
		return
	}
	a.openSummary(a.summarySubject())
}

// openSummary shows a summary in the window and tells the hub the person saw
// it, so coming back does not toast the same news again.
func (a *App) openSummary(subject string) {
	b, ok := a.bufs[summaryBuf]
	if !ok {
		b = newBuffer(summaryBuf, KindSummary)
		a.bufs[summaryBuf] = b
	}
	b.Workspace, a.summaryOf = a.workspace, subject
	b.SetRender(func(w int) []Line { return a.summaryLines(subject, w) })
	a.cur.show(b)
	a.cur.Follow = false
	a.mode = modeNormal
	if err := a.c.SeenRecap(a.workspace, subject); err != nil {
		a.notify(err.Error(), a.th.Love)
	}
}

// renderSummary draws the summary again when what it shows changes.
func (a *App) renderSummary() {
	if b, ok := a.bufs[summaryBuf]; ok {
		b.dirty = true
	}
}

// openSummaryItem opens what a summary line links to.
func (a *App) openSummaryItem(item string) {
	if name, ok := strings.CutPrefix(item, summaryThread); ok {
		a.showThread(name)
	} else if name, ok := strings.CutPrefix(item, summaryArtifact); ok {
		a.openArtifact(name)
	}
}

// summaryLines are a summary, w cells wide: a title, then the sections.
func (a *App) summaryLines(subject string, w int) []Line {
	th := a.th
	in := []Span{{" ", th.Text}}
	line := func(spans ...Span) Line { return Line{Prefix: in, Spans: spans} }
	var out []Line
	if subject == model.Coordinator {
		v := a.workspaceView()
		title := []Span{{a.workspace, th.Text}}
		r := v.Recap
		if r != nil && r.Goal != "" {
			title = append(title, Span{" · " + r.Goal, th.Subtle})
		}
		out = append(out, wrap(title, w, in, in)...)
		var counts []Span
		if n := v.Counts[model.StateWaiting]; n > 0 {
			counts = append(counts, Span{fmt.Sprintf("◆ %d", n), th.Gold}, Span{" wait for you", th.Muted})
		}
		if r != nil && r.Failed > 0 {
			if len(counts) > 0 {
				counts = append(counts, Span{" · ", th.Muted})
			}
			counts = append(counts, Span{fmt.Sprintf("✗ %d", r.Failed), th.Love}, Span{" failed", th.Muted})
		}
		if len(counts) > 0 {
			out = append(out, line(counts...))
		}
		if r != nil && len(r.Done)+len(r.Doing)+len(r.Needs) > 0 {
			out = append(out, a.sections(r.Needs, r.Done, r.Doing, w)...)
		} else {
			out = append(out, a.threadRows()...)
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
			out = append(out, wrap([]Span{{names + " edited ", th.Subtle}, {o.Path, th.Text}}, w, in, in)...)
		}
		if !r.At.IsZero() && r.Newer > 0 {
			ended := "1 thread ended since"
			if r.Newer > 1 {
				ended = fmt.Sprintf("%d threads ended since", r.Newer)
			}
			out = append(out, Line{}, line(Span{"as of " + r.At.Local().Format("15:04") + " · " + ended, th.Muted}))
		}
		return out
	}

	v := a.threads[subject]
	out = append(out, line(Span{v.Name, th.Text}, Span{" · " + string(v.State), th.Subtle}))
	r := v.Recap
	if r == nil {
		return append(out, Line{}, line(Span{"no summary yet · :summary! asks for one", th.Muted}))
	}
	if r.Error != "" {
		out = append(out, wrap([]Span{{"✗ " + r.Error, th.Love}}, w, in, in)...)
	}
	done, doing, needs := r.Done, r.Doing, r.Needs
	// Until the thread writes its tasks, its last turn stands in.
	if len(done)+len(doing)+len(needs) == 0 {
		if r.Outcome != "" {
			done = []model.RecapItem{{Text: r.Outcome}}
		}
		if r.Open != "" {
			needs = []model.RecapItem{{Text: r.Open}}
		}
	}
	out = append(out, a.sections(needs, done, doing, w)...)
	var facts []Span
	for i, s := range evidence(r, th) {
		if i > 0 {
			facts = append(facts, Span{" · ", th.Muted})
		}
		facts = append(facts, s...)
	}
	return append(out, Line{}, line(append([]Span{{"last turn  ", th.Muted}}, facts...)...))
}

// sections are a summary's lists, what needs the person first. Each item
// wraps under its glyph; every row of it opens its link.
func (a *App) sections(needs, done, doing []model.RecapItem, w int) []Line {
	th := a.th
	var out []Line
	section := func(head, glyph string, st tcell.Style, items []model.RecapItem) {
		if len(items) == 0 {
			return
		}
		out = append(out, Line{}, Line{Prefix: []Span{{" ", th.Text}}, Spans: []Span{{head, th.Subtle}}})
		for _, it := range items {
			spans := []Span{{it.Text, th.Text}}
			item := ""
			switch {
			case it.Thread != "":
				spans = append(spans, Span{"  → " + it.Thread, th.Muted})
				item = summaryThread + it.Thread
			case it.Artifact != "":
				spans = append(spans, Span{"  ◇ ", th.Foam}, Span{it.Artifact, th.Subtle})
				item = summaryArtifact + it.Artifact
			}
			rows := wrap(spans, w, []Span{{"   " + glyph + " ", st}}, []Span{{"     ", th.Text}})
			for i := range rows {
				rows[i].Item = item
			}
			out = append(out, rows...)
		}
	}
	section("Needs you", "◆", th.Gold, needs)
	section("Done", "✓", th.Pine, done)
	section("In progress", "●", th.Foam, doing)
	return out
}

// threadRows are the open threads, one row each, for a workspace whose
// coordinator wrote no summary yet.
func (a *App) threadRows() []Line {
	var open []model.ThreadView
	for _, n := range a.listOrder() {
		if v := a.threads[n]; v.State != model.StateResolved {
			open = append(open, v)
		}
	}
	if len(open) == 0 {
		return nil
	}
	slices.SortStableFunc(open, func(x, y model.ThreadView) int { return boxRank(x) - boxRank(y) })
	nameW := 0
	for _, t := range open {
		nameW = min(max(nameW, width(t.Name)), 18)
	}
	out := []Line{{}}
	for i, t := range open {
		if i == boxThreads {
			out = append(out, Line{Prefix: []Span{{" ", a.th.Text}}, Spans: []Span{{fmt.Sprintf("  +%d more in the threads list", len(open)-i), a.th.Muted}}})
			break
		}
		l := boxRow(t, a.th, nameW)
		l.Prefix, l.Item = []Span{{"   ", a.th.Text}}, summaryThread+t.Name
		out = append(out, l)
	}
	return out
}

// cameBack toasts news the person has not seen when they come back: the
// thread's in the window, else the workspace's. It does not while they
// type, and not when neoharness.o.summary is false. The hub says what is
// news; this says only when.
func (a *App) cameBack() {
	if a.workspace == "" || a.opt("summary") == lua.LFalse || a.typing() || a.picker != nil {
		return
	}
	subject, name := a.summarySubject(), ""
	switch {
	case subject != model.Coordinator && a.threads[subject].RecapUnseen:
		name = subject
	case a.workspaceView().RecapUnseen:
		subject, name = model.Coordinator, a.workspace
	default:
		return
	}
	a.addToast(&toast{workspace: a.workspace, thread: subject, glyph: Span{"≡", a.th.Pine},
		body: "news in " + name + " · <leader>ns", border: a.th.Pine}, 10*time.Second)
}
