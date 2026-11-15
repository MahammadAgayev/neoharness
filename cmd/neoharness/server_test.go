package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/MahammadAgayev/neoharness/api/hubapi"
	"github.com/MahammadAgayev/neoharness/api/model"
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

// TestBinaryVersionAsksTheBinaryOnDisk: a TUI opened before an update must
// compare the hub with the binary it restarts it on, not with itself.
func TestBinaryVersionAsksTheBinaryOnDisk(t *testing.T) {
	dir := t.TempDir()
	write := func(name, out string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho "+out+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if v := binaryVersion(write("new", "new1111")); v != "new1111" {
		t.Fatalf("version = %q, want new1111", v)
	}
	if v := binaryVersion(write("dev", "unknown")); v != "" {
		t.Fatalf("version of a build without vcs data = %q, want empty", v)
	}
	if v := binaryVersion(filepath.Join(dir, "gone")); v != "" {
		t.Fatalf("version of a missing binary = %q, want empty", v)
	}
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
	// A restart binds the port as soon as stop returns.
	ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("the MCP port is still taken after stop: %v", err)
	}
	ln.Close()
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
