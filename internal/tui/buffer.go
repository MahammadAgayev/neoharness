package tui

import (
	"cmp"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/MahammadAgayev/neoharness/internal/model"
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
)

// toolCall is one tool call; failed holds why it failed or was denied.
type toolCall struct{ tool, detail, id, failed string }

// block is one logical piece of a transcript. It renders itself to lines and
// caches them per width, so appending to a long transcript only renders the
// new block.
type block struct {
	kind   blockKind
	from   string
	text   string
	text2  string // a permission's answer, a shell command's output
	id     string
	exit   int
	art    model.Artifact
	tools  []toolCall
	live   bool
	open   bool
	raw    []Line
	cacheW int
	cache  []Line
}

// Prompt is the editable region at the end of a chat buffer.
type Prompt struct {
	Text []rune
	Cur  int
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
	// marks are m's places, a line and a column; ' is the place before the
	// last jump.
	marks map[rune][2]int

	blocks []*block
	lines  []Line
	linesW int
	dirty  bool
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
	live := false
	for _, bl := range b.blocks {
		live = live || bl.live
	}
	if !b.dirty && !live && b.linesW == w {
		return b.lines
	}
	var out []Line
	for i, bl := range b.blocks {
		if i > 0 && bl.kind != bRaw && !b.joins(i) {
			out = append(out, Line{Block: i})
		}
		rendered := bl.render(w, th, spin)
		for _, l := range rendered {
			l.Block = i
			out = append(out, l)
		}
	}
	b.lines, b.linesW, b.dirty = out, w, false
	return out
}

// joins reports whether block i sits directly under the one before it,
// without a blank line: a run of permission lines reads as one list.
func (b *Buffer) joins(i int) bool {
	k := b.blocks[i].kind
	return (k == bPermission || k == bAnswer) && b.blocks[i-1].kind == bPermission
}

// Live reports whether something in the buffer animates.
func (b *Buffer) Live() bool {
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
	pad := []Span{{"  ", th.Text}}
	switch bl.kind {
	case bMessage:
		if bl.from == model.User {
			bar := []Span{{"▎ ", th.Iris}}
			return renderMarkdown(bl.text, w, th, bar)
		}
		bar := []Span{{"▎ ", th.Rose}}
		label := Line{Prefix: bar, Spans: []Span{{"from " + bl.from, th.Rose.Italic(true)}}}
		return append([]Line{label}, renderMarkdown(bl.text, w, th, bar)...)
	case bText:
		if lines, ok := renderJSON(bl.text, w, th, pad); ok {
			return lines
		}
		return renderMarkdown(bl.text, w, th, pad)
	case bTools:
		return bl.renderTools(w, th, spin)
	case bError:
		return wrap([]Span{{bl.text, th.Love}}, w, []Span{{"  ✗ ", th.Love}}, []Span{{"    ", th.Love}})
	case bPermission:
		if bl.text2 == "" {
			return wrap([]Span{{shortTool(bl.from), th.Subtle}, {"  " + bl.text, th.Muted}}, w,
				[]Span{{"  ◆ ", th.Gold}}, []Span{{"    ", th.Text}})
		}
		st := th.Muted
		if strings.HasPrefix(bl.text2, string(model.Deny)) {
			st = th.Love
		}
		spans := []Span{{shortTool(bl.from), th.Muted}}
		if bl.text != "" {
			spans = append(spans, Span{"  " + truncate(bl.text, max(w-30, 10)), th.Muted})
		}
		spans = append(spans, Span{"  · " + bl.text2, st})
		return []Line{{Prefix: []Span{{"  ◇ ", th.Muted}}, Spans: spans}}
	case bAnswer:
		st := th.Pine
		if strings.HasPrefix(bl.text, string(model.Deny)) {
			st = th.Love
		}
		return wrap([]Span{{bl.text, st}}, w, []Span{{"    ↳ ", th.Muted}}, []Span{{"      ", th.Text}})
	case bAsk:
		bar := []Span{{"▎ ", th.Gold}}
		label := Line{Prefix: bar, Spans: []Span{{"question", th.Gold.Italic(true)}}}
		return append([]Line{label}, renderMarkdown(bl.text, w, th, bar)...)
	case bProgress:
		return wrap([]Span{{bl.text, th.Subtle}}, w, []Span{{"  ↳ ", th.Foam}}, []Span{{"    ", th.Text}})
	case bNote:
		return wrap([]Span{{bl.text, th.Muted.Italic(true)}}, w, []Span{{"  — ", th.Muted}}, []Span{{"    ", th.Text}})
	case bShell:
		return bl.renderShell(w, th, spin)
	case bArtifact:
		spans := []Span{{bl.art.Title, th.Text}, {"  · artifact", th.Muted}}
		if bl.art.Version > 1 {
			spans = append(spans, Span{" v" + strconv.Itoa(bl.art.Version), th.Muted})
		}
		if bl.art.UserEdit {
			spans = append(spans, Span{" · edited by you", th.Muted})
		}
		return wrap(spans, w, []Span{{"  ◇ ", th.Foam}}, []Span{{"    ", th.Text}})
	}
	return nil
}

// shellLines is how many lines of a `!` command's output show before za.
const shellLines = 10

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func (bl *block) renderShell(w int, th *Theme, spin int) []Line {
	mark := Span{"  ! ", th.Rose}
	if bl.live {
		mark = Span{"  " + spinner[spin%len(spinner)] + " ", th.Foam}
	}
	lines := wrap([]Span{{bl.text, th.Subtle}}, w, []Span{mark}, []Span{{"    ", th.Text}})
	out := strings.Split(strings.TrimRight(bl.text2, "\n"), "\n")
	if bl.text2 == "" {
		out = nil
	}
	hidden := 0
	if !bl.open && len(out) > shellLines {
		hidden = len(out) - shellLines
		out = out[:shellLines]
	}
	pad := []Span{{"    ", th.Text}}
	for _, l := range out {
		l = ansi.ReplaceAllString(l, "")
		if i := strings.LastIndexByte(l, '\r'); i >= 0 {
			l = l[i+1:]
		}
		l = strings.ReplaceAll(l, "\t", "    ")
		lines = append(lines, wrap([]Span{{l, th.Muted}}, w, pad, pad)...)
	}
	if hidden > 0 {
		lines = append(lines, Line{Prefix: pad, Spans: []Span{{fmt.Sprintf("… %d more lines · za", hidden), th.Muted.Italic(true)}}})
	}
	if bl.exit != 0 && !bl.live {
		lines = append(lines, Line{Prefix: pad, Spans: []Span{{"exit " + strconv.Itoa(bl.exit), th.Love}}})
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
		mark = []Span{{fmt.Sprintf(" · %d failed", failed), th.Love}}
	}
	var head Line
	switch {
	case bl.live:
		head = Line{Prefix: []Span{{"  " + spinner[spin%len(spinner)] + " ", th.Foam}}, Spans: []Span{
			{shortTool(last.tool), th.Subtle},
			{"  " + truncate(shortTools(last.detail), max(w-30, 10)), th.Muted},
			{"  · " + count, th.Muted},
		}}
	case bl.open:
		head = Line{Prefix: []Span{{"  ▾ ", th.Muted}}, Spans: append([]Span{{count, th.Muted}}, mark...)}
	default:
		head = Line{Prefix: []Span{{"  ▸ ", th.Muted}}, Spans: append([]Span{{count, th.Muted}}, mark...)}
	}
	lines := []Line{head}
	if bl.open {
		for _, t := range bl.tools {
			lines = append(lines, Line{Prefix: []Span{{"      ", th.Text}}, Spans: []Span{
				{shortTool(t.tool), th.Subtle},
				{"  " + truncate(shortTools(t.detail), max(w-24, 10)), th.Muted},
			}})
			if t.failed != "" {
				lines = append(lines, Line{Prefix: []Span{{"        ✗ ", th.Love}}, Spans: []Span{
					{truncate(t.failed, max(w-12, 10)), th.Love},
				}})
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
