package hub

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
	"github.com/MahammadAgayev/neoharness/hub/agent"
)

// TestTheReplyStreamsButOnlyTheTextIsKept: the reply so far goes to
// subscribers, merged to one update per streamEvery, and never to the
// transcript. The finished text replaces it.
func TestTheReplyStreamsButOnlyTheTextIsKept(t *testing.T) {
	defer func(d time.Duration) { streamEvery = d }(streamEvery)
	streamEvery = 30 * time.Millisecond
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		so := ""
		for _, w := range strings.Fields("tea is one of the most loved drinks") {
			so = strings.TrimSpace(so + " " + w)
			req.OnEvent(agent.Event{Kind: agent.KindTextDelta, Text: so})
		}
		time.Sleep(60 * time.Millisecond)
		req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: so + "."})
		return agent.Result{Text: so + "."}
	}
	h := newHub(t, f)
	ch, stop := h.Subscribe()
	defer stop()
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "tea?")

	var deltas []string
	timeout := time.After(3 * time.Second)
	for done := false; !done; {
		select {
		case u := <-ch:
			switch {
			case u.Kind == model.UpdateDelta && u.Thread == model.Coordinator:
				deltas = append(deltas, u.Text)
			case u.Kind == model.UpdateEvent && u.Event.Kind == model.EventText:
				done = true
			}
		case <-timeout:
			t.Fatal("no text event")
		}
	}
	if len(deltas) == 0 || len(deltas) >= 8 {
		t.Fatalf("deltas = %q, want a few merged ones", deltas)
	}
	if last := deltas[len(deltas)-1]; last != "tea is one of the most loved drinks" {
		t.Fatalf("last delta = %q, want the whole text so far", last)
	}
	stateIs(t, h, "demo", model.Coordinator, model.StateIdle)
	evs, _ := h.Events("demo", model.Coordinator, 0)
	var texts int
	for _, e := range evs {
		if e.Kind == model.EventText {
			texts++
		}
	}
	if texts != 1 {
		t.Fatalf("transcript has %d texts, want only the finished one", texts)
	}
}

// TestAStreamCutShortIsCleared: a turn that ends before its text says so
// with an empty delta, so no client keeps half a reply.
func TestAStreamCutShortIsCleared(t *testing.T) {
	defer func(d time.Duration) { streamEvery = d }(streamEvery)
	streamEvery = 10 * time.Millisecond
	f := &fake{}
	f.script = func(ctx context.Context, req agent.Request) agent.Result {
		req.OnEvent(agent.Event{Kind: agent.KindTextDelta, Text: "half a"})
		time.Sleep(40 * time.Millisecond)
		return agent.Result{ExitCode: 1, Stderr: "fake: cut short"}
	}
	h := newHub(t, f)
	ch, stop := h.Subscribe()
	defer stop()
	pinned(t, h, "demo")
	_ = h.Send("demo", model.User, model.Coordinator, "go")
	var deltas []string
	timeout := time.After(3 * time.Second)
	for len(deltas) < 2 {
		select {
		case u := <-ch:
			if u.Kind == model.UpdateDelta {
				deltas = append(deltas, u.Text)
			}
		case <-timeout:
			t.Fatalf("deltas = %q, want the text and then a clear", deltas)
		}
	}
	if deltas[0] != "half a" || deltas[1] != "" {
		t.Fatalf("deltas = %q", deltas)
	}
}
