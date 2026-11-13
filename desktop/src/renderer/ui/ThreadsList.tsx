import { useEffect, useRef, useState } from 'react'

import { Coordinator, location } from '../../shared/model'
import { elapsed, stateGlyph, stateTone } from '../search'
import { store, useStore, type Win } from '../store'
import { handlers, type WinAction } from './registry'
import { Glyph } from './Icon'
import { CloseWindow } from './CloseWindow'

export function ThreadsList(props: { win: Win; focused: boolean }) {
  const { win, focused } = props
  const workspace = useStore((s) => s.workspace)
  const threads = useStore((s) => s.threads)
  const showResolved = useStore((s) => s.showResolved)
  useStore((s) => s.order)
  const rows = store.threadRows()
  const cursor = Math.min(Math.max(win.cursor, 0), Math.max(rows.length - 1, 0))
  const el = useRef<HTMLDivElement>(null)
  const [, tick] = useState(0)
  useEffect(() => {
    const t = setInterval(() => tick((n) => n + 1), 1000)
    return () => clearInterval(t)
  }, [])

  const open = (i: number, how?: 'right' | 'above') => {
    const r = rows[i]
    if (!r) return
    if (r.state === 'resolved-fold') {
      store.toggleResolved()
      return
    }
    if (how) store.split(how === 'right', { kind: 'chat', thread: r.name })
    else store.show(win.id, { kind: 'chat', thread: r.name })
    store.focusPrompt()
  }

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
        open(cursor)
        return true
      case 'fold':
        if (rows[cursor]?.state === 'resolved-fold') store.toggleResolved()
        return true
      case 'splitRight':
        open(cursor, 'right')
        return true
      case 'splitAbove':
        open(cursor, 'above')
        return true
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

  const resolvedCount = Object.values(threads).filter((v) => v.state === 'resolved' && v.role === 'thread').length
  let lastGroup = ''
  return (
    <div className="window threads" onMouseDown={() => store.focus(win.id)}>
      <div className={`statusline ${focused ? 'focused' : ''}`}>
        <span className="sl-ws">{workspace} · </span>
        <span className="sl-name">threads</span>
        <span className="sl-fill" />
        <span className="sl-part t-muted">{rows.filter((r) => r.state !== 'resolved-fold').length}</span>
        <CloseWindow id={win.id} />
      </div>
      <div className="list" ref={el}>
        {rows.length === 0 && <div className="empty">No threads yet. The coordinator opens them, or :open a thread yourself.</div>}
        {rows.map((r, i) => {
          if (r.state === 'resolved-fold') {
            return (
              <div key="fold" data-i={i} className={`list-row fold ${focused && i === cursor ? 'cursor' : ''}`} onClick={() => open(i)}>
                <span className="t-muted">{showResolved ? '▾' : '▸'}</span>
                <span className="t-muted">{resolvedCount} resolved</span>
              </div>
            )
          }
          const v = threads[r.name]
          if (!v) return null
          const group = v.state === 'resolved' ? '' : v.state
          const head = group && group !== lastGroup ? group : ''
          lastGroup = group || lastGroup
          const detail =
            v.permission ? `permission · ${v.permission.tool}`
            : v.waiting === 'paused' ? 'paused · write to continue'
            : v.state === 'waiting' ? v.waiting || 'question'
            : v.progress || location(v.location)
          return (
            <div key={r.name}>
              {head && <div className="group-head">{head}</div>}
              <div
                data-i={i}
                className={`list-row ${focused && i === cursor ? 'cursor' : ''}`}
                onClick={() => {
                  store.updateWin(win.id, { cursor: i })
                  open(i)
                }}
                onContextMenu={(e) => {
                  e.preventDefault()
                  store.updateWin(win.id, { cursor: i })
                  void threadMenu(v.name, v.state, (how) => open(i, how))
                }}
              >
                <span className={`t-${stateTone(v.state)} glyph ${v.state === 'running' ? 'pulse' : ''}`}><Glyph g={stateGlyph(v.state)} /></span>
                <div className="list-main">
                  <div className="list-title">
                    <span className="name">{v.name}</span>
                    {v.queued > 0 && <span className="t-muted"> +{v.queued}</span>}
                    {(v.state === 'running' || v.state === 'waiting') && v.since && (
                      <span className="t-muted right">{elapsed(v.since)}</span>
                    )}
                  </div>
                  <div className={`list-detail ${v.state === 'waiting' ? 't-gold' : 't-muted'}`}>{detail}</div>
                </div>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

// threadMenu is the right click on a thread: what its keys and commands do.
async function threadMenu(name: string, state: string, open: (how?: 'right' | 'above') => void): Promise<void> {
  const own = name !== Coordinator
  const id = await window.nh.menu([
    { id: 'open', label: 'Open' },
    { id: 'right', label: 'Open in a split on the right' },
    { id: 'above', label: 'Open in a split above' },
    { id: '-', label: '' },
    ...(own ? [state === 'resolved' ? { id: 'reopen', label: 'Reopen' } : { id: 'resolve', label: 'Resolve' }] : []),
    ...(state === 'running' ? [{ id: 'stop', label: 'Stop the turn' }] : []),
    { id: 'copy', label: 'Copy the name' },
    ...(own ? [{ id: '-', label: '' }, { id: 'delete', label: 'Delete…' }] : []),
  ])
  if (id === 'open') open()
  else if (id === 'right' || id === 'above') open(id)
  else if (id === 'resolve') void store.resolve(name)
  else if (id === 'reopen') void store.reopen(name)
  else if (id === 'stop') void store.stop(name)
  else if (id === 'copy') window.nh.copy(name)
  else if (id === 'delete') store.askDeleteThread(name)
}
