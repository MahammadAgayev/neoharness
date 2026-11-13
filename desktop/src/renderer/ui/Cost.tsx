import { useEffect, useRef, useState } from 'react'

import type { CostReport as CostData, CostPart } from '../../shared/model'
import { keyName } from '../lib/keys'
import { store, useStore } from '../store'

const money = (n: number) => (n > 0 && n < 0.01 ? '<$0.01' : `$${n.toFixed(2)}`)

function dayLabel(d: string, today: boolean): string {
  if (today) return 'today'
  return new Date(d + 'T12:00:00').toLocaleDateString(undefined, { weekday: 'narrow' })
}

function when(at: string): string {
  return new Date(at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

// Bars are one hue: they show how much, and the label says of what.
function Bars(props: { title: string; parts: CostPart[]; open?: (p: CostPart) => void; prefix?: boolean }) {
  const top = Math.max(...props.parts.map((p) => p.cost), 0)
  if (!props.parts.length) return null
  return (
    <section className="cost-section">
      <h2>{props.title}</h2>
      {props.parts.map((p) => (
        <div
          key={`${p.workspace ?? ''}/${p.name}`}
          className={`cost-bar-row ${props.open ? 'clickable' : ''}`}
          onClick={() => props.open?.(p)}
          data-tip={p.turns ? `${p.turns} turn${p.turns === 1 ? '' : 's'} logged` : undefined}
        >
          <span className="cost-bar-name">
            {props.prefix && p.workspace && <span className="t-subtle">{p.workspace} · </span>}
            {p.name}
          </span>
          <span className="cost-bar-track">
            <span className="cost-bar" style={{ width: `${top ? Math.max((p.cost / top) * 100, 1) : 0}%` }} />
          </span>
          <span className="cost-bar-cost">{money(p.cost)}</span>
        </div>
      ))}
    </section>
  )
}

// Cost is what the open workspace, or every workspace, cost: the totals, the
// last 14 days, by thread and model, and the turns that cost the most. Tab
// switches the scope, Esc closes it.
export function Cost() {
  const scope = useStore((s) => s.cost)
  const workspace = useStore((s) => s.workspace)
  const screen = useStore((s) => s.screen)
  const [data, setData] = useState<CostData | null>(null)
  const [failed, setFailed] = useState(false)
  const el = useRef<HTMLDivElement>(null)

  useEffect(() => {
    setData(null)
    setFailed(false)
    if (!scope) return
    let live = true
    store
      .costOf(scope)
      .then((d) => live && setData(d))
      .catch(() => live && setFailed(true))
    return () => {
      live = false
    }
  }, [scope, workspace])
  useEffect(() => {
    if (scope) el.current?.focus()
  }, [scope])

  if (!scope) return null
  const canWorkspace = !!workspace && screen === 'workspace'
  const close = () => store.openCost(null)
  const goThread = (ws: string, thread: string) => {
    close()
    void store.goThread(ws, thread)
  }
  const onKey = (e: React.KeyboardEvent) => {
    const k = keyName(e.nativeEvent)
    const d = el.current
    if (!d || !k) return
    const by: Record<string, number> = { j: 40, k: -40, '<Down>': 40, '<Up>': -40, '<C-d>': d.clientHeight / 2, '<C-u>': -d.clientHeight / 2 }
    if (k === '<Esc>' || k === 'q') close()
    else if (k === '<Tab>' || k === '<S-Tab>') {
      if (canWorkspace) store.openCost(scope === 'all' ? 'workspace' : 'all')
    } else if (by[k] !== undefined) d.scrollBy({ top: by[k] })
    else return
    e.preventDefault()
    e.stopPropagation()
  }
  const days = data?.days ?? []
  const dayTop = Math.max(...days.map((d) => d.cost), 0)
  const fortnight = days.reduce((n, d) => n + d.cost, 0)
  return (
    <div className="overlay" onMouseDown={close}>
      <div className="palette settings cost-dialog" onMouseDown={(e) => e.stopPropagation()} onKeyDown={onKey}>
        <div className="settings-head">
          <span className="settings-title">Cost</span>
          <div className="cost-tabs">
            {canWorkspace && (
              <button className={`tab ${scope === 'workspace' ? 'active' : ''}`} onClick={() => store.openCost('workspace')}>
                {workspace}
              </button>
            )}
            <button className={`tab ${scope === 'all' ? 'active' : ''}`} onClick={() => store.openCost('all')}>
              All workspaces
            </button>
          </div>
          <span className="t-muted">{canWorkspace ? 'Tab switches · Esc closes' : 'Esc closes'}</span>
        </div>
        <div className="settings-body cost-body" ref={el} tabIndex={-1}>
          {!data && <div className="palette-empty t-muted">{failed ? 'the hub did not answer' : 'adding it up…'}</div>}
          {data && data.total === 0 && <div className="palette-empty t-muted">Nothing spent yet.</div>}
          {data && data.total > 0 && (
            <>
              <div className="cost-stats">
                <div className="cost-stat">
                  <div className="cost-stat-value">{money(data.total)}</div>
                  <div className="cost-stat-label">in all</div>
                </div>
                <div className="cost-stat">
                  <div className="cost-stat-value">{money(data.today)}</div>
                  <div className="cost-stat-label">today</div>
                </div>
                <div className="cost-stat">
                  <div className="cost-stat-value">{money(fortnight)}</div>
                  <div className="cost-stat-label">last 14 days</div>
                </div>
              </div>
              <section className="cost-section">
                <h2>last 14 days</h2>
                <div className="cost-days">
                  {days.map((d, i) => {
                    const today = i === days.length - 1
                    return (
                      <div
                        key={d.name}
                        className={`cost-day ${today ? 'today' : ''}`}
                        data-tip={`${new Date(d.name + 'T12:00:00').toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} · ${money(d.cost)}${d.turns ? ` · ${d.turns} turn${d.turns === 1 ? '' : 's'}` : ''}`}
                      >
                        <div className="cost-day-plot">
                          <div className="cost-day-bar" style={{ height: `${dayTop ? Math.max((d.cost / dayTop) * 100, d.cost ? 2 : 0) : 0}%` }} />
                        </div>
                        <div className="cost-day-label">{dayLabel(d.name, today)}</div>
                      </div>
                    )
                  })}
                </div>
              </section>
              <div className="cost-columns">
                {scope === 'all' && <Bars title="by workspace" parts={data.workspaces} open={(p) => goThread(p.name, 'coordinator')} />}
                <Bars
                  title={scope === 'all' ? 'threads that cost the most' : 'by thread'}
                  parts={scope === 'all' ? data.threads.slice(0, 10) : data.threads}
                  prefix={scope === 'all'}
                  open={(p) => goThread(p.workspace ?? workspace, p.name)}
                />
                <Bars title="by model" parts={data.models} />
              </div>
              {data.top.length > 0 && (
                <section className="cost-section">
                  <h2>turns that cost the most</h2>
                  {data.top.map((t, i) => (
                    <div key={i} className="cost-turn clickable" onClick={() => goThread(t.workspace ?? workspace, t.thread)}>
                      <span className="cost-turn-name">
                        {scope === 'all' && t.workspace && <span className="t-subtle">{t.workspace} · </span>}
                        {t.thread}
                      </span>
                      <span className="t-muted">{when(t.at)}</span>
                      <span className="t-muted">{t.model?.replace(/^claude-/, '')}</span>
                      <span className="cost-bar-cost">{money(t.cost)}</span>
                    </div>
                  ))}
                </section>
              )}
              {data.since && (
                <div className="cost-note t-muted">
                  The days, the models and the turns count from {new Date(data.since).toLocaleDateString(undefined, { month: 'long', day: 'numeric' })}, when the hub began to log each turn. The totals count every turn.
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}
