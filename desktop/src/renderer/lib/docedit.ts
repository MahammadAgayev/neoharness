// The editor around the document schema: markdown shortcuts as you type
// (# for a heading, - for a list, ``` for code), the keys of a text editor
// in Insert mode, highlighted code, and links to artifacts named in code.

import hljs from 'highlight.js/lib/common'
import { baseKeymap, chainCommands, exitCode, setBlockType, lift } from 'prosemirror-commands'
import { history, redo, undo } from 'prosemirror-history'
import { InputRule, inputRules, textblockTypeInputRule, undoInputRule, wrappingInputRule } from 'prosemirror-inputrules'
import { keymap } from 'prosemirror-keymap'
import { type Attrs, DOMSerializer, Fragment, type MarkType, type Node, type NodeType } from 'prosemirror-model'
import { type Command, type EditorState, Plugin, PluginKey, Selection, TextSelection, type Transaction } from 'prosemirror-state'
import { liftListItem, sinkListItem, splitListItem } from 'prosemirror-schema-list'
import { addColumnAfter, addRowAfter, deleteColumn, deleteRow, goToNextCell, isInTable, selectedRect, tableEditing } from 'prosemirror-tables'
import { Decoration, DecorationSet, type EditorView, type NodeView } from 'prosemirror-view'

import { fuzzy } from './fuzzy'
import { MarkdownDoc, schema } from './mdoc'

const n = schema.nodes

// markRule makes **text** bold as you type the last *: m[1] is the letter
// before the mark, m[2] the text.
function markRule(re: RegExp, type: MarkType): InputRule {
  return new InputRule(re, (state, m, start, end) =>
    state.tr.replaceWith(start + m[1].length, end, schema.text(m[2], [type.create()])).removeStoredMark(type),
  )
}

const rules = inputRules({
  rules: [
    textblockTypeInputRule(/^(#{1,6})\s$/, n.heading, (m) => ({ level: m[1].length })),
    new InputRule(/^\s*([-+*])\s\[([ xX])\]\s$/, (state, m, start, end) => {
      const tr = state.tr.delete(start, end)
      const $s = tr.doc.resolve(start)
      const range = $s.blockRange()
      if (!range) return null
      tr.wrap(range, [
        { type: n.bullet_list, attrs: { bullet: m[1] } },
        { type: n.list_item, attrs: { checked: m[2] !== ' ' } },
      ])
      return tr
    }),
    new InputRule(/^\[([ xX])\]\s$/, (state, m, start, end) => {
      const $s = state.doc.resolve(start)
      if ($s.depth < 2 || $s.node(-1).type !== n.list_item || $s.index(-1) !== 0) return null
      return state.tr.delete(start, end).setNodeMarkup($s.before(-1), undefined, { checked: m[1] !== ' ' })
    }),
    wrappingInputRule(/^\s*([-+*])\s$/, n.bullet_list, (m) => ({ bullet: m[1] })),
    wrappingInputRule(
      /^(\d+)([.)])\s$/,
      n.ordered_list,
      (m) => ({ order: +m[1], delim: m[2] }),
      (m, node) => node.childCount + node.attrs.order === +m[1],
    ),
    wrappingInputRule(/^\s*>\s$/, n.blockquote),
    textblockTypeInputRule(/^(```|~~~)([\w+-]*)\s$/, n.code_block, (m) => ({ fence: m[1], params: m[2] })),
    new InputRule(/^(?:---|\*\*\*|___)$/, (state, _m, start, end) => {
      const $s = state.doc.resolve(start)
      if ($s.parent.type !== n.paragraph || $s.parent.content.size !== end - start) return null
      const tr = state.tr.replaceWith($s.before(), $s.after(), [n.horizontal_rule.create(), n.paragraph.create()])
      return tr.setSelection(TextSelection.create(tr.doc, $s.before() + 2))
    }),
    markRule(/(^|[^*])\*\*([^*\s](?:[^*]*[^*\s])?)\*\*$/, schema.marks.strong),
    markRule(/(^|[^~])~~([^~\s](?:[^~]*[^~\s])?)~~$/, schema.marks.strike),
    markRule(/(^|[^*\w])\*([^*\s](?:[^*]*[^*\s])?)\*$/, schema.marks.em),
    markRule(/(^|[^`])`([^`]+)`$/, schema.marks.code),
  ],
})

const atStart: Command = (state) => {
  const { $from, empty } = state.selection
  return empty && $from.parentOffset === 0
}

// Backspace at the start of a heading makes it a paragraph, at the start
// of a list item or a quote takes it out, as a word processor does.
const backspace: Command = (state, dispatch) => {
  if (!atStart(state)) return false
  const { $from } = state.selection
  if ($from.parent.type === n.heading || $from.parent.type === n.code_block) return setBlockType(n.paragraph)(state, dispatch)
  if ($from.depth > 1 && $from.node(-1).type === n.list_item && $from.index(-1) === 0) return liftListItem(n.list_item)(state, dispatch)
  if ($from.depth > 1 && $from.node(-1).type === n.blockquote) return lift(state, dispatch)
  return false
}

// Enter in a table goes to the cell below, as in a spreadsheet. In the last
// row it adds a row; on an empty last row it takes the row away and leaves
// the table, as Enter on an empty list item leaves the list.
const tableEnter: Command = (state, dispatch, view) => {
  if (!isInTable(state)) return false
  if (!dispatch || !view) return true
  const rect = selectedRect(state)
  if (rect.top + 1 >= rect.map.height) {
    const row = rect.table.child(rect.top)
    if (row.textContent !== '') {
      addRowAfter(state, dispatch)
      return tableEnter(view.state, dispatch, view)
    }
    const tableEnd = rect.tableStart - 1 + rect.table.nodeSize
    const rowPos = rect.tableStart + rect.map.positionAt(rect.top, 0, rect.table) - 1
    const tr = state.tr
    if (rect.map.height > 2) tr.delete(rowPos, rowPos + row.nodeSize)
    const after = tr.mapping.map(tableEnd)
    if (!tr.doc.resolve(after).nodeAfter?.isTextblock) tr.insert(after, n.paragraph.create())
    dispatch(tr.setSelection(TextSelection.create(tr.doc, after + 1)).scrollIntoView())
    return true
  }
  const pos = rect.tableStart + rect.map.positionAt(rect.top + 1, rect.left, rect.table)
  const cell = state.doc.nodeAt(pos)!
  dispatch(state.tr.setSelection(TextSelection.create(state.doc, pos + 1 + cell.content.size)).scrollIntoView())
  return true
}

// Enter in a list, as Docs and Notion: on an empty item it takes the item
// out, one level or out of the list, even with items after it; at the end
// of an item with sub-items the new item is the first of them, so they
// stay where they are.
const listEnter: Command = (state, dispatch) => {
  const { $from, empty } = state.selection
  if (!empty || $from.depth < 2 || $from.node(-1).type !== n.list_item || $from.index(-1) !== 0) return false
  const item = $from.node(-1)
  if ($from.parent.content.size === 0) return liftListItem(n.list_item)(state, dispatch)
  const sub = item.childCount > 1 ? item.child(1) : null
  if ($from.parentOffset !== $from.parent.content.size || !sub || (sub.type !== n.bullet_list && sub.type !== n.ordered_list)) return false
  const at = $from.after() + 1
  const checked = sub.firstChild?.attrs.checked == null ? null : false
  const tr = state.tr.insert(at, n.list_item.create({ checked }, n.paragraph.create()))
  dispatch?.(tr.setSelection(TextSelection.create(tr.doc, at + 2)).scrollIntoView())
  return true
}

const aligns: Record<string, string | null> = { l: 'left', left: 'left', c: 'center', center: 'center', r: 'right', right: 'right', none: null }

// table runs :table on the table under the cursor: col+ adds a column after
// the cursor's, col- and row- take its column or row away, align sets its
// column's alignment, as GFM aligns by column. It says what went wrong.
export function table(view: EditorView, args: string[]): string | null {
  const [op, arg = ''] = args
  let cmd: Command
  if (op === 'col+') cmd = addColumnAfter
  else if (op === 'col-') cmd = deleteColumn
  else if (op === 'row-') cmd = deleteRow
  else if (op === 'align' && arg in aligns)
    cmd = (state, dispatch) => {
      const rect = selectedRect(state)
      const tr = state.tr
      for (let r = 0; r < rect.map.height; r++) {
        const pos = rect.tableStart + rect.map.positionAt(r, rect.left, rect.table)
        tr.setNodeMarkup(pos, undefined, { ...state.doc.nodeAt(pos)!.attrs, align: aligns[arg] })
      }
      dispatch?.(tr)
      return true
    }
  else return op === 'align' ? 'usage: :table align l|c|r|none' : 'usage: :table col+|col-|row-|align l|c|r|none'
  if (!isInTable(view.state)) return 'not in a table'
  const { top, left } = selectedRect(view.state)
  if (!cmd(view.state, view.dispatch)) return 'a table keeps one column and one row'
  // A column command keeps the cursor in its row, in the column it was in
  // or the one now there.
  if (op.startsWith('col') && isInTable(view.state)) {
    const r = selectedRect(view.state)
    const pos = r.tableStart + r.map.positionAt(Math.min(top, r.map.height - 1), Math.min(left, r.map.width - 1), r.table)
    view.dispatch(view.state.tr.setSelection(Selection.near(view.state.doc.resolve(pos + 1))))
  }
  return null
}

// The block menu: / on an empty line in Insert lists what the page can
// hold, as in Notion; the letters after it filter the list. Anywhere else
// / is a letter, as in a path.
export interface Slash {
  from: number
  query: string
  index: number
}

export interface SlashItem {
  key: string
  label: string
  glyph: string
  // make turns the empty paragraph at pos into the block, and puts the
  // cursor in it.
  make?: (tr: Transaction, pos: number) => void
  artifact?: string
  page?: string
}

export const slashKey = new PluginKey<Slash | null>('slash')

const wrapAt = (tr: Transaction, pos: number, wrappers: { type: NodeType; attrs?: Attrs }[]) => {
  tr.wrap(tr.doc.resolve(pos + 1).blockRange()!, wrappers)
  tr.setSelection(TextSelection.create(tr.doc, pos + 1 + wrappers.length))
}

const blocks: SlashItem[] = [
  ...[1, 2, 3].map((level) => ({
    key: `h${level}`,
    label: `Heading ${level}`,
    glyph: `H${level}`,
    make: (tr: Transaction, pos: number) => void tr.setNodeMarkup(pos, n.heading, { level }),
  })),
  { key: 'bullet', label: 'Bullet list', glyph: '•', make: (tr, pos) => wrapAt(tr, pos, [{ type: n.bullet_list }, { type: n.list_item }]) },
  { key: 'numbered', label: 'Numbered list', glyph: '1.', make: (tr, pos) => wrapAt(tr, pos, [{ type: n.ordered_list }, { type: n.list_item }]) },
  {
    key: 'task',
    label: 'Task list',
    glyph: '☐',
    make: (tr, pos) => wrapAt(tr, pos, [{ type: n.bullet_list }, { type: n.list_item, attrs: { checked: false } }]),
  },
  {
    key: 'table',
    label: 'Table',
    glyph: '⊞',
    make: (tr, pos) => {
      const row = (cell: NodeType) => n.table_row.create(null, [cell.create(), cell.create(), cell.create()])
      tr.replaceWith(pos, pos + 2, n.table.create(null, [row(n.table_header), row(n.table_cell)]))
      tr.setSelection(TextSelection.create(tr.doc, pos + 3))
    },
  },
  {
    key: 'code',
    label: 'Code block',
    glyph: '</>',
    make: (tr, pos) => void tr.setNodeMarkup(pos, n.code_block, { fence: '```', params: '' }),
  },
  { key: 'quote', label: 'Quote', glyph: '❝', make: (tr, pos) => wrapAt(tr, pos, [{ type: n.blockquote }]) },
  {
    key: 'note',
    label: 'Note',
    glyph: 'ⓘ',
    make: (tr, pos) => {
      tr.replaceWith(pos, pos + 2, n.blockquote.create(null, n.paragraph.create(null, schema.text('[!NOTE]\n'))))
      tr.setSelection(TextSelection.create(tr.doc, pos + 2 + '[!NOTE]\n'.length))
    },
  },
  {
    key: 'rule',
    label: 'Rule',
    glyph: '—',
    make: (tr, pos) => {
      tr.replaceWith(pos, pos + 2, [n.horizontal_rule.create(), n.paragraph.create()])
      tr.setSelection(TextSelection.create(tr.doc, pos + 2))
    },
  },
]

// slashItems is what the menu lists for its query: the blocks, the
// artifacts to link, and a new page by the name typed.
export function slashItems(state: EditorState): SlashItem[] {
  const s = slashKey.getState(state)
  if (!s) return []
  const arts = [...(decorations.getState(state)?.artifacts ?? [])].map((a) => ({ key: `a:${a}`, label: a, glyph: '↗', artifact: a }))
  const out = fuzzy([...blocks, ...arts], s.query)
  if (/^[a-z0-9][a-z0-9_-]*$/.test(s.query) && !arts.some((a) => a.artifact === s.query))
    out.push({ key: 'page', label: `New page ${s.query}`, glyph: '+', page: s.query })
  return out
}

// linkArtifact puts an artifact's name in code, as the TUI and the agents
// write a link to one: at the caret in Insert, after the letter under the
// cursor in Normal, as Vim's p. A space keeps it apart from a word before.
export function linkArtifact(state: EditorState, name: string): Transaction {
  const $h = state.selection.$head
  const at = fatCursorKey.getState(state)?.on && $h.pos < $h.end() ? $h.pos + 1 : $h.pos
  const before = $h.parent.textBetween(0, at - $h.start())
  const nodes = [schema.text(name, [schema.marks.code.create()])]
  if (before && !/\s$/.test(before)) nodes.unshift(schema.text(' '))
  const tr = state.tr.insert(at, nodes)
  tr.setSelection(TextSelection.create(tr.doc, at + nodes.length - 1 + name.length))
  return tr.removeStoredMark(schema.marks.code).scrollIntoView()
}

export function slashRun(view: EditorView, it: SlashItem, newPage: (name: string) => void): void {
  const s = slashKey.getState(view.state)!
  const tr = view.state.tr.delete(s.from - 1, view.state.selection.head).setMeta(slashKey, null)
  if (it.make) it.make(tr, s.from - 2)
  view.dispatch(tr.scrollIntoView())
  if (it.artifact) view.dispatch(linkArtifact(view.state, it.artifact))
  if (it.page) newPage(it.page)
}

const slash = new Plugin<Slash | null>({
  key: slashKey,
  state: {
    init: () => null,
    apply: (tr, v, _old, s) => {
      const meta = tr.getMeta(slashKey) as Slash | null | undefined
      if (meta !== undefined) return meta
      if (!v) return null
      const from = tr.mapping.map(v.from, -1)
      const $h = s.selection.$head
      const text = $h.parent.textBetween(0, $h.parent.content.size)
      // The menu goes when the cursor leaves the / or a space ends the word.
      if (!s.selection.empty || $h.start() !== from - 1 || text[0] !== '/' || $h.pos < from || /\s/.test(text)) return null
      const query = text.slice(1, $h.pos - $h.start())
      if (query !== v.query) return { from, query, index: 0 }
      return from === v.from ? v : { ...v, from }
    },
  },
  // Nothing matches: the menu goes and the text stays, as /usr/bin.
  appendTransaction: (_trs, _old, s) => {
    const v = slashKey.getState(s)
    return v && !slashItems(s).length ? s.tr.setMeta(slashKey, null) : null
  },
  props: {
    handleTextInput(view, from, to, text) {
      const { $from } = view.state.selection
      if (text !== '/' || fatCursorKey.getState(view.state)?.on || $from.parent.type !== n.paragraph || $from.parent.content.size || isInTable(view.state)) return false
      view.dispatch(view.state.tr.insertText('/', from, to).setMeta(slashKey, { from: from + 1, query: '', index: 0 }))
      return true
    },
  },
})

// slashKeys are the menu's keys, before any other: <C-n> <C-p> and the
// arrows move, <CR> and <Tab> take, <Esc> closes it and keeps the /.
function slashKeys(newPage: (name: string) => void): Plugin {
  return keymap(
    Object.fromEntries(
      [
        ['ArrowDown', 1],
        ['Ctrl-n', 1],
        ['ArrowUp', -1],
        ['Ctrl-p', -1],
        ['Enter', 0],
        ['Tab', 0],
        ['Escape', null],
      ].map(([key, step]) => [
        key,
        (state: EditorState, dispatch?: (tr: Transaction) => void, view?: EditorView) => {
          const s = slashKey.getState(state)
          if (!s || !dispatch || !view) return !!s
          const items = slashItems(state)
          if (step === null) dispatch(state.tr.setMeta(slashKey, null))
          else if (step === 0) slashRun(view, items[s.index] ?? items[0], newPage)
          else dispatch(state.tr.setMeta(slashKey, { ...s, index: (s.index + (step as number) + items.length) % items.length }))
          return true
        },
      ]),
    ),
  )
}

export function editorPlugins(leave: () => void, newPage: (name: string) => void): Plugin[] {
  return [
    // DocVim.group starts each undo step, by command or by time.
    history({ newGroupDelay: Infinity }),
    slash,
    slashKeys(newPage),
    rules,
    keymap({
      Escape: () => {
        leave()
        return true
      },
      'Mod-z': undo,
      'Shift-Mod-z': redo,
      Enter: chainCommands(tableEnter, listEnter, splitListItem(n.list_item)),
      // A GFM table cell is one line, so it takes no line break.
      'Shift-Enter': chainCommands(exitCode, isInTable, (state, dispatch) => {
        dispatch?.(state.tr.replaceSelectionWith(n.hard_break.create()).scrollIntoView())
        return true
      }),
      // Tab in the last cell of a table adds a row, as in Docs.
      Tab: chainCommands((state, dispatch, view) => {
        if (!isInTable(state)) return false
        if (goToNextCell(1)(state, dispatch)) return true
        if (!dispatch || !view) return true
        addRowAfter(state, dispatch)
        return goToNextCell(1)(view.state, dispatch)
      }, sinkListItem(n.list_item), () => true),
      'Shift-Tab': chainCommands(goToNextCell(-1), liftListItem(n.list_item), () => true),
      // Backspace right after a shortcut fired gives back what you typed.
      Backspace: chainCommands(undoInputRule, backspace),
    }),
    keymap(baseKeymap),
    tableEditing(),
    decorations,
    marks,
    fatCursor,
  ]
}

// spans reads highlight.js HTML as ranges of text with a class.
function spans(html: string): { from: number; to: number; cls: string }[] {
  const out: { from: number; to: number; cls: string }[] = []
  const stack: { cls: string; from: number }[] = []
  let pos = 0
  for (const m of html.matchAll(/<span class="([^"]+)">|<\/span>|&(?:amp|lt|gt|quot|#x27|#39);|[^<&]+/g)) {
    if (m[1]) stack.push({ cls: m[1], from: pos })
    else if (m[0] === '</span>') {
      const s = stack.pop()
      if (s && pos > s.from) out.push({ from: s.from, to: pos, cls: s.cls })
    } else pos += m[0].startsWith('&') ? 1 : m[0].length
  }
  return out
}

const highlights = new Map<string, { from: number; to: number; cls: string }[]>()

function highlight(code: string, lang: string) {
  const key = lang + '\0' + code
  let r = highlights.get(key)
  if (!r) {
    r = hljs.getLanguage(lang) ? spans(hljs.highlight(code, { language: lang, ignoreIllegals: true }).value) : []
    if (highlights.size > 200) highlights.clear()
    highlights.set(key, r)
  }
  return r
}

const alertMark = /^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]/
const alertTitles: Record<string, string> = { NOTE: 'Note', TIP: 'Tip', IMPORTANT: 'Important', WARNING: 'Warning', CAUTION: 'Caution' }

// mentions are the artifacts a text node names, as the TUI finds them: a
// code span or a link that is a name, or in prose a name with a - _ or a
// digit in it, so a plain word is never a link.
export function mentions(node: Node, artifacts: Set<string>): { from: number; to: number; name: string }[] {
  const text = node.text ?? ''
  const link = node.marks.find((m) => m.type === schema.marks.link)?.attrs.href as string | undefined
  if (link && artifacts.has(link)) return [{ from: 0, to: text.length, name: link }]
  if (node.marks.some((m) => m.type === schema.marks.code)) return artifacts.has(text) ? [{ from: 0, to: text.length, name: text }] : []
  const out: { from: number; to: number; name: string }[] = []
  const nameChar = (c: string | undefined) => !!c && /[a-z0-9-]/.test(c)
  for (const name of artifacts) {
    if (!/[-_0-9]/.test(name)) continue
    for (let i = text.indexOf(name); i >= 0; i = text.indexOf(name, i + name.length))
      if (!nameChar(text[i - 1]) && !nameChar(text[i + name.length])) out.push({ from: i, to: i + name.length, name })
  }
  return out.sort((a, b) => a.from - b.from)
}

// linkAt is the artifact or the URL at a position in the document.
export function linkAt(doc: Node, pos: number, artifacts: Set<string>): { artifact?: string; url?: string } | null {
  const $p = doc.resolve(pos)
  if (!$p.parent.isTextblock) return null
  for (const { node, offset } of [$p.parent.childAfter($p.parentOffset), $p.parent.childBefore($p.parentOffset)]) {
    if (!node?.isText) continue
    const at = $p.parentOffset - offset
    const m = mentions(node, artifacts).find((x) => at >= x.from && at <= x.to)
    if (m) return { artifact: m.name }
    const href = node.marks.find((mk) => mk.type === schema.marks.link)?.attrs.href as string | undefined
    if (href) return { url: href }
  }
  return null
}

export const artifactsKey = new PluginKey<Set<string>>('artifacts')

function build(doc: Node, artifacts: Set<string>): DecorationSet {
  const out: Decoration[] = []
  doc.descendants((node, pos) => {
    if (node.type === n.code_block) {
      const lang = String(node.attrs.params).trim().split(/\s/)[0].toLowerCase()
      if (lang) for (const s of highlight(node.textContent, lang)) out.push(Decoration.inline(pos + 1 + s.from, pos + 1 + s.to, { class: s.cls }))
      return false
    }
    if (node.type === n.blockquote) {
      const first = node.firstChild
      const m = first?.type === n.paragraph ? alertMark.exec(first.textContent) : null
      if (m) {
        out.push(Decoration.node(pos, pos + node.nodeSize, { class: `alert alert-${m[1].toLowerCase()}` }))
        out.push(Decoration.inline(pos + 2, pos + 2 + m[0].length, { class: 'alert-mark', 'data-label': alertTitles[m[1]] }))
      }
      return true
    }
    if (node.isText && artifacts.size)
      for (const m of mentions(node, artifacts))
        out.push(Decoration.inline(pos + m.from, pos + m.to, { class: 'artifact-link', 'data-artifact': m.name }))
    return true
  })
  return DecorationSet.create(doc, out)
}

const decorations = new Plugin<{ deco: DecorationSet; artifacts: Set<string> }>({
  state: {
    init: (_c, s) => ({ deco: build(s.doc, new Set()), artifacts: new Set() }),
    apply: (tr, v, _old, s) => {
      const arts = tr.getMeta(artifactsKey) as Set<string> | undefined
      if (arts) return { deco: build(s.doc, arts), artifacts: arts }
      return tr.docChanged ? { deco: build(s.doc, v.artifacts), artifacts: v.artifacts } : v
    },
  },
  props: {
    decorations(s) {
      return this.getState(s)?.deco
    },
  },
})

// blockKey names a top-level block by what it holds, so the same block in
// two versions has the same key. A block keeps its node while you edit
// elsewhere, so the key is made once per node.
const keys = new WeakMap<Node, string>()
function blockKey(n: Node): string {
  let k = keys.get(n)
  if (k === undefined) keys.set(n, (k = JSON.stringify(n.toJSON())))
  return k
}

function blockKeys(text: string): Set<string> {
  const out = new Set<string>()
  new MarkdownDoc(text).doc.forEach((n) => out.add(blockKey(n)))
  return out
}

// marksKey takes the texts an edit compares with: base, the version it saves
// over; start, the text it began on; theirs, the newest version; and the
// blocks both changed, with label naming who wrote theirs. null ends the
// edit and its marks.
export const marksKey = new PluginKey('marks')
// takeKey on a transaction says which conflict it took.
export const takeKey = new PluginKey('take')

export interface MarksMeta {
  base: string
  start: string
  theirs: string
  conflicts: { mine: string; theirs: string; prev: string }[]
  label: string
}
// freshKey takes a range that just came in from outside, an agent's newer
// version or a change you took; null clears its tint.
export const freshKey = new PluginKey('fresh')

interface Marks {
  base: Set<string>
  start: Set<string>
  theirs: Set<string>
  conflicts: { mine: string[]; prev: string; theirs: string }[]
  label: string
}

// A group is where a conflict is in the page: the blocks that hold mine,
// or the place after prev when mine is gone.
export interface Group {
  i: number
  from: number
  to: number
}

function groups(doc: Node, m: Marks): Group[] {
  const nodes: { key: string; from: number; to: number }[] = []
  doc.forEach((n, pos) => nodes.push({ key: blockKey(n), from: pos, to: pos + n.nodeSize }))
  const used = new Set<number>()
  const out: Group[] = []
  m.conflicts.forEach((c, i) => {
    if (!c.mine.length) {
      const j = c.prev ? nodes.findIndex((x) => x.key === c.prev) : -1
      if (c.prev && j < 0) return
      const at = j < 0 ? 0 : nodes[j].to
      out.push({ i, from: at, to: at })
      return
    }
    for (let j = 0; j + c.mine.length <= nodes.length; j++) {
      if (used.has(j) || !c.mine.every((k, d) => nodes[j + d].key === k)) continue
      c.mine.forEach((_k, d) => used.add(j + d))
      out.push({ i, from: nodes[j].from, to: nodes[j + c.mine.length - 1].to })
      return
    }
  })
  return out.sort((a, b) => a.from - b.from)
}

const serializer = DOMSerializer.fromSchema(schema)

// theirsDOM shows a newer version's block under yours, rendered as the page
// is, with Take on it.
function theirsDOM(view: EditorView, text: string, i: number, label: string): HTMLElement {
  const box = document.createElement('div')
  box.className = 'doc-theirs'
  box.contentEditable = 'false'
  const head = document.createElement('div')
  head.className = 'doc-theirs-head'
  const who = document.createElement('span')
  who.textContent = text.trim() ? label : `${label} took this block out`
  const take = document.createElement('button')
  take.className = 'btn small'
  take.textContent = 'Take'
  take.title = 'do'
  take.addEventListener('mousedown', (e) => e.preventDefault())
  take.addEventListener('click', () => view.dispatch(takeTr(view.state, i)))
  head.append(who, take)
  const body = document.createElement('div')
  body.className = 'doc-theirs-body'
  body.append(serializer.serializeFragment(new MarkdownDoc(text).doc.content))
  box.append(head, body)
  return box
}

export function conflictGroups(s: EditorState): Group[] {
  return marks.getState(s)?.groups ?? []
}

// takeTr takes theirs for a conflict: its blocks replace mine, as an edit
// you can undo, and the tint shows what came in.
export function takeTr(s: EditorState, i: number): Transaction {
  const st = marks.getState(s)
  const g = st?.groups.find((x) => x.i === i)
  const c = st?.m?.conflicts[i]
  if (!g || !c) return s.tr
  const content = c.theirs.trim() ? new MarkdownDoc(c.theirs).doc.content : Fragment.empty
  const tr = s.tr.replaceWith(g.from, g.to, content)
  if (tr.doc.childCount === 0) tr.insert(0, schema.nodes.paragraph.create())
  tr.setSelection(Selection.near(tr.doc.resolve(Math.min(g.from + 1, tr.doc.content.size))))
  return tr.setMeta(takeKey, i).setMeta(freshKey, { from: g.from, to: g.from + content.size })
}

// marks puts a bar in the gutter of each block you changed (yours) and each
// block an agent changed since you began (theirs), and a tint that fades on
// what just came in, so a merge shows where it landed without moving text.
const marks = new Plugin<{ m: Marks | null; fresh: { from: number; to: number } | null; deco: DecorationSet; groups: Group[] }>({
  state: {
    init: () => ({ m: null, fresh: null, deco: DecorationSet.empty, groups: [] }),
    apply: (tr, v, _old, s) => {
      const meta = tr.getMeta(marksKey) as MarksMeta | null | undefined
      const fm = tr.getMeta(freshKey) as { from: number; to: number } | null | undefined
      if (meta === undefined && fm === undefined && !tr.docChanged) return v
      let m = v.m
      if (meta === null) m = null
      else if (meta) {
        const keysOf = (t: string) => {
          const out: string[] = []
          if (t.trim()) new MarkdownDoc(t).doc.forEach((n) => out.push(blockKey(n)))
          return out
        }
        m = {
          base: blockKeys(meta.base),
          start: meta.start === meta.base ? blockKeys(meta.base) : blockKeys(meta.start),
          theirs: blockKeys(meta.theirs),
          conflicts: meta.conflicts.map((c) => ({ mine: keysOf(c.mine), prev: keysOf(c.prev).pop() ?? '', theirs: c.theirs })),
          label: meta.label,
        }
      }
      let fresh = fm !== undefined ? fm : v.fresh && { from: tr.mapping.map(v.fresh.from, -1), to: tr.mapping.map(v.fresh.to, 1) }
      if (fresh && fresh.to <= fresh.from) fresh = null
      const out: Decoration[] = []
      const gs = m?.conflicts.length ? groups(s.doc, m) : []
      for (const g of gs) {
        const c = m!.conflicts[g.i]
        out.push(
          Decoration.widget(g.to, (view) => theirsDOM(view, c.theirs, g.i, m!.label), {
            side: 1,
            key: `theirs\0${g.i}\0${m!.label}\0${c.theirs}`,
            ignoreSelection: true,
            stopEvent: () => true,
          }),
        )
      }
      s.doc.forEach((n, pos) => {
        const k = m ? blockKey(n) : ''
        const conflict = gs.some((g) => pos >= g.from && pos < g.to)
        const cls = [
          conflict ? 'doc-mark doc-mark-mine doc-mark-conflict' : '',
          !conflict && m && !m.base.has(k) ? 'doc-mark doc-mark-mine' : !conflict && m && m.theirs.has(k) && !m.start.has(k) ? 'doc-mark doc-mark-agent' : '',
          fresh && pos < fresh.to && pos + n.nodeSize > fresh.from ? 'doc-fresh' : '',
        ]
          .filter(Boolean)
          .join(' ')
        if (cls) out.push(Decoration.node(pos, pos + n.nodeSize, { class: cls }))
      })
      return { m, fresh, deco: DecorationSet.create(s.doc, out), groups: gs }
    },
  },
  props: {
    decorations(s) {
      return this.getState(s)?.deco
    },
  },
})

export const fatCursorKey = new PluginKey<{ on: boolean; at: number | null }>('fatCursor')
// cursorAtKey sets where the block cursor is when it is not the head of the
// selection: in Visual mode the selection takes the letter under the cursor.
export const cursorAtKey = new PluginKey('cursorAt')

// fatCursor draws Vim's block cursor on the letter at the head, in Normal
// and Visual mode; a browser caret is always a line. At the end of a block,
// where there is no letter, it is a space wide.
const fatCursor = new Plugin<{ on: boolean; at: number | null }>({
  key: fatCursorKey,
  state: {
    init: () => ({ on: true, at: null }),
    apply: (tr, v) => {
      const on = (tr.getMeta(fatCursorKey) as boolean | undefined) ?? v.on
      const at = tr.getMeta(cursorAtKey) as number | null | undefined
      if (at !== undefined) return { on, at }
      return { on, at: v.at === null ? null : tr.mapping.map(v.at) }
    },
  },
  props: {
    decorations(s) {
      const st = fatCursorKey.getState(s)
      if (!st?.on) return null
      const $h = st.at === null ? s.selection.$head : s.doc.resolve(Math.min(st.at, s.doc.content.size))
      if (!$h.parent.isTextblock) return null
      const h = $h.pos
      const deco =
        h < $h.end()
          ? Decoration.inline(h, h + 1, { class: 'doc-fat-cursor' })
          : Decoration.widget(h, () => Object.assign(document.createElement('span'), { className: 'doc-fat-cursor eol' }), { side: 1 })
      return DecorationSet.create(s.doc, [deco])
    },
  },
})

// TaskItem is a list item; a task has a box you click to check it.
export class TaskItem implements NodeView {
  dom: HTMLElement
  contentDOM: HTMLElement
  private box: HTMLElement | null = null

  constructor(
    private node: Node,
    view: EditorView,
    getPos: () => number | undefined,
  ) {
    this.dom = document.createElement('li')
    if (node.attrs.checked == null) {
      this.contentDOM = this.dom
      return
    }
    this.box = document.createElement('span')
    this.box.contentEditable = 'false'
    this.box.className = 'task-box'
    this.box.addEventListener('mousedown', (e) => {
      e.preventDefault()
      const pos = getPos()
      if (pos === undefined) return
      view.dispatch(view.state.tr.setNodeMarkup(pos, undefined, { ...this.node.attrs, checked: !this.node.attrs.checked }))
    })
    this.contentDOM = document.createElement('div')
    this.contentDOM.className = 'task-body'
    this.dom.append(this.box, this.contentDOM)
    this.paint()
  }

  private paint() {
    this.dom.className = this.node.attrs.checked ? 'task done' : 'task'
  }

  update(node: Node): boolean {
    if (node.type !== this.node.type || (node.attrs.checked == null) !== (this.node.attrs.checked == null)) return false
    this.node = node
    if (this.box) this.paint()
    return true
  }

  ignoreMutation(m: { type: string; target: globalThis.Node }): boolean {
    return !!this.box && (m.target === this.box || this.box.contains(m.target))
  }
}
