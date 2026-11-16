import { memo, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'

import { Coordinator, User, type Event, type ThreadView, type Todo } from '../../shared/model'
import { blockText, shortTool, shortTools, type Block } from '../lib/transcript'
import { wakeLabel } from '../lib/wake'
import { boardTime, stateGlyph, stateLabel, stateTone } from '../search'
import { store, useStore, type Win } from '../store'
import { escapeHTML, blocks as mdBlocks, openFence, render, renderOnce } from '../lib/markdown'
import { copied, Markdown, mdClick } from './Markdown'
import { Prompt, prompts } from './Prompt'
import { handlers, yank, type WinAction } from './registry'
import { Glyph } from './Icon'
import { CloseWindow } from './CloseWindow'
import { Echo } from './Echo'
import { Leave } from './Leave'
import { TodoMark } from './Panels'

const shellLines = 10

function shortModel(m: string): string {
  return m.replace(/^claude-/, '').replace('[', ' ').replace(/\]$/, '')
}

// PromptInfo is the line under the prompt, as in Claude Code: what the next
// message goes with. It has a line of its own, so
// a narrow column never squeezes the text you write.
function PromptInfo(props: { v: ThreadView | undefined; thread: string }) {
  const { v } = props
  const warn = useStore((s) => s.settings.compactwarn)
  const right: { text: string; tone: string; title?: string; run?: () => void }[] = []
  if (v) {
    if (v.queued > 0) right.push({ text: `+${v.queued} queued`, tone: 'muted' })
    if (v.location.host) right.push({ text: `ssh:${v.location.host}`, tone: 'muted' })
    if (v.branch) right.push({ text: v.branch, tone: 'muted', title: 'git branch' })
    if (v.model)
      right.push({ text: shortModel(v.model), tone: 'muted', title: 'Change the model', run: () => store.openChoice({ thread: props.thread, kind: 'model' }) })
    if (v.contextWindow) {
      const pct = Math.floor(((v.context ?? 0) * 100) / v.contextWindow)
      // Every turn sends the whole context again: time to /compact.
      right.push(
        warn > 0 && pct >= warn
          ? { text: `ctx ${pct}% · /compact`, tone: 'love', title: 'every turn sends the whole context; /compact saves tokens' }
          : { text: `ctx ${pct}%`, tone: 'muted', title: 'context used' },
      )
    }
  }
  if (!right.length) return null
  return (
    <div className="prompt-info">
      {right.reverse().map((p, i) => (
        <span
          key={i}
          className={`sl-part t-${p.tone} ${p.run ? 'pick' : ''}`}
          data-tip={p.title}
          data-keys={p.run ? '/model' : undefined}
          onClick={p.run}
        >
          {p.text}
        </span>
      ))}
    </div>
  )
}

function StatusLine(props: { v: ThreadView | undefined; focused: boolean; workspace: string; name: string; peek: boolean; id: number }) {
  const { v, focused } = props
  const [, tick] = useState(0)
  useEffect(() => {
    const ms = v?.state === 'running' || v?.state === 'waiting' || v?.wakeAt ? 60000 : 0
    if (!ms) return
    const t = setInterval(() => tick((n) => n + 1), ms)
    return () => clearInterval(t)
  }, [v?.state, v?.wakeAt])
  const wake = v ? wakeLabel(v) : ''
  return (
    <div className={`statusline ${focused ? 'focused' : ''}`}>
      <span className={`sl-ws ${props.peek ? 'peek' : ''}`}>{props.workspace} · </span>
      <span className="sl-name">{props.name}</span>
      {v && (
        <span className={`sl-state t-${stateTone(v.state)}`}>
          <Glyph g={stateGlyph(v.state)} size={12} /> {stateLabel(v)}
        </span>
      )}
      {v && v.state !== 'idle' && v.since && <span className="sl-dim">{boardTime(v.since)}</span>}
      {wake && <span className="sl-dim">{wake}</span>}
      <Echo focused={focused} />
      <CloseWindow id={props.id} />
    </div>
  )
}

// CopyButton copies a whole message as its markdown. It shows at the end
// of the message on hover or under the cursor, not under every reply.
function CopyButton(props: { text: string }) {
  return (
    <button
      className="copy msg-copy"
      onClick={(e) => {
        e.stopPropagation()
        copied(e.currentTarget, props.text)
      }}
    >
      Copy
    </button>
  )
}

const BlockView = memo(function BlockView(props: {
  b: Block
  open: boolean
  artifacts: Set<string>
  thread: string
  pendingPermission: boolean
  pendingQuestion?: boolean
  scope?: string
}) {
  const { b, open } = props
  switch (b.kind) {
    case 'message':
      if (b.from === User) return <div className="b-user">{b.text}</div>
      return (
        <div className="b-from copyable">
          <div className="b-label t-rose">from {b.from}</div>
          <Markdown text={b.text} artifacts={props.artifacts} />
          <CopyButton text={b.text} />
        </div>
      )
    case 'text':
      return (
        <div className="copyable">
          <Markdown className="b-text" text={b.text} artifacts={props.artifacts} />
          <CopyButton text={b.text} />
        </div>
      )
    case 'tools': {
      const last = b.tools[b.tools.length - 1]
      const failed = b.tools.filter((t) => t.failed).length
      const count = b.tools.length === 1 ? '1 tool call' : `${b.tools.length} tool calls`
      return (
        <div className="b-tools">
          <div className="b-tools-head" onClick={() => store.toggleFold(b.key)}>
            {b.live ? (
              <>
                <span className="spinner t-foam" />
                <span className="t-subtle">{shortTool(last.tool)}</span>
                <span className="t-muted ellipsis">{shortTools(last.detail)}</span>
                <span className="t-muted">· {count}</span>
              </>
            ) : (
              <>
                <span className="t-muted chevron">{open ? '▾' : '▸'}</span>
                <span className="t-muted">{count}</span>
                {failed > 0 && <span className="t-love">· {failed} failed</span>}
              </>
            )}
          </div>
          {open &&
            b.tools.map((t, i) => (
              <div key={i} className="b-tool">
                <span className="t-subtle">{shortTool(t.tool)}</span> <span className="t-muted">{shortTools(t.detail)}</span>
                {t.failed && <div className="t-love b-tool-failed">✗ {t.failed}</div>}
              </div>
            ))}
        </div>
      )
    }
    case 'error':
      return (
        <div className="card alert">
          <div className="card-head t-love">failed</div>
          <div className="pre-wrap">{b.text}</div>
        </div>
      )
    case 'permission': {
      if (b.answer) {
        return (
          <div className="b-line t-muted">
            <span>{shortTool(b.tool)}</span>
            <span className="ellipsis">{b.detail}</span>
            <span className={b.answer.startsWith('deny') ? 't-love' : ''}>· {b.answer}</span>
          </div>
        )
      }
      return (
        <div className="card permission">
          <div className="card-head">{shortTool(b.tool)}</div>
          {b.detail && <pre className="card-detail">{b.detail}</pre>}
          {props.pendingPermission && (
            <div className="card-actions">
              <button className="btn primary" onClick={() => void store.answerThread(props.thread, 'allow')} data-tip="Allow" data-keys="<leader>a">
                Allow <kbd className="hint">␣a</kbd>
              </button>
              {props.scope && (
                <button className="btn" onClick={() => void store.answerThread(props.thread, 'always')} data-tip={`Always allow ${props.scope}`} data-keys="<leader>A">
                  Always: {props.scope} <kbd className="hint">␣A</kbd>
                </button>
              )}
              <button className="btn danger" onClick={() => void store.answerThread(props.thread, 'deny')} data-tip="Deny" data-keys="<leader>x">
                Deny <kbd className="hint">␣x</kbd>
              </button>
            </div>
          )}
        </div>
      )
    }
    case 'answer':
      return (
        <div className={`b-line ${b.text.startsWith('deny') ? 't-love' : 't-pine'}`}>
          <span className="glyph t-muted">↳</span>
          {b.text}
        </div>
      )
    case 'ask':
      // A question that waits is a card, as a permission request is.
      if (props.pendingQuestion)
        return (
          <div className="card question">
            <div className="card-head">question</div>
            <Markdown className="card-question" text={b.text} artifacts={props.artifacts} />
            <div className="card-actions">
              <span className="t-muted card-hint">Write your answer below</span>
              <button className="btn" onClick={() => void store.answerThread(props.thread, 'deny')} data-tip="Skip the question" data-keys="<leader>x">
                Skip <kbd className="hint">␣x</kbd>
              </button>
            </div>
          </div>
        )
      return (
        <div className="b-ask">
          <div className="b-label t-muted">question</div>
          <Markdown text={b.text} artifacts={props.artifacts} />
        </div>
      )
    case 'progress':
      return (
        <div className="b-line t-subtle">
          <span className="glyph t-foam">↳</span>
          {b.text}
        </div>
      )
    case 'note':
      return (
        <div className="b-line t-muted italic">
          <span className="glyph">—</span>
          {b.text}
        </div>
      )
    case 'notice':
      return (
        <div className="b-line">
          <span className={`glyph ${b.needsYou ? 't-gold' : 't-foam'}`}>↗</span>
          <span className="t-muted">notified you:&nbsp;</span>
          <span className="t-subtle">{b.text}</span>
        </div>
      )
    case 'shell': {
      const lines = b.output
        .replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, '')
        .replace(/\n$/, '')
        .split('\n')
        .map((l) => l.slice(l.lastIndexOf('\r') + 1))
      const out = b.output ? lines : []
      const hidden = !open && out.length > shellLines ? out.length - shellLines : 0
      return (
        <div className="b-shell">
          <div className="b-line" onClick={() => store.toggleFold(b.key)}>
            {b.live ? <span className="spinner t-foam" /> : <span className="glyph t-rose">!</span>}
            <span className="t-subtle mono">{b.command}</span>
          </div>
          {out.length > 0 && <pre className="shell-out">{(hidden ? out.slice(0, shellLines) : out).join('\n')}</pre>}
          {hidden > 0 && <div className="t-muted italic indent">… {hidden} more lines · za</div>}
          {b.exit !== 0 && !b.live && <div className="t-love indent">exit {b.exit}</div>}
        </div>
      )
    }
    case 'artifact':
      return (
        <div className="b-line b-artifact" onClick={() => void store.openArtifactOf(props.thread, b.name)}>
          <span className="glyph t-foam">
            <Glyph g="▤" />
          </span>
          <span className="link">{b.title}</span>
          <span className="t-muted">
            · artifact{b.version > 1 ? ` v${b.version}` : ''}
            {b.userEdit ? ' · edited by you' : ''}
          </span>
        </div>
      )
  }
})

// TodoLine is the thread's todo list above the prompt: folded, the count
// and the item in hand; unfolded, every item.
function TodoLine(props: { todos: Todo[]; open: boolean; selected: boolean; onClick: () => void }) {
  const done = props.todos.filter((t) => t.state === 'done').length
  const cur = props.todos.find((t) => t.state === 'active') ?? props.todos.find((t) => t.state === 'pending')
  return (
    <div className={`row todo-line ${props.selected ? 'cursor' : ''}`}>
      <div className="todo-head" onClick={props.onClick} data-tip={props.open ? 'Fold the todo list' : 'Show the todo list'} data-keys="za">
        <span className="todo-fold">{props.open ? '▾' : '▸'}</span>
        {done} of {props.todos.length}
        {!props.open && cur && (
          <>
            {' · '}
            <span className="t-text">{cur.text}</span>
          </>
        )}
      </div>
      {props.open &&
        props.todos.map((t) => (
          <div key={t.id} className={`todo-item ${t.state}`}>
            <TodoMark state={t.state} />
            <span className="todo-text">{t.text}</span>
          </div>
        ))}
    </div>
  )
}

// StreamingText is the reply being written: its finished blocks, each
// rendered once from the cache, and the block still being written, rendered
// again on each delta. Code whose fence is still open shows plain,
// highlighted only once it closes.
function StreamingText(props: { text: string; artifacts: Set<string> }) {
  const bs = mdBlocks(props.text)
  return (
    <div className="md b-text" onClick={mdClick}>
      {bs.map((b, i) => (
        <StreamBlock key={i} text={b} done={i < bs.length - 1} artifacts={props.artifacts} />
      ))}
    </div>
  )
}

// StreamBlock fades in once, when the block first shows. A block keeps its
// element when it finishes, so a paragraph you read never fades again.
const StreamBlock = memo(function StreamBlock(props: { text: string; done: boolean; artifacts: Set<string> }) {
  const ref = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return
    for (const c of Array.from(ref.current?.children ?? []))
      c.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 120, easing: 'cubic-bezier(.16,1,.3,1)' })
  }, [])
  const html = props.done
    ? render(props.text, props.artifacts)
    : openFence(props.text)
      ? `<pre class="code"><code>${escapeHTML(props.text.replace(/^.*\n?/, ''))}</code></pre>`
      : renderOnce(props.text, props.artifacts)
  return <div ref={ref} className="md-block" dangerouslySetInnerHTML={{ __html: html }} />
})

function Queued(props: { e: Event; selected: boolean; onClick: () => void }) {
  return (
    <div className={`row b-queued ${props.selected ? 'cursor' : ''}`} onClick={props.onClick} data-tip="Send it now: the running turn stops" data-keys="<CR>">
      <div className="b-label italic">{props.e.from === User ? 'queued' : `queued from ${props.e.from}`}</div>
      <div className="pre-wrap">{props.e.text}</div>
    </div>
  )
}

export function ChatWindow(props: { win: Win; focused: boolean }) {
  const { win, focused } = props
  const thread = win.buf.kind === 'chat' ? win.buf.thread : Coordinator
  const open = useStore((s) => s.workspace)
  const { workspace, thread: name } = store.at(thread)
  const peek = workspace !== open
  const v = useStore((s) => s.threads[thread])
  const blocks = useStore((s) => s.chats[thread]) ?? []
  const folds = useStore((s) => s.folds)
  const recap = useStore(() => store.showsRecap(thread)) ? v?.recap : undefined
  const artifactList = useStore((s) => s.artifacts)
  // A peek's artifacts are of its workspace: they open from their blocks.
  const artifacts = useMemo(() => new Set(peek ? [] : artifactList.map((a) => a.name)), [artifactList, peek])
  const pending = v?.pending ?? []
  const todos = v?.todos ?? []
  const todoAt = todos.length > 0 ? blocks.length + pending.length : -1
  const todoOpen = useStore((s) => !!s.todoOpen[thread])
  const streaming = useStore((s) => s.streams[thread])
  const working = v?.state === 'running' && !streaming && !blocks.some((b) => 'live' in b && b.live)
  const scroller = useRef<HTMLDivElement>(null)
  const [search, setSearch] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  // vstart is where v started: the blocks from it to the cursor are selected.
  const [vstart, setVstart] = useState<number | null>(null)
  const total = blocks.length + pending.length + (todoAt < 0 ? 0 : 1)
  const cursor = win.cursor

  // The newest pending permission's card gets the buttons.
  const permKey = useMemo(() => {
    if (!v?.permission) return -1
    for (let i = blocks.length - 1; i >= 0; i--) {
      const b = blocks[i]
      if (b.kind === 'permission' && !b.answer) return b.key
    }
    return -1
  }, [blocks, v?.permission])
  const askKey = useMemo(() => {
    if (!v?.questionId) return -1
    for (let i = blocks.length - 1; i >= 0; i--) if (blocks[i].kind === 'ask') return blocks[i].key
    return -1
  }, [blocks, v?.questionId])

  // Follow the end as the chat grows, unless the person scrolled away or
  // selects: new blocks would scroll the selection away. The observer pins
  // the end after layout and before paint, only when the size changed, so
  // an update that changes nothing in sight costs no layout.
  const pinned = useRef(false)
  pinned.current = win.follow && vstart === null
  // fresh: blocks came while the person reads above; a chip offers the end
  // in place of a jump.
  const seen = useRef({ thread, n: blocks.length })
  if (win.follow || seen.current.thread !== thread || seen.current.n > blocks.length) seen.current = { thread, n: blocks.length }
  const fresh = !win.follow && blocks.length > seen.current.n
  useLayoutEffect(() => {
    const el = scroller.current
    if (el && pinned.current) el.scrollTop = el.scrollHeight
  }, [win.follow, vstart, thread])
  useEffect(() => {
    const el = scroller.current
    const inner = el?.firstElementChild
    if (!el || !inner) return
    const ro = new ResizeObserver(() => {
      if (pinned.current) el.scrollTop = el.scrollHeight
    })
    ro.observe(inner)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  // A long chat shows its end at once, and the older chunks above it while
  // the browser is idle, a few at a time, so it opens without a pause. A
  // move to a block not shown yet shows them all.
  const starts = chunkStarts(blocks.length)
  const [shownFrom, setShownFrom] = useState(() => Math.max(0, starts.length - 2))
  useLayoutEffect(() => setShownFrom(Math.max(0, chunkStarts(store.state.chats[thread]?.length ?? 0).length - 2)), [thread])
  useEffect(() => {
    if (shownFrom === 0) return
    const id = requestIdleCallback(() => setShownFrom((n) => Math.max(0, n - 2)), { timeout: 200 })
    return () => cancelIdleCallback(id)
  }, [shownFrom])
  const firstShown = starts[Math.min(shownFrom, starts.length - 1)] ?? 0
  useEffect(() => {
    if (cursor >= 0 && cursor < firstShown) setShownFrom(0)
  }, [cursor, firstShown])

  useEffect(() => {
    if (cursor < 0) return
    scroller.current?.querySelector(`[data-i="${cursor}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [cursor, shownFrom === 0])

  const setCursor = (i: number) => {
    const c = Math.max(0, Math.min(i, total - 1))
    store.updateWin(win.id, { cursor: c, follow: false })
  }
  const leavePrompt = () => {
    prompts.get(win.id)?.view.contentDOM.blur()
    store.setInsert(false)
    store.focus(win.id)
    store.updateWin(win.id, { cursor: total - 1 })
    scroller.current?.focus()
  }
  const toPrompt = (insert: boolean) => {
    if (v?.state === 'resolved') store.say(`${thread} is resolved · writing to it brings it back`, 'muted')
    store.updateWin(win.id, { cursor: -1, follow: true })
    prompts.get(win.id)?.focus(insert)
  }
  const matches = (q: string): number[] => {
    if (!q) return [] as number[]
    const fold = q.toLowerCase() === q
    const out: number[] = []
    blocks.forEach((b, i) => {
      const t = blockText(b)
      if ((fold ? t.toLowerCase() : t).includes(q)) out.push(i)
    })
    return out
  }
  const seek = (d: number) => {
    const ms = matches(query)
    if (!ms.length) {
      if (query) store.say(`not found: ${query}`, 'love')
      return
    }
    const from = cursor < 0 ? (d > 0 ? -1 : total) : cursor
    const next = d > 0 ? (ms.find((i) => i > from) ?? ms[0]) : ([...ms].reverse().find((i) => i < from) ?? ms[ms.length - 1])
    setCursor(next)
  }

  const matched = useMemo(() => new Set(matches(query)), [query, blocks])

  // Register the window's keys.
  const handle = (a: WinAction, count: number): boolean => {
    const el = scroller.current
    const half = el ? Math.floor(el.clientHeight / 2) : 300
    const at = cursor < 0 ? total : cursor
    const isMsg = (b: Block) => b.kind === 'message' || b.kind === 'text' || b.kind === 'ask'
    switch (a) {
      case 'down':
        if (cursor < 0) return true
        if (cursor + count >= total) {
          toPrompt(false)
          return true
        }
        setCursor(cursor + count)
        return true
      case 'up':
        setCursor(at - count)
        return true
      case 'top':
        setCursor(0)
        return true
      case 'bottom':
        toPrompt(false)
        return true
      case 'halfDown':
      case 'pageDown':
      case 'halfUp':
      case 'pageUp': {
        if (!el) return true
        const dy = (a.startsWith('half') ? half : el.clientHeight - 40) * (a.endsWith('Down') ? 1 : -1)
        el.scrollBy({ top: dy })
        // The cursor goes to the first block in sight, as <C-d> moves it in Vim.
        const top = el.getBoundingClientRect().top
        const rows = Array.from(el.querySelectorAll<HTMLElement>('[data-i]'))
        const first = rows.find((r) => r.getBoundingClientRect().bottom > top + dy + 8)
        if (first) store.updateWin(win.id, { cursor: Number(first.dataset.i), follow: false })
        return true
      }
      case 'nextMsg':
      case 'prevMsg': {
        const d = a === 'nextMsg' ? 1 : -1
        for (let i = at + d; i >= 0 && i < blocks.length; i += d) {
          if (isMsg(blocks[i])) {
            setCursor(i)
            return true
          }
        }
        return true
      }
      case 'enter': {
        if (cursor < 0) return false
        if (cursor === todoAt) {
          store.toggleTodo(thread)
          return true
        }
        if (cursor >= blocks.length) {
          void store.sendQueuedNow(thread)
          return true
        }
        const b = blocks[cursor]
        if (b.kind === 'artifact') void store.openArtifactOf(thread, b.name)
        else if (b.kind === 'tools' || b.kind === 'shell') store.toggleFold(b.key)
        else if (b.kind === 'message' && b.from === User) {
          // As Claude Code's message list: your message back to the prompt.
          prompts.get(win.id)?.setText(b.text)
          toPrompt(true)
        }
        return true
      }
      case 'todo':
        if (todoAt < 0) store.say('no todo list here · :todo shows the whole list', 'muted')
        else store.toggleTodo(thread)
        return true
      case 'fold': {
        if (cursor >= 0 && cursor === todoAt) {
          store.toggleTodo(thread)
          return true
        }
        const b = blocks[cursor]
        if (b && (b.kind === 'tools' || b.kind === 'shell')) store.toggleFold(b.key)
        return true
      }
      case 'visual':
        if (vstart !== null) setVstart(null)
        else if (cursor >= 0 && cursor < blocks.length) setVstart(cursor)
        return true
      case 'escape':
        if (vstart === null && !query) return false
        setVstart(null)
        setQuery('')
        return true
      case 'yank': {
        const sel = window.getSelection()?.toString()
        let text = sel || (blocks[cursor] ? blockText(blocks[cursor]) : '')
        if (vstart !== null) {
          const [a, b] = [Math.min(vstart, cursor), Math.max(vstart, cursor)]
          text = blocks.slice(a, b + 1).map(blockText).join('\n\n')
          setVstart(null)
        }
        if (!text) return true
        yank.text = text
        window.nh.copy(text)
        store.echo(`yanked ${text.split('\n').length} lines`)
        return true
      }
      case 'insert':
        toPrompt(true)
        return true
      case 'append':
        toPrompt(true)
        return true
      case 'stopHere':
        if (v?.state === 'running') void store.stop(thread)
        return true
      case 'interrupt':
        if (vstart !== null) setVstart(null)
        else void store.stop(thread)
        return true
      case 'takeBack':
        void store.takeBack(thread).then((t) => {
          if (t) {
            prompts.get(win.id)?.setText(t)
            toPrompt(false)
          }
        })
        return true
      case 'put': {
        const p = prompts.get(win.id)
        if (p && yank.text) {
          p.setText(p.text() + yank.text)
          toPrompt(false)
        }
        return true
      }
      case 'link': {
        const row = scroller.current?.querySelector(`[data-i="${cursor}"]`)
        const a = row?.querySelector<HTMLAnchorElement>('a[href]')
        const art = row?.querySelector<HTMLElement>('[data-artifact]')
        if (art?.dataset.artifact) void store.openArtifact(art.dataset.artifact)
        else if (a) void window.nh.openExternal(a.href)
        return true
      }
      case 'search':
        setSearch('')
        return true
      case 'searchNext':
        seek(1)
        return true
      case 'searchPrev':
        seek(-1)
        return true
    }
    return false
  }
  const handleRef = useRef(handle)
  handleRef.current = handle
  useEffect(() => {
    handlers.set(win.id, (a, n) => handleRef.current(a, n))
    return () => void handlers.delete(win.id)
  }, [win.id])

  // A request to write (i, :open, a new workspace) goes to the window in focus.
  const promptRequest = useStore(() => store.promptRequest)
  useEffect(() => {
    // A window that mounts after the request still takes it: :open shows
    // the thread and asks for its prompt at once.
    if (focused && store.promptRequest > store.promptHandled) {
      store.promptHandled = store.promptRequest
      toPrompt(true)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [promptRequest, focused])

  return (
    <div className="window chat" onMouseDown={() => store.focus(win.id)}>
      <StatusLine v={v} focused={focused} workspace={workspace} name={name} peek={peek} id={win.id} />
      {search !== null && (
        <div className="findbar">
          <span className="t-iris">/</span>
          <input
            autoFocus
            value={search}
            placeholder="search this chat"
            onChange={(e) => setSearch(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                setQuery(search)
                setSearch(null)
                const ms = matches(search)
                if (ms.length) setCursor([...ms].reverse().find((i) => i < (cursor < 0 ? total : cursor)) ?? ms[ms.length - 1])
                else store.say(`not found: ${search}`, 'love')
                scroller.current?.focus()
              }
              if (e.key === 'Escape') {
                setSearch(null)
                scroller.current?.focus()
              }
              e.stopPropagation()
            }}
          />
        </div>
      )}
      <div
        className="transcript"
        ref={scroller}
        tabIndex={-1}
        onWheel={(e) => {
          const el = scroller.current
          if (!el) return
          // The wheel event comes before the scroll it makes: judge where it
          // goes, so one turn up already stops the follow.
          const atEnd = el.scrollHeight - (el.scrollTop + e.deltaY) - el.clientHeight < 24
          if (atEnd !== win.follow) store.updateWin(win.id, { follow: atEnd })
        }}
      >
        <div
          className="transcript-inner"
          onMouseDown={(e) => {
            const i = rowAt(e)
            if (i >= 0 && e.detail === 2) {
              store.updateWin(win.id, { cursor: i, follow: false })
              handleRef.current('enter', 1)
            }
          }}
          onClick={(e) => {
            const i = rowAt(e)
            if (i >= 0 && !window.getSelection()?.toString()) store.updateWin(win.id, { cursor: i, follow: false })
          }}
          onContextMenu={(e) => {
            const i = rowAt(e)
            if (i < 0 || !blocks[i]) return
            e.preventDefault()
            store.updateWin(win.id, { cursor: i, follow: false })
            void blockMenu(blocks[i], (a) => handleRef.current(a, 1))
          }}
        >
          {blocks.length === 0 && pending.length === 0 && (
            <div className="empty">
              {thread === Coordinator
                ? 'Tell the coordinator what you want done. It opens threads, and threads do the work.'
                : 'Nothing here yet.'}
            </div>
          )}
          {starts.slice(Math.min(shownFrom, starts.length)).map((start) => (
            <div key={blocks[start].key} className="chunk">
              {blocks.slice(start, start + chunkRows).map((b, j) => {
                const i = start + j
                return (
                  <Row
                    key={b.key}
                    b={b}
                    i={i}
                    marks={`${focused && cursor === i ? 'cursor' : ''} ${matched.has(i) ? 'match' : ''} ${vstart !== null && i >= Math.min(vstart, cursor) && i <= Math.max(vstart, cursor) ? 'selected' : ''}`}
                    open={!!folds[b.key]}
                    artifacts={artifacts}
                    thread={thread}
                    pendingPermission={b.key === permKey}
                    pendingQuestion={b.key === askKey}
                    scope={b.key === permKey ? v?.permission?.scope : undefined}
                  />
                )
              })}
            </div>
          ))}
          {streaming && (
            <div className="row stream">
              <StreamingText text={streaming} artifacts={artifacts} />
            </div>
          )}
          {working && (
            <div className="row b-line t-subtle">
              <span className="spinner t-foam" />
              working
            </div>
          )}
          {pending.map((e, i) => (
            <div key={e.at} data-i={blocks.length + i}>
              <Queued e={e} selected={focused && cursor === blocks.length + i} onClick={() => void store.sendQueuedNow(thread)} />
            </div>
          ))}
        </div>
      </div>
      <div className="chip-slot">
        <Leave>
          {fresh && (
            <button className="new-chip" onClick={() => store.updateWin(win.id, { follow: true, cursor: -1 })} data-tip="Go to the end" data-keys="G">
              ↓ new
            </button>
          )}
        </Leave>
      </div>
      {/* The prompt dock: the recap, the todo line and the prompt, one object
          where the eyes go to write. */}
      <div className="dock">
        <Leave>
          {recap && (
            <div className="recap-card">
              <div className="recap-head">
                <span>recap</span>
                <button className="recap-close" data-tip="Close (Esc)" onClick={() => store.closeRecap(thread)}>
                  ×
                </button>
              </div>
              <div className="recap-text">{recap}</div>
            </div>
          )}
        </Leave>
        {todoAt >= 0 && (
          <div data-i={todoAt}>
            <TodoLine todos={todos} open={todoOpen} selected={focused && cursor === todoAt} onClick={() => store.toggleTodo(thread)} />
          </div>
        )}
        <div className={`prompt ${focused && cursor < 0 ? 'active' : ''}`} onClick={() => toPrompt(true)}>
          <Prompt win={win.id} thread={thread} resolved={v?.state === 'resolved'} onLeave={leavePrompt} />
        </div>
      </div>
      <PromptInfo v={v} thread={thread} />
    </div>
  )
}

// Row is one block of the transcript. It is memoized, so an update renders
// again only the rows whose block or marks changed, not the whole chat.
const Row = memo(function Row(props: {
  b: Block
  i: number
  marks: string
  open: boolean
  artifacts: Set<string>
  thread: string
  pendingPermission: boolean
  pendingQuestion: boolean
  scope?: string
}) {
  const { b } = props
  return (
    <div data-i={props.i} className={`row k-${b.kind} ${props.marks}`}>
      <BlockView b={b} open={props.open} artifacts={props.artifacts} thread={props.thread} pendingPermission={props.pendingPermission} pendingQuestion={props.pendingQuestion} scope={props.scope} />
    </div>
  )
})

// The rows go in chunks, and a chunk out of sight is not laid out or
// painted (styles/transcript.css). Per row, the browser would check 2000
// rows for sight on every frame; per chunk it checks a hundred.
const chunkRows = 24

function chunkStarts(n: number): number[] {
  const out: number[] = []
  for (let i = 0; i < n; i += chunkRows) out.push(i)
  return out
}

// rowAt is the index of the block row a mouse event is on, or -1. The rows
// share these handlers, so each row stays the same between renders.
function rowAt(e: React.MouseEvent): number {
  const el = (e.target as HTMLElement).closest<HTMLElement>('.row[data-i]')
  return el ? Number(el.dataset.i) : -1
}

// blockMenu is the right click on a block: the keys that act on it.
async function blockMenu(b: Block, act: (a: WinAction) => void): Promise<void> {
  const items = [{ id: 'yank', label: window.getSelection()?.toString() ? 'Copy the selection' : 'Copy' }]
  if (b.kind === 'artifact') items.push({ id: 'enter', label: 'Open the artifact' })
  if (b.kind === 'tools' || b.kind === 'shell') items.push({ id: 'fold', label: 'Show or hide the tool calls' })
  if (b.kind === 'message' && b.from === User) items.push({ id: 'enter', label: 'Edit and send again' })
  const id = await window.nh.menu(items)
  if (id) act(id as WinAction)
}
