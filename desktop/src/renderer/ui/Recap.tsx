import type { ReactNode } from 'react'

import { Coordinator, type Recap, type ThreadView, type WorkspaceView } from '../../shared/model'
import { store, useStore } from '../store'

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

// RecapHead is a thread's recap in its box, one row per part: the thread's
// words, then what the hub saw.
function RecapHead(props: { r?: Recap }) {
  const r = props.r
  if (!r) return null
  // The thread's words wrap to a few lines; what the hub saw keeps to one.
  const row = (label: string, body: ReactNode, words = false) => (
    <>
      <span className="recap-label">{label}</span>
      <span className={words ? 'recap-words' : 'recap-body'}>{body}</span>
    </>
  )
  const commands = r.commands ?? []
  const more = (r.ran ?? 0) - commands.length
  const changed: ReactNode[] = []
  if (r.edited?.length) changed.push(<span key="e" className="t-muted">{r.edited.join(', ')}</span>)
  for (const a of r.artifacts ?? [])
    changed.push(
      <span key={a}>
        <span className="t-foam">◇ </span>
        <span className="t-subtle">{a}</span>
      </span>,
    )
  return (
    <div className="recap">
      <div className="recap-inner">
        {r.asked && row('asked', <span className="t-subtle">{r.asked}</span>, true)}
        {r.outcome && row(r.fromReply ? 'reply' : 'outcome', <span className="t-text">{r.outcome}</span>, true)}
        {r.error && row('failed', <span className="t-love">{r.error}</span>, true)}
        {r.open && row('open', <span className="t-text">{r.open}</span>, true)}
        {row(
          'ran',
          commands.length === 0 ? (
            <span className="t-muted">no commands</span>
          ) : (
            <span className="t-muted">
              {/* The newest first: the last commands are most often the checks. */}
              {joined([
                ...[...commands].reverse().map((c, i) =>
                  c.failed ? (
                    <span key={i} className="recap-failed t-subtle">✗ {c.text}</span>
                  ) : (
                    <span key={i}>{c.text}</span>
                  ),
                ),
                ...(more > 0 ? [<span key="more">+{more} more</span>] : []),
              ])}
            </span>
          ),
        )}
        {changed.length > 0 && row('changed', <span className="t-muted">{joined(changed)}</span>)}
      </div>
    </div>
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

// boxRank orders the threads in the workspace's box: what needs the person,
// what failed, what came out that they have not seen, what still runs, and
// the rest.
function boxRank(v: ThreadView): number {
  if (v.state === 'waiting') return 0
  if (v.recap?.error) return 1
  if (v.state === 'idle' && v.recapUnseen) return 2
  if (v.state === 'running') return 3
  return 4
}

// tone is the color of a thread's state in the box: gold waits for the
// person, love failed, foam runs, pine came out.
function tone(v: ThreadView): string {
  if (v.state === 'waiting') return 'gold'
  if (v.recap?.error) return 'love'
  if (v.state === 'running') return 'foam'
  return v.recap ? 'pine' : 'muted'
}

const glyph = (v: ThreadView) =>
  v.state === 'waiting' ? '◆' : v.recap?.error ? '✗' : v.state === 'running' ? '●' : v.recap ? '✓' : '○'

// boxThreads is how many threads the box lists before it counts the rest.
const boxThreads = 8

// BoxRow is one thread in the workspace's box: its state and its headline.
// What came since the person last closed the box is in Text.
function BoxRow(props: { v: ThreadView }) {
  const { v } = props
  const news = v.recapUnseen ? 't-text' : 't-muted'
  let detail: ReactNode = null
  if (v.state === 'waiting')
    detail = v.permission ? (
      <>
        <span className="t-gold">permission · {v.permission.tool} </span>
        <span className="t-text">{v.permission.detail}</span>
      </>
    ) : v.waiting === 'paused' ? (
      <span className="t-gold">paused</span>
    ) : (
      <>
        <span className="t-gold">asks: </span>
        <span className="t-text">{v.waiting}</span>
      </>
    )
  else if (v.recap?.error) detail = <span className={news}>{v.recap.error}</span>
  else if (v.state === 'running') detail = <span className="t-muted">{v.progress}</span>
  else if (v.recap) detail = <span className={news}>{v.recap.headline || v.recap.outcome}</span>
  return (
    <button className="box-row" onClick={() => void store.showThread(v.name)}>
      <span className={`t-${tone(v)}`}>{glyph(v)}</span>
      <span className={v.recapUnseen || v.state === 'waiting' ? 't-text' : 't-subtle'}>{v.name}</span>
      <span className="box-detail">{detail}</span>
    </button>
  )
}

// WorkspaceBox is how the workspace stands: a strip of one cell per thread,
// the counts of what needs a look, one row per open thread, then the coordinator's lines and the
// files two threads edited. The hub counts; this only lays out.
function WorkspaceBox(props: { w?: WorkspaceView; threads: ThreadView[] }) {
  const r = props.w?.recap
  const { waits, failed } = workspaceCounts(props.w)
  const threads = [...props.threads].sort((a, b) => boxRank(a) - boxRank(b))
  const counts: ReactNode[] = []
  if (waits) counts.push(<span key="w"><span className="t-gold">◆ {waits}</span> {waits === 1 ? 'waits' : 'wait'} for you</span>)
  if (failed) counts.push(<span key="f"><span className="t-love">✗ {failed}</span> failed</span>)
  const newer = r?.newer ?? 0
  const names = (ts: string[]) =>
    ts.length > 2 ? `${ts.slice(0, -1).join(', ')} and ${ts[ts.length - 1]} all` : `${ts[0]} and ${ts[1]} both`
  return (
    <>
      <div className="box-counts t-muted">
        {threads.length > 0 && (
          <span className="box-strip">
            {threads.map((v) => (
              <span key={v.name} className={`box-cell bg-${tone(v)}`} data-tip={v.name} />
            ))}
          </span>
        )}
        <span className="recap-counts">{joined(counts)}</span>
      </div>
      {threads.length > 0 && (
        <div className="box-threads">
          {threads.slice(0, boxThreads).map((v) => (
            <BoxRow key={v.name} v={v} />
          ))}
          {threads.length > boxThreads && <div className="t-muted">+{threads.length - boxThreads} more in the threads list</div>}
        </div>
      )}
      {(!!r?.lines?.length || !!r?.overlaps?.length) && (
        <div className="box-lines">
          {r?.lines?.map((l, i) => (
            <div key={i} className="recap-line t-text">
              {l}
            </div>
          ))}
          {r?.overlaps?.map((o) => (
            <div key={o.path} className="recap-body">
              <span className="t-subtle">{names(o.threads)} edited </span>
              <span className="t-text">{o.path}</span>
            </div>
          ))}
          {!!r?.lines?.length && newer > 0 && (
            <div className="t-muted">
              as of {hhmm(r.at!)} · {newer === 1 ? '1 thread' : `${newer} threads`} ended since
            </div>
          )}
        </div>
      )}
    </>
  )
}

// RecapBox pops up at the top of the window when the person comes back to
// news, or on :recap: the workspace's, or a thread's in its window. Its
// border takes the color of what matters most in it. Closing it marks it
// seen in the hub.
export function RecapBox() {
  const subject = useStore((s) => s.recapBox)
  const screen = useStore((s) => s.screen)
  const workspace = useStore((s) => s.workspace)
  const threads = useStore((s) => s.threads)
  const order = useStore((s) => s.order)
  const w = useStore((s) => s.workspaces.find((x) => x.name === s.workspace))
  if (!subject || screen !== 'workspace') return null
  let title: ReactNode
  let body: ReactNode
  let border: string
  if (subject === Coordinator) {
    const open = order.filter((n) => n !== Coordinator && !n.includes('/') && threads[n] && threads[n].state !== 'resolved').map((n) => threads[n])
    const { waits, running, failed } = workspaceCounts(w)
    border = waits ? 'gold' : failed ? 'love' : running ? 'foam' : 'pine'
    title = (
      <>
        <span className="box-name">{workspace}</span>
        {w?.recap?.goal && <span className="t-subtle"> · {w.recap.goal}</span>}
      </>
    )
    body = <WorkspaceBox w={w} threads={open} />
  } else {
    const v = threads[subject]
    if (!v?.recap) return null
    border = tone(v)
    title = (
      <>
        <span className="box-name">{v.name}</span>
        <span className="t-subtle"> · {v.state}</span>
      </>
    )
    body = <RecapHead r={v.recap} />
  }
  return (
    <div className={`recap-box b-${border}`} role="dialog" aria-label="recap">
      <div className="box-head">
        <span className="box-title">{title}</span>
        <button className="btn ghost small" data-tip="Ask for a fresh recap. It runs a turn. · :recap!" onClick={() => void store.askRecap(subject)}>
          ask again
        </button>
        <button className="box-close" aria-label="close" data-tip="Close · Esc  q" onClick={() => void store.closeBox()}>
          ×
        </button>
      </div>
      {body}
    </div>
  )
}
