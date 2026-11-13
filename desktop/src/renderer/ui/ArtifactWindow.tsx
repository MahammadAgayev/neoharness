import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { markdown } from '@codemirror/lang-markdown'
import { Compartment, EditorState } from '@codemirror/state'
import { EditorView, keymap } from '@codemirror/view'
import { getCM, vim, Vim } from '@replit/codemirror-vim'
import { useEffect, useMemo, useRef, useState } from 'react'

import { lines } from '../lib/linediff'
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

function SourceEditor(props: { name: string; text: string; onLeave: () => void; win: number }) {
  const host = useRef<HTMLDivElement>(null)
  const vimOn = useStore((s) => s.settings.vim)
  const comp = useRef(new Compartment())
  const viewRef = useRef<EditorView | null>(null)
  const leave = useRef(props.onLeave)
  leave.current = props.onLeave

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
    // <Esc> in Normal mode shows the document rendered again, as the TUI does.
    editorKeys.set(view, (key) => {
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

// Conflict is what a refused :w shows: the newer version against your edit,
// and the two ways out.
function Conflict(props: { name: string; doc: Doc }) {
  const [open, setOpen] = useState(true)
  const { doc } = props
  const ops = useMemo(() => lines(doc.text.split('\n'), (doc.edit?.text ?? '').split('\n')), [doc.text, doc.edit?.text])
  return (
    <div className="conflict">
      <div className="conflict-head">
        <span className="t-love">●</span>
        <span>
          {doc.artifact.thread} changed the same lines in v{doc.artifact.version}
        </span>
        <span className="sl-fill" />
        <button className="btn small ghost" onClick={() => setOpen(!open)}>
          {open ? 'Hide diff' : 'Show diff'}
        </button>
        <button className="btn small" onClick={() => void store.revert(props.name)}>
          Take theirs <kbd>:e!</kbd>
        </button>
        <button className="btn small primary" onClick={() => void store.save(props.name, true)}>
          Keep mine <kbd>:w!</kbd>
        </button>
      </div>
      {open && (
        <pre className="diff">
          <div className="diff-legend t-muted">
            <span className="t-love">− v{doc.artifact.version}</span> <span className="t-pine">+ yours</span>
          </div>
          {ops.map((o, i) => (
            <div key={i} className={`diff-line d-${o.kind === '-' ? 'del' : o.kind === '+' ? 'add' : 'same'}`}>
              <span className="diff-mark">{o.kind}</span>
              {o.text || ' '}
            </div>
          ))}
        </pre>
      )}
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
      {doc?.conflict && doc.edit && <Conflict name={name} doc={doc} />}
      {!doc ? (
        <div className="empty">loading…</div>
      ) : editing && doc.edit ? (
        <SourceEditor name={name} text={doc.edit.text} win={win.id} onLeave={() => setEditing(false)} />
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
