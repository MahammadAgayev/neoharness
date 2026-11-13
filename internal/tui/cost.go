package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/MahammadAgayev/neoharness/internal/model"
)

// costRow starts the Item of a row of :cost that opens a thread; the
// workspace and the thread follow, apart by a NUL.
const costRow = "\x00cost\x00"

const costBuf = "neoharness://cost"

// coster is a client that adds up the cost; the hub's client does.
type coster interface {
	Cost(workspace string) (model.CostReport, error)
}

// showCost fills the cost buffer with what the open workspace cost, or
// every workspace with all, and shows it in the window.
func (a *App) showCost(all bool) {
	c, ok := a.c.(coster)
	if !ok {
		a.notify("this hub cannot add up the cost", a.th.Love)
		return
	}
	ws := a.workspace
	if all || ws == "" {
		ws, all = "", true
	}
	sp, err := c.Cost(ws)
	if err != nil {
		a.notify(err.Error(), a.th.Love)
		return
	}
	b, ok := a.bufs[costBuf]
	if !ok {
		b = newBuffer(costBuf, KindCost)
		a.bufs[costBuf] = b
	}
	b.Workspace = ws
	b.SetLines(a.costLines(sp, all))
	a.cur.show(b)
	a.cur.Follow = false
}

func money(n float64) string {
	if n > 0 && n < 0.01 {
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", n)
}

func (a *App) costLines(sp model.CostReport, all bool) []Line {
	th := a.th
	in := []Span{{" ", th.Text}}
	line := func(spans ...Span) Line { return Line{Prefix: in, Spans: spans} }
	head := func(s string) []Line { return []Line{{}, line(Span{s, th.Muted})} }
	if sp.Total == 0 {
		return []Line{line(Span{"nothing spent yet", th.Muted})}
	}
	fortnight := 0.0
	for _, d := range sp.Days {
		fortnight += d.Cost
	}
	out := []Line{line(
		Span{money(sp.Total), th.Text.Bold(true)}, Span{" in all    ", th.Subtle},
		Span{money(sp.Today), th.Text.Bold(true)}, Span{" today    ", th.Subtle},
		Span{money(fortnight), th.Text.Bold(true)}, Span{" last 14 days", th.Subtle},
	)}

	// The days as one row of bars, eighths of a cell high, today last.
	out = append(out, head("last 14 days")...)
	top := 0.0
	for _, d := range sp.Days {
		top = max(top, d.Cost)
	}
	const levels = " ▁▂▃▄▅▆▇█"
	var bars, names []Span
	for i, d := range sp.Days {
		st := th.Iris
		if i < len(sp.Days)-1 {
			st = th.Subtle
		}
		n := 0
		if top > 0 && d.Cost > 0 {
			n = max(1, int(d.Cost/top*8+0.5))
		}
		bars = append(bars, Span{strings.Repeat(string([]rune(levels)[n]), 2) + " ", st})
		day, _ := time.ParseInLocation("2006-01-02", d.Name, time.Local)
		names = append(names, Span{day.Format("Mon")[:2] + " ", th.Muted})
	}
	out = append(out, line(bars...), line(names...))

	bar := func(parts []model.CostPart, prefix bool) []Line {
		top, nameW := 0.0, 0
		label := func(p model.CostPart) string {
			if prefix && p.Workspace != "" {
				return p.Workspace + " · " + p.Name
			}
			return p.Name
		}
		for _, p := range parts {
			top, nameW = max(top, p.Cost), max(nameW, width(label(p)))
		}
		var lines []Line
		for _, p := range parts {
			n := 1
			if top > 0 {
				n = max(1, int(p.Cost/top*24+0.5))
			}
			l := line(Span{pad(label(p), nameW+2), th.Text}, Span{pad(strings.Repeat("━", n), 26), th.Iris}, Span{money(p.Cost), th.Subtle})
			if p.Workspace != "" {
				l.Item = costRow + p.Workspace + "\x00" + p.Name
			}
			lines = append(lines, l)
		}
		return lines
	}
	if all {
		out = append(out, head("by workspace")...)
		ws := make([]model.CostPart, len(sp.Workspaces))
		for i, p := range sp.Workspaces {
			ws[i] = model.CostPart{Workspace: p.Name, Name: model.Coordinator, Cost: p.Cost}
		}
		// A workspace row opens its coordinator, so it shows by its name.
		for i, l := range bar(ws, false) {
			l.Spans[0].Text = pad(sp.Workspaces[i].Name, width(l.Spans[0].Text))
			out = append(out, l)
		}
		out = append(out, head("threads that cost the most")...)
		out = append(out, bar(sp.Threads[:min(len(sp.Threads), 10)], true)...)
	} else {
		out = append(out, head("by thread")...)
		out = append(out, bar(sp.Threads, false)...)
	}
	if len(sp.Models) > 0 {
		out = append(out, head("by model")...)
		models := make([]model.CostPart, len(sp.Models))
		for i, p := range sp.Models {
			models[i] = model.CostPart{Name: shortModel(p.Name), Cost: p.Cost}
		}
		out = append(out, bar(models, false)...)
	}
	if len(sp.Top) > 0 {
		out = append(out, head("turns that cost the most")...)
		nameW := 0
		name := func(t model.TurnCost) string {
			if all {
				return t.Workspace + " · " + t.Thread
			}
			return t.Thread
		}
		for _, t := range sp.Top {
			nameW = max(nameW, width(name(t)))
		}
		for _, t := range sp.Top {
			l := line(
				Span{pad(name(t), nameW+2), th.Text},
				Span{t.At.Local().Format("Jan 2 15:04") + "  ", th.Muted},
				Span{pad(shortModel(t.Model), 12), th.Muted},
				Span{money(t.Cost), th.Subtle},
			)
			l.Item = costRow + t.Workspace + "\x00" + t.Thread
			out = append(out, l)
		}
	}
	if !sp.Since.IsZero() {
		out = append(out, Line{}, line(Span{"days, models and turns since " + sp.Since.Local().Format("Jan 2") + ", when the log began", th.Muted}))
	}
	return out
}

// openCostRow opens the thread a row of :cost names, in its workspace.
func (a *App) openCostRow(item string) {
	ws, thread, _ := strings.Cut(strings.TrimPrefix(item, costRow), "\x00")
	if ws != a.workspace {
		a.openWorkspace(ws)
		if a.workspace != ws {
			return
		}
	}
	a.showThread(thread)
}
