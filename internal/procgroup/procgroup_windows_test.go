package procgroup

import (
	"bytes"
	"os/exec"
	"testing"
	"time"
)

// TestKillStopsTheGrandchild: with only cmd killed, ping would keep the
// stdout pipe open and Wait would not return.
func TestKillStopsTheGrandchild(t *testing.T) {
	cmd := exec.Command("cmd", "/C", "ping -n 300 127.0.0.1")
	var out bytes.Buffer
	cmd.Stdout = &out
	Isolate(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := Kill(cmd); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the grandchild is still alive")
	}
}
