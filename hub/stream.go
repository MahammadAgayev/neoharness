package hub

import (
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// streamEvery is how often the reply so far goes out. Faster is more
// updates than a person can read, and each one costs every client a frame.
var streamEvery = 50 * time.Millisecond

// streamText keeps the reply so far and sends the newest of it once per
// streamEvery. Callers hold h.mu.
func (h *Hub) streamText(ws *space, th *thread, text string) {
	if th.quiet || th.deleted {
		return
	}
	th.stream = text
	if th.streamTimer != nil {
		return
	}
	var tm *time.Timer
	tm = time.AfterFunc(streamEvery, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if th.streamTimer != tm {
			return
		}
		th.streamTimer = nil
		if th.stream == "" || th.deleted {
			return
		}
		th.streamed = true
		h.publish(model.Update{Kind: model.UpdateDelta, Workspace: ws.p.Name, Thread: th.t.Name, Text: th.stream})
	})
	th.streamTimer = tm
}

// endStream stops the stream. clear tells clients to drop what they show,
// when no text event will replace it. Callers hold h.mu.
func (h *Hub) endStream(ws *space, th *thread, clear bool) {
	if th.streamTimer != nil {
		th.streamTimer.Stop()
		th.streamTimer = nil
	}
	th.stream = ""
	if clear && th.streamed {
		h.publish(model.Update{Kind: model.UpdateDelta, Workspace: ws.p.Name, Thread: th.t.Name})
	}
	th.streamed = false
}
