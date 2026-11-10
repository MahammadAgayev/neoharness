package tui

import (
	"os"
	"path/filepath"
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
	"allow", "always", "artifacts", "b", "buffer", "deny", "edit", "folder", "help", "ls", "messages", "new",
	"only", "open", "q", "qa", "quit", "reopen", "resolve", "restore", "split", "threads",
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
		if c.prefix == '/' {
			a.search = line
			a.findNext(1)
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
		} else if (argIdx == 2 && (fields[0] == "new" || fields[0] == "open") || argIdx == 1 && fields[0] == "folder") && !strings.Contains(word, ":") {
			pool = folderCompletions(word)
		} else {
			switch fields[0] {
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
	if strings.HasPrefix(read, "~/") {
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

	edit, held := a.cur.Buf.Edit, a.cur.Buf.Held
	switch cmd {
	case "q", "quit", "close":
		if edit != nil {
			a.stopEdit(a.cur)
			return
		}
		a.closeCur(false)
	case "q!", "quit!":
		if edit != nil || held != nil {
			a.dropEdit()
			return
		}
		a.closeCur(true)
	case "e", "edit":
		if a.cur.Buf.Kind != KindArtifact {
			a.notify("only an artifact can be edited", a.th.Muted)
			return
		}
		a.startEdit(0)
	case "e!", "edit!":
		if edit != nil || held != nil {
			a.dropEdit()
			a.startEdit(0)
		}
	case "w", "write", "w!", "write!":
		switch {
		case held != nil:
			a.writeHeld(strings.HasSuffix(cmd, "!"))
		case edit == nil:
			a.notify("nothing to write here", a.th.Muted)
		default:
			a.saveEdit(strings.HasSuffix(cmd, "!"))
		}
	case "wq", "x", "xit":
		switch {
		case held != nil:
			a.writeHeld(false)
		case edit == nil:
			a.notify("nothing to write here", a.th.Muted)
		case a.saveEdit(false):
			a.stopEdit(a.cur)
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
			a.cur.show(b)
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
		a.cur.show(a.helpBuf())
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

// findNext moves to the next match of the last search. Smart case: a query
// with an upper-case letter matches case exactly.
func (a *App) findNext(d int) {
	if a.search == "" {
		return
	}
	w := a.cur
	lines := w.Buf.Lines(a.textWidth(w), a.th, a.spin)
	q := a.search
	fold := strings.ToLower(q) == q
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
		if fold {
			text = strings.ToLower(text)
			for k := range joins {
				joins[k] = strings.ToLower(joins[k])
			}
		}
		for _, s := range append([]string{text}, joins...) {
			if j := strings.Index(s, q); j >= 0 && j < len(text) {
				a.moveTo(w, i)
				w.Col = lines[i].prefixWidth() + width(text[:j])
				w.Follow = false
				return
			}
		}
	}
	a.notify("pattern not found: "+q, a.th.Love)
}

func (a *App) helpBuf() *Buffer {
	if b, ok := a.bufs["neoharness://help"]; ok {
		return b
	}
	th := a.th
	sec := func(s string) Line { return Line{Prefix: []Span{{"  ", th.Text}}, Spans: []Span{{s, th.Muted}}} }
	row := func(k, d string) Line {
		return Line{Prefix: []Span{{"  ", th.Text}}, Spans: []Span{{pad(k, 29), th.Iris}, {d, th.Subtle}}}
	}
	lines := []Line{
		{},
		{Prefix: []Span{{"  ", th.Text}}, Spans: []Span{{"neoharness", th.Iris.Bold(true)}, {"  keys", th.Subtle}}},
		{},
		sec("write"),
		row("i a o", "write to the thread in this window"),
		row("<CR>", "send (in Insert)"),
		row("<C-x><C-s>", "send now: stop the running turn (in Insert)"),
		row("!<command>", "run it in the thread's folder; the output goes with your next message"),
		row("<C-j>  <S-CR>", "new line (in Insert)"),
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
		row("/ n N", "search"),
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
		row("<Esc>  :q", "close the artifact popup (in Normal)"),
		row("]b [b  <C-^>", "next / previous thread, alternate"),
		row("<CR>", "open the workspace, or switch to the thread"),
		row("<C-v>  <C-x>", "thread in the pane / a split above (pickers, threads list)"),
		row("<C-d>", "delete the thread or workspace, after y (pickers)"),
		{},
		sec("leader  <Space>"),
		row("<leader><leader>", "buffers"),
		row("<leader>t", "threads pane on the right; <CR> opens a thread in it"),
		row("<leader>w", "workspaces"),
		row("<leader>pv  <leader>pc", "workspaces screen, coordinator"),
		row("<leader>pa", "artifacts; <CR> on a name in a chat opens it too"),
		row("<leader>nc  <leader>nh", "dismiss notifications, their history"),
		row("<leader>/", "search every transcript"),
		row("<leader>a A x", "allow, allow always, deny a permission"),
		{},
		sec("commands"),
		row(":new <name> [folder]", "create a workspace; no folder opens a folder picker"),
		row(":folder [folder]", "change the workspace's folder; no folder opens a folder picker"),
		row(":open <name> [where]", "open a thread: folder or host:dir"),
		row(":resolve  :reopen", "close or bring back a thread"),
		row(":allow :always :deny", "answer a permission (:deny <reason>)"),
		row(":b <name>  :ls", "switch buffer"),
		row(":vsplit :split [name]", "split with a buffer"),
		row(":threads", "threads pane, as <leader>t"),
		row(":artifacts", "pick an artifact; <CR> on ◇ in a chat opens one beside it"),
		row(":messages", "notice history"),
		row(":workspaces  :workspace <n>", "workspace list, switch workspace"),
		row(":restore [name]", "bring back a deleted workspace; no name lists them"),
		row(":q  :qa", "close window, quit"),
		row(":q!  :qa!", "quit even if threads are running (stops them)"),
		{},
		sec("edit an artifact"),
		row(":e  i a o", "edit its markdown, where you are reading"),
		row(":w", "save, merged with a newer version; the writer gets the diff"),
		row("<Esc>", "back to the document; an unsaved edit stays, [+]"),
		row(":wq  :q!", "save and go back, drop the changes"),
		row("<CR>", "in a list item, the next item (Insert)"),
		row(":w!  :e!", "save over a newer version, take the newer version"),
		row("x s r J  dd cc D C", "change in Normal"),
		row("d c y  w e b $ 0 iw aw", "operators and motions; v V then d c y"),
		row("i( a[ i\" a` ib iB ip", "text objects after d c y: ci( da\" yap"),
		row("f t F T  %", "finds and brackets after d c y: dt, cf) d%"),
		row("p P  u <C-r>  .", "paste, undo, redo, repeat"),
		row("gx  gf", "open the link, or the artifact named, under the cursor"),
	}
	b := newBuffer("neoharness://help", KindHelp)
	b.SetLines(lines)
	a.bufs[b.Name] = b
	return b
}
