// A fake hub for the end-to-end tests: the hub API on a unix socket, with
// state the test sets and updates the test pushes. No model, no cost, and
// every card the app can show on demand.

import fs from 'node:fs'
import http from 'node:http'

import type { Artifact, Event, ThreadView, Update, WorkspaceView } from '../src/shared/model'

export interface Call {
  method: string
  path: string
  body: unknown
}

const now = () => new Date().toISOString()

export function view(workspace: string, name: string, v: Partial<ThreadView> = {}): ThreadView {
  return {
    workspace,
    name,
    role: name === 'coordinator' ? 'coordinator' : 'thread',
    location: { dir: '/tmp/fake' },
    state: 'idle',
    queued: 0,
    since: now(),
    updatedAt: now(),
    createdAt: now(),
    ...v,
  }
}

export class FakeHub {
  workspaces: WorkspaceView[] = []
  threads: Record<string, ThreadView[]> = {}
  events: Record<string, Event[]> = {}
  artifacts: Record<string, { artifact: Artifact; text: string }[]> = {}
  calls: Call[] = []
  private streams = new Set<http.ServerResponse>()
  private server: http.Server

  constructor(private socket: string) {
    this.server = http.createServer((req, res) => this.handle(req, res))
  }

  listen(): Promise<void> {
    if (fs.existsSync(this.socket)) fs.unlinkSync(this.socket)
    return new Promise((r) => this.server.listen(this.socket, r))
  }

  close(): Promise<void> {
    for (const s of this.streams) s.end()
    return new Promise((r) => this.server.close(() => r()))
  }

  workspace(name: string, threads: string[]): void {
    this.workspaces.push({ name, pin: '/tmp/fake', createdAt: now(), counts: { idle: threads.length }, activity: [0, 1, 3, 2, 5, 1] })
    this.threads[name] = ['coordinator', ...threads].map((t) => view(name, t))
  }

  setView(v: ThreadView): void {
    const list = this.threads[v.workspace]
    const i = list.findIndex((x) => x.name === v.name)
    if (i < 0) list.push(v)
    else list[i] = v
    this.push({ kind: 'thread', workspace: v.workspace, thread: v.name, view: v })
  }

  event(workspace: string, thread: string, e: Partial<Event>): void {
    const ev = { at: now(), kind: 'text', ...e } as Event
    ;(this.events[`${workspace}/${thread}`] ??= []).push(ev)
    this.push({ kind: 'event', workspace, thread, event: ev })
  }

  artifact(workspace: string, name: string, title: string, text: string, version = 1): void {
    const a: Artifact = { name, title, thread: 'api', version, createdAt: now(), updatedAt: now() }
    const list = (this.artifacts[workspace] ??= [])
    const i = list.findIndex((x) => x.artifact.name === name)
    if (i < 0) list.push({ artifact: a, text })
    else list[i] = { artifact: a, text }
    this.push({ kind: 'artifact', workspace, artifact: a })
  }

  push(u: Update): void {
    for (const s of this.streams) s.write(`data: ${JSON.stringify(u)}\n\n`)
  }

  // stop ends the streams as a hub that was asked to stop does.
  stop(): void {
    this.push({ kind: 'stopped', workspace: '' })
    for (const s of this.streams) s.end()
    this.streams.clear()
  }

  private handle(req: http.IncomingMessage, res: http.ServerResponse): void {
    let data = ''
    req.on('data', (c) => (data += c))
    req.on('end', () => {
      const url = new URL(req.url ?? '/', 'http://hub')
      const body = data ? JSON.parse(data) : undefined
      const parts = url.pathname.split('/').filter(Boolean).map(decodeURIComponent)
      if (url.pathname !== '/updates') this.calls.push({ method: req.method ?? '', path: url.pathname + url.search, body })
      const json = (v: unknown, code = 200) => {
        res.writeHead(code, { 'Content-Type': 'application/json' })
        res.end(JSON.stringify(v ?? {}))
      }
      const m = req.method
      if (url.pathname === '/updates') {
        res.writeHead(200, { 'Content-Type': 'text/event-stream' })
        res.write('\n')
        this.streams.add(res)
        res.on('close', () => this.streams.delete(res))
        return
      }
      if (url.pathname === '/hub') return json({ pid: process.pid, started: now() })
      if (url.pathname === '/workspaces' && m === 'GET') return json(this.workspaces)
      if (url.pathname === '/workspaces' && m === 'POST') {
        this.workspace(body.name, [])
        this.workspaces[this.workspaces.length - 1].pin = body.pin
        return json({})
      }
      if (url.pathname === '/deleted') return json([])
      if (url.pathname === '/spend') {
        const all = !url.searchParams.get('workspace')
        const day = (back: number) => {
          const d = new Date(Date.now() - back * 86400000)
          return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
        }
        const costs = [0, 1.2, 0.4, 0, 2.6, 3.1, 0.8, 0, 0, 1.9, 4.2, 2.2, 0.6, 1.5]
        return json({
          total: all ? 31.4 : 24.75,
          today: 1.5,
          since: new Date(Date.now() - 13 * 86400000).toISOString(),
          threads: [
            { workspace: 'demo', name: 'api', cost: 12.5, turns: 30 },
            { workspace: 'demo', name: 'coordinator', cost: 9.25, turns: 41 },
            { workspace: 'demo', name: 'docs', cost: 3, turns: 6 },
          ],
          workspaces: all ? [{ name: 'demo', cost: 24.75 }, { name: 'billing', cost: 6.65 }] : [],
          models: [{ name: 'claude-opus-5-5', cost: 19.5, turns: 50 }, { name: 'claude-sonnet-5-5', cost: 5.25, turns: 27 }],
          days: costs.map((c, i) => ({ name: day(13 - i), cost: c, turns: c ? Math.ceil(c * 3) : 0 })),
          top: [
            { at: new Date().toISOString(), workspace: 'demo', thread: 'api', model: 'claude-opus-5-5', cost: 1.42, tools: 31 },
            { at: new Date().toISOString(), workspace: 'demo', thread: 'coordinator', model: 'claude-opus-5-5', cost: 0.9, tools: 4 },
          ],
        })
      }
      const [, ws, kind, name, sub] = parts
      if (kind === 'threads' && !name && m === 'GET') return json(this.threads[ws] ?? [])
      if (kind === 'threads' && !name && m === 'POST') {
        const v = view(ws, body.name)
        this.setView(v)
        return json({})
      }
      if (kind === 'threads' && sub === 'events') return json(this.events[`${ws}/${name}`] ?? [])
      if (kind === 'threads' && sub === 'completions') return json({ commands: ['/clear', '/compact', '/model'], files: ['main.go', 'go.mod'], threads: ['api'] })
      if (kind === 'threads' && sub === 'choices')
        return json({
          model: 'opus',
          effort: '',
          models: [
            { name: 'default', label: 'Default (recommended)', version: 'model-b-2', description: 'Use the default model', efforts: ['low', 'high'] },
            { name: 'opus', label: 'Opus', version: 'model-a-1', description: 'The most capable', efforts: ['low', 'high', 'max'] },
            { name: 'sonnet', label: 'Sonnet', version: 'model-b-2', description: 'Fast and capable' },
          ],
          efforts: ['low', 'medium', 'high'],
        })
      if (kind === 'threads' && sub === 'messages') {
        // An empty message with now sends the queue; it is no message.
        if (body.text) this.event(ws, name, { kind: 'message', from: 'user', text: body.text })
        return json({})
      }
      if (kind === 'threads' && sub === 'answer') {
        const v = this.threads[ws].find((t) => t.name === name)!
        this.event(ws, name, { kind: 'answer', text: body.message ? `${body.decision}: ${body.message}` : body.decision })
        this.setView({ ...v, state: 'running', permission: undefined, waiting: undefined })
        return json({})
      }
      if (kind === 'threads' && (sub === 'stop' || sub === 'resolve' || sub === 'reopen')) {
        const v = this.threads[ws].find((t) => t.name === name)!
        this.setView({ ...v, state: sub === 'resolve' ? 'resolved' : 'idle' })
        return json({})
      }
      if (kind === 'artifacts' && !name) return json((this.artifacts[ws] ?? []).map((a) => a.artifact))
      if (kind === 'artifacts' && m === 'GET') return json(this.artifacts[ws].find((a) => a.artifact.name === name))
      if (kind === 'artifacts' && m === 'PUT') {
        const cur = (this.artifacts[ws] ?? []).find((a) => a.artifact.name === name)
        // From version 0 the person starts a doc, as the hub does.
        if (!cur && body.base === 0) {
          this.artifact(ws, name, body.text.replace(/^# /, '').split('\n')[0], body.text, 1)
          return json(this.artifacts[ws].find((a) => a.artifact.name === name))
        }
        if (!cur) return json({ error: `no artifact ${name}` }, 400)
        if (cur.artifact.version !== body.base && !body.force) return json({ error: 'the same lines changed in a newer version' }, 400)
        this.artifact(ws, name, cur.artifact.title, body.text, cur.artifact.version + 1)
        return json(this.artifacts[ws].find((a) => a.artifact.name === name))
      }
      json({ error: `fake hub: no route ${m} ${url.pathname}` }, 404)
    })
  }
}
