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
	"github.com/MahammadAgayev/neoharness/internal/project"
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
	h, err := hub.New(hub.Config{Factory: func(project.Project, project.Thread) agent.Agent { return echo{} }})
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

	if err := c.CreateProject("demo", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateProject("demo", ""); err == nil || err.Error() != `project "demo" already exists` {
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

	ps, err := c.Projects()
	if err != nil || len(ps) != 1 || ps[0].Name != "demo" {
		t.Fatalf("projects = %+v, %v", ps, err)
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
}
