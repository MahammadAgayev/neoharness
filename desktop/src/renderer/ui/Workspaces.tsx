import { useEffect, useRef } from 'react'

import type { WorkspaceView } from '../../shared/model'
import { store, useStore } from '../store'
import { handlers } from './registry'

// The workspaces screen has no window id of its own; 0 is free.
export const workspacesWin = 0

function Spark(props: { values: number[] }) {
  const vs = props.values.length ? props.values : new Array(24).fill(0)
  const max = Math.max(1, ...vs)
  const w = 96
  const h = 22
  const step = w / Math.max(vs.length - 1, 1)
  const pts = vs.map((v, i) => `${(i * step).toFixed(1)},${(h - 2 - (v / max) * (h - 4)).toFixed(1)}`).join(' ')
  return (
    <svg className="spark" width={w} height={h} viewBox={`0 0 ${w} ${h}`} aria-label="turns per hour, last 24 hours">
      <polyline points={pts} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  )
}

function Counts(props: { w: WorkspaceView }) {
  const c = props.w.counts
  const parts: [string, string, number][] = [
    ['◆', 'gold', (c.waiting ?? 0) + (props.w.coordinator === 'waiting' ? 1 : 0)],
    ['●', 'foam', (c.running ?? 0) + (props.w.coordinator === 'running' ? 1 : 0)],
    ['○', 'muted', c.idle ?? 0],
    ['✓', 'pine', c.resolved ?? 0],
  ]
  return (
    <span className="counts">
      {parts
        .filter(([, , n]) => n > 0)
        .map(([g, t, n]) => (
          <span key={g} className={`t-${t}`}>
            {g} {n}
          </span>
        ))}
    </span>
  )
}

export function Workspaces() {
  const workspaces = useStore((s) => s.workspaces)
  const cursor = useStore((s) => s.workspaceCursor)
  const open = useStore((s) => s.workspace)
  const el = useRef<HTMLDivElement>(null)

  const handle = (a: string, count: number): boolean => {
    const w = workspaces[cursor]
    switch (a) {
      case 'down':
        store.setWorkspaceCursor(cursor + count)
        return true
      case 'up':
        store.setWorkspaceCursor(cursor - count)
        return true
      case 'top':
        store.setWorkspaceCursor(0)
        return true
      case 'bottom':
        store.setWorkspaceCursor(workspaces.length - 1)
        return true
      case 'enter':
        if (w) void store.openWorkspace(w.name)
        return true
      case 'stopHere':
      case 'delete':
        if (w) store.askDeleteWorkspace(w.name)
        return true
    }
    return false
  }
  const ref = useRef(handle)
  ref.current = handle
  useEffect(() => {
    handlers.set(workspacesWin, (a, n) => ref.current(a, n))
    return () => void handlers.delete(workspacesWin)
  }, [])
  useEffect(() => {
    el.current?.querySelector(`[data-i="${cursor}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [cursor])

  return (
    <div className="workspaces-screen" ref={el}>
      <div className="ws-inner">
        <div className="ws-head">
          <h1>Workspaces</h1>
          <button className="btn primary" onClick={() => store.openNewWorkspace()}>
            New workspace
          </button>
        </div>
        {workspaces.length === 0 && (
          <div className="ws-empty">
            <p>A workspace is a coordinator and its threads, working in one folder.</p>
            <p className="t-muted">
              <kbd>:new api</kbd> creates one and asks for its folder.
            </p>
          </div>
        )}
        <div className="ws-list">
          {workspaces.map((w, i) => (
            <div
              key={w.name}
              data-i={i}
              className={`ws-row ${i === cursor ? 'cursor' : ''} ${w.name === open ? 'open' : ''}`}
              onClick={() => void store.openWorkspace(w.name)}
              onMouseEnter={() => store.setWorkspaceCursor(i)}
            >
              <div className="ws-main">
                <div className="ws-name">{w.name}</div>
                <div className="ws-pin t-muted">{w.pin ? store.short(w.pin) : 'not pinned'}</div>
              </div>
              <Counts w={w} />
              <span className="t-iris ws-spark">
                <Spark values={w.activity} />
              </span>
              {i < 9 && <kbd className="ws-kbd">⌘{i + 1}</kbd>}
            </div>
          ))}
        </div>
        <div className="ws-foot t-muted">
          <kbd>j</kbd> <kbd>k</kbd> move · <kbd>↵</kbd> open · <kbd>x</kbd> delete · <kbd>:restore</kbd> bring one back
        </div>
      </div>
    </div>
  )
}
