package tui

import (
	_ "embed"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	lua "github.com/yuin/gopher-lua"
)

//go:embed defaults.lua
var defaultsLua string

// mapOp is one vim.keymap.set or vim.keymap.del. leader is the leader when
// it was made, as Neovim takes it then; "" in defaults.lua, whose maps take
// the leader the user's init.lua sets.
type mapOp struct {
	lhs, rhs, leader string
	fn               func(int)
	remap, del       bool
	insert           bool
}

// loadConfig runs the user's init.lua, if there is one, after defaults.lua.
// It takes a small part of Neovim's Lua API, so maps copy over from a Neovim
// config; defaults.lua shows all of it.
func (a *App) loadConfig(path string) {
	src, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		a.notify(err.Error(), a.th.Love)
		return
	}
	a.runConfig(string(src), path, false)
}

// runConfig runs a config's text: defaults.lua, or the user's init.lua. The
// Lua state stays, as maps may call its functions later. The maps are built
// again after each: the Go bindings, then defaults.lua's, then the user's.
func (a *App) runConfig(src, name string, defaults bool) {
	if a.lua == nil {
		a.luaAPI()
	}
	a.cfgName, a.cfgDefaults, a.cfgLoading, a.cfgCmds = name, defaults, true, nil
	if err := a.lua.DoString(src); err != nil {
		a.notify(fmt.Sprintf("%s: %v", name, err), a.th.Love)
	}
	a.cfgLoading = false
	a.buildMaps()
	// The commands run once the maps are in place, as Neovim runs init.lua
	// before the first screen.
	for _, c := range a.cfgCmds {
		a.exec(c)
	}
}

// luaAPI makes the vim table that configs see.
func (a *App) luaAPI() {
	L := lua.NewState()
	a.lua = L
	vim, g, keymap, api := L.NewTable(), L.NewTable(), L.NewTable(), L.NewTable()
	L.SetField(vim, "g", g)
	L.SetField(vim, "keymap", keymap)
	L.SetField(vim, "api", api)
	L.SetGlobal("vim", vim)
	// modes says which of Normal (with Visual) and Insert a map is for.
	modes := func(l *lua.LState) (normal, insert bool) {
		check := func(m string) {
			normal = normal || m == "n" || m == "v" || m == "x" || m == ""
			insert = insert || m == "i"
		}
		switch m := l.Get(1).(type) {
		case lua.LString:
			check(string(m))
		case *lua.LTable:
			m.ForEach(func(_, v lua.LValue) { check(v.String()) })
		}
		if !normal && !insert {
			a.notify(fmt.Sprintf("%s: only Normal, Visual and Insert maps work, not %q", a.cfgName, l.Get(1).String()), a.th.Gold)
		}
		return normal, insert
	}
	add := func(op mapOp, normal, insert bool) {
		if !a.cfgDefaults {
			op.leader = a.leader()
		}
		for _, in := range []bool{false, true} {
			if in && !insert || !in && !normal {
				continue
			}
			op.insert = in
			if a.cfgDefaults {
				a.defaultMaps = append(a.defaultMaps, op)
			} else {
				a.userMaps = append(a.userMaps, op)
			}
		}
	}
	L.SetField(keymap, "set", L.NewFunction(func(l *lua.LState) int {
		normal, insert := modes(l)
		if !normal && !insert {
			return 0
		}
		op := mapOp{lhs: l.CheckString(2)}
		if opts, ok := l.Get(4).(*lua.LTable); ok {
			op.remap = lua.LVAsBool(l.GetField(opts, "remap"))
		}
		switch rhs := l.Get(3).(type) {
		case *lua.LFunction:
			op.fn = func(int) {
				if err := L.CallByParam(lua.P{Fn: rhs, NRet: 0, Protect: true}); err != nil {
					a.notify(err.Error(), a.th.Love)
				}
			}
		case lua.LString:
			op.rhs = string(rhs)
		default:
			l.ArgError(3, "a string of keys or a function")
		}
		add(op, normal, insert)
		return 0
	}))
	L.SetField(keymap, "del", L.NewFunction(func(l *lua.LState) int {
		normal, insert := modes(l)
		add(mapOp{lhs: l.CheckString(2), del: true}, normal, insert)
		return 0
	}))
	L.SetField(api, "nvim_set_hl", L.NewFunction(func(l *lua.LState) int {
		if err := a.setHighlight(l.CheckString(2), l.CheckTable(3)); err != nil {
			a.notify(fmt.Sprintf("%s: %v", a.cfgName, err), a.th.Gold)
		}
		return 0
	}))
	L.SetField(vim, "cmd", L.NewFunction(func(l *lua.LState) int {
		line := strings.TrimPrefix(strings.TrimSpace(l.CheckString(1)), ":")
		if line == "" {
			return 0
		}
		if a.cfgLoading {
			a.cfgCmds = append(a.cfgCmds, line)
		} else {
			a.exec(line)
		}
		return 0
	}))
	L.SetField(vim, "notify", L.NewFunction(func(l *lua.LState) int {
		a.notify(l.CheckString(1), a.th.Subtle)
		return 0
	}))
}

// leader is vim.g.mapleader as a key name, <Space> when it is not set.
func (a *App) leader() string {
	if a.lua == nil {
		return "<Space>"
	}
	g, _ := a.lua.GetField(a.lua.GetGlobal("vim"), "g").(*lua.LTable)
	if g == nil {
		return "<Space>"
	}
	if s, ok := a.lua.GetField(g, "mapleader").(lua.LString); ok && s != "" && s != " " {
		return strings.Join(parseKeys(string(s), "<Space>"), "")
	}
	return "<Space>"
}

// buildMaps makes the bindings: the Go ones, defaults.lua's maps with the
// leader the user set, and the user's maps over them. A map that is not
// remap reaches the first two only (baseBinds).
func (a *App) buildMaps() {
	leader := a.leader()
	apply := func(b map[string]func(int), op mapOp) {
		ld := op.leader
		if ld == "" {
			ld = leader
		}
		lhs := strings.Join(parseKeys(op.lhs, ld), "")
		if op.del {
			delete(b, lhs)
			return
		}
		fn := op.fn
		if fn == nil {
			keys := parseKeys(op.rhs, ld)
			if len(keys) == 1 && strings.HasPrefix(keys[0], "<Plug>") {
				if fn = a.builtin[keys[0]]; fn == nil {
					a.notify(fmt.Sprintf("no %s", keys[0]), a.th.Gold)
					return
				}
			} else {
				fn = func(int) { a.feed(keys, op.remap) }
			}
		}
		b[lhs] = fn
	}
	base := maps.Clone(a.builtin)
	a.imaps = map[string]func(int){}
	for _, op := range a.defaultMaps {
		if op.insert {
			apply(a.imaps, op)
		} else {
			apply(base, op)
		}
	}
	a.baseBinds = base
	a.binds = maps.Clone(base)
	for _, op := range a.userMaps {
		if op.insert {
			apply(a.imaps, op)
		} else {
			apply(a.binds, op)
		}
	}
}

// insertMap holds keys in Insert that start an Insert map, as Neovim does
// for timeoutlen: the map runs when they make its keys, else they go in as
// typed. It reports that it took the key.
func (a *App) insertMap(ev *tcell.EventKey, k string) bool {
	if a.mode != modeInsert || len(a.imaps) == 0 || a.noremap > 0 || a.iflush {
		return false
	}
	a.iheld = append(a.iheld, ev)
	a.ikeys = append(a.ikeys, k)
	seq := strings.Join(a.ikeys, "")
	fn, exact := a.imaps[seq]
	longer := false
	for s := range a.imaps {
		if len(s) > len(seq) && strings.HasPrefix(s, seq) {
			longer = true
			break
		}
	}
	switch {
	case exact && !longer:
		a.iheld, a.ikeys = nil, nil
		fn(0)
	case longer:
		a.pendSeq++
		seqID := a.pendSeq
		time.AfterFunc(timeoutLen, func() { _ = a.s.PostEvent(evKeyTimeout{seqID}) })
	default:
		a.flushInsert(false)
	}
	return true
}

// flushInsert ends a wait for an Insert map: a whole map runs, after a
// timeout, else the held keys go in as typed.
func (a *App) flushInsert(timeout bool) {
	held, seq := a.iheld, strings.Join(a.ikeys, "")
	a.iheld, a.ikeys = nil, nil
	if fn, ok := a.imaps[seq]; ok && timeout {
		fn(0)
		return
	}
	a.iflush = true
	for _, ev := range held {
		a.key(ev)
	}
	a.iflush = false
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
	// NO_COLOR asks for no colors, from any config.
	if th.NoColor {
		return nil
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
		if strings.HasPrefix(s, "<Plug>(") {
			if end := strings.IndexByte(s, ')'); end > 0 {
				out = append(out, s[:end+1])
				s = s[end+1:]
				continue
			}
		}
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
