// Package tunnel keeps one ssh reverse tunnel open per remote host, so that a
// Claude session on that host reaches the hub's MCP server at the same
// 127.0.0.1 address a local session uses.
//
// One tunnel per host rather than one per turn: a turn's ssh process carries
// the claude stream, and a second -R for the same port on the same host would
// fail to bind while the first is up.
package tunnel

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// startGrace is how long a new tunnel must stay up before it counts as open.
// ssh with ExitOnForwardFailure exits within this window when the forward
// cannot be bound or the host cannot be reached.
var startGrace = 1500 * time.Millisecond

// Manager owns the tunnels.
type Manager struct {
	Port int

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	tunnels map[string]*tunnel
	// command builds the ssh command; replaced in tests.
	command func(ctx context.Context, host string, port int) *exec.Cmd
}

type tunnel struct {
	cmd  *exec.Cmd
	done chan struct{}
}

// New returns a manager forwarding port on every remote host to the same port
// here.
func New(port int) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{Port: port, ctx: ctx, cancel: cancel, tunnels: map[string]*tunnel{}, command: sshCommand}
}

func sshCommand(ctx context.Context, host string, port int) *exec.Cmd {
	fwd := fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", port, port)
	return exec.CommandContext(ctx, "ssh", "-N",
		"-o", "BatchMode=yes",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-R", fwd, host)
}

// Ensure opens the tunnel to host if it is not open. It returns an error the
// user can act on when the tunnel cannot be opened.
func (m *Manager) Ensure(host string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tunnels[host]; ok {
		select {
		case <-t.done:
		default:
			return nil
		}
	}
	cmd := m.command(m.ctx, host, m.Port)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("tunnel to %s: %w", host, err)
	}
	t := &tunnel{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(t.done)
	}()
	select {
	case <-t.done:
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = "ssh exited"
		}
		return fmt.Errorf("tunnel to %s failed: %s", host, msg)
	case <-time.After(startGrace):
	}
	m.tunnels[host] = t
	return nil
}

// Close stops every tunnel.
func (m *Manager) Close() {
	m.cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tunnels {
		<-t.done
	}
}
