package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/internal/model"
)

// pickItem is one row of a picker.
type pickItem struct {
	glyph  Span
	label  string
	detail string
	run    func()
	// split opens the row in a split, right of the window (vert) or above
	// it: <C-v> and <C-x>, as in Telescope.
	split func(vert bool)
	// thread is set on a row that is a thread, so <C-d> can delete it.
	thread string
	// workspace is set on a row that is a workspace, so <C-d> can delete it.
	workspace string
}

// picker is a floating fuzzy finder.
type picker struct {
	title  string
	query  []rune
	sel    int
	items  []pickItem
	source func(q string) []pickItem
	// all is how many rows the picker has before a query; its box fits them.
	all int
}

func (a *App) openPicker(title string, source func(q string) []pickItem) {
	a.picker = &picker{title: title, source: source}
	a.picker.items = source("")
	a.picker.all = len(a.picker.items)
	a.mode = modePicker
}

// fuzzy filters items whose label contains the query's letters in order,
// best matches first: contiguous runs and an early start score higher.
func fuzzy(items []pickItem, q string) []pickItem {
	if q == "" {
		return items
	}
	type scored struct {
		it    pickItem
		score int
	}
	var out []scored
	ql := []rune(strings.ToLower(q))
	for _, it := range items {
		s, ok := fuzzyScore([]rune(strings.ToLower(it.label+" "+it.detail)), ql)
		if ok {
			out = append(out, scored{it, s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	res := make([]pickItem, len(out))
	for i, s := range out {
		res[i] = s.it
	}
	return res
}

func fuzzyScore(text, q []rune) (int, bool) {
	score, qi, run := 0, 0, 0
	for i, r := range text {
		if qi == len(q) {
			break
		}
		if r == q[qi] {
			run++
			score += 1 + run*2
			if i == 0 || !unicode.IsLetter(text[i-1]) {
				score += 3
			}
			qi++
		} else {
			run = 0
		}
	}
	return score - len(text)/10, qi == len(q)
}

func (a *App) pickerKey(k string, ev *tcell.EventKey) {
	p := a.picker
	switch k {
	case "<Esc>", "<C-c>":
		a.closePicker()
	case "<CR>":
		var run func()
		if p.sel < len(p.items) {
			run = p.items[p.sel].run
		}
		a.closePicker()
		if run != nil {
			run()
		}
	case "<C-d>":
		if p.sel >= len(p.items) {
			break
		}
		switch it := p.items[p.sel]; {
		case it.thread != "":
			a.closePicker()
			a.askDelete(it.thread)
		case it.workspace != "":
			a.closePicker()
			a.askDeleteWorkspace(it.workspace)
		}
	case "<C-v>", "<C-x>":
		if p.sel < len(p.items) && p.items[p.sel].split != nil {
			split := p.items[p.sel].split
			a.closePicker()
			split(k == "<C-v>")
		}
	case "<Down>", "<C-n>", "<C-j>", "<Tab>":
		if len(p.items) > 0 {
			p.sel = (p.sel + 1) % len(p.items)
		}
	case "<Up>", "<C-p>", "<C-k>", "<S-Tab>":
		if len(p.items) > 0 {
			p.sel = (p.sel - 1 + len(p.items)) % len(p.items)
		}
	case "<BS>":
		if len(p.query) > 0 {
			p.query = p.query[:len(p.query)-1]
			p.refresh()
		}
	case "<C-u>":
		p.query = nil
		p.refresh()
	case "<Space>":
		p.query = append(p.query, ' ')
		p.refresh()
	default:
		if ev.Key() == tcell.KeyRune {
			p.query = append(p.query, ev.Rune())
			p.refresh()
		}
	}
}

func (p *picker) refresh() {
	p.items = p.source(string(p.query))
	p.sel = 0
}

func (a *App) closePicker() {
	a.picker = nil
	a.mode = modeNormal
}

func (a *App) threadItems() []pickItem {
	var items []pickItem
	for _, n := range append([]string{model.Coordinator}, a.listOrder()...) {
		v, ok := a.threads[n]
		if !ok {
			continue
		}
		detail := v.Progress
		if v.Waiting != "" {
			detail = "waiting · " + v.Waiting
		}
		name := n
		items = append(items, pickItem{
			glyph:  Span{stateGlyph(v.State), a.th.state(v.State)},
			label:  n,
			detail: detail,
			run:    func() { a.showThread(name) },
			split:  func(vert bool) { a.splitThread(name, vert) },
			thread: name,
		})
	}
	return items
}

func (a *App) artifactItems() []pickItem {
	var items []pickItem
	for _, art := range a.artifacts {
		art := art
		items = append(items, pickItem{
			glyph:  Span{"◇", a.th.Foam},
			label:  art.Name,
			detail: fmt.Sprintf("%s · %s · v%d", art.Title, art.Thread, art.Version),
			run:    func() { a.openArtifact(art.Name) },
			split: func(vert bool) {
				a.splitCur(vert, nil)
				a.openArtifact(art.Name)
			},
		})
	}
	return items
}

func (a *App) pickArtifacts() {
	if a.workspace == "" {
		a.notify("open a workspace first", a.th.Muted)
		return
	}
	if len(a.artifacts) == 0 {
		a.notify("no artifacts yet", a.th.Muted)
		return
	}
	items := a.artifactItems()
	a.openPicker("artifacts", func(q string) []pickItem { return fuzzy(items, q) })
}

func (a *App) pickWorkspaces() {
	var items []pickItem
	for _, p := range a.workspaces {
		name := p.Name
		detail := displayPin(p.Pin)
		if n := p.Counts[model.StateWaiting]; n > 0 {
			detail += fmt.Sprintf("  · %d waiting", n)
		}
		items = append(items, pickItem{
			glyph:     Span{"▪", a.th.Iris},
			label:     name,
			detail:    detail,
			run:       func() { a.openWorkspace(name) },
			workspace: name,
		})
	}
	a.openPicker("workspaces", func(q string) []pickItem { return fuzzy(items, q) })
}

// folderDepth is how many levels below the current folder the folder picker
// looks.
const folderDepth = 6

// skipFolders are never workspaces, and they are large: from $HOME, Library and
// node_modules hold most of the folders and make the picker slow to open.
var skipFolders = map[string]bool{"Library": true, "node_modules": true}

// pickFolder chooses the folder to pin a new workspace to.
func (a *App) pickFolder(name string) {
	items := []pickItem{{
		glyph: Span{"○", a.th.Muted},
		label: "not pinned",
		run:   func() { a.createWorkspace(name, "") },
	}}
	items = append(items, folderItems(a.th, func(dir string) { a.createWorkspace(name, dir) })...)
	a.openPicker("folder for "+name, func(q string) []pickItem { return fuzzy(items, q) })
}

// pickNewFolder chooses a new folder for the open workspace.
func (a *App) pickNewFolder() {
	items := folderItems(a.th, a.setFolder)
	a.openPicker("new folder for "+a.workspace, func(q string) []pickItem { return fuzzy(items, q) })
}

func folderItems(th *Theme, run func(dir string)) []pickItem {
	var items []pickItem
	for _, dir := range candidateFolders() {
		items = append(items, pickItem{
			glyph: Span{"▪", th.Iris},
			label: shortHome(dir),
			run:   func() { run(dir) },
		})
	}
	return items
}

// candidateFolders is the current folder and every visible folder below it,
// down to folderDepth levels.
func candidateFolders() []string {
	wd, err := os.Getwd()
	if err != nil {
		return nil
	}
	out := []string{wd}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || skipFolders[e.Name()] {
				continue
			}
			p := filepath.Join(dir, e.Name())
			out = append(out, p)
			if depth > 1 {
				walk(p, depth-1)
			}
		}
	}
	walk(wd, folderDepth)
	return out
}

func (a *App) pickBuffers() {
	var items []pickItem
	if a.workspace != "" {
		items = append(a.threadItems(), a.artifactItems()...)
		if a.threadsBuf != nil {
			b := a.threadsBuf
			items = append(items, pickItem{glyph: Span{"≡", a.th.Muted}, label: "threads", detail: b.Name, run: func() { a.showBuf(a.cur, b) },
				split: func(vert bool) { a.splitCur(vert, b) }})
		}
	}
	for _, b := range []*Buffer{a.workspacesBuf, a.messagesBuf, a.helpBuf()} {
		b := b
		label := strings.TrimPrefix(b.Name, "neoharness://")
		items = append(items, pickItem{glyph: Span{"≡", a.th.Muted}, label: label, detail: b.Name, run: func() { a.showBuf(a.cur, b) },
			split: func(vert bool) { a.splitCur(vert, b) }})
	}
	a.openPicker("buffers", func(q string) []pickItem { return fuzzy(items, q) })
}

// pickGrep searches every transcript in the workspace as the query is typed.
func (a *App) pickGrep() {
	if a.workspace == "" {
		a.notify("open a workspace first", a.th.Muted)
		return
	}
	a.openPicker("search transcripts", func(q string) []pickItem {
		if strings.TrimSpace(q) == "" {
			return nil
		}
		fold := strings.ToLower(q) == q
		var items []pickItem
		for _, n := range append([]string{model.Coordinator}, a.listOrder()...) {
			b := a.chat(n)
			w := 80
			if b.linesW > 0 {
				w = b.linesW
			}
			for i, l := range b.Lines(w, a.th, a.spin) {
				text := l.Text()
				hay := text
				if fold {
					hay = strings.ToLower(text)
				}
				at := strings.Index(hay, q)
				if at < 0 {
					continue
				}
				// The row starts a little before the match, so the match
				// shows even late in a long line.
				detail := strings.TrimSpace(text)
				if at > 24 && len(hay) == len(text) {
					cut := strings.LastIndex(text[:at-12], " ")
					detail = "…" + strings.TrimSpace(text[max(cut, 0):])
				}
				name, line := n, i
				v := a.threads[n]
				items = append(items, pickItem{
					glyph:  Span{stateGlyph(v.State), a.th.state(v.State)},
					label:  n,
					detail: detail,
					run: func() {
						a.showThread(name)
						a.moveTo(a.cur, line)
						a.cur.Follow = false
					},
					split: func(vert bool) {
						a.splitThread(name, vert)
						a.moveTo(a.cur, line)
						a.cur.Follow = false
					},
				})
				if len(items) >= 200 {
					return items
				}
			}
		}
		return items
	})
}
