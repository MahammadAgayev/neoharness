package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
)

func delta(thread, text string) model.Update {
	return model.Update{Kind: model.UpdateDelta, Workspace: "demo", Thread: thread, Text: text}
}

func TestAReplyStreamsInAndTheFinishedTextReplacesIt(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.setThread(model.Coordinator, func(v *model.ThreadView) { v.State = model.StateRunning })
	h.see("working")
	h.a.apply(delta(model.Coordinator, "Hello wor"))
	h.a.draw()
	h.see("Hello wor")
	if strings.Contains(h.screen(), "working") {
		t.Fatal("streaming text replaces the working line")
	}
	h.a.apply(delta(model.Coordinator, "Hello world, the **plan**"))
	h.a.draw()
	h.see("Hello world, the plan")
	e := model.Event{Kind: model.EventText, Text: "Hello world, the **plan** is done."}
	h.a.apply(model.Update{Kind: model.UpdateEvent, Workspace: "demo", Thread: model.Coordinator, Event: &e})
	h.a.draw()
	if n := strings.Count(h.screen(), "Hello world"); n != 1 {
		t.Fatalf("the finished text should replace the stream, shown %d times:\n%s", n, h.screen())
	}
	h.see("Hello world, the plan is done.")
}

func TestAnEmptyDeltaDropsTheStream(t *testing.T) {
	h := start(t)
	h.openDemo()
	h.a.apply(delta(model.Coordinator, "half a thou"))
	h.a.apply(delta("", ""))
	h.a.apply(model.Update{Kind: model.UpdateDelta, Workspace: "other", Thread: model.Coordinator, Text: "elsewhere"})
	h.a.draw()
	h.see("half a thou")
	h.a.apply(delta(model.Coordinator, ""))
	h.a.draw()
	if strings.Contains(h.screen(), "half a thou") || strings.Contains(h.screen(), "elsewhere") {
		t.Fatalf("an empty delta drops the stream:\n%s", h.screen())
	}
}

func TestStreamingDrawsWithinTheFrameBudget(t *testing.T) {
	c := startClocked(t)
	c.h.openDemo()
	var events []timer
	text := ""
	for i := 0; i < 100; i++ {
		text += "word "
		events = append(events, timer{c.now.Add(time.Duration(i) * 50 * time.Millisecond), evUpdate{delta(model.Coordinator, text)}, i + 1})
	}
	c.h.a.frames = 0
	c.run(5*time.Second, events)
	if n := c.h.a.frames; n > 40 {
		t.Fatalf("20 deltas a second for 5 s drew %d frames, want at most 40", n)
	}
	c.h.see("word word")
}
