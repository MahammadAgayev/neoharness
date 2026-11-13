import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { cssLanguage } from '@codemirror/lang-css'
import { htmlLanguage } from '@codemirror/lang-html'
import { javascriptLanguage, jsxLanguage, tsxLanguage, typescriptLanguage } from '@codemirror/lang-javascript'
import { markdown, markdownLanguage } from '@codemirror/lang-markdown'
import { HighlightStyle, StreamLanguage, syntaxHighlighting, syntaxTree, type Language } from '@codemirror/language'
import { dockerFile } from '@codemirror/legacy-modes/mode/dockerfile'
import { go } from '@codemirror/legacy-modes/mode/go'
import { python } from '@codemirror/legacy-modes/mode/python'
import { rust } from '@codemirror/legacy-modes/mode/rust'
import { shell } from '@codemirror/legacy-modes/mode/shell'
import { standardSQL } from '@codemirror/legacy-modes/mode/sql'
import { toml } from '@codemirror/legacy-modes/mode/toml'
import { yaml } from '@codemirror/legacy-modes/mode/yaml'
import { Compartment, EditorState, Transaction } from '@codemirror/state'
import { EditorView, keymap } from '@codemirror/view'
import { tags as t } from '@lezer/highlight'
import { getCM, vim, Vim } from '@replit/codemirror-vim'
import { useEffect, useMemo, useRef, useState } from 'react'

import { hunks, lines, type Hunk } from '../lib/linediff'
import { livePreview } from '../lib/livepreview'
import { outline } from '../lib/markdown'
import { store, useStore, type Doc, type Win } from '../store'
import { editorKeys, editors, handlers, type WinAction } from './registry'
import { CloseWindow } from './CloseWindow'

const docTheme = EditorView.theme({
  '&': { background: 'transparent', color: 'var(--text)', height: '100%' },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': { fontFamily: 'var(--font-text)', lineHeight: '1.7' },
  // The column of a document, as a page.
  '.cm-content': { caretColor: 'var(--iris)', padding: '32px max(32px, calc((100% - 760px) / 2)) 40vh' },
  '.cm-cursor': { borderLeftColor: 'var(--iris)', borderLeftWidth: '2px' },
  '.cm-fat-cursor': { background: 'var(--iris) !important', color: 'var(--base) !important' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': { background: 'var(--hl-med) !important' },
  '&:not(.cm-focused) .cm-cursorLayer': { display: 'none' },
  '.cm-gutters': { display: 'none' },
})

const codeColors = HighlightStyle.define([
  { tag: [t.keyword, t.controlKeyword, t.moduleKeyword, t.operatorKeyword], color: 'var(--pine)' },
  { tag: [t.string, t.special(t.string), t.regexp], color: 'var(--gold)' },
  { tag: [t.comment, t.lineComment, t.blockComment], color: 'var(--muted)', fontStyle: 'italic' },
  { tag: [t.number, t.bool, t.null, t.atom], color: 'var(--rose)' },
  { tag: [t.function(t.variableName), t.function(t.propertyName)], color: 'var(--foam)' },
  { tag: [t.typeName, t.className, t.tagName], color: 'var(--foam)' },
  { tag: [t.propertyName, t.attributeName], color: 'var(--iris)' },
])

// Code blocks in these languages are highlighted; others show plain.
const codeLanguages: Record<string, Language> = {
  js: javascriptLanguage,
  javascript: javascriptLanguage,
  jsx: jsxLanguage,
  ts: typescriptLanguage,
  typescript: typescriptLanguage,
  tsx: tsxLanguage,
  html: htmlLanguage,
  css: cssLanguage,
  json: javascriptLanguage,
  go: StreamLanguage.define(go),
  py: StreamLanguage.define(python),
  python: StreamLanguage.define(python),
  rs: StreamLanguage.define(rust),
  rust: StreamLanguage.define(rust),
  sh: StreamLanguage.define(shell),
  bash: StreamLanguage.define(shell),
  shell: StreamLanguage.define(shell),
  sql: StreamLanguage.define(standardSQL),
  yaml: StreamLanguage.define(yaml),
  yml: StreamLanguage.define(yaml),
  toml: StreamLanguage.define(toml),
  dockerfile: StreamLanguage.define(dockerFile),
}

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

// DocEditor is the artifact as a document: rendered where you read, the
// markdown where the cursor is, as the TUI shows it. A change starts an edit;
// :w or ⌘S saves it.
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
  const vimOn = useStore((s) => s.settings.vim)
  const comp = useRef(new Compartment())
  const viewRef = useRef<EditorView | null>(null)
  const onHunk = useRef(props.onHunk)
  onHunk.current = props.onHunk
  const arts = useRef(props.artifacts)
  arts.current = props.artifacts

  useEffect(() => {
    const view = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: props.text,
        extensions: [
          comp.current.of(store.state.settings.vim ? vim({ status: false }) : []),
          history(),
          keymap.of([indentWithTab, ...historyKeymap, ...defaultKeymap]),
          markdown({ base: markdownLanguage, codeLanguages: (info) => codeLanguages[info.toLowerCase()] ?? null }),
          syntaxHighlighting(codeColors),
          livePreview(() => arts.current),
          EditorView.lineWrapping,
          docTheme,
          EditorView.updateListener.of((u) => {
            // A newer version or a merge comes in as remote: it is no edit.
            if (u.docChanged && !u.transactions.some((tr) => tr.annotation(Transaction.remote)))
              store.editText(props.name, u.state.doc.toString())
            if (u.focusChanged) store.setInsert(u.view.hasFocus && (!store.state.settings.vim || !!getCM(u.view)?.state.vim?.insertMode))
          }),
          EditorView.domEventHandlers({
            focus: () => store.focus(props.win),
            // ⌘-click opens an artifact named in code, or a link.
            mousedown: (e, v) => {
              if (!e.metaKey) return false
              const art = (e.target as HTMLElement).closest<HTMLElement>('[data-artifact]')?.dataset.artifact
              if (art) {
                void store.openArtifact(art)
                return true
              }
              const pos = v.posAtCoords({ x: e.clientX, y: e.clientY })
              for (let n = pos === null ? null : syntaxTree(v.state).resolveInner(pos, 1); n; n = n.parent) {
                if (n.name !== 'Link') continue
                const url = n.getChild('URL')
                if (url) void window.nh.openExternal(v.state.sliceDoc(url.from, url.to))
                return true
              }
              return false
            },
          }),
        ],
      }),
    })
    viewRef.current = view
    props.onView(view)
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
          const i = hunkAt(hs, line())
          if (i < 0) store.say('no change here', 'muted')
          else {
            store.takeHunk(props.name, i)
            requestAnimationFrame(() => toLine(hs[i].mFrom))
          }
          return true
        }
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
      return false
    })
    // gO picks a heading: the cursor goes to its line.
    const onHeading = (e: Event) => {
      const d = (e as CustomEvent<{ win: number; index: number }>).detail
      if (d.win !== props.win) return
      const h = outline(view.state.doc.toString())[d.index]
      if (h) {
        toLine(h.line)
        view.focus()
      }
    }
    document.addEventListener('nh:heading', onHeading)
    // A new doc opens ready to write: the cursor at its end, in Insert.
    if (store.writeDoc === props.name) {
      store.writeDoc = ''
      view.focus()
      view.dispatch({ selection: { anchor: view.state.doc.length }, scrollIntoView: true })
      if (cm && store.state.settings.vim) Vim.handleKey(cm as never, 'i', 'mapping')
    }
    return () => {
      document.removeEventListener('nh:heading', onHeading)
      cm?.off('vim-mode-change', onMode)
      editors.delete(view)
      props.onView(null)
      view.destroy()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.name])

  // A newer version, a merge or :e! changes the text under the editor.
  useEffect(() => {
    const v = viewRef.current
    if (v && v.state.doc.toString() !== props.text)
      v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: props.text }, annotations: Transaction.remote.of(true) })
  }, [props.text])

  useEffect(() => {
    viewRef.current?.dispatch({ effects: comp.current.reconfigure(vimOn ? vim({ status: false }) : []) })
  }, [vimOn])

  // The window in focus has the cursor in its document; a window that
  // loses focus gives the keys back, or they would still type here.
  // It takes them back too when the command line or a dialog over it closes.
  const over = useStore((s) => s.cmdline !== null || !!s.picker || s.settingsOpen || s.helpOpen || !!s.choice || s.newDoc)
  useEffect(() => {
    const v = viewRef.current
    if (props.focused && !over && !v?.hasFocus) v?.focus()
    else if (!props.focused && v?.hasFocus) v.contentDOM.blur()
  }, [props.focused, over])

  return <div className="doc-editor" ref={host} />
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
