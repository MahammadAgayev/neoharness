import { useRef, useState } from 'react'

import type { WorkspaceView } from '../../shared/model'
import { inState } from '../search'
import { store, useStore } from '../store'
import { Glyph, Icon } from './Icon'
import { Leave } from './Leave'

const minWidth = 160
const maxWidth = 320
const openWidth = 200

// The rail lists the workspaces and what in each needs you, so a thread that
// waits in another workspace is one look away. ⌘B folds it to 48 px of
// letters: the mouse resting on it shows it whole, over the windows, until
// the mouse leaves. Everything else is in the palette and the ⋯ menu.
export function Sidebar(props: { folded: boolean }) {
  const workspaces = useStore((s) => s.workspaces)
  const open = useStore((s) => s.workspace)
  const screen = useStore((s) => s.screen)
  const saved = useStore((s) => s.settings.sidebarWidth)
  // While a drag runs the width is local; it is saved when the drag ends.
  const [dragged, setDragged] = useState<number | null>(null)
  const width = Math.min(Math.max(dragged ?? saved, minWidth), maxWidth)
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
  // The peek waits for a mouse that rests, so one on its way to the bar
  // does not open it, and stays a moment after, so one that slips off the
  // edge does not close it.
  const hover = (on: boolean) => {
    if (wait.current) clearTimeout(wait.current)
    wait.current = setTimeout(() => setOver(on), on ? 300 : 150)
  }
  const item = (w: WorkspaceView, i: number, rail: boolean) => {
    const st = wsState(w)
    const tip = [w.pin ? store.short(w.pin) : 'not pinned', ...st.parts].join(' · ')
    return (
      <button
        key={w.name}
        className={`sb-item ${w.name === open && screen === 'workspace' ? 'active' : ''}`}
        onClick={() => void store.openWorkspace(w.name)}
        data-tip={rail ? `${w.name} · ${tip}` : tip}
        data-keys={i < 9 ? `⌘${i + 1}` : undefined}
      >
        {rail && <span className="sb-letter">{w.name.slice(0, 1).toUpperCase()}</span>}
        {!rail && <span className="sb-label">{w.name}</span>}
        {st.glyph && (
          <span className={`sb-state t-${st.tone} ${st.glyph === '●' ? 'pulse' : ''}`}>
            {rail ? <span className="sb-dot" /> : <Glyph g={st.glyph} size={12} />}
          </span>
        )}
      </button>
    )
  }
  // Folded, the rail keeps its width in the layout, and the whole sidebar
  // arrives over the windows: a slide, not a resize, so nothing reflows.
  const nav = (mode: '' | 'rail' | 'peek') => (
    <nav className={`sidebar ${mode}`} style={mode === '' ? { flexBasis: width } : mode === 'peek' ? { width } : undefined}>
      {mode === '' && (
        <div
          className="sb-resizer"
          onMouseDown={drag}
          onDoubleClick={() => store.setSettings({ sidebarWidth: openWidth })}
          data-tip="Drag to resize · double-click resets"
        />
      )}
      {/* The heading opens the overview of every workspace; the list marks the open one. */}
      <button className={`sb-head ${screen === 'workspaces' ? 'active' : ''}`} onClick={() => store.showWorkspaces()} data-tip="All workspaces">
        Workspaces
      </button>
      <div className="sb-section sb-scroll">
        {workspaces.map((w, i) => item(w, i, mode === 'rail'))}
        <button className="sb-item sb-new" onClick={() => store.openNewWorkspace()} data-tip="New workspace">
          <Icon name="plus" className="sb-icon" />
          {mode !== 'rail' && <span className="sb-label">New workspace</span>}
        </button>
      </div>
      <div className="sb-foot">
        <button className="sb-btn" onClick={() => store.openPicker('all')} data-tip="Search everything" data-keys="⌘K">
          ⌘K
        </button>
        <button className="sb-btn" onClick={() => void more()} data-tip="Artifacts, new doc, cost, settings, help">
          ⋯
        </button>
      </div>
    </nav>
  )
  if (!props.folded) return nav('')
  return (
    <div className="sb-slot" onMouseEnter={() => hover(true)} onMouseLeave={() => hover(false)}>
      {nav('rail')}
      <Leave>{over && nav('peek')}</Leave>
    </div>
  )
}

// wsState is what a workspace shows on the rail: what needs you first, then
// what failed, then what runs.
function wsState(w: WorkspaceView): { glyph: string; tone: string; parts: string[] } {
  const waiting = inState(w, 'waiting')
  const failed = inState(w, 'failed')
  const running = inState(w, 'running')
  const parts = [waiting && `${waiting} waiting`, failed && `${failed} failed`, running && `${running} running`].filter(
    (p): p is string => !!p,
  )
  if (waiting) return { glyph: '◆', tone: 'gold', parts }
  if (failed) return { glyph: '✗', tone: 'love', parts }
  if (running) return { glyph: '●', tone: 'foam', parts }
  return { glyph: '', tone: 'muted', parts }
}

// more is the ⋯ menu: what the rail held before, each one also a key and a
// palette command.
async function more(): Promise<void> {
  const ws = !!store.state.workspace
  const id = await window.nh.menu([
    ...(ws
      ? [
          { id: 'artifacts', label: 'Artifacts' },
          { id: 'newdoc', label: 'New doc' },
          { id: '-', label: '' },
        ]
      : []),
    { id: 'cost', label: 'Cost' },
    { id: 'settings', label: 'Settings' },
    { id: 'help', label: 'Help' },
  ])
  if (id === 'artifacts') store.openPicker('artifacts', true)
  else if (id === 'newdoc') store.openNewDoc()
  else if (id === 'cost') store.openCost('workspace')
  else if (id === 'settings') store.openSettings()
  else if (id === 'help') void store.ex('help')
}
