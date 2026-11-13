package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/internal/model"
)

// goneHub is a hub whose update stream ends once, with or without a stopped
// update, and then does not answer.
type goneHub struct {
	*fakeClient
	stopped bool
	calls   atomic.Int32
}

func (g *goneHub) Updates(ctx context.Context) (<-chan model.Update, error) {
	if g.calls.Add(1) > 1 {
		return nil, errors.New("no hub")
	}
	ch := make(chan model.Update, 1)
	if g.stopped {
		ch <- model.Update{Kind: model.UpdateStopped}
	}
	close(ch)
	return ch, nil
}

func listenUntilGone(t *testing.T, stopped bool) (restarts int32, screen string) {
	t.Helper()
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(120, 36)
	g := &goneHub{fakeClient: newFake(), stopped: stopped}
	a := newApp(s, g)
	var n atomic.Int32
	a.restart = func() error { n.Add(1); return errors.New("no hub") }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.listen(ctx)
	for deadline := time.Now().Add(3 * time.Second); g.calls.Load() < 2; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("listen did not try again")
		}
	}
	for s.HasPendingEvent() {
		a.handle(s.PollEvent())
	}
	a.draw()
	h := &neoharness{t: t, a: a, s: s, hub: g.fakeClient}
	return n.Load(), h.screen()
}

func TestStoppedHubIsNotStartedAgain(t *testing.T) {
	restarts, screen := listenUntilGone(t, true)
	if restarts != 0 {
		t.Fatalf("a hub stopped on purpose was started again %d times", restarts)
	}
	if !strings.Contains(screen, "the hub stopped") {
		t.Fatalf("screen does not say the hub stopped:\n%s", screen)
	}
}

func TestLostHubIsStartedAgain(t *testing.T) {
	restarts, screen := listenUntilGone(t, false)
	if restarts == 0 {
		t.Fatal("a hub that went away by itself should be started again")
	}
	if !strings.Contains(screen, "lost the hub") {
		t.Fatalf("screen does not say the hub was lost:\n%s", screen)
	}
}
