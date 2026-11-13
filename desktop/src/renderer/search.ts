// The items of search everywhere, per tab. "all" mixes the tabs, best
// matches first, as JetBrains' Search Everywhere does. ⌘K opens it.

import { Coordinator, type ThreadView } from '../shared/model'
import { commands } from './commands'
import { fuzzy, grep } from './lib/fuzzy'
import { outline } from './lib/markdown'
import { blockText } from './lib/transcript'
import type { Tone } from './lib/toasts'
import { store, type BufRef, type Tab } from './store'

export interface Item {
  key: string
  section: string
  glyph: string
  tone: Tone | 'subtle' | 'text' | 'rose'
  label: string
  detail?: string
  hint?: string
  // open names what Enter does when it is more than open, as a switch of
  // workspace.
  open?: string
  run: () => void
  split?: (vertical: boolean) => void
  del?: () => void
}

export const tabs: { tab: Tab; title: string }[] = [
  { tab: 'all', title: 'All' },
  { tab: 'threads', title: 'Threads' },
  { tab: 'artifacts', title: 'Artifacts' },
  { tab: 'text', title: 'Text' },
  { tab: 'commands', title: 'Commands' },
  { tab: 'workspaces', title: 'Workspaces' },
  { tab: 'notifications', title: 'Notifications' },
  { tab: 'buffers', title: 'Buffers' },
  { tab: 'outline', title: 'Outline' },
]

export function stateGlyph(s: string): string {
  return s === 'running' ? '●' : s === 'waiting' ? '◆' : s === 'resolved' ? '✓' : '○'
}

export function stateTone(s: string): Tone | 'subtle' {
  return s === 'running' ? 'foam' : s === 'waiting' ? 'gold' : s === 'resolved' ? 'pine' : 'muted'
}

export function elapsed(since: string): string {
  const ms = Date.now() - Date.parse(since)
  if (!(ms > 0)) return ''
  const m = Math.floor(ms / 60000)
  if (m < 1) return `${Math.floor(ms / 1000)}s`
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  return h < 24 ? `${h}h${m % 60 ? ` ${m % 60}m` : ''}` : `${Math.floor(h / 24)}d`
}

export function threadDetail(v: ThreadView): string {
  let d = v.state
  if (v.permission) d += ` · permission · ${v.permission.tool}`
  else if (v.waiting === 'paused') d += ' · paused'
  else if (v.state === 'waiting') d += ' · question'
  if ((v.state === 'running' || v.state === 'waiting') && v.since) d += ` · ${elapsed(v.since)}`
  return d
}

function threadItems(local: boolean): Item[] {
  const s = store.state
  const items: Item[] = []
  for (const n of [Coordinator, ...store.listOrder()]) {
    const v = s.threads[n]
    // Every workspace's list shows what is open; this one's, all of it.
    if (!v || (!local && v.state === 'resolved')) continue
    items.push({
      key: `t:${s.workspace}:${n}`,
      section: 'Threads',
      glyph: stateGlyph(v.state),
      tone: stateTone(v.state),
      label: local ? n : `${s.workspace} · ${n}`,
      detail: threadDetail(v),
      // A thread you open is one you mean to write to: the caret goes to
      // its prompt.
      run: () => {
        store.showThread(n)
        store.focusPrompt()
      },
      split: (vert) => {
        store.split(vert, { kind: 'chat', thread: n })
        store.focusPrompt()
      },
      del: () => store.askDeleteThread(n),
    })
  }
  if (local) return items
  for (const { workspace, view: v } of s.picker?.remote ?? []) {
    if (v.state === 'resolved') continue
    items.push({
      key: `t:${workspace}:${v.name}`,
      section: 'Threads',
      glyph: stateGlyph(v.state),
      tone: stateTone(v.state),
      label: `${workspace} · ${v.name}`,
      detail: threadDetail(v),
      open: `go to ${workspace}`,
      run: () => void store.goThread(workspace, v.name).then(() => store.focusPrompt()),
    })
  }
  return items
}

function artifactItems(): Item[] {
  return store.state.artifacts.map((a) => ({
    key: `a:${a.name}`,
    section: 'Artifacts',
    glyph: '◇',
    tone: 'foam' as const,
    label: a.name,
    detail: `${a.title} · ${a.thread} · v${a.version}`,
    run: () => void store.openArtifact(a.name),
    split: (vert: boolean) => {
      store.split(vert)
      void store.openArtifact(a.name)
    },
  }))
}

function commandItems(): Item[] {
  return commands.map((c) => ({
    key: `c:${c.id}`,
    section: 'Commands',
    glyph: '›',
    tone: 'iris' as const,
    label: c.title,
    hint: c.keys.find((k) => /^[⌘⇧⌃]/.test(k)) ?? c.keys[0],
    detail: c.keys.filter((k) => !/^[⌘⇧⌃]/.test(k)).join('  '),
    run: c.run,
  }))
}

function workspaceItems(): Item[] {
  return store.state.workspaces.map((w) => {
    const waiting = (w.counts.waiting ?? 0) + (w.coordinator === 'waiting' ? 1 : 0)
    const running = (w.counts.running ?? 0) + (w.coordinator === 'running' ? 1 : 0)
    const parts = [w.pin ? store.short(w.pin) : 'not pinned']
    if (running) parts.push(`${running} running`)
    if (waiting) parts.push(`${waiting} waiting`)
    return {
      key: `w:${w.name}`,
      section: 'Workspaces',
      glyph: waiting ? '◆' : running ? '●' : '▪',
      tone: waiting ? ('gold' as const) : running ? ('foam' as const) : ('muted' as const),
      label: w.name,
      detail: parts.join(' · '),
      run: () => void store.openWorkspace(w.name),
      del: () => store.askDeleteWorkspace(w.name),
    }
  })
}

function notificationItems(): Item[] {
  return store.state.log
    .slice()
    .reverse()
    .map((t) => ({
      key: `n:${t.id}`,
      section: 'Notifications',
      glyph: t.glyph,
      tone: t.tone,
      label: `${t.workspace} · ${t.thread}`,
      detail: t.body,
      hint: new Date(t.at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
      open: t.workspace === store.state.workspace ? undefined : `go to ${t.workspace}`,
      run: () => void store.goThread(t.workspace, t.thread),
    }))
}

function bufferItems(): Item[] {
  const s = store.state
  const show = (buf: BufRef) => () => {
    const w = store.cur()
    if (w) store.show(w.id, buf)
  }
  const items = s.workspace ? [...threadItems(true), ...artifactItems()] : []
  for (const [label, buf] of [
    ['threads', { kind: 'threads' }],
    ['messages', { kind: 'messages' }],
    ['help', { kind: 'help' }],
  ] as [string, BufRef][]) {
    if (label === 'threads' && !s.workspace) continue
    items.push({
      key: `b:${label}`,
      section: 'Buffers',
      glyph: '≡',
      tone: 'muted',
      label,
      run: () => {
        show(buf)()
        if (buf.kind === 'chat') store.focusPrompt()
      },
      split: (vert) => {
        store.split(vert, buf)
        if (buf.kind === 'chat') store.focusPrompt()
      },
    })
  }
  return items.map((it) => ({ ...it, section: 'Buffers' }))
}

// textItems search every transcript of the workspace as the query is typed.
function textItems(q: string): Item[] {
  const s = store.state
  if (q.trim().length < 2) return []
  const items: Item[] = []
  for (const n of [Coordinator, ...store.listOrder()]) {
    const bs = s.chats[n] ?? []
    bs.forEach((b, bi) => {
      for (const m of grep(blockText(b), q)) {
        const v = s.threads[n]
        items.push({
          key: `x:${n}:${b.key}:${m.line}`,
          section: 'Text',
          glyph: stateGlyph(v?.state ?? 'idle'),
          tone: stateTone(v?.state ?? 'idle'),
          label: n,
          detail: m.snippet,
          run: () => {
            store.showThread(n)
            const w = store.cur()
            if (w) store.updateWin(w.id, { cursor: bi, follow: false })
          },
        })
        if (items.length >= 200) return
      }
    })
    if (items.length >= 200) break
  }
  return items
}

function outlineItems(): Item[] {
  const s = store.state
  const w = store.cur()
  if (!w) return []
  if (w.buf.kind === 'artifact') {
    const d = s.docs[w.buf.name]
    if (!d) return []
    return outline(d.edit?.text ?? d.text).map((h, index) => ({
      key: `o:${h.line}`,
      section: 'Outline',
      glyph: '#'.repeat(h.level),
      tone: 'iris' as const,
      label: h.title,
      run: () => document.dispatchEvent(new CustomEvent('nh:heading', { detail: { win: w.id, line: h.line, index } })),
    }))
  }
  if (w.buf.kind !== 'chat') return []
  const bs = s.chats[w.buf.thread] ?? []
  const items: Item[] = []
  bs.forEach((b, i) => {
    if (b.kind !== 'message' && b.kind !== 'text' && b.kind !== 'ask') return
    const label = b.text.trim().split('\n')[0] ?? ''
    items.push({
      key: `o:${b.key}`,
      section: 'Outline',
      glyph: b.kind === 'message' ? '▎' : b.kind === 'ask' ? '?' : '·',
      tone: b.kind === 'message' ? (b.from === 'user' ? 'iris' : 'rose') : b.kind === 'ask' ? 'gold' : 'subtle',
      label,
      detail: b.kind === 'message' && b.from !== 'user' ? `from ${b.from}` : undefined,
      run: () => store.updateWin(w.id, { cursor: i, follow: false }),
    })
  })
  return items
}

// needYouFirst puts the threads that wait first while nothing is typed:
// those are the ones you open search for.
function needYouFirst(items: Item[], q: string): Item[] {
  if (q) return items
  const rank = (it: Item) => (it.glyph === '◆' ? 0 : it.glyph === '●' ? 1 : 2)
  return items.slice().sort((a, b) => rank(a) - rank(b))
}

const perTabLimit = 8

export function items(tab: Tab, q: string): Item[] {
  switch (tab) {
    case 'threads':
      return fuzzy(needYouFirst(threadItems(!!store.state.picker?.local), q), q)
    case 'artifacts':
      return fuzzy(artifactItems(), q)
    case 'text':
      return textItems(q)
    case 'commands':
      return fuzzy(commandItems(), q)
    case 'workspaces':
      return fuzzy(workspaceItems(), q)
    case 'notifications':
      return fuzzy(notificationItems(), q)
    case 'buffers':
      return fuzzy(bufferItems(), q)
    case 'outline':
      return fuzzy(outlineItems(), q)
    case 'all': {
      // With no query: what you most likely want, the threads that need you
      // and the commands. With one: the best of every tab, in groups.
      const groups = [
        fuzzy(needYouFirst(threadItems(false), q), q),
        fuzzy(artifactItems(), q),
        fuzzy(workspaceItems(), q),
        fuzzy(commandItems(), q),
        textItems(q),
      ]
      return groups.flatMap((g) => g.slice(0, q ? perTabLimit : g === groups[3] ? 6 : perTabLimit))
    }
  }
}
