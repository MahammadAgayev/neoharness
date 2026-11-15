import { useStore } from '../store'

// Echo is the note at the right of the focused window's status line: a
// confirmation nobody asked a key for, for 2 s. It never moves the name,
// the state or the time; a long one is cut.
export function Echo(props: { focused: boolean }) {
  const text = useStore((s) => s.echo)
  if (!props.focused || !text) return null
  return <span className="sl-echo">{text}</span>
}
