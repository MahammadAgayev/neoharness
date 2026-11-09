package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/internal/model"
)

// timeoutLen is how long a partial key sequence waits, like Neovim's
// 'timeoutlen'.
const timeoutLen = time.Second

// keyName writes a key in Vim notation: "j", "<C-w>", "<CR>", "<Space>".
func keyName(ev *tcell.EventKey) string {
	switch ev.Key() {
	case tcell.KeyRune:
		r := ev.Rune()
		if r == ' ' {
			return "<Space>"
		}
		if ev.Modifiers()&tcell.ModAlt != 0 {
			return "<M-" + string(r) + ">"
		}
		return string(r)
	case tcell.KeyEnter:
		if ev.Modifiers()&(tcell.ModAlt|tcell.ModShift) != 0 {
			return "<S-CR>"
		}
		return "<CR>"
	case tcell.KeyEscape:
		return "<Esc>"
	case tcell.KeyTab:
		return "<Tab>"
	case tcell.KeyBacktab:
		return "<S-Tab>"
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		return "<BS>"
	case tcell.KeyDelete:
		return "<Del>"
	case tcell.KeyUp:
		return "<Up>"
	case tcell.KeyDown:
		return "<Down>"
	case tcell.KeyLeft:
		return "<Left>"
	case tcell.KeyRight:
		return "<Right>"
	case tcell.KeyHome:
		return "<Home>"
	case tcell.KeyEnd:
		return "<End>"
	case tcell.KeyPgUp:
		return "<PageUp>"
	case tcell.KeyPgDn:
		return "<PageDown>"
	case tcell.KeyCtrlCarat:
		return "<C-^>"
	}
	if k := ev.Key(); k >= tcell.KeyCtrlA && k <= tcell.KeyCtrlZ {
		return "<C-" + string(rune('a'+int(k-tcell.KeyCtrlA))) + ">"
	}
	return ""
}

func (a *App) key(ev *tcell.EventKey) {
	k := keyName(ev)
	if k == "" {
		return
	}
	switch a.mode {
	case modeInsert:
		a.insertKey(k, ev)
	case modeCommand:
		a.cmdKey(k, ev)
	case modePicker:
		a.pickerKey(k, ev)
	default:
		a.normalKey(k)
	}
}

// normalBindings are Normal and Visual mode sequences. Each takes the count
// typed before it (0 when none).
func (a *App) normalBindings() map[string]func(n int) {
	w := func() *Window { return a.cur }
	return map[string]func(int){
		"j":              func(n int) { a.move(w(), max(n, 1)) },
		"<Down>":         func(n int) { a.move(w(), max(n, 1)) },
		"k":              func(n int) { a.move(w(), -max(n, 1)) },
		"<Up>":           func(n int) { a.move(w(), -max(n, 1)) },
		"gg":             func(n int) { a.moveTo(w(), max(n, 1)-1) },
		"G":              func(n int) { a.goEnd(w(), n) },
		"<C-d>":          func(int) { a.move(w(), w().textH/2) },
		"<C-u>":          func(int) { a.move(w(), -w().textH/2) },
		"<C-f>":          func(int) { a.move(w(), w().textH) },
		"<PageDown>":     func(int) { a.move(w(), w().textH) },
		"<C-b>":          func(int) { a.move(w(), -w().textH) },
		"<PageUp>":       func(int) { a.move(w(), -w().textH) },
		"h":              func(n int) { w().Col = max(w().Col-max(n, 1), 0) },
		"<Left>":         func(n int) { w().Col = max(w().Col-max(n, 1), 0) },
		"l":              func(n int) { w().Col += max(n, 1); a.clampCol(w()) },
		"<Right>":        func(n int) { w().Col += max(n, 1); a.clampCol(w()) },
		"0":              func(int) { w().Col = 0 },
		"^":              func(int) { w().Col = a.line(w()).prefixWidth() },
		"$":              func(int) { w().Col = 1 << 20; a.clampCol(w()) },
		"w":              func(n int) { a.word(w(), max(n, 1)) },
		"b":              func(n int) { a.word(w(), -max(n, 1)) },
		"zt":             func(int) { w().Top = w().Cur },
		"zz":             func(int) { w().Top = max(w().Cur-w().textH/2, 0) },
		"zb":             func(int) { w().Top = max(w().Cur-w().textH+1, 0) },
		"za":             func(int) { a.toggleFold(w()) },
		"]m":             func(n int) { a.nextMsg(w(), max(n, 1)) },
		"[m":             func(n int) { a.nextMsg(w(), -max(n, 1)) },
		"]b":             func(n int) { a.cycleThread(max(n, 1)) },
		"[b":             func(n int) { a.cycleThread(-max(n, 1)) },
		"<C-^>":          func(int) { a.alternate() },
		"/":              func(int) { a.startCmd('/') },
		":":              func(int) { a.startCmd(':') },
		"n":              func(int) { a.findNext(1) },
		"N":              func(int) { a.findNext(-1) },
		"yy":             func(int) { a.yankLines(w().Cur, w().Cur) },
		"y":              func(int) { a.yankVisual() },
		"v":              func(int) { a.visual('v') },
		"V":              func(int) { a.visual('V') },
		"i":              func(int) { a.insert() },
		"a":              func(int) { a.insert() },
		"o":              func(int) { a.insert() },
		"A":              func(int) { a.insert() },
		"I":              func(int) { a.insert() },
		"<CR>":           func(int) { a.enter() },
		"<C-v>":          func(int) { a.splitFromList(true) },
		"<C-x>":          func(int) { a.splitFromList(false) },
		"<Tab>":          func(int) { a.cycleWindow(1) },
		"<S-Tab>":        func(int) { a.cycleWindow(-1) },
		"<C-w>w":         func(int) { a.cycleWindow(1) },
		"<C-w><C-w>":     func(int) { a.cycleWindow(1) },
		"<C-w>h":         func(int) { a.focusDir('h') },
		"<C-w>j":         func(int) { a.focusDir('j') },
		"<C-w>k":         func(int) { a.focusDir('k') },
		"<C-w>l":         func(int) { a.focusDir('l') },
		"<C-h>":          func(int) { a.focusDir('h') },
		"<C-j>":          func(int) { a.focusDir('j') },
		"<C-k>":          func(int) { a.focusDir('k') },
		"<C-l>":          func(int) { a.focusDir('l') },
		"<C-w>v":         func(int) { a.splitCur(true, nil) },
		"<C-w>s":         func(int) { a.splitCur(false, nil) },
		"<C-w>c":         func(int) { a.closeCur(false) },
		"<C-w>q":         func(int) { a.closeCur(false) },
		"<C-w>o":         func(int) { a.only() },
		"<C-w>=":         func(int) { a.root.equalize() },
		"<Esc>":          func(int) { a.cur.vmode = 0; a.mode = modeNormal },
		"<Space><Space>": func(int) { a.pickBuffers() },
		"<Space>t":       func(int) { a.pickThreads() },
		"<Space>p":       func(int) { a.pickProjects() },
		"<Space>/":       func(int) { a.pickGrep() },
		"<Space>w":       func(int) { a.nextWaiting() },
		"<Space>y":       func(int) { a.answer(model.Allow, "") },
		"<Space>Y":       func(int) { a.answer(model.Always, "") },
		"<Space>n":       func(int) { a.answer(model.Deny, "") },
		"<Space>r":       func(int) { a.insert() },
		"<Space>e":       func(int) { a.toggleFold(w()) },
	}
}

func (a *App) normalKey(k string) {
	if len(a.pending) == 0 && len(k) == 1 && unicode.IsDigit(rune(k[0])) && (k != "0" || a.count > 0) {
		a.count = a.count*10 + int(k[0]-'0')
		return
	}
	if a.mode == modeVisual && len(a.pending) == 0 && k == "y" {
		a.yankVisual()
		return
	}
	a.pending = append(a.pending, k)
	seq := strings.Join(a.pending, "")
	binds := a.binds
	fn, exact := binds[seq]
	longer := false
	for s := range binds {
		if len(s) > len(seq) && strings.HasPrefix(s, seq) {
			longer = true
			break
		}
	}
	switch {
	case exact && !longer:
		a.pending = nil
		n := a.count
		a.count = 0
		fn(n)
	case longer:
		a.pendSeq++
		seqID := a.pendSeq
		time.AfterFunc(timeoutLen, func() { _ = a.s.PostEvent(evKeyTimeout{seqID}) })
	default:
		a.pending = nil
		a.count = 0
	}
}

// flushPending runs a sequence that was a complete binding but also the start
// of a longer one, once the user stops typing.
func (a *App) flushPending() {
	seq := strings.Join(a.pending, "")
	a.pending = nil
	if fn, ok := a.binds[seq]; ok {
		n := a.count
		a.count = 0
		fn(n)
	}
}

// --- motions ---

func (a *App) line(w *Window) Line {
	lines := w.Buf.Lines(a.textWidth(w), a.th, a.spin)
	if w.Cur >= 0 && w.Cur < len(lines) {
		return lines[w.Cur]
	}
	return Line{}
}

func (a *App) textWidth(w *Window) int { return max(w.rect.W-1, 8) }

func (a *App) lineCount(w *Window) int {
	return len(w.Buf.Lines(a.textWidth(w), a.th, a.spin))
}

func (a *App) moveTo(w *Window, i int) {
	n := a.lineCount(w)
	w.Cur = max(min(i, n-1), 0)
	w.Follow = w.Cur >= n-1
	a.clampCol(w)
}

func (a *App) move(w *Window, d int) { a.moveTo(w, w.Cur+d) }

func (a *App) goEnd(w *Window, n int) {
	if n > 0 {
		a.moveTo(w, n-1)
		return
	}
	a.moveTo(w, a.lineCount(w)-1)
	w.Follow = true
}

func (a *App) clampCol(w *Window) {
	l := a.line(w)
	lw := l.prefixWidth() + width(l.Text())
	w.Col = max(min(w.Col, lw-1), 0)
}

// word moves by words within the line, Vim's w and b without crossing lines.
func (a *App) word(w *Window, d int) {
	l := a.line(w)
	cells := []rune(strings.Repeat(" ", l.prefixWidth()) + l.Text())
	i := min(w.Col, len(cells)-1)
	for ; d > 0; d-- {
		for i < len(cells)-1 && !unicode.IsSpace(cells[i]) {
			i++
		}
		for i < len(cells)-1 && unicode.IsSpace(cells[i]) {
			i++
		}
	}
	for ; d < 0; d++ {
		if i > 0 {
			i--
		}
		for i > 0 && unicode.IsSpace(cells[i]) {
			i--
		}
		for i > 0 && !unicode.IsSpace(cells[i-1]) {
			i--
		}
	}
	w.Col = max(i, 0)
}

func (a *App) nextMsg(w *Window, d int) {
	lines := w.Buf.Lines(a.textWidth(w), a.th, a.spin)
	i := w.Cur
	for d != 0 {
		step := 1
		if d < 0 {
			step = -1
		}
		j := i + step
		for j >= 0 && j < len(lines) && !lines[j].Msg {
			j += step
		}
		if j < 0 || j >= len(lines) {
			break
		}
		i = j
		d -= step
	}
	a.moveTo(w, i)
}

func (a *App) toggleFold(w *Window) {
	if !w.Buf.Toggle(a.line(w).Block) {
		a.notify("no fold here", a.th.Muted)
	}
}

// --- windows ---

func (a *App) cycleWindow(d int) {
	wins := a.root.windows()
	for i, w := range wins {
		if w == a.cur {
			a.cur = wins[(i+d+len(wins))%len(wins)]
			return
		}
	}
}

func (a *App) focusDir(dir rune) {
	if w := neighbor(a.root.windows(), a.cur, dir); w != nil {
		a.cur = w
	}
}

func (a *App) splitCur(vert bool, b *Buffer) {
	if b == nil {
		b = a.cur.Buf
	}
	nw := newWindow(b)
	a.root.splitWindow(a.cur, nw, vert)
	a.cur = nw
}

func (a *App) closeCur(force bool) {
	if !a.root.closeWindow(a.cur) {
		a.quitAll(force)
		return
	}
	a.cur = a.root.windows()[0]
}

// quitAll quits, unless that stops running turns and force is not set. The
// hub lives in this process, so quitting ends every turn in flight.
func (a *App) quitAll(force bool) {
	n := 0
	for _, p := range a.projects {
		n += p.Counts[model.StateRunning]
	}
	if n > 0 && !force {
		a.notify(fmt.Sprintf("%d running · :qa! stops them", n), a.th.Gold)
		return
	}
	a.quit = true
}

func (a *App) only() {
	a.cur.rect = Rect{}
	a.root = leaf(a.cur, 1)
}

func (a *App) alternate() {
	if a.cur.Alt != nil {
		a.cur.show(a.cur.Alt)
	}
}

// cycleThread shows the next or previous thread in the current window.
func (a *App) cycleThread(d int) {
	names := append([]string{model.Coordinator}, a.listOrder()...)
	if len(names) == 0 || a.project == "" {
		return
	}
	i := 0
	if a.cur.Buf.Kind == KindChat {
		for j, n := range names {
			if n == a.cur.Buf.Thread {
				i = j
			}
		}
	}
	a.cur.show(a.chat(names[(i+d%len(names)+len(names))%len(names)]))
}

func (a *App) enter() {
	l := a.line(a.cur)
	switch a.cur.Buf.Kind {
	case KindProjects:
		if l.Item != "" {
			a.openProject(l.Item)
		}
	case KindThreads:
		if l.Item != "" {
			a.showThread(l.Item)
		}
	default:
		a.cur.Buf.Toggle(l.Block)
	}
}

// splitFromList opens the thread under the cursor of the threads list in a
// split, as <C-v> and <C-x> do in a picker.
func (a *App) splitFromList(vert bool) {
	if a.cur.Buf.Kind != KindThreads {
		return
	}
	if l := a.line(a.cur); l.Item != "" {
		a.splitThread(l.Item, vert)
	}
}

func (a *App) nextWaiting() {
	var waiting []string
	for _, n := range append([]string{model.Coordinator}, a.listOrder()...) {
		if a.threads[n].State == model.StateWaiting {
			waiting = append(waiting, n)
		}
	}
	if len(waiting) == 0 {
		a.notify("nothing is waiting for you", a.th.Muted)
		return
	}
	a.waitNext %= len(waiting)
	a.showThread(waiting[a.waitNext])
	a.waitNext++
}

// --- insert ---

func (a *App) insert() {
	if a.cur.Buf.Prompt == nil {
		a.notify("this buffer is read-only; i writes in a thread or the coordinator", a.th.Muted)
		return
	}
	if v := a.threads[a.cur.Buf.Thread]; v.State == model.StateResolved {
		a.notify(v.Name+" is resolved · :reopen to write to it", a.th.Muted)
		return
	}
	a.mode = modeInsert
	a.cur.vmode = 0
	a.cur.Follow = true
}

func (a *App) insertKey(k string, ev *tcell.EventKey) {
	p := a.cur.Buf.Prompt
	if p == nil {
		a.mode = modeNormal
		return
	}
	switch k {
	case "<Esc>":
		a.mode = modeNormal
	case "<CR>":
		if a.pasting {
			p.insert('\n')
			return
		}
		a.send()
	case "<S-CR>", "<C-j>", "<M-\r>":
		p.insert('\n')
	case "<BS>":
		if p.Cur > 0 {
			p.Text = append(p.Text[:p.Cur-1], p.Text[p.Cur:]...)
			p.Cur--
		}
	case "<Del>":
		if p.Cur < len(p.Text) {
			p.Text = append(p.Text[:p.Cur], p.Text[p.Cur+1:]...)
		}
	case "<Left>", "<C-b>":
		p.Cur = max(p.Cur-1, 0)
	case "<Right>", "<C-f>":
		p.Cur = min(p.Cur+1, len(p.Text))
	case "<Home>", "<C-a>":
		p.Cur = 0
	case "<End>", "<C-e>":
		p.Cur = len(p.Text)
	case "<C-u>":
		p.Text = append([]rune{}, p.Text[p.Cur:]...)
		p.Cur = 0
	case "<C-w>":
		i := p.Cur
		for i > 0 && unicode.IsSpace(p.Text[i-1]) {
			i--
		}
		for i > 0 && !unicode.IsSpace(p.Text[i-1]) {
			i--
		}
		p.Text = append(p.Text[:i], p.Text[p.Cur:]...)
		p.Cur = i
	case "<Tab>":
		p.insert('\t')
	default:
		if ev.Key() == tcell.KeyRune {
			p.insert(ev.Rune())
		}
	}
}

func (p *Prompt) insert(r rune) {
	p.Text = append(p.Text[:p.Cur], append([]rune{r}, p.Text[p.Cur:]...)...)
	p.Cur++
}

func (a *App) send() {
	b := a.cur.Buf
	text := strings.TrimSpace(string(b.Prompt.Text))
	if text == "" {
		return
	}
	if err := a.c.Send(b.Project, b.Thread, text); err != nil {
		a.notify(err.Error(), a.th.Love)
		return
	}
	b.Prompt.Text, b.Prompt.Cur = nil, 0
	a.cur.Follow = true
}

// --- permissions ---

// answer replies to the permission request of the thread in focus, or of the
// first window on screen that has one.
func (a *App) answer(d model.Decision, msg string) {
	name := ""
	for _, w := range append([]*Window{a.cur}, a.root.windows()...) {
		if w.Buf.Kind == KindChat && a.threads[w.Buf.Thread].Permission != nil {
			name = w.Buf.Thread
			break
		}
	}
	if name == "" {
		a.notify("no permission request in this window · <leader>w finds one", a.th.Muted)
		return
	}
	v := a.threads[name]
	if err := a.c.Answer(a.project, name, v.Permission.ID, d, msg); err != nil {
		a.notify(err.Error(), a.th.Love)
		return
	}
	a.notify(string(d)+" · "+v.Permission.Tool+" · "+name, a.th.Pine)
}

// --- visual and yank ---

func (a *App) visual(m rune) {
	w := a.cur
	if a.mode == modeVisual && w.vmode == m {
		w.vmode = 0
		a.mode = modeNormal
		return
	}
	if a.mode != modeVisual {
		w.vLine, w.vCol = w.Cur, w.Col
	}
	w.vmode = m
	a.mode = modeVisual
}

func (a *App) yankVisual() {
	w := a.cur
	if a.mode != modeVisual {
		return
	}
	l1, c1, l2, c2 := w.vLine, w.vCol, w.Cur, w.Col
	if l2 < l1 || l2 == l1 && c2 < c1 {
		l1, c1, l2, c2 = l2, c2, l1, c1
	}
	if w.vmode == 'V' {
		a.yankLines(l1, l2)
	} else {
		lines := w.Buf.Lines(a.textWidth(w), a.th, a.spin)
		var parts []string
		for i := l1; i <= l2 && i < len(lines); i++ {
			pw := lines[i].prefixWidth()
			from, to := 0, 1<<20
			if i == l1 {
				from = max(c1-pw, 0)
			}
			if i == l2 {
				to = max(c2-pw+1, 0)
			}
			parts = append(parts, cellSlice(lines[i].Text(), from, to))
		}
		a.clip(strings.Join(parts, "\n"), l2-l1+1)
	}
	w.vmode = 0
	a.mode = modeNormal
}

func (a *App) yankLines(l1, l2 int) {
	lines := a.cur.Buf.Lines(a.textWidth(a.cur), a.th, a.spin)
	var parts []string
	for i := l1; i <= l2 && i < len(lines); i++ {
		parts = append(parts, strings.TrimRight(lines[i].Text(), " "))
	}
	a.clip(strings.Join(parts, "\n"), l2-l1+1)
}

// clip copies to the system clipboard with OSC 52, which works inside tmux
// and over ssh where no local clipboard tool can.
func (a *App) clip(s string, n int) {
	a.s.SetClipboard([]byte(s))
	if n == 1 {
		a.notify("yanked 1 line", a.th.Subtle)
	} else {
		a.notify(fmt.Sprintf("yanked %d lines", n), a.th.Subtle)
	}
}

// cellSlice cuts s between two cell positions.
func cellSlice(s string, from, to int) string {
	var b strings.Builder
	c := 0
	for _, r := range s {
		rw := width(string(r))
		if c >= from && c+rw <= to {
			b.WriteRune(r)
		}
		c += rw
	}
	return b.String()
}
