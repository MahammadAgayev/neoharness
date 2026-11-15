import { Selection, EditorState, TextSelection } from 'prosemirror-state'
import { EditorView } from 'prosemirror-view'
import { useEffect, useMemo, useRef, useState } from 'react'

import {
  artifactsKey,
  conflictGroups,
  editorPlugins,
  fatCursorKey,
  freshKey,
  linkArtifact,
  linkAt,
  marksKey,
  openSlash,
  slashItems,
  slashKey,
  slashRun,
  table,
  takeKey,
  takeTr,
  TaskItem,
  type MarksMeta,
  type Slash,
} from '../lib/docedit'
import { DocVim } from '../lib/docvim'
import { MarkdownDoc } from '../lib/mdoc'
import { store, useStore, type Win } from '../store'
import { docs, handlers, yank, type DocKeys, type WinAction } from './registry'
import { CloseWindow } from './CloseWindow'
import { Icon } from './Icon'
import 'prosemirror-view/style/prosemirror.css'

const noConflicts: MarksMeta['conflicts'] = []

// conflictMove is ]c and [c: the cursor goes to the next or the previous
// block both sides changed.
function conflictMove(v: EditorView, dir: 1 | -1): void {
  const gs = conflictGroups(v.state)
  const h = v.state.selection.head
  const g = dir > 0 ? gs.find((x) => x.from > h) : [...gs].reverse().find((x) => x.to < h)
  if (!g) return store.say(dir > 0 ? 'no change below' : 'no change above', 'muted')
  v.dispatch(v.state.tr.setSelection(Selection.near(v.state.doc.resolve(Math.min(g.from + 1, v.state.doc.content.size)))).scrollIntoView())
}

// conflictTake is do: theirs replaces the block under the cursor.
function conflictTake(v: EditorView): void {
  const h = v.state.selection.head
  const g = conflictGroups(v.state).find((x) => h >= x.from && h <= x.to)
  if (!g) return store.say('no change here', 'muted')
  v.dispatch(takeTr(v.state, g.i))
}

// SlashMenu is the block menu, as Vim's Insert completion menu: under the
// cursor line, or above it where there is no room below. The caret stays
// in the text and the keys stay in the page; a click takes a row too.
function SlashMenu(props: { view: EditorView; slash: Slash; scroller: HTMLElement; newPage: (name: string) => void }) {
  const { view, slash, scroller, newPage } = props
  const list = useRef<HTMLDivElement>(null)
  const items = slashItems(view.state)
  useEffect(() => {
    list.current?.querySelector('.active')?.scrollIntoView({ block: 'nearest' })
  }, [slash.index])
  const at = view.coordsAtPos(slash.from - 1)
  const box = scroller.getBoundingClientRect()
  const row = parseFloat(getComputedStyle(scroller).getPropertyValue('--row')) || 28
  const height = Math.min(items.length, 8) * row + 10
  const below = at.bottom + 4 + height <= box.bottom || at.top - box.top < height + 4
  const top = (below ? at.bottom + 4 : at.top - 4 - height) - box.top + scroller.scrollTop
  return (
    <div className="doc-slash" ref={list} style={{ top, left: at.left - box.left }} onMouseDown={(e) => e.preventDefault()}>
      {items.map((it, i) => (
        <div
          key={it.key}
          className={`doc-slash-row ${i === slash.index ? 'active' : ''}`}
          onMouseMove={() => i !== slash.index && view.dispatch(view.state.tr.setMeta(slashKey, { ...slash, index: i }))}
          onClick={() => slashRun(view, it, newPage)}
        >
          <Icon name={it.icon} size={14} />
          <span className="doc-slash-label">{it.label}</span>
        </div>
      ))}
    </div>
  )
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
  marks: MarksMeta | null
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
  const arts = useRef(props.artifacts)
  arts.current = props.artifacts
  const [find, setFind] = useState<string | null>(null)
  const [slash, setSlash] = useState<Slash | null>(null)
  const newPage = useRef<(name: string) => void>(() => {})

  useEffect(() => {
    md.current = new MarkdownDoc(props.text)
    last.current = props.text
    let vimMode: DocVim | null = null
    // A new page is made, then linked at the cursor; one that is there
    // already is only linked.
    newPage.current = async (name: string) => {
      if (!arts.current.has(name) && !(await store.makePage(name))) return
      const v = viewRef.current
      if (v) v.dispatch(linkArtifact(v.state, name))
    }
    const leave = () => {
      if (store.state.settings.vim) vimMode?.setMode('normal')
    }
    const view = new EditorView(host.current!, {
      state: EditorState.create({ doc: md.current.doc, plugins: editorPlugins(leave, (name) => newPage.current(name)) }),
      nodeViews: { list_item: (node, v, getPos) => new TaskItem(node, v, getPos) },
      // Spellcheck marks words only while you write, as names and ids in
      // a page you read are no typos.
      attributes: (s) => ({ class: 'md doc-page', spellcheck: fatCursorKey.getState(s)?.on ? 'false' : 'true' }),
      dispatchTransaction(tr) {
        const v = viewRef.current!
        vimMode?.group(tr)
        v.updateState(v.state.apply(tr))
        setSlash(slashKey.getState(v.state) ?? null)
        if (tr.selectionSet && !tr.docChanged) vimMode?.mouse(!!tr.getMeta('pointer'))
        if (tr.docChanged && !tr.getMeta('remote')) {
          // Taking theirs, or writing in the block yourself, decides a conflict.
          const open = store.state.docs[props.name]?.conflicts
          if (open?.length) {
            const left = conflictGroups(v.state).map((g) => g.i).filter((i) => i !== tr.getMeta(takeKey))
            if (left.length < open.length) store.keepConflicts(props.name, left)
          }
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
          // A click leaves Visual mode, as with Vim and a mouse; the click
          // then puts the cursor where it lands.
          if (vimMode?.mode === 'visual' || vimMode?.mode === 'vline') vimMode.setMode('normal')
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
          // The cursor goes where you click at once, not when the browser
          // tells of its selection a few ms later: a key typed at once acts
          // there, and nothing redrawn in between puts the old cursor back.
          const at = e.button === 0 && !e.shiftKey && e.detail === 1 ? _v.posAtCoords({ left: e.clientX, top: e.clientY }) : null
          if (at) _v.dispatch(_v.state.tr.setSelection(Selection.near(_v.state.doc.resolve(at.pos))).setMeta('pointer', true))
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
        const diff = conflictGroups(view.state).length > 0
        if (held) {
          const first = held
          held = ''
          if (diff && (first === ']' || first === '[') && key === 'c') {
            conflictMove(view, first === ']' ? 1 : -1)
            return true
          }
          if (diff && first === 'd' && (key === 'o' || key === 'p')) {
            conflictTake(view)
            return true
          }
          vim.key(first)
          return vim.key(key)
        }
        if (diff && !vim.busy() && (key === ']' || key === '[' || key === 'd')) {
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
    const onTable = (e: Event) => {
      const d = (e as CustomEvent<{ win: number; args: string[] }>).detail
      if (d.win !== props.win) return
      const err = table(view, d.args)
      if (err) store.say(err, 'love')
    }
    document.addEventListener('nh:table', onTable)
    const onPage = (e: Event) => {
      const d = (e as CustomEvent<{ win: number; name: string }>).detail
      if (d.win === props.win) newPage.current(d.name)
    }
    document.addEventListener('nh:page', onPage)
    const onSlash = (e: Event) => {
      if ((e as CustomEvent<{ win: number }>).detail.win !== props.win) return
      vim.setMode('insert')
      view.dispatch(openSlash(view.state))
    }
    document.addEventListener('nh:slash', onSlash)
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
      document.removeEventListener('nh:table', onTable)
      document.removeEventListener('nh:page', onPage)
      document.removeEventListener('nh:slash', onSlash)
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
    v.dispatch(tr.setMeta(freshKey, { from: start, to: b }))
    // The tint fades in CSS over --t-settle; then it goes.
    const settle = parseFloat(getComputedStyle(v.dom).getPropertyValue('--t-settle')) || 1000
    const t = setTimeout(() => viewRef.current?.dispatch(viewRef.current.state.tr.setMeta(freshKey, null).setMeta('remote', true)), settle)
    return () => clearTimeout(t)
  }, [props.text])

  const m = props.marks
  useEffect(() => {
    viewRef.current?.dispatch(viewRef.current.state.tr.setMeta(marksKey, m && { ...m }).setMeta('remote', true))
  }, [m?.base, m?.start, m?.theirs, m?.conflicts, m?.label, m === null])

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
        {slash && viewRef.current && scroller.current && <SlashMenu view={viewRef.current} slash={slash} scroller={scroller.current} newPage={newPage.current} />}
      </div>
    </>
  )
}

export function ArtifactWindow(props: { win: Win; focused: boolean }) {
  const { win, focused } = props
  const name = win.buf.kind === 'artifact' ? win.buf.name : ''
  const doc = useStore((s) => s.docs[name])
  const workspace = useStore((s) => s.workspace)
  const artifactList = useStore((s) => s.artifacts)
  const artifacts = useMemo(() => new Set(artifactList.map((a) => a.name)), [artifactList])
  const view = useRef<EditorView | null>(null)
  const dirty = store.dirty(name)

  const handle = (a: WinAction, count: number): boolean => {
    if (a === 'nextHunk' || a === 'prevHunk' || a === 'takeHunk') {
      const v = view.current
      if (!v || !conflictGroups(v.state).length) {
        store.say('not in a diff', 'muted')
        return true
      }
      v.focus()
      if (a === 'takeHunk') conflictTake(v)
      else for (let k = 0; k < count; k++) conflictMove(v, a === 'nextHunk' ? 1 : -1)
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
          <button className="btn small ghost" title=":w  ⌘S" onClick={() => void store.save(name)}>
            Save
          </button>
        )}
        <CloseWindow id={win.id} />
      </div>
      {a && !!doc?.conflicts?.length && (
        <div className="doc-conflict-bar">
          <span>
            {a.thread} changed the same blocks in v{a.version} · {doc.conflicts.length === 1 ? '1 block' : `${doc.conflicts.length} blocks`} to decide
          </span>
          <span className="sl-fill" />
          <button className="btn small ghost" title=":e!" onClick={() => void store.revert(name)}>
            Take all of v{a.version}
          </button>
          <button className="btn small" title=":w" onClick={() => void store.save(name)}>
            Keep mine
          </button>
        </div>
      )}
      {!doc ? (
        <div className="empty">loading…</div>
      ) : (
        <DocEditor
          name={name}
          text={doc.edit?.text ?? doc.text}
          win={win.id}
          focused={focused}
          artifacts={artifacts}
          marks={
            doc.edit && {
              base: doc.edit.baseText,
              start: doc.edit.start,
              theirs: doc.text,
              conflicts: doc.conflicts ?? noConflicts,
              label: `v${doc.artifact.version} · ${doc.artifact.thread}`,
            }
          }
          onView={(v) => (view.current = v)}
        />
      )}
    </div>
  )
}
