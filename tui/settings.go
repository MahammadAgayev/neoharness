package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// HubSettings are the settings of the Lua config that the hub needs before
// the TUI starts: require("neoharness").setup and
// require("neoharness.agents").setup.
type HubSettings struct {
	MCPPort int
	// Notify is "coordinator" when the coordinator decides what reaches the
	// person, or "threads" when every thread notifies them itself.
	Notify string
	// Coordinator names the entry of Agents that coordinators run, and
	// Threads the one threads run; empty runs the coordinator's.
	Coordinator, Threads string
	Agents               map[string]AgentSettings
	// ReopenCompact is what a thread the coordinator reopens does when its
	// context is past CompactWarn percent, neoharness.o.compactwarn: compact,
	// ask or off.
	ReopenCompact string
	CompactWarn   int
}

// AgentSettings configure one agent.
type AgentSettings struct {
	// Kind is the adapter that runs it, such as "claude". Empty means the
	// agent's name.
	Kind string
	// Command is the command words here, RemoteCommand on ssh hosts. The
	// local one may be a wrapper that exists only on this machine.
	Command, RemoteCommand []string
	// Model and Effort are used when a thread has no /model or /effort of its
	// own. Empty leaves the agent's default.
	Model, Effort string
	// ContextWindow, when not 0, is the most of the model's window the
	// context percentage counts.
	ContextWindow int
}

// setupModules gives a Lua state the modules: require("neoharness") is the
// global neoharness table nh, and require("neoharness.agents") the agents'.
// require("neoharness.claude").setup(t) is agents.setup({ claude = t }), as
// it was before there were agents. Each setup call sets only the keys it
// names, so init.lua changes what defaults.lua set. set is nil in the TUI,
// which reads none of it.
func setupModules(L *lua.LState, nh *lua.LTable, set func(module, key string, v lua.LValue)) {
	setup := func(name string) *lua.LFunction {
		return L.NewFunction(func(l *lua.LState) int {
			if set != nil {
				l.CheckTable(1).ForEach(func(k, v lua.LValue) { set(name, k.String(), v) })
			}
			return 0
		})
	}
	L.SetField(nh, "setup", setup("neoharness"))
	L.PreloadModule("neoharness", func(l *lua.LState) int {
		l.Push(nh)
		return 1
	})
	for _, name := range []string{"neoharness.agents", "neoharness.claude"} {
		L.PreloadModule(name, func(l *lua.LState) int {
			mod := l.NewTable()
			l.SetField(mod, "setup", setup(name))
			l.Push(mod)
			return 1
		})
	}
}

// ReadHubSettings runs defaults.lua, then the init.lua at path if there is
// one, for their setup calls. Maps, colors and commands do nothing here; the
// TUI runs the same files again for those.
func ReadHubSettings(path string) (HubSettings, error) {
	s := HubSettings{Agents: map[string]AgentSettings{}}
	L := lua.NewState()
	defer L.Close()
	words := func(v lua.LValue) []string {
		if v == lua.LNil {
			return nil
		}
		return strings.Fields(v.String())
	}
	text := func(v lua.LValue) string {
		if v == lua.LNil {
			return ""
		}
		return strings.TrimSpace(v.String())
	}
	agentKey := func(name, key string, v lua.LValue) {
		a := s.Agents[name]
		switch key {
		case "kind":
			a.Kind = text(v)
		case "command":
			a.Command = words(v)
		case "remote_command":
			a.RemoteCommand = words(v)
		case "model":
			a.Model = text(v)
		case "effort":
			a.Effort = text(v)
		case "context_window":
			if n, ok := v.(lua.LNumber); ok {
				a.ContextWindow = int(n)
			}
		}
		s.Agents[name] = a
	}
	nh := L.NewTable()
	setupModules(L, nh, func(module, key string, v lua.LValue) {
		switch module {
		case "neoharness.claude":
			agentKey("claude", key, v)
		case "neoharness.agents":
			if t, ok := v.(*lua.LTable); ok {
				t.ForEach(func(k, v lua.LValue) { agentKey(key, k.String(), v) })
			}
		}
		switch module + "." + key {
		case "neoharness.mcp_port":
			if n, ok := v.(lua.LNumber); ok {
				s.MCPPort = int(n)
			}
		case "neoharness.notify":
			s.Notify = text(v)
		case "neoharness.coordinator":
			s.Coordinator = text(v)
		case "neoharness.threads":
			s.Threads = text(v)
		case "neoharness.reopen_compact":
			s.ReopenCompact = text(v)
		}
	})
	noop := L.NewFunction(func(*lua.LState) int { return 0 })
	keymap := L.NewTable()
	L.SetField(keymap, "set", noop)
	L.SetField(keymap, "del", noop)
	L.SetField(nh, "keymap", keymap)
	o := L.NewTable()
	L.SetField(nh, "o", o)
	L.SetField(nh, "hl", noop)
	L.SetField(nh, "cmd", noop)
	L.SetGlobal("neoharness", nh)

	if err := L.DoString(defaultsLua); err != nil {
		return HubSettings{}, fmt.Errorf("defaults.lua: %w", err)
	}
	src, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return HubSettings{}, err
	default:
		if err := L.DoString(string(src)); err != nil {
			return HubSettings{}, fmt.Errorf("%s: %w", path, err)
		}
	}
	if n, ok := L.GetField(o, "compactwarn").(lua.LNumber); ok {
		s.CompactWarn = int(n)
	}
	return s, nil
}

// Kind is the adapter of the named agent: its kind, or else its name.
func (s HubSettings) Kind(name string) string {
	if k := s.Agents[name].Kind; k != "" {
		return k
	}
	return name
}

// Check says what is wrong with the agents the roles name.
func (s HubSettings) Check() error {
	names := []string{s.Coordinator}
	if s.Threads != "" {
		names = append(names, s.Threads)
	}
	for _, n := range names {
		if _, ok := s.Agents[n]; !ok {
			return fmt.Errorf("agent %q is not in require(\"neoharness.agents\").setup", n)
		}
	}
	switch s.ReopenCompact {
	case "", "compact", "ask", "off":
	default:
		return fmt.Errorf("reopen_compact is compact, ask or off, not %q", s.ReopenCompact)
	}
	return nil
}
