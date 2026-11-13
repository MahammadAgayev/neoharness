import { store, useStore } from '../store'
import { Icon } from './Icon'

// CloseWindow is the × at the end of a window's status line: ⌘W, :q and
// <C-w>c are keys you must know, the × shows itself on hover.
export function CloseWindow(props: { id: number }) {
  const many = useStore((s) => s.layout.cols.reduce((n, c) => n + c.wins.length, 0) > 1)
  if (!many) return null
  return (
    <button
      className="sl-close"
      aria-label="Close the window"
      data-tip="Close"
      data-keys="⌘W"
      onMouseDown={(e) => e.stopPropagation()}
      onClick={() => store.close(props.id)}
    >
      <Icon name="x" size={13} />
    </button>
  )
}
