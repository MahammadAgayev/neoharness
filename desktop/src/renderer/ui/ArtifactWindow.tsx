import type { Node } from 'prosemirror-model'
import { Selection, EditorState, TextSelection } from 'prosemirror-state'
import { EditorView } from 'prosemirror-view'
import { useEffect, useMemo, useRef, useState } from 'react'

import { artifactsKey, editorPlugins, linkAt, TaskItem } from '../lib/docedit'
import { DocVim } from '../lib/docvim'
import { hunks, lines, type Hunk } from '../lib/linediff'
import { MarkdownDoc } from '../lib/mdoc'
import { store, useStore, type Doc, type Win } from '../store'
import { docs, handlers, yank, type DocKeys, type WinAction } from './registry'
import { CloseWindow } from './CloseWindow'
import 'prosemirror-view/style/prosemirror.css'

function conflictHunks(name: string): Hunk[] | null {
  const d = store.state.docs[name]
  if (!d?.conflict || !d.edit) return null
  return hunks(lines(d.edit.text.split('\n'), d.text.split('\n')))
}

// blockStarts is the markdown line where each top-level block starts, so
// ]c and do find the change in the block you are on.
function blockStarts(md: MarkdownDoc, doc: Node): number[] {
  const out: number[] = []
  doc.forEach((_n, off, i) => {
    const before = i === 0 ? '' : md.markdown(doc.copy(doc.content.cut(0, off))).replace(/\n+$/, '')
    out.push(before ? before.split('\n').length + 1 : 0)
  })
  return out
}

// DocEditor is the artifact as a document you read and write in place:
// headings, lists, tables and code stay rendered while you type, as in a
// word processor, and what is saved is markdown. Vim's Normal mode moves
// in it; i, a or o start Insert and <Esc> leaves it. :w or ⌘S saves.
function DocEditor(props: {
  name: string
  text: string
  win: number
  focused: boolean
  artifacts: Set<string>
  onHunk: (i: number) => void
  onView: (v: EditorView | null) => void
}) {
  const host = useRef<HTMLDivElement>(null)
  const scroller = useRef<HTMLDivElement>(null)
  const vimOn = useStore((s) => s.settings.vim)
  const viewRef = useRef<EditorView | null>(null)
  const vimRef = useRef<DocVim | null>(null)
  const md = useRef<MarkdownDoc | null>(null)
  // last is the markdown the editor holds, so its own edit coming back as
  // props is no new text.
  const last = useRef(props.text)
  const onHunk = useRef(props.onHunk)
  onHunk.current = props.onHunk
  const arts = useRef(props.artifacts)
  arts.current = props.artifacts
  const [find, setFind] = useState<string | null>(null)

  useEffect(() => {
    md.current = new MarkdownDoc(props.text)
    last.current = props.text
    let vimMode: DocVim | null = null
    const leave = () => {
      if (store.state.settings.vim) vimMode?.setMode('normal')
    }
    const view = new EditorView(host.current!, {
      state: EditorState.create({ doc: md.current.doc, plugins: editorPlugins(leave) }),
      nodeViews: { list_item: (node, v, getPos) => new TaskItem(node, v, getPos) },
      attributes: { class: 'md doc-page', spellcheck: 'true' },
      dispatchTransaction(tr) {
        const v = viewRef.current!
        vimMode?.group(tr)
        v.updateState(v.state.apply(tr))
        if (tr.selectionSet && !tr.docChanged) vimMode?.mouse(!!tr.getMeta('pointer'))
        if (tr.docChanged && !tr.getMeta('remote')) {
          const text = md.current!.markdown(v.state.doc)
          last.current = text
          store.editText(props.name, text)
        }
      },
      handleDOMEvents: {
        focus: () => {
          store.focus(props.win)
          store.setInsert(!store.state.settings.vim || vimMode?.mode === 'insert')
          return false
        },
        blur: () => {
          store.setInsert(false)
          return false
        },
        // A click opens an artifact named in the page, or a link, as in a
        // page you read; in Insert, where a click places the cursor, ⌘-click.
        mousedown: (_v, e) => {
          if (!e.metaKey && (!store.state.settings.vim || vimMode?.mode === 'insert')) return false
          const el = e.target as HTMLElement
          const art = el.closest<HTMLElement>('[data-artifact]')?.dataset.artifact
          if (art) {
            e.preventDefault()
            void store.openArtifact(art)
            return true
          }
          const href = el.closest('a')?.getAttribute('href')
          if (href) {
            e.preventDefault()
            void window.nh.openExternal(href)
            return true
          }
          return false
        },
      },
      // A link pasted over selected words makes them a link.
      handlePaste(v, e) {
        const url = e.clipboardData?.getData('text/plain').trim() ?? ''
        if (v.state.selection.empty || !/^https?:\/\/\S+$/.test(url)) return false
        const { from, to } = v.state.selection
        v.dispatch(v.state.tr.addMark(from, to, v.state.schema.marks.link.create({ href: url })))
        return true
      },
    })
    viewRef.current = view
    props.onView(view)
    const vim = new DocVim(view, () => scroller.current, {
      mode: (m) => store.setInsert(view.hasFocus() && m === 'insert'),
      find: () => setFind(''),
      yank: (t) => (yank.text = t),
      say: (t) => store.say(t, 'love'),
      open: (pos) => {
        const l = linkAt(view.state.doc, pos, arts.current)
        if (l?.artifact) void store.openArtifact(l.artifact)
        else if (l?.url) void window.nh.openExternal(l.url)
        return !!l
      },
      vim: () => store.state.settings.vim,
    })
    vimMode = vim
    vimRef.current = vim
    if (!store.state.settings.vim) vim.setMode('insert')
    else view.dom.classList.add('vim-normal')
    view.dispatch(view.state.tr.setMeta(artifactsKey, props.artifacts).setMeta('remote', true))

    // block is the markdown lines of the block under the cursor.
    const block = () => {
      const starts = blockStarts(md.current!, view.state.doc)
      const i = view.state.selection.$head.index(0)
      return { starts, from: starts[i], to: starts[i + 1] ?? Infinity }
    }
    const toLine = (n: number) => {
      const { starts } = block()
      let i = 0
      while (i + 1 < starts.length && starts[i + 1] <= n) i++
      let at = 0
      view.state.doc.forEach((_n, off, k) => {
        if (k === i) at = off
      })
      view.dispatch(view.state.tr.setSelection(Selection.near(view.state.doc.resolve(at + 1))).scrollIntoView())
    }
    // In a conflict ]c [c move between changes and do dp take one, as in
    // Neovim's diff mode; d goes back to Vim when o or p does not follow.
    let held = ''
    const keys: DocKeys = {
      hasFocus: () => view.hasFocus(),
      insert: () => vim.mode === 'insert',
      busy: () => held !== '' || vim.busy(),
      leaveInsert: () => vim.setMode('normal'),
      deleteWord: () => vim.deleteWordBack(),
      key: (key) => {
        const hs = conflictHunks(props.name)
        if (held) {
          const first = held
          held = ''
          if (hs && (first === ']' || first === '[') && key === 'c') {
            const b = block()
            const h = first === ']' ? hs.find((x) => x.mFrom >= b.to) : [...hs].reverse().find((x) => x.mFrom < b.from)
            if (h) {
              toLine(h.mFrom)
              onHunk.current(hs.indexOf(h))
            }
            return true
          }
          if (hs && first === 'd' && (key === 'o' || key === 'p')) {
            const b = block()
            const i = hs.findIndex((h) => h.mFrom <= b.to && Math.max(h.mTo, h.mFrom + 1) > b.from)
            if (i < 0) store.say('no change here', 'muted')
            else store.takeHunk(props.name, i)
            return true
          }
          vim.key(first)
          return vim.key(key)
        }
        if (hs && !vim.busy() && (key === ']' || key === '[' || key === 'd')) {
          held = key
          return true
        }
        return vim.key(key)
      },
    }
    docs.add(keys)

    // gO picks a heading: the cursor goes to it.
    const onHeading = (e: Event) => {
      const d = (e as CustomEvent<{ win: number; index: number }>).detail
      if (d.win !== props.win) return
      let k = 0
      let at = -1
      view.state.doc.descendants((n, pos) => {
        if (at >= 0) return false
        if (n.type.name === 'heading' && k++ === d.index) at = pos + 1
        return !n.isTextblock
      })
      if (at >= 0) {
        view.dispatch(view.state.tr.setSelection(TextSelection.create(view.state.doc, at)).scrollIntoView())
        view.focus()
      }
    }
    document.addEventListener('nh:heading', onHeading)
    // A new doc opens ready to write: the cursor at its end, in Insert.
    if (store.writeDoc === props.name) {
      store.writeDoc = ''
      view.focus()
      // Under the title, on a line of its own; an empty line is no change.
      const tr = view.state.tr
      if (view.state.doc.lastChild?.type.name === 'heading') tr.insert(tr.doc.content.size, view.state.schema.nodes.paragraph.create())
      view.dispatch(tr.setSelection(Selection.atEnd(tr.doc)).scrollIntoView())
      vim.setMode('insert')
    }
    return () => {
      document.removeEventListener('nh:heading', onHeading)
      docs.delete(keys)
      props.onView(null)
      view.destroy()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.name])

  // A newer version, a merge, a change taken in a conflict or :e! changes
  // the text under the editor. Only the blocks that differ are replaced,
  // so the cursor stays where it was.
  useEffect(() => {
    const v = viewRef.current
    if (!v || props.text === last.current) return
    last.current = props.text
    md.current = new MarkdownDoc(props.text)
    const next = md.current.doc
    const start = v.state.doc.content.findDiffStart(next.content)
    if (start === null) return
    const end = v.state.doc.content.findDiffEnd(next.content)!
    let { a, b } = end
    const over = start - Math.min(a, b)
    if (over > 0) {
      a += over
      b += over
    }
    const tr = v.state.tr.replace(start, a, next.slice(start, b)).setMeta('remote', true).setMeta('addToHistory', false)
    v.dispatch(tr)
  }, [props.text])

  useEffect(() => {
    viewRef.current?.dispatch(viewRef.current.state.tr.setMeta(artifactsKey, props.artifacts).setMeta('remote', true))
  }, [props.artifacts])

  const wasVim = useRef(vimOn)
  useEffect(() => {
    if (wasVim.current !== vimOn) vimRef.current?.setMode(vimOn ? 'normal' : 'insert')
    wasVim.current = vimOn
  }, [vimOn])

  // The window in focus has the cursor in its document; a window that
  // loses focus gives the keys back, or they would still type here.
  // It takes them back too when the command line or a dialog over it closes.
  const over = useStore((s) => s.cmdline !== null || !!s.picker || s.settingsOpen || s.helpOpen || !!s.cost || !!s.choice || s.newDoc)
  useEffect(() => {
    const v = viewRef.current
    if (find !== null) return
    if (props.focused && !over && !v?.hasFocus()) v?.focus()
    else if (!props.focused && v?.hasFocus()) (v.dom as HTMLElement).blur()
  }, [props.focused, over, find])

  return (
    <>
      {find !== null && (
        <div className="findbar">
          <span className="t-iris">/</span>
          <input
            autoFocus
            value={find}
            placeholder="search this document"
            onChange={(e) => setFind(e.target.value)}
            onKeyDown={(e) => {
              e.stopPropagation()
              // The page takes the focus back at once: Enter must not split
              // its paragraph there.
              if (e.key === 'Enter' || e.key === 'Escape') e.preventDefault()
              if (e.key === 'Enter') {
                vimRef.current?.find(find, 1)
                setFind(null)
              } else if (e.key === 'Escape') setFind(null)
            }}
          />
        </div>
      )}
      <div className="doc-editor" ref={scroller}>
        <div className="doc-host" ref={host} />
      </div>
    </>
  )
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
  const [hunk, setHunk] = useState(0)
  const view = useRef<EditorView | null>(null)
  const dirty = store.dirty(name)

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
    // Keys that reach the window when its document has no focus give it back.
    view.current?.focus()
    return false
  }
  const ref = useRef(handle)
  ref.current = handle
  useEffect(() => {
    handlers.set(win.id, (a, n) => ref.current(a, n))
    return () => void handlers.delete(win.id)
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
        {dirty && (
          <button className="btn small primary" onClick={() => void store.save(name)}>
            Save <kbd>⌘S</kbd>
          </button>
        )}
        <CloseWindow id={win.id} />
      </div>
      {doc?.conflict && doc.edit && <Conflict name={name} doc={doc} cursor={hunk} setCursor={setHunk} />}
      {!doc ? (
        <div className="empty">loading…</div>
      ) : (
        <DocEditor
          name={name}
          text={doc.edit?.text ?? doc.text}
          win={win.id}
          focused={focused}
          artifacts={artifacts}
          onHunk={setHunk}
          onView={(v) => (view.current = v)}
        />
      )}
    </div>
  )
}
