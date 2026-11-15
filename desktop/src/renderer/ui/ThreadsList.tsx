import { useEffect, useLayoutEffect, useRef, useState } from 'react'

import { Coordinator, type ThreadView } from '../../shared/model'
import { plain } from '../lib/toasts'
import { boardTime, stateGlyph, stateTone } from '../search'
import { store, useStore, type Win } from '../store'
import { handlers, type WinAction } from './registry'
import { Glyph } from './Icon'
import { CloseWindow } from './CloseWindow'

export function ThreadsList(props: { win: Win; focused: boolean }) {
  const { win, focused } = props
  const threads = useStore((s) => s.threads)
  const showResolved = useStore((s) => s.showResolved)
  const layout = useStore((s) => s.layout)
  useStore((s) => s.order)
  const [over, setOver] = useState(false)
  // Nothing moves under the person: a row that changes group waits while
  // the cursor or the mouse is on the board, then settles.
  const moved = store.settleBoard(focused || over)
  const rows = store.threadRows()
  const cursor = Math.min(Math.max(win.cursor, 0), Math.max(rows.length - 1, 0))
  const el = useRef<HTMLDivElement>(null)
  const [, tick] = useState(0)
  useEffect(() => {
    const t = setInterval(() => tick((n) => n + 1), 60000)
    return () => {
      clearInterval(t)
      store.settleBoard(false)
    }
  }, [])
  useFlip(el, moved)
  const shown = new Set(layout.cols.flatMap((c) => c.wins).flatMap((w) => (w.buf.kind === 'chat' ? [w.buf.thread] : [])))

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
      case 'hover': {
        const v = threads[rows[cursor]?.name ?? '']
        if (v) store.say(v.reply ? `${v.name}: ${plain(v.reply)}` : `${v.name} has no reply yet`, 'muted')
        return true
      }
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
  const now = Date.now()
  let lastGroup = ''
  return (
    <div className="window threads" onMouseDown={() => store.focus(win.id)}>
      <div className={`statusline ${focused ? 'focused' : ''}`}>
        <span className="sl-name">threads</span>
        <CloseWindow id={win.id} />
      </div>
      <div className="list board" ref={el} onMouseEnter={() => setOver(true)} onMouseLeave={() => setOver(false)}>
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
          const group = r.state === 'resolved' ? '' : r.state
          const head = group && group !== lastGroup ? `${group} · ${rows.filter((o) => o.state === group).length}` : ''
          lastGroup = group || lastGroup
          const live = v.state === 'running' || v.state === 'waiting'
          const settling = now - (store.settled[`${v.workspace}/${v.name}`] ?? 0) < settleMs
          return (
            <div key={r.name} data-name={r.name}>
              {head && <div className="group-head">{head}</div>}
              <div
                data-i={i}
                className={`list-row ${focused && i === cursor ? 'cursor' : ''} ${settling ? 'settle' : ''}`}
                data-tip={v.state === 'idle' && v.reply ? plain(v.reply) : undefined}
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
                <span className={`t-${stateTone(v.state)} glyph ${v.state === 'running' ? 'pulse' : ''}`}>
                  <Glyph g={stateGlyph(v.state)} />
                </span>
                <span className={`name ${shown.has(v.name) ? 'open' : ''}`}>{v.name}</span>
                {v.queued > 0 && <span className="t-muted">+{v.queued}</span>}
                <span className="time">{boardTime(live ? v.since : v.updatedAt, now)}</span>
              </div>
              <Need v={v} />
            </div>
          )
        })}
      </div>
    </div>
  )
}

const settleMs = 1000

// Need is what a waiting thread needs, to read without opening it.
function Need(props: { v: ThreadView }) {
  const { v } = props
  if (v.state !== 'waiting') return null
  const [label, text] = v.permission
    ? [`permission · ${v.permission.tool}`, v.permission.detail]
    : v.waiting === 'paused'
      ? ['paused', 'write to continue']
      : ['question', plain(v.waiting ?? '').split('\n')[0]]
  return (
    <div className="list-need">
      <span className="t-gold">{label}</span> {text}
    </div>
  )
}


// useFlip slides each row that moved to another group from where it was,
// so the eye can follow it. It runs only when the person is off the board.
function useFlip(list: React.RefObject<HTMLDivElement | null>, moved: string[]) {
  const tops = useRef(new Map<string, number>())
  useLayoutEffect(() => {
    const el = list.current
    if (!el) return
    const next = new Map<string, number>()
    for (const n of el.querySelectorAll<HTMLElement>('[data-name]')) next.set(n.dataset.name!, n.offsetTop)
    if (!window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      const ms = parseFloat(getComputedStyle(el).getPropertyValue('--t-3')) || 0
      for (const name of moved) {
        const dy = (tops.current.get(name) ?? NaN) - (next.get(name) ?? NaN)
        const row = el.querySelector<HTMLElement>(`[data-name="${CSS.escape(name)}"]`)
        if (row && dy) row.animate([{ transform: `translateY(${dy}px)` }, { transform: 'none' }], { duration: ms, easing: 'cubic-bezier(.16,1,.3,1)' })
      }
    }
    tops.current = next
  })
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
