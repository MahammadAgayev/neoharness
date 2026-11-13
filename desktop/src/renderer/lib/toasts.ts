// Toast rules, as tui/toast.go: which change of a thread is news,
// and how long it stays. A toast with life 0 stays while its thread waits
// and is not on screen.

import { Coordinator, location, type Event, type ThreadView, type WorkspaceView } from '../../shared/model'

export type Tone = 'iris' | 'gold' | 'muted' | 'pine' | 'love' | 'foam'

export interface Toast {
  id: number
  workspace: string
  thread: string
  glyph: string
  tone: Tone
  body: string
  // until is when it goes; 0 keeps it while the thread waits for the person.
  until: number
  notice?: boolean
  at: number
  // needsYou says the person must act: it also goes to macOS.
  needsYou?: boolean
}

export type ToastSpec = Omit<Toast, 'id' | 'until' | 'at'> & { life: number }

const s = 1000

export function plain(text: string): string {
  return text
    .replace(/[`*_#>]/g, '')
    .replace(/\s+/g, ' ')
    .trim()
}

function shortHome(p: string, home: string): string {
  return home && p.startsWith(home) ? '~' + p.slice(home.length) : p
}

// viewToast turns a thread's change into a toast. Of a quiet thread only a
// question or a permission request does: the coordinator tells the rest.
// reply is the first line of its newest reply, for a turn that ended.
export function viewToast(
  workspace: string,
  old: ThreadView | undefined,
  v: ThreadView,
  opts: { reply: string; away: boolean; home: string },
): ToastSpec | null {
  const t = (glyph: string, tone: Tone, body: string, life: number, needsYou = false): ToastSpec => ({
    workspace,
    thread: v.name,
    glyph,
    tone,
    body,
    life,
    needsYou,
  })
  if (v.quiet && v.state !== 'waiting') return null
  if (!old && v.role === 'thread') return t('+', 'iris', `opened in ${shortHome(location(v.location), opts.home)}`, 4 * s)
  if (v.state === 'waiting' && old?.state !== 'waiting') {
    let body = `asks: ${plain(v.waiting ?? '')}`
    if (v.permission) body = `needs permission · ${v.permission.tool}`
    else if (v.waiting === 'paused') body = 'paused · write to continue'
    return t('◆', 'gold', body, 0, true)
  }
  if (v.state === 'resolved' && old?.state !== 'resolved') return t('✓', 'muted', 'resolved', 4 * s)
  if (old?.state === 'running' && v.state === 'idle') {
    // You are less likely to be looking at a workspace that is not open.
    const body = plain(opts.reply) || (opts.away ? plain(v.progress ?? '') : '') || 'finished a turn'
    return t('✓', 'pine', body, opts.away ? 10 * s : 4 * s)
  }
  return null
}

export function noticeToast(workspace: string, e: Event): ToastSpec {
  return e.needsYou
    ? { workspace, thread: Coordinator, glyph: '↗', tone: 'gold', body: e.text ?? '', life: 0, notice: true, needsYou: true }
    : { workspace, thread: Coordinator, glyph: '↗', tone: 'foam', body: e.text ?? '', life: 8 * s, notice: true }
}

export function errorToast(workspace: string, thread: string, e: Event): ToastSpec {
  return { workspace, thread, glyph: '✗', tone: 'love', body: e.text ?? '', life: 8 * s, needsYou: true }
}

// keep reports whether a toast still shows. onScreen says whether its
// thread is in a window now; for another workspace only its summary is known.
export function keep(
  t: Toast,
  now: number,
  ctx: {
    open: string
    onScreen: (thread: string) => boolean
    threads: Record<string, ThreadView>
    workspaces: WorkspaceView[]
  },
): boolean {
  if (t.until) return now < t.until
  if (t.notice) return t.workspace !== ctx.open || !ctx.onScreen(t.thread)
  if (t.workspace === ctx.open) return ctx.threads[t.thread]?.state === 'waiting' && !ctx.onScreen(t.thread)
  const w = ctx.workspaces.find((p) => p.name === t.workspace)
  return !!w && ((w.counts.waiting ?? 0) > 0 || w.coordinator === 'waiting')
}
