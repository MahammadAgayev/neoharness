package tui

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
)

func TestANewerBinaryRestartsTheHubOnceTheTurnsEnd(t *testing.T) {
	h := start(t)
	var restarts atomic.Int32
	h.a.hub = Hub{Restart: func() error { restarts.Add(1); return nil }, Version: "new1111"}
	h.hub.version = "old0000"
	h.a.workspaces = []model.WorkspaceView{{Name: "demo", Counts: map[model.State]int{model.StateRunning: 1}}}
	h.a.handle(evResync{})
	h.a.draw()
	if !strings.Contains(h.screen(), "restarts when the turns end") {
		t.Fatalf("a running turn should hold the restart and say so:\n%s", h.screen())
	}
	time.Sleep(50 * time.Millisecond)
	if restarts.Load() != 0 {
		t.Fatal("the hub restarted while a turn ran")
	}
	idle := model.WorkspaceView{Name: "demo", Counts: map[model.State]int{model.StateIdle: 1}}
	h.a.handle(evUpdate{model.Update{Kind: model.UpdateWorkspace, Workspace: "demo", Summary: &idle}})
	for end := time.Now().Add(time.Second); restarts.Load() == 0 && time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
	}
	if restarts.Load() != 1 {
		t.Fatalf("restarts = %d, want 1 once the turn ended", restarts.Load())
	}
	// The same version again: nothing to do.
	h.hub.version = "new1111"
	h.a.handle(evResync{})
	time.Sleep(50 * time.Millisecond)
	if restarts.Load() != 1 {
		t.Fatal("a hub of this version restarted")
	}
}

func TestHubRestartAsksWhileTurnsRun(t *testing.T) {
	h := start(t)
	var restarts atomic.Int32
	h.a.hub = Hub{Restart: func() error { restarts.Add(1); return nil }}
	h.a.workspaces = []model.WorkspaceView{{Name: "demo", Coordinator: model.StateRunning}}
	h.press(":hub restart", "<CR>")
	if !strings.Contains(h.screen(), "the running turn stops with the hub") {
		t.Fatalf("screen:\n%s", h.screen())
	}
	h.press("y")
	for end := time.Now().Add(time.Second); restarts.Load() == 0 && time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
	}
	if restarts.Load() != 1 {
		t.Fatal(":hub restart and y should restart the hub")
	}
}
