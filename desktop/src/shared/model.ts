// The wire types of api/model. Keep them in step with model.go: the app
// sees only these neutral fields, never an agent's own.

// failed: the last turn ended in an error and nothing came after it.
// background: the turn ended, but work the agent started runs on.
export type State = 'idle' | 'running' | 'waiting' | 'failed' | 'resolved' | 'background'
export type Role = 'coordinator' | 'thread'
export type Decision = 'allow' | 'always' | 'deny'

export const Coordinator = 'coordinator'
export const User = 'user'

export interface Location {
  host?: string
  dir: string
}

export type EventKind =
  | 'message'
  | 'text'
  | 'tool'
  | 'turn'
  | 'error'
  | 'permission'
  | 'answer'
  | 'ask'
  | 'progress'
  | 'note'
  | 'clear'
  | 'shell'
  | 'artifact'
  | 'notice'
  | 'takeback'

export interface Event {
  at: string
  kind: EventKind
  from?: string
  text?: string
  tool?: string
  detail?: string
  id?: string
  tools?: number
  tokens?: number
  exit?: number
  // background is how many background tasks a turn end left running.
  background?: number
  version?: number
  needsYou?: boolean
  // level is how much of the person's attention the event asks for, on the
  // attention ladder (Level); the hub sets it.
  level?: Level
}

// Level is a step of the attention ladder: 0 quiet, 1 ambient, 2 notice,
// 3 needs you.
export type Level = 0 | 1 | 2 | 3

// SeenDwellMs is how long a chat is on screen, in a window in front, before
// the app tells the hub it was seen (model.SeenDwell).
export const SeenDwellMs = 2000

// Wait is one item of the waits list: what needs the person, in any
// workspace. id names it to answer or dismiss.
export interface Wait {
  workspace: string
  thread: string
  kind: 'permission' | 'question' | 'failed' | 'notice'
  id: string
  text: string
  since: string
}

export interface Artifact {
  name: string
  title: string
  thread: string
  version: number
  userEdit?: boolean
  createdAt: string
  updatedAt: string
}

export interface Permission {
  id: string
  tool: string
  detail: string
  // What "always" allows; absent when the hub offers no "always".
  scope?: string
}

// model.RecapAway.
export const RecapAwayMs = 2 * 60 * 1000

export interface ThreadView {
  workspace: string
  name: string
  role: Role
  location: Location
  state: State
  queued: number
  // background is how many tasks the agent runs in the background.
  background?: number
  wakeAt?: string
  waiting?: string
  // The question the running turn waits on, when waiting is one: writing
  // answers it, and a deny with this id skips it.
  questionId?: string
  permission?: Permission
  pending?: Event[]
  // The start of the last turn's reply; absent while a turn runs.
  reply?: string
  // Where the session's work stands, in its own words; it shows above the
  // prompt when the person comes back after RecapAwayMs.
  recap?: string
  recapAt?: string
  // The todo list the session keeps while it works, in the order it made
  // the items.
  todos?: Todo[]
  quiet?: boolean
  // level is the highest attention level still open on the thread: what
  // waits on the person, or what came since seenAt.
  level?: Level
  seenAt?: string
  model?: string
  context?: number
  contextWindow?: number
  branch?: string
  since: string
  updatedAt: string
  createdAt: string
}

export interface Todo {
  id: string
  text: string
  state: 'pending' | 'active' | 'done'
}

export interface Completions {
  commands: string[]
  files: string[]
  threads: string[]
  many?: boolean
  query?: string
}

// TurnCost is what one turn cost, from a workspace's cost log.
export interface TurnCost {
  at: string
  workspace?: string
  thread: string
  model?: string
  cost: number
  tools?: number
}

// CostReport is what a workspace, or every workspace, cost. Days, models and top
// count only the turns in the cost logs, from since on.
export interface CostReport {
  total: number
  today: number
  since: string
  threads: CostPart[]
  workspaces: CostPart[]
  models: CostPart[]
  days: CostPart[]
  top: TurnCost[]
}

export interface CostPart {
  workspace?: string
  name: string
  cost: number
  turns?: number
}

// Choices are what /model and /effort of a thread take; an empty model or
// effort is the agent's default.
export interface Choices {
  model: string
  effort: string
  models: ModelChoice[]
  efforts: string[]
}

export interface ModelChoice {
  name: string
  label?: string
  version?: string
  description?: string
  efforts?: string[]
}

export interface WorkspaceView {
  name: string
  pin?: string
  createdAt: string
  counts: Partial<Record<State, number>>
  coordinator?: State
  activity: number[]
  coordinatorAgent?: string
  threadsAgent?: string
  agentNames?: string[]
  // What a resolved thread with a full context does when the coordinator
  // reopens it: compact, ask or off.
  reopenCompact?: string
  // The context percent at which a session compacts before its next turn;
  // 0 never.
  autoCompact?: number
}

export type UpdateKind = 'event' | 'thread' | 'workspace' | 'deleted' | 'artifact' | 'stopped' | 'waits' | 'delta'

export interface Update {
  kind: UpdateKind
  workspace: string
  thread?: string
  event?: Event
  view?: ThreadView
  summary?: WorkspaceView
  artifact?: Artifact
  // waits is the whole waits list, oldest first, on a "waits" update.
  waits?: Wait[]
  // text is the reply so far on a "delta" update; empty drops it.
  text?: string
}

export interface HubInfo {
  version?: string
  pid: number
  started: string
}

export interface ArtifactText {
  artifact: Artifact
  text: string
}

// Merge is what an edit becomes when the hub merges it into the newest
// version. Where both sides changed a block, merged keeps the edit's side and
// conflicts say where, as line ranges [first, end) from 0.
export interface Merge {
  merged: string
  clean: boolean
  version: number
  conflicts?: { base: [number, number]; ours: [number, number]; theirs: [number, number]; merged: [number, number] }[]
}

export function location(l: Location): string {
  return l.host ? `${l.host}:${l.dir}` : l.dir
}

const nameRE = /^[a-z0-9][a-z0-9-]{0,39}$/

export function validName(name: string): string | null {
  return nameRE.test(name)
    ? null
    : `name "${name}" must be lowercase letters, digits and dashes, at most 40 characters`
}
