package tui

import (
	"fmt"
	"strings"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// recapRows are a thread's recap under its row in the threads pane: what came
// out and what the hub saw, then what is open. A running thread shows its
// live report instead.
func recapRows(v model.ThreadView, th *Theme) []Line {
	r := v.Recap
	if r == nil || v.State == model.StateRunning || v.State == model.StateResolved {
		return nil
	}
	pre := []Span{{"   ", th.Text}}
	var first []Span
	switch {
	case r.Error != "":
		first = append(first, Span{"✗ " + r.Error, th.Love})
	case r.Outcome != "":
		st := th.Muted
		if r.FromReply {
			st = st.Italic(true)
		}
		first = append(first, Span{r.Outcome, st})
	}
	for i, s := range evidence(r, th) {
		if i > 0 || len(first) > 0 {
			first = append(first, Span{" · ", th.Muted})
		}
		first = append(first, s...)
	}
	out := []Line{{Prefix: pre, Spans: first, Item: v.Name}}
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

// recapHead is a thread's recap at the top of its window, one row per part:
// the thread's words, then what the hub saw.
func recapHead(r *model.Recap, th *Theme) []Line {
	if r == nil {
		return nil
	}
	row := func(label string, spans ...Span) Line {
		return Line{Prefix: []Span{{"  " + pad(label, 9), th.Muted}}, Spans: spans}
	}
	var out []Line
	if r.Asked != "" {
		out = append(out, row("asked", Span{r.Asked, th.Subtle}))
	}
	if r.Outcome != "" {
		label := "outcome"
		if r.FromReply {
			label = "reply"
		}
		out = append(out, row(label, Span{r.Outcome, th.Text}))
	}
	if r.Error != "" {
		out = append(out, row("failed", Span{r.Error, th.Love}))
	}
	if r.Open != "" {
		out = append(out, row("open", Span{r.Open, th.Text}))
	}
	var ran []Span
	for i, c := range r.Commands {
		if i > 0 {
			ran = append(ran, Span{" · ", th.Muted})
		}
		if c.Failed {
			ran = append(ran, Span{"✗ " + c.Text, th.Subtle})
		} else {
			ran = append(ran, Span{c.Text, th.Muted})
		}
	}
	if more := r.Ran - len(r.Commands); more > 0 {
		ran = append(ran, Span{fmt.Sprintf(" · +%d", more), th.Muted})
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
