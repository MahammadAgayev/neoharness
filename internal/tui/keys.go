package tui

import (
	"fmt"
	"slices"
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
	// A fast <Esc> and the next key reach us as one Alt+key. Nothing is bound
	// to Alt with a printable rune, so they are the two keys, as Neovim reads
	// them: <Esc>:w in Insert saves, and <Esc>: in Normal starts a command.
	if ev.Key() == tcell.KeyRune && ev.Modifiers()&tcell.ModAlt != 0 && unicode.IsPrint(ev.Rune()) {
		a.key(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
		a.key(tcell.NewEventKey(tcell.KeyRune, ev.Rune(), tcell.ModNone))
		return
	}
	// The same for a control key: <Esc><C-o> leaves Visual, then goes back.
	// Alt+<CR> is not split; it is a new line in Insert.
	if c := ev.Key(); ev.Modifiers()&tcell.ModAlt != 0 && (c >= tcell.KeyCtrlA && c <= tcell.KeyCtrlZ && c != tcell.KeyEnter || c == tcell.KeyCtrlCarat) {
		a.key(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
		a.key(tcell.NewEventKey(c, ev.Rune(), ev.Modifiers()&^tcell.ModAlt))
		return
	}
	k := keyName(ev)
	if k == "" {
		return
	}
	if run := a.confirm; run != nil {
		a.confirm = nil
		a.notice = ""
		if k == "y" {
			run()
		}
		return
	}
	// Changes are recorded in an artifact's source and on its document,
	// where Insert starts in the source and ends back on the document.
	b := a.cur.Buf
	ed := b.Edit
	recording := (b.Edit != nil || b.Kind == KindArtifact || a.cur.inPrompt) && !a.replaying
	if ed == nil && b.Prompt != nil {
		ed = b.Prompt.ed
	}
	idle := func() bool {
		return a.mode == modeNormal && len(a.pending) == 0 && a.count == 0 && !a.replacing && a.finding == "" && a.selecting == "" && a.marking == "" && a.macroKey == ""
	}
	if a.recordInto != 0 && a.playing == 0 {
		if k == "q" && idle() {
			a.recordInto = 0
			return
		}
		a.macros[a.recordInto] = append(a.macros[a.recordInto], ev)
	}
	if a.insertMap(ev, k) {
		return
	}
	if recording {
		if idle() {
			a.rec, a.recRev = nil, 0
			if ed != nil {
				a.recRev = ed.rev
			}
		}
		a.rec = append(a.rec, ev)
	}
	a.dispatch(k, ev)
	// A window that shows another buffer drops its selection.
	if a.mode == modeVisual && a.cur.vmode == 0 {
		a.mode = modeNormal
	}
	a.settle(a.cur)
	// A change ends back in Normal; its keys are what `.` types again.
	if recording && idle() && a.cur.Buf == b {
		e := b.Edit
		if e == nil && b.Prompt != nil {
			e = b.Prompt.ed
		}
		if e != nil && (e != ed && e.Dirty || e == ed && e.rev != a.recRev) && !a.noRepeat {
			a.lastChange = slices.Clone(a.rec)
		}
		a.noRepeat = false
	}
}

func (a *App) dispatch(k string, ev *tcell.EventKey) {
	if a.replacing {
		a.replacing = false
		if ev.Key() == tcell.KeyRune {
			a.replace(ev.Rune(), a.replaceN)
		}
		return
	}
	if m := a.marking; m != "" {
		a.marking = ""
		if ev.Key() == tcell.KeyRune {
			a.mark(m, ev.Rune())
		}
		return
	}
	if s := a.selecting; s != "" {
		a.selecting = ""
		a.selectObject(s, k)
		return
	}
	if f := a.finding; f != "" {
		a.finding = ""
		if ev.Key() == tcell.KeyRune {
			a.find(f, ev.Rune(), a.findN)
		}
		return
	}
	if m := a.macroKey; m != "" {
		a.macroKey = ""
		if ev.Key() == tcell.KeyRune {
			a.macro(m, ev.Rune(), a.macroN)
		}
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
	binds := map[string]func(int){
		"j":          func(n int) { a.move(w(), max(n, 1)) },
		"<Down>":     func(n int) { a.move(w(), max(n, 1)) },
		"k":          func(n int) { a.move(w(), -max(n, 1)) },
		"<Up>":       func(n int) { a.move(w(), -max(n, 1)) },
		"gg":         func(n int) { a.moveTo(w(), max(n, 1)-1) },
		"gx":         func(int) { a.openLink() },
		"gf":         func(int) { a.openArtifactAt() },
		"gO":         func(int) { a.outline() },
		"G":          func(n int) { a.goEnd(w(), n) },
		"<C-d>":      func(int) { a.move(w(), w().textH/2); center(w()) },
		"<C-u>":      func(int) { a.move(w(), -w().textH/2); center(w()) },
		"<C-f>":      func(int) { a.move(w(), w().textH) },
		"<PageDown>": func(int) { a.move(w(), w().textH) },
		"<C-b>":      func(int) { a.move(w(), -w().textH) },
		"<PageUp>":   func(int) { a.move(w(), -w().textH) },
		"h":          func(n int) { w().Col = max(w().Col-max(n, 1), 0); a.clampCol(w()) },
		"<Left>":     func(n int) { w().Col = max(w().Col-max(n, 1), 0); a.clampCol(w()) },
		"l":          func(n int) { w().Col += max(n, 1); a.clampCol(w()) },
		"<Right>":    func(n int) { w().Col += max(n, 1); a.clampCol(w()) },
		"0":          func(int) { w().Col = 0; a.clampCol(w()) },
		"^":          func(int) { w().Col = a.line(w()).prefixWidth() },
		"$":          func(int) { w().Col = 1 << 20; a.clampCol(w()) },
		"w":          func(n int) { a.word(w(), max(n, 1)) },
		"b":          func(n int) { a.word(w(), -max(n, 1)) },
		"e":          func(n int) { a.wordEnd(w(), max(n, 1)) },
		"zt":         func(int) { w().Top = w().Cur },
		"zz":         func(int) { center(w()) },
		"zb":         func(int) { w().Top = max(w().Cur-w().textH+1, 0) },
		"za":         func(int) { a.toggleFold(w()) },
		"]m":         func(n int) { a.nextMsg(w(), max(n, 1)) },
		"[m":         func(n int) { a.nextMsg(w(), -max(n, 1)) },
		"]b":         func(n int) { a.cycleThread(max(n, 1)) },
		"[b":         func(n int) { a.cycleThread(-max(n, 1)) },
		"<C-^>":      func(int) { a.alternate() },
		"<C-o>":      func(int) { a.jumpBack() },
		"]c":         func(n int) { a.diffJump(max(n, 1)) },
		"[c":         func(n int) { a.diffJump(-max(n, 1)) },
		"do":         func(int) { a.diffTake() },
		"dp":         func(int) { a.diffTake() },
		"/":          func(int) { a.startCmd('/') },
		"?":          func(int) { a.startCmd('?') },
		":":          func(int) { a.startCmd(':') },
		"n":          func(int) { a.findNext(a.searchDir()); center(w()) },
		"N":          func(int) { a.findNext(-a.searchDir()); center(w()) },
		"yy":         func(n int) { a.yy(n) },
		"y":          func(int) { a.yankVisual() },
		"v":          func(int) { a.visual('v') },
		"V":          func(int) { a.visual('V') },
		"i":          func(int) { a.insert('i') },
		"a":          func(int) { a.insert('a') },
		"o":          func(int) { a.insert('o') },
		"O":          func(int) { a.insert('O') },
		"A":          func(int) { a.insert('A') },
		"I":          func(int) { a.insert('I') },
		"x":          func(n int) { a.editNormal("x", n) },
		"D":          func(n int) { a.editNormal("D", n) },
		"dd":         func(n int) { a.editNormal("dd", n) },
		"p":          func(n int) { a.editNormal("p", n) },
		"P":          func(n int) { a.editNormal("P", n) },
		"u":          func(n int) { a.editNormal("u", n) },
		"<C-r>":      func(n int) { a.editNormal("<C-r>", n) },
		"J":          func(n int) { a.editNormal("J", n) },
		"s":          func(n int) { a.editNormal("s", n) },
		"S":          func(n int) { a.editNormal("S", n) },
		"C":          func(n int) { a.editNormal("C", n) },
		"cc":         func(n int) { a.editNormal("cc", n) },
		".":          func(int) { a.repeat() },
		"r":          func(n int) { a.replacing, a.replaceN = a.cur.Buf.Edit != nil || a.cur.inPrompt, n },
		"%":          func(int) { a.matchPair() },
		"m":          func(int) { a.marking = "m" },
		"'":          func(int) { a.marking = "'" },
		"`":          func(int) { a.marking = "`" },
		";":          func(n int) { a.findAgain(false, n) },
		",":          func(n int) { a.findAgain(true, n) },
		"<CR>":       func(int) { a.enter() },
		"<C-v>":      func(int) { a.splitFromList(true) },
		"<C-x>":      func(int) { a.splitFromList(false) },
		"<C-w>w":     func(int) { a.cycleWindow(1) },
		"<C-w><C-w>": func(int) { a.cycleWindow(1) },
		"<C-w>W":     func(int) { a.cycleWindow(-1) },
		"<C-w>h":     func(int) { a.focusDir('h') },
		"<C-w>j":     func(int) { a.focusDir('j') },
		"<C-w>k":     func(int) { a.focusDir('k') },
		"<C-w>l":     func(int) { a.focusDir('l') },
		"<C-w>v":     func(int) { a.splitCur(true, nil) },
		"<C-w>s":     func(int) { a.splitCur(false, nil) },
		"<C-w>c":     func(int) { a.closeCur(false) },
		"<C-w>q":     func(int) { a.closeCur(false) },
		"<C-w>o":     func(int) { a.only() },
		"<C-w>=":     func(int) { a.root.equalize(true); a.root.equalize(false) },
		"<Esc>":      func(int) { a.escape() },
		"q":          func(int) { a.macroKey = "q" },
		"@":          func(n int) { a.macroKey, a.macroN = "@", n },
		// Actions with no ex command, for maps in defaults.lua and init.lua.
		"<Plug>(neoharness-grep)":        func(int) { a.pickGrep() },
		"<Plug>(neoharness-threads)":     func(int) { a.pickThreads() },
		"<Plug>(neoharness-yank)":        func(int) { a.yank() },
		"<Plug>(neoharness-dismiss)":     func(int) { a.toasts = nil },
		"<Plug>(neoharness-coordinator)": func(int) { a.goCoordinator() },
	}
	for _, f := range []string{"f", "F", "t", "T"} {
		binds[f] = func(n int) { a.finding, a.findN = f, n }
	}
	for _, op := range []string{"d", "c", "y"} {
		for _, m := range []string{"w", "e", "b", "$", "0", "^", "iw", "aw", "ip", "ap"} {
			k := op + m
			binds[k] = func(n int) { a.editNormal(k, n) }
		}
		for _, f := range []string{"f", "F", "t", "T"} {
			k := op + f
			binds[k] = func(n int) { a.finding, a.findN = k, n }
		}
		binds[op+"%"] = func(n int) { a.editNormal(op+"%", n) }
		for obj := range textObjects {
			for _, ia := range []string{"i", "a"} {
				k := op + ia + string(obj)
				binds[k] = func(n int) { a.editNormal(k, n) }
			}
		}
	}
	return binds
}

// yy yanks the line; in an artifact's source, into the register too.
func (a *App) yy(n int) {
	if a.cur.Buf.Edit != nil || a.cur.inPrompt {
		a.editNormal("yy", n)
		return
	}
	a.yankLines(a.cur.Cur, a.cur.Cur)
}

// maxMacroDepth stops a macro that plays itself.
const maxMacroDepth = 100

// macro is q{a-z}, which records the keys that follow into a register until
// the next q, and @{a-z}, which types them again n times. @@ plays the last
// one again.
func (a *App) macro(m string, r rune, n int) {
	if m == "q" {
		if r >= 'a' && r <= 'z' {
			if a.macros == nil {
				a.macros = map[rune][]*tcell.EventKey{}
			}
			a.recordInto, a.macros[r] = r, nil
		}
		return
	}
	if r == '@' {
		r = a.lastMacro
	}
	keys := a.macros[r]
	if len(keys) == 0 || a.playing >= maxMacroDepth {
		return
	}
	a.lastMacro = r
	a.playing++
	for range max(n, 1) {
		for _, ev := range keys {
			a.key(ev)
		}
	}
	a.playing--
}

// repeat is `.`: it types the last change in an artifact again.
func (a *App) repeat() {
	if a.cur.Buf.Kind != KindArtifact && !a.cur.inPrompt || len(a.lastChange) == 0 {
		return
	}
	a.replaying = true
	for _, ev := range a.lastChange {
		a.key(ev)
	}
	a.replaying = false
	a.noRepeat = true
}

func (a *App) normalKey(k string) {
	if a.mode == modeVisual && len(a.pending) == 0 && (k == "i" || k == "a") {
		a.selecting = k
		return
	}
	if len(a.pending) == 0 && len(k) == 1 && unicode.IsDigit(rune(k[0])) && (k != "0" || a.count > 0) {
		a.count = a.count*10 + int(k[0]-'0')
		return
	}
	if a.mode == modeVisual && len(a.pending) == 0 && k == "y" {
		a.yankVisual()
		return
	}
	if a.mode == modeVisual && len(a.pending) == 0 && (a.cur.Buf.Edit != nil || a.cur.inPrompt) && (k == "d" || k == "x" || k == "c") {
		a.editVisual(k)
		return
	}
	a.pending = append(a.pending, k)
	seq := strings.Join(a.pending, "")
	binds := a.bindings()
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
		a.run(seq, fn, n)
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
	if fn, ok := a.bindings()[seq]; ok {
		n := a.count
		a.count = 0
		a.run(seq, fn, n)
	}
}

// run calls a binding. A key that reads or moves in the transcript takes
// the cursor out of the prompt first.
func (a *App) run(seq string, fn func(int), n int) {
	if a.cur.inPrompt && transcriptKeys[seq] && a.mode != modeVisual {
		a.leavePrompt(a.cur)
	}
	fn(n)
}

// --- motions ---

func (a *App) line(w *Window) Line {
	lines := a.rows(w)
	if w.Cur >= 0 && w.Cur < len(lines) {
		return lines[w.Cur]
	}
	return Line{}
}

func (a *App) textWidth(w *Window) int { return max(w.rect.W-1-railWidth(w), 8) }

func (a *App) lineCount(w *Window) int { return len(a.rows(w)) }

// rows are the rows the cursor moves over: the prompt's while it is there.
func (a *App) rows(w *Window) []Line {
	if w.inPrompt {
		a.fitPrompt(w)
		return a.promptRows(w)
	}
	return w.Buf.Lines(a.textWidth(w), a.th, a.spin)
}

func (a *App) moveTo(w *Window, i int) {
	n := a.lineCount(w)
	// k above the prompt goes back to the transcript; j below the
	// transcript goes into the prompt, as the prompt ends the buffer.
	if w.inPrompt {
		// A selection in the prompt stays in it.
		if i < 0 && a.mode == modeVisual {
			i = 0
		}
		if i < 0 {
			a.leavePrompt(w)
			a.moveTo(w, a.lineCount(w)+i)
			return
		}
		w.Cur = min(i, n-1)
		a.clampCol(w)
		return
	}
	if i >= n && n > 0 && w.Buf.Prompt != nil && a.mode == modeNormal {
		w.Cur = n - 1
		a.enterPrompt(w)
		a.moveTo(w, i-n)
		return
	}
	w.Cur = max(min(i, n-1), 0)
	if w.Buf.Edit != nil {
		w.Cur = max(w.Cur, metaRows)
	}
	w.Follow = w.Cur >= n-1
	a.clampCol(w)
}

func (a *App) move(w *Window, d int) { a.moveTo(w, w.Cur+d) }

// center puts the cursor line in the middle of the window, as zz does.
func center(w *Window) { w.Top = max(w.Cur-w.textH/2, 0) }

func (a *App) goEnd(w *Window, n int) {
	if n > 0 {
		a.leavePrompt(w)
		a.moveTo(w, n-1)
		return
	}
	// The last line of a chat is the prompt's.
	if w.Buf.Prompt != nil && a.mode == modeNormal {
		a.enterPrompt(w)
	}
	a.moveTo(w, a.lineCount(w)-1)
	w.Follow = true
}

func (a *App) clampCol(w *Window) {
	l := a.line(w)
	lw := l.prefixWidth() + width(l.Text())
	w.Col = max(min(w.Col, lw-1), 0)
	// The cursor sits on the first column of a wide character.
	rs := []rune(strings.Repeat(" ", l.prefixWidth()) + l.Text())
	w.Col = width(string(rs[:runeAt(rs, w.Col)]))
	// In an artifact's source and the prompt the margin is not text.
	if w.Buf.Edit != nil || w.inPrompt {
		w.Col = max(w.Col, l.prefixWidth())
	}
}

// word moves by words within the line, Vim's w and b without crossing lines.
func (a *App) word(w *Window, d int) {
	l := a.line(w)
	rs := []rune(strings.Repeat(" ", l.prefixWidth()) + l.Text())
	i := min(runeAt(rs, w.Col), len(rs)-1)
	for ; d > 0; d-- {
		i = min(wordFwd(rs, i), len(rs)-1)
	}
	for ; d < 0; d++ {
		i = wordBack(rs, i)
	}
	w.Col = width(string(rs[:max(i, 0)]))
	a.clampCol(w)
}

// wordEnd is Vim's e within the line.
func (a *App) wordEnd(w *Window, n int) {
	l := a.line(w)
	rs := []rune(strings.Repeat(" ", l.prefixWidth()) + l.Text())
	i := runeAt(rs, w.Col)
	for range n {
		i = wordEnd(rs, i)
	}
	w.Col = width(string(rs[:min(max(i, 0), len(rs))]))
	a.clampCol(w)
}

// find runs f F t T with the rune r: a move on the screen row, or the reach
// of an operator in an artifact's source (df, ct).
func (a *App) find(f string, r rune, n int) { a.findFrom(f, r, n, false) }

func (a *App) findFrom(f string, r rune, n int, again bool) {
	kind := f[len(f)-1:]
	a.lastFind = kind + string(r)
	if len(f) > 1 {
		a.editNormal(f[:1]+kind+string(r), n)
		return
	}
	w := a.cur
	l := a.line(w)
	rs := []rune(strings.Repeat(" ", l.prefixWidth()) + l.Text())
	if j, ok := findRune(rs, runeAt(rs, w.Col), kind[0], r, max(n, 1), again); ok {
		w.Col = width(string(rs[:j]))
		a.clampCol(w)
	}
}

// mark is m{a-z}, '{a-z} and `{a-z}: set a mark in the buffer, go to its
// line, or to its place. ” and “ go back to where the last jump started.
func (a *App) mark(m string, r rune) {
	w := a.cur
	b := w.Buf
	if b.marks == nil {
		b.marks = map[rune][2]int{}
	}
	// An artifact's rows move as its lines show raw or rendered, so its
	// marks are places in the source.
	if e := b.Edit; e != nil {
		a.fromWindow(w)
		p, ok := b.marks[r]
		switch {
		case m == "m" && r >= 'a' && r <= 'z':
			b.marks[r] = [2]int{e.Line, e.Off}
		case m == "m":
		case r != '`' && r != '\'' && !ok:
			a.notify("mark not set", a.th.Muted)
		default:
			if r == '`' || r == '\'' {
				p, ok = b.marks['\'']
				if !ok {
					return
				}
			}
			b.marks['\''] = [2]int{e.Line, e.Off}
			e.Line, e.Off = p[0], p[1]
			if m == "'" {
				e.Off = firstNonBlank(e.Src[min(p[0], len(e.Src)-1)])
			}
			e.shown = min(p[0], len(e.Src)-1)
			e.shownTo = e.shown
			a.toWindow(w)
		}
		return
	}
	if m == "m" {
		if r >= 'a' && r <= 'z' {
			b.marks[r] = [2]int{w.Cur, w.Col}
		}
		return
	}
	if r == '`' {
		r = '\''
	}
	p, ok := b.marks[r]
	if !ok {
		a.notify("mark not set", a.th.Muted)
		return
	}
	b.marks['\''] = [2]int{w.Cur, w.Col}
	a.moveTo(w, p[0])
	w.Follow = false
	if m == "`" {
		w.Col = p[1]
	} else {
		w.Col = a.line(w).prefixWidth()
	}
	a.clampCol(w)
}

// matchPair is %: from the bracket at or after the cursor on the row to the
// one that matches it.
func (a *App) matchPair() {
	w := a.cur
	l := a.line(w)
	rs := []rune(strings.Repeat(" ", l.prefixWidth()) + l.Text())
	if j, ok := matchBracket(rs, runeAt(rs, w.Col)); ok {
		w.Col = width(string(rs[:j]))
		a.clampCol(w)
	}
}

// selectObject is a text object in Visual (iw aw i( a" …): it selects it on
// the screen row.
func (a *App) selectObject(ia, k string) {
	w := a.cur
	l := a.line(w)
	pre := l.prefixWidth()
	cells := []rune(l.Text())
	i := runeAt(cells, max(w.Col-pre, 0))
	var from, to int
	ok := true
	if k == "p" {
		lines := a.rows(w)
		blank := func(i int) bool { return strings.TrimSpace(lines[i].Text()) == "" }
		if len(lines) == 0 {
			return
		}
		w.vLine, w.Cur = paragraph(len(lines), w.Cur, ia == "a", blank)
		w.vmode = 'V'
		return
	}
	switch obj := []rune(k); {
	case k == "w" && ia == "i":
		from, to = innerWord(cells, i)
	case k == "w":
		from, to = aroundWord(cells, i)
	case len(obj) == 1 && obj[0] < 128:
		pair, known := textObjects[byte(obj[0])]
		if !known {
			return
		}
		if pair[0] == pair[1] {
			from, to, ok = quoted(cells, i, pair[0])
		} else {
			from, to, ok = bracketed(cells, i, pair[0], pair[1])
		}
		if ia == "i" {
			from++
		} else {
			to++
		}
	default:
		return
	}
	if !ok || to <= from {
		return
	}
	w.vLine, w.vCol = w.Cur, pre+width(string(cells[:from]))
	w.Col = pre + width(string(cells[:to])) - 1
	w.vmode = 'v'
}

// findAgain is ; and its reverse ,.
func (a *App) findAgain(reverse bool, n int) {
	if a.lastFind == "" {
		return
	}
	last := a.lastFind
	kind, r := last[0], []rune(last[1:])[0]
	if reverse {
		kind = map[byte]byte{'f': 'F', 'F': 'f', 't': 'T', 'T': 't'}[kind]
	}
	a.findFrom(string(kind), r, n, true)
	a.lastFind = last
}

// pairs maps each bracket to the one that closes or opens it.
var pairs = map[rune]rune{'(': ')', ')': '(', '[': ']', ']': '[', '{': '}', '}': '{'}

// matchBracket is where % lands from i: the first bracket at or after i,
// then the one that matches it, counting nested ones.
func matchBracket(l []rune, i int) (int, bool) {
	for ; i < len(l); i++ {
		if _, ok := pairs[l[i]]; ok {
			break
		}
	}
	if i >= len(l) {
		return 0, false
	}
	open, close, step := l[i], pairs[l[i]], 1
	if open == ')' || open == ']' || open == '}' {
		step = -1
	}
	for depth, j := 0, i+step; j >= 0 && j < len(l); j += step {
		switch l[j] {
		case open:
			depth++
		case close:
			if depth == 0 {
				return j, true
			}
			depth--
		}
	}
	return 0, false
}

// findRune is where f F t T with r land from i, the nth match: f on it, t
// just before it, F and T the same backwards. again (; and ,) makes t skip
// a match right next to the cursor, so it goes on to the next one.
func findRune(l []rune, i int, kind byte, r rune, n int, again bool) (int, bool) {
	step, from := 1, i+1
	if kind == 'F' || kind == 'T' {
		step, from = -1, i-1
	}
	if again && (kind == 't' || kind == 'T') {
		from += step
	}
	for j := from; j >= 0 && j < len(l); j += step {
		if l[j] != r {
			continue
		}
		if n--; n > 0 {
			continue
		}
		switch kind {
		case 't':
			return j - 1, true
		case 'T':
			return j + 1, true
		}
		return j, true
	}
	return i, false
}

// goCoordinator focuses the coordinator of the open workspace, as <leader>c.
// The pane stays as it is.
func (a *App) goCoordinator() {
	if a.workspace == "" {
		a.notify("open a workspace first · <leader>w", a.th.Muted)
		return
	}
	if w := a.windowOf(model.Coordinator); w != nil {
		a.cur = w
		return
	}
	for _, w := range a.root.windows() {
		if w != a.pane {
			a.cur = w
			break
		}
	}
	a.cur.show(a.chat(model.Coordinator))
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

// outline is gO: a picker of the window's headings, or its messages in a
// chat, as Neovim's gO lists a document's outline.
func (a *App) outline() {
	w := a.cur
	var items []pickItem
	// here is the entry the cursor is in, the last one at or above it.
	here := 0
	lines := w.Buf.Lines(a.textWidth(w), a.th, a.spin)
	for i, l := range lines {
		if !l.Msg {
			continue
		}
		if i <= w.Cur {
			here = len(items)
		}
		label := strings.TrimSpace(l.Text())
		// A thread's message and a question start with a label line ("from
		// lint", "question"); the line under it says what it is about.
		if w.Buf.Edit == nil && l.Block < len(w.Buf.blocks) && i+1 < len(lines) {
			if bl := w.Buf.blocks[l.Block]; bl.kind == bAsk || bl.kind == bMessage && bl.from != model.User {
				label = strings.TrimPrefix(label, "from ") + ": " + strings.TrimSpace(lines[i+1].Text())
			}
		}
		items = append(items, pickItem{
			glyph: Span{"─", a.th.Muted},
			label: label,
			run:   func() { a.moveTo(w, i); w.Follow = false; center(w) },
		})
	}
	if len(items) == 0 {
		a.notify("no headings here", a.th.Muted)
		return
	}
	a.openPicker("outline", func(q string) []pickItem { return fuzzy(items, q) })
	a.picker.sel = here
}

// openArtifactAt is gf: it opens the artifact named under the cursor.
func (a *App) openArtifactAt() {
	if name := a.artifactAt(a.line(a.cur), a.cur.Col); name != "" {
		a.openArtifact(name)
		return
	}
	a.notify("no artifact name under the cursor", a.th.Muted)
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
	a.equalAlways(vert)
	a.cur = nw
	if b.Edit != nil {
		nw.Follow = false
		a.toWindow(nw)
	}
}

// escape leaves Visual.
func (a *App) escape() {
	if a.mode == modeVisual {
		a.cur.vmode = 0
		a.mode = modeNormal
	}
}

func (a *App) closeCur(force bool) {
	if e := a.cur.Buf.diffOf; e != nil && e.diff != nil && e.diff.win == a.cur {
		a.closeDiff(e)
		return
	}
	if e := a.cur.Buf.Edit; e != nil {
		// Edits no window shows are forgotten, and the agents' new versions
		// then warn about them out of the blue.
		if e.Dirty && !force && !a.shownElsewhere(a.cur) {
			a.notify("no write since the last change · :w saves, :q! drops it", a.th.Gold)
			return
		}
		a.closeDiff(e)
	}
	to, vert := a.root.closeWindow(a.cur)
	if to == nil {
		a.quitAll(force)
		return
	}
	a.equalAlways(vert)
	a.cur = to
}

// quitAll quits, unless that stops running turns and force is not set. The
// hub lives in this process, so quitting ends every turn in flight.
func (a *App) quitAll(force bool) {
	// In the open workspace every thread is known, the coordinator too, and a
	// thread at a permission card still has its turn in flight.
	n := 0
	for _, v := range a.threads {
		if v.State == model.StateRunning || v.Permission != nil {
			n++
		}
	}
	for _, p := range a.workspaces {
		if p.Name != a.workspace {
			n += p.Counts[model.StateRunning]
		}
	}
	if n > 0 && !force {
		a.notify(fmt.Sprintf("%d running · :qa! stops them", n), a.th.Gold)
		return
	}
	for _, b := range a.bufs {
		if e := b.Edit; e != nil && e.Dirty && !force {
			a.notify("unsaved changes in "+b.Artifact+" · :w saves, :qa! drops them", a.th.Gold)
			return
		}
	}
	a.quit = true
}

// shownElsewhere reports whether another window shows w's buffer.
func (a *App) shownElsewhere(w *Window) bool {
	for _, o := range a.root.windows() {
		if o != w && o.Buf == w.Buf {
			return true
		}
	}
	return false
}

func (a *App) only() {
	a.cur.rect = Rect{}
	a.root = leaf(a.cur, 1)
}

func (a *App) alternate() {
	if a.cur.Alt != nil {
		a.showBuf(a.cur, a.cur.Alt)
	}
}

// jumpBack is <C-o>: the window goes back to the buffer it showed before.
func (a *App) jumpBack() {
	w := a.cur
	if !w.back() {
		return
	}
	if w.Buf.Edit != nil {
		a.toWindow(w)
	}
}

// cycleThread shows the next or previous thread in the current window.
func (a *App) cycleThread(d int) {
	names := append([]string{model.Coordinator}, a.listOrder()...)
	if len(names) == 0 || a.workspace == "" {
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
	// <CR> on the prompt sends it, as in Insert.
	if a.cur.inPrompt {
		a.send()
		return
	}
	if a.cur.Buf.Edit != nil {
		if name := a.artifactAt(a.line(a.cur), a.cur.Col); name != "" && a.mode == modeNormal {
			a.openArtifact(name)
			return
		}
		a.move(a.cur, 1)
		return
	}
	l := a.line(a.cur)
	switch a.cur.Buf.Kind {
	case KindWorkspaces:
		if l.Item != "" {
			a.openWorkspace(l.Item)
		}
	case KindThreads:
		switch l.Item {
		case "":
		case resolvedRow:
			a.showResolved = !a.showResolved
			a.renderThreads()
		default:
			a.showThread(l.Item)
		}
	default:
		if l.Item == queuedRow {
			if err := a.c.SendNow(a.cur.Buf.Workspace, a.cur.Buf.Thread, ""); err != nil {
				a.notify(err.Error(), a.th.Love)
			}
			return
		}
		if bs := a.cur.Buf.blocks; l.Block < len(bs) && bs[l.Block].kind == bArtifact {
			a.openArtifact(bs[l.Block].art.Name)
			return
		}
		if name := a.artifactAt(l, a.cur.Col); name != "" {
			a.openArtifact(name)
			return
		}
		a.cur.Buf.Toggle(l.Block)
	}
}

// splitFromList opens the thread under the cursor of the threads list in the
// split on the right (<C-v>) or above (<C-x>), as in a picker.
func (a *App) splitFromList(vert bool) {
	if a.cur.Buf.Kind != KindThreads {
		return
	}
	l := a.line(a.cur)
	switch {
	case l.Item == "" || l.Item == resolvedRow:
	default:
		a.splitThread(l.Item, vert)
	}
}

// --- insert ---

// insert starts Insert mode with one of i a I A o O. In a chat they all
// write in the prompt; in an artifact they edit its source.
func (a *App) insert(k rune) {
	if a.cur.Buf.Kind == KindArtifact {
		a.startEdit(k)
		return
	}
	if a.cur.Buf.Prompt == nil {
		a.notify("this buffer is read-only; i writes in a thread or the coordinator", a.th.Muted)
		return
	}
	if v := a.threads[a.cur.Buf.Thread]; v.State == model.StateResolved {
		a.notify(v.Name+" is resolved · :reopen to write to it", a.th.Muted)
		return
	}
	if a.cur.inPrompt {
		a.insertInPrompt(k)
	}
	a.mode = modeInsert
	a.cur.vmode = 0
	a.cur.Follow = true
	a.fetchCompletions(a.cur.Buf, "")
}

func (a *App) insertKey(k string, ev *tcell.EventKey) {
	// <C-r>" puts the unnamed register, as in Vim; 0 + * name it too, as
	// every yank is also the clipboard.
	if a.ctrlR {
		a.ctrlR = false
		if k == "\"" || k == "0" || k == "+" || k == "*" {
			if e := a.cur.Buf.Edit; e != nil {
				e.insertText(a.reg.text)
				a.toWindow(a.cur)
			} else if p := a.cur.Buf.Prompt; p != nil {
				for _, r := range a.reg.text {
					p.insert(r)
				}
			}
		}
		return
	}
	if k == "<C-r>" {
		a.ctrlR = true
		return
	}
	if a.cur.Buf.Edit != nil {
		a.editInsert(k, ev.Rune(), ev.Key() == tcell.KeyRune)
		return
	}
	p := a.cur.Buf.Prompt
	if p == nil {
		a.mode = modeNormal
		return
	}
	if a.menuKey(k) {
		return
	}
	defer a.updateMenu()
	switch k {
	case "<Esc>":
		a.mode = modeNormal
		// As in Vim, the cursor stays where you typed, one rune back.
		if _, off := lineOff(p.Text, p.Cur); off > 0 {
			p.Cur--
		}
		a.cur.outOfPrompt()
		a.enterPrompt(a.cur)
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
	if err := a.c.Send(b.Workspace, b.Thread, text); err != nil {
		a.notify(err.Error(), a.th.Love)
		return
	}
	b.Prompt.Text, b.Prompt.Cur, b.Prompt.ed = nil, 0, nil
	if a.cur.inPrompt {
		a.cur.Cur, a.cur.Col = 0, width(" › ")
	}
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
		a.notify("no permission request in this window", a.th.Muted)
		return
	}
	v := a.threads[name]
	if err := a.c.Answer(a.workspace, name, v.Permission.ID, d, msg); err != nil {
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
	if w.inPrompt {
		a.editVisual("y")
		return
	}
	if a.mode != modeVisual {
		return
	}
	if w.Buf.Edit != nil {
		a.editVisual("y")
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
		a.reg = register{text: strings.Join(parts, "\n")}
		a.clip(a.reg.text, l2-l1+1)
	}
	w.vmode = 0
	a.mode = modeNormal
}

// yank is <leader>y: the selection in Visual, else the line. y already goes
// to the system clipboard, so this is y for Neovim fingers.
func (a *App) yank() {
	if a.mode == modeVisual {
		a.yankVisual()
		return
	}
	if a.cur.Buf.Edit != nil {
		a.editNormal("yy", 1)
		return
	}
	a.yankLines(a.cur.Cur, a.cur.Cur)
}

func (a *App) yankLines(l1, l2 int) {
	lines := a.cur.Buf.Lines(a.textWidth(a.cur), a.th, a.spin)
	var parts []string
	for i := l1; i <= l2 && i < len(lines); i++ {
		parts = append(parts, strings.TrimRight(lines[i].Text(), " "))
	}
	a.reg = register{text: strings.Join(parts, "\n"), lines: true}
	a.clip(a.reg.text, l2-l1+1)
}

// putPrompt is p and P in a chat: the register goes into the prompt, the
// only text there you can change, at its cursor.
func (a *App) putPrompt() {
	p := a.cur.Buf.Prompt
	if p == nil || a.reg.text == "" {
		return
	}
	for _, r := range a.reg.text {
		p.insert(r)
	}
	a.cur.Follow = true
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
