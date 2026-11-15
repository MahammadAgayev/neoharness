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
  type Wait,
  type WorkspaceView,
  SeenDwellMs,
} from '../shared/model'
import { Away } from './lib/away'
import { parseEx } from './lib/ex'
import { schemeNames } from './lib/themes'
import { endHidesError, errorToast, keep, noticeToast, viewToast, type Toast, type ToastSpec, type Tone } from './lib/toasts'
import { addEvent, fromEvents, lastText, lastUserMessage, settle, type Block } from './lib/transcript'
import { typeahead } from './ui/registry'

// historyTail is how much of each transcript loads, as in the TUI.
const historyTail = 2000
const toastsShown = 3

export type MsgTone = Tone | 'text' | 'subtle'

// A chat's thread is a name of the open workspace, or workspace/name for a
// thread of another workspace on screen beside it: a peek. Names have no /.
export type BufRef =
  | { kind: 'chat'; thread: string }
  | { kind: 'threads' }
  | { kind: 'artifact'; name: string }
  | { kind: 'messages' }
  | { kind: 'waits' }
  | { kind: 'todo'; thread: string }

export interface Win {
  id: number
  buf: BufRef
  // back is where <C-o> goes: the buffers the window showed before; fwd
  // is where <C-i> goes after it, as Slack's back and forward.
  back: BufRef[]
  fwd: BufRef[]
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
  // merges from, baseText that version's text, and start the text the edit
  // began on, so the page marks what an agent changed since.
  edit: { text: string; base: number; baseText: string; start: string } | null
  // conflicts are the blocks both the edit and a newer version changed: the
  // edit keeps mine, and the page offers theirs to take. prev is the block
  // before, where theirs shows when mine is gone.
  conflicts?: Conflict[]
}

export interface Conflict {
  mine: string
  theirs: string
  prev: string
}

export type Tab = 'all' | 'threads' | 'artifacts' | 'text' | 'commands' | 'workspaces' | 'notifications' | 'outline'

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
  sidebarWidth: number
}

export interface State {
  status: HubStatus
  statusDetail: string
  workspaces: WorkspaceView[]
  // waits is the hub's waits list: what needs the person in every
  // workspace, oldest first.
  waits: Wait[]
  screen: 'workspaces' | 'workspace'
  workspace: string
  threads: Record<string, ThreadView>
  order: string[]
  chats: Record<string, Block[]>
  // streams are the replies being written now, by chat key: the hub's
  // delta, which the text event that follows replaces.
  streams: Record<string, string>
  artifacts: Artifact[]
  docs: Record<string, Doc>
  layout: Layout
  toasts: Toast[]
  log: Toast[]
  message: { text: string; tone: MsgTone } | null
  // echo is a confirmation in the focused window's status line (Echo).
  echo: string
  confirm: { text: string; run: () => void } | null
  picker: Picker | null
  cmdline: string | null
  // insert is set while an editor takes the keys.
  insert: boolean
  settings: Settings
  settingsOpen: boolean
  helpOpen: boolean
  // cost is the cost dialog: of the open workspace, or of all of them.
  cost: 'workspace' | 'all' | null
  // choice is the /model or /effort picker of a thread.
  choice: { thread: string; kind: 'model' | 'effort' } | null
  newWorkspace: boolean
  newDoc: boolean
  hints: Hint[] | null
  showResolved: boolean
  folds: Record<number, boolean>
  // todoOpen holds the threads whose todo list above the prompt is unfolded.
  todoOpen: Record<string, boolean>
  // workspaceCursor is the row of the workspaces screen.
  workspaceCursor: number
  pending: string
  // recapOpen holds the chats, by workspace/thread, that show their recap
  // card: the person came back to a recap they had not closed.
  recapOpen: Record<string, boolean>
}

let ids = 1
const nextId = () => ids++

function loadSettings(): Settings {
  const d: Settings = { scheme: 'system', vim: true, fontSize: 14, compactwarn: 80, sidebar: true, leader: '<Space>', maps: [], keyhints: true, sidebarWidth: 200 }
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
    waits: [],
    threads: {},
    order: [],
    chats: {},
    streams: {},
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
    echo: '',
    settings: loadSettings(),
    settingsOpen: false,
  helpOpen: false,
  cost: null,
  choice: null,
    newWorkspace: false,
    newDoc: false,
    hints: null,
    showResolved: false,
    folds: {},
    todoOpen: {},
    workspaceCursor: 0,
    pending: '',
    recapOpen: {},
  }
  private listeners = new Set<() => void>()
  // writeDoc is a new doc whose editor opens ready to write.
  writeDoc = ''
  private layouts: Record<string, Layout> = {}
  // away are the threads of workspaces that are not open, as last seen, so
  // their changes still toast.
  private away: Record<string, ThreadView> = {}
  private awayText: Record<string, string> = {}
  private connected = false
  // opening are threads the person opens here: they see them open, so the
  // "opened" toast would only cover the screen.
  private opening = new Set<string>()
  // boardAt is the state each board row is grouped by: it lags the thread's
  // while the person is on the board, so no row moves under them (settle).
  // settled is when each row that moved took its new group.
  private boardAt: Record<string, string> = {}
  private boardHold = false
  // lastChat is the chat window that had focus last: what the waits list
  // opens a thread in, as Vim's quickfix opens in the previous window.
  private lastChat = 0
  settled: Record<string, number> = {}
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
    if (patch.layout) {
      const f = this.cur()
      if (f?.buf.kind === 'chat') this.lastChat = f.id
    }
    if (this.batching) this.changed = true
    else for (const fn of this.listeners) fn()
  }

  // Each hub update comes in an IPC message of its own; applied as they come,
  // a burst of them would render the window once each. They are applied
  // together once a frame instead. A hidden window gets no frames, so the
  // timer still applies them within 100 ms, for toasts and the Dock badge.
  private queue: Update[] = []
  private batching = false
  private changed = false

  private enqueue(u: Update): void {
    this.queue.push(u)
    if (this.queue.length > 1) return
    const flush = () => {
      if (this.queue.length) this.flush()
    }
    requestAnimationFrame(flush)
    setTimeout(flush, 100)
  }

  private flush(): void {
    const q = this.queue
    this.queue = []
    this.batching = true
    try {
      for (const u of q) this.apply(u)
    } finally {
      this.batching = false
      if (this.changed) {
        this.changed = false
        for (const fn of this.listeners) fn()
      }
    }
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

  // echo is a confirmation nobody asked a key for ("saved", "stopped api"):
  // it shows for 2 s at the right of the focused window's status line. With
  // an overlay open, or no window, it goes on the command line.
  private echoTimer: ReturnType<typeof setTimeout> | undefined
  echo(text: string): void {
    const s = this.state
    if (s.screen !== 'workspace' || s.picker || s.settingsOpen || s.helpOpen || s.cost || s.choice || s.newWorkspace || s.newDoc)
      return this.say(text, 'muted')
    clearTimeout(this.echoTimer)
    this.set({ echo: text })
    this.echoTimer = setTimeout(() => this.set({ echo: '' }), 2000)
  }

  // --- connection ---

  start(): void {
    nh().onStatus((s, detail) => {
      this.set({ status: s, statusDetail: detail })
      if (s === 'connected') void this.reload().then(() => this.checkVersion())
      if (s !== 'connected') this.connected = false
    })
    nh().onUpdate((u) => this.enqueue(u))
    nh().onOpenTarget((t) => void this.goThread(t.workspace, t.thread))
    nh().ready()
    setInterval(() => this.prune(), 1000)
    setInterval(() => this.see(Date.now()), 500)
    const away = new Away()
    const back = (yes: boolean) => yes && this.cameBack()
    window.addEventListener('blur', () => away.blur(Date.now()))
    window.addEventListener('focus', () => back(away.focus(Date.now())))
    for (const e of ['keydown', 'mousedown']) window.addEventListener(e, () => back(away.input(Date.now())), true)
  }

  // recapSeen is the time of the recap the person closed last in each chat.
  private recapSeen: Record<string, string> = {}

  // cameBack opens the recap card of each chat with a recap the person has
  // not closed.
  private cameBack(): void {
    const s = this.state
    const open = { ...s.recapOpen }
    for (const [n, v] of Object.entries(s.threads)) {
      const k = `${s.workspace}/${n}`
      if (v.recap && v.recapAt && v.recapAt > (this.recapSeen[k] ?? '')) open[k] = true
    }
    this.set({ recapOpen: open })
  }

  // showsRecap says a chat shows its recap card. A turn makes the recap old
  // news, so a running chat does not.
  showsRecap(thread: string): boolean {
    const s = this.state
    const v = s.threads[thread]
    return !!s.recapOpen[`${s.workspace}/${thread}`] && !!v?.recap && v.state !== 'running'
  }

  // openRecap shows the recap card of the chat in focus, as :recap asks.
  openRecap(): void {
    const s = this.state
    const w = this.cur()
    const v = w?.buf.kind === 'chat' ? s.threads[w.buf.thread] : undefined
    if (!w || w.buf.kind !== 'chat' || !v?.recap) return this.say('no recap yet', 'muted')
    if (v.state === 'running') return this.say('no recap while a turn runs', 'muted')
    this.set({ recapOpen: { ...s.recapOpen, [`${s.workspace}/${w.buf.thread}`]: true } })
  }

  // closeRecap closes a chat's recap card; it opens again only for a newer
  // recap.
  closeRecap(thread: string): void {
    const s = this.state
    const k = `${s.workspace}/${thread}`
    this.recapSeen[k] = s.threads[thread]?.recapAt ?? ''
    if (s.recapOpen[k]) {
      const open = { ...s.recapOpen }
      delete open[k]
      this.set({ recapOpen: open })
    }
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
    const [ws, waits] = await Promise.all([this.try(this.call('workspaces', undefined)), this.try(this.call('waits', undefined))])
    if (ws) this.set({ workspaces: ws })
    if (waits) this.set({ waits })
    this.badge()
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
    for (const w of this.wins()) if (w.buf.kind === 'chat' && w.buf.thread.includes('/')) await this.loadPeek(w.buf.thread)
  }

  // at is the workspace and the name of a chat's thread.
  at(key: string): { workspace: string; thread: string } {
    const i = key.indexOf('/')
    return i < 0 ? { workspace: this.state.workspace, thread: key } : { workspace: key.slice(0, i), thread: key.slice(i + 1) }
  }

  private async loadPeek(key: string): Promise<boolean> {
    const { workspace, thread } = this.at(key)
    const vs = await this.try(this.call('threads', { workspace }))
    const v = vs?.find((x) => x.name === thread)
    if (!v) return false
    const evs = await this.call('events', { workspace, thread, tail: historyTail }).catch(() => [] as Event[])
    const s = this.state
    this.set({ threads: { ...s.threads, [key]: v }, chats: { ...s.chats, [key]: fromEvents(evs, v.state === 'running') } })
    return true
  }

  // peek shows a thread of another workspace in a split, so you answer it
  // without leaving the one you work in.
  async peek(vertical: boolean, workspace: string, thread: string): Promise<void> {
    if (workspace === this.state.workspace) {
      this.split(vertical, { kind: 'chat', thread })
      return
    }
    const key = `${workspace}/${thread}`
    if (!this.state.threads[key] && !(await this.loadPeek(key))) return
    const there = this.wins().find((w) => w.buf.kind === 'chat' && w.buf.thread === key)
    if (there) this.focus(there.id)
    else this.split(vertical, { kind: 'chat', thread: key })
  }

  // openArtifactOf opens an artifact a chat names: one of another
  // workspace opens there.
  async openArtifactOf(thread: string, name: string): Promise<void> {
    const { workspace } = this.at(thread)
    if (workspace !== this.state.workspace) await this.openWorkspace(workspace)
    await this.openArtifact(name)
  }

  // --- updates ---

  private apply(u: Update, replay = false): void {
    if (this.held && u.workspace === this.state.workspace && u.kind !== 'workspace' && u.kind !== 'waits') {
      this.held.push(u)
      return
    }
    const s = this.state
    switch (u.kind) {
      case 'waits':
        this.set({ waits: u.waits ?? [] })
        this.badge()
        return
      case 'delta': {
        if (!u.thread) return
        const key = u.workspace === s.workspace ? u.thread : `${u.workspace}/${u.thread}`
        if (key !== u.thread && !s.chats[key]) return
        this.stream(key, u.text ?? '')
        return
      }
      case 'workspace': {
        if (!u.summary) return
        const i = s.workspaces.findIndex((w) => w.name === u.workspace)
        const workspaces = s.workspaces.slice()
        if (i < 0) workspaces.push(u.summary)
        else workspaces[i] = u.summary
        this.set({ workspaces })
        this.badge()
        this.restartWhenIdle()
        return
      }
      case 'thread':
        if (!u.view) return
        if (u.workspace === s.workspace) this.onView(u.view)
        else {
          this.peekView(`${u.workspace}/${u.view.name}`, u.view)
          this.awayView(u.workspace, u.view)
        }
        return
      case 'event': {
        const e = u.event
        if (!e) return
        if (e.kind === 'notice') this.toast(noticeToast(u.workspace, e))
        if (e.kind === 'error') this.toast(errorToast(u.workspace, u.thread ?? '', e))
        const ends = e.kind === 'text' || e.kind === 'turn' || e.kind === 'error'
        if (u.thread && ends && u.workspace !== s.workspace) this.stream(`${u.workspace}/${u.thread}`, '')
        if (u.workspace === s.workspace && u.thread) {
          // The finished text takes the stream's place in the same frame.
          let streams = s.streams
          if (ends && u.thread in streams) {
            streams = { ...streams }
            delete streams[u.thread]
          }
          // A held event may be in the history that loaded with it already.
          if (replay && this.loaded[u.thread]?.some((x) => JSON.stringify(x) === JSON.stringify(e))) {
            if (streams !== s.streams) this.set({ streams })
            return
          }
          this.set({ streams, chats: { ...this.state.chats, [u.thread]: addEvent(this.state.chats[u.thread] ?? [], e) } })
        } else if (u.thread) {
          const key = `${u.workspace}/${u.thread}`
          if (s.chats[key]) this.set({ chats: { ...s.chats, [key]: addEvent(s.chats[key], e) } })
          if (e.kind === 'text') this.awayText[`${u.workspace}\0${u.thread}`] = (e.text ?? '').trim().split('\n')[0] ?? ''
        }
        return
      }
      case 'artifact': {
        const a = u.artifact
        if (u.workspace !== s.workspace || !a) return
        this.set({ artifacts: [a, ...s.artifacts.filter((x) => x.name !== a.name)] })
        const d = this.state.docs[a.name]
        if (!d) return
        if (d.edit && a.version !== d.edit.base) void this.mergeIn(a.name)
        else void this.loadDoc(a.name)
        return
      }
      case 'deleted':
        if (!u.thread) this.dropWorkspace(u.workspace)
        else if (u.workspace === s.workspace) this.dropThread(u.thread)
        else this.dropThread(`${u.workspace}/${u.thread}`)
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
    if (v.state === 'idle' || v.state === 'failed' || v.state === 'resolved') chat = settle(chat, false)
    // A turn makes the recap old news; its next one waits for the next return.
    const recapOpen = { ...s.recapOpen }
    if (v.state === 'running') delete recapOpen[`${s.workspace}/${v.name}`]
    this.set({ threads, order, chats: { ...s.chats, [v.name]: chat }, recapOpen })
    const spec = viewToast(s.workspace, old, v, { reply: lastText(chat), away: false, home: nh().home() })
    if (!old && this.opening.delete(v.name)) return
    if (spec) this.toast(spec)
  }

  private peekView(key: string, v: ThreadView): void {
    const s = this.state
    if (!s.threads[key]) return
    let chat = s.chats[key] ?? []
    if (v.state === 'idle' || v.state === 'failed' || v.state === 'resolved') chat = settle(chat, false)
    this.set({ threads: { ...s.threads, [key]: v }, chats: { ...s.chats, [key]: chat } })
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

  // stream sets the reply a chat writes now; an empty text drops it.
  private stream(key: string, text: string): void {
    const streams = this.state.streams
    if ((streams[key] ?? '') === text) return
    const next = { ...streams }
    if (text) next[key] = text
    else delete next[key]
    this.set({ streams: next })
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
    this.echo(`${name} deleted`)
  }

  private dropWorkspace(name: string): void {
    const s = this.state
    delete this.layouts[name]
    this.set({ workspaces: s.workspaces.filter((w) => w.name !== name) })
    for (const k of Object.keys(s.threads)) if (k.startsWith(`${name}/`)) this.dropThread(k)
    if (s.workspace === name) {
      this.set({ workspace: '', threads: {}, order: [], chats: {}, artifacts: [], docs: {}, screen: 'workspaces' })
      localStorage.removeItem('workspace')
    }
    this.echo(`${name} deleted · :restore ${name} brings it back`)
  }

  // --- toasts ---

  onScreen(thread: string): boolean {
    return this.state.screen === 'workspace' && this.wins().some((w) => w.buf.kind === 'chat' && w.buf.thread === thread)
  }

  // heldToasts are notices that came while the person typed: they show once
  // Insert or the command line ends, so nothing arrives under the typing.
  private heldToasts: ToastSpec[] = []

  private toast(spec: ToastSpec): void {
    const s = this.state
    const shown = spec.workspace === s.workspace ? this.onScreen(spec.thread) : this.onScreen(`${spec.workspace}/${spec.thread}`)
    if ((shown && document.hasFocus()) || endHidesError(s.toasts, spec)) return
    if (!spec.needsYou && (s.insert || s.cmdline !== null)) {
      this.heldToasts = [...this.heldToasts.filter((o) => !(o.workspace === spec.workspace && o.thread === spec.thread)), spec]
      return
    }
    this.heldToasts = this.heldToasts.filter((o) => !(o.workspace === spec.workspace && o.thread === spec.thread))
    const now = Date.now()
    const t: Toast = { ...spec, id: nextId(), at: now, until: spec.life ? now + spec.life : 0 }
    const toasts = s.toasts.filter((o) => !(o.workspace === t.workspace && o.thread === t.thread))
    this.set({ toasts: [...toasts, t], log: [...s.log, t].slice(-500) })
    if (spec.needsYou) nh().notify(`${spec.workspace} · ${spec.thread}`, spec.body, { workspace: spec.workspace, thread: spec.thread })
  }

  // visibleToasts are the toasts on screen, in the order of attention: what
  // needs you first, then the newest.
  visibleToasts(): Toast[] {
    return this.state.toasts
      .slice()
      .sort((a, b) => Number(!!b.needsYou) - Number(!!a.needsYou) || b.at - a.at)
      .slice(0, toastsShown)
  }

  private prune(): void {
    if (this.heldToasts.length && !this.state.insert && this.state.cmdline === null) {
      const held = this.heldToasts
      this.heldToasts = []
      for (const t of held) this.toast(t)
    }
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
    nh().setBadge(this.state.waits.length)
  }

  // --- the waits list ---
  // Vim's quickfix list for the person's attention, as in the TUI: ]q and
  // [q, :cn and :cp walk it; :waits and :copen show it.

  // waitAt is the wait ]q went to last.
  private waitAt = ''

  // nextWait goes n waits on from the last one ]q went to, and opens it.
  nextWait(n: number): void {
    const ws = this.state.waits
    if (!ws.length) return this.say('nothing waits for you', 'muted')
    let i = ws.findIndex((w) => waitKey(w) === this.waitAt)
    i = i < 0 ? (n > 0 ? n - 1 : ws.length + n) : i + n
    if (i < 0 || i >= ws.length) return this.say('no more waits', 'muted')
    void this.jumpWait(i)
  }

  // jumpWait opens the thread of a wait, at its end, where its card is.
  async jumpWait(i: number): Promise<void> {
    const ws = this.state.waits
    const w = ws[i]
    if (!w) return
    this.waitAt = waitKey(w)
    await this.goThread(w.workspace, w.thread)
    const cur = this.cur()
    if (this.state.workspace !== w.workspace || cur?.buf.kind !== 'chat' || cur.buf.thread !== w.thread) return
    this.updateWin(cur.id, { follow: true, cursor: -1 })
    this.say(`(${i + 1} of ${ws.length}) ${w.kind} · ${w.text.replace(/\s+/g, ' ').trim()}`, 'muted')
  }

  // dismissWait drops a notice or a failure from the list; a permission or
  // a question is answered in its thread.
  async dismissWait(i: number): Promise<void> {
    const w = this.state.waits[i]
    if (!w) return
    if (w.kind !== 'notice' && w.kind !== 'failed') return this.say('answer it in its thread: <CR> goes there', 'muted')
    await this.try(this.call('dismiss', { workspace: w.workspace, thread: w.thread, id: w.id }))
  }

  // seenFrom is when each chat on screen began to show, for the seen dwell;
  // seenSent the chats said seen since.
  private seenFrom: Record<string, number> = {}
  private seenSent: Record<string, boolean> = {}

  // see tells the hub which chats the person saw: on screen, in a window in
  // front, for SeenDwellMs without a break. It quiets what came since at
  // levels 1 and 2; what needs the person stays until resolved.
  private see(now: number): void {
    const s = this.state
    const on = new Set<string>()
    if (document.hasFocus() && s.screen === 'workspace')
      for (const w of this.wins()) if (w.buf.kind === 'chat' && !w.buf.thread.includes('/')) on.add(w.buf.thread)
    for (const k of Object.keys(this.seenFrom))
      if (!on.has(k)) {
        delete this.seenFrom[k]
        delete this.seenSent[k]
      }
    for (const t of on) {
      const level = s.threads[t]?.level ?? 0
      if (level !== 1 && level !== 2) {
        delete this.seenSent[t]
        if (level === 0) this.seenFrom[t] = now
        continue
      }
      const since = this.seenFrom[t]
      if (since === undefined) {
        this.seenFrom[t] = now
        continue
      }
      if (!this.seenSent[t] && now - since >= SeenDwellMs) {
        this.seenSent[t] = true
        void this.try(this.call('seen', { workspace: s.workspace, thread: t, at: new Date(now).toISOString() }))
      }
    }
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
    for (const [n, v] of Object.entries(s.threads)) if (!n.includes('/')) this.away[`${s.workspace}\0${n}`] = v
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
    this.echo(`${this.state.workspace} now works in ${this.short(dir)}`)
  }

  async setLocal(args: string[]): Promise<void> {
    if (!this.needWorkspace()) return
    const v = this.state.workspaces.find((w) => w.name === this.state.workspace)
    if (!v) return
    const options = ['coordinator', 'threads', 'reopen_compact', 'auto_compact']
    const values: Record<string, string> = {
      coordinator: v.coordinatorAgent ?? '',
      threads: v.threadsAgent ?? '',
      reopen_compact: v.reopenCompact ?? '',
      auto_compact: String(v.autoCompact ?? 0),
    }
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
    this.echo(`${name} restored`)
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
    return { id: nextId(), buf, back: [], fwd: [], cursor: -1, follow: true }
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
          x.id === id ? { ...x, buf, back, fwd: [], cursor: buf.kind === 'threads' ? 0 : -1, follow: buf.kind !== 'threads' } : x,
        ),
        focus: id,
      },
    })
  }

  goBack(): void {
    const w = this.cur()
    if (w?.buf.kind === 'waits' && w.back.length === 0) return this.close(w.id)
    if (!w || w.back.length === 0) return
    const buf = w.back[w.back.length - 1]
    this.updateWin(w.id, { buf, back: w.back.slice(0, -1), fwd: [...w.fwd, w.buf], cursor: buf.kind === 'threads' ? 0 : -1, follow: true })
  }

  goForward(): void {
    const w = this.cur()
    if (!w || w.fwd.length === 0) return
    const buf = w.fwd[w.fwd.length - 1]
    this.updateWin(w.id, { buf, fwd: w.fwd.slice(0, -1), back: [...w.back, w.buf], cursor: buf.kind === 'threads' ? 0 : -1, follow: true })
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

  // showThread shows a thread: where it is on screen already, else as
  // showFromList does.
  showThread(name: string): void {
    const there = this.wins().find((w) => w.buf.kind === 'chat' && w.buf.thread === name)
    if (there) {
      this.focus(there.id)
      return
    }
    this.showFromList({ kind: 'chat', thread: name })
  }

  // showFromList shows buf in the window in focus. From the board that is
  // the board's own window, beside the coordinator, and <C-o> brings the
  // board back; from the waits list, the window listWin gives.
  private showFromList(buf: BufRef): void {
    const cur = this.cur()
    const w = cur?.buf.kind === 'threads' ? cur : this.listWin()
    if (w) this.show(w.id, buf)
  }

  // listWin is the window in focus, or, from the board or the waits list,
  // the chat window that had focus last, else any window that is not a list.
  private listWin(): Win | undefined {
    const cur = this.cur()
    const list = (w: Win) => w.buf.kind === 'threads' || w.buf.kind === 'waits'
    if (!cur || !list(cur)) return cur
    const wins = this.wins()
    return wins.find((w) => w.id === this.lastChat) ?? wins.find((w) => !list(w)) ?? cur
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

  // waitsPane is :waits: the list in a window of its own under the board,
  // or in a column of its own on the right, so what is on screen stays.
  waitsPane(): void {
    const s = this.state
    const there = this.wins().find((x) => x.buf.kind === 'waits')
    if (there) return this.focus(there.id)
    const nw = { ...this.win({ kind: 'waits' }), cursor: 0, follow: false }
    const ci = s.layout.cols.findIndex((c) => c.wins.some((x) => x.buf.kind === 'threads'))
    const cols = s.layout.cols.map((c, i) => (i === ci ? { ...c, wins: [...c.wins, nw] } : c))
    if (ci < 0) cols.push({ id: nextId(), wins: [nw] })
    this.set({ layout: { cols, focus: nw.id } })
  }

  coordinator(): void {
    if (!this.needWorkspace()) return
    if (this.state.screen !== 'workspace') void this.openWorkspace(this.state.workspace)
    const w = this.wins().find((x) => x.buf.kind === 'chat' && x.buf.thread === Coordinator)
    if (w) this.focus(w.id)
    else {
      const c = this.listWin()
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
  // then failed, running, idle and resolved.
  listOrder(): string[] {
    const rank: Record<string, number> = { waiting: 0, failed: 1, running: 2, idle: 3, resolved: 4 }
    return this.state.order
      .filter((n) => n !== Coordinator)
      .map((n) => this.state.threads[n])
      .filter(Boolean)
      .sort((a, b) => (rank[this.boardGroup(a.name)] ?? 3) - (rank[this.boardGroup(b.name)] ?? 3) || b.updatedAt.localeCompare(a.updatedAt))
      .map((v) => v.name)
  }

  // boardGroup is the group a thread's row shows in.
  boardGroup(name: string): string {
    const s = this.state.threads[name]?.state ?? 'idle'
    return this.boardHold ? (this.boardAt[`${this.state.workspace}/${name}`] ?? s) : s
  }

  // settleBoard lets each row whose thread changed group take its new one,
  // unless hold: the person is on the board. It returns the rows that move.
  settleBoard(hold: boolean): string[] {
    this.boardHold = hold
    const moved: string[] = []
    const now = Date.now()
    for (const v of Object.values(this.state.threads)) {
      const k = `${this.state.workspace}/${v.name}`
      const was = this.boardAt[k]
      // A resolved thread is done with; holding it would show it as waiting.
      if (was === undefined || was === v.state || v.state === 'resolved') this.boardAt[k] = v.state
      else if (!hold) {
        this.boardAt[k] = v.state
        this.settled[k] = now
        moved.push(v.name)
      }
    }
    return moved
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
    // A new thread opens beside the chat you write in, as the TUI's thread
    // pane, so the chat and the board stay.
    const cur = this.cur()
    if (cur?.buf.kind === 'chat' && !this.wins().some((w) => w.buf.kind === 'chat' && w.buf.thread === name))
      this.split(true, { kind: 'chat', thread: name })
    else this.showThread(name)
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
    if (this.needWorkspace() && name) await this.try(this.call('resolve', this.at(name)))
  }

  async reopen(name = this.target()): Promise<void> {
    if (this.needWorkspace() && name) await this.try(this.call('reopen', this.at(name)))
  }

  async stop(name = this.target()): Promise<void> {
    if (!name) return
    const v = this.state.threads[name]
    if (v?.state !== 'running' && v?.state !== 'waiting') {
      this.say(`${name} has no turn running`, 'muted')
      return
    }
    if ((await this.try(this.call('stop', this.at(name)))) !== undefined)
      this.echo(`stopped ${name}`)
  }

  // threadRows are the rows of the threads list, as the list draws them.
  threadRows(): ({ name: string; state: string } | { name: ''; state: 'resolved-fold' })[] {
    const names = this.listOrder()
    const open = names.filter((n) => this.boardGroup(n) !== 'resolved')
    const resolved = names.filter((n) => this.boardGroup(n) === 'resolved')
    const rows: ({ name: string; state: string } | { name: ''; state: 'resolved-fold' })[] = open.map((n) => ({
      name: n,
      state: this.boardGroup(n),
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

  // images are base64, named in the text as [image 1] and so on.
  async send(thread: string, text: string, images: string[] = []): Promise<boolean> {
    const t = text.trim()
    if (!t && !images.length) return false
    const ok = await this.try(this.call('send', { ...this.at(thread), text: t, images }))
    if (ok === undefined) return false
    const w = this.cur()
    if (w) this.updateWin(w.id, { follow: true, cursor: -1 })
    this.closeRecap(thread)
    return true
  }

  async sendQueuedNow(thread: string): Promise<void> {
    await this.try(this.call('send', { ...this.at(thread), text: '', now: true }))
  }

  // takeBack is u with an empty prompt, as Esc in Claude Code: your last
  // message comes back while the agent has not answered it. A queued one
  // leaves the queue; one whose turn runs stops it and leaves the
  // transcript. It returns the text,
  // or null when there is no such message.
  async takeBack(thread: string): Promise<string | null> {
    const v = this.state.threads[thread]
    const bs = this.state.chats[thread] ?? []
    const queued = (v?.pending ?? []).filter((e) => e.from === User).pop()
    if (queued) {
      const ok = await this.try(this.call('unqueue', { ...this.at(thread), at: queued.at }))
      return ok === undefined ? '' : (queued.text ?? '')
    }
    const last = lastUserMessage(bs)
    if (last >= 0 && (v?.state === 'running' || v?.state === 'waiting')) {
      const ok = await this.try(this.call('takeBack', this.at(thread)))
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
    const w = wins.find((x) => x.buf.kind === 'chat' && (this.state.threads[x.buf.thread]?.permission || this.state.threads[x.buf.thread]?.questionId))
    if (!w || w.buf.kind !== 'chat') {
      this.say('no permission request or question in this window', 'muted')
      return
    }
    await this.answerThread(w.buf.thread, d, msg)
  }

  async answerThread(name: string, d: Decision, msg = ''): Promise<void> {
    const v = this.state.threads[name]
    if (!v?.permission && v?.questionId) {
      // A question takes a written answer; deny skips it.
      if (d !== 'deny') return this.say('write the answer, or space x to skip', 'muted')
      const ok = await this.try(this.call('answer', { ...this.at(name), id: v.questionId, decision: 'deny', message: '' }))
      if (ok !== undefined) this.echo(`skipped · ${name}`)
      return
    }
    if (!v?.permission) return
    const ok = await this.try(
      this.call('answer', { ...this.at(name), id: v.permission.id, decision: d, message: msg }),
    )
    if (ok !== undefined) this.echo(`${d} · ${v.permission.tool} · ${name}`)
  }

  completions(thread: string, query: string) {
    return this.call('completions', { ...this.at(thread), query })
  }

  choices(thread: string) {
    return this.call('choices', this.at(thread))
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

  toggleTodo(thread: string): void {
    this.set({ todoOpen: { ...this.state.todoOpen, [thread]: !this.state.todoOpen[thread] } })
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
    this.set({ docs: { ...this.state.docs, [name]: { artifact: r.artifact, text: r.text, edit: d?.edit ?? null, conflicts: d?.conflicts } } })
  }

  startEdit(name: string): void {
    const d = this.state.docs[name]
    if (!d || d.edit) return
    this.set({
      docs: { ...this.state.docs, [name]: { ...d, edit: { text: d.text, base: d.artifact.version, baseText: d.text, start: d.text } } },
    })
  }

  // editText takes a change in the document. The first one starts an edit;
  // undoing back to the saved text ends it, so newer versions show again.
  editText(name: string, text: string): void {
    const d = this.state.docs[name]
    if (!d) return
    const edit = d.edit ?? { text: d.text, base: d.artifact.version, baseText: d.text, start: d.text }
    const done = text === edit.baseText && !d.conflicts?.length
    this.set({ docs: { ...this.state.docs, [name]: { ...d, edit: done ? null : { ...edit, text } } } })
  }

  dirty(name: string): boolean {
    const e = this.state.docs[name]?.edit
    return !!e && e.text !== e.baseText
  }

  // save is :w. The hub merges by blocks with a newer version; changes to
  // the same lines are refused unless force (:w!).
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
      // As the TUI: the edit now starts from the newer version, so the next
      // :w saves what you made of both over it.
      await this.mergeIn(name)
      return false
    }
    const merged = r.text !== d.edit.text
    this.set({ docs: { ...this.state.docs, [name]: { artifact: r.artifact, text: r.text, edit: null } } })
    this.echo(`${merged ? 'merged and saved' : 'saved'} ${name} v${r.artifact.version} · ${r.artifact.thread} sees it with your next message`)
    return true
  }

  // mergeIn takes an agent's newer version into the edit at once, through
  // the hub's merge that writes nothing: a clean merge shows in the page in
  // place, and :w then saves it as it is.
  private async mergeIn(name: string): Promise<void> {
    await this.loadDoc(name)
    for (let tries = 0; tries < 3; tries++) {
      const d = this.state.docs[name]
      if (!d?.edit || d.edit.base === d.artifact.version) return
      const sent = d.edit.text
      const m = await this.try(
        this.call('mergeArtifact', { workspace: this.state.workspace, name, text: sent, base: d.edit.base, baseText: d.edit.baseText }),
      )
      if (!m) return
      const cur = this.state.docs[name]
      if (!cur?.edit) return
      // You typed, or it wrote again, while it merged: merge from what is
      // there now.
      if (cur.edit.text !== sent || m.version !== cur.artifact.version) {
        await this.loadDoc(name)
        continue
      }
      const ours = m.merged.split('\n')
      const theirs = cur.text.split('\n')
      const conflicts = (m.conflicts ?? []).map((c) => ({
        mine: ours.slice(...c.merged).join('\n'),
        theirs: theirs.slice(...c.theirs).join('\n'),
        prev: ours.slice(0, c.merged[0]).join('\n').replace(/\n+$/, '').split(/\n\n+/).pop() ?? '',
      }))
      const edit = m.merged === cur.text ? null : { ...cur.edit, text: m.merged, base: m.version, baseText: cur.text }
      this.set({ docs: { ...this.state.docs, [name]: { ...cur, edit, conflicts } } })
      const n = conflicts.length
      if (n) this.say(`${cur.artifact.thread} changed the same blocks in v${m.version}`, 'gold')
      else this.echo(`${cur.artifact.thread} wrote v${m.version} · merged into your edit`)
      return
    }
  }

  // keepConflicts keeps only the conflicts still open in the page: taking
  // theirs, or writing in the block yourself, decides one.
  keepConflicts(name: string, open: number[]): void {
    const d = this.state.docs[name]
    if (!d?.conflicts) return
    this.set({ docs: { ...this.state.docs, [name]: { ...d, conflicts: d.conflicts.filter((_c, i) => open.includes(i)) } } })
  }

  // revert is :e! (drop the changes, take the newest) and :q! on an artifact.
  async revert(name: string): Promise<void> {
    const d = this.state.docs[name]
    if (!d) return
    this.set({ docs: { ...this.state.docs, [name]: { ...d, edit: null, conflicts: undefined } } })
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

  openNewDoc(open = true): void {
    if (open && !this.needWorkspace()) return
    this.set({ newDoc: open, picker: null, cmdline: null })
  }

  // newDoc starts a document the person writes, beside the window in focus.
  // The coordinator holds it and works from it.
  async newDoc(name: string): Promise<boolean> {
    if (!(await this.makePage(name))) return false
    this.writeDoc = name
    this.split(true)
    await this.openArtifact(name)
    return true
  }

  // makePage starts an artifact with only its title, for the person to write.
  async makePage(name: string): Promise<boolean> {
    if (!this.needWorkspace()) return false
    const bad = validName(name)
    if (bad) {
      this.say(bad, 'love')
      return false
    }
    const title = name.charAt(0).toUpperCase() + name.slice(1).replace(/-/g, ' ')
    const text = `# ${title}\n\n`
    return !!(await this.try(this.call('editArtifact', { workspace: this.state.workspace, name, text, base: 0, baseText: '', force: false })))
  }

  openNewWorkspace(open = true): void {
    this.set({ newWorkspace: open, picker: null, cmdline: null })
  }

  openSettings(open = true): void {
    this.set({ settingsOpen: open, helpOpen: false, picker: null, cmdline: null })
  }
  openChoice(c: { thread: string; kind: 'model' | 'effort' } | null): void {
    this.set({ choice: c, picker: null, cmdline: null })
  }

  openHelp(open = true): void {
    this.set({ helpOpen: open, settingsOpen: false, picker: null, cmdline: null })
  }

  // openCost opens the cost dialog; with no workspace open it shows all.
  openCost(scope: 'workspace' | 'all' | null): void {
    if (scope === 'workspace' && (!this.state.workspace || this.state.screen !== 'workspace')) scope = 'all'
    this.set({ cost: scope, helpOpen: false, settingsOpen: false, picker: null, cmdline: null })
  }

  costOf(scope: 'workspace' | 'all') {
    return this.call('cost', { workspace: scope === 'all' ? '' : this.state.workspace })
  }


  setWorkspaceCursor(i: number): void {
    const n = this.state.workspaces.length
    this.set({ workspaceCursor: Math.max(0, Math.min(i, n - 1)) })
  }

  async startHub(): Promise<void> {
    this.say('starting the hub…', 'muted')
    await this.try(nh().startHub())
  }

  // newer is the installed neoharness while the hub runs an older one: the
  // hub restarts on it as soon as no turn runs, so an update cuts no turn.
  private newer = ''

  private async checkVersion(): Promise<void> {
    const [info, own] = await Promise.all([this.call('info', undefined).catch(() => null), nh().installedVersion().catch(() => '')])
    this.newer = info?.version && own && info.version !== own ? own : ''
    if (!this.newer) return
    if (this.running() > 0) this.say('a new neoharness is installed; the hub restarts on it when the turns end', 'muted')
    this.restartWhenIdle()
  }

  private running(): number {
    return this.state.workspaces.reduce((n, w) => n + (w.counts.running ?? 0) + (w.coordinator === 'running' ? 1 : 0), 0)
  }

  private restartWhenIdle(): void {
    if (!this.newer || this.running() > 0) return
    this.newer = ''
    void this.restartHub('the hub restarts on the new neoharness')
  }

  async restartHub(why = 'restarting the hub…'): Promise<void> {
    this.say(why, 'muted')
    await this.try(nh().restartHub())
  }

  // askRestartHub restarts the hub, after a yes when turns run: they stop.
  askRestartHub(): void {
    const n = this.running()
    if (!n) return void this.restartHub()
    this.set({ confirm: { text: `${n} turn${n === 1 ? '' : 's'} running stop with the hub. Restart it? (y/n)`, run: () => void this.restartHub() } })
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
        if (!a[0]) return this.openPicker('threads', true, true)
        const buf = this.findBuffer(a[0])
        if (!buf) return this.say(`no buffer ${a[0]}`, 'love')
        return this.showFromList(buf)
      }
      case 'ls':
      case 'buffers':
        return this.openPicker('threads', true, true)
      case 'messages':
      case 'mes':
        if (w) this.show(w.id, { kind: 'messages' })
        return
      case 'waits':
      case 'copen':
      case 'cope':
      case 'cw':
      case 'cwindow':
        return this.waitsPane()
      case 'cn':
      case 'cnext':
        return this.nextWait(1)
      case 'cp':
      case 'cN':
      case 'cprevious':
      case 'cNext':
        return this.nextWait(-1)
      case 'recap':
        return this.openRecap()
      case 'todo': {
        const cur = w?.buf.kind === 'chat' && !w.buf.thread.includes('/') ? w.buf.thread : Coordinator
        const thread = a[0] || cur
        if (!this.state.threads[thread]) return this.say(`no thread ${thread}`, 'love')
        if (w) this.show(w.id, { kind: 'todo', thread })
        return
      }
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
        return this.openHelp(true)
      case 'hub':
        if (a[0] === 'restart') return this.askRestartHub()
        return this.say('the hub commands here: :hub restart', 'love')
      case 'cost':
        return this.openCost(a[0] === 'all' ? 'all' : 'workspace')
      case 'table':
        if (!art || !w) return this.say(':table works in an artifact', 'muted')
        document.dispatchEvent(new CustomEvent('nh:table', { detail: { win: w.id, args: a } }))
        return
      case 'page':
        if (!art || !w) return this.say(':page works in an artifact', 'muted')
        if (!a[0]) return this.say('usage: :page <name>', 'love')
        document.dispatchEvent(new CustomEvent('nh:page', { detail: { win: w.id, name: a[0] } }))
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
      case 'doc':
        if (!a[0]) return this.openNewDoc()
        void this.newDoc(a[0])
        return
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
    if (name === 'messages') return { kind: 'messages' }
    if (name === 'waits') return { kind: 'waits' }
    const ms = this.state.order.filter((n) => n.includes(name))
    return ms.length === 1 ? { kind: 'chat', thread: ms[0] } : null
  }
}

function waitKey(w: Wait): string {
  return `${w.workspace}\0${w.thread}\0${w.id}`
}

export function sameBuf(a: BufRef, b: BufRef): boolean {
  if (a.kind !== b.kind) return false
  if (a.kind === 'chat' && b.kind === 'chat') return a.thread === b.thread
  if (a.kind === 'artifact' && b.kind === 'artifact') return a.name === b.name
  if (a.kind === 'todo' && b.kind === 'todo') return a.thread === b.thread
  return true
}

export const store = new Store()

export function useStore<T>(pick: (s: State) => T): T {
  return useSyncExternalStore(store.subscribe, () => pick(store.state))
}
