// The rows of the summary card, as data: the store moves its cursor and
// opens a row, and the card draws them. The TUI lays out the same rows.

import { Coordinator, type Recap, type RecapItem, type ThreadView, type WorkspaceView } from '../../shared/model'

// Part is a piece of a row in a tone: text, subtle, muted, or a state color.
export interface Part {
  text: string
  tone: string
}

// CardRow is one row: a mark or a label before its parts, and what <CR>
// on it opens. A gap is an empty row between threads.
export interface CardRow {
  mark?: Part
  label?: Part
  parts: Part[]
  open?: { thread?: string; artifact?: string }
  gap?: boolean
}

export const rowText = (r: CardRow) => r.parts.map((p) => p.text).join('')

// rank orders the threads of the workspace's card: what needs the person,
// what failed, what came out that they have not seen, what still runs, and
// the rest.
export function rank(v: ThreadView): number {
  if (v.state === 'waiting') return 0
  if (v.recap?.error) return 1
  if (v.state === 'idle' && v.recapUnseen) return 2
  if (v.state === 'running') return 3
  return 4
}

// tone is the color of a thread's state: gold waits for the person, love
// failed, foam runs, pine came out.
export function tone(v: ThreadView): string {
  if (v.state === 'waiting') return 'gold'
  if (v.recap?.error) return 'love'
  if (v.state === 'running') return 'foam'
  return v.recap ? 'pine' : 'muted'
}

export const mark = (v: ThreadView) => (v.state === 'waiting' ? '◆' : v.recap?.error ? '✗' : v.state === 'running' ? '●' : v.recap ? '✓' : '○')

// itemParts are an item and what holds it, after sep.
function itemParts(it: RecapItem, sep: string): Part[] {
  const parts: Part[] = [{ text: it.text, tone: 'text' }]
  if (it.thread) parts.push({ text: `${sep}→ ${it.thread}`, tone: 'muted' })
  else if (it.artifact) parts.push({ text: `${sep}◇ `, tone: 'foam' }, { text: it.artifact, tone: 'subtle' })
  return parts
}

// threadPart is a thread in plain words: what it is for, then what needs
// the person, what failed, is done and in progress. In the workspace's card
// a heading names it, each list takes one row, and every row opens the
// thread; in its own card each item takes a row that opens what holds it.
export function threadPart(v: ThreadView, own: boolean): CardRow[] {
  const r: Recap = v.recap ?? { at: '' }
  const out: CardRow[] = []
  const goal = r.goal || r.asked || ''
  const open = own ? undefined : { thread: v.name }
  if (own) {
    if (goal) out.push({ parts: [{ text: goal, tone: 'subtle' }] })
  } else {
    out.push({ mark: { text: mark(v), tone: tone(v) }, parts: [{ text: v.name, tone: 'text' }, ...(goal ? [{ text: `  ${goal}`, tone: 'subtle' }] : [])], open })
  }
  let done = r.done ?? []
  const doing = r.doing ?? []
  let needs = r.needs ?? []
  // Until the thread writes its summary, its last turn stands in.
  if (done.length + doing.length + needs.length === 0) {
    done = r.outcome ? [{ text: r.outcome }] : []
    needs = r.open ? [{ text: r.open }] : []
  }
  if (v.state === 'waiting') {
    const wait = v.permission ? `permission · ${v.permission.tool} ${v.permission.detail}` : v.waiting === 'paused' ? 'paused' : v.waiting ? `asks: ${v.waiting}` : ''
    if (wait) needs = [{ text: wait }, ...needs]
  }
  const list = (label: string, t: string, items: RecapItem[]) => {
    if (!items.length) return
    if (!own) {
      const parts = items.flatMap((it, i) => [...(i ? [{ text: ' · ', tone: 'muted' }] : []), ...itemParts(it, ' ')])
      out.push({ label: { text: label, tone: t }, parts, open })
      return
    }
    items.forEach((it, i) =>
      out.push({
        label: i === 0 ? { text: label, tone: t } : { text: '', tone: t },
        parts: itemParts(it, '  '),
        open: it.thread ? { thread: it.thread } : it.artifact ? { artifact: it.artifact } : undefined,
      }),
    )
  }
  list('needs you', 'gold', needs)
  if (r.error) list('failed', 'love', [{ text: r.error }])
  list('done', 'pine', done)
  list('in progress', 'foam', doing)
  return out
}

const hhmm = (at: string) => new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })

// cardRows are the rows of the card of a subject: the workspace's holds the
// counts and the coordinator's view, then each open thread and each
// resolved one in news, the threads that had news when the card opened;
// a thread's holds only its own part.
export function cardRows(subject: string, w: WorkspaceView | undefined, threads: Record<string, ThreadView>, order: string[], news: string[]): CardRow[] {
  if (subject !== Coordinator) return threads[subject] ? threadPart(threads[subject], true) : []
  const out: CardRow[] = []
  const r = w?.recap
  const waits = w?.counts?.waiting ?? 0
  const failed = r?.failed ?? 0
  const counts: Part[] = []
  if (waits) counts.push({ text: `◆ ${waits}`, tone: 'gold' }, { text: waits === 1 ? ' waits for you' : ' wait for you', tone: 'muted' })
  if (failed) counts.push(...(counts.length ? [{ text: ' · ', tone: 'muted' }] : []), { text: `✗ ${failed}`, tone: 'love' }, { text: ' failed', tone: 'muted' })
  if (counts.length) out.push({ parts: counts })
  for (const l of r?.lines ?? []) out.push({ parts: [{ text: l, tone: 'text' }] })
  const shown = order
    .filter((n) => n !== Coordinator && !n.includes('/') && threads[n] && (threads[n].state !== 'resolved' || news.includes(n)))
    .map((n) => threads[n])
    .sort((a, b) => rank(a) - rank(b))
  for (const v of shown) out.push({ parts: [], gap: true }, ...threadPart(v, false))
  const names = (ts: string[]) => (ts.length > 2 ? `${ts.slice(0, -1).join(', ')} and ${ts[ts.length - 1]} all` : `${ts[0]} and ${ts[1]} both`)
  if (r?.overlaps?.length) out.push({ parts: [], gap: true })
  for (const o of r?.overlaps ?? []) out.push({ parts: [{ text: `${names(o.threads)} edited `, tone: 'subtle' }, { text: o.path, tone: 'text' }] })
  const newer = r?.newer ?? 0
  if (r?.at && newer > 0) out.push({ parts: [{ text: `as of ${hhmm(r.at)} · ${newer === 1 ? '1 thread' : `${newer} threads`} ended since`, tone: 'muted' }] })
  return out
}
