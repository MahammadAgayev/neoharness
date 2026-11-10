package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/gdamore/tcell/v2"
	lua "github.com/yuin/gopher-lua"
)

// loadConfig runs the user's init.lua, if there is one. It takes a small part
// of Neovim's Lua API, so maps copy over from a Neovim config:
//
//	vim.g.mapleader = ","
//	vim.keymap.set("n", "<leader>t", ":threads<CR>")
//	vim.keymap.set("n", "gt", function() vim.cmd("threads") end)
//	vim.keymap.del("n", "<C-d>")
//	vim.api.nvim_set_hl(0, "Iris", { fg = "#c4a7e7", bold = true })
//	vim.cmd("workspace demo")
//	vim.notify("loaded")
func (a *App) loadConfig(path string) {
	src, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		a.notify(err.Error(), a.th.Love)
		return
	}
	a.runConfig(string(src), path)
}

// runConfig runs a config's text. The Lua state stays, as maps may call its
// functions later.
func (a *App) runConfig(src, name string) {
	L := lua.NewState()
	a.lua = L
	leader := "<Space>"
	// The maps and deletes apply in order once the leader is known, over the
	// default bindings moved to that leader.
	var ops []func(map[string]func(int))
	var cmds []string
	loading := true

	vim, g, keymap, api := L.NewTable(), L.NewTable(), L.NewTable(), L.NewTable()
	L.SetField(vim, "g", g)
	L.SetField(vim, "keymap", keymap)
	L.SetField(vim, "api", api)
	L.SetGlobal("vim", vim)
	leaderNow := func() string {
		if s, ok := L.GetField(g, "mapleader").(lua.LString); ok && s != "" {
			if s == " " {
				return "<Space>"
			}
			return strings.Join(parseKeys(string(s), "<Space>"), "")
		}
		return "<Space>"
	}
	normal := func(l *lua.LState) bool {
		ok := false
		check := func(m string) { ok = ok || m == "n" || m == "v" || m == "x" || m == "" }
		switch m := l.Get(1).(type) {
		case lua.LString:
			check(string(m))
		case *lua.LTable:
			m.ForEach(func(_, v lua.LValue) { check(v.String()) })
		}
		if !ok {
			a.notify(fmt.Sprintf("%s: only Normal mode maps work, not %q", name, l.Get(1).String()), a.th.Gold)
		}
		return ok
	}
	L.SetField(keymap, "set", L.NewFunction(func(l *lua.LState) int {
		if !normal(l) {
			return 0
		}
		leader = leaderNow()
		lhs := strings.Join(parseKeys(l.CheckString(2), leader), "")
		remap := false
		if opts, ok := l.Get(4).(*lua.LTable); ok {
			remap = lua.LVAsBool(l.GetField(opts, "remap"))
		}
		var fn func(int)
		switch rhs := l.Get(3).(type) {
		case *lua.LFunction:
			fn = func(int) {
				if err := L.CallByParam(lua.P{Fn: rhs, NRet: 0, Protect: true}); err != nil {
					a.notify(err.Error(), a.th.Love)
				}
			}
		case lua.LString:
			keys := parseKeys(string(rhs), leader)
			fn = func(int) { a.feed(keys, remap) }
		default:
			l.ArgError(3, "a string of keys or a function")
		}
		ops = append(ops, func(b map[string]func(int)) { b[lhs] = fn })
		return 0
	}))
	L.SetField(keymap, "del", L.NewFunction(func(l *lua.LState) int {
		if !normal(l) {
			return 0
		}
		lhs := strings.Join(parseKeys(l.CheckString(2), leaderNow()), "")
		ops = append(ops, func(b map[string]func(int)) { delete(b, lhs) })
		return 0
	}))
	L.SetField(api, "nvim_set_hl", L.NewFunction(func(l *lua.LState) int {
		if err := a.setHighlight(l.CheckString(2), l.CheckTable(3)); err != nil {
			a.notify(fmt.Sprintf("%s: %v", name, err), a.th.Gold)
		}
		return 0
	}))
	L.SetField(vim, "cmd", L.NewFunction(func(l *lua.LState) int {
		line := strings.TrimPrefix(strings.TrimSpace(l.CheckString(1)), ":")
		if line == "" {
			return 0
		}
		if loading {
			cmds = append(cmds, line)
		} else {
			a.exec(line)
		}
		return 0
	}))
	L.SetField(vim, "notify", L.NewFunction(func(l *lua.LState) int {
		a.notify(l.CheckString(1), a.th.Subtle)
		return 0
	}))

	if err := L.DoString(src); err != nil {
		a.notify(fmt.Sprintf("%s: %v", name, err), a.th.Love)
	}
	loading = false
	leader = leaderNow()
	base := map[string]func(int){}
	for k, fn := range a.binds {
		if rest, ok := strings.CutPrefix(k, "<Space>"); ok {
			k = leader + rest
		}
		base[k] = fn
	}
	a.baseBinds = base
	a.binds = map[string]func(int){}
	for k, fn := range base {
		a.binds[k] = fn
	}
	for _, op := range ops {
		op(a.binds)
	}
	// The commands run once the maps are in place, as Neovim runs init.lua
	// before the first screen.
	for _, c := range cmds {
		a.exec(c)
	}
}

// feed types a map's keys. A map that is not remap reaches only the default
// bindings, as nnoremap does. The keys are not recorded again for q and .:
// the key that started the map was.
func (a *App) feed(keys []string, remap bool) {
	if a.playing >= maxMacroDepth {
		return
	}
	replaying := a.replaying
	a.playing++
	a.replaying = true
	if !remap {
		a.noremap++
	}
	for _, k := range keys {
		a.key(keyEvent(k))
	}
	if !remap {
		a.noremap--
	}
	a.replaying = replaying
	a.playing--
}

// bindings are the maps a key sequence is looked up in: only the defaults
// inside a map that is not remap.
func (a *App) bindings() map[string]func(int) {
	if a.noremap > 0 && a.baseBinds != nil {
		return a.baseBinds
	}
	return a.binds
}

// setHighlight is nvim_set_hl for a theme color. Neovim's own group names
// work for the ones that have a match here.
func (a *App) setHighlight(group string, opts *lua.LTable) error {
	th := a.th
	groups := map[string]*tcell.Style{
		"Text": &th.Text, "Normal": &th.Text, "Subtle": &th.Subtle, "Muted": &th.Muted, "Comment": &th.Muted,
		"Iris": &th.Iris, "Rose": &th.Rose, "Foam": &th.Foam, "Gold": &th.Gold, "Pine": &th.Pine,
		"Love": &th.Love, "Error": &th.Love, "CursorLine": &th.CursorLine, "Selection": &th.Selection,
		"Visual": &th.Selection, "Separator": &th.Separator, "WinSeparator": &th.Separator, "Code": &th.Code,
	}
	st, ok := groups[group]
	if !ok {
		return fmt.Errorf("no highlight group %q", group)
	}
	out := tcell.StyleDefault
	for _, key := range []string{"fg", "bg"} {
		v, ok := opts.RawGetString(key).(lua.LString)
		if !ok {
			continue
		}
		color := tcell.GetColor(string(v))
		if color == tcell.ColorDefault && !strings.EqualFold(string(v), "default") {
			return fmt.Errorf("%s: no color %q", group, v)
		}
		if key == "fg" {
			out = out.Foreground(color)
		} else {
			out = out.Background(color)
		}
	}
	out = out.Bold(lua.LVAsBool(opts.RawGetString("bold"))).
		Italic(lua.LVAsBool(opts.RawGetString("italic"))).
		Underline(lua.LVAsBool(opts.RawGetString("underline")))
	*st = out
	return nil
}

// parseKeys splits keys written as in Neovim into key names as keyName
// writes them: "<leader>t" with leader <Space> is <Space> and t, "<c-w>v" is
// <C-w> and v. A < that does not start a key name is the key <.
func parseKeys(s, leader string) []string {
	var out []string
	for len(s) > 0 {
		if s[0] == '<' {
			if end := strings.IndexByte(s, '>'); end > 1 {
				if k, ok := keyNotation(s[1:end], leader); ok {
					out = append(out, k)
					s = s[end+1:]
					continue
				}
			}
		}
		r := []rune(s)[0]
		out = append(out, string(r))
		s = s[len(string(r)):]
	}
	return out
}

// keyNotation is the key name for a <...> name, in any case.
func keyNotation(name, leader string) (string, bool) {
	low := strings.ToLower(name)
	switch low {
	case "leader":
		return leader, true
	case "cr", "enter", "return":
		return "<CR>", true
	case "s-cr":
		return "<S-CR>", true
	case "esc":
		return "<Esc>", true
	case "space":
		return "<Space>", true
	case "tab":
		return "<Tab>", true
	case "s-tab":
		return "<S-Tab>", true
	case "bs":
		return "<BS>", true
	case "del":
		return "<Del>", true
	case "lt":
		return "<", true
	case "bar":
		return "|", true
	case "up", "down", "left", "right", "home", "end":
		return "<" + strings.ToUpper(low[:1]) + low[1:] + ">", true
	case "pageup", "pagedown":
		return map[string]string{"pageup": "<PageUp>", "pagedown": "<PageDown>"}[low], true
	case "c-^":
		return "<C-^>", true
	}
	if len(low) == 3 && strings.HasPrefix(low, "c-") && low[2] >= 'a' && low[2] <= 'z' {
		return "<C-" + low[2:] + ">", true
	}
	return "", false
}

// keyEvent is the key event for a key name, the other way from keyName.
func keyEvent(k string) *tcell.EventKey {
	named := map[string]tcell.Key{
		"<CR>": tcell.KeyEnter, "<Esc>": tcell.KeyEscape, "<Tab>": tcell.KeyTab, "<S-Tab>": tcell.KeyBacktab,
		"<BS>": tcell.KeyBackspace2, "<Del>": tcell.KeyDelete, "<Up>": tcell.KeyUp, "<Down>": tcell.KeyDown,
		"<Left>": tcell.KeyLeft, "<Right>": tcell.KeyRight, "<Home>": tcell.KeyHome, "<End>": tcell.KeyEnd,
		"<PageUp>": tcell.KeyPgUp, "<PageDown>": tcell.KeyPgDn, "<C-^>": tcell.KeyCtrlCarat,
	}
	switch {
	case k == "<Space>":
		return tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone)
	case k == "<S-CR>":
		return tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModShift)
	case named[k] != 0:
		return tcell.NewEventKey(named[k], 0, tcell.ModNone)
	case len(k) == 5 && strings.HasPrefix(k, "<C-"):
		return tcell.NewEventKey(tcell.KeyCtrlA+tcell.Key(k[3]-'a'), 0, tcell.ModNone)
	}
	return tcell.NewEventKey(tcell.KeyRune, []rune(k)[0], tcell.ModNone)
}
