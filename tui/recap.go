package tui

import (
	"fmt"
	"strings"

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
		out = append(out, Line{Prefix: pre, Spans: []Span{{r.Outcome, st}}, Item: v.Name})
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
	if f := r.Failed(); f > 0 {
		ran = append(ran, Span{fmt.Sprintf(", %d failed", f), th.Subtle})
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
	// The newest first: the last commands are most often the checks, and a
	// long row is cut at its end.
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
		ran = append(ran, Span{fmt.Sprintf(" · +%d earlier", more), th.Muted})
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
