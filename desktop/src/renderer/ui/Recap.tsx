import { useEffect, useRef, type ReactNode } from 'react'

import { Coordinator, type Recap, type ThreadView } from '../../shared/model'
import { tone } from '../lib/summary'
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

// SummaryCard sits on the prompt, where the person looks, and stands out
// from the chat: the coordinator's short view, then each thread in plain
// words, what needs the person first; in a thread's window, that thread's
// own part. j k move its cursor row, <CR> or a click opens what the row
// holds, q, Esc or × close it.
export function SummaryCard() {
  const subject = useStore((s) => s.summaryOf)
  const workspace = useStore((s) => s.workspace)
  const threads = useStore((s) => s.threads)
  useStore((s) => s.order)
  const w = useStore((s) => s.workspaces.find((x) => x.name === s.workspace))
  const cursor = useStore((s) => s.cardCursor)
  const el = useRef<HTMLDivElement>(null)
  useEffect(() => {
    el.current?.querySelector(`[data-i="${cursor}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [cursor])
  if (!subject) return null
  const rows = store.cardRows()
  let title: ReactNode
  let border: string
  if (subject === Coordinator) {
    const waits = w?.counts?.waiting ?? 0
    border = waits ? 'gold' : w?.recap?.failed ? 'love' : w?.counts?.running ? 'foam' : 'pine'
    title = (
      <>
        <span className="box-name">{workspace}</span>
        {w?.recap?.goal && <span className="t-subtle"> · {w.recap.goal}</span>}
      </>
    )
  } else {
    const v = threads[subject]
    if (!v) return null
    border = tone(v)
    title = (
      <>
        <span className="box-name">{v.name}</span>
        <span className="t-subtle"> · {v.state}</span>
      </>
    )
  }
  return (
    <div className={`recap-box summary-card b-${border}`} role="dialog" aria-label="summary">
      <div className="box-head">
        <span className="box-title">{title}</span>
        <button className="box-close" aria-label="close" data-tip="Close · Esc  q" onClick={() => store.closeSummary()}>
          ×
        </button>
      </div>
      <div className="summary-rows" ref={el}>
        {rows.length === 0 && <div className="summary-row t-muted">no summary yet · :summary! asks for one</div>}
        {rows.map((r, i) => (
          <div
            key={i}
            data-i={i}
            className={`summary-row ${r.gap ? 'gap' : ''} ${r.open ? 'linked' : ''} ${i === cursor ? 'cursor' : ''}`}
            onClick={() => store.openCardRow(i)}
          >
            {r.mark && <span className={`glyph t-${r.mark.tone}`}>{r.mark.text}</span>}
            {r.label && <span className={`summary-label t-${r.label.tone}`}>{r.label.text}</span>}
            <span className="summary-text">
              {r.parts.map((p, j) => (
                <span key={j} className={`t-${p.tone}`}>
                  {p.text}
                </span>
              ))}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}
