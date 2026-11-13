// Live preview of markdown, as the TUI draws an artifact (render-markdown.nvim,
// Obsidian): every line shows rendered and the lines under the cursor show
// raw, so there is one mode and the text stays the markdown it is.

import { ensureSyntaxTree, syntaxTree } from '@codemirror/language'
import { StateField, type EditorState, type Range } from '@codemirror/state'
import { Decoration, type DecorationSet, EditorView, ViewPlugin, type ViewUpdate, WidgetType } from '@codemirror/view'

import { render } from './markdown'

const alertTitles: Record<string, string> = { NOTE: 'Note', TIP: 'Tip', IMPORTANT: 'Important', WARNING: 'Warning', CAUTION: 'Caution' }

class Label extends WidgetType {
  constructor(readonly text: string) {
    super()
  }
  toDOM() {
    const s = document.createElement('span')
    s.className = 'cm-alert-title'
    s.textContent = this.text
    return s
  }
  eq(o: Label) {
    return o.text === this.text
  }
}

// A table shows in columns, as the TUI draws it, until the cursor goes in.
class Table extends WidgetType {
  constructor(readonly html: string) {
    super()
  }
  toDOM() {
    const d = document.createElement('div')
    d.className = 'cm-table-widget md'
    d.innerHTML = this.html
    return d
  }
  eq(o: Table) {
    return o.html === this.html
  }
  ignoreEvent() {
    return false
  }
}

function tables(state: EditorState): DecorationSet {
  const raw = rawLines(state)
  const out: Range<Decoration>[] = []
  const tree = ensureSyntaxTree(state, state.doc.length, 100) ?? syntaxTree(state)
  tree.iterate({
    enter: (n) => {
      if (n.name !== 'Table') return
      const a = state.doc.lineAt(n.from)
      const b = state.doc.lineAt(n.to)
      for (let l = a.number; l <= b.number; l++) if (raw.has(l)) return false
      out.push(Decoration.replace({ widget: new Table(render(state.sliceDoc(a.from, b.to))), block: true }).range(a.from, b.to))
      return false
    },
  })
  return Decoration.set(out)
}

// Block decorations come from a state field: a view plugin may not replace
// whole lines.
const tableField = StateField.define<DecorationSet>({
  create: tables,
  update: (v, tr) => (tr.docChanged || tr.selection ? tables(tr.state) : v),
  provide: (f) => EditorView.decorations.from(f),
})

class Bullet extends WidgetType {
  toDOM() {
    const s = document.createElement('span')
    s.className = 'cm-bullet'
    s.textContent = '•'
    return s
  }
  eq() {
    return true
  }
}

class Check extends WidgetType {
  constructor(readonly done: boolean) {
    super()
  }
  toDOM() {
    const s = document.createElement('span')
    s.className = `cm-check ${this.done ? 'done' : ''}`
    s.textContent = this.done ? '✓' : ''
    return s
  }
  eq(o: Check) {
    return o.done === this.done
  }
}

const hidden = Decoration.replace({})
const mark = (cls: string) => Decoration.mark({ class: cls })
const line = (cls: string) => Decoration.line({ class: cls })

// rawLines are the line numbers the selection touches: they show raw.
function rawLines(state: EditorState): Set<number> {
  const out = new Set<number>()
  for (const r of state.selection.ranges) {
    const a = state.doc.lineAt(r.from).number
    const b = state.doc.lineAt(r.to).number
    for (let n = a; n <= b; n++) out.add(n)
  }
  return out
}

function build(view: EditorView, artifacts: () => Set<string>): DecorationSet {
  const { state } = view
  const raw = view.hasFocus ? rawLines(state) : new Set<number>()
  const isRaw = (pos: number) => raw.has(state.doc.lineAt(pos).number)
  const out: Range<Decoration>[] = []
  const lines = (from: number, to: number, cls: string) => {
    for (let n = state.doc.lineAt(from).number; n <= state.doc.lineAt(to).number; n++) out.push(line(cls).range(state.doc.line(n).from))
  }
  const hide = (from: number, to: number) => {
    if (from < to && !isRaw(from)) out.push(hidden.range(from, to))
  }
  for (const { from, to } of view.visibleRanges) {
    syntaxTree(state).iterate({
      from,
      to,
      enter: (n) => {
        const h = /^(?:ATX|Setext)Heading(\d)$/.exec(n.name)
        if (h) {
          lines(n.from, n.to, `cm-h cm-h${h[1]}`)
          return
        }
        switch (n.name) {
          case 'HeaderMark': {
            // The # and the space after it.
            const after = state.doc.sliceString(n.to, n.to + 1) === ' ' ? 1 : 0
            hide(n.from, n.to + after)
            return
          }
          case 'StrongEmphasis':
            out.push(mark('cm-strong').range(n.from, n.to))
            return
          case 'Emphasis':
            out.push(mark('cm-em').range(n.from, n.to))
            return
          case 'Strikethrough':
            out.push(mark('cm-strike').range(n.from, n.to))
            return
          case 'EmphasisMark':
          case 'StrikethroughMark':
          case 'LinkMark':
          case 'QuoteMark':
            hide(n.from, n.name === 'QuoteMark' && state.doc.sliceString(n.to, n.to + 1) === ' ' ? n.to + 1 : n.to)
            return
          case 'URL':
            if (n.node.parent?.name === 'Link') hide(n.from, n.to)
            return
          case 'Link': {
            // GitHub's alerts: a quote that starts with [!NOTE] and the like.
            const alert = /^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]$/.exec(state.sliceDoc(n.from, n.to))
            const quote = n.node.parent?.parent
            if (alert && quote?.name === 'Blockquote') {
              lines(quote.from, quote.to, `cm-alert cm-alert-${alert[1].toLowerCase()}`)
              if (!isRaw(n.from)) out.push(Decoration.replace({ widget: new Label(alertTitles[alert[1]]) }).range(n.from, n.to))
              return false
            }
            out.push(mark('cm-link').range(n.from, n.to))
            return
          }
          case 'InlineCode': {
            const text = state.doc.sliceString(n.from, n.to).replace(/^`+|`+$/g, '')
            const art = artifacts().has(text)
            out.push(
              Decoration.mark({
                class: art ? 'cm-icode artifact-link' : 'cm-icode',
                attributes: art ? { 'data-artifact': text } : undefined,
              }).range(n.from, n.to),
            )
            return
          }
          case 'CodeMark':
            if (n.node.parent?.name === 'InlineCode') hide(n.from, n.to)
            return
          case 'FencedCode':
            lines(n.from, n.to, 'cm-fence')
            if (!isRaw(n.from)) out.push(line('cm-fence-mark').range(state.doc.lineAt(n.from).from))
            if (!isRaw(n.to) && state.doc.lineAt(n.to).number !== state.doc.lineAt(n.from).number)
              out.push(line('cm-fence-mark').range(state.doc.lineAt(n.to).from))
            return
          case 'Blockquote':
            lines(n.from, n.to, 'cm-quote')
            return
          case 'ListMark': {
            const t = state.doc.sliceString(n.from, n.to)
            const task = n.node.nextSibling?.firstChild?.name === 'TaskMarker'
            if (!isRaw(n.from) && /^[-*+]$/.test(t) && !task) out.push(Decoration.replace({ widget: new Bullet() }).range(n.from, n.to))
            else if (!isRaw(n.from) && task) hide(n.from, n.to + 1)
            return
          }
          case 'TaskMarker':
            if (!isRaw(n.from)) out.push(Decoration.replace({ widget: new Check(/x/i.test(state.doc.sliceString(n.from, n.to))) }).range(n.from, n.to))
            return
          case 'HorizontalRule':
            out.push(line('cm-hr').range(state.doc.lineAt(n.from).from))
            hide(n.from, n.to)
            return
          case 'Table':
            lines(n.from, n.to, 'cm-table')
            return
        }
      },
    })
  }
  return Decoration.set(out, true)
}

// livePreview draws markdown in place. artifacts names the artifacts, so an
// inline code span that names one is a link to it.
export function livePreview(artifacts: () => Set<string>) {
  return [tableField, inline(artifacts)]
}

function inline(artifacts: () => Set<string>) {
  return ViewPlugin.fromClass(
    class {
      decorations: DecorationSet
      constructor(view: EditorView) {
        this.decorations = build(view, artifacts)
      }
      update(u: ViewUpdate) {
        if (u.docChanged || u.selectionSet || u.viewportChanged || u.focusChanged) this.decorations = build(u.view, artifacts)
      }
    },
    { decorations: (v) => v.decorations },
  )
}
