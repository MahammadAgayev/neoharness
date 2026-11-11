package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// HubSettings are the settings of the Lua config that the hub needs before
// the TUI starts: require("neoharness").setup and, for the claude agent,
// require("neoharness.claude").setup.
type HubSettings struct {
	MCPPort int
	Claude  ClaudeSettings
}

// ClaudeSettings configure the claude agent.
type ClaudeSettings struct {
	// Command is the command words for claude here, RemoteCommand on ssh
	// hosts. The local one may be a wrapper that exists only on this machine.
	Command, RemoteCommand []string
	// Model and Effort are used when a thread has no /model or /effort of its
	// own. Empty leaves claude's default.
	Model, Effort string
	// ContextWindow, when not 0, is the most of the model's window the
	// context percentage counts.
	ContextWindow int
}

// setupModules gives a Lua state the neoharness modules. Each setup call
// sets only the keys it names, so init.lua changes what defaults.lua set.
// set is nil in the TUI, which reads none of it.
func setupModules(L *lua.LState, set func(module, key string, v lua.LValue)) {
	for _, name := range []string{"neoharness", "neoharness.claude"} {
		L.PreloadModule(name, func(l *lua.LState) int {
			mod := l.NewTable()
			l.SetField(mod, "setup", l.NewFunction(func(l *lua.LState) int {
				if set != nil {
					l.CheckTable(1).ForEach(func(k, v lua.LValue) { set(name, k.String(), v) })
				}
				return 0
			}))
			l.Push(mod)
			return 1
		})
	}
}

// ReadHubSettings runs defaults.lua, then the init.lua at path if there is
// one, for their setup calls. Maps, colors and commands do nothing here; the
// TUI runs the same files again for those.
func ReadHubSettings(path string) (HubSettings, error) {
	var s HubSettings
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
	setupModules(L, func(module, key string, v lua.LValue) {
		switch module + "." + key {
		case "neoharness.mcp_port":
			if n, ok := v.(lua.LNumber); ok {
				s.MCPPort = int(n)
			}
		case "neoharness.claude.command":
			s.Claude.Command = words(v)
		case "neoharness.claude.remote_command":
			s.Claude.RemoteCommand = words(v)
		case "neoharness.claude.model":
			s.Claude.Model = text(v)
		case "neoharness.claude.effort":
			s.Claude.Effort = text(v)
		case "neoharness.claude.context_window":
			if n, ok := v.(lua.LNumber); ok {
				s.Claude.ContextWindow = int(n)
			}
		}
	})
	noop := L.NewFunction(func(*lua.LState) int { return 0 })
	vim, g, o, keymap, api := L.NewTable(), L.NewTable(), L.NewTable(), L.NewTable(), L.NewTable()
	L.SetField(vim, "g", g)
	L.SetField(vim, "o", o)
	L.SetField(vim, "opt", o)
	L.SetField(keymap, "set", noop)
	L.SetField(keymap, "del", noop)
	L.SetField(vim, "keymap", keymap)
	L.SetField(api, "nvim_set_hl", noop)
	L.SetField(vim, "api", api)
	L.SetField(vim, "cmd", noop)
	L.SetField(vim, "notify", noop)
	L.SetGlobal("vim", vim)

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
	return s, nil
}
