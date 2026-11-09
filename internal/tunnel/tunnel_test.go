package tunnel

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestEnsureReusesALiveTunnelAndReportsFailure(t *testing.T) {
	startGrace = 50 * time.Millisecond
	m := New(4242)
	defer m.Close()
	starts := 0
	m.command = func(ctx context.Context, host string, port int) *exec.Cmd {
		starts++
		if host == "bad" {
			return exec.CommandContext(ctx, "sh", "-c", "echo 'remote port forwarding failed' >&2; exit 255")
		}
		return exec.CommandContext(ctx, "sleep", "30")
	}

	if err := m.Ensure("gpu1"); err != nil {
		t.Fatal(err)
	}
	if err := m.Ensure("gpu1"); err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatalf("started %d tunnels, want 1 reused", starts)
	}

	err := m.Ensure("bad")
	if err == nil || !strings.Contains(err.Error(), "remote port forwarding failed") {
		t.Fatalf("err = %v, want ssh's own message", err)
	}
}

func TestSSHCommandForwardsTheSamePort(t *testing.T) {
	got := strings.Join(sshCommand(context.Background(), "gpu1", 4242).Args, " ")
	for _, want := range []string{"-N", "-R 127.0.0.1:4242:127.0.0.1:4242", "ExitOnForwardFailure=yes", "BatchMode=yes", "gpu1"} {
		if !strings.Contains(got, want) {
			t.Errorf("ssh args %q missing %q", got, want)
		}
	}
}
