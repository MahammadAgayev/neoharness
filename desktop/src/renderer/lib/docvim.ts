// Vim's Normal mode over the rich-text document: motions move the cursor in
// the rendered text, operators act on words and blocks, i a o leave for
// Insert and <Esc> comes back. A block (a paragraph, a heading, a list item)
// is Vim's line.

import { closeHistory, redo, undo } from 'prosemirror-history'
import type { Node, ResolvedPos, Slice } from 'prosemirror-model'
import { Selection, TextSelection, type Transaction } from 'prosemirror-state'
import { deleteRow, isInTable } from 'prosemirror-tables'
import type { EditorView } from 'prosemirror-view'

import { cursorAtKey, fatCursorKey } from './docedit'

export type Mode = 'normal' | 'insert' | 'visual' | 'vline'

interface Block {
  node: Node
  from: number
  to: number
}

const cls = (c: string | undefined) => (c === undefined || /\s/.test(c) ? 0 : /[\p{L}\p{N}_]/u.test(c) ? 1 : 2)

function textOf(n: Node): string {
  return n.textBetween(0, n.content.size, undefined, '￼')
}

// Word motions on one block's text, as offsets; null when the block ends.
export function nextWord(t: string, o: number): number | null {
  let i = o
  const c = cls(t[i])
  if (c !== 0) while (i < t.length && cls(t[i]) === c) i++
  while (i < t.length && cls(t[i]) === 0) i++
  return i < t.length ? i : null
}

export function wordEnd(t: string, o: number): number | null {
  let i = o + 1
  while (i < t.length && cls(t[i]) === 0) i++
  if (i >= t.length) return null
  const c = cls(t[i])
  while (i + 1 < t.length && cls(t[i + 1]) === c) i++
  return i
}

export function prevWord(t: string, o: number): number | null {
  let i = o - 1
  while (i >= 0 && cls(t[i]) === 0) i--
  if (i < 0) return null
  const c = cls(t[i])
  while (i > 0 && cls(t[i - 1]) === c) i--
  return i
}

export class DocVim {
  mode: Mode = 'normal'
  query = ''
  private count = ''
  private pending = ''
  private anchor = 0
  // cur is the cursor in Visual mode: the selection runs from anchor to
  // it and takes the letters under both, as in Vim.
  private cur = 0
  // goal is the column j and k keep, as Vim's curswant.
  private goal: number | null = null
  private register: { slice: Slice; lines: boolean; text: string } | null = null
  // step counts Normal commands, so an undo takes back one command or one
  // Insert, as in Vim; grouped is the step the last change belonged to.
  private step = 0
  private grouped = -1
  private last = 0

  constructor(
    private view: EditorView,
    private scroller: () => HTMLElement | null,
    private on: {
      mode: (m: Mode) => void
      find: () => void
      yank: (text: string) => void
      say: (s: string) => void
      open: (pos: number) => boolean
      vim: () => boolean
    },
  ) {}

  // group starts a new undo step for a change from a new Normal command or
  // Insert; without Vim, after half a second of no typing, as a text editor.
  group(tr: Transaction): void {
    if (!tr.docChanged) return
    const now = Date.now()
    const split = this.on.vim() ? this.mode !== 'insert' || this.step !== this.grouped : now - this.last > 500
    this.grouped = this.step
    this.last = now
    if (split) closeHistory(tr)
  }

  busy(): boolean {
    return this.pending !== '' || this.count !== '' || this.mode === 'visual' || this.mode === 'vline'
  }

  setMode(m: Mode): void {
    if (m === this.mode) return
    if (m === 'normal' && this.mode !== 'insert') this.collapse(this.head())
    // Leaving Insert, the cursor goes back onto the last letter, as in Vim.
    if (m === 'normal' && this.mode === 'insert') {
      this.flush()
      const b = this.block()
      const h = this.state.selection.head
      if (b && h > b.from && this.state.selection.empty) this.collapse(h - 1, false)
    }
    this.mode = m
    this.pending = ''
    this.count = ''
    this.view.dom.classList.toggle('vim-normal', m !== 'insert')
    this.view.dispatch(this.view.state.tr.setMeta(fatCursorKey, m !== 'insert').setMeta(cursorAtKey, null))
    this.on.mode(m)
  }

  // mouse takes a selection made outside Vim: a drag or ⌘A in Normal mode
  // enters Visual, as with Vim and a mouse, so d y c act on it. Vim itself
  // never leaves a selection in Normal mode.
  mouse(pointer: boolean): void {
    const sel = this.state.selection
    if (!(this.mode === 'normal' || (this.mode === 'visual' && pointer)) || sel.empty || !(sel instanceof TextSelection)) return
    // The browser's selection ends after the last letter; Vim's cursor is on it.
    const back = sel.head < sel.anchor
    this.anchor = back ? sel.anchor - 1 : sel.anchor
    if (this.mode === 'normal') this.setMode('visual')
    this.moveTo(back ? sel.head : sel.head - 1, false)
  }

  // flush reads the selection a click made in the page: keys never reach
  // ProseMirror in Normal mode, and <Esc> may come before it reads it.
  private flush(): void {
    ;(this.view as unknown as { domObserver: { flush(): void } }).domObserver.flush()
  }

  private get state() {
    return this.view.state
  }

  private blocks(): Block[] {
    const out: Block[] = []
    this.state.doc.descendants((node, pos) => {
      if (!node.isTextblock) return true
      out.push({ node, from: pos + 1, to: pos + 1 + node.content.size })
      return false
    })
    return out
  }

  private block($p: ResolvedPos = this.state.selection.$head): Block | null {
    if (!$p.parent.isTextblock) return null
    return { node: $p.parent, from: $p.start(), to: $p.end() }
  }

  private collapse(pos: number, scroll = true): void {
    const sel = Selection.near(this.state.doc.resolve(Math.max(0, Math.min(pos, this.state.doc.content.size))))
    const tr = this.state.tr.setSelection(sel)
    this.view.dispatch(scroll ? tr.scrollIntoView() : tr)
  }

  private moveTo(pos: number, scroll = true): void {
    const doc = this.state.doc
    pos = Math.max(0, Math.min(pos, doc.content.size))
    if (this.mode === 'visual') {
      this.cur = pos
      const $a = doc.resolve(this.anchor)
      const $c = doc.resolve(pos)
      const after = ($p: typeof $a) => ($p.parent.isTextblock ? Math.min($p.pos + 1, $p.end()) : $p.pos)
      const [anchor, head] = pos >= this.anchor ? [this.anchor, after($c)] : [after($a), pos]
      const tr = this.state.tr.setSelection(TextSelection.between(doc.resolve(anchor), doc.resolve(head))).setMeta(cursorAtKey, pos)
      this.view.dispatch(scroll ? tr.scrollIntoView() : tr)
    } else if (this.mode === 'vline') {
      const a = this.block(doc.resolve(this.anchor))
      const b = this.block(doc.resolve(pos))
      if (!a || !b) return this.collapse(pos, scroll)
      this.cur = pos
      const [from, to] = a.from <= b.from ? [a.from, b.to] : [a.to, b.from]
      const tr = this.state.tr.setSelection(TextSelection.create(doc, from, to)).setMeta(cursorAtKey, pos)
      this.view.dispatch(scroll ? tr.scrollIntoView() : tr)
    } else this.collapse(pos, scroll)
  }

  // scrolloff keeps the cursor line a few lines from the top and bottom
  // edges, as Vim's 'scrolloff'. One rule after the move, measured in the
  // page's line height, so the cursor stays still while the text scrolls.
  private scrolloff(): void {
    const s = this.scroller()
    if (!s) return
    const fs = parseFloat(getComputedStyle(this.view.dom).fontSize) || 16
    const box = s.getBoundingClientRect()
    const margin = Math.min(fs * 1.6 * 4, box.height / 3)
    const c = this.view.coordsAtPos(this.head())
    if (c.bottom > box.bottom - margin) s.scrollTop += c.bottom - (box.bottom - margin)
    else if (c.top < box.top + margin) s.scrollTop -= box.top + margin - c.top
  }

  private head(): number {
    return this.mode === 'visual' || this.mode === 'vline' ? this.cur : this.state.selection.head
  }

  // motion is where a motion key goes from pos, or null for no motion.
  private motion(key: string, pos: number, n: number): number | null {
    const blocks = () => this.blocks()
    const b = this.block(this.state.doc.resolve(pos))
    const last = (bl: Block) => Math.max(bl.from, bl.to - 1)
    switch (key) {
      case 'h':
      case '<Left>':
        return b ? Math.max(b.from, pos - n) : pos
      case 'l':
      case '<Right>':
        return b ? Math.min(last(b), pos + n) : pos
      case '0':
      case '^':
      case '<Home>':
        return b ? b.from + (key === '^' ? (textOf(b.node).match(/^\s*/)?.[0].length ?? 0) : 0) : pos
      case '$':
      case '<End>':
        return b ? last(b) : pos
      case 'w':
      case 'e':
      case 'b': {
        let p = pos
        for (let k = 0; k < n; k++) p = this.word(key, p, blocks())
        return p
      }
      case 'gg':
        return Selection.atStart(this.state.doc).from
      case 'G':
        return Selection.atEnd(this.state.doc).from
      case '}':
      case '{': {
        const bs = blocks()
        let i = bs.findIndex((x) => pos >= x.from && pos <= x.to)
        i = key === '}' ? Math.min(bs.length - 1, i + n) : Math.max(0, i - (b && pos > b.from ? n - 1 : n))
        return bs[i]?.from ?? pos
      }
    }
    return null
  }

  private word(key: string, pos: number, bs: Block[]): number {
    let i = bs.findIndex((x) => pos >= x.from && pos <= x.to)
    if (i < 0) return pos
    let o = pos - bs[i].from
    for (;;) {
      const t = textOf(bs[i].node)
      const r = key === 'w' ? nextWord(t, o) : key === 'e' ? wordEnd(t, o) : prevWord(t, o)
      if (r !== null) return bs[i].from + r
      if (key === 'b' ? i === 0 : i === bs.length - 1) return key === 'b' ? bs[i].from : bs[i].to
      i += key === 'b' ? -1 : 1
      const nt = textOf(bs[i].node)
      // An empty block is a stop, as an empty line is in Vim.
      if (!nt.length) return bs[i].from
      if (key === 'b') o = nt.length
      else if (key === 'w') return bs[i].from + (nt.match(/^\s*/)?.[0].length ?? 0)
      else o = -1
    }
  }

  // vertical is the position a screen line below or above pos, in the
  // column j and k keep; pos when there is no line there.
  private vertical(pos: number, dir: 1 | -1): number {
    const v = this.view
    const s = this.scroller()
    let c = v.coordsAtPos(pos)
    const lh = Math.max(12, c.bottom - c.top)
    // The line it goes to must be on screen to be found; scrolloff keeps it
    // there, except past a block taller than its margin.
    if (s) {
      const box = s.getBoundingClientRect()
      const room = dir > 0 ? box.bottom - c.bottom : c.top - box.top
      if (room < lh * 2) {
        s.scrollTop += dir * (lh * 2 - room)
        c = v.coordsAtPos(pos)
      }
    }
    if (this.goal === null) this.goal = c.left
    for (let dy = lh / 2; dy < 400; dy += 3) {
      const top = dir > 0 ? c.bottom + dy : c.top - dy
      const at = this.textAt(this.goal, top)
      if (at === null) continue
      const r = v.coordsAtPos(at)
      if (dir > 0 ? r.top >= c.bottom - 2 : r.bottom <= c.top + 2) return at
    }
    return pos
  }

  // textAt is the text position at left, top, or null between blocks. A
  // quote, list or table keeps its text right of its margin, so a column
  // left of that text goes to the text block on the same line.
  private textAt(left: number, top: number): number | null {
    const v = this.view
    const at = v.posAtCoords({ left, top })
    if (!at) return null
    const $p = v.state.doc.resolve(at.pos)
    if ($p.parent.isTextblock) return at.pos
    for (const d of [1, -1] as const) {
      const sel = Selection.findFrom($p, d, true)
      if (!sel) continue
      const dom = v.nodeDOM(sel.$from.before())
      if (!(dom instanceof HTMLElement)) continue
      const box = dom.getBoundingClientRect()
      if (top < box.top || top > box.bottom || left >= box.left) continue
      const inside = v.posAtCoords({ left: box.left + 1, top })
      if (inside && v.state.doc.resolve(inside.pos).parent.isTextblock) return inside.pos
    }
    return null
  }

  private lineMove(dir: 1 | -1, n: number): void {
    const goal = this.goal
    let p = this.head()
    for (let k = 0; k < n; k++) p = this.vertical(p, dir)
    if (this.mode === 'normal') {
      const b = this.block(this.state.doc.resolve(p))
      // Normal mode stays on a letter, as l and $ do.
      if (b && p >= b.to && b.to > b.from) p = b.to - 1
    }
    this.moveTo(p, false)
    this.scrolloff()
    this.goal = goal ?? this.goal
  }

  private scroll(f: number): void {
    const s = this.scroller()
    if (!s) return
    const c = this.view.coordsAtPos(this.head())
    s.scrollTop += s.clientHeight * f
    const box = s.getBoundingClientRect()
    const top = Math.max(box.top + 8, Math.min(box.bottom - 24, c.top))
    const at = this.view.posAtCoords({ left: c.left, top: top + 4 })
    if (at) this.moveTo(at.pos)
  }

  // unit is the block dd takes: a list item with one paragraph, a table
  // row, else the text block itself.
  private unit(): { from: number; to: number } | null {
    const $h = this.state.selection.$head
    if (!$h.parent.isTextblock) return null
    for (let d = $h.depth - 1; d > 0; d--) {
      const n = $h.node(d)
      if (n.type.name === 'list_item' && n.childCount === 1) return { from: $h.before(d), to: $h.after(d) }
      if (n.type.name !== 'list_item') break
    }
    return { from: $h.before(), to: $h.after() }
  }

  private yankRange(from: number, to: number, lines: boolean): void {
    const slice = this.state.doc.slice(from, to)
    const text = this.state.doc.textBetween(from, to, '\n', '￼')
    this.register = { slice, lines, text }
    this.on.yank(text)
  }

  private deleteLines(n: number, yank = true): void {
    if (isInTable(this.state)) {
      deleteRow(this.state, this.view.dispatch)
      return
    }
    const first = this.unit()
    if (!first) return
    let to = first.to
    for (let k = 1; k < n; k++) {
      const next = Selection.findFrom(this.state.doc.resolve(to), 1, true)
      if (!next) break
      const save = this.state.selection
      this.view.dispatch(this.state.tr.setSelection(next))
      const u = this.unit()
      this.view.dispatch(this.state.tr.setSelection(save))
      if (!u) break
      to = u.to
    }
    if (yank) this.yankRange(first.from, to, true)
    const tr = this.state.tr.delete(first.from, to)
    if (tr.doc.childCount === 0 || tr.doc.content.size === 0) tr.insert(0, this.state.schema.nodes.paragraph.create())
    const at = Math.min(tr.mapping.map(first.from), tr.doc.content.size)
    tr.setSelection(Selection.near(tr.doc.resolve(at)))
    this.view.dispatch(tr.scrollIntoView())
  }

  private deleteRange(from: number, to: number, insert = false): void {
    if (to < from) [from, to] = [to, from]
    if (to > from) {
      this.yankRange(from, to, false)
      this.view.dispatch(this.state.tr.delete(from, to).scrollIntoView())
    }
    if (insert) this.setMode('insert')
  }

  private put(before: boolean): void {
    const r = this.register
    if (!r) return
    if (r.lines) {
      const u = this.unit()
      if (!u) return
      const at = before ? u.from : u.to
      const tr = this.state.tr.replace(at, at, r.slice)
      tr.setSelection(Selection.near(tr.doc.resolve(Math.min(at + 1, tr.doc.content.size))))
      this.view.dispatch(tr.scrollIntoView())
      return
    }
    const h = this.state.selection.head
    const b = this.block()
    const at = before || !b || b.from === b.to ? h : Math.min(b.to, h + 1)
    this.view.dispatch(this.state.tr.replace(at, at, r.slice).scrollIntoView())
  }

  // open makes a new block below or above the cursor's, as o and O.
  private open(above: boolean): void {
    const $h = this.state.selection.$head
    if (!$h.parent.isTextblock) return
    const { schema } = this.state
    const item = $h.depth > 1 && $h.node($h.depth - 1).type.name === 'list_item' ? $h.depth - 1 : -1
    const tr = this.state.tr
    let at: number
    if (item > 0) {
      const old = $h.node(item)
      at = above ? $h.before(item) : $h.after(item)
      tr.insert(at, schema.nodes.list_item.create({ checked: old.attrs.checked == null ? null : false }, schema.nodes.paragraph.create()))
      at += 2
    } else if ($h.parent.type.name === 'code_block') {
      at = above ? $h.start() : $h.end()
      tr.insertText('\n', at)
      if (!above) at++
    } else {
      at = above ? $h.before() : $h.after()
      tr.insert(at, schema.nodes.paragraph.create())
      at += 1
    }
    tr.setSelection(TextSelection.create(tr.doc, at))
    this.view.dispatch(tr.scrollIntoView())
    this.setMode('insert')
  }

  find(query: string, dir: 1 | -1): boolean {
    if (!query) return false
    this.query = query
    const fold = query === query.toLowerCase()
    const q = fold ? query.toLowerCase() : query
    const bs = this.blocks()
    const h = this.state.selection.head
    const hits: number[] = []
    for (const b of bs) {
      const t = fold ? textOf(b.node).toLowerCase() : textOf(b.node)
      for (let i = t.indexOf(q); i >= 0; i = t.indexOf(q, i + 1)) hits.push(b.from + i)
    }
    if (!hits.length) {
      this.on.say(`not found: ${query}`)
      return false
    }
    const next = dir > 0 ? (hits.find((p) => p > h) ?? hits[0]) : ([...hits].reverse().find((p) => p < h) ?? hits[hits.length - 1])
    this.moveTo(next)
    return true
  }

  deleteWordBack(): void {
    const h = this.state.selection.head
    const b = this.block()
    if (!b) return
    const o = prevWord(textOf(b.node), h - b.from)
    const from = o === null ? b.from : b.from + o
    if (from < h) this.view.dispatch(this.state.tr.delete(from, h))
  }

  // key takes a key in Normal or Visual mode; false means it is not Vim's.
  key(key: string): boolean {
    this.flush()
    if (!/^(j|k|<Down>|<Up>|<CR>|[0-9])$/.test(key)) this.goal = null
    if (this.mode !== 'insert' || key === '<Esc>') this.step++
    if (this.mode === 'insert') {
      if (key === '<Esc>') {
        this.setMode('normal')
        return true
      }
      return false
    }
    if (/^[1-9]$/.test(key) || (key === '0' && this.count)) {
      this.count += key
      return true
    }
    const n = Math.max(1, Number(this.count) || 1)
    const had = this.count !== ''
    const p = this.pending
    if (p === 'g' || p === 'dg' || p === 'yg' || p === 'cg') {
      this.pending = p.slice(0, -1)
      // gf opens the artifact or the link under the cursor, as in the TUI.
      if (key === 'f' && p === 'g') {
        if (!this.on.open(this.head())) this.on.say('no artifact or link here')
        return this.reset()
      }
      if (key !== 'g') return this.reset()
      key = 'gg'
    }
    if (p === 'r') {
      this.reset()
      if (key.length !== 1) return true
      const h = this.state.selection.head
      const b = this.block()
      if (b && h < b.to) this.view.dispatch(this.state.tr.insertText(key, h, h + 1).setSelection(TextSelection.create(this.state.tr.doc, h)))
      return true
    }
    if (p.endsWith('i') || p.endsWith('a')) {
      const op = p[0]
      this.reset()
      if (key !== 'w') return true
      const b = this.block()
      if (!b) return true
      const t = textOf(b.node)
      const o = this.state.selection.head - b.from
      const c = cls(t[o])
      let s = o
      let e = o
      while (s > 0 && cls(t[s - 1]) === c) s--
      while (e < t.length && cls(t[e]) === c) e++
      if (p.endsWith('a')) while (e < t.length && cls(t[e]) === 0) e++
      if (op === 'y') this.yankRange(b.from + s, b.from + e, false)
      else this.deleteRange(b.from + s, b.from + e, op === 'c')
      return true
    }
    this.count = ''

    if (this.mode === 'visual' || this.mode === 'vline') return this.visualKey(key, n)

    const op = this.pending
    if (op === 'd' || op === 'c' || op === 'y') {
      this.pending = ''
      if (key === op) {
        if (op === 'y') {
          const u = this.unit()
          if (u) this.yankRange(u.from, u.to, true)
        } else if (op === 'd') this.deleteLines(n)
        else {
          const b = this.block()
          if (b) this.deleteRange(b.from, b.to, true)
        }
        return true
      }
      if (key === 'i' || key === 'a') {
        this.pending = op + key
        return true
      }
      if (key === 'g') {
        this.pending = op + 'g'
        return true
      }
      const h = this.state.selection.head
      const b = this.block()
      let to: number | null
      if (key === 'w' && op === 'c') to = this.motion('e', h, n)
      else to = this.motion(key, h, n)
      if (to === null || !b) return true
      // e and $ take the letter they stop on; w stops at the end of a block.
      if (key === 'e' || (key === 'w' && op === 'c') || key === '$') to = Math.min(to + 1, Math.max(b.to, to))
      if (key === 'w' && to > b.to) to = b.to
      if (op === 'y') this.yankRange(Math.min(h, to), Math.max(h, to), false)
      else this.deleteRange(h, to, op === 'c')
      return true
    }

    const h = this.state.selection.head
    const b = this.block()
    switch (key) {
      case '<CR>':
        // On an artifact or a link, <CR> opens it, as in the TUI.
        if (!had && this.on.open(h)) return true
        this.lineMove(1, n)
        return true
      case 'j':
      case '<Down>':
        this.lineMove(1, n)
        return true
      case 'k':
      case '<Up>':
        this.lineMove(-1, n)
        return true
      case 'g':
      case 'd':
      case 'c':
      case 'y':
      case 'r':
        this.pending = key
        return true
      case 'G':
        if (had) {
          const bs = this.blocks()
          this.collapse(bs[Math.min(bs.length, n) - 1]?.from ?? h)
          return true
        }
        break
      case '<C-d>':
      case '<C-u>':
        this.scroll(key === '<C-d>' ? 0.5 : -0.5)
        return true
      case '<C-f>':
      case '<PageDown>':
      case '<C-b>':
      case '<PageUp>':
        this.scroll(key === '<C-f>' || key === '<PageDown>' ? 0.9 : -0.9)
        return true
      case 'i':
        this.setMode('insert')
        return true
      case 'a':
        if (b && h < b.to) this.collapse(h + 1)
        this.setMode('insert')
        return true
      case 'I':
        if (b) this.collapse(b.from)
        this.setMode('insert')
        return true
      case 'A':
        if (b) this.collapse(b.to)
        this.setMode('insert')
        return true
      case 'o':
      case 'O':
        this.open(key === 'O')
        return true
      case 'x':
      case '<Del>':
        if (b && h < b.to) this.deleteRange(h, Math.min(b.to, h + n))
        return true
      case 's':
        if (b) this.deleteRange(h, Math.min(b.to, h + n), true)
        else this.setMode('insert')
        return true
      case 'D':
      case 'C':
        if (b) this.deleteRange(h, b.to, key === 'C')
        return true
      case 'p':
      case 'P':
        this.put(key === 'P')
        return true
      case 'u':
        for (let k = 0; k < n; k++) undo(this.state, this.view.dispatch)
        return true
      case '<C-r>':
        for (let k = 0; k < n; k++) redo(this.state, this.view.dispatch)
        return true
      case 'v':
      case 'V':
        this.anchor = h
        this.setMode(key === 'v' ? 'visual' : 'vline')
        this.moveTo(h)
        return true
      case '/':
        this.on.find()
        return true
      case 'n':
      case 'N':
        if (!this.find(this.query, key === 'n' ? 1 : -1)) this.on.say(this.query ? `not found: ${this.query}` : 'no search')
        return true
      case '<Esc>':
        return true
    }
    const to = this.motion(key, h, n)
    if (to === null) return false
    this.collapse(to)
    return true
  }

  private visualKey(key: string, n: number): boolean {
    const sel = this.state.selection
    switch (key) {
      case '<Esc>':
      case 'v':
      case 'V':
        if ((key === 'v' && this.mode === 'vline') || (key === 'V' && this.mode === 'visual')) {
          this.mode = key === 'v' ? 'visual' : 'vline'
          this.on.mode(this.mode)
          this.moveTo(this.head())
        } else this.setMode('normal')
        return true
      case 'y':
        this.yankRange(sel.from, sel.to, this.mode === 'vline')
        this.setMode('normal')
        this.collapse(sel.from)
        return true
      case 'd':
      case 'x':
      case 'c':
      case 's': {
        const lines = this.mode === 'vline'
        this.mode = 'normal'
        if (lines && key !== 'c' && key !== 's') {
          const $a = this.state.doc.resolve(sel.from)
          const $b = this.state.doc.resolve(sel.to)
          if ($a.parent.isTextblock && $b.parent.isTextblock) {
            this.yankRange($a.before(), $b.after(), true)
            const tr = this.state.tr.delete($a.before(), $b.after())
            if (tr.doc.content.size === 0) tr.insert(0, this.state.schema.nodes.paragraph.create())
            tr.setSelection(Selection.near(tr.doc.resolve(Math.min($a.before(), tr.doc.content.size))))
            this.view.dispatch(tr.scrollIntoView())
          }
          this.setMode('normal')
          this.on.mode('normal')
          return true
        }
        this.deleteRange(sel.from, sel.to, key === 'c' || key === 's')
        if (key !== 'c' && key !== 's') {
          this.mode = 'visual'
          this.setMode('normal')
        }
        return true
      }
      case 'o': {
        const h = this.head()
        const a = this.anchor
        this.anchor = h
        this.moveTo(a)
        return true
      }
      case 'j':
      case '<Down>':
        this.lineMove(1, n)
        return true
      case 'k':
      case '<Up>':
        this.lineMove(-1, n)
        return true
      case 'g':
        this.pending = 'g'
        return true
      case '/':
        this.on.find()
        return true
    }
    const to = this.motion(key, this.head(), n)
    if (to === null) return true
    this.moveTo(to)
    return true
  }

  private reset(): boolean {
    this.pending = ''
    this.count = ''
    return true
  }
}
