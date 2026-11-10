package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/MahammadAgayev/neoharness/internal/agent"
	agentclaude "github.com/MahammadAgayev/neoharness/internal/agent/claude"
	"github.com/MahammadAgayev/neoharness/internal/hub"
	"github.com/MahammadAgayev/neoharness/internal/hubapi"
	"github.com/MahammadAgayev/neoharness/internal/hubserver"
	"github.com/MahammadAgayev/neoharness/internal/mcpserver"
	"github.com/MahammadAgayev/neoharness/internal/tunnel"
	"github.com/MahammadAgayev/neoharness/internal/workspace"
)

// defaultMCPPort is fixed rather than random because remote hosts reach it
// through a tunnel bound to the same number, and a session resumed after a
// hub restart still has the old URL in its MCP config.
const defaultMCPPort = 47821

func mcpPort() int {
	if p, err := strconv.Atoi(os.Getenv("NEOHARNESS_MCP_PORT")); err == nil && p > 0 {
		return p
	}
	return defaultMCPPort
}

// logOut is where the hub logs. Never the terminal: the TUI owns it.
var logOut io.Writer = io.Discard

// startHub runs the hub in this process: the local API on the socket, so a
// second neoharness can share it, and MCP on the fixed port. stop cancels the
// running turns and waits for them to end.
func startHub() (stop func(), err error) {
	sock := hubapi.SocketPath()
	if err := os.MkdirAll(filepath.Dir(sock), 0o755); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(filepath.Join(filepath.Dir(sock), "hub.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	logOut = logFile

	port := mcpPort()
	mcpLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		logFile.Close()
		return nil, fmt.Errorf("MCP port %d is taken (set NEOHARNESS_MCP_PORT to use another): %w", port, err)
	}
	// The caller found no hub answering, so a socket file left here belongs to
	// a hub that died without cleaning up.
	_ = os.Remove(sock)
	apiLn, err := net.Listen("unix", sock)
	if err != nil {
		mcpLn.Close()
		logFile.Close()
		return nil, err
	}
	// The socket drives agents that edit code; it is nobody's business but
	// its owner's.
	_ = os.Chmod(sock, 0o600)

	if err := workspace.Migrate(); err != nil {
		apiLn.Close()
		mcpLn.Close()
		logFile.Close()
		return nil, err
	}

	tunnels := tunnel.New(port)
	h, err := hub.New(hub.Config{MCPPort: port, Factory: agentFactory(tunnels), Files: listFiles, Branch: gitBranch, Shell: runShell})
	if err != nil {
		apiLn.Close()
		mcpLn.Close()
		logFile.Close()
		return nil, err
	}

	mcpMux := http.NewServeMux()
	mcpMux.Handle("/mcp", mcpserver.Handler(h))
	mcpSrv := &http.Server{Handler: mcpMux}
	apiSrv := &http.Server{Handler: hubserver.Handler(h)}
	go mcpSrv.Serve(mcpLn)
	go apiSrv.Serve(apiLn)

	logf("hub up · api %s · mcp 127.0.0.1:%d", sock, port)
	logf("workspaces in %s", workspace.Root())
	go logUpdates(h)

	return func() {
		logf("hub stopping")
		// Turns end first, so their final events still reach anyone listening.
		h.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = apiSrv.Shutdown(ctx)
		_ = mcpSrv.Shutdown(ctx)
		tunnels.Close()
		_ = os.Remove(sock)
		logFile.Close()
	}, nil
}

// agentFactory builds the claude adapter for a thread, opening the ssh tunnel
// first for a remote one.
func agentFactory(tunnels *tunnel.Manager) hub.AgentFactory {
	return func(p workspace.Workspace, t workspace.Thread) agent.Agent {
		if !t.Location.Remote() {
			return agentclaude.New(commandFor("NEOHARNESS_CLAUDE_COMMAND")...)
		}
		// The local command may be a wrapper that only exists here, so the
		// remote one has its own override.
		a := agentclaude.New(commandFor("NEOHARNESS_REMOTE_CLAUDE_COMMAND")...)
		a.Host, a.Dir = t.Location.Host, t.Location.Dir
		return tunneled{Agent: a, tunnels: tunnels, host: t.Location.Host}
	}
}

type tunneled struct {
	*agentclaude.Agent
	tunnels *tunnel.Manager
	host    string
}

func (t tunneled) Run(ctx context.Context, req agent.Request) (agent.Result, error) {
	if err := t.tunnels.Ensure(t.host); err != nil {
		return agent.Result{}, err
	}
	return t.Agent.Run(ctx, req)
}

// logUpdates writes a line per thread state change, so hub.log is a readable
// record of what happened.
func logUpdates(h *hub.Hub) {
	ch, stop := h.Subscribe()
	defer stop()
	last := map[string]string{}
	for u := range ch {
		if u.View == nil {
			continue
		}
		key := u.Workspace + "/" + u.Thread
		s := string(u.View.State)
		if last[key] != s {
			last[key] = s
			logf("%s  %s", key, s)
		}
	}
}

func logf(format string, args ...any) {
	fmt.Fprintf(logOut, "%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}
