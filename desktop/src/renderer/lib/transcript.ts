// A transcript as blocks, as tui/buffer.go builds them from events:
// a turn's tool calls fold into one block, a permission's answer goes onto
// its card, a shell command's output onto its command.

import { User, type Event } from '../../shared/model'

export interface ToolCall {
  tool: string
  detail: string
  id: string
  failed?: string
}

interface Base {
  key: number
}

export type Block = Base &
  (
    | { kind: 'message'; from: string; text: string }
    | { kind: 'text'; text: string }
    | { kind: 'tools'; tools: ToolCall[]; live: boolean }
    | { kind: 'error'; text: string }
    | { kind: 'permission'; tool: string; detail: string; answer: string }
    | { kind: 'answer'; text: string }
    | { kind: 'ask'; text: string }
    | { kind: 'progress'; text: string }
    | { kind: 'note'; text: string }
    | { kind: 'notice'; text: string; needsYou: boolean }
    | { kind: 'shell'; id: string; command: string; output: string; exit: number; live: boolean }
    | { kind: 'artifact'; name: string; title: string; version: number; userEdit: boolean }
  )

let next = 1
const key = () => next++

function endLive(bs: Block[]): Block[] {
  const last = bs[bs.length - 1]
  if (last && 'live' in last && last.live) return [...bs.slice(0, -1), { ...last, live: false }]
  return bs
}

// settle stops every activity except, while the thread runs, its last tool
// calls. History may end mid-turn, and a stopped turn sends no turn end.
export function settle(bs: Block[], running: boolean): Block[] {
  let lastTools = -1
  bs.forEach((b, i) => {
    if (b.kind === 'tools') lastTools = i
  })
  let changed = false
  const out = bs.map((b, i) => {
    if ('live' in b && b.live && !(running && i === lastTools)) {
      changed = true
      return { ...b, live: false }
    }
    return b
  })
  return changed ? out : bs
}

export function addEvent(bs: Block[], e: Event): Block[] {
  const text = e.text ?? ''
  switch (e.kind) {
    case 'message':
      return [...endLive(bs), { key: key(), kind: 'message', from: e.from ?? '', text }]
    case 'text':
      return [...endLive(bs), { key: key(), kind: 'text', text }]
    case 'tool': {
      if (e.exit) return failTool(bs, e.id ?? '', text)
      const call: ToolCall = { tool: e.tool ?? '', detail: e.detail ?? '', id: e.id ?? '' }
      const last = bs[bs.length - 1]
      if (last && last.kind === 'tools' && last.live) {
        return [...bs.slice(0, -1), { ...last, tools: [...last.tools, call] }]
      }
      // Only the newest activity spins; a permission card between two
      // tool calls splits them into two blocks.
      const quiet = bs.map((b) => (b.kind === 'tools' && b.live ? { ...b, live: false } : b))
      return [...quiet, { key: key(), kind: 'tools', tools: [call], live: true }]
    }
    case 'turn':
      return settle(bs, false)
    case 'error':
      return [...settle(bs, false), { key: key(), kind: 'error', text }]
    case 'permission':
      return [...bs, { key: key(), kind: 'permission', tool: e.tool ?? '', detail: e.detail ?? '', answer: '' }]
    case 'answer': {
      for (let i = bs.length - 1; i >= 0; i--) {
        const b = bs[i]
        if (b.kind === 'permission' && b.answer === '') {
          const out = bs.slice()
          out[i] = { ...b, answer: text }
          return out
        }
      }
      return [...bs, { key: key(), kind: 'answer', text }]
    }
    case 'ask':
      return [...bs, { key: key(), kind: 'ask', text }]
    case 'progress':
      return [...bs, { key: key(), kind: 'progress', text }]
    case 'note':
      return [...bs, { key: key(), kind: 'note', text }]
    case 'notice':
      return [...bs, { key: key(), kind: 'notice', text, needsYou: !!e.needsYou }]
    case 'shell': {
      const i = bs.findIndex((b) => b.kind === 'shell' && b.id === e.id)
      if (i >= 0) {
        const out = bs.slice()
        out[i] = { ...(bs[i] as Extract<Block, { kind: 'shell' }>), output: text, exit: e.exit ?? 0, live: false }
        return out
      }
      return [
        ...endLive(bs),
        { key: key(), kind: 'shell', id: e.id ?? '', command: e.detail ?? '', output: '', exit: 0, live: true },
      ]
    }
    case 'artifact':
      return [
        ...endLive(bs),
        {
          key: key(),
          kind: 'artifact',
          name: e.detail ?? '',
          title: text,
          version: e.version ?? 0,
          userEdit: e.from === User,
        },
      ]
    case 'clear':
      return [{ key: key(), kind: 'note', text }]
  }
  return bs
}

function failTool(bs: Block[], id: string, why: string): Block[] {
  if (!id) return bs
  for (let i = bs.length - 1; i >= 0; i--) {
    const b = bs[i]
    if (b.kind !== 'tools') continue
    const k = b.tools.findIndex((t) => t.id === id)
    if (k >= 0) {
      const tools = b.tools.slice()
      tools[k] = { ...tools[k], failed: why || 'failed' }
      const out = bs.slice()
      out[i] = { ...b, tools }
      return out
    }
  }
  return bs
}

export function fromEvents(evs: Event[], running: boolean): Block[] {
  let bs: Block[] = []
  for (const e of evs) bs = addEvent(bs, e)
  return settle(bs, running)
}

// lastText is the first line of the newest reply.
export function lastText(bs: Block[]): string {
  for (let i = bs.length - 1; i >= 0; i--) {
    const b = bs[i]
    if (b.kind === 'text') return firstLine(b.text)
  }
  return ''
}

// lastUserMessage is the index of the last message to the thread when it is
// from the person; -1 when the last message is someone else's or none.
export function lastUserMessage(bs: Block[]): number {
  for (let i = bs.length - 1; i >= 0; i--) {
    const b = bs[i]
    if (b.kind === 'message') return b.from === User ? i : -1
  }
  return -1
}

export function firstLine(s: string): string {
  return s.trim().split('\n')[0] ?? ''
}

// shortTool shows an MCP tool as server·tool instead of mcp__server__tool.
export function shortTool(name: string): string {
  if (!name.startsWith('mcp__')) return name
  const rest = name.slice(5)
  const i = rest.indexOf('__')
  return i < 0 ? name : `${rest.slice(0, i)}·${rest.slice(i + 2)}`
}

export function shortTools(s: string): string {
  return s.replace(/mcp__([A-Za-z0-9_-]+?)__([A-Za-z0-9_-]+)/g, '$1·$2')
}

// The text of a block, as y yanks it and the transcript search reads it.
export function blockText(b: Block): string {
  switch (b.kind) {
    case 'message':
    case 'text':
    case 'error':
    case 'answer':
    case 'ask':
    case 'progress':
    case 'note':
    case 'notice':
      return b.text
    case 'tools':
      return b.tools.map((t) => `${shortTool(t.tool)}  ${shortTools(t.detail)}`).join('\n')
    case 'permission':
      return `${shortTool(b.tool)}  ${b.detail}${b.answer ? `  · ${b.answer}` : ''}`
    case 'shell':
      return `!${b.command}\n${b.output}`
    case 'artifact':
      return `${b.title} · ${b.name}`
  }
}
