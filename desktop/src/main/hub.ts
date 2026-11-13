import { execFile } from 'node:child_process'
import http from 'node:http'
import os from 'node:os'
import path from 'node:path'

import { calls, type CallName, type HubStatus } from '../shared/api'
import type { Update } from '../shared/model'

// socketPath follows NEOHARNESS_HOME, as hubapi.SocketPath does, so a
// scratch home gets its own hub.
export function socketPath(): string {
  const home = process.env.NEOHARNESS_HOME || path.join(os.homedir(), '.neoharness')
  return path.join(home, 'hub.sock')
}

function request(method: string, p: string, body?: unknown): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const data = body === undefined ? undefined : JSON.stringify(body)
    const req = http.request(
      {
        socketPath: socketPath(),
        method,
        path: p,
        headers: data ? { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(data) } : {},
      },
      (res) => {
        const chunks: Buffer[] = []
        res.on('data', (c: Buffer) => chunks.push(c))
        res.on('end', () => {
          const text = Buffer.concat(chunks).toString('utf8')
          let json: unknown = undefined
          try {
            json = text ? JSON.parse(text) : undefined
          } catch {
            // A reply that is not JSON is an error of the hub; its status says enough.
          }
          if ((res.statusCode ?? 500) >= 300) {
            const msg = (json as { error?: string } | undefined)?.error
            reject(new Error(msg || `${method} ${p}: ${res.statusCode}`))
            return
          }
          resolve(json)
        })
      },
    )
    req.on('error', reject)
    if (data) req.write(data)
    req.end()
  })
}

export function call(name: CallName, args: unknown): Promise<unknown> {
  const build = calls[name] as ((a: unknown) => { method: string; path: string; body?: unknown }) | undefined
  if (!build) return Promise.reject(new Error(`no hub call ${String(name)}`))
  const r = build(args)
  return request(r.method, r.path, r.body)
}

export async function ping(): Promise<boolean> {
  try {
    await request('GET', '/workspaces')
    return true
  } catch {
    return false
  }
}

// neoharness runs the installed binary with args. An app opened from Finder
// has none of the shell's PATH or environment, and the hub's agents need
// both, so it runs in a login shell unless NEOHARNESS_BIN names the binary.
function neoharness(args: string[], timeout = 30_000): Promise<string> {
  const bin = process.env.NEOHARNESS_BIN
  const [file, argv] = bin
    ? [bin, args]
    : [process.env.SHELL || '/bin/zsh', ['-lic', `exec neoharness ${args.join(' ')}`]]
  return new Promise((resolve, reject) => {
    execFile(file, argv, { timeout, cwd: os.homedir() }, (err, stdout, stderr) => {
      if (err) {
        const last = stderr.trim().split('\n').pop() || err.message
        reject(new Error(last.includes('not found') ? 'neoharness is not on your PATH; install it with ./install.sh' : last))
        return
      }
      resolve(stdout)
    })
  })
}

export async function startHub(): Promise<void> {
  await neoharness(['hub', 'start'])
}

// installedVersion is the commit of the neoharness on the PATH, which an
// update replaces under a hub that still runs the old one; '' when unknown.
export async function installedVersion(): Promise<string> {
  const out = await neoharness(['version']).catch(() => '')
  const v = out.trim().split('\n').pop() ?? ''
  return v === 'unknown' ? '' : v
}

// Updates streams the hub's updates and keeps the connection: a hub that went
// away by itself is started again, one that was stopped is left alone until
// start() is called.
export class Updates {
  private status: HubStatus = 'connecting'
  private detail = ''
  private stopped = false
  private req: http.ClientRequest | null = null
  private timer: NodeJS.Timeout | null = null
  private closed = false

  constructor(
    private onUpdate: (u: Update) => void,
    private onStatus: (s: HubStatus, detail: string) => void,
  ) {}

  open(): void {
    void this.connect()
  }

  close(): void {
    this.closed = true
    if (this.timer) clearTimeout(this.timer)
    this.req?.destroy()
  }

  // start brings back a hub the person stopped.
  async start(): Promise<void> {
    this.stopped = false
    this.set('connecting', '')
    await startHub()
    if (this.timer) clearTimeout(this.timer)
    void this.connect()
  }

  // restart stops the hub and starts the installed one, after an update.
  async restart(): Promise<void> {
    this.set('connecting', '')
    await neoharness(['hub', 'restart'], 60_000)
    this.stopped = false
    if (this.timer) clearTimeout(this.timer)
    void this.connect()
  }

  // replay sends the status again, for a page that loaded after it changed.
  replay(): void {
    this.onStatus(this.status, this.detail)
  }

  private set(s: HubStatus, detail: string): void {
    this.status = s
    this.detail = detail
    this.onStatus(s, detail)
  }

  private retry(ms: number): void {
    if (this.closed) return
    if (this.timer) clearTimeout(this.timer)
    this.timer = setTimeout(() => void this.connect(), ms)
  }

  private async connect(): Promise<void> {
    if (this.closed) return
    if (!(await ping())) {
      if (this.stopped) {
        this.retry(2000)
        return
      }
      try {
        await startHub()
      } catch (err) {
        this.set('lost', (err as Error).message)
        this.retry(3000)
        return
      }
    }
    const req = http.request({ socketPath: socketPath(), method: 'GET', path: '/updates' }, (res) => {
      if (res.statusCode !== 200) {
        res.resume()
        this.set('lost', `updates: ${res.statusCode}`)
        this.retry(1000)
        return
      }
      this.stopped = false
      this.set('connected', '')
      let buf = ''
      res.setEncoding('utf8')
      res.on('data', (chunk: string) => {
        buf += chunk
        let i: number
        while ((i = buf.indexOf('\n')) >= 0) {
          const line = buf.slice(0, i)
          buf = buf.slice(i + 1)
          const u = parseLine(line)
          if (!u) continue
          if (u.kind === 'stopped') {
            this.stopped = true
            continue
          }
          this.onUpdate(u)
        }
      })
      res.on('end', () => this.dropped())
      res.on('error', () => this.dropped())
    })
    req.on('error', () => this.dropped())
    req.end()
    this.req = req
  }

  private dropped(): void {
    if (this.closed || this.status === 'lost' || this.status === 'stopped') {
      if (!this.closed) this.retry(1000)
      return
    }
    if (this.stopped) {
      this.set('stopped', 'the hub stopped; neoharness hub start brings it back')
      this.retry(2000)
      return
    }
    this.set('lost', 'lost the hub; reconnecting…')
    this.retry(300)
  }
}

export function parseLine(line: string): Update | null {
  if (!line.startsWith('data: ')) return null
  try {
    return JSON.parse(line.slice(6)) as Update
  } catch {
    return null
  }
}
