package tui

import "unicode"

// menuRows is how many completions show at once.
const menuRows = 8

// menu is the completion popup over the prompt: `/` commands at the start
// of the prompt, `@` threads and files at the start of a word. Thread rows
// carry a `#` so they stand out from files.
type menu struct {
	start int // where the word with the sigil starts in the prompt
	sigil rune
	items []pickItem
	sel   int
}

// fetchCompletions asks the hub in the background: for a remote thread the
// file list costs an ssh round trip, and the prompt must not wait on it.
func (a *App) fetchCompletions(b *Buffer) {
	go func() {
		if c, err := a.c.Completions(b.Workspace, b.Thread); err == nil {
			_ = a.s.PostEvent(evComplete{b.Name, c})
		}
	}()
}

// updateMenu matches the word before the cursor against the completions.
func (a *App) updateMenu() {
	old := a.menu
	a.menu = nil
	p := a.cur.Buf.Prompt
	if a.mode != modeInsert || p == nil {
		return
	}
	i := p.Cur
	for i > 0 && !unicode.IsSpace(p.Text[i-1]) {
		i--
	}
	if i == p.Cur {
		a.menuShut = -1
		return
	}
	if i != a.menuShut {
		a.menuShut = -1
	}
	if a.menuShut == i {
		return
	}
	c := a.comps[a.cur.Buf.Name]
	var items []pickItem
	switch sigil := p.Text[i]; {
	case sigil == '/' && i == 0:
		for _, s := range c.Commands {
			items = append(items, pickItem{glyph: Span{"/", a.th.Muted}, label: s})
		}
	case sigil == '@':
		// Threads first: fuzzy keeps this order between equal scores.
		for _, s := range c.Threads {
			items = append(items, pickItem{glyph: Span{"#", a.th.Iris}, label: s})
		}
		for _, s := range c.Files {
			items = append(items, pickItem{label: s})
		}
	}
	m := &menu{start: i, sigil: p.Text[i], items: fuzzy(items, string(p.Text[i+1:p.Cur]))}
	if len(m.items) == 0 {
		return
	}
	if old != nil && old.start == m.start {
		m.sel = min(old.sel, len(m.items)-1)
	}
	a.menu = m
}

// menuKey handles the keys the popup takes while it shows, and reports
// whether it took k. <Esc> closes it until the next word.
func (a *App) menuKey(k string) bool {
	m := a.menu
	if m == nil {
		return false
	}
	switch k {
	case "<C-n>", "<Down>":
		m.sel = (m.sel + 1) % len(m.items)
	case "<C-p>", "<Up>":
		m.sel = (m.sel - 1 + len(m.items)) % len(m.items)
	case "<Tab>", "<CR>":
		p := a.cur.Buf.Prompt
		word := []rune(string(m.sigil) + m.items[m.sel].label + " ")
		p.Text = append(append(append([]rune{}, p.Text[:m.start]...), word...), p.Text[p.Cur:]...)
		p.Cur = m.start + len(word)
		a.menu = nil
	case "<Esc>":
		a.menuShut = m.start
		a.menu = nil
	default:
		return false
	}
	return true
}
