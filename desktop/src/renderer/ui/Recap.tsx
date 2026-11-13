import type { ReactNode } from 'react'

import type { Recap, ThreadView, WorkspaceRecap } from '../../shared/model'

const counted = (n: number, what: string) => (n === 1 ? `1 ${what}` : `${n} ${what}s`)

// evidence is what the hub saw the turn do, in short. "no commands" stays:
// it tells a claim that nothing checked.
function evidence(r: Recap): ReactNode[] {
  const ran = r.ran ?? 0
  const failed = (r.commands ?? []).filter((c) => c.failed).length
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
        r.outcome && <div className={`list-detail list-recap t-muted ${r.fromReply ? 'from-reply' : ''}`}>{r.outcome}</div>
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

// RecapHead is a thread's recap at the top of its chat, one row per part:
// the thread's words, then what the hub saw.
export function RecapHead(props: { r?: Recap }) {
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
                ...(more > 0 ? [<span key="more">+{more} earlier</span>] : []),
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

// WorkspaceRecapHead is the workspace recap at the top of the coordinator's
// chat: the goal, the counts, the files two threads edited, then the
// coordinator's lines. It says only what no thread's row says, and shows
// nothing while no thread is open.
export function WorkspaceRecapHead(props: { r?: WorkspaceRecap; threads: ThreadView[] }) {
  const { r } = props
  const threads = props.threads.filter((v) => v.role === 'thread')
  const waits = threads.filter((v) => v.state === 'waiting').length
  const running = threads.filter((v) => v.state === 'running').length
  const failed = threads.filter((v) => v.state === 'idle' && v.recap?.error).length
  const idle = threads.filter((v) => v.state === 'idle' && !v.recap?.error).length
  if (waits + running + failed + idle === 0) return null
  const counts: ReactNode[] = []
  if (waits) counts.push(<span key="w"><span className="t-gold">◆ {waits}</span> {waits === 1 ? 'waits' : 'wait'} for you</span>)
  if (failed) counts.push(<span key="f"><span className="t-love">✗ {failed}</span> failed</span>)
  if (running) counts.push(<span key="r"><span className="t-foam">● {running}</span> running</span>)
  if (idle) counts.push(<span key="i">{idle} idle</span>)
  const newer = r?.at ? threads.filter((v) => v.recap && v.recap.at > r.at!).length : 0
  const names = (ts: string[]) =>
    ts.length > 2 ? `${ts.slice(0, -1).join(', ')} and ${ts[ts.length - 1]} all` : `${ts[0]} and ${ts[1]} both`
  return (
    <div className="recap recap-ws">
      <div className="recap-inner">
        {r?.goal && <div className="recap-goal t-subtle">{r.goal}</div>}
        <div className="recap-counts t-muted">{joined(counts)}</div>
        {r?.overlaps?.map((o) => (
          <div key={o.path} className="recap-body">
            <span className="t-subtle">{names(o.threads)} edited </span>
            <span className="t-text">{o.path}</span>
          </div>
        ))}
        {r?.lines?.map((l, i) => (
          <div key={i} className="recap-line t-text">
            {l}
          </div>
        ))}
        {!!r?.lines?.length && newer > 0 && (
          <div className="t-muted">
            as of {hhmm(r.at!)} · {newer === 1 ? '1 thread' : `${newer} threads`} ended since
          </div>
        )}
      </div>
    </div>
  )
}
