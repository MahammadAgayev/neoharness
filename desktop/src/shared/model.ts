// The wire types of api/model. Keep them in step with model.go: the app
// sees only these neutral fields, never an agent's own.

export type State = 'idle' | 'running' | 'waiting' | 'resolved'
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
  version?: number
  needsYou?: boolean
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

// Recap is what a thread's work produced. asked, outcome and open are the
// thread's words; the rest is what the hub saw its last turn do.
export interface Recap {
  asked?: string
  outcome?: string
  // What a row shows of outcome: its first sentence, short.
  headline?: string
  open?: string
  fromReply?: boolean
  ran?: number
  // The commands whose last run failed.
  failed?: number
  commands?: RecapCommand[]
  edited?: string[]
  artifacts?: string[]
  error?: string
  at: string
  // What the thread is for and how its tasks stand, in its own words: its
  // summary.
  goal?: string
  done?: RecapItem[]
  doing?: RecapItem[]
  needs?: RecapItem[]
}

// RecapItem is one task of a summary; thread or artifact is what opens it.
export interface RecapItem {
  text: string
  thread?: string
  artifact?: string
}

export interface RecapCommand {
  text: string
  failed?: boolean
}

// RecapAway is how long the person must be away before coming back shows
// a toast, when there is news they have not seen: model.RecapAway.
export const RecapAway = 2 * 60_000

// WorkspaceRecap is how a workspace stands: the coordinator's goal and
// lines, and what the hub joins from the threads' recaps.
export interface WorkspaceRecap {
  goal?: string
  lines?: string[]
  at?: string
  overlaps?: Overlap[]
  failed?: number
  // The open threads whose recap is newer than the lines.
  newer?: number
}

export interface Overlap {
  path: string
  threads: string[]
}

export interface ThreadView {
  workspace: string
  name: string
  role: Role
  location: Location
  state: State
  queued: number
  wakeAt?: string
  waiting?: string
  permission?: Permission
  pending?: Event[]
  recap?: Recap
  // The recap is newer than the last time the person closed a box that
  // showed it.
  recapUnseen?: boolean
  quiet?: boolean
  model?: string
  context?: number
  contextWindow?: number
  cost?: number
  branch?: string
  since: string
  updatedAt: string
  createdAt: string
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
  // cost is what every thread of the workspace cost, the coordinator too.
  cost?: number
  coordinatorAgent?: string
  threadsAgent?: string
  agentNames?: string[]
  // What a resolved thread with a full context does when the coordinator
  // reopens it: compact, ask or off.
  reopenCompact?: string
  // The context percent at which a session compacts before its next turn;
  // 0 never.
  autoCompact?: number
  recap?: WorkspaceRecap
  // An open thread's recap, or the coordinator's lines, came after the
  // person last closed the workspace's recap box.
  recapUnseen?: boolean
}

export type UpdateKind = 'event' | 'thread' | 'workspace' | 'deleted' | 'artifact' | 'stopped'

export interface Update {
  kind: UpdateKind
  workspace: string
  thread?: string
  event?: Event
  view?: ThreadView
  summary?: WorkspaceView
  artifact?: Artifact
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

export function location(l: Location): string {
  return l.host ? `${l.host}:${l.dir}` : l.dir
}

const nameRE = /^[a-z0-9][a-z0-9-]{0,39}$/

export function validName(name: string): string | null {
  return nameRE.test(name)
    ? null
    : `name "${name}" must be lowercase letters, digits and dashes, at most 40 characters`
}
