import { useRef, useState } from 'react'

import { store, useStore } from '../store'
import { Icon } from './Icon'

const minWidth = 160
const maxWidth = 400

const railWidth = 48

// Sidebar lists the workspaces and what in each needs you, so a thread that
// waits in another workspace is one look away. ⌘B folds it to a rail of
// icons, as Chrome's vertical tabs: the mouse on the rail shows it whole,
// over the windows, until the mouse leaves.
export function Sidebar(props: { folded: boolean }) {
  const workspaces = useStore((s) => s.workspaces)
  const open = useStore((s) => s.workspace)
  const screen = useStore((s) => s.screen)
  const artifacts = useStore((s) => s.artifacts)
  const leader = useStore((s) => s.settings.leader)
  const lead = leader === '<Space>' ? 'Space' : leader.replace(/^<|>$/g, '')
  const saved = useStore((s) => s.settings.sidebarWidth)
  // While a drag runs the width is local; it is saved when the drag ends.
  const [dragged, setDragged] = useState<number | null>(null)
  const width = dragged ?? saved
  const drag = (e: React.MouseEvent) => {
    e.preventDefault()
    const x0 = e.clientX
    let w = width
    const move = (ev: MouseEvent) => {
      w = Math.min(Math.max(width + ev.clientX - x0, minWidth), maxWidth)
      setDragged(w)
    }
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
      document.body.classList.remove('resizing')
      store.setSettings({ sidebarWidth: w })
      setDragged(null)
    }
    document.body.classList.add('resizing')
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }
  const [over, setOver] = useState(false)
  const wait = useRef<ReturnType<typeof setTimeout> | null>(null)
  // A short wait, so a mouse on its way to the title bar does not open it.
  const hover = (on: boolean) => {
    if (wait.current) clearTimeout(wait.current)
    wait.current = on ? setTimeout(() => setOver(true), 150) : null
    if (!on) setOver(false)
  }
  const nav = (
    <nav
      className={`sidebar ${props.folded ? (over ? 'peek' : 'rail') : ''}`}
      style={props.folded ? { width: over ? width : railWidth } : { flexBasis: width }}
      onMouseEnter={props.folded ? () => hover(true) : undefined}
      onMouseLeave={props.folded ? () => hover(false) : undefined}
    >
      {!props.folded && (
        <div
          className="sb-resizer"
          onMouseDown={drag}
          onDoubleClick={() => store.setSettings({ sidebarWidth: 216 })}
          data-tip="Drag to resize · double-click resets"
        />
      )}
      <div className="sb-section">
        {open && artifacts.length > 0 && (
          <button className="sb-item" onClick={() => store.openPicker('artifacts', true)}>
            <Icon name="diamond" className="sb-icon t-foam" />
            <span className="sb-label">Artifacts</span>
            <span className="sb-count">{artifacts.length}</span>
          </button>
        )}
        {open && (
          <button className="sb-item" onClick={() => store.openNewDoc()}>
            <Icon name="file-plus" className="sb-icon" />
            <span className="sb-label">New doc</span>
            <kbd className="sb-kbd shown">{lead} d</kbd>
          </button>
        )}
      </div>
      {/* The heading opens the overview of every workspace; the list marks the open one. */}
      <button
        className={`sb-head ${screen === 'workspaces' ? 'active' : ''}`}
        onClick={() => store.showWorkspaces()}
        data-tip="All workspaces"
        data-keys={`${lead} p v`}
      >
        Workspaces
      </button>
      <div className="sb-section sb-scroll">
        {workspaces.map((w, i) => {
          const waiting = (w.counts.waiting ?? 0) + (w.coordinator === 'waiting' ? 1 : 0)
          const running = (w.counts.running ?? 0) + (w.coordinator === 'running' ? 1 : 0)
          return (
            <button
              key={w.name}
              className={`sb-item ${w.name === open && screen === 'workspace' ? 'active' : ''}`}
              onClick={() => void store.openWorkspace(w.name)}
              data-tip={w.pin ? store.short(w.pin) : 'not pinned'}
            >
              <Icon
                name={waiting ? 'waiting' : running ? 'running' : 'idle'}
                className={`sb-icon ${waiting ? 't-gold' : running ? 't-foam pulse' : 't-muted'}`}
              />
              <span className="sb-label">{w.name}</span>
              {waiting > 0 ? (
                <span className="sb-count t-gold">{waiting}</span>
              ) : running > 0 ? (
                <span className="sb-count t-foam">{running}</span>
              ) : (
                i < 9 && <kbd className="sb-kbd">⌘{i + 1}</kbd>
              )}
            </button>
          )
        })}
        <button className="sb-item sb-new" onClick={() => store.openNewWorkspace()}>
          <Icon name="plus" className="sb-icon" />
          <span className="sb-label">New workspace</span>
        </button>
      </div>
      <div className="sb-foot">
        <button className="sb-item" onClick={() => store.openSpend('workspace')}>
          <Icon name="chart-column" className="sb-icon" />
          <span className="sb-label">Spend</span>
          <kbd className="sb-kbd shown">{lead} $</kbd>
        </button>
        <button className="sb-item" onClick={() => store.openSettings()}>
          <Icon name="settings" className="sb-icon" />
          <span className="sb-label">Settings</span>
          <kbd className="sb-kbd shown">{lead} s</kbd>
        </button>
        <button className="sb-item" onClick={() => void store.ex('help')}>
          <Icon name="keyboard" className="sb-icon" />
          <span className="sb-label">Help</span>
          <kbd className="sb-kbd shown">{lead} h</kbd>
        </button>
      </div>
    </nav>
  )
  return props.folded ? <div className="sb-slot">{nav}</div> : nav
}
