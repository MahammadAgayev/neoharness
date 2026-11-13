package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MahammadAgayev/neoharness/internal/agent"
	agentclaude "github.com/MahammadAgayev/neoharness/internal/agent/claude"
	"github.com/MahammadAgayev/neoharness/internal/hub"
	"github.com/MahammadAgayev/neoharness/internal/hubapi"
	"github.com/MahammadAgayev/neoharness/internal/hubserver"
	"github.com/MahammadAgayev/neoharness/internal/mcpserver"
	"github.com/MahammadAgayev/neoharness/internal/tui"
	"github.com/MahammadAgayev/neoharness/internal/tunnel"
	"github.com/MahammadAgayev/neoharness/internal/workspace"
)

// defaultMCPPort is fixed rather than random because remote hosts reach it
// through a tunnel bound to the same number, and a session resumed after a
// hub restart still has the old URL in its MCP config.
const defaultMCPPort = 47821

// logOut is where the hub logs. Never the terminal: the TUI owns it.
var logOut io.Writer = io.Discard

// startHub runs the hub in this process: the local API on the socket, which
// every neoharness shares, and MCP on the fixed port. asked is called when a
// client asks the hub to stop. stop cancels the running turns and waits for
// them to end.
func startHub(asked func()) (stop func(), err error) {
	sock := hubapi.SocketPath()
	if err := os.MkdirAll(filepath.Dir(sock), 0o755); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(filepath.Join(filepath.Dir(sock), "hub.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	logOut = logFile

	// Read when the hub starts, so a hub taken over from another neoharness
	// gets this machine's init.lua as it is now.
	cfg, err := tui.ReadHubSettings(filepath.Join(filepath.Dir(sock), "init.lua"))
	if err != nil {
		logFile.Close()
		return nil, err
	}
	if err := cfg.Check(); err != nil {
		logFile.Close()
		return nil, err
	}
	// One provider per kind, which all agents of that kind share. Every agent
	// is ready, so :setlocal can choose any of them.
	providers := map[string]agent.Provider{}
	agents := map[string]configured{}
	defaults := map[string]hub.Defaults{}
	for name, a := range cfg.Agents {
		kind := cfg.Kind(name)
		newProvider, ok := kinds[kind]
		if !ok {
			logFile.Close()
			return nil, fmt.Errorf("agent %q: there is no kind %q; the kinds are %s", name, kind, strings.Join(slices.Sorted(maps.Keys(kinds)), ", "))
		}
		if providers[kind] == nil {
			providers[kind] = newProvider()
		}
		agents[name] = configured{provider: providers[kind], settings: agent.Settings{Command: a.Command, RemoteCommand: a.RemoteCommand}}
		defaults[name] = hub.Defaults{Model: a.Model, Effort: a.Effort, ContextWindow: a.ContextWindow}
	}
	port := cmp.Or(cfg.MCPPort, defaultMCPPort)
	mcpLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		logFile.Close()
		return nil, fmt.Errorf("MCP port %d is taken (set mcp_port in require(\"neoharness\").setup to use another): %w", port, err)
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
	h, err := hub.New(hub.Config{MCPPort: port, Agents: defaults, Coordinator: cfg.Coordinator, Threads: cfg.Threads,
		Factory: agentFactory(tunnels, agents), Files: listFiles, Branch: gitBranch, Shell: runShell,
		Notify: cfg.Notify == "coordinator"})
	if err != nil {
		apiLn.Close()
		mcpLn.Close()
		logFile.Close()
		return nil, err
	}

	mcpMux := http.NewServeMux()
	mcpMux.Handle("/mcp", mcpserver.Handler(h))
	mcpSrv := &http.Server{Handler: mcpMux}
	info := hubapi.Info{Version: version(), PID: os.Getpid(), Started: time.Now()}
	apiSrv := &http.Server{Handler: hubserver.Handler(h, info, asked)}
	go mcpSrv.Serve(mcpLn)
	go apiSrv.Serve(apiLn)

	logf("hub up · api %s · mcp 127.0.0.1:%d", sock, port)
	logf("workspaces in %s", workspace.Root())
	go logUpdates(h)

	return func() {
		logf("hub stopping")
		// Turns end first, so their final events still reach anyone listening.
		h.Close()
		for _, p := range providers {
			p.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = apiSrv.Shutdown(ctx)
		_ = mcpSrv.Shutdown(ctx)
		tunnels.Close()
		_ = os.Remove(sock)
		logFile.Close()
	}, nil
}

// kinds are the agent adapters init.lua can name as an agent's kind.
var kinds = map[string]func() agent.Provider{
	"claude": func() agent.Provider { return agentclaude.NewProvider() },
}

// configured is an agent of the config: its kind's provider and what it runs.
type configured struct {
	provider agent.Provider
	settings agent.Settings
}

// agentFactory builds a thread's agent, opening the ssh tunnel first for a
// remote one.
func agentFactory(tunnels *tunnel.Manager, agents map[string]configured) hub.AgentFactory {
	return func(name string, p workspace.Workspace, t workspace.Thread) agent.Agent {
		c := agents[name]
		if !t.Location.Remote() {
			return c.provider.Agent(c.settings, "", "")
		}
		return tunneled{Agent: c.provider.Agent(c.settings, t.Location.Host, t.Location.Dir), tunnels: tunnels, host: t.Location.Host}
	}
}

type tunneled struct {
	agent.Agent
	tunnels *tunnel.Manager
	host    string
}

func (t tunneled) Run(ctx context.Context, req agent.Request) (agent.Result, error) {
	if err := t.tunnels.Ensure(t.host); err != nil {
		return agent.Result{}, err
	}
	return t.Agent.Run(ctx, req)
}

// Efforts passes the agent's levels on, so /effort checks them on remote
// threads too.
func (t tunneled) Efforts() []string {
	if e, ok := t.Agent.(agent.Efforter); ok {
		return e.Efforts()
	}
	return nil
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
