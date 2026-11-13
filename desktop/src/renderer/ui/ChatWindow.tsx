import { memo, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'

import { Coordinator, User, type Event, type ThreadView } from '../../shared/model'
import { blockText, shortTool, shortTools, type Block } from '../lib/transcript'
import { elapsed, stateGlyph, stateTone } from '../search'
import { store, useStore, type Win } from '../store'
import { Markdown } from './Markdown'
import { Prompt, prompts } from './Prompt'
import { handlers, yank, type WinAction } from './registry'

const shellLines = 10

function shortModel(m: string): string {
  return m.replace(/^claude-/, '').replace('[', ' ').replace(/\]$/, '')
}

function StatusLine(props: { v: ThreadView | undefined; focused: boolean; workspace: string; name: string }) {
  const { v, focused } = props
  const warn = useStore((s) => s.settings.compactwarn)
  const [, tick] = useState(0)
  useEffect(() => {
    if (v?.state !== 'running' && v?.state !== 'waiting') return
    const t = setInterval(() => tick((n) => n + 1), 1000)
    return () => clearInterval(t)
  }, [v?.state])
  const right: { text: string; tone: string; title?: string }[] = []
  if (v) {
    if (v.queued > 0) right.push({ text: `+${v.queued} queued`, tone: 'muted' })
    if (v.location.host) right.push({ text: `ssh:${v.location.host}`, tone: 'muted' })
    if (v.branch) right.push({ text: v.branch, tone: 'foam', title: 'git branch' })
    if (v.model) right.push({ text: shortModel(v.model), tone: 'rose', title: 'model' })
    if (v.contextWindow) {
      const pct = Math.floor(((v.context ?? 0) * 100) / v.contextWindow)
      // Every turn sends the whole context again: time to /compact.
      right.push(
        warn > 0 && pct >= warn
          ? { text: `ctx ${pct}% · /compact`, tone: 'love', title: 'every turn sends the whole context; /compact saves tokens' }
          : { text: `ctx ${pct}%`, tone: 'gold', title: 'context used' },
      )
    }
    if (v.cost) right.push({ text: `$${v.cost.toFixed(2)}`, tone: 'pine', title: 'cost of every turn' })
  }
  const wake = v && v.state === 'idle' && v.wakeAt ? `wakes at ${new Date(v.wakeAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}` : ''
  return (
    <div className={`statusline ${focused ? 'focused' : ''}`}>
      <span className="sl-ws">{props.workspace} · </span>
      <span className="sl-name">{props.name}</span>
      {v && (
        <span className={`sl-state t-${stateTone(v.state)}`}>
          {stateGlyph(v.state)} {v.state}
        </span>
      )}
      {v && v.state !== 'idle' && v.since && <span className="sl-dim">{elapsed(v.since)}</span>}
      {wake && <span className="sl-dim">{wake}</span>}
      <span className="sl-fill" />
      {right.map((p, i) => (
        <span key={i} className={`sl-part t-${focused || p.tone === 'love' ? p.tone : 'muted'}`} title={p.title}>
          {p.text}
        </span>
      ))}
    </div>
  )
}

const BlockView = memo(function BlockView(props: {
  b: Block
  open: boolean
  artifacts: Set<string>
  thread: string
  pendingPermission: boolean
}) {
  const { b, open } = props
  switch (b.kind) {
    case 'message':
      if (b.from === User) return <div className="b-user">{b.text}</div>
      return (
        <div className="b-from">
          <div className="b-label t-rose">from {b.from}</div>
          <Markdown text={b.text} artifacts={props.artifacts} />
        </div>
      )
    case 'text':
      return <Markdown className="b-text" text={b.text} artifacts={props.artifacts} />
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
        <div className="b-line t-love">
          <span className="glyph">✗</span>
          <span className="pre-wrap">{b.text}</span>
        </div>
      )
    case 'permission': {
      if (b.answer) {
        return (
          <div className="b-line t-muted">
            <span className="glyph">◇</span>
            <span>{shortTool(b.tool)}</span>
            <span className="ellipsis">{b.detail}</span>
            <span className={b.answer.startsWith('deny') ? 't-love' : ''}>· {b.answer}</span>
          </div>
        )
      }
      return (
        <div className="card permission">
          <div className="card-head">
            <span className="t-gold">◆</span> <span className="t-subtle">{shortTool(b.tool)}</span>
          </div>
          {b.detail && <pre className="card-detail">{b.detail}</pre>}
          {props.pendingPermission && (
            <div className="card-actions">
              <button className="btn primary" onClick={() => void store.answerThread(props.thread, 'allow')}>
                Allow <kbd>space a</kbd>
              </button>
              <button className="btn" onClick={() => void store.answerThread(props.thread, 'always')}>
                Always <kbd>space A</kbd>
              </button>
              <button className="btn danger" onClick={() => void store.answerThread(props.thread, 'deny')}>
                Deny <kbd>space x</kbd>
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
      return (
        <div className="b-ask">
          <div className="b-label t-gold">question</div>
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
        <div className="b-line b-artifact" onClick={() => void store.openArtifact(b.name)}>
          <span className="glyph t-foam">◇</span>
          <span className="link">{b.title}</span>
          <span className="t-muted">
            · artifact{b.version > 1 ? ` v${b.version}` : ''}
            {b.userEdit ? ' · edited by you' : ''}
          </span>
        </div>
      )
  }
})

function Queued(props: { e: Event; selected: boolean; onClick: () => void }) {
  return (
    <div className={`row b-queued ${props.selected ? 'cursor' : ''}`} onClick={props.onClick} title="send it now: the running turn stops">
      <div className="b-label italic">{props.e.from === User ? 'queued' : `queued from ${props.e.from}`}</div>
      <div className="pre-wrap">{props.e.text}</div>
    </div>
  )
}

export function ChatWindow(props: { win: Win; focused: boolean }) {
  const { win, focused } = props
  const thread = win.buf.kind === 'chat' ? win.buf.thread : Coordinator
  const workspace = useStore((s) => s.workspace)
  const v = useStore((s) => s.threads[thread])
  const blocks = useStore((s) => s.chats[thread]) ?? []
  const folds = useStore((s) => s.folds)
  const artifactList = useStore((s) => s.artifacts)
  const artifacts = useMemo(() => new Set(artifactList.map((a) => a.name)), [artifactList])
  const pending = v?.pending ?? []
  const working = v?.state === 'running' && !blocks.some((b) => 'live' in b && b.live)
  const scroller = useRef<HTMLDivElement>(null)
  const [search, setSearch] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const total = blocks.length + pending.length
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

  // Follow the end as the chat grows, unless the person scrolled away.
  useLayoutEffect(() => {
    const el = scroller.current
    if (el && win.follow) el.scrollTop = el.scrollHeight
  })

  useEffect(() => {
    if (cursor < 0) return
    scroller.current?.querySelector(`[data-i="${cursor}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [cursor])

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
        if (cursor >= blocks.length) {
          void store.sendQueuedNow(thread)
          return true
        }
        const b = blocks[cursor]
        if (b.kind === 'artifact') void store.openArtifact(b.name)
        else if (b.kind === 'tools' || b.kind === 'shell') store.toggleFold(b.key)
        else if (b.kind === 'message' && b.from === User) {
          // As Claude Code's message list: your message back to the prompt.
          prompts.get(win.id)?.setText(b.text)
          toPrompt(true)
        }
        return true
      }
      case 'fold': {
        const b = blocks[cursor]
        if (b && (b.kind === 'tools' || b.kind === 'shell')) store.toggleFold(b.key)
        return true
      }
      case 'yank': {
        const sel = window.getSelection()?.toString()
        const text = sel || (blocks[cursor] ? blockText(blocks[cursor]) : '')
        if (!text) return true
        yank.text = text
        window.nh.copy(text)
        store.say(`yanked ${text.split('\n').length} lines`, 'muted')
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
      <StatusLine v={v} focused={focused} workspace={workspace} name={thread} />
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
        onWheel={() => {
          const el = scroller.current
          if (!el) return
          const atEnd = el.scrollHeight - el.scrollTop - el.clientHeight < 24
          if (atEnd !== win.follow) store.updateWin(win.id, { follow: atEnd })
        }}
      >
        <div className="transcript-inner">
          {blocks.length === 0 && pending.length === 0 && (
            <div className="empty">
              {thread === Coordinator
                ? 'Tell the coordinator what you want done. It opens threads, and threads do the work.'
                : 'Nothing here yet.'}
            </div>
          )}
          {blocks.map((b, i) => (
            <div
              key={b.key}
              data-i={i}
              className={`row k-${b.kind} ${focused && cursor === i ? 'cursor' : ''} ${matched.has(i) ? 'match' : ''}`}
              onMouseDown={(e) => {
                if (e.detail === 2) {
                  store.updateWin(win.id, { cursor: i, follow: false })
                  handleRef.current('enter', 1)
                }
              }}
              onClick={() => {
                if (!window.getSelection()?.toString()) store.updateWin(win.id, { cursor: i, follow: false })
              }}
            >
              <BlockView b={b} open={!!folds[b.key]} artifacts={artifacts} thread={thread} pendingPermission={b.key === permKey} />
            </div>
          ))}
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
      <div className={`prompt ${focused && cursor < 0 ? 'active' : ''}`} onClick={() => toPrompt(true)}>
        <span className="prompt-mark">›</span>
        <Prompt win={win.id} thread={thread} resolved={v?.state === 'resolved'} onLeave={leavePrompt} />
      </div>
    </div>
  )
}
