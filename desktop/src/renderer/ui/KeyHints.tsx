import { useStore } from '../store'

// KeyHints is which-key: after a key that starts other maps, what can follow
// shows at once, so it is there before the next key.
export function KeyHints() {
  const hints = useStore((s) => s.hints)
  const pending = useStore((s) => s.pending)
  const on = useStore((s) => s.settings.keyhints)
  const leader = useStore((s) => s.settings.leader)
  if (!on || !hints?.length) return null
  return (
    <div className="keyhints">
      <div className="keyhints-head mono">{pending.startsWith(leader) ? '<leader>' + pending.slice(leader.length) : pending}</div>
      <div className="keyhints-grid">
        {hints.map((h) => (
          <div key={h.key + h.label} className="keyhint">
            <span className="keyhint-key mono">{h.key}</span>
            <span className={h.label.startsWith('+') ? 't-iris' : ''}>{h.label}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
