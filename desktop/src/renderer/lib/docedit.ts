// The editor around the document schema: markdown shortcuts as you type
// (# for a heading, - for a list, ``` for code), the keys of a text editor
// in Insert mode, highlighted code, and links to artifacts named in code.

import hljs from 'highlight.js/lib/common'
import { baseKeymap, chainCommands, exitCode, setBlockType, lift } from 'prosemirror-commands'
import { history, redo, undo } from 'prosemirror-history'
import { InputRule, inputRules, textblockTypeInputRule, undoInputRule, wrappingInputRule } from 'prosemirror-inputrules'
import { keymap } from 'prosemirror-keymap'
import type { MarkType, Node } from 'prosemirror-model'
import { type Command, Plugin, PluginKey, TextSelection } from 'prosemirror-state'
import { liftListItem, sinkListItem, splitListItem } from 'prosemirror-schema-list'
import { addRowAfter, goToNextCell, isInTable, selectedRect, tableEditing } from 'prosemirror-tables'
import { Decoration, DecorationSet, type EditorView, type NodeView } from 'prosemirror-view'

import { schema } from './mdoc'

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

export function editorPlugins(leave: () => void): Plugin[] {
  return [
    // DocVim.group starts each undo step, by command or by time.
    history({ newGroupDelay: Infinity }),
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
