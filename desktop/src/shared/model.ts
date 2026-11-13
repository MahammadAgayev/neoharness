// The wire types of internal/model. Keep them in step with model.go: the app
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
}

export interface ThreadView {
  workspace: string
  name: string
  role: Role
  location: Location
  state: State
  queued: number
  progress?: string
  wakeAt?: string
  waiting?: string
  permission?: Permission
  pending?: Event[]
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

// TurnCost is what one turn cost, from a workspace's spend log.
export interface TurnCost {
  at: string
  workspace?: string
  thread: string
  model?: string
  cost: number
  tools?: number
}

// Spend is what a workspace, or every workspace, cost. Days, models and top
// count only the turns in the spend logs, from since on.
export interface Spend {
  total: number
  today: number
  since: string
  threads: SpendPart[]
  workspaces: SpendPart[]
  models: SpendPart[]
  days: SpendPart[]
  top: TurnCost[]
}

export interface SpendPart {
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
