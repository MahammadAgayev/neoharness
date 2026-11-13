import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { markdown } from '@codemirror/lang-markdown'
import { Compartment, EditorState } from '@codemirror/state'
import { EditorView, keymap } from '@codemirror/view'
import { getCM, vim, Vim } from '@replit/codemirror-vim'
import { useEffect, useMemo, useRef, useState } from 'react'

import { hunks, lines, type Hunk } from '../lib/linediff'
import { store, useStore, type Doc, type Win } from '../store'
import { Markdown } from './Markdown'
import { editorKeys, editors, handlers, type WinAction } from './registry'

const editorTheme = EditorView.theme({
  '&': { background: 'transparent', color: 'var(--text)', height: '100%' },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '1.6' },
  // The column of the rendered document: the text stays where it was.
  '.cm-content': { caretColor: 'var(--iris)', padding: '28px max(32px, calc((100% - 780px) / 2))' },
  '.cm-cursor': { borderLeftColor: 'var(--iris)', borderLeftWidth: '2px' },
  '.cm-fat-cursor': { background: 'var(--iris) !important', color: 'var(--base) !important' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': { background: 'var(--hl-med) !important' },
  '.cm-activeLine': { background: 'var(--hl-low)' },
  '&:not(.cm-focused) .cm-cursorLayer': { display: 'none' },
  '.cm-gutters': { display: 'none' },
})

// hunkAt is the change at a source line, as diffTake in the TUI: a line it
// changes, or the line above lines it adds.
function hunkAt(hs: Hunk[], line: number): number {
  const i = hs.findIndex((h) => line >= h.mFrom && line < Math.max(h.mTo, h.mFrom + 1))
  return i >= 0 ? i : hs.findIndex((h) => h.mFrom === h.mTo && h.mFrom === line + 1)
}

function conflictHunks(name: string): Hunk[] | null {
  const d = store.state.docs[name]
  if (!d?.conflict || !d.edit) return null
  return hunks(lines(d.edit.text.split('\n'), d.text.split('\n')))
}

function SourceEditor(props: { name: string; text: string; onLeave: () => void; win: number; onHunk: (i: number) => void }) {
  const host = useRef<HTMLDivElement>(null)
  const vimOn = useStore((s) => s.settings.vim)
  const comp = useRef(new Compartment())
  const viewRef = useRef<EditorView | null>(null)
  const leave = useRef(props.onLeave)
  leave.current = props.onLeave
  const onHunk = useRef(props.onHunk)
  onHunk.current = props.onHunk

  useEffect(() => {
    const view = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: props.text,
        extensions: [
          comp.current.of(store.state.settings.vim ? vim({ status: false }) : []),
          history(),
          keymap.of([
            {
              key: 'Escape',
              run: () => {
                if (store.state.settings.vim) return false
                leave.current()
                return true
              },
            },
            indentWithTab,
            ...historyKeymap,
            ...defaultKeymap,
          ]),
          markdown(),
          EditorView.lineWrapping,
          editorTheme,
          EditorView.updateListener.of((u) => {
            if (u.docChanged) store.editText(props.name, u.state.doc.toString())
            if (u.focusChanged) store.setInsert(u.view.hasFocus && (!store.state.settings.vim || !!getCM(u.view)?.state.vim?.insertMode))
          }),
          EditorView.domEventHandlers({ focus: () => store.focus(props.win) }),
        ],
      }),
    })
    viewRef.current = view
    editors.add(view)
    const cm = getCM(view)
    const onMode = (e: { mode: string }) => store.setInsert(e.mode === 'insert')
    cm?.on('vim-mode-change', onMode)
    const line = () => view.state.doc.lineAt(view.state.selection.main.head).number - 1
    const toLine = (n: number) => {
      const l = view.state.doc.line(Math.min(n, view.state.doc.lines - 1) + 1)
      view.dispatch({ selection: { anchor: l.from }, scrollIntoView: true })
    }
    // In a conflict ]c [c move between changes and do dp take one, as in
    // Neovim's diff mode; Vim gets d back when o or p does not follow.
    let held = ''
    editorKeys.set(view, (key) => {
      const hs = conflictHunks(props.name)
      if (held) {
        const first = held
        held = ''
        if (hs && (first === ']' || first === '[') && key === 'c') {
          const at = line()
          const h = first === ']' ? hs.find((x) => x.mFrom > at) : [...hs].reverse().find((x) => x.mFrom < at)
          if (h) {
            toLine(h.mFrom)
            onHunk.current(hs.indexOf(h))
          }
          return true
        }
        if (hs && first === 'd' && (key === 'o' || key === 'p')) {
          const at = line()
          const i = hunkAt(hs, at)
          if (i < 0) store.say('no change here', 'muted')
          else {
            store.takeHunk(props.name, i)
            requestAnimationFrame(() => toLine(hs[i].mFrom))
          }
          return true
        }
        const cm = getCM(view)
        if (cm) {
          Vim.handleKey(cm as never, first, 'user')
          Vim.handleKey(cm as never, key, 'user')
        }
        return true
      }
      if (hs && (key === ']' || key === '[' || key === 'd')) {
        held = key
        return true
      }
      // <Esc> in Normal mode shows the document rendered again, as the TUI does.
      if (key === '<Esc>') {
        leave.current()
        return true
      }
      return false
    })
    view.focus()
    // i a o start an edit in Insert mode, as in the TUI.
    if (cm && store.state.settings.vim) Vim.handleKey(cm as never, 'i', 'mapping')
    return () => {
      cm?.off('vim-mode-change', onMode)
      editors.delete(view)
      view.destroy()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.name])

  // A merge or :e! changes the text under the editor.
  useEffect(() => {
    const v = viewRef.current
    if (v && v.state.doc.toString() !== props.text) v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: props.text } })
  }, [props.text])

  useEffect(() => {
    viewRef.current?.dispatch({ effects: comp.current.reconfigure(vimOn ? vim({ status: false }) : []) })
  }, [vimOn])

  return <div className="source-editor" ref={host} />
}

// Conflict is what a refused :w shows: your edit against the newer version,
// change by change. do or a click takes one change; :w saves the rest as
// yours.
function Conflict(props: { name: string; doc: Doc; cursor: number; setCursor: (i: number) => void }) {
  const [open, setOpen] = useState(true)
  const { doc, cursor } = props
  const v = doc.artifact.version
  const ops = useMemo(() => lines((doc.edit?.text ?? '').split('\n'), doc.text.split('\n')), [doc.text, doc.edit?.text])
  const hs = useMemo(() => hunks(ops), [ops])
  const starts = new Map(hs.map((h, i) => [h.at, i]))
  const box = useRef<HTMLPreElement>(null)
  useEffect(() => {
    box.current?.querySelector('.hunk.active')?.scrollIntoView({ block: 'nearest' })
  }, [cursor, open])
  const rows: React.ReactNode[] = []
  for (let k = 0; k < ops.length; ) {
    const i = starts.get(k)
    if (i === undefined) {
      rows.push(<DiffLine key={k} kind={ops[k].kind} text={ops[k].text} />)
      k++
      continue
    }
    const from = k
    while (k < ops.length && ops[k].kind !== ' ') k++
    rows.push(
      <div key={from} className={`hunk ${i === cursor ? 'active' : ''}`} onMouseDown={() => props.setCursor(i)}>
        <button className="btn small hunk-take" onClick={() => store.takeHunk(props.name, i)}>
          Take v{v} <kbd>do</kbd>
        </button>
        {ops.slice(from, k).map((o, n) => (
          <DiffLine key={n} kind={o.kind} text={o.text} />
        ))}
      </div>,
    )
  }
  return (
    <div className="conflict">
      <div className="conflict-head">
        <span className="t-love">●</span>
        <span>
          {doc.artifact.thread} changed the same lines in v{v}
        </span>
        <span className="t-muted">
          {hs.length === 1 ? '1 change' : `${hs.length} changes`} · <kbd>]c</kbd> <kbd>[c</kbd> move
        </span>
        <span className="sl-fill" />
        <button className="btn small ghost" onClick={() => setOpen(!open)}>
          {open ? 'Hide diff' : 'Show diff'}
        </button>
        <button className="btn small" onClick={() => void store.revert(props.name)}>
          Take all of v{v} <kbd>:e!</kbd>
        </button>
        <button className="btn small primary" onClick={() => void store.save(props.name)}>
          Save <kbd>:w</kbd>
        </button>
      </div>
      {open && (
        <pre className="diff" ref={box}>
          <div className="diff-legend t-muted">
            <span className="t-love">− yours</span> <span className="t-pine">+ v{v}</span>
          </div>
          {rows}
        </pre>
      )}
    </div>
  )
}

function DiffLine(props: { kind: ' ' | '-' | '+'; text: string }) {
  return (
    <div className={`diff-line d-${props.kind === '-' ? 'del' : props.kind === '+' ? 'add' : 'same'}`}>
      <span className="diff-mark">{props.kind}</span>
      {props.text || ' '}
    </div>
  )
}

export function ArtifactWindow(props: { win: Win; focused: boolean }) {
  const { win, focused } = props
  const name = win.buf.kind === 'artifact' ? win.buf.name : ''
  const doc = useStore((s) => s.docs[name])
  const workspace = useStore((s) => s.workspace)
  const artifactList = useStore((s) => s.artifacts)
  const artifacts = useMemo(() => new Set(artifactList.map((a) => a.name)), [artifactList])
  const [editing, setEditing] = useState(false)
  const [hunk, setHunk] = useState(0)
  const scroller = useRef<HTMLDivElement>(null)
  const dirty = !!doc?.edit && doc.edit.text !== doc.edit.baseText

  // :e starts an edit from the command line; :w and :e! end it.
  const hasEdit = !!doc?.edit
  useEffect(() => setEditing(hasEdit), [hasEdit])

  const edit = () => {
    store.startEdit(name)
    setEditing(true)
  }

  const handle = (a: WinAction, count: number): boolean => {
    if (a === 'nextHunk' || a === 'prevHunk' || a === 'takeHunk') {
      if (!doc?.conflict || !doc.edit) {
        store.say('not in a diff', 'muted')
        return true
      }
      const n = hunks(lines(doc.edit.text.split('\n'), doc.text.split('\n'))).length
      if (a === 'takeHunk') {
        store.takeHunk(name, hunk)
        setHunk(Math.max(0, Math.min(hunk, n - 2)))
      } else setHunk(Math.max(0, Math.min(n - 1, hunk + (a === 'nextHunk' ? count : -count))))
      return true
    }
    const el = scroller.current
    if (!el) return false
    const line = 24
    const headings = () => Array.from(el.querySelectorAll<HTMLElement>('h1, h2, h3, h4, h5, h6'))
    switch (a) {
      case 'down':
        el.scrollBy({ top: line * count })
        return true
      case 'up':
        el.scrollBy({ top: -line * count })
        return true
      case 'top':
        el.scrollTo({ top: 0 })
        return true
      case 'bottom':
        el.scrollTo({ top: el.scrollHeight })
        return true
      case 'halfDown':
        el.scrollBy({ top: el.clientHeight / 2 })
        return true
      case 'halfUp':
        el.scrollBy({ top: -el.clientHeight / 2 })
        return true
      case 'pageDown':
        el.scrollBy({ top: el.clientHeight - 40 })
        return true
      case 'pageUp':
        el.scrollBy({ top: -(el.clientHeight - 40) })
        return true
      case 'nextMsg':
      case 'prevMsg': {
        const top = el.getBoundingClientRect().top
        const hs = headings()
        const h =
          a === 'nextMsg'
            ? hs.find((x) => x.getBoundingClientRect().top > top + 8)
            : [...hs].reverse().find((x) => x.getBoundingClientRect().top < top - 8)
        h?.scrollIntoView({ block: 'start' })
        return true
      }
      case 'insert':
      case 'append':
      case 'enter':
        edit()
        return true
      case 'yank': {
        const text = window.getSelection()?.toString() || doc?.edit?.text || doc?.text || ''
        window.nh.copy(text)
        store.say('yanked the document', 'muted')
        return true
      }
      case 'link': {
        const a = el.querySelector<HTMLAnchorElement>('a[href]:hover') ?? el.querySelector<HTMLAnchorElement>('a[href]')
        if (a) void window.nh.openExternal(a.href)
        return true
      }
    }
    return false
  }
  const ref = useRef(handle)
  ref.current = handle
  useEffect(() => {
    handlers.set(win.id, (a, n) => ref.current(a, n))
    return () => void handlers.delete(win.id)
  }, [win.id])

  useEffect(() => {
    const on = (e: Event) => {
      const d = (e as CustomEvent<{ win: number; index: number }>).detail
      if (d.win !== win.id) return
      setEditing(false)
      requestAnimationFrame(() => scroller.current?.querySelectorAll('h1, h2, h3, h4, h5, h6')[d.index]?.scrollIntoView({ block: 'start' }))
    }
    document.addEventListener('nh:heading', on)
    return () => document.removeEventListener('nh:heading', on)
  }, [win.id])

  const a = doc?.artifact
  return (
    <div className="window artifact" onMouseDown={() => store.focus(win.id)}>
      <div className={`statusline ${focused ? 'focused' : ''}`}>
        <span className="sl-ws">{workspace} · </span>
        <span className="sl-name">
          {name}
          {dirty ? ' [+]' : ''}
        </span>
        {a && (
          <span className="sl-dim">
            v{a.version} · {a.userEdit ? 'edited by you' : a.thread}
          </span>
        )}
        <span className="sl-fill" />
        {editing ? (
          <>
            <button className="btn small" onClick={() => void store.save(name)}>
              Save <kbd>⌘S</kbd>
            </button>
            <button className="btn small ghost" onClick={() => setEditing(false)}>
              Done
            </button>
          </>
        ) : (
          <button className="btn small ghost" onClick={edit}>
            Edit <kbd>i</kbd>
          </button>
        )}
      </div>
      {doc?.conflict && doc.edit && <Conflict name={name} doc={doc} cursor={hunk} setCursor={setHunk} />}
      {!doc ? (
        <div className="empty">loading…</div>
      ) : editing && doc.edit ? (
        <SourceEditor name={name} text={doc.edit.text} win={win.id} onLeave={() => setEditing(false)} onHunk={setHunk} />
      ) : (
        <div className="doc" ref={scroller} tabIndex={-1} onDoubleClick={edit}>
          <article className="doc-inner">
            {a && <div className="doc-title">{a.title}</div>}
            <Markdown text={doc.edit?.text ?? doc.text} artifacts={artifacts} />
          </article>
        </div>
      )}
    </div>
  )
}
