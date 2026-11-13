import { store, useStore } from '../store'
import { Icon } from './Icon'

// Sidebar lists the workspaces and what in each needs you, so a thread that
// waits in another workspace is one look away. ⌘B hides it for focus.
export function Sidebar() {
  const workspaces = useStore((s) => s.workspaces)
  const open = useStore((s) => s.workspace)
  const screen = useStore((s) => s.screen)
  const artifacts = useStore((s) => s.artifacts)
  const leader = useStore((s) => s.settings.leader)
  const lead = leader === '<Space>' ? 'Space' : leader.replace(/^<|>$/g, '')
  return (
    <nav className="sidebar">
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
            <kbd className="sb-kbd shown">{lead} D</kbd>
          </button>
        )}
      </div>
      {/* The heading opens the overview of every workspace; the list marks the open one. */}
      <button
        className={`sb-head ${screen === 'workspaces' ? 'active' : ''}`}
        onClick={() => store.showWorkspaces()}
        data-tip="All workspaces"
        data-keys={`${lead} P V`}
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
        <button className="sb-item" onClick={() => store.openSettings()}>
          <Icon name="settings" className="sb-icon" />
          <span className="sb-label">Settings</span>
          <kbd className="sb-kbd shown">{lead} S</kbd>
        </button>
        <button className="sb-item" onClick={() => void store.ex('help')}>
          <Icon name="keyboard" className="sb-icon" />
          <span className="sb-label">Keys and commands</span>
          <kbd className="sb-kbd shown">{lead} H</kbd>
        </button>
      </div>
    </nav>
  )
}
