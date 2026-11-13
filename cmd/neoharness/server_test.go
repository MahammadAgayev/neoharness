package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/MahammadAgayev/neoharness/internal/hubapi"
	"github.com/MahammadAgayev/neoharness/internal/model"
)

// TestMain lets the test binary stand in for neoharness: spawnHub starts
// os.Executable, which here is this binary.
func TestMain(m *testing.M) {
	if os.Getenv("NEOHARNESS_TEST_MAIN") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestHubRunsApartFromItsClients starts the hub as the first neoharness does,
// checks that it is a process of its own that a second client shares, and
// stops it as `neoharness hub stop` does.
func TestHubRunsApartFromItsClients(t *testing.T) {
	home := t.TempDir()
	t.Setenv("NEOHARNESS_HOME", home)
	t.Setenv("NEOHARNESS_TEST_MAIN", "1")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	lua := fmt.Sprintf("require(\"neoharness\").setup({ mcp_port = %d })\n", port)
	if err := os.WriteFile(filepath.Join(home, "init.lua"), []byte(lua), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := ensureHub()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.StopHub() })
	info, err := c.Info()
	if err != nil {
		t.Fatal(err)
	}
	if info.PID == os.Getpid() || info.PID == 0 {
		t.Fatalf("hub pid = %d, want a process of its own", info.PID)
	}
	again, err := ensureHub()
	if err != nil {
		t.Fatal(err)
	}
	if i, _ := again.Info(); i.PID != info.PID {
		t.Fatalf("a second client started another hub: pid %d, then %d", info.PID, i.PID)
	}

	updates, err := c.Updates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stopHub(c); err != nil {
		t.Fatal(err)
	}
	if c.Ping() == nil {
		t.Fatal("the hub still answers after stop")
	}
	if _, err := os.Stat(hubapi.SocketPath()); !os.IsNotExist(err) {
		t.Fatalf("the socket is still there: %v", err)
	}
	last := model.Update{}
	for u := range updates {
		last = u
	}
	if last.Kind != model.UpdateStopped {
		t.Fatalf("last update = %q, want %q", last.Kind, model.UpdateStopped)
	}
	if err := stopHub(c); err == nil || err.Error() != "no hub is running" {
		t.Fatalf("stop with no hub = %v", err)
	}
}
