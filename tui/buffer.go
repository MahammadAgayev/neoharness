package tui

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// BufKind is what a buffer shows. Every view in the TUI is a buffer; the
// kind decides how it is filled and what <CR> does on a line.
type BufKind int

const (
	KindWorkspaces BufKind = iota
	KindThreads
	KindChat
	KindMessages
	KindHelp
	KindArtifact
	// KindDiff is the newer version of an artifact beside its source in
	// diff mode.
	KindDiff
	// KindCost is what a workspace, or every workspace, cost.
	KindCost
	// KindTodo is a thread's todo list.
	KindTodo
)

type blockKind int

const (
	bRaw blockKind = iota
	bMessage
	bText
	bTools
	bError
	bPermission
	bAnswer
	bAsk
	bProgress
	bNote
	bShell
	bArtifact
	// bNotice is what the coordinator told the person with notify.
	bNotice
	// bKey is a row of :help: a key and what it does, or a heading when
	// there is no key.
	bKey
)

// toolCall is one tool call; failed holds why it failed or was denied.
type toolCall struct{ tool, detail, id, failed string }

// block is one logical piece of a transcript. It renders itself to lines and
// caches them per width, so appending to a long transcript only renders the
// new block.
type block struct {
	kind  blockKind
	from  string
	text  string
	text2 string // a permission's answer, a shell command's output
	id    string
	exit  int
	// needsYou marks a notice the person must act on.
	needsYou bool
	art      model.Artifact
	tools    []toolCall
	live     bool
	open     bool
	raw      []Line
	cacheW   int
	cache    []Line
}

// Prompt is the editable region at the end of a chat buffer.
type Prompt struct {
	Text []rune
	Cur  int
	// Images are the pictures :image put in, by the number of their token;
	// sent is the last message that went with images, so taking it back
	// brings them back.
	Images map[int][]byte
	sent   sentImages
	// ed runs Normal mode changes on the text, with their undo.
	ed *Editor
}

type sentImages struct {
	text   string
	images map[int][]byte
}

// Buffer is a named, append-mostly list of blocks.
type Buffer struct {
	Name      string
	Kind      BufKind
	Workspace string
	Thread    string
	Artifact  string
	Prompt    *Prompt
	// Edit is an artifact's source.
	Edit *Editor
	// diffOf is the source a KindDiff buffer is beside.
	diffOf *Editor
	// marks are m's places, a line and a column; ' is the place before the
	// last jump.
	marks map[rune][2]int

	blocks []*block
	// pending are a chat's queued messages, shown after the transcript
	// until the turn that reads them starts.
	pending []model.Event
	// working is set while the chat's thread has a turn running.
	working bool
	// asking hides the last question block: the card above the prompt
	// shows it until the answer.
	asking bool
	// todos is the thread's todo list, shown after the queue while it has
	// open items; todoOpen unfolds it.
	todos    []model.Todo
	todoOpen bool
	lines    []Line
	linesW   int
	dirty    bool
}

func newBuffer(name string, kind BufKind) *Buffer {
	return &Buffer{Name: name, Kind: kind, dirty: true}
}

func newChat(workspace, thread string) *Buffer {
	b := newBuffer(chatName(workspace, thread), KindChat)
	b.Workspace, b.Thread = workspace, thread
	b.Prompt = &Prompt{}
	return b
}

func chatName(workspace, thread string) string { return "neoharness://" + workspace + "/" + thread }

func newArtifact(workspace, name string) *Buffer {
	b := newBuffer(artifactName(workspace, name), KindArtifact)
	b.Workspace, b.Artifact = workspace, name
	return b
}

func artifactName(workspace, name string) string {
	return "neoharness://" + workspace + "/artifacts/" + name
}

// SetDoc gives an artifact buffer the hub's newest version. Unsaved changes
// stay; :w merges them with it.
func (b *Buffer) SetDoc(a model.Artifact, text string) {
	e := b.Edit
	switch {
	case e == nil:
		b.Edit = newEditor(a.Name, text, a.Version)
	case !e.Dirty:
		if text != e.Text() {
			e.Src = nil
			for _, l := range strings.Split(text, "\n") {
				e.Src = append(e.Src, []rune(l))
			}
			e.rev++
		}
		e.Base, e.saved = a.Version, text
	}
	b.Edit.art = a
	// The meta line changes; it is not a change of the text.
	b.Edit.cacheW = 0
	b.dirty = true
}

// headingLevel is the level of a markdown heading line, 0 if it is not one.
func headingLevel(s string) int {
	n := len(s) - len(strings.TrimLeft(s, "#"))
	if n < 1 || n > 6 || len(s) > n && s[n] != ' ' && s[n] != '\t' {
		return 0
	}
	return n
}

// SetLines replaces the buffer's content with fixed lines.
func (b *Buffer) SetLines(lines []Line) {
	b.blocks = []*block{{kind: bRaw, raw: lines}}
	b.dirty = true
}

// Lines returns the buffer rendered at width w.
func (b *Buffer) Lines(w int, th *Theme, spin int) []Line {
	if b.Edit != nil {
		return b.Edit.render(w, th)
	}
	live := b.Live()
	if !b.dirty && !live && b.linesW == w {
		return b.lines
	}
	var out []Line
	ask := -1
	if b.asking {
		for i, bl := range b.blocks {
			if bl.kind == bAsk {
				ask = i
			}
		}
	}
	for i, bl := range b.blocks {
		if i == ask {
			continue
		}
		if i > 0 && bl.kind != bRaw && !b.joins(i) {
			out = append(out, Line{Block: i})
		}
		rendered := bl.render(w, th, spin)
		for _, l := range rendered {
			l.Block = i
			out = append(out, l)
		}
	}
	if b.waiting() {
		out = append(out, Line{Block: len(b.blocks)}, Line{Block: len(b.blocks),
			Prefix: []Span{{"  " + spinner[spin%len(spinner)] + " ", th.Run}},
			Spans:  []Span{{"working", th.Fg2}}})
	}
	for _, e := range b.pending {
		bar := []Span{{"▎ ", th.Fg3}}
		label := "queued"
		if e.From != model.User {
			label = "queued from " + e.From
		}
		rows := []Line{{}, {Prefix: bar, Spans: []Span{{label, th.Fg3.Italic(true)}}}}
		for _, l := range strings.Split(e.Text, "\n") {
			rows = append(rows, wrap([]Span{{l, th.Fg3}}, w, bar, bar)...)
		}
		for i, l := range rows {
			l.Block = len(b.blocks)
			if i > 0 {
				l.Item = queuedRow + e.At.Format(time.RFC3339Nano)
			}
			out = append(out, l)
		}
	}
	out = append(out, b.todoLines(w, th)...)
	b.lines, b.linesW, b.dirty = out, w, false
	return out
}

// todoLines are the chat's todo list above the prompt: one line, folded,
// with the count and the item in hand; unfolded, every item. A list with
// nothing left to do does not show.
func (b *Buffer) todoLines(w int, th *Theme) []Line {
	if !b.hasTodo() {
		return nil
	}
	done, cur := 0, ""
	for _, t := range b.todos {
		switch {
		case t.State == model.TodoDone:
			done++
		case cur == "" || t.State == model.TodoActive:
			cur = t.Text
		}
	}
	in := []Span{{"  ", th.Fg}}
	fold, head := "▸ ", []Span{{fmt.Sprintf("%d of %d", done, len(b.todos)), th.Fg3}}
	if b.todoOpen {
		fold = "▾ "
	} else {
		head = append(head, Span{" · ", th.Fg3}, Span{cur, th.Fg})
	}
	rows := append([]Line{{}}, wrap(append([]Span{{fold, th.Fg3}}, head...), w, in, append(clone(in), Span{"  ", th.Fg}))...)
	if b.todoOpen {
		for _, t := range b.todos {
			mark, ms, ts := "○ ", th.Fg2, th.Fg2
			switch t.State {
			case model.TodoDone:
				mark, ms, ts = "✓ ", th.Fg3, th.Fg3.StrikeThrough(true)
			case model.TodoActive:
				mark, ms, ts = "● ", th.Run, th.Fg
			}
			pre := append(clone(in), Span{"  ", th.Fg})
			rows = append(rows, wrap([]Span{{mark, ms}, {t.Text, ts}}, w, pre, append(clone(pre), Span{"  ", th.Fg}))...)
		}
	}
	for i := range rows {
		rows[i].Block = len(b.blocks)
		if i > 0 {
			rows[i].Item = todoRow
		}
	}
	return rows
}

// lastMessage is the block of the last message to the thread, from anyone;
// -1 when there is none.
func (b *Buffer) lastMessage() int {
	for i := len(b.blocks) - 1; i >= 0; i-- {
		if b.blocks[i].kind == bMessage {
			return i
		}
	}
	return -1
}

// SetPending shows a chat's queued messages after its transcript.
func (b *Buffer) SetPending(evs []model.Event) {
	if !slices.Equal(b.pending, evs) {
		b.pending = evs
		b.dirty = true
	}
}

func (b *Buffer) hasTodo() bool {
	return slices.ContainsFunc(b.todos, func(t model.Todo) bool { return t.State != model.TodoDone })
}

// SetTodos shows the thread's todo list above the prompt.
func (b *Buffer) SetTodos(todos []model.Todo) {
	if !slices.Equal(b.todos, todos) {
		b.todos = todos
		b.dirty = true
	}
}

// ToggleTodo opens or closes the todo list above the prompt; false when
// there is none.
func (b *Buffer) ToggleTodo() bool {
	if !b.hasTodo() {
		return false
	}
	b.todoOpen = !b.todoOpen
	b.dirty = true
	return true
}

// SetWorking tells a chat whether its thread has a turn running.
// SetAsking hides the last question block while its card shows.
func (b *Buffer) SetAsking(on bool) {
	if b.asking != on {
		b.asking = on
		b.dirty = true
	}
}

func (b *Buffer) SetWorking(on bool) {
	if b.working != on {
		b.working = on
		b.dirty = true
	}
}

// waiting reports whether a turn runs and nothing in it spins yet: claude
// takes seconds to start and to think, and the chat must show that it heard.
func (b *Buffer) waiting() bool {
	if !b.working {
		return false
	}
	for _, bl := range b.blocks {
		if bl.live {
			return false
		}
	}
	return true
}

// joins reports whether block i sits directly under the one before it,
// without a blank line: a run of permission lines reads as one list.
func (b *Buffer) joins(i int) bool {
	k := b.blocks[i].kind
	return (k == bPermission || k == bAnswer) && b.blocks[i-1].kind == bPermission || k == bKey
}

// Live reports whether something in the buffer animates.
func (b *Buffer) Live() bool {
	if b.working {
		return true
	}
	for _, bl := range b.blocks {
		if bl.live {
			return true
		}
	}
	return false
}

// Toggle opens or closes the fold at a block.
func (b *Buffer) Toggle(i int) bool {
	if i < 0 || i >= len(b.blocks) || b.blocks[i].kind != bTools && b.blocks[i].kind != bShell {
		return false
	}
	b.blocks[i].open = !b.blocks[i].open
	b.blocks[i].cache = nil
	b.dirty = true
	return true
}

func (b *Buffer) push(bl *block) {
	b.blocks = append(b.blocks, bl)
	b.dirty = true
}

func (b *Buffer) last() *block {
	if len(b.blocks) == 0 {
		return nil
	}
	return b.blocks[len(b.blocks)-1]
}

// settle stops every activity line except, if the thread is running, the
// last one. Used after loading history, which may end mid-turn.
func (b *Buffer) settle(running bool) {
	lastTools := -1
	for i, bl := range b.blocks {
		if bl.kind == bTools {
			lastTools = i
		}
	}
	for i, bl := range b.blocks {
		if bl.live && !(running && i == lastTools) {
			bl.live = false
			bl.cache = nil
		}
	}
	b.dirty = true
}

func (b *Buffer) endLive() {
	if l := b.last(); l != nil && l.live {
		l.live = false
		l.cache = nil
		b.dirty = true
	}
}

// AddEvent appends a transcript event.
func (b *Buffer) AddEvent(e model.Event) {
	switch e.Kind {
	case model.EventMessage:
		b.endLive()
		b.push(&block{kind: bMessage, from: e.From, text: e.Text})
	case model.EventText:
		b.endLive()
		b.push(&block{kind: bText, text: e.Text})
	case model.EventTool:
		if e.Exit != 0 {
			b.failTool(e.ID, e.Text)
			return
		}
		if l := b.last(); l != nil && l.kind == bTools && l.live {
			l.tools = append(l.tools, toolCall{tool: e.Tool, detail: e.Detail, id: e.ID})
			l.cache = nil
			b.dirty = true
			return
		}
		// Only the newest activity spins; a permission card between two tool
		// calls splits them into two blocks.
		for _, bl := range b.blocks {
			if bl.kind == bTools && bl.live {
				bl.live, bl.cache = false, nil
			}
		}
		b.push(&block{kind: bTools, live: true, tools: []toolCall{{tool: e.Tool, detail: e.Detail, id: e.ID}}})
	case model.EventTurnEnd:
		// A question or permission card after the tool calls is the last
		// block, so ending only the last one left the tools spinning.
		b.settle(false)
	case model.EventError:
		b.settle(false)
		b.push(&block{kind: bError, text: e.Text})
	case model.EventPermission:
		b.push(&block{kind: bPermission, from: e.Tool, text: e.Detail})
	case model.EventAnswer:
		for i := len(b.blocks) - 1; i >= 0; i-- {
			if bl := b.blocks[i]; bl.kind == bPermission && bl.text2 == "" {
				bl.text2 = e.Text
				bl.cache = nil
				b.dirty = true
				return
			}
		}
		b.push(&block{kind: bAnswer, text: e.Text})
	case model.EventAsk:
		b.push(&block{kind: bAsk, text: e.Text})
	case model.EventProgress:
		b.push(&block{kind: bProgress, text: e.Text})
	case model.EventNote:
		b.push(&block{kind: bNote, text: e.Text})
	case model.EventNotice:
		b.push(&block{kind: bNotice, text: e.Text, needsYou: e.NeedsYou})
	case model.EventShell:
		for _, bl := range b.blocks {
			if bl.kind == bShell && bl.id == e.ID {
				bl.text2, bl.exit, bl.live, bl.cache = e.Text, e.Exit, false, nil
				b.dirty = true
				return
			}
		}
		b.endLive()
		b.push(&block{kind: bShell, id: e.ID, text: e.Detail, live: true})
	case model.EventArtifact:
		b.endLive()
		b.push(&block{kind: bArtifact, art: model.Artifact{Name: e.Detail, Title: e.Text, Version: e.Version, UserEdit: e.From == model.User}})
	case model.EventClear:
		b.blocks = nil
		b.push(&block{kind: bNote, text: e.Text})
	case model.EventTakeBack:
		for i := len(b.blocks) - 1; i >= 0; i-- {
			if bl := b.blocks[i]; bl.kind == bMessage && bl.from == model.User && bl.text == e.Text {
				b.blocks = slices.Delete(b.blocks, i, i+1)
				b.dirty = true
				break
			}
		}
	}
}

func (bl *block) render(w int, th *Theme, spin int) []Line {
	if bl.kind == bRaw {
		return bl.raw
	}
	if !bl.live && bl.cache != nil && bl.cacheW == w {
		return bl.cache
	}
	lines := bl.renderFresh(w, th, spin)
	if len(lines) > 0 && (bl.kind == bMessage || bl.kind == bText || bl.kind == bAsk) {
		lines[0].Msg = true
	}
	bl.cache, bl.cacheW = lines, w
	return lines
}

func (bl *block) renderFresh(w int, th *Theme, spin int) []Line {
	pad := []Span{{"  ", th.Fg}}
	switch bl.kind {
	case bMessage:
		if bl.from == model.User {
			// What you typed shows as you typed it: a * is a *.
			bar := []Span{{"▎ ", th.Accent}}
			var lines []Line
			for _, l := range strings.Split(bl.text, "\n") {
				rows := wrap([]Span{{l, th.Fg}}, w, bar, bar)
				if len(rows) == 0 {
					rows = []Line{{Prefix: bar}}
				}
				lines = append(lines, rows...)
			}
			return lines
		}
		bar := []Span{{"▎ ", th.Peer}}
		label := Line{Prefix: bar, Spans: []Span{{"from " + bl.from, th.Peer.Italic(true)}}}
		return append([]Line{label}, renderMarkdown(bl.text, w, th, bar)...)
	case bText:
		if lines, ok := renderJSON(bl.text, w, th, pad); ok {
			return lines
		}
		return renderMarkdown(bl.text, w, th, pad)
	case bTools:
		return bl.renderTools(w, th, spin)
	case bError:
		return wrap([]Span{{bl.text, th.Alert}}, w, []Span{{"  ✗ ", th.Alert}}, []Span{{"    ", th.Alert}})
	case bPermission:
		if bl.text2 == "" {
			return wrap([]Span{{shortTool(bl.from), th.Fg2}, {"  " + bl.text, th.Fg3}}, w,
				[]Span{{"  ◆ ", th.Wait}}, []Span{{"    ", th.Fg}})
		}
		st := th.Fg3
		if strings.HasPrefix(bl.text2, string(model.Deny)) {
			st = th.Alert
		}
		tool, tail := shortTool(bl.from), "  · "+bl.text2
		spans := []Span{{tool, th.Fg3}}
		if bl.text != "" {
			spans = append(spans, Span{"  " + truncate(bl.text, w-4-width(tool)-2-width(tail)), th.Fg3})
		}
		spans = append(spans, Span{tail, st})
		return []Line{{Prefix: []Span{{"  ◇ ", th.Fg3}}, Spans: spans}}
	case bAnswer:
		st := th.Done
		if strings.HasPrefix(bl.text, string(model.Deny)) {
			st = th.Alert
		}
		return wrap([]Span{{bl.text, st}}, w, []Span{{"    ↳ ", th.Fg3}}, []Span{{"      ", th.Fg}})
	case bAsk:
		bar := []Span{{"▎ ", th.Wait}}
		label := Line{Prefix: bar, Spans: []Span{{"question", th.Wait.Italic(true)}}}
		return append([]Line{label}, renderMarkdown(bl.text, w, th, bar)...)
	case bProgress:
		return wrap([]Span{{bl.text, th.Fg2}}, w, []Span{{"  ↳ ", th.Run}}, []Span{{"    ", th.Fg}})
	case bNote:
		return wrap([]Span{{bl.text, th.Fg3.Italic(true)}}, w, []Span{{"  — ", th.Fg3}}, []Span{{"    ", th.Fg}})
	case bNotice:
		glyph := th.Run
		if bl.needsYou {
			glyph = th.Wait
		}
		return wrap([]Span{{"notified you: ", th.Fg3}, {bl.text, th.Fg2}}, w, []Span{{"  ↗ ", glyph}}, []Span{{"    ", th.Fg}})
	case bShell:
		return bl.renderShell(w, th, spin)
	case bKey:
		if bl.text == "" {
			return wrap([]Span{{bl.text2, th.Fg3}}, w, pad, pad)
		}
		// The keys take a column when the window has room for it; else the
		// text goes on the next line.
		if w >= keyCol+30 {
			return wrap([]Span{{bl.text2, th.Fg2}}, w, []Span{{"  ", th.Fg}, {bl.text + strings.Repeat(" ", max(keyCol-2-width(bl.text), 1)), th.Accent}},
				[]Span{{strings.Repeat(" ", keyCol), th.Fg}})
		}
		in := []Span{{"      ", th.Fg}}
		return append(wrap([]Span{{bl.text, th.Accent}}, w, pad, pad), wrap([]Span{{bl.text2, th.Fg2}}, w, in, in)...)
	case bArtifact:
		spans := []Span{{bl.art.Title, th.Fg}, {"  · artifact", th.Fg3}}
		if bl.art.Version > 1 {
			spans = append(spans, Span{" v" + strconv.Itoa(bl.art.Version), th.Fg3})
		}
		if bl.art.UserEdit {
			spans = append(spans, Span{" · edited by you", th.Fg3})
		}
		return wrap(spans, w, []Span{{"  ◇ ", th.Run}}, []Span{{"    ", th.Fg}})
	}
	return nil
}

// keyCol is where the text of a :help row starts.
const keyCol = 31

// shellLines is how many lines of a `!` command's output show before za.
const shellLines = 10

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func (bl *block) renderShell(w int, th *Theme, spin int) []Line {
	mark := Span{"  ! ", th.Peer}
	if bl.live {
		mark = Span{"  " + spinner[spin%len(spinner)] + " ", th.Run}
	}
	lines := wrap([]Span{{bl.text, th.Fg2}}, w, []Span{mark}, []Span{{"    ", th.Fg}})
	out := strings.Split(strings.TrimRight(bl.text2, "\n"), "\n")
	if bl.text2 == "" {
		out = nil
	}
	hidden := 0
	if !bl.open && len(out) > shellLines {
		hidden = len(out) - shellLines
		out = out[:shellLines]
	}
	pad := []Span{{"    ", th.Fg}}
	for _, l := range out {
		l = ansi.ReplaceAllString(l, "")
		if i := strings.LastIndexByte(l, '\r'); i >= 0 {
			l = l[i+1:]
		}
		l = strings.ReplaceAll(l, "\t", "    ")
		lines = append(lines, wrap([]Span{{l, th.Fg3}}, w, pad, pad)...)
	}
	if hidden > 0 {
		lines = append(lines, Line{Prefix: pad, Spans: []Span{{fmt.Sprintf("… %d more lines · za", hidden), th.Fg3.Italic(true)}}})
	}
	if bl.exit != 0 && !bl.live {
		lines = append(lines, Line{Prefix: pad, Spans: []Span{{"exit " + strconv.Itoa(bl.exit), th.Alert}}})
	}
	return lines
}

// failTool marks the tool call with this ID as failed, newest first.
func (b *Buffer) failTool(id, why string) {
	for i := len(b.blocks) - 1; i >= 0 && id != ""; i-- {
		bl := b.blocks[i]
		for k := range bl.tools {
			if bl.tools[k].id == id {
				bl.tools[k].failed = cmp.Or(why, "failed")
				bl.cache = nil
				b.dirty = true
				return
			}
		}
	}
}

func (bl *block) renderTools(w int, th *Theme, spin int) []Line {
	n := len(bl.tools)
	last := bl.tools[n-1]
	count := fmt.Sprintf("%d tool calls", n)
	if n == 1 {
		count = "1 tool call"
	}
	failed := 0
	for _, t := range bl.tools {
		if t.failed != "" {
			failed++
		}
	}
	var mark []Span
	if failed > 0 {
		mark = []Span{{fmt.Sprintf(" · %d failed", failed), th.Alert}}
	}
	var head Line
	switch {
	case bl.live:
		tool, tail := shortTool(last.tool), "  · "+count
		head = Line{Prefix: []Span{{"  " + spinner[spin%len(spinner)] + " ", th.Run}}, Spans: []Span{
			{tool, th.Fg2},
			{"  " + truncate(shortTools(last.detail), w-4-width(tool)-2-width(tail)), th.Fg3},
			{tail, th.Fg3},
		}}
	case bl.open:
		head = Line{Prefix: []Span{{"  ▾ ", th.Fg3}}, Spans: append([]Span{{count, th.Fg3}}, mark...)}
	default:
		head = Line{Prefix: []Span{{"  ▸ ", th.Fg3}}, Spans: append([]Span{{count, th.Fg3}}, mark...)}
	}
	lines := []Line{head}
	if bl.open {
		for _, t := range bl.tools {
			lines = append(lines, Line{Prefix: []Span{{"      ", th.Fg}}, Spans: []Span{
				{shortTool(t.tool), th.Fg2},
				{"  " + truncate(shortTools(t.detail), max(w-24, 10)), th.Fg3},
			}})
			if t.failed != "" {
				// The reason wraps, at most three rows: it says why.
				why := truncate(strings.Join(strings.Fields(t.failed), " "), 3*max(w-12, 10))
				lines = append(lines, wrap([]Span{{why, th.Alert}}, w, []Span{{"        ✗ ", th.Alert}}, []Span{{"          ", th.Alert}})...)
			}
		}
	}
	return lines
}

// shortTool shows an MCP tool as server·tool instead of mcp__server__tool.
// mcpName is an MCP tool's full name inside a text.
var mcpName = regexp.MustCompile(`mcp__([A-Za-z0-9_-]+?)__([A-Za-z0-9_-]+)`)

// shortTools writes the MCP tool names in a tool call's detail as
// shortTool does: ToolSearch's select:mcp__neoharness__x.
func shortTools(s string) string { return mcpName.ReplaceAllString(s, "$1·$2") }

func shortTool(name string) string {
	if rest, ok := strings.CutPrefix(name, "mcp__"); ok {
		if server, tool, ok := strings.Cut(rest, "__"); ok {
			return server + "·" + tool
		}
	}
	return name
}
