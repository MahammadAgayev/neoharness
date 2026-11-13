import { useEffect, useRef, useState } from 'react'

import type { Spend as SpendData, SpendPart } from '../../shared/model'
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
function Bars(props: { title: string; parts: SpendPart[]; open?: (p: SpendPart) => void; prefix?: boolean }) {
  const top = Math.max(...props.parts.map((p) => p.cost), 0)
  if (!props.parts.length) return null
  return (
    <section className="spend-section">
      <h2>{props.title}</h2>
      {props.parts.map((p) => (
        <div
          key={`${p.workspace ?? ''}/${p.name}`}
          className={`spend-bar-row ${props.open ? 'clickable' : ''}`}
          onClick={() => props.open?.(p)}
          data-tip={p.turns ? `${p.turns} turn${p.turns === 1 ? '' : 's'} logged` : undefined}
        >
          <span className="spend-bar-name">
            {props.prefix && p.workspace && <span className="t-subtle">{p.workspace} · </span>}
            {p.name}
          </span>
          <span className="spend-bar-track">
            <span className="spend-bar" style={{ width: `${top ? Math.max((p.cost / top) * 100, 1) : 0}%` }} />
          </span>
          <span className="spend-bar-cost">{money(p.cost)}</span>
        </div>
      ))}
    </section>
  )
}

// Spend is what the open workspace, or every workspace, cost: the totals, the
// last 14 days, by thread and model, and the turns that cost the most. Tab
// switches the scope, Esc closes it.
export function Spend() {
  const scope = useStore((s) => s.spend)
  const workspace = useStore((s) => s.workspace)
  const screen = useStore((s) => s.screen)
  const [data, setData] = useState<SpendData | null>(null)
  const [failed, setFailed] = useState(false)
  const el = useRef<HTMLDivElement>(null)

  useEffect(() => {
    setData(null)
    setFailed(false)
    if (!scope) return
    let live = true
    store
      .spendOf(scope)
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
  const close = () => store.openSpend(null)
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
      if (canWorkspace) store.openSpend(scope === 'all' ? 'workspace' : 'all')
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
      <div className="palette settings spend-dialog" onMouseDown={(e) => e.stopPropagation()} onKeyDown={onKey}>
        <div className="settings-head">
          <span className="settings-title">Spend</span>
          <div className="spend-tabs">
            {canWorkspace && (
              <button className={`tab ${scope === 'workspace' ? 'active' : ''}`} onClick={() => store.openSpend('workspace')}>
                {workspace}
              </button>
            )}
            <button className={`tab ${scope === 'all' ? 'active' : ''}`} onClick={() => store.openSpend('all')}>
              All workspaces
            </button>
          </div>
          <span className="t-muted">{canWorkspace ? 'Tab switches · Esc closes' : 'Esc closes'}</span>
        </div>
        <div className="settings-body spend-body" ref={el} tabIndex={-1}>
          {!data && <div className="palette-empty t-muted">{failed ? 'the hub did not answer' : 'adding it up…'}</div>}
          {data && data.total === 0 && <div className="palette-empty t-muted">Nothing spent yet.</div>}
          {data && data.total > 0 && (
            <>
              <div className="spend-stats">
                <div className="spend-stat">
                  <div className="spend-stat-value">{money(data.total)}</div>
                  <div className="spend-stat-label">in all</div>
                </div>
                <div className="spend-stat">
                  <div className="spend-stat-value">{money(data.today)}</div>
                  <div className="spend-stat-label">today</div>
                </div>
                <div className="spend-stat">
                  <div className="spend-stat-value">{money(fortnight)}</div>
                  <div className="spend-stat-label">last 14 days</div>
                </div>
              </div>
              <section className="spend-section">
                <h2>last 14 days</h2>
                <div className="spend-days">
                  {days.map((d, i) => {
                    const today = i === days.length - 1
                    return (
                      <div
                        key={d.name}
                        className={`spend-day ${today ? 'today' : ''}`}
                        data-tip={`${new Date(d.name + 'T12:00:00').toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} · ${money(d.cost)}${d.turns ? ` · ${d.turns} turn${d.turns === 1 ? '' : 's'}` : ''}`}
                      >
                        <div className="spend-day-plot">
                          <div className="spend-day-bar" style={{ height: `${dayTop ? Math.max((d.cost / dayTop) * 100, d.cost ? 2 : 0) : 0}%` }} />
                        </div>
                        <div className="spend-day-label">{dayLabel(d.name, today)}</div>
                      </div>
                    )
                  })}
                </div>
              </section>
              <div className="spend-columns">
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
                <section className="spend-section">
                  <h2>turns that cost the most</h2>
                  {data.top.map((t, i) => (
                    <div key={i} className="spend-turn clickable" onClick={() => goThread(t.workspace ?? workspace, t.thread)}>
                      <span className="spend-turn-name">
                        {scope === 'all' && t.workspace && <span className="t-subtle">{t.workspace} · </span>}
                        {t.thread}
                      </span>
                      <span className="t-muted">{when(t.at)}</span>
                      <span className="t-muted">{t.model?.replace(/^claude-/, '')}</span>
                      <span className="spend-bar-cost">{money(t.cost)}</span>
                    </div>
                  ))}
                </section>
              )}
              {data.since && (
                <div className="spend-note t-muted">
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
