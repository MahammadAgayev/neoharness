package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/internal/model"
)

// cmdline is the bottom line in Command-line mode, for `:` commands and `/`
// searches.
type cmdline struct {
	prefix  rune
	text    []rune
	cur     int
	history map[rune][]string
	hidx    int
	// Tab completion: the candidates for the word being completed, and
	// which one is showing.
	comp     []string
	compIdx  int
	compHead string
}

var commands = []string{
	"allow", "always", "artifacts", "b", "buffer", "deny", "diffoff", "edit", "folder", "help", "ls", "messages", "new",
	"only", "open", "q", "qa", "quit", "reopen", "resolve", "restore", "setlocal", "split", "threads",
	"vsplit", "workspace", "workspaces", "wq",
}

func (a *App) startCmd(prefix rune) {
	if a.cmd.history == nil {
		a.cmd.history = map[rune][]string{}
	}
	a.cmd.prefix = prefix
	a.cmd.text, a.cmd.cur = nil, 0
	a.cmd.hidx = len(a.cmd.history[prefix])
	a.cmd.comp = nil
	a.mode = modeCommand
}

func (a *App) cmdKey(k string, ev *tcell.EventKey) {
	c := a.cmd
	if k != "<Tab>" && k != "<S-Tab>" {
		c.comp = nil
	}
	switch k {
	case "<Esc>", "<C-c>":
		a.mode = modeNormal
	case "<CR>":
		line := strings.TrimSpace(string(c.text))
		a.mode = modeNormal
		if line == "" {
			return
		}
		h := c.history[c.prefix]
		c.history[c.prefix] = append(slices.DeleteFunc(h, func(s string) bool { return s == line }), line)
		if c.prefix == '/' || c.prefix == '?' {
			a.search, a.searchBack = line, c.prefix == '?'
			a.findNext(a.searchDir())
			return
		}
		a.exec(line)
	case "<BS>":
		if c.cur == 0 {
			if len(c.text) == 0 {
				a.mode = modeNormal
			}
			return
		}
		c.text = append(c.text[:c.cur-1], c.text[c.cur:]...)
		c.cur--
	case "<Left>":
		c.cur = max(c.cur-1, 0)
	case "<Right>":
		c.cur = min(c.cur+1, len(c.text))
	case "<Home>", "<C-b>":
		c.cur = 0
	case "<End>", "<C-e>":
		c.cur = len(c.text)
	case "<C-u>":
		c.text, c.cur = append([]rune{}, c.text[c.cur:]...), 0
	case "<C-w>":
		i := c.cur
		for i > 0 && unicode.IsSpace(c.text[i-1]) {
			i--
		}
		for i > 0 && !unicode.IsSpace(c.text[i-1]) {
			i--
		}
		c.text = append(c.text[:i], c.text[c.cur:]...)
		c.cur = i
	case "<Up>", "<Down>":
		h := c.history[c.prefix]
		if k == "<Up>" {
			c.hidx = max(c.hidx-1, 0)
		} else {
			c.hidx = min(c.hidx+1, len(h))
		}
		if c.hidx < len(h) {
			c.text = []rune(h[c.hidx])
		} else {
			c.text = nil
		}
		c.cur = len(c.text)
	case "<Tab>", "<S-Tab>":
		if c.prefix == ':' {
			a.complete(k == "<Tab>")
		}
	default:
		if ev.Key() == tcell.KeyRune {
			c.text = append(c.text[:c.cur], append([]rune{ev.Rune()}, c.text[c.cur:]...)...)
			c.cur++
		} else if k == "<Space>" {
			c.text = append(c.text[:c.cur], append([]rune{' '}, c.text[c.cur:]...)...)
			c.cur++
		}
	}
}

// complete cycles through candidates for the word before the cursor: a
// command name for the first word, a folder for the folder of :new and :open,
// else a thread or workspace name.
func (a *App) complete(forward bool) {
	c := a.cmd
	if c.comp == nil {
		before := string(c.text[:c.cur])
		fields := strings.Fields(before)
		word := ""
		if len(fields) > 0 && !strings.HasSuffix(before, " ") {
			word = fields[len(fields)-1]
		}
		c.compHead = before[:len(before)-len(word)]
		argIdx := len(fields)
		if word != "" {
			argIdx--
		}
		var pool []string
		if argIdx == 0 {
			pool = commands
		} else if (argIdx == 2 && (fields[0] == "new" || fields[0] == "open") || argIdx == 1 && fields[0] == "folder") && !remoteWord(word) {
			pool = folderCompletions(word)
		} else {
			switch fields[0] {
			case "setlocal":
				for _, o := range localOptions {
					for _, v := range a.workspaces {
						if v.Name != a.workspace {
							continue
						}
						for _, n := range v.AgentNames {
							pool = append(pool, o+"="+n)
						}
					}
					pool = append(pool, o+"&")
				}
			case "workspace", "new":
				for _, p := range a.workspaces {
					pool = append(pool, p.Name)
				}
			default:
				pool = append([]string{model.Coordinator}, a.listOrder()...)
			}
		}
		for _, p := range pool {
			if strings.HasPrefix(p, word) {
				c.comp = append(c.comp, p)
			}
		}
		if len(c.comp) == 0 {
			c.comp = nil
			return
		}
		c.compIdx = -1
	}
	if forward {
		c.compIdx = (c.compIdx + 1) % len(c.comp)
	} else {
		c.compIdx = (c.compIdx - 1 + len(c.comp)) % len(c.comp)
	}
	rest := string(c.text[c.cur:])
	c.text = []rune(c.compHead + c.comp[c.compIdx] + rest)
	c.cur = len([]rune(c.compHead + c.comp[c.compIdx]))
	// With one candidate there is nothing to cycle, so the next <Tab> starts
	// again from the new text: a folder then completes one level deeper.
	if len(c.comp) == 1 {
		c.comp = nil
	}
}

// folderCompletions lists the folders that complete word, in the form it was
// typed: "~/Doc" gives "~/Documents/".
func folderCompletions(word string) []string {
	if word == "~" {
		return []string{"~/"}
	}
	dir, base := filepath.Split(word)
	read := dir
	if read == "" {
		read = "."
	}
	if strings.HasPrefix(read, "~/") || strings.HasPrefix(read, `~\`) {
		home, _ := os.UserHomeDir()
		read = filepath.Join(home, read[2:])
	}
	entries, _ := os.ReadDir(read)
	var out []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() || !strings.HasPrefix(n, base) || strings.HasPrefix(n, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		out = append(out, dir+n+"/")
	}
	return out
}

// exec runs a `:` command.
func (a *App) exec(line string) {
	fields := strings.Fields(line)
	cmd, args := fields[0], fields[1:]
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	// The thread a command acts on when none is named.
	target := func() string {
		if n := arg(0); n != "" {
			return n
		}
		if a.cur.Buf.Kind == KindChat {
			return a.cur.Buf.Thread
		}
		if a.cur.Buf.Kind == KindThreads {
			return a.line(a.cur).Item
		}
		return ""
	}
	needWorkspace := func() bool {
		if a.workspace == "" {
			a.notify("open a workspace first · <leader>w", a.th.Muted)
			return false
		}
		return true
	}

	edit := a.cur.Buf.Edit
	switch cmd {
	case "q", "quit", "close":
		a.closeCur(false)
	case "q!", "quit!":
		if edit != nil && edit.Dirty {
			a.dropEdit()
		}
		a.closeCur(true)
	case "e", "edit":
		switch {
		case edit == nil:
			a.notify("only an artifact can be edited", a.th.Muted)
		case edit.Dirty:
			a.notify("no write since the last change · :w saves, :e! drops it", a.th.Gold)
		default:
			a.dropEdit()
		}
	case "e!", "edit!":
		if edit != nil {
			a.dropEdit()
		}
	case "diffoff", "diffo":
		if e := diffOf(a.cur); e != nil {
			a.closeDiff(e)
		}
	case "w", "write", "w!", "write!":
		if edit == nil {
			a.notify("nothing to write here", a.th.Muted)
			return
		}
		a.saveEdit(strings.HasSuffix(cmd, "!"))
	case "wq", "x", "xit":
		switch {
		case edit == nil:
			a.notify("nothing to write here", a.th.Muted)
		case a.saveEdit(false):
			a.closeCur(false)
		}
	case "qa", "qall", "quitall":
		a.quitAll(false)
	case "qa!", "qall!", "quitall!":
		a.quitAll(true)
	case "only", "on":
		a.only()
	case "threads":
		if !needWorkspace() {
			return
		}
		a.showList()
	case "workspaces":
		a.loadWorkspaces()
		a.showWorkspaces()
	case "workspace", "p":
		if arg(0) == "" {
			a.pickWorkspaces()
			return
		}
		a.openWorkspace(arg(0))
	case "new":
		if arg(0) == "" {
			a.notify("usage: :new <name> [folder]", a.th.Muted)
			return
		}
		if arg(1) == "" {
			a.pickFolder(arg(0))
			return
		}
		a.createWorkspace(arg(0), arg(1))
	case "restore":
		a.restoreWorkspace(arg(0))
	case "setlocal":
		if needWorkspace() {
			a.setLocal(args)
		}
	case "folder":
		if !needWorkspace() {
			return
		}
		if arg(0) == "" {
			a.pickNewFolder()
			return
		}
		a.setFolder(arg(0))
	case "open":
		if !needWorkspace() {
			return
		}
		if arg(0) == "" {
			a.notify("usage: :open <name> [folder|host:dir]", a.th.Muted)
			return
		}
		if err := a.c.OpenThread(a.workspace, arg(0), "", arg(1)); err != nil {
			a.notify(err.Error(), a.th.Love)
			return
		}
		// The view arrives over the update stream; make the thread known now
		// so it can be shown at once.
		if _, ok := a.threads[arg(0)]; !ok {
			a.threads[arg(0)] = model.ThreadView{Name: arg(0), Role: model.RoleThread, State: model.StateIdle}
			a.order = append(a.order, arg(0))
			a.renderThreads()
		}
		a.paneThread(arg(0))
		a.insert('i')
	case "resolve":
		if !needWorkspace() {
			return
		}
		if err := a.c.Resolve(a.workspace, target()); err != nil {
			a.notify(err.Error(), a.th.Love)
		}
	case "reopen":
		if !needWorkspace() {
			return
		}
		if err := a.c.Reopen(a.workspace, target()); err != nil {
			a.notify(err.Error(), a.th.Love)
		}
	case "b", "buffer", "buf":
		if arg(0) == "" {
			a.pickBuffers()
			return
		}
		if b := a.findBuffer(arg(0)); b != nil {
			a.showBuf(a.cur, b)
		} else {
			a.notify("no buffer "+arg(0), a.th.Love)
		}
	case "ls", "buffers":
		a.pickBuffers()
	case "messages", "mes":
		a.cur.show(a.messagesBuf)
		a.cur.Follow = true
	case "vsplit", "vs", "split", "sp":
		var b *Buffer
		if arg(0) != "" {
			if b = a.findBuffer(arg(0)); b == nil {
				a.notify("no buffer "+arg(0), a.th.Love)
				return
			}
		}
		a.splitCur(cmd == "vsplit" || cmd == "vs", b)
	case "allow":
		a.answer(model.Allow, "")
	case "always":
		a.answer(model.Always, "")
	case "deny":
		a.answer(model.Deny, strings.Join(args, " "))
	case "artifacts":
		a.pickArtifacts()
	case "help", "h":
		a.showHelp()
	default:
		a.notify("not a command: "+cmd, a.th.Love)
	}
}

// findBuffer resolves a name typed after :b — a thread name, a full buffer
// name, or a unique substring of one.
func (a *App) findBuffer(name string) *Buffer {
	if a.workspace != "" {
		if _, ok := a.threads[name]; ok {
			return a.chat(name)
		}
	}
	if b, ok := a.bufs[name]; ok {
		return b
	}
	var match *Buffer
	for n, b := range a.bufs {
		if strings.Contains(n, name) {
			if match != nil {
				return nil
			}
			match = b
		}
	}
	return match
}

// searchDir is the way n goes: down after /, up after ?.
func (a *App) searchDir() int {
	if a.searchBack {
		return -1
	}
	return 1
}

// findNext moves to the next match of the last search. Smart case: a query
// with an upper-case letter matches case exactly.
func (a *App) findNext(d int) {
	if a.search == "" {
		return
	}
	// A search writes over the last message, as in Vim.
	a.notice = ""
	w := a.cur
	q := a.search
	re := vimPattern(q, strings.ToLower(q) == q)
	if e := w.Buf.Edit; e != nil {
		a.fromWindow(w)
		p, ok := e.find(re, d)
		if !ok {
			a.notify("pattern not found: "+q, a.th.Love)
			return
		}
		wrapped := d > 0 && (p.line < e.Line || p.line == e.Line && p.off <= e.Off) ||
			d < 0 && (p.line > e.Line || p.line == e.Line && p.off >= e.Off)
		rows := make([]string, len(e.Src))
		for i, l := range e.Src {
			rows[i] = string(l)
		}
		a.searchCount(re, rows, p.line, len(string(e.Src[p.line][:p.off])), wrapped)
		e.Line, e.Off, e.shown, e.shownTo = p.line, p.off, p.line, p.line
		a.toWindow(w)
		return
	}
	lines := w.Buf.Lines(a.textWidth(w), a.th, a.spin)
	for step := 1; step <= len(lines); step++ {
		i := ((w.Cur+d*step)%len(lines) + len(lines)) % len(lines)
		text := lines[i].Text()
		// A match may go on in the next row of a wrapped line. A rendered
		// wrap drops the space at the break, the source keeps it.
		var joins []string
		if i+1 < len(lines) && lines[i+1].Text() != "" {
			next := lines[i+1].Text()
			joins = []string{text + next, text + " " + next}
		}
		for _, s := range append([]string{text}, joins...) {
			if m := re.FindStringIndex(s); m != nil && m[0] < len(text) {
				j := m[0]
				rows := make([]string, len(lines))
				for k, l := range lines {
					rows[k] = l.Text()
				}
				a.searchCount(re, rows, i, j, d > 0 && i <= w.Cur || d < 0 && i >= w.Cur)
				a.moveTo(w, i)
				w.Col = lines[i].prefixWidth() + width(text[:j])
				w.Follow = false
				return
			}
		}
	}
	a.notify("pattern not found: "+q, a.th.Love)
}

// searchCount shows the search with which match the cursor is on and how
// many there are, and W when it went past the end, as Neovim does when
// shortmess has no S: "/thread  W [1/7]". at is the match's byte in its row.
func (a *App) searchCount(re *regexp.Regexp, rows []string, row, at int, wrapped bool) {
	n, total := 0, 0
	for i, r := range rows {
		for _, m := range re.FindAllStringIndex(r, -1) {
			total++
			if i < row || i == row && m[0] <= at {
				n++
			}
		}
	}
	// A match over a wrapped row's break is in no single row.
	n, total = max(n, 1), max(total, n, 1)
	dir, w := "/", ""
	if a.searchBack {
		dir = "?"
	}
	if wrapped {
		w = "W "
	}
	a.notify(fmt.Sprintf("%s%s  %s[%d/%d]", dir, a.search, w, n, total), a.th.Subtle)
}

// showHelp opens help in a split above, as Neovim's :help does, so :q
// closes it; a window that shows it already takes the focus.
func (a *App) showHelp() {
	b := a.helpBuf()
	for _, w := range a.root.windows() {
		if w.Buf == b {
			a.cur = w
			return
		}
	}
	a.splitCur(false, b)
	a.cur.Follow = false
}

func (a *App) helpBuf() *Buffer {
	if b, ok := a.bufs["neoharness://help"]; ok {
		return b
	}
	th := a.th
	// Key rows and headings wrap to the window; the other lines are fixed.
	type helpLine struct {
		key, what string
		l         Line
	}
	sec := func(s string) helpLine { return helpLine{what: s} }
	row := func(k, d string) helpLine { return helpLine{key: k, what: d} }
	lines := []helpLine{
		{},
		{l: Line{Prefix: []Span{{"  ", th.Text}}, Spans: []Span{{"neoharness", th.Iris.Bold(true)}, {"  keys", th.Subtle}}}},
		{},
		sec("write"),
		row("i a o", "write to the thread in this window"),
		row("<CR>", "send (in Insert)"),
		row("<CR>", "on a queued message: send it now, stopping the running turn"),
		row("x  dd", "on a queued message: take it back; on the last one while it runs: stop the turn"),
		row("u", "with an empty prompt: your last message back to the prompt, if not answered yet"),
		row("!<command>", "run it in the thread's folder; the output goes with your next message"),
		row("<C-j>  <S-CR>", "new line (in Insert)"),
		row("p  <C-r>\"", "put the last yank in the prompt; <C-r>\" in Insert"),
		row("<Esc>  j  G", "the cursor goes into the prompt; x dw cc u . r work on it"),
		row("/  @", "complete a command, a thread (#) or a file (in Insert)"),
		row("<C-n> <C-p>  <Tab>", "next / previous completion, take it"),
		row("<Esc>", "back to Normal"),
		{},
		sec("move"),
		row("j k  gg G  <C-d> <C-u>", "lines, ends, half pages"),
		row("h l  w b e  0 $", "within a line"),
		row("f F t T  ; ,  %", "to a character in the line, again, back; matching bracket"),
		row("]m [m", "next / previous message, or heading in an artifact"),
		row("gO", "outline: pick a heading, or a message in a chat"),
		row("ma  'a  `a  ''", "set a mark, go to its line, its place, back"),
		row("/ ? n N", "search down, up, again, back"),
		row("za  <leader>e", "open or close a turn's tool calls"),
		row("v V  y  yy  <leader>y", "select and yank to the clipboard"),
		row("v then iw a( i\" ip", "select a word, brackets, quotes, a paragraph"),
		row("qa … q  @a  @@", "record a macro, play it, play the last one again"),
		{},
		sec("windows and buffers"),
		row("<Tab>  <C-w>w", "next window"),
		row("<C-w>h j k l  <C-h>…", "window in a direction"),
		row("<C-w>v  <C-w>s", "split vertically / horizontally"),
		row("<C-w>c  <C-w>o  <C-w>=", "close, only, equal sizes"),
		row("]b [b  <C-^>", "next / previous thread, alternate"),
		row("<C-o>", "back to the buffer the window showed before"),
		row("<CR>", "open the workspace, or switch to the thread"),
		row("<C-v>  <C-x>", "thread in a split on the right / above (pickers, threads list)"),
		row("<C-d>", "delete the thread or workspace, after y (pickers)"),
		row("x", "delete the workspace, after y (workspaces screen)"),
		{},
		sec("leader  <Space>"),
		row("<leader><leader>  <leader>pb", "pick a buffer"),
		row("<leader>pt  <leader>w", "pick a thread, a workspace"),
		row("<leader>pa", "pick an artifact; <CR> on a name in a chat opens it too"),
		row("<leader>ps", "search every transcript"),
		row("<leader>pv", "workspaces screen"),
		row("<leader>t", "threads pane on the right; <CR> opens a thread in it"),
		row("<leader>c", "coordinator"),
		row("<leader>nc  <leader>nh", "dismiss notifications, their history"),
		row("<leader>a A x", "allow, allow always, deny a permission"),
		{},
		sec("commands"),
		row(":new <name> [folder]", "create a workspace; no folder opens a folder picker"),
		row(":folder [folder]", "change the workspace's folder; no folder opens a folder picker"),
		row(":setlocal coordinator=<agent>", "this workspace's coordinator runs that agent; threads= for its threads"),
		row(":setlocal threads& / threads?", "back to the config / show; no argument shows both"),
		row(":open <name> [where]", "open a thread: folder or host:dir"),
		row(":resolve  :reopen", "close or bring back a thread; your message to one brings it back too"),
		row(":allow :always :deny", "answer a permission (:deny <reason>)"),
		row(":b <name>  :ls", "switch buffer"),
		row(":vsplit :split [name]", "split with a buffer"),
		row(":threads", "threads pane, as <leader>t"),
		row(":artifacts", "pick an artifact; <CR> on ◇ in a chat opens it too"),
		row(":messages", "notice history"),
		row(":workspaces  :workspace <n>", "workspace list, switch workspace"),
		row(":restore [name]", "bring back a deleted workspace; no name lists them"),
		row(":q  :qa", "close window, quit"),
		row(":q!  :qa!", "quit even if threads are running (stops them)"),
		{},
		sec("config  ~/.neoharness/init.lua, after the built-in defaults.lua"),
		row("defaults.lua", "every default map, color and setting; copy a line to init.lua and edit it"),
		row("neoharness.o.leader", "the leader, <Space> by default; it moves the default maps"),
		row("neoharness.o.equalalways", "on: a split or a close makes the windows the same size"),
		row("neoharness.o.compactwarn", "past this context %, the status line says /compact; 0 never"),
		row("neoharness.keymap.set / del", "n x i maps: keys, an ex command (:threads<CR>), a function"),
		row("<Plug>(neoharness-…)", "threads grep yank dismiss coordinator: actions with no ex command"),
		row("neoharness.hl", "a color: Text Subtle Muted Iris Foam Gold Rose Pine Love Code …"),
		row("neoharness.cmd", "run a command"),
		row("require(\"neoharness\").setup", "hub settings; coordinator = and threads = name agents of require(\"neoharness.agents\").setup"),
		row("notify = \"coordinator\"", "in the hub setup: the coordinator decides what reaches you, not every thread"),
		{},
		sec("edit an artifact"),
		row("i a o", "write in the markdown; only the cursor's line shows raw"),
		row(":w", "save, merged with a newer version; the writer gets the diff"),
		row("<Esc>", "back to Normal, rendered; an unsaved change shows [+]"),
		row(":wq  :q!", "save and close, drop the changes and close"),
		row("<CR>", "in a list item, the next item (Insert)"),
		row(":w!  :e!", "save over a newer version, take the newer version"),
		row("]c [c  do dp", "after a :w that a newer version refused: next / previous change, take it"),
		row(":diffoff  :q", "end the diff (:q in the newer version's window)"),
		row("x s r J  dd cc D C", "change in Normal"),
		row("d c y  w e b $ 0 iw aw", "operators and motions; v V then d c y"),
		row("i( a[ i\" a` ib iB ip", "text objects after d c y: ci( da\" yap"),
		row("f t F T  %", "finds and brackets after d c y: dt, cf) d%"),
		row("p P  u <C-r>  .", "paste, undo, redo, repeat"),
		row("gx  gf", "open the link, or the artifact named, under the cursor"),
	}
	b := newBuffer("neoharness://help", KindHelp)
	for _, hl := range lines {
		if hl.what != "" {
			b.push(&block{kind: bKey, text: hl.key, text2: hl.what})
		} else if l := b.last(); l != nil && l.kind == bRaw {
			l.raw = append(l.raw, hl.l)
		} else {
			b.push(&block{kind: bRaw, raw: []Line{hl.l}})
		}
	}
	a.bufs[b.Name] = b
	return b
}

// remoteWord reports a word that names a folder on an ssh host, which cannot
// be completed from here. C:\src on Windows is not one.
func remoteWord(word string) bool {
	l, err := model.ParseLocation(word)
	return err == nil && l.Remote()
}
