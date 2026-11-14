import type { ThreadView } from '../../shared/model'

// A passed time says nothing: the hub clears it when the wake-up comes.
export function wakeLabel(v: ThreadView, now = Date.now()): string {
  if (v.state !== 'idle' || !v.wakeAt || Date.parse(v.wakeAt) <= now) return ''
  return `wakes at ${new Date(v.wakeAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
}
