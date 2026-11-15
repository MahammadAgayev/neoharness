// Caret is the Insert cursor an editor draws itself, as the browser's caret
// has no width and blinks: a line of --caret-w in --accent, still. The
// editor gives its place in its scroller's content, so it scrolls with it.
export interface CaretAt {
  left: number
  top: number
  height: number
}

export function Caret(props: { at: CaretAt | null; className?: string }) {
  const { at } = props
  if (!at) return null
  return <div className={`caret ${props.className ?? ''}`} style={{ left: at.left, top: at.top, height: at.height }} />
}
