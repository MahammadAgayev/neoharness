package hubserver

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/MahammadAgayev/neoharness/internal/agent"
	"github.com/MahammadAgayev/neoharness/internal/hub"
	"github.com/MahammadAgayev/neoharness/internal/hubapi"
	"github.com/MahammadAgayev/neoharness/internal/model"
	"github.com/MahammadAgayev/neoharness/internal/workspace"
)

type echo struct{}

func (echo) Name() string { return "echo" }
func (echo) Run(ctx context.Context, req agent.Request) (agent.Result, error) {
	req.OnEvent(agent.Event{Kind: agent.KindAssistantText, Text: "echo: " + req.Prompt})
	return agent.Result{Text: "ok"}, nil
}

// TestClientServerRoundTrip drives the API the way the TUI does: over a real
// unix socket, with the update stream open.
func TestClientServerRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("NEOHARNESS_HOME", home)
	h, err := hub.New(hub.Config{Factory: func(workspace.Workspace, workspace.Thread) agent.Agent { return echo{} }})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	sock := filepath.Join(home, "hub.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: Handler(h)}
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
	if err := c.Send("demo", "api-tests", "more"); err == nil {
		t.Fatal("resolved thread should refuse")
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
	if a, err := c.EditArtifact("demo", "plan", "# Plan\n\nnew\n", 1, false); err != nil || a.Version != 2 || !a.UserEdit {
		t.Fatalf("edit = %+v, %v", a, err)
	}
	if _, err := c.EditArtifact("demo", "plan", "# Plan\n\nagain\n", 1, false); err == nil {
		t.Fatal("an edit from an old version should be refused")
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
