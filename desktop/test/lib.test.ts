import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

import { describe, expect, it } from 'vitest'

import { folders } from '../src/main/folders'

import { parseLine } from '../src/main/hub'
import { calls } from '../src/shared/api'
import type { Event, ThreadView } from '../src/shared/model'
import { parseEx, words } from '../src/renderer/lib/ex'
import { fuzzy, fuzzyScore, grep } from '../src/renderer/lib/fuzzy'
import { keyName, Sequencer, split } from '../src/renderer/lib/keys'
import { hunks, lines, take } from '../src/renderer/lib/linediff'
import { colors, contrast, ratios, resolveScheme, schemes } from '../src/renderer/lib/themes'
import { keep, viewToast, type Toast } from '../src/renderer/lib/toasts'
import { addEvent, fromEvents, lastText, lastUserMessage, shortTool, type Block } from '../src/renderer/lib/transcript'

const at = '2026-11-13T10:00:00Z'
const ev = (e: Partial<Event>): Event => ({ at, kind: 'text', ...e }) as Event

describe('transcript', () => {
  it('folds a turn of tool calls into one block that stops spinning at the turn end', () => {
    const bs = fromEvents(
      [
        ev({ kind: 'message', from: 'user', text: 'fix it' }),
        ev({ kind: 'tool', tool: 'Read', detail: 'a.go', id: '1' }),
        ev({ kind: 'tool', tool: 'Edit', detail: 'a.go', id: '2' }),
        ev({ kind: 'text', text: 'done' }),
        ev({ kind: 'turn' }),
      ],
      false,
    )
    expect(bs.map((b) => b.kind)).toEqual(['message', 'tools', 'text'])
    const tools = bs[1] as Extract<Block, { kind: 'tools' }>
    expect(tools.tools.map((t) => t.tool)).toEqual(['Read', 'Edit'])
    expect(tools.live).toBe(false)
  })

  it('splits tool calls at a permission card, and only the newest spins', () => {
    let bs: Block[] = []
    bs = addEvent(bs, ev({ kind: 'tool', tool: 'Read', id: '1' }))
    bs = addEvent(bs, ev({ kind: 'permission', tool: 'Bash', detail: 'rm -rf x', id: 'p' }))
    bs = addEvent(bs, ev({ kind: 'tool', tool: 'Bash', id: '2' }))
    const tools = bs.filter((b) => b.kind === 'tools') as Extract<Block, { kind: 'tools' }>[]
    expect(tools).toHaveLength(2)
    expect(tools.map((t) => t.live)).toEqual([false, true])
  })

  it("puts a permission's answer on its card", () => {
    let bs: Block[] = []
    bs = addEvent(bs, ev({ kind: 'permission', tool: 'Bash', detail: 'ls' }))
    bs = addEvent(bs, ev({ kind: 'answer', text: 'deny: no' }))
    expect(bs).toHaveLength(1)
    expect((bs[0] as Extract<Block, { kind: 'permission' }>).answer).toBe('deny: no')
  })

  it('marks a failed tool call by its id', () => {
    let bs: Block[] = []
    bs = addEvent(bs, ev({ kind: 'tool', tool: 'Bash', id: 'x' }))
    bs = addEvent(bs, ev({ kind: 'tool', id: 'x', exit: 1, text: 'exit 2' }))
    expect((bs[0] as Extract<Block, { kind: 'tools' }>).tools[0].failed).toBe('exit 2')
  })

  it("gives a shell command its output when it ends", () => {
    let bs: Block[] = []
    bs = addEvent(bs, ev({ kind: 'shell', id: 's', detail: 'go test' }))
    bs = addEvent(bs, ev({ kind: 'shell', id: 's', detail: 'go test', text: 'ok\n', exit: 0 }))
    expect(bs).toHaveLength(1)
    const b = bs[0] as Extract<Block, { kind: 'shell' }>
    expect([b.output, b.live]).toEqual(['ok\n', false])
  })

  it('starts again at a clear', () => {
    const bs = fromEvents([ev({ text: 'a' }), ev({ kind: 'clear', text: 'cleared' })], false)
    expect(bs.map((b) => b.kind)).toEqual(['note'])
  })

  it('keeps the last tool calls spinning while history loads mid-turn', () => {
    const bs = fromEvents([ev({ kind: 'tool', tool: 'Read', id: '1' })], true)
    expect((bs[0] as Extract<Block, { kind: 'tools' }>).live).toBe(true)
  })

  it('finds the last reply and your last message', () => {
    const bs = fromEvents([ev({ kind: 'message', from: 'user', text: 'q' }), ev({ text: '  first\nsecond' })], false)
    expect(lastText(bs)).toBe('first')
    expect(lastUserMessage(bs)).toBe(0)
    expect(lastUserMessage(addEvent(bs, ev({ kind: 'message', from: 'coordinator', text: 'x' })))).toBe(-1)
  })

  it('writes MCP tools as server·tool', () => {
    expect(shortTool('mcp__neoharness__open_thread')).toBe('neoharness·open_thread')
    expect(shortTool('Read')).toBe('Read')
  })
})

const view = (v: Partial<ThreadView>): ThreadView =>
  ({ workspace: 'w', name: 'api', role: 'thread', location: { dir: '/x' }, state: 'idle', queued: 0, since: at, updatedAt: at, createdAt: at, ...v }) as ThreadView

describe('toasts', () => {
  const opts = { reply: '', away: false, home: '/Users/me' }

  it('says a thread waits, with the tool of its permission', () => {
    const t = viewToast('w', view({ state: 'running' }), view({ state: 'waiting', permission: { id: '1', tool: 'Bash', detail: '' } }), opts)
    expect(t).toMatchObject({ glyph: '◆', body: 'needs permission · Bash', life: 0, needsYou: true })
  })

  it('says a turn ended with the first line of the reply', () => {
    const t = viewToast('w', view({ state: 'running' }), view({ state: 'idle' }), { ...opts, reply: '**Done** with it' })
    expect(t).toMatchObject({ glyph: '✓', body: 'Done with it', life: 4000 })
  })

  it('says a resolved thread came back', () => {
    const t = viewToast('w', view({ state: 'resolved' }), view({ state: 'running' }), opts)
    expect(t).toMatchObject({ glyph: '↺', body: 'reopened', tone: 'foam' })
  })

  it('keeps a quiet thread quiet unless it waits', () => {
    expect(viewToast('w', view({ state: 'running' }), view({ state: 'idle', quiet: true }), opts)).toBeNull()
    expect(viewToast('w', view({}), view({ state: 'waiting', quiet: true, waiting: 'which db?' }), opts)?.body).toBe('asks: which db?')
  })

  it('keeps a waiting toast only while the thread waits off screen', () => {
    const t: Toast = { id: 1, workspace: 'w', thread: 'api', glyph: '◆', tone: 'gold', body: '', until: 0, at: 0 }
    const ctx = { open: 'w', onScreen: () => false, threads: { api: view({ state: 'waiting' }) }, workspaces: [] }
    expect(keep(t, 1, ctx)).toBe(true)
    expect(keep(t, 1, { ...ctx, onScreen: () => true })).toBe(false)
    expect(keep(t, 1, { ...ctx, threads: { api: view({ state: 'idle' }) } })).toBe(false)
  })
})

describe('fuzzy', () => {
  it('matches letters in order and ranks runs and word starts first', () => {
    expect(fuzzyScore('open thread', 'xyz')).toBeNull()
    const out = fuzzy([{ label: 'a-p-i-long-name' }, { label: 'docs-api' }, { label: 'xyz' }], 'api')
    expect(out.map((x) => x.label)).toEqual(['docs-api', 'a-p-i-long-name'])
  })

  it('greps with smart case', () => {
    expect(grep('Hello\nhello', 'hello').map((m) => m.line)).toEqual([0, 1])
    expect(grep('Hello\nhello', 'Hello').map((m) => m.line)).toEqual([0])
  })
})

describe('keys', () => {
  const k = (key: string, mods: Partial<KeyboardEvent> = {}) =>
    keyName({ key, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...mods } as KeyboardEvent)

  it('names keys as Vim does', () => {
    expect(k(' ')).toBe('<Space>')
    expect(k('Enter')).toBe('<CR>')
    expect(k('Enter', { shiftKey: true })).toBe('<S-CR>')
    expect(k('w', { ctrlKey: true })).toBe('<C-w>')
    expect(k('k', { metaKey: true })).toBe('<D-k>')
    expect(k('F', { metaKey: true, shiftKey: true })).toBe('<D-S-f>')
    expect(k('G', { shiftKey: true })).toBe('G')
    expect(k('Shift')).toBeNull()
  })

  it('splits sequences', () => {
    expect(split('<leader>pt')).toEqual(['<leader>', 'p', 't'])
    expect(split('<C-w>v')).toEqual(['<C-w>', 'v'])
  })

  it('waits for the rest of a map, and counts', () => {
    const s = new Sequencer({ '<leader>pt': 'threads', gg: 'top', j: 'down', g: 'short', gO: 'outline' })
    expect(s.feed('<Space>')).toEqual({ kind: 'pending' })
    expect(s.feed('p')).toEqual({ kind: 'pending' })
    expect(s.feed('t')).toEqual({ kind: 'action', action: 'threads', count: 1 })
    expect(s.feed('3')).toEqual({ kind: 'pending' })
    expect(s.feed('j')).toEqual({ kind: 'action', action: 'down', count: 3 })
    expect(s.feed('g')).toEqual({ kind: 'pending' })
    expect(s.flush()).toEqual({ kind: 'action', action: 'short', count: 1 })
    expect(s.feed('z')).toEqual({ kind: 'none' })
  })
})

describe('ex', () => {
  it('reads a command, its bang and its words', () => {
    expect(parseEx(':w!')).toMatchObject({ cmd: 'w', bang: true, args: [] })
    expect(parseEx('open api host:~/src')).toMatchObject({ cmd: 'open', args: ['api', 'host:~/src'] })
    expect(parseEx('deny not that one')?.rest).toBe('not that one')
    expect(words('new demo "~/My Code"')).toEqual(['new', 'demo', '~/My Code'])
  })
})

describe('hub', () => {
  it('reads server-sent events', () => {
    expect(parseLine('data: {"kind":"stopped","workspace":""}')?.kind).toBe('stopped')
    expect(parseLine(': ping')).toBeNull()
    expect(parseLine('data: {bad')).toBeNull()
  })

  it('escapes names in paths', () => {
    expect(calls.send({ workspace: 'a b', thread: 'c', text: 'hi' })).toEqual({
      method: 'POST',
      path: '/workspaces/a%20b/threads/c/messages',
      body: { text: 'hi', now: false },
    })
    expect(calls.unqueue({ workspace: 'w', thread: 't', at: '2026-11-13T10:00:00.5+01:00' }).path).toBe(
      '/workspaces/w/threads/t/queue?at=2026-11-13T10%3A00%3A00.5%2B01%3A00',
    )
  })
})

describe('themes', () => {
  it('follows the system for "system" and keeps every scheme complete', () => {
    expect(resolveScheme('system', false).dark).toBe(false)
    expect(resolveScheme('system', true).dark).toBe(true)
    for (const p of Object.values(schemes)) expect(Object.values(p).every((v) => v !== '')).toBe(true)
  })
})

describe('linediff', () => {
  it('keeps common lines and marks the change', () => {
    const ops = lines(['a', 'b', 'c', 'd'], ['a', 'x', 'c', 'd', 'e'])
    expect(ops.map((o) => o.kind + o.text)).toEqual([' a', '-b', '+x', ' c', ' d', '+e'])
  })

  it('handles empty texts', () => {
    expect(lines([], ['a']).map((o) => o.kind)).toEqual(['+'])
    expect(lines(['a'], []).map((o) => o.kind)).toEqual(['-'])
  })
})

describe('folders', () => {
  it('completes folders in the form typed, hides dot folders, and skips relative words', () => {
    const home = fs.mkdtempSync(path.join(os.tmpdir(), 'nhfold-'))
    for (const d of ['Documents', 'Downloads', '.config', 'Desktop/x']) fs.mkdirSync(path.join(home, d), { recursive: true })
    fs.writeFileSync(path.join(home, 'Dfile'), '')
    expect(folders('~/Do', home)).toEqual(['~/Documents/', '~/Downloads/'])
    expect(folders('~/', home)).toEqual(['~/Desktop/', '~/Documents/', '~/Downloads/'])
    expect(folders('~/.c', home)).toEqual(['~/.config/'])
    expect(folders('~/Desktop/', home)).toEqual(['~/Desktop/x/'])
    expect(folders(home + '/Doc', home)).toEqual([home + '/Documents/'])
    expect(folders('~', home)).toEqual(['~/'])
    expect(folders('Doc', home)).toEqual([])
    fs.rmSync(home, { recursive: true })
  })
})

describe('hunks', () => {
  it('finds each change and takes one from theirs', () => {
    const mine = ['a', 'B', 'c', 'd', 'E']
    const theirs = ['a', 'b', 'c', 'd', 'e', 'f']
    const hs = hunks(lines(mine, theirs))
    expect(hs.map(({ mFrom, mTo, tFrom, tTo }) => [mFrom, mTo, tFrom, tTo])).toEqual([
      [1, 2, 1, 2],
      [4, 5, 4, 6],
    ])
    expect(take(mine, theirs, hs[1])).toEqual(['a', 'B', 'c', 'd', 'e', 'f'])
    expect(take(take(mine, theirs, hs[1]), theirs, hs[0])).toEqual(theirs)
  })
})

describe('next', () => {
  it('lists what can follow the keys typed so far', () => {
    const s = new Sequencer({ '<leader>pt': 'threads', '<leader>pa': 'artifacts', '<leader>c': 'coordinator', gg: 'top' })
    s.feed('<Space>')
    expect(s.next()).toEqual([
      { keys: ['p', 't'], action: 'threads' },
      { keys: ['p', 'a'], action: 'artifacts' },
      { keys: ['c'], action: 'coordinator' },
    ])
    s.feed('p')
    expect(s.next().map((n) => n.keys.join(''))).toEqual(['t', 'a'])
  })
})

describe('schemes', () => {
  it('every level of text reads at its contrast on the panel and the shell', () => {
    for (const [name, p] of Object.entries(schemes)) {
      const c = colors(p)
      for (const level of ['text', 'subtle', 'muted'] as const) {
        for (const bg of ['--panel', '--shell', '--surface']) {
          expect(contrast(c[`--${level}`], c[bg]), `${name} ${level} on ${bg}`).toBeGreaterThanOrEqual(ratios[level])
        }
      }
      for (const accent of ['love', 'gold', 'rose', 'pine', 'foam', 'iris']) {
        for (const bg of ['--panel', '--shell', '--surface']) {
          expect(contrast(c[`--${accent}`], c[bg]), `${name} ${accent} on ${bg}`).toBeGreaterThanOrEqual(ratios.muted)
        }
      }
    }
  })
  it('keeps the paper of the light scheme', () => {
    expect(colors(schemes['rose-pine-dawn'])['--panel']).toBe('#fffaf3')
  })
})
