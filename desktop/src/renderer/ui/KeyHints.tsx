import { useLayoutEffect, useState } from 'react'

import { useStore } from '../store'

// KeyHints is which-key: after a key that starts other maps, what can follow
// shows at once, so it is there before the next key. It is a sheet at the
// foot of the window in focus, as wide as it, so no hint is cut.
export function KeyHints() {
  const hints = useStore((s) => s.hints)
  const pending = useStore((s) => s.pending)
  const on = useStore((s) => s.settings.keyhints)
  const leader = useStore((s) => s.settings.leader)
  const [at, setAt] = useState<React.CSSProperties>({})
  const shown = on && !!hints?.length
  useLayoutEffect(() => {
    if (!shown) return
    const r = (document.querySelector('.pane.focused') ?? document.querySelector('.main'))?.getBoundingClientRect()
    // Up to three columns, as many as fit at about 200px each.
    if (r) setAt({ left: r.left, width: r.width, bottom: window.innerHeight - r.bottom, ['--cols' as string]: Math.max(1, Math.min(3, Math.floor(r.width / 200))) })
  }, [shown, hints])
  if (!shown) return null
  return (
    <div className="keyhints" style={at}>
      <div className="keyhints-head mono">{pending.startsWith(leader) ? '<leader>' + pending.slice(leader.length) : pending}</div>
      <div className="keyhints-grid">
        {hints!.map((h) => (
          <div key={h.key + h.label} className="keyhint">
            <span className="keyhint-key mono">{h.key}</span>
            <span className={h.label.startsWith('+') ? 't-subtle' : ''}>{h.label}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
