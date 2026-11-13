// The hub calls the app makes, as in internal/hubapi. The renderer names a
// call and its arguments; only the main process turns them into requests, so
// the page cannot reach any other path of the socket.

import type {
  Artifact,
  ArtifactText,
  Completions,
  Decision,
  Event,
  HubInfo,
  ThreadView,
  WorkspaceView,
} from './model'

interface Req {
  method: 'GET' | 'POST' | 'PUT' | 'DELETE'
  path: string
  body?: unknown
}

const e = encodeURIComponent
const ws = (w: string) => `/workspaces/${e(w)}`
const th = (w: string, t: string) => `${ws(w)}/threads/${e(t)}`

export const calls = {
  info: (): Req => ({ method: 'GET', path: '/hub' }),
  workspaces: (): Req => ({ method: 'GET', path: '/workspaces' }),
  createWorkspace: (a: { name: string; pin?: string }): Req => ({
    method: 'POST',
    path: '/workspaces',
    body: { name: a.name, pin: a.pin ?? '' },
  }),
  setFolder: (a: { workspace: string; pin: string }): Req => ({
    method: 'PUT',
    path: `${ws(a.workspace)}/folder`,
    body: { pin: a.pin },
  }),
  setLocal: (a: { workspace: string; option: string; agent: string }): Req => ({
    method: 'PUT',
    path: `${ws(a.workspace)}/local`,
    body: { option: a.option, agent: a.agent },
  }),
  deleteWorkspace: (a: { workspace: string }): Req => ({ method: 'DELETE', path: ws(a.workspace) }),
  deletedWorkspaces: (): Req => ({ method: 'GET', path: '/deleted' }),
  restoreWorkspace: (a: { workspace: string }): Req => ({
    method: 'POST',
    path: `${ws(a.workspace)}/restore`,
  }),
  artifacts: (a: { workspace: string }): Req => ({ method: 'GET', path: `${ws(a.workspace)}/artifacts` }),
  artifact: (a: { workspace: string; name: string }): Req => ({
    method: 'GET',
    path: `${ws(a.workspace)}/artifacts/${e(a.name)}`,
  }),
  editArtifact: (a: {
    workspace: string
    name: string
    text: string
    base: number
    baseText: string
    force?: boolean
  }): Req => ({
    method: 'PUT',
    path: `${ws(a.workspace)}/artifacts/${e(a.name)}`,
    body: { text: a.text, base: a.base, baseText: a.baseText, force: a.force ?? false },
  }),
  threads: (a: { workspace: string }): Req => ({ method: 'GET', path: `${ws(a.workspace)}/threads` }),
  openThread: (a: { workspace: string; name: string; brief?: string; location?: string }): Req => ({
    method: 'POST',
    path: `${ws(a.workspace)}/threads`,
    body: { name: a.name, brief: a.brief ?? '', location: a.location ?? '' },
  }),
  events: (a: { workspace: string; thread: string; tail: number }): Req => ({
    method: 'GET',
    path: `${th(a.workspace, a.thread)}/events?tail=${a.tail}`,
  }),
  completions: (a: { workspace: string; thread: string; query: string }): Req => ({
    method: 'GET',
    path: `${th(a.workspace, a.thread)}/completions?q=${e(a.query)}`,
  }),
  send: (a: { workspace: string; thread: string; text: string; now?: boolean }): Req => ({
    method: 'POST',
    path: `${th(a.workspace, a.thread)}/messages`,
    body: { text: a.text, now: a.now ?? false },
  }),
  stop: (a: { workspace: string; thread: string }): Req => ({
    method: 'POST',
    path: `${th(a.workspace, a.thread)}/stop`,
  }),
  unqueue: (a: { workspace: string; thread: string; at: string }): Req => ({
    method: 'DELETE',
    path: `${th(a.workspace, a.thread)}/queue?at=${e(a.at)}`,
  }),
  resolve: (a: { workspace: string; thread: string }): Req => ({
    method: 'POST',
    path: `${th(a.workspace, a.thread)}/resolve`,
  }),
  reopen: (a: { workspace: string; thread: string }): Req => ({
    method: 'POST',
    path: `${th(a.workspace, a.thread)}/reopen`,
  }),
  deleteThread: (a: { workspace: string; thread: string }): Req => ({
    method: 'DELETE',
    path: th(a.workspace, a.thread),
  }),
  answer: (a: { workspace: string; thread: string; id: string; decision: Decision; message?: string }): Req => ({
    method: 'POST',
    path: `${th(a.workspace, a.thread)}/answer`,
    body: { id: a.id, decision: a.decision, message: a.message ?? '' },
  }),
}

export type CallName = keyof typeof calls
export type CallArgs<N extends CallName> = Parameters<(typeof calls)[N]>[0]

// What each call returns; calls not named here return nothing.
export interface Results {
  info: HubInfo
  workspaces: WorkspaceView[]
  deletedWorkspaces: string[]
  artifacts: Artifact[]
  artifact: ArtifactText
  editArtifact: ArtifactText
  threads: ThreadView[]
  events: Event[]
  completions: Completions
}

export type Result<N extends CallName> = N extends keyof Results ? Results[N] : void

// HubStatus is the connection as the app shows it. "stopped" is a hub that
// was asked to stop: the app does not start a new one by itself.
export type HubStatus = 'connecting' | 'connected' | 'lost' | 'stopped'

// The bridge the preload script gives the page as window.nh.
export interface Bridge {
  call<N extends CallName>(name: N, args: CallArgs<N>): Promise<Result<N>>
  onUpdate(fn: (u: import('./model').Update) => void): () => void
  onStatus(fn: (s: HubStatus, detail: string) => void): () => void
  startHub(): Promise<void>
  pickFolder(): Promise<string | null>
  openExternal(url: string): Promise<void>
  notify(title: string, body: string, target: { workspace: string; thread: string }): void
  onOpenTarget(fn: (t: { workspace: string; thread: string }) => void): () => void
  setBadge(n: number): void
  copy(text: string): void
  home(): string
}
