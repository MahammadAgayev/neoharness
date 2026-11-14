// The artifact as a rich-text document: a ProseMirror schema, and markdown in
// and out of it. Agents read and write artifacts as markdown, so the round
// trip must not change what you did not touch: a top-level block you did not
// edit goes back as the exact text it came from, with the blank lines around
// it. Only a block you edited is written again, in a plain GFM form.

import MarkdownIt from 'markdown-it'
import type Token from 'markdown-it/lib/token.mjs'
import { MarkdownParser, MarkdownSerializer, type MarkdownSerializerState } from 'prosemirror-markdown'
import { Schema, type Mark, type Node } from 'prosemirror-model'

export const schema = new Schema({
  nodes: {
    doc: { content: 'block+' },
    paragraph: { content: 'inline*', group: 'block', parseDOM: [{ tag: 'p' }], toDOM: () => ['p', 0] },
    heading: {
      attrs: { level: { default: 1 } },
      content: 'inline*',
      group: 'block',
      defining: true,
      parseDOM: [1, 2, 3, 4, 5, 6].map((level) => ({ tag: `h${level}`, attrs: { level } })),
      toDOM: (n) => [`h${n.attrs.level}`, 0],
    },
    blockquote: { content: 'block+', group: 'block', defining: true, parseDOM: [{ tag: 'blockquote' }], toDOM: () => ['blockquote', 0] },
    code_block: {
      attrs: { params: { default: '' }, fence: { default: '```' } },
      content: 'text*',
      marks: '',
      group: 'block',
      code: true,
      defining: true,
      parseDOM: [{ tag: 'pre', preserveWhitespace: 'full' }],
      toDOM: (n) => ['pre', { class: 'code', 'data-lang': n.attrs.params || null }, ['code', 0]],
    },
    horizontal_rule: { attrs: { markup: { default: '---' } }, group: 'block', parseDOM: [{ tag: 'hr' }], toDOM: () => ['hr'] },
    // raw is markdown the editor has no node for, as a link reference
    // definition: it shows and goes back as it is.
    raw: {
      content: 'text*',
      marks: '',
      group: 'block',
      code: true,
      toDOM: () => ['pre', { class: 'raw' }, ['code', 0]],
    },
    bullet_list: {
      attrs: { tight: { default: true }, bullet: { default: '-' } },
      content: 'list_item+',
      group: 'block',
      parseDOM: [{ tag: 'ul' }],
      toDOM: (n) => ['ul', { class: n.content.firstChild?.attrs.checked != null ? 'tasks' : null }, 0],
    },
    ordered_list: {
      attrs: { tight: { default: true }, order: { default: 1 }, delim: { default: '.' } },
      content: 'list_item+',
      group: 'block',
      parseDOM: [{ tag: 'ol' }],
      toDOM: (n) => ['ol', { start: n.attrs.order === 1 ? null : n.attrs.order }, 0],
    },
    // checked is null for a list item, true or false for a task.
    list_item: {
      attrs: { checked: { default: null } },
      content: 'block+',
      defining: true,
      parseDOM: [{ tag: 'li' }],
      toDOM: (n) => ['li', { class: n.attrs.checked == null ? null : n.attrs.checked ? 'task done' : 'task' }, 0],
    },
    table: {
      content: 'table_row+',
      group: 'block',
      tableRole: 'table',
      isolating: true,
      parseDOM: [{ tag: 'table' }],
      toDOM: () => ['div', { class: 'table-wrap' }, ['table', ['tbody', 0]]],
    },
    table_row: { content: '(table_cell | table_header)*', tableRole: 'row', parseDOM: [{ tag: 'tr' }], toDOM: () => ['tr', 0] },
    table_cell: cell('td', 'cell'),
    table_header: cell('th', 'header_cell'),
    text: { group: 'inline' },
    image: {
      inline: true,
      attrs: { src: {}, alt: { default: null }, title: { default: null } },
      group: 'inline',
      draggable: true,
      parseDOM: [{ tag: 'img[src]', getAttrs: (d) => ({ src: (d as HTMLElement).getAttribute('src'), alt: (d as HTMLElement).getAttribute('alt') }) }],
      toDOM: (n) => ['img', { src: n.attrs.src, alt: n.attrs.alt, title: n.attrs.title }],
    },
    hard_break: { inline: true, group: 'inline', selectable: false, parseDOM: [{ tag: 'br' }], toDOM: () => ['br'] },
  },
  marks: {
    em: { parseDOM: [{ tag: 'em' }, { tag: 'i' }], toDOM: () => ['em', 0] },
    strong: { parseDOM: [{ tag: 'strong' }, { tag: 'b' }], toDOM: () => ['strong', 0] },
    strike: { parseDOM: [{ tag: 's' }, { tag: 'del' }], toDOM: () => ['s', 0] },
    link: {
      attrs: { href: {}, title: { default: null } },
      inclusive: false,
      parseDOM: [{ tag: 'a[href]', getAttrs: (d) => ({ href: (d as HTMLElement).getAttribute('href'), title: (d as HTMLElement).getAttribute('title') }) }],
      toDOM: (m) => ['a', { href: m.attrs.href, title: m.attrs.title }, 0],
    },
    code: { parseDOM: [{ tag: 'code' }], toDOM: () => ['code', 0] },
  },
})

function cell(tag: string, role: string) {
  return {
    attrs: { align: { default: null }, colspan: { default: 1 }, rowspan: { default: 1 }, colwidth: { default: null } },
    content: 'inline*',
    tableRole: role,
    isolating: true,
    parseDOM: [{ tag }],
    toDOM: (n: Node) => [tag, { style: n.attrs.align ? `text-align: ${n.attrs.align}` : null }, 0] as const,
  }
}

// HTML stays text: the editor shows what the agent wrote, and it goes back
// as it came.
const md = MarkdownIt('default', { html: false, linkify: false, typographer: false })

// GFM task lists: "- [ ] x" is a list item with a box.
md.core.ruler.after('block', 'tasks', (state) => {
  const ts = state.tokens
  for (let i = 0; i + 2 < ts.length; i++) {
    if (ts[i].type !== 'list_item_open' || ts[i + 1].type !== 'paragraph_open' || ts[i + 2].type !== 'inline') continue
    const m = /^\[([ xX])\](?:[ \t]+|$)/.exec(ts[i + 2].content)
    if (!m) continue
    ts[i].attrSet('checked', m[1] === ' ' ? 'false' : 'true')
    ts[i + 2].content = ts[i + 2].content.slice(m[0].length)
  }
  return true
})

const align = (t: Token) => /text-align:\s*(left|right|center)/.exec(t.attrGet('style') ?? '')?.[1] ?? null

const parser = new MarkdownParser(schema, md, {
  blockquote: { block: 'blockquote' },
  paragraph: { block: 'paragraph' },
  list_item: { block: 'list_item', getAttrs: (t) => ({ checked: t.attrGet('checked') === null ? null : t.attrGet('checked') === 'true' }) },
  bullet_list: { block: 'bullet_list', getAttrs: (t, ts, i) => ({ tight: tight(ts, i), bullet: t.markup || '-' }) },
  ordered_list: {
    block: 'ordered_list',
    getAttrs: (t, ts, i) => ({ order: +(t.attrGet('start') ?? 1), tight: tight(ts, i), delim: t.markup === ')' ? ')' : '.' }),
  },
  heading: { block: 'heading', getAttrs: (t) => ({ level: +t.tag.slice(1) }) },
  code_block: { block: 'code_block', noCloseToken: true, getAttrs: () => ({ fence: '```' }) },
  fence: { block: 'code_block', noCloseToken: true, getAttrs: (t) => ({ params: t.info || '', fence: t.markup[0] === '~' ? '~~~' : '```' }) },
  hr: { node: 'horizontal_rule', getAttrs: (t) => ({ markup: t.markup || '---' }) },
  image: { node: 'image', getAttrs: (t) => ({ src: t.attrGet('src'), title: t.attrGet('title') || null, alt: t.children?.[0]?.content || null }) },
  hardbreak: { node: 'hard_break' },
  em: { mark: 'em' },
  strong: { mark: 'strong' },
  s: { mark: 'strike' },
  link: { mark: 'link', getAttrs: (t) => ({ href: t.attrGet('href'), title: t.attrGet('title') || null }) },
  code_inline: { mark: 'code', noCloseToken: true },
  table: { block: 'table' },
  thead: { ignore: true },
  tbody: { ignore: true },
  tr: { block: 'table_row' },
  th: { block: 'table_header', getAttrs: (t) => ({ align: align(t) }) },
  td: { block: 'table_cell', getAttrs: (t) => ({ align: align(t) }) },
})
// A line break in a paragraph stays one, as the agent wrote it.
;(parser as unknown as { tokenHandlers: Record<string, (s: { addText: (t: string) => void }) => void> }).tokenHandlers.softbreak = (s) =>
  s.addText('\n')

function tight(ts: readonly Token[], i: number): boolean {
  while (++i < ts.length) if (ts[i].type !== 'list_item_open') return ts[i].hidden
  return false
}

const alertMark = /^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]/

function list(state: MarkdownSerializerState, node: Node, marker: (i: number) => string) {
  const width = Math.max(...Array.from({ length: node.childCount }, (_, i) => marker(i).length))
  state.renderList(node, ' '.repeat(width), (i) => {
    const box = node.child(i).attrs.checked
    return marker(i).padEnd(width) + (box == null ? '' : box ? '[x] ' : '[ ] ')
  })
}

function cellText(state: MarkdownSerializerState, c: Node): string {
  // A cell is one line; its inline markdown is made as a paragraph's.
  const s = new (state.constructor as new (n: unknown, m: unknown, o: unknown) => MarkdownSerializerState)(
    (state as unknown as { nodes: unknown }).nodes,
    (state as unknown as { marks: unknown }).marks,
    state.options,
  )
  s.renderInline(c)
  return (s as unknown as { out: string }).out.replace(/\n/g, ' ').replace(/\|/g, '\\|').trim()
}

const serializer = new MarkdownSerializer(
  {
    blockquote: (s, n) => s.wrapBlock('> ', null, n, () => s.renderContent(n)),
    code_block: (s, n) => {
      const tick = n.attrs.fence[0] as string
      const runs = n.textContent.match(new RegExp(`${tick === '`' ? '`' : '~'}{3,}`, 'g')) ?? []
      const fence = tick.repeat(Math.max(3, ...runs.map((r) => r.length + 1)))
      s.write(fence + (n.attrs.params || '') + '\n')
      s.text(n.textContent, false)
      s.ensureNewLine()
      s.write(fence)
      s.closeBlock(n)
    },
    raw: (s, n) => {
      s.text(n.textContent, false)
      s.closeBlock(n)
    },
    heading: (s, n) => {
      s.write('#'.repeat(n.attrs.level) + ' ')
      s.renderInline(n, false)
      s.closeBlock(n)
    },
    horizontal_rule: (s, n) => {
      s.write(n.attrs.markup || '---')
      s.closeBlock(n)
    },
    bullet_list: (s, n) => list(s, n, () => `${n.attrs.bullet} `),
    ordered_list: (s, n) => list(s, n, (i) => `${n.attrs.order + i}${n.attrs.delim} `),
    list_item: (s, n) => s.renderContent(n),
    paragraph: (s, n) => {
      s.renderInline(n)
      s.closeBlock(n)
    },
    table: (s, n) => {
      const rows: string[][] = []
      n.forEach((r) => {
        const row: string[] = []
        r.forEach((c) => row.push(cellText(s, c)))
        rows.push(row)
      })
      const cols = Math.max(...rows.map((r) => r.length))
      const aligns: (string | null)[] = []
      n.firstChild?.forEach((c) => aligns.push(c.attrs.align))
      const line = (r: string[]) => '| ' + Array.from({ length: cols }, (_, i) => r[i] ?? '').join(' | ') + ' |'
      const rule = Array.from({ length: cols }, (_, i) =>
        aligns[i] === 'center' ? ':---:' : aligns[i] === 'right' ? '---:' : aligns[i] === 'left' ? ':---' : '---',
      )
      s.write([line(rows[0] ?? []), '|' + rule.join('|') + '|', ...rows.slice(1).map(line)].join('\n'))
      s.closeBlock(n)
    },
    table_row: () => {},
    table_cell: () => {},
    table_header: () => {},
    image: (s, n) =>
      s.write(
        '![' + s.esc(n.attrs.alt || '') + '](' + n.attrs.src.replace(/[()]/g, '\\$&') + (n.attrs.title ? ` "${n.attrs.title.replace(/"/g, '\\"')}"` : '') + ')',
      ),
    hard_break: (s, n, parent, index) => {
      for (let i = index + 1; i < parent.childCount; i++)
        if (parent.child(i).type !== n.type) {
          s.write('\\\n')
          return
        }
    },
    text: (s, n, parent, index) => {
      // An alert's [!NOTE] is no link: it goes back unescaped.
      const m = index === 0 && parent.type.name === 'paragraph' ? alertMark.exec(n.text!) : null
      if (m) {
        s.text(m[0], false)
        s.text(n.text!.slice(m[0].length))
        return
      }
      s.text(n.text!, !(s as unknown as { inAutolink?: boolean }).inAutolink)
    },
  },
  {
    em: { open: '*', close: '*', mixable: true, expelEnclosingWhitespace: true },
    strong: { open: '**', close: '**', mixable: true, expelEnclosingWhitespace: true },
    strike: { open: '~~', close: '~~', mixable: true, expelEnclosingWhitespace: true },
    link: {
      open: (s, mark, parent, index) => {
        const auto = plainURL(mark, parent, index)
        ;(s as unknown as { inAutolink?: boolean }).inAutolink = auto
        return auto ? '<' : '['
      },
      close: (s, mark) => {
        const st = s as unknown as { inAutolink?: boolean }
        const auto = st.inAutolink
        st.inAutolink = undefined
        return auto ? '>' : '](' + mark.attrs.href.replace(/[()"]/g, '\\$&') + (mark.attrs.title ? ` "${mark.attrs.title.replace(/"/g, '\\"')}"` : '') + ')'
      },
      mixable: true,
    },
    code: {
      open: (_s, _m, parent, index) => ticks(parent.child(index), -1),
      close: (_s, _m, parent, index) => ticks(parent.child(index - 1), 1),
      escape: false,
    },
  },
  // < before a letter would start HTML in other renderers.
  { escapeExtraCharacters: /<(?=[A-Za-z/!?])/g, strict: true },
)

function ticks(n: Node, side: number): string {
  let len = 0
  if (n.isText) for (const m of n.text!.matchAll(/`+/g)) len = Math.max(len, m[0].length)
  const t = '`'.repeat(len + 1)
  return len === 0 ? '`' : side < 0 ? t + ' ' : ' ' + t
}

function plainURL(link: Mark, parent: Node, index: number): boolean {
  if (link.attrs.title || !/^\w+:/.test(link.attrs.href ?? '')) return false
  const c = parent.child(index)
  if (!c.isText || c.text !== link.attrs.href) return false
  return index === parent.childCount - 1 || !link.isInSet(parent.child(index + 1).marks)
}

// Source is where a parsed top-level node came from.
interface Source {
  text: string
  // before is the text between the block before it and this one.
  before: string
  index: number
}

// MarkdownDoc is one artifact's text as a document, and the memory of where
// each untouched block came from.
export class MarkdownDoc {
  readonly doc: Node
  private sources = new WeakMap<Node, Source>()
  private byJSON = new Map<string, Source>()
  private tail: string
  private last = -1

  constructor(text: string) {
    const lines = text.split('\n')
    const starts: number[] = []
    let at = 0
    for (const l of lines) {
      starts.push(at)
      at += l.length + 1
    }
    const end = (line: number) => starts[line] + lines[line].length
    const tokens = md.parse(text, {})
    const top: { a: number; b: number }[] = []
    for (const t of tokens) if (t.level === 0 && t.nesting >= 0 && t.map) top.push({ a: t.map[0], b: t.map[1] })
    // Lines no block took (as link definitions) become raw blocks, so
    // nothing is dropped.
    const blocks: { a: number; b: number; raw: boolean }[] = []
    let line = 0
    const gap = (to: number) => {
      for (let k = line; k < to; ) {
        if (!lines[k].trim()) {
          k++
          continue
        }
        let j = k
        while (j < to && lines[j].trim()) j++
        blocks.push({ a: k, b: j, raw: true })
        k = j
      }
    }
    for (const { a, b } of top) {
      gap(a)
      let e = b
      while (e > a + 1 && !lines[e - 1].trim()) e--
      blocks.push({ a, b: e, raw: false })
      line = b
    }
    gap(lines.length)

    const parsed = parser.parse(text)
    const kids: Node[] = []
    let p = 0
    let prevEnd = 0
    blocks.forEach((bl, index) => {
      const src = text.slice(starts[bl.a], end(bl.b - 1))
      const node = bl.raw ? schema.nodes.raw.create(null, schema.text(src)) : parsed.child(p++)
      const s: Source = { text: src, before: text.slice(prevEnd, starts[bl.a]), index }
      this.sources.set(node, s)
      this.byJSON.set(JSON.stringify(node.toJSON()), s)
      kids.push(node)
      prevEnd = end(bl.b - 1)
    })
    this.tail = text.slice(prevEnd)
    this.last = blocks.length - 1
    if (kids.length === 0) {
      kids.push(schema.nodes.paragraph.create())
      this.tail = text
    }
    this.doc = schema.nodes.doc.create(null, kids)
  }

  private source(n: Node): Source | undefined {
    let s = this.sources.get(n)
    if (!s) {
      // Undo makes a new node equal to the old one: it is still untouched.
      s = this.byJSON.get(JSON.stringify(n.toJSON()))
      if (s) this.sources.set(n, s)
    }
    return s
  }

  // markdown is the text of a document made from this one.
  markdown(doc: Node): string {
    let out = ''
    // slot is the source index the last block holds: an edited block holds
    // the place of the one after the block before it.
    let slot = -1
    let last: Source | undefined
    let first = true
    doc.forEach((n) => {
      const s = this.source(n)
      // An empty paragraph is a line you opened and left empty.
      if (!s && n.type.name === 'paragraph' && n.childCount === 0) return
      if (first) out += s?.index === 0 ? s.before : ''
      else out += s && s.index === slot + 1 ? s.before : '\n\n'
      out += s ? s.text : toMarkdown(n)
      slot = s ? s.index : slot + 1
      last = s
      first = false
    })
    if (last && last.index === this.last) return out + this.tail
    return out + (/\n$/.test(this.tail) ? '\n' : '')
  }
}

// toMarkdown writes one block in plain GFM.
export function toMarkdown(n: Node): string {
  return serializer.serialize(schema.nodes.doc.create(null, n), { tightLists: true }).replace(/\n+$/, '')
}

// parse is markdown as a document, for tests and a whole new text.
export function parse(text: string): Node {
  return new MarkdownDoc(text).doc
}

// normalize is the markdown of a text as an edit of every block would write it.
export function normalize(text: string): string {
  const doc = parse(text)
  const out: string[] = []
  doc.forEach((n) => out.push(toMarkdown(n)))
  return out.join('\n\n') + '\n'
}

// html is how a markdown text reads, for tests that compare two texts.
export function html(text: string): string {
  return md.render(text)
}
