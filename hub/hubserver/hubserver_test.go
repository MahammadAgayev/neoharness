package hubserver

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/MahammadAgayev/neoharness/api/hubapi"
	"github.com/MahammadAgayev/neoharness/api/model"
	"github.com/MahammadAgayev/neoharness/hub"
	"github.com/MahammadAgayev/neoharness/hub/agent"
	"github.com/MahammadAgayev/neoharness/hub/workspace"
)

type echo struct{}

func (echo) Name() string { return "echo" }
func (echo) Run(ctx context.Context, req agent.Request) (agent.Result, error) {
	text := "echo: " + req.Prompt
	for _, im := range req.Images {
		text += " +" + im.Type
	}
	req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: text})
	return agent.Result{Text: "ok"}, nil
}

// TestClientServerRoundTrip drives the API the way the TUI does: over a real
// unix socket, with the update stream open.
func TestClientServerRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("NEOHARNESS_HOME", home)
	h, err := hub.New(hub.Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return echo{} }})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	sock := filepath.Join(home, "hub.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: Handler(h, hubapi.Info{}, func() {})}
	go srv.Serve(ln)
	defer srv.Close()

	c := hubapi.New(sock)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates, err := c.Updates(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := c.CreateWorkspace("demo", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateWorkspace("demo", ""); err == nil || err.Error() != `workspace "demo" already exists` {
		t.Fatalf("duplicate create error = %v", err)
	}
	if err := c.OpenThread("demo", "api-tests", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Send("demo", "api-tests", "hello"); err != nil {
		t.Fatal(err)
	}

	timeout := time.After(3 * time.Second)
	for got := false; !got; {
		select {
		case u := <-updates:
			got = u.Kind == model.UpdateEvent && u.Thread == "api-tests" && u.Event.Kind == model.EventText && u.Event.Text == "echo: hello"
		case <-timeout:
			t.Fatal("no echo over the update stream")
		}
	}

	if err := c.Send("demo", "api-tests", "see [image 1]", []byte("\x89PNG\r\n\x1a\n")); err != nil {
		t.Fatal(err)
	}
	for got := false; !got; {
		select {
		case u := <-updates:
			got = u.Kind == model.UpdateEvent && u.Event.Kind == model.EventText && u.Event.Text == "echo: see [image 1] +image/png"
		case <-timeout:
			t.Fatal("the image did not reach the agent")
		}
	}

	ps, err := c.Workspaces()
	if err != nil || len(ps) != 1 || ps[0].Name != "demo" {
		t.Fatalf("workspaces = %+v, %v", ps, err)
	}
	vs, err := c.Threads("demo")
	if err != nil || len(vs) != 2 || vs[0].Name != model.Coordinator {
		t.Fatalf("threads = %+v, %v", vs, err)
	}
	evs, err := c.Events("demo", "api-tests", 0)
	if err != nil || len(evs) < 2 {
		t.Fatalf("events = %+v, %v", evs, err)
	}
	if err := c.Resolve("demo", "api-tests"); err != nil {
		t.Fatal(err)
	}
	if err := c.Reopen("demo", "api-tests"); err != nil {
		t.Fatal(err)
	}
	if err := c.SendNow("demo", "api-tests", "now"); err != nil {
		t.Fatal(err)
	}
	for got := false; !got; {
		select {
		case u := <-updates:
			got = u.Kind == model.UpdateEvent && u.Thread == "api-tests" && u.Event.Kind == model.EventText && u.Event.Text == "echo: now"
		case <-timeout:
			t.Fatal("no echo for a message sent now")
		}
	}
	if _, err := h.WriteArtifact("demo", "api-tests", "plan", "# Plan\n\nold\n"); err != nil {
		t.Fatal(err)
	}
	if a, text, err := c.EditArtifact("demo", "plan", "# Plan\n\nnew\n", "# Plan\n\nold\n", 1, false); err != nil || a.Version != 2 || !a.UserEdit || text != "# Plan\n\nnew\n" {
		t.Fatalf("edit = %+v, %q, %v", a, text, err)
	}
	if _, _, err := c.EditArtifact("demo", "plan", "# Plan\n\nagain\n", "# Plan\n\nold\n", 1, false); err == nil {
		t.Fatal("an edit of the same line from an old version should be refused")
	}
	if _, text, _ := c.Artifact("demo", "plan"); text != "# Plan\n\nnew\n" {
		t.Fatalf("artifact text = %q", text)
	}
	dir := t.TempDir()
	if err := c.SetFolder("demo", dir); err != nil {
		t.Fatal(err)
	}
	if ps, _ := c.Workspaces(); ps[0].Pin != dir {
		t.Fatalf("pin = %q", ps[0].Pin)
	}
	if err := c.DeleteWorkspace("demo"); err != nil {
		t.Fatal(err)
	}
	if ps, _ := c.Workspaces(); len(ps) != 0 {
		t.Fatalf("workspaces after delete = %+v", ps)
	}
}

// TestStopRequest checks what `neoharness hub stop` relies on: the request
// reaches the process, and listeners get a last update that says the hub
// stopped on purpose, so they do not start a new one.
func TestStopRequest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("NEOHARNESS_HOME", home)
	h, err := hub.New(hub.Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return echo{} }})
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(home, "hub.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	asked := make(chan struct{}, 1)
	started := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	srv := &http.Server{Handler: Handler(h, hubapi.Info{Version: "abc", PID: 42, Started: started}, func() { asked <- struct{}{} })}
	go srv.Serve(ln)
	defer srv.Close()

	c := hubapi.New(sock)
	info, err := c.Info()
	if err != nil || info.Version != "abc" || info.PID != 42 || !info.Started.Equal(started) {
		t.Fatalf("info = %+v, %v", info, err)
	}
	updates, err := c.Updates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.StopHub(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-asked:
	case <-time.After(time.Second):
		t.Fatal("the stop request did not reach the process")
	}
	h.Close()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case u, ok := <-updates:
			if !ok {
				t.Fatal("the stream ended without a stopped update")
			}
			if u.Kind != model.UpdateStopped {
				continue
			}
			if _, ok := <-updates; ok {
				t.Fatal("the stream should end after the stopped update")
			}
			return
		case <-timeout:
			t.Fatal("no stopped update")
		}
	}
}

// TestWaitsOverTheAPI: the waits list, seen and dismiss reach the hub, and
// the list comes on the update stream when it changes.
func TestWaitsOverTheAPI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("NEOHARNESS_HOME", home)
	h, err := hub.New(hub.Config{Factory: func(string, workspace.Workspace, workspace.Thread) agent.Agent { return echo{} }})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	sock := filepath.Join(home, "hub.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: Handler(h, hubapi.Info{}, func() {})}
	go srv.Serve(ln)
	defer srv.Close()
	c := hubapi.New(sock)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates, err := c.Updates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CreateWorkspace("demo", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if ws, err := c.Waits(); err != nil || len(ws) != 0 {
		t.Fatalf("waits = %+v, %v", ws, err)
	}
	if err := h.Notify("demo", model.Coordinator, "pick a database", true); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(3 * time.Second)
	var w model.Wait
	for w.ID == "" {
		select {
		case u := <-updates:
			if u.Kind == model.UpdateWaits && len(u.Waits) == 1 {
				w = u.Waits[0]
			}
		case <-timeout:
			t.Fatal("no waits update")
		}
	}
	if ws, _ := c.Waits(); len(ws) != 1 || ws[0].ID != w.ID || ws[0].Kind != model.WaitNotice {
		t.Fatalf("waits = %+v", ws)
	}
	if err := c.Seen("demo", model.Coordinator, time.Now()); err != nil {
		t.Fatal(err)
	}
	if vs, _ := c.Threads("demo"); vs[0].SeenAt.IsZero() || vs[0].Level != model.LevelNeedsYou {
		t.Fatalf("after seen = %+v", vs[0])
	}
	if err := c.Dismiss("demo", model.Coordinator, w.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.Dismiss("demo", model.Coordinator, w.ID); err == nil {
		t.Fatal("dismissed twice")
	}
	if ws, _ := c.Waits(); len(ws) != 0 {
		t.Fatalf("waits after dismiss = %+v", ws)
	}
}
