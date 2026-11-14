import { useEffect, useRef, type ReactNode } from 'react'

import { Coordinator, type Recap, type RecapItem, type ThreadView, type WorkspaceView } from '../../shared/model'
import { store, useStore, type Win } from '../store'
import { CloseWindow } from './CloseWindow'
import { handlers, yank, type WinAction } from './registry'

const counted = (n: number, what: string) => (n === 1 ? `1 ${what}` : `${n} ${what}s`)

// evidence is what the hub saw the turn do, in short. "no commands" stays:
// it tells a claim that nothing checked.
function evidence(r: Recap): ReactNode[] {
  const ran = r.ran ?? 0
  const failed = r.failed ?? 0
  const out: ReactNode[] = [
    <span key="ran">
      {ran === 0 ? 'no commands' : counted(ran, 'command')}
      {failed > 0 && <span className="t-subtle">, {failed} failed</span>}
    </span>,
  ]
  if (r.edited?.length) out.push(<span key="edited">{counted(r.edited.length, 'file')} edited</span>)
  for (const a of r.artifacts ?? [])
    out.push(
      <span key={`a-${a}`}>
        <span className="t-foam">◇ </span>
        <span className="t-subtle">{a}</span>
      </span>,
    )
  return out
}

const joined = (parts: ReactNode[]) => parts.flatMap((p, i) => (i === 0 ? [p] : [<span key={`s${i}`}> · </span>, p]))

// RecapRows are a thread's recap under its row in the threads list: what came
// out, what the hub saw, and what is open, one row each, so a long outcome
// does not push the facts out. A running thread shows its live report instead.
export function RecapRows(props: { v: ThreadView }) {
  const { v } = props
  const r = v.recap
  if (!r || v.state === 'running' || v.state === 'resolved') return null
  return (
    <>
      {r.error ? (
        <div className="list-detail list-recap t-love">✗ {r.error}</div>
      ) : (
        r.outcome && <div className={`list-detail list-recap t-muted ${r.fromReply ? 'from-reply' : ''}`}>{r.headline || r.outcome}</div>
      )}
      <div className="list-detail list-recap t-muted">{joined(evidence(r))}</div>
      {r.open && (
        <div className="list-detail list-recap t-muted">
          open: <span className="t-subtle">{r.open}</span>
        </div>
      )}
    </>
  )
}

const hhmm = (at: string) => new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })

// workspaceCounts are the hub's counts of a workspace's threads, as the
// recap shows them: a thread whose last turn failed is not idle.
export function workspaceCounts(w?: WorkspaceView) {
  const c = w?.counts ?? {}
  const failed = w?.recap?.failed ?? 0
  const counts = { waits: c.waiting ?? 0, running: c.running ?? 0, failed, idle: Math.max(0, (c.idle ?? 0) - failed) }
  return { ...counts, any: counts.waits + counts.running + counts.failed + counts.idle > 0 }
}

// rank orders the threads of a workspace summary with no items of its
// coordinator: what needs the person, what failed, what came out that they
// have not seen, what still runs, and the rest.
function rank(v: ThreadView): number {
  if (v.state === 'waiting') return 0
  if (v.recap?.error) return 1
  if (v.state === 'idle' && v.recapUnseen) return 2
  if (v.state === 'running') return 3
  return 4
}

// tone is the color of a thread's state: gold waits for the person, love
// failed, foam runs, pine came out.
function tone(v: ThreadView): string {
  if (v.state === 'waiting') return 'gold'
  if (v.recap?.error) return 'love'
  if (v.state === 'running') return 'foam'
  return v.recap ? 'pine' : 'muted'
}

const glyph = (v: ThreadView) =>
  v.state === 'waiting' ? '◆' : v.recap?.error ? '✗' : v.state === 'running' ? '●' : v.recap ? '✓' : '○'

// Row is one line of a summary. text is what y yanks; open, when set, is
// what <CR> and a click open.
interface Row {
  node: ReactNode
  text: string
  open?: () => void
  cls?: string
}

// sections are the three lists of a summary, in the order the person acts
// on them.
const sections: { key: 'needs' | 'done' | 'doing'; title: string; glyph: string; tone: string }[] = [
  { key: 'needs', title: 'Needs you', glyph: '◆', tone: 'gold' },
  { key: 'done', title: 'Done', glyph: '✓', tone: 'pine' },
  { key: 'doing', title: 'In progress', glyph: '●', tone: 'foam' },
]

function itemRows(lists: { done?: RecapItem[]; doing?: RecapItem[]; needs?: RecapItem[] }, win: number): Row[] {
  const out: Row[] = []
  for (const sec of sections) {
    const items = lists[sec.key] ?? []
    if (items.length === 0) continue
    if (out.length) out.push({ node: '', text: '' })
    out.push({ node: <span className="t-subtle">{sec.title}</span>, text: sec.title, cls: 'summary-head' })
    for (const it of items) {
      const link = it.thread ? `→ ${it.thread}` : it.artifact ? `◇ ${it.artifact}` : ''
      out.push({
        node: (
          <>
            <span className={`glyph t-${sec.tone}`}>{sec.glyph}</span>
            <span className="t-text">{it.text}</span>
            {link && <span className="t-muted summary-link">{link}</span>}
          </>
        ),
        text: it.text,
        open: it.thread ? () => store.show(win, { kind: 'chat', thread: it.thread! }) : it.artifact ? () => void store.openArtifact(it.artifact!, win) : undefined,
      })
    }
  }
  return out
}

function threadRows(v: ThreadView, win: number): Row[] {
  const r = v.recap
  const out: Row[] = [
    {
      node: (
        <>
          <span className="box-name">{v.name}</span>
          <span className="t-subtle"> · {v.state}</span>
        </>
      ),
      text: `${v.name} · ${v.state}`,
      cls: 'summary-title',
    },
  ]
  if (!r) return [...out, { node: <span className="t-muted">no summary yet · :summary! asks for one</span>, text: '' }]
  if (r.error) out.push({ node: <span className="t-love">✗ {r.error}</span>, text: r.error })
  out.push({ node: '', text: '' })
  const lists = r.done?.length || r.doing?.length || r.needs?.length ? r : { done: r.outcome ? [{ text: r.outcome }] : [], needs: r.open ? [{ text: r.open }] : [] }
  out.push(...itemRows(lists, win))
  out.push({ node: '', text: '' })
  out.push({ node: <span className="t-muted">last turn  {joined(evidence(r))}</span>, text: '' })
  return out
}

function workspaceRows(name: string, w: WorkspaceView | undefined, threads: ThreadView[], win: number): Row[] {
  const r = w?.recap
  const out: Row[] = [
    {
      node: (
        <>
          <span className="box-name">{name}</span>
          {r?.goal && <span className="t-subtle"> · {r.goal}</span>}
        </>
      ),
      text: r?.goal ? `${name} · ${r.goal}` : name,
      cls: 'summary-title',
    },
  ]
  const { waits, failed } = workspaceCounts(w)
  const counts: ReactNode[] = []
  if (waits) counts.push(<span key="w"><span className="t-gold">◆ {waits}</span> {waits === 1 ? 'waits' : 'wait'} for you</span>)
  if (failed) counts.push(<span key="f"><span className="t-love">✗ {failed}</span> failed</span>)
  if (counts.length) out.push({ node: <span className="t-muted">{joined(counts)}</span>, text: '' })
  out.push({ node: '', text: '' })
  if (r?.done?.length || r?.doing?.length || r?.needs?.length) out.push(...itemRows(r, win))
  else
    for (const v of [...threads].sort((a, b) => rank(a) - rank(b)))
      out.push({
        node: (
          <>
            <span className={`glyph t-${tone(v)}`}>{glyph(v)}</span>
            <span className={v.recapUnseen || v.state === 'waiting' ? 't-text' : 't-subtle'}>{v.name}</span>
            <span className={v.recapUnseen ? 't-text' : 't-muted'}>{v.state === 'waiting' ? `asks: ${v.waiting ?? ''}` : v.recap?.error || (v.state === 'running' ? '' : v.recap?.headline || v.recap?.outcome || '')}</span>
          </>
        ),
        text: v.name,
        open: () => store.show(win, { kind: 'chat', thread: v.name }),
      })
  const names = (ts: string[]) => (ts.length > 2 ? `${ts.slice(0, -1).join(', ')} and ${ts[ts.length - 1]} all` : `${ts[0]} and ${ts[1]} both`)
  if (r?.overlaps?.length) out.push({ node: '', text: '' })
  for (const o of r?.overlaps ?? [])
    out.push({
      node: (
        <>
          <span className="t-subtle">{names(o.threads)} edited </span>
          <span className="t-text">{o.path}</span>
        </>
      ),
      text: o.path,
    })
  const newer = r?.newer ?? 0
  if (r?.at && newer > 0)
    out.push({ node: <span className="t-muted">as of {hhmm(r.at)} · {newer === 1 ? '1 thread' : `${newer} threads`} ended since</span>, text: '' })
  return out
}

// SummaryWindow is how a thread's work, or the workspace, stands: what
// needs the person, what is done and what is in progress, in the agents'
// words. It is a window like any other: j k move its cursor line, <CR> or
// a click opens the thread or artifact of an item, and <C-o> goes back.
export function SummaryWindow(props: { win: Win; focused: boolean }) {
  const { win, focused } = props
  const subject = win.buf.kind === 'summary' ? win.buf.subject : Coordinator
  const workspace = useStore((s) => s.workspace)
  const threads = useStore((s) => s.threads)
  const order = useStore((s) => s.order)
  const w = useStore((s) => s.workspaces.find((x) => x.name === s.workspace))
  const rows: Row[] =
    subject === Coordinator
      ? workspaceRows(workspace, w, order.filter((n) => n !== Coordinator && !n.includes('/') && threads[n] && threads[n].state !== 'resolved').map((n) => threads[n]), win.id)
      : threads[subject]
        ? threadRows(threads[subject], win.id)
        : [{ node: <span className="t-muted">no thread {subject}</span>, text: '' }]
  const cursor = Math.min(Math.max(win.cursor, 0), rows.length - 1)
  const el = useRef<HTMLDivElement>(null)

  const handle = (a: WinAction, count: number): boolean => {
    const set = (i: number) => store.updateWin(win.id, { cursor: Math.max(0, Math.min(i, rows.length - 1)) })
    switch (a) {
      case 'down':
        set(cursor + count)
        return true
      case 'up':
        set(cursor - count)
        return true
      case 'top':
        set(0)
        return true
      case 'bottom':
        set(rows.length - 1)
        return true
      case 'halfDown':
      case 'pageDown':
        set(cursor + 10)
        return true
      case 'halfUp':
      case 'pageUp':
        set(cursor - 10)
        return true
      case 'enter':
        rows[cursor]?.open?.()
        return true
      case 'yank': {
        const text = rows[cursor]?.text
        if (!text) return true
        yank.text = text
        window.nh.copy(text)
        store.say('yanked 1 line', 'muted')
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
    el.current?.querySelector(`[data-i="${cursor}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [cursor])

  return (
    <div className="window summary" onMouseDown={() => store.focus(win.id)}>
      <div className={`statusline ${focused ? 'focused' : ''}`}>
        <span className="sl-ws">{workspace} · </span>
        <span className="sl-name">summary</span>
        <span className="sl-fill" />
        <span className="sl-part t-muted">{subject === Coordinator ? 'workspace' : subject}</span>
        <CloseWindow id={win.id} />
      </div>
      <div className="list summary-list" ref={el} role="list" aria-label="summary">
        {rows.map((r, i) => (
          <div
            key={i}
            data-i={i}
            role="listitem"
            className={`summary-row ${r.cls ?? ''} ${r.open ? 'linked' : ''} ${focused && i === cursor ? 'cursor' : ''}`}
            onClick={() => {
              store.updateWin(win.id, { cursor: i })
              r.open?.()
            }}
          >
            {r.node || '\u00a0'}
          </div>
        ))}
      </div>
    </div>
  )
}
