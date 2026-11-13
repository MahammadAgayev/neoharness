// The app's state and every action on it. Components read it with useStore
// and call the actions; only actions talk to the hub. The hub holds the
// truth: this is a view of it, rebuilt from the API on every connect.

import { useSyncExternalStore } from 'react'

import type { CallArgs, CallName, HubStatus, Result } from '../shared/api'
import {
  Coordinator,
  User,
  validName,
  type Artifact,
  type Decision,
  type Event,
  type ThreadView,
  type Update,
  type WorkspaceView,
} from '../shared/model'
import { parseEx } from './lib/ex'
import { hunks, lines, take } from './lib/linediff'
import { schemeNames } from './lib/themes'
import { errorToast, keep, noticeToast, viewToast, type Toast, type ToastSpec, type Tone } from './lib/toasts'
import { addEvent, fromEvents, lastText, lastUserMessage, settle, type Block } from './lib/transcript'
import { typeahead } from './ui/registry'

// historyTail is how much of each transcript loads, as in the TUI.
const historyTail = 2000
const toastsShown = 3

export type MsgTone = Tone | 'text' | 'subtle'

export type BufRef =
  | { kind: 'chat'; thread: string }
  | { kind: 'threads' }
  | { kind: 'artifact'; name: string }
  | { kind: 'help' }
  | { kind: 'messages' }

export interface Win {
  id: number
  buf: BufRef
  // back is where <C-o> goes: the buffers the window showed before.
  back: BufRef[]
  // cursor is the block of a chat, or the row of a list.
  cursor: number
  // follow keeps a chat at its end as it grows.
  follow: boolean
}

export interface Column {
  id: number
  wins: Win[]
}

export interface Layout {
  cols: Column[]
  focus: number
}

export interface Doc {
  artifact: Artifact
  text: string
  // edit is the source while the person changes it; base is the version it
  // started from.
  edit: { text: string; base: number; baseText: string } | null
  // conflict says the last :w was refused: a newer version changed the
  // same lines.
  conflict?: boolean
}

export type Tab = 'all' | 'threads' | 'artifacts' | 'text' | 'commands' | 'workspaces' | 'notifications' | 'buffers' | 'outline'

export interface Picker {
  tab: Tab
  query: string
  index: number
  // only names one tab: the tab bar does not show, as <leader>pa picks
  // only artifacts.
  only: boolean
  // remote holds the threads of other workspaces once they load.
  remote: { workspace: string; view: ThreadView }[]
  // local keeps the threads tab to this workspace, resolved ones too, as
  // <leader>pl does.
  local?: boolean
}

// A UserMap runs a command, or an ex command when run starts with ":", on
// its keys. An empty run turns the keys off.
export interface UserMap {
  keys: string
  run: string
}

// Hint is a row of the key hints: a key and what it does next.
export interface Hint {
  key: string
  label: string
}

export interface Settings {
  scheme: string
  vim: boolean
  fontSize: number
  compactwarn: number
  sidebar: boolean
  leader: string
  maps: UserMap[]
  keyhints: boolean
}

export interface State {
  status: HubStatus
  statusDetail: string
  workspaces: WorkspaceView[]
  screen: 'workspaces' | 'workspace'
  workspace: string
  threads: Record<string, ThreadView>
  order: string[]
  chats: Record<string, Block[]>
  artifacts: Artifact[]
  docs: Record<string, Doc>
  layout: Layout
  toasts: Toast[]
  log: Toast[]
  message: { text: string; tone: MsgTone } | null
  confirm: { text: string; run: () => void } | null
  picker: Picker | null
  cmdline: string | null
  // insert is set while an editor takes the keys.
  insert: boolean
  settings: Settings
  settingsOpen: boolean
  newWorkspace: boolean
  hints: Hint[] | null
  showResolved: boolean
  folds: Record<number, boolean>
  // workspaceCursor is the row of the workspaces screen.
  workspaceCursor: number
  pending: string
}

let ids = 1
const nextId = () => ids++

function loadSettings(): Settings {
  const d: Settings = { scheme: 'system', vim: true, fontSize: 14, compactwarn: 80, sidebar: true, leader: '<Space>', maps: [], keyhints: true }
  try {
    return { ...d, ...JSON.parse(localStorage.getItem('settings') || '{}') }
  } catch {
    return d
  }
}

function errText(err: unknown): string {
  const m = err instanceof Error ? err.message : String(err)
  // ipcRenderer.invoke wraps the main process's error in its own words.
  return m.replace(/^Error invoking remote method '[^']+': (Error: )?/, '')
}

const nh = () => window.nh

class Store {
  state: State = {
    status: 'connecting',
    statusDetail: '',
    workspaces: [],
    screen: 'workspaces',
    workspace: '',
    threads: {},
    order: [],
    chats: {},
    artifacts: [],
    docs: {},
    layout: { cols: [], focus: 0 },
    toasts: [],
    log: [],
    message: null,
    confirm: null,
    picker: null,
    cmdline: null,
    insert: false,
    settings: loadSettings(),
    settingsOpen: false,
    newWorkspace: false,
    hints: null,
    showResolved: false,
    folds: {},
    workspaceCursor: 0,
    pending: '',
  }
  private listeners = new Set<() => void>()
  private layouts: Record<string, Layout> = {}
  // away are the threads of workspaces that are not open, as last seen, so
  // their changes still toast.
  private away: Record<string, ThreadView> = {}
  private awayText: Record<string, string> = {}
  private connected = false
  // opening are threads the person opens here: they see them open, so the
  // "opened" toast would only cover the screen.
  private opening = new Set<string>()
  // held are the updates of the workspace that loads: they come after the
  // history it fetches, so they go on top of it, not under it.
  private held: Update[] | null = null
  // loaded are the last events of each history as it loaded.
  private loaded: Record<string, Event[]> = {}

  subscribe = (fn: () => void) => {
    this.listeners.add(fn)
    return () => this.listeners.delete(fn)
  }

  private set(patch: Partial<State>): void {
    this.state = { ...this.state, ...patch }
    for (const fn of this.listeners) fn()
  }

  private async call<N extends CallName>(name: N, args: CallArgs<N>): Promise<Result<N>> {
    return nh().call(name, args)
  }

  // try runs a hub call and shows its error, as the TUI's notify in Love.
  private async try<T>(p: Promise<T>): Promise<T | undefined> {
    try {
      return await p
    } catch (err) {
      this.say(errText(err), 'love')
      return undefined
    }
  }

  say(text: string, tone: MsgTone = 'text'): void {
    this.set({ message: { text, tone } })
  }

  // --- connection ---

  start(): void {
    nh().onStatus((s, detail) => {
      this.set({ status: s, statusDetail: detail })
      if (s === 'connected') void this.reload()
      if (s !== 'connected') this.connected = false
    })
    nh().onUpdate((u) => this.apply(u))
    nh().onOpenTarget((t) => void this.goThread(t.workspace, t.thread))
    nh().ready()
    setInterval(() => this.prune(), 1000)
  }

  private async reload(): Promise<void> {
    const first = !this.connected && this.state.workspaces.length === 0
    this.connected = true
    await this.loadWorkspaces()
    if (this.state.workspace) {
      await this.resync()
      return
    }
    const last = localStorage.getItem('workspace')
    if (first && last && this.state.workspaces.some((w) => w.name === last)) await this.openWorkspace(last)
  }

  async loadWorkspaces(): Promise<void> {
    const ws = await this.try(this.call('workspaces', undefined))
    if (ws) {
      this.set({ workspaces: ws })
      this.badge()
    }
  }

  // resync loads the open workspace again: its threads, their transcripts
  // and its artifacts.
  private async resync(): Promise<void> {
    const name = this.state.workspace
    this.held = []
    try {
      await this.load(name)
    } finally {
      const held = this.held ?? []
      this.held = null
      for (const u of held) if (u.workspace === this.state.workspace) this.apply(u, true)
    }
  }

  private async load(name: string): Promise<void> {
    const vs = await this.try(this.call('threads', { workspace: name }))
    if (!vs || name !== this.state.workspace) return
    const threads: Record<string, ThreadView> = {}
    for (const v of vs) threads[v.name] = v
    const order = vs.map((v) => v.name)
    const evs = await Promise.all(
      vs.map((v) => this.call('events', { workspace: name, thread: v.name, tail: historyTail }).catch(() => [] as Event[])),
    )
    const chats: Record<string, Block[]> = {}
    this.loaded = {}
    vs.forEach((v, i) => {
      chats[v.name] = fromEvents(evs[i], v.state === 'running')
      this.loaded[v.name] = evs[i].slice(-50)
    })
    const artifacts = (await this.call('artifacts', { workspace: name }).catch(() => [])) ?? []
    if (name !== this.state.workspace) return
    this.set({ threads, order, chats, artifacts })
    for (const [n, d] of Object.entries(this.state.docs)) if (!d.edit) void this.loadDoc(n)
  }

  // --- updates ---

  private apply(u: Update, replay = false): void {
    if (this.held && u.workspace === this.state.workspace && u.kind !== 'workspace') {
      this.held.push(u)
      return
    }
    const s = this.state
    switch (u.kind) {
      case 'workspace': {
        if (!u.summary) return
        const i = s.workspaces.findIndex((w) => w.name === u.workspace)
        const workspaces = s.workspaces.slice()
        if (i < 0) workspaces.push(u.summary)
        else workspaces[i] = u.summary
        this.set({ workspaces })
        this.badge()
        return
      }
      case 'thread':
        if (!u.view) return
        if (u.workspace === s.workspace) this.onView(u.view)
        else this.awayView(u.workspace, u.view)
        return
      case 'event': {
        const e = u.event
        if (!e) return
        if (e.kind === 'notice') this.toast(noticeToast(u.workspace, e))
        if (e.kind === 'error') this.toast(errorToast(u.workspace, u.thread ?? '', e))
        if (u.workspace === s.workspace && u.thread) {
          // A held event may be in the history that loaded with it already.
          if (replay && this.loaded[u.thread]?.some((x) => JSON.stringify(x) === JSON.stringify(e))) return
          this.set({ chats: { ...s.chats, [u.thread]: addEvent(s.chats[u.thread] ?? [], e) } })
        } else if (e.kind === 'text' && u.thread) {
          this.awayText[`${u.workspace}\0${u.thread}`] = (e.text ?? '').trim().split('\n')[0] ?? ''
        }
        return
      }
      case 'artifact': {
        const a = u.artifact
        if (u.workspace !== s.workspace || !a) return
        this.set({ artifacts: [a, ...s.artifacts.filter((x) => x.name !== a.name)] })
        const d = this.state.docs[a.name]
        if (!d) return
        if (d.edit && a.version !== d.edit.base) {
          this.say(`${a.thread} wrote v${a.version} while you edit · :w merges it, :e! takes it`, 'gold')
        }
        void this.loadDoc(a.name)
        return
      }
      case 'deleted':
        if (!u.thread) this.dropWorkspace(u.workspace)
        else if (u.workspace === s.workspace) this.dropThread(u.thread)
        return
    }
  }

  private onView(v: ThreadView): void {
    const s = this.state
    const old = s.threads[v.name]
    const threads = { ...s.threads, [v.name]: v }
    const order = old ? s.order : [...s.order, v.name]
    let chat = s.chats[v.name] ?? []
    // A stopped turn sends no turn end, so its tool calls would spin forever.
    if (v.state === 'idle' || v.state === 'resolved') chat = settle(chat, false)
    this.set({ threads, order, chats: { ...s.chats, [v.name]: chat } })
    const spec = viewToast(s.workspace, old, v, { reply: lastText(chat), away: false, home: nh().home() })
    if (!old && this.opening.delete(v.name)) return
    if (spec) this.toast(spec)
  }

  private awayView(workspace: string, v: ThreadView): void {
    const key = `${workspace}\0${v.name}`
    const old = this.away[key]
    this.away[key] = v
    // Its first view since the start is news only when it waits.
    if (!old && v.state !== 'waiting') return
    const reply = this.awayText[key] ?? ''
    delete this.awayText[key]
    const spec = viewToast(workspace, old, v, { reply, away: true, home: nh().home() })
    if (spec) this.toast(spec)
  }

  private dropThread(name: string): void {
    const s = this.state
    if (!s.threads[name]) return
    const threads = { ...s.threads }
    delete threads[name]
    const chats = { ...s.chats }
    delete chats[name]
    const layout = this.mapWins(s.layout, (w) =>
      w.buf.kind === 'chat' && w.buf.thread === name
        ? { ...w, buf: { kind: 'chat', thread: Coordinator }, back: [] }
        : { ...w, back: w.back.filter((b) => !(b.kind === 'chat' && b.thread === name)) },
    )
    this.set({ threads, chats, order: s.order.filter((n) => n !== name), layout })
    this.say(`${name} deleted`, 'muted')
  }

  private dropWorkspace(name: string): void {
    const s = this.state
    delete this.layouts[name]
    this.set({ workspaces: s.workspaces.filter((w) => w.name !== name) })
    if (s.workspace === name) {
      this.set({ workspace: '', threads: {}, order: [], chats: {}, artifacts: [], docs: {}, screen: 'workspaces' })
      localStorage.removeItem('workspace')
    }
    this.say(`${name} deleted · :restore ${name} brings it back`, 'muted')
  }

  // --- toasts ---

  onScreen(thread: string): boolean {
    return this.state.screen === 'workspace' && this.wins().some((w) => w.buf.kind === 'chat' && w.buf.thread === thread)
  }

  private toast(spec: ToastSpec): void {
    const s = this.state
    if (spec.workspace === s.workspace && this.onScreen(spec.thread) && document.hasFocus()) return
    const now = Date.now()
    const t: Toast = { ...spec, id: nextId(), at: now, until: spec.life ? now + spec.life : 0 }
    const toasts = s.toasts.filter((o) => !(o.workspace === t.workspace && o.thread === t.thread))
    this.set({ toasts: [...toasts, t], log: [...s.log, t].slice(-500) })
    if (spec.needsYou) nh().notify(`${spec.workspace} · ${spec.thread}`, spec.body, { workspace: spec.workspace, thread: spec.thread })
  }

  visibleToasts(): Toast[] {
    return this.state.toasts.slice(-toastsShown)
  }

  private prune(): void {
    const s = this.state
    const now = Date.now()
    const ctx = { open: s.workspace, onScreen: (t: string) => this.onScreen(t), threads: s.threads, workspaces: s.workspaces }
    const toasts = s.toasts.filter((t) => keep(t, now, ctx))
    if (toasts.length !== s.toasts.length) this.set({ toasts })
  }

  dismissToasts(): void {
    this.set({ toasts: [] })
  }

  private badge(): void {
    let n = 0
    for (const w of this.state.workspaces) n += (w.counts.waiting ?? 0) + (w.coordinator === 'waiting' ? 1 : 0)
    nh().setBadge(n)
  }

  // --- screens and workspaces ---

  showWorkspaces(): void {
    this.saveLayout()
    const i = this.state.workspaces.findIndex((w) => w.name === this.state.workspace)
    this.set({ screen: 'workspaces', workspaceCursor: Math.max(i, 0), insert: false })
  }

  private saveLayout(): void {
    const s = this.state
    if (s.workspace && s.screen === 'workspace') this.layouts[s.workspace] = s.layout
  }

  async openWorkspace(name: string): Promise<void> {
    const s = this.state
    if (!s.workspaces.some((w) => w.name === name)) {
      this.say(`no workspace ${name}`, 'love')
      return
    }
    this.saveLayout()
    // The threads of the workspace left behind keep toasting.
    for (const [n, v] of Object.entries(s.threads)) this.away[`${s.workspace}\0${n}`] = v
    const layout = this.layouts[name] ?? {
      cols: [
        { id: nextId(), wins: [this.win({ kind: 'chat', thread: Coordinator })] },
        { id: nextId(), wins: [{ ...this.win({ kind: 'threads' }), follow: false, cursor: 0 }] },
      ],
      focus: 0,
    }
    if (!this.layouts[name]) layout.focus = layout.cols[0].wins[0].id
    localStorage.setItem('workspace', name)
    this.set({ workspace: name, screen: 'workspace', layout, threads: {}, order: [], chats: {}, artifacts: [], docs: {} })
    await this.resync()
    // Windows of threads that are gone show the coordinator.
    const known = this.state.threads
    this.set({
      layout: this.mapWins(this.state.layout, (w) =>
        w.buf.kind === 'chat' && !known[w.buf.thread] ? { ...w, buf: { kind: 'chat', thread: Coordinator } } : w,
      ),
    })
  }

  async createWorkspace(name: string, pin?: string): Promise<boolean> {
    const bad = validName(name)
    if (bad) {
      this.say(bad, 'love')
      return false
    }
    if (pin === undefined) {
      const dir = await nh().pickFolder()
      if (dir === null) return false
      pin = dir
    }
    if ((await this.try(this.call('createWorkspace', { name, pin }))) === undefined) return false
    await this.loadWorkspaces()
    await this.openWorkspace(name)
    this.focusPrompt()
    return true
  }

  async setFolder(dir?: string): Promise<void> {
    if (!this.needWorkspace()) return
    if (dir === undefined) {
      const d = await nh().pickFolder()
      if (d === null) return
      dir = d
    }
    if ((await this.try(this.call('setFolder', { workspace: this.state.workspace, pin: dir }))) === undefined) return
    this.say(`${this.state.workspace} now works in ${this.short(dir)}`, 'pine')
  }

  async setLocal(args: string[]): Promise<void> {
    if (!this.needWorkspace()) return
    const v = this.state.workspaces.find((w) => w.name === this.state.workspace)
    if (!v) return
    const options = ['coordinator', 'threads']
    const values: Record<string, string> = { coordinator: v.coordinatorAgent ?? '', threads: v.threadsAgent ?? '' }
    const shown: string[] = []
    const set: string[] = []
    for (const w of args.length ? args : options) {
      let [name, agent] = w.split('=', 2) as [string, string | undefined]
      if (w.endsWith('&')) [name, agent] = [w.slice(0, -1), '']
      if (agent === undefined) {
        name = name.replace(/\?$/, '')
        if (!(name in values)) {
          this.say(`no option ${name}; the options are ${options.join(', ')}`, 'love')
          return
        }
        shown.push(`${name}=${values[name]}`)
        continue
      }
      const ok = await this.try(this.call('setLocal', { workspace: this.state.workspace, option: name, agent }))
      if (ok === undefined) return
      set.push(w)
    }
    if (shown.length) this.say(shown.join('  '))
    else if (set.length) this.say(`${this.state.workspace}: ${set.join(' ')}`, 'pine')
  }

  async restoreWorkspace(name?: string): Promise<void> {
    if (!name) {
      const names = await this.try(this.call('deletedWorkspaces', undefined))
      if (!names) return
      this.say(names.length ? `deleted: ${names.join(', ')} · :restore <name>` : 'no deleted workspaces', 'muted')
      return
    }
    if ((await this.try(this.call('restoreWorkspace', { workspace: name }))) === undefined) return
    await this.loadWorkspaces()
    this.say(`${name} restored`, 'subtle')
  }

  askDeleteWorkspace(name: string): void {
    this.set({
      confirm: {
        text: `delete the workspace ${name} and stop its threads? y/n`,
        run: () => void this.try(this.call('deleteWorkspace', { workspace: name })),
      },
    })
  }

  askDeleteThread(name: string): void {
    if (name === Coordinator) {
      this.say('the coordinator cannot be deleted', 'muted')
      return
    }
    const workspace = this.state.workspace
    this.set({
      confirm: {
        text: `delete ${name} and its transcript? y/n`,
        run: () => void this.try(this.call('deleteThread', { workspace, thread: name })),
      },
    })
  }

  answerConfirm(yes: boolean): void {
    const c = this.state.confirm
    this.set({ confirm: null })
    if (yes) c?.run()
  }

  private needWorkspace(): boolean {
    if (this.state.workspace) return true
    this.say('open a workspace first', 'muted')
    return false
  }

  short(p: string): string {
    const home = nh().home()
    return home && p.startsWith(home) ? '~' + p.slice(home.length) : p
  }

  // --- windows ---

  private win(buf: BufRef): Win {
    return { id: nextId(), buf, back: [], cursor: -1, follow: true }
  }

  wins(): Win[] {
    return this.state.layout.cols.flatMap((c) => c.wins)
  }

  cur(): Win | undefined {
    return this.wins().find((w) => w.id === this.state.layout.focus) ?? this.wins()[0]
  }

  private mapWins(l: Layout, fn: (w: Win) => Win): Layout {
    return { ...l, cols: l.cols.map((c) => ({ ...c, wins: c.wins.map(fn) })) }
  }

  updateWin(id: number, patch: Partial<Win>): void {
    this.set({ layout: this.mapWins(this.state.layout, (w) => (w.id === id ? { ...w, ...patch } : w)) })
  }

  focus(id: number): void {
    if (this.state.layout.focus !== id) this.set({ layout: { ...this.state.layout, focus: id } })
  }

  // show puts a buffer in a window; the one it showed goes on its back list.
  show(id: number, buf: BufRef): void {
    const w = this.wins().find((x) => x.id === id)
    if (!w) return
    if (sameBuf(w.buf, buf)) {
      this.focus(id)
      return
    }
    const back = [...w.back.filter((b) => !sameBuf(b, buf)), w.buf].slice(-50)
    this.set({
      layout: {
        ...this.mapWins(this.state.layout, (x) =>
          x.id === id ? { ...x, buf, back, cursor: buf.kind === 'threads' ? 0 : -1, follow: buf.kind !== 'threads' } : x,
        ),
        focus: id,
      },
    })
  }

  goBack(): void {
    const w = this.cur()
    if (!w || w.back.length === 0) return
    const buf = w.back[w.back.length - 1]
    this.updateWin(w.id, { buf, back: w.back.slice(0, -1), cursor: buf.kind === 'threads' ? 0 : -1, follow: true })
  }

  // alternate is <C-^>: the buffer the window showed last.
  alternate(): void {
    const w = this.cur()
    if (!w || w.back.length === 0) return
    const buf = w.back[w.back.length - 1]
    this.updateWin(w.id, { buf, back: [...w.back.slice(0, -1), w.buf], cursor: -1, follow: true })
  }

  split(vertical: boolean, buf?: BufRef): void {
    const s = this.state
    const w = this.cur()
    if (!w) return
    const nw = this.win(buf ?? w.buf)
    const cols = s.layout.cols.map((c) => ({ ...c, wins: c.wins.slice() }))
    const ci = cols.findIndex((c) => c.wins.some((x) => x.id === w.id))
    if (vertical) cols.splice(ci + 1, 0, { id: nextId(), wins: [nw] })
    else {
      const wi = cols[ci].wins.findIndex((x) => x.id === w.id)
      // :split puts the new window above, as Vim does.
      cols[ci].wins.splice(wi, 0, nw)
    }
    this.set({ layout: { cols, focus: nw.id } })
  }

  close(id?: number): void {
    const s = this.state
    const target = id ?? s.layout.focus
    if (this.wins().length <= 1) {
      this.say('the last window stays · <leader>pv for the workspaces', 'muted')
      return
    }
    const w = this.wins().find((x) => x.id === target)
    if (w?.buf.kind === 'artifact' && s.docs[w.buf.name]?.edit) {
      this.say('unsaved changes · :w saves, :q! drops them', 'gold')
      return
    }
    const flat = this.wins()
    const i = flat.findIndex((x) => x.id === target)
    const cols = s.layout.cols.map((c) => ({ ...c, wins: c.wins.filter((x) => x.id !== target) })).filter((c) => c.wins.length)
    const next = flat[i + 1] ?? flat[i - 1]
    this.set({ layout: { cols, focus: next?.id ?? cols[0].wins[0].id } })
  }

  only(): void {
    const w = this.cur()
    if (w) this.set({ layout: { cols: [{ id: nextId(), wins: [w] }], focus: w.id } })
  }

  nextWindow(d: number): void {
    const flat = this.wins()
    const i = flat.findIndex((w) => w.id === this.state.layout.focus)
    const w = flat[(i + d + flat.length) % flat.length]
    if (w) this.focus(w.id)
  }

  // move goes to the window in a direction: h l across columns, j k within one.
  move(dir: 'h' | 'j' | 'k' | 'l'): void {
    const cols = this.state.layout.cols
    const ci = cols.findIndex((c) => c.wins.some((w) => w.id === this.state.layout.focus))
    if (ci < 0) return
    const wi = cols[ci].wins.findIndex((w) => w.id === this.state.layout.focus)
    if (dir === 'h' || dir === 'l') {
      const c = cols[ci + (dir === 'l' ? 1 : -1)]
      if (c) this.focus(c.wins[Math.min(wi, c.wins.length - 1)].id)
    } else {
      const w = cols[ci].wins[wi + (dir === 'j' ? 1 : -1)]
      if (w) this.focus(w.id)
    }
  }

  // --- threads ---

  // showThread shows a thread: where it is on screen already, else in the
  // threads pane's window, else in this window.
  showThread(name: string): void {
    const wins = this.wins()
    const there = wins.find((w) => w.buf.kind === 'chat' && w.buf.thread === name)
    if (there) {
      this.focus(there.id)
      return
    }
    const cur = this.cur()
    const pane = cur?.buf.kind === 'threads' ? cur : wins.find((w) => w.buf.kind === 'threads')
    const target = pane ?? cur
    if (target) this.show(target.id, { kind: 'chat', thread: name })
  }

  async goThread(workspace: string, thread: string): Promise<void> {
    if (workspace !== this.state.workspace || this.state.screen !== 'workspace') await this.openWorkspace(workspace)
    if (workspace === this.state.workspace) this.showThread(thread)
  }

  // threadsPane is <leader>t: it goes to the threads list, or opens it in a
  // column of its own on the right, so the thread on screen stays.
  threadsPane(): void {
    if (!this.needWorkspace()) return
    if (this.state.screen !== 'workspace') void this.openWorkspace(this.state.workspace)
    const w = this.wins().find((x) => x.buf.kind === 'threads')
    if (w) {
      this.focus(w.id)
      return
    }
    const nw = { ...this.win({ kind: 'threads' }), cursor: 0, follow: false }
    this.set({ layout: { cols: [...this.state.layout.cols, { id: nextId(), wins: [nw] }], focus: nw.id } })
  }

  coordinator(): void {
    if (!this.needWorkspace()) return
    if (this.state.screen !== 'workspace') void this.openWorkspace(this.state.workspace)
    const w = this.wins().find((x) => x.buf.kind === 'chat' && x.buf.thread === Coordinator)
    if (w) this.focus(w.id)
    else {
      const c = this.cur()
      if (c) this.show(c.id, { kind: 'chat', thread: Coordinator })
    }
  }

  // cycleThread is ]b [b: the next thread of the list in this window.
  cycleThread(d: number): void {
    const w = this.cur()
    const names = [Coordinator, ...this.listOrder()]
    if (!w || names.length === 0) return
    const i = w.buf.kind === 'chat' ? names.indexOf(w.buf.thread) : -1
    const n = names[(i + d + names.length) % names.length]
    this.show(w.id, { kind: 'chat', thread: n })
  }

  // listOrder is the threads of the list, as it groups them: waiting first,
  // then running, idle and resolved.
  listOrder(): string[] {
    const rank: Record<string, number> = { waiting: 0, running: 1, idle: 2, resolved: 3 }
    return this.state.order
      .filter((n) => n !== Coordinator)
      .map((n) => this.state.threads[n])
      .filter(Boolean)
      .sort((a, b) => rank[a.state] - rank[b.state] || b.updatedAt.localeCompare(a.updatedAt))
      .map((v) => v.name)
  }

  async openThread(name: string, where?: string): Promise<void> {
    if (!this.needWorkspace()) return
    const bad = validName(name)
    if (bad) {
      this.say(bad, 'love')
      return
    }
    this.opening.add(name)
    const ok = await this.try(this.call('openThread', { workspace: this.state.workspace, name, location: where ?? '' }))
    if (ok === undefined) {
      this.opening.delete(name)
      return
    }
    this.showThread(name)
    this.focusPrompt()
  }

  // target is the thread of the window in focus, or the one under the cursor
  // of the threads list.
  target(): string {
    const w = this.cur()
    if (!w) return ''
    if (w.buf.kind === 'chat') return w.buf.thread
    if (w.buf.kind === 'threads') return this.threadRows()[w.cursor]?.name ?? ''
    return ''
  }

  async resolve(name = this.target()): Promise<void> {
    if (this.needWorkspace() && name) await this.try(this.call('resolve', { workspace: this.state.workspace, thread: name }))
  }

  async reopen(name = this.target()): Promise<void> {
    if (this.needWorkspace() && name) await this.try(this.call('reopen', { workspace: this.state.workspace, thread: name }))
  }

  async stop(name = this.target()): Promise<void> {
    if (!name) return
    const v = this.state.threads[name]
    if (v?.state !== 'running' && v?.state !== 'waiting') {
      this.say(`${name} has no turn running`, 'muted')
      return
    }
    if ((await this.try(this.call('stop', { workspace: this.state.workspace, thread: name }))) !== undefined)
      this.say(`stopped ${name}`, 'muted')
  }

  // threadRows are the rows of the threads list, as the list draws them.
  threadRows(): ({ name: string; state: string } | { name: ''; state: 'resolved-fold' })[] {
    const names = this.listOrder()
    const open = names.filter((n) => this.state.threads[n].state !== 'resolved')
    const resolved = names.filter((n) => this.state.threads[n].state === 'resolved')
    const rows: ({ name: string; state: string } | { name: ''; state: 'resolved-fold' })[] = open.map((n) => ({
      name: n,
      state: this.state.threads[n].state,
    }))
    if (resolved.length) {
      rows.push({ name: '', state: 'resolved-fold' })
      if (this.state.showResolved) rows.push(...resolved.map((n) => ({ name: n, state: 'resolved' })))
    }
    return rows
  }

  toggleResolved(): void {
    this.set({ showResolved: !this.state.showResolved })
  }

  // --- writing ---

  async send(thread: string, text: string): Promise<boolean> {
    const t = text.trim()
    if (!t) return false
    const ok = await this.try(this.call('send', { workspace: this.state.workspace, thread, text: t }))
    if (ok === undefined) return false
    const w = this.cur()
    if (w) this.updateWin(w.id, { follow: true, cursor: -1 })
    return true
  }

  async sendQueuedNow(thread: string): Promise<void> {
    await this.try(this.call('send', { workspace: this.state.workspace, thread, text: '', now: true }))
  }

  // takeBack is u with an empty prompt, as Esc in Claude Code: your last
  // message comes back while the agent has not answered it. A queued one
  // leaves the queue; one whose turn runs stops it. It returns the text,
  // or null when there is no such message.
  async takeBack(thread: string): Promise<string | null> {
    const v = this.state.threads[thread]
    const bs = this.state.chats[thread] ?? []
    const queued = (v?.pending ?? []).filter((e) => e.from === User).pop()
    if (queued) {
      const ok = await this.try(this.call('unqueue', { workspace: this.state.workspace, thread, at: queued.at }))
      return ok === undefined ? '' : (queued.text ?? '')
    }
    const last = lastUserMessage(bs)
    if (last >= 0 && (v?.state === 'running' || v?.state === 'waiting')) {
      const ok = await this.try(this.call('stop', { workspace: this.state.workspace, thread }))
      const b = bs[last]
      return ok === undefined || b.kind !== 'message' ? '' : b.text
    }
    return null
  }

  // answer replies to the permission request of the thread in focus, or of
  // the first window on screen that has one.
  async answer(d: Decision, msg = ''): Promise<void> {
    const cur = this.cur()
    const wins = cur ? [cur, ...this.wins()] : this.wins()
    const w = wins.find((x) => x.buf.kind === 'chat' && this.state.threads[x.buf.thread]?.permission)
    if (!w || w.buf.kind !== 'chat') {
      this.say('no permission request in this window', 'muted')
      return
    }
    await this.answerThread(w.buf.thread, d, msg)
  }

  async answerThread(name: string, d: Decision, msg = ''): Promise<void> {
    const v = this.state.threads[name]
    if (!v?.permission) return
    const ok = await this.try(
      this.call('answer', { workspace: this.state.workspace, thread: name, id: v.permission.id, decision: d, message: msg }),
    )
    if (ok !== undefined) this.say(`${d} · ${v.permission.tool} · ${name}`, 'pine')
  }

  completions(thread: string, query: string) {
    return this.call('completions', { workspace: this.state.workspace, thread, query })
  }

  // promptRequest asks the chat window in focus to take the keys; the
  // window's prompt listens for it.
  promptRequest = 0
  promptHandled = 0
  focusPrompt(): void {
    typeahead.waiting = true
    typeahead.text = ''
    typeahead.send = false
    // Never wait for ever: a prompt that does not come lets keys go again.
    setTimeout(() => (typeahead.waiting = false), 2000)
    this.promptRequest++
    this.set({})
  }

  setInsert(on: boolean): void {
    if (this.state.insert !== on) this.set({ insert: on })
  }

  toggleFold(key: number): void {
    this.set({ folds: { ...this.state.folds, [key]: !this.state.folds[key] } })
  }

  // --- artifacts ---

  async openArtifact(name: string, id = this.cur()?.id): Promise<void> {
    if (!this.needWorkspace() || id === undefined) return
    await this.loadDoc(name)
    if (this.state.docs[name]) this.show(id, { kind: 'artifact', name })
  }

  private async loadDoc(name: string): Promise<void> {
    const r = await this.try(this.call('artifact', { workspace: this.state.workspace, name }))
    if (!r) return
    const d = this.state.docs[name]
    this.set({ docs: { ...this.state.docs, [name]: { artifact: r.artifact, text: r.text, edit: d?.edit ?? null } } })
  }

  startEdit(name: string): void {
    const d = this.state.docs[name]
    if (!d || d.edit) return
    this.set({
      docs: { ...this.state.docs, [name]: { ...d, edit: { text: d.text, base: d.artifact.version, baseText: d.text } } },
    })
  }

  // editText takes a change in the document. The first one starts an edit;
  // undoing back to the saved text ends it, so newer versions show again.
  editText(name: string, text: string): void {
    const d = this.state.docs[name]
    if (!d) return
    const edit = d.edit ?? { text: d.text, base: d.artifact.version, baseText: d.text }
    const done = text === edit.baseText && !d.conflict
    this.set({ docs: { ...this.state.docs, [name]: { ...d, edit: done ? null : { ...edit, text } } } })
  }

  dirty(name: string): boolean {
    const e = this.state.docs[name]?.edit
    return !!e && e.text !== e.baseText
  }

  // save is :w. The hub merges by lines with a newer version; changes to the
  // same lines are refused unless force (:w!).
  async save(name: string, force = false): Promise<boolean> {
    const d = this.state.docs[name]
    if (!d?.edit) return true
    if (!this.dirty(name)) {
      this.set({ docs: { ...this.state.docs, [name]: { ...d, edit: null } } })
      return true
    }
    let r
    try {
      r = await this.call('editArtifact', {
        workspace: this.state.workspace,
        name,
        text: d.edit.text,
        base: d.edit.base,
        baseText: d.edit.baseText,
        force,
      })
    } catch (err) {
      this.say(`${errText(err)} · do takes a change, :w saves yours, :e! takes the newer version`, 'love')
      await this.loadDoc(name)
      // As the TUI: the edit now starts from the newer version, so the next
      // :w saves what you made of both over it.
      const cur = this.state.docs[name]
      if (cur?.edit) {
        const edit = { ...cur.edit, base: cur.artifact.version, baseText: cur.text }
        this.set({ docs: { ...this.state.docs, [name]: { ...cur, edit, conflict: true } } })
      }
      return false
    }
    const merged = r.text !== d.edit.text
    this.set({ docs: { ...this.state.docs, [name]: { artifact: r.artifact, text: r.text, edit: null } } })
    this.say(
      `${merged ? 'merged and saved' : 'saved'} ${name} v${r.artifact.version} · ${r.artifact.thread} sees it with your next message`,
      'subtle',
    )
    return true
  }

  // takeHunk is do: one change of the newer version comes into the edit.
  takeHunk(name: string, index: number): void {
    const d = this.state.docs[name]
    if (!d?.edit || !d.conflict) return
    const mine = d.edit.text.split('\n')
    const theirs = d.text.split('\n')
    const h = hunks(lines(mine, theirs))[index]
    if (!h) return this.say('no change here', 'muted')
    const text = take(mine, theirs, h).join('\n')
    this.set({ docs: { ...this.state.docs, [name]: { ...d, edit: { ...d.edit, text }, conflict: text !== d.text } } })
  }

  // revert is :e! (drop the changes, take the newest) and :q! on an artifact.
  async revert(name: string): Promise<void> {
    const d = this.state.docs[name]
    if (!d) return
    this.set({ docs: { ...this.state.docs, [name]: { ...d, edit: null } } })
    await this.loadDoc(name)
  }

  // --- pickers, the command line, settings ---

  openPicker(tab: Tab, only = false, local = false): void {
    if (['artifacts', 'text', 'outline'].includes(tab) && !this.state.workspace) {
      this.say('open a workspace first', 'muted')
      return
    }
    if (local && !this.needWorkspace()) return
    this.set({ picker: { tab, query: '', index: 0, only, remote: [], local }, cmdline: null })
    void this.loadRemoteThreads()
  }

  private async loadRemoteThreads(): Promise<void> {
    const others = this.state.workspaces.filter((w) => w.name !== this.state.workspace)
    const lists = await Promise.all(
      others.map((w) =>
        this.call('threads', { workspace: w.name })
          .then((vs) => vs.map((view) => ({ workspace: w.name, view })))
          .catch(() => []),
      ),
    )
    const p = this.state.picker
    if (p) this.set({ picker: { ...p, remote: lists.flat() } })
  }

  setPicker(patch: Partial<Picker>): void {
    const p = this.state.picker
    if (p) this.set({ picker: { ...p, ...patch } })
  }

  closePicker(): void {
    this.set({ picker: null })
  }

  openCmdline(text = ''): void {
    this.set({ cmdline: text, picker: null })
  }

  setCmdline(text: string | null): void {
    this.set({ cmdline: text })
  }

  setPending(p: string): void {
    if (p !== this.state.pending) this.set({ pending: p })
  }

  setSettings(patch: Partial<Settings>): void {
    const settings = { ...this.state.settings, ...patch }
    localStorage.setItem('settings', JSON.stringify(settings))
    this.set({ settings })
  }

  setHints(hints: Hint[] | null): void {
    if (hints || this.state.hints) this.set({ hints })
  }

  openNewWorkspace(open = true): void {
    this.set({ newWorkspace: open, picker: null, cmdline: null })
  }

  openSettings(open = true): void {
    this.set({ settingsOpen: open, picker: null, cmdline: null })
  }

  setWorkspaceCursor(i: number): void {
    const n = this.state.workspaces.length
    this.set({ workspaceCursor: Math.max(0, Math.min(i, n - 1)) })
  }

  async startHub(): Promise<void> {
    this.say('starting the hub…', 'muted')
    await this.try(nh().startHub())
  }

  // --- ex commands ---

  async ex(line: string): Promise<void> {
    const e = parseEx(line)
    this.set({ cmdline: null })
    if (!e) return
    const a = e.args
    const w = this.cur()
    const art = w?.buf.kind === 'artifact' ? w.buf.name : ''
    switch (e.cmd) {
      case 'new':
        if (!a[0]) return this.say('usage: :new <name> [folder]', 'muted')
        void this.createWorkspace(a[0], a[1])
        return
      case 'folder':
        return this.setFolder(a[0])
      case 'setlocal':
        return this.setLocal(a)
      case 'workspace':
        return a[0] ? this.openWorkspace(a[0]) : this.openPicker('workspaces', true)
      case 'workspaces':
        return this.showWorkspaces()
      case 'restore':
        return this.restoreWorkspace(a[0])
      case 'open':
        if (!a[0]) return this.say('usage: :open <name> [folder|host:dir]', 'muted')
        return this.openThread(a[0], a[1])
      case 'resolve':
        return this.resolve(a[0])
      case 'reopen':
        return this.reopen(a[0])
      case 'stop':
        return this.stop(a[0])
      case 'b':
      case 'buffer':
      case 'buf': {
        if (!a[0]) return this.openPicker('buffers', true)
        const buf = this.findBuffer(a[0])
        if (!buf) return this.say(`no buffer ${a[0]}`, 'love')
        if (w) this.show(w.id, buf)
        return
      }
      case 'ls':
      case 'buffers':
        return this.openPicker('buffers', true)
      case 'messages':
      case 'mes':
        if (w) this.show(w.id, { kind: 'messages' })
        return
      case 'vsplit':
      case 'vs':
      case 'split':
      case 'sp': {
        let buf: BufRef | undefined
        if (a[0] && !(buf = this.findBuffer(a[0]) ?? undefined)) return this.say(`no buffer ${a[0]}`, 'love')
        return this.split(e.cmd.startsWith('v'), buf)
      }
      case 'only':
      case 'on':
        return this.only()
      case 'close':
      case 'clo':
        return this.close()
      case 'threads':
        return this.threadsPane()
      case 'artifacts':
        return this.openPicker('artifacts', true)
      case 'allow':
        return this.answer('allow')
      case 'always':
        return this.answer('always')
      case 'deny':
        return this.answer('deny', e.rest.trim())
      case 'help':
      case 'h':
        if (w) this.split(true, { kind: 'help' })
        return
      case 'w':
      case 'write':
        if (!art) return this.say('nothing to write here; :w saves an artifact', 'muted')
        await this.save(art, e.bang)
        return
      case 'wq':
      case 'x':
        if (art && (await this.save(art, e.bang))) this.closeOrBack()
        return
      case 'e':
      case 'edit':
        if (!art) return
        if (e.bang) return this.revert(art)
        return this.startEdit(art)
      case 'q':
      case 'quit':
        if (art && e.bang) await this.revert(art)
        return this.closeOrBack()
      case 'qa':
      case 'qall':
      case 'quitall':
        return this.say('the window closes with ⌘W, the app with ⌘Q; the hub and its turns run on', 'muted')
      case 'colorscheme':
      case 'colo':
        if (!a[0]) return this.say(`colorscheme ${this.state.settings.scheme} · ${schemeNames.join(' ')}`)
        if (!schemeNames.includes(a[0])) return this.say(`no colorscheme ${a[0]}; ${schemeNames.join(' ')}`, 'love')
        return this.setSettings({ scheme: a[0] })
      case 'set':
        return this.setOption(a)
      case 'settings':
        return this.openSettings()
      default:
        this.say(`not a command: ${e.cmd}`, 'love')
    }
  }

  // closeOrBack is :q: an artifact goes back to what the window showed,
  // another window closes.
  private closeOrBack(): void {
    const w = this.cur()
    if (w?.buf.kind === 'artifact' && w.back.length) {
      if (this.dirty(w.buf.name)) return this.say('unsaved changes · :w saves, :q! drops them', 'gold')
      return this.goBack()
    }
    this.close()
  }

  private setOption(args: string[]): void {
    const s = this.state.settings
    if (!args.length) return this.say(`vim=${s.vim} sidebar=${s.sidebar} fontsize=${s.fontSize} compactwarn=${s.compactwarn} colorscheme=${s.scheme}`)
    for (const arg of args) {
      const [k, v] = arg.split('=', 2)
      switch (k) {
        case 'vim':
          this.setSettings({ vim: true })
          break
        case 'novim':
          this.setSettings({ vim: false })
          break
        case 'sidebar':
          this.setSettings({ sidebar: true })
          break
        case 'nosidebar':
          this.setSettings({ sidebar: false })
          break
        case 'fontsize':
        case 'fs':
          if (Number(v) >= 9 && Number(v) <= 28) this.setSettings({ fontSize: Number(v) })
          else return this.say('fontsize is 9 to 28', 'love')
          break
        case 'compactwarn':
          if (Number(v) >= 0 && Number(v) <= 100) this.setSettings({ compactwarn: Number(v) })
          else return this.say('compactwarn is 0 to 100', 'love')
          break
        default:
          return this.say(`no option ${k}; the options are vim novim sidebar nosidebar fontsize compactwarn`, 'love')
      }
    }
  }

  findBuffer(name: string): BufRef | null {
    if (this.state.threads[name]) return { kind: 'chat', thread: name }
    if (this.state.artifacts.some((a) => a.name === name)) return { kind: 'artifact', name }
    if (name === 'threads') return { kind: 'threads' }
    if (name === 'help') return { kind: 'help' }
    if (name === 'messages') return { kind: 'messages' }
    const ms = this.state.order.filter((n) => n.includes(name))
    return ms.length === 1 ? { kind: 'chat', thread: ms[0] } : null
  }
}

export function sameBuf(a: BufRef, b: BufRef): boolean {
  if (a.kind !== b.kind) return false
  if (a.kind === 'chat' && b.kind === 'chat') return a.thread === b.thread
  if (a.kind === 'artifact' && b.kind === 'artifact') return a.name === b.name
  return true
}

export const store = new Store()

export function useStore<T>(pick: (s: State) => T): T {
  return useSyncExternalStore(store.subscribe, () => pick(store.state))
}
