// The frame budget of the app: it runs the built app on the fake hub, with
// the CPU slowed 4×, as on an older or a busy Mac, and measures each scenario
// in the page. A frame at 120 Hz is 8.3 ms, so a frame over 17 ms lost at
// least one. npm run perf writes test-results/perf.json.

import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

import { _electron as electron, expect, test, type CDPSession, type ElectronApplication, type Page } from '@playwright/test'

import { FakeHub } from './fakehub'

const throttle = Number(process.env.PERF_THROTTLE ?? 4)
const events = 2000

// known are the scenarios over budget today, with the build step that brings
// them in. They report but do not fail; any other scenario over budget fails.
const known: Record<string, string> = {}

interface Frames {
  frames: number
  fps: number
  p95: number
  over17: number
  longTasks: number
  longTaskMax: number
  ms: { script: number; style: number; layout: number }
}

let tmp = ''
let hub: FakeHub
let app: ElectronApplication
let page: Page
let cdp: CDPSession
const results: Record<string, unknown> = { throttle, events }

const reply = (i: number) =>
  `## Step ${i}\n\nI read \`server.go\` and **changed** the router. Here is the *new* handler with a [link](https://example.com):\n\n` +
  '```go\nfunc health(w http.ResponseWriter, r *http.Request) {\n\tw.WriteHeader(http.StatusOK)\n\tfmt.Fprintln(w, "ok")\n}\n```\n\n' +
  `- one item\n- two item\n- [ ] a task ${i}\n\n| a | b |\n|---|---|\n| 1 | 2 |\n`

async function metrics(): Promise<Record<string, number>> {
  const r = await cdp.send('Performance.getMetrics')
  return Object.fromEntries(r.metrics.map((m) => [m.name, m.value]))
}

// frames measures what run does: rAF deltas, long tasks and where the main
// thread's time went.
async function frames(run: () => Promise<void>): Promise<Frames> {
  const m0 = await metrics()
  await page.evaluate(() => {
    const w = window as unknown as { __f: { d: number[]; lt: number[]; run: boolean; po?: PerformanceObserver } }
    w.__f = { d: [], lt: [], run: true }
    let last = performance.now()
    const tick = (t: number) => {
      w.__f.d.push(t - last)
      last = t
      if (w.__f.run) requestAnimationFrame(tick)
    }
    requestAnimationFrame(tick)
    w.__f.po = new PerformanceObserver((l) => l.getEntries().forEach((e) => w.__f.lt.push(e.duration)))
    w.__f.po.observe({ type: 'longtask' })
  })
  await run()
  const r = await page.evaluate(() => {
    const w = window as unknown as { __f: { d: number[]; lt: number[]; run: boolean; po?: PerformanceObserver } }
    w.__f.run = false
    w.__f.po?.disconnect()
    const d = w.__f.d.slice(1)
    const s = [...d].sort((a, b) => a - b)
    const total = d.reduce((a, b) => a + b, 0)
    return {
      frames: d.length,
      fps: +((d.length / total) * 1000).toFixed(1),
      p95: +(s[Math.floor(0.95 * s.length)] ?? 0).toFixed(1),
      over17: d.filter((x) => x > 17.5).length,
      longTasks: w.__f.lt.length,
      longTaskMax: +Math.max(0, ...w.__f.lt).toFixed(0),
    }
  })
  const m1 = await metrics()
  const ms = (k: string) => Math.round((m1[k] - m0[k]) * 1000)
  return { ...r, ms: { script: ms('ScriptDuration'), style: ms('RecalcStyleDuration'), layout: ms('LayoutDuration') } }
}

// latency is the time from each key to the next frame, as the person feels it.
async function latency(keys: string): Promise<{ p50: number; p95: number }> {
  await page.evaluate(() => ((window as unknown as { __lat: number[] }).__lat = []))
  for (const ch of keys) {
    await page.keyboard.type(ch)
    await page.waitForTimeout(40)
  }
  return page.evaluate(() => {
    const s = [...(window as unknown as { __lat: number[] }).__lat].sort((a, b) => a - b)
    return { p50: +s[Math.floor(s.length / 2)].toFixed(1), p95: +s[Math.floor(s.length * 0.95)].toFixed(1) }
  })
}

// budget records a result and fails it when it misses, unless it is known.
function budget(name: string, value: unknown, misses: string[]): void {
  results[name] = { value, misses, known: known[name] ?? null }
  const line = `${name}: ${JSON.stringify(value)}`
  if (misses.length === 0) {
    console.log(`ok    ${line}${known[name] ? ` · within budget now: take it out of known` : ''}`)
    return
  }
  if (known[name]) {
    console.log(`known ${line} · ${misses.join(', ')} · until the ${known[name]}`)
    return
  }
  expect(misses, line).toEqual([])
}

function frameMisses(f: Frames): string[] {
  const out: string[] = []
  if (f.over17 > 1) out.push(`${f.over17} frames over 17 ms`)
  if (f.longTasks > 0) out.push(`${f.longTasks} long tasks, up to ${f.longTaskMax} ms`)
  return out
}

test.describe.configure({ mode: 'serial' })

test.beforeAll(async () => {
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'nhperf-'))
  hub = new FakeHub(path.join(tmp, 'hub.sock'))
  hub.workspace('demo', ['api', 'long'])
  for (let i = 0; i < events / 4; i++) {
    hub.event('demo', 'long', { kind: 'message', from: 'user', text: `do step ${i}` })
    hub.event('demo', 'long', { kind: 'tool', tool: 'Read', detail: `file${i}.go`, id: `t${i}` })
    hub.event('demo', 'long', { kind: 'text', text: reply(i) })
    hub.event('demo', 'long', { kind: 'turn' })
  }
  hub.event('demo', 'api', { kind: 'message', from: 'user', text: 'hello' })
  await hub.listen()
  app = await electron.launch({
    args: [path.join(__dirname, '..', 'out', 'main', 'index.js')],
    env: { ...process.env, NEOHARNESS_DESKTOP_TEST: process.env.NEOHARNESS_DESKTOP_TEST ?? 'shown', NEOHARNESS_HOME: tmp, NEOHARNESS_BIN: '/usr/bin/false' },
  })
  page = await app.firstWindow()
  await page.setViewportSize({ width: 1400, height: 860 })
  await expect(page.locator('.ws-row', { hasText: 'demo' })).toBeVisible({ timeout: 20_000 })
  await page.keyboard.press('Enter')
  await expect(page.locator('.statusline .sl-name', { hasText: 'coordinator' })).toBeVisible()
  cdp = await page.context().newCDPSession(page)
  await cdp.send('Performance.enable')
  if (throttle > 1) await cdp.send('Emulation.setCPUThrottlingRate', { rate: throttle })
  await page.evaluate(() => {
    const w = window as unknown as { __lat: number[] }
    w.__lat = []
    window.addEventListener(
      'keydown',
      (e) => {
        const t = e.timeStamp
        requestAnimationFrame(() => setTimeout(() => w.__lat.push(performance.now() - t), 0))
      },
      true,
    )
  })
})

test.afterAll(async () => {
  fs.mkdirSync(path.join(__dirname, '..', 'test-results'), { recursive: true })
  fs.writeFileSync(path.join(__dirname, '..', 'test-results', 'perf.json'), JSON.stringify(results, null, 2))
  await app?.close()
  await hub?.close()
})

test('idle', async () => {
  const f = await frames(() => page.waitForTimeout(1000))
  budget('idle', f, frameMisses(f))
})

test('open a thread of 2000 events', async () => {
  const t0 = Date.now()
  await page.locator('.list-row', { hasText: 'long' }).first().click()
  await expect(page.locator('.transcript .row').nth(10)).toBeVisible({ timeout: 60_000 })
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))))
  const ms = Date.now() - t0
  budget('openLongThread', { ms }, ms > 300 ? [`${ms} ms, budget 300`] : [])
})

test('wheel scroll the long chat', async () => {
  const box = await page.locator('.transcript').first().boundingBox()
  await page.mouse.move(box!.x + box!.width / 2, box!.y + box!.height / 2)
  const f = await frames(async () => {
    for (let i = 0; i < 120; i++) {
      await page.mouse.wheel(0, -60)
      await page.waitForTimeout(8)
    }
  })
  budget('wheelScroll', f, frameMisses(f))
})

test('k moves the block cursor', async () => {
  await page.locator('.transcript').first().focus()
  const f = await frames(async () => {
    for (let i = 0; i < 40; i++) await page.keyboard.press('k')
  })
  budget('kMoves', f, frameMisses(f))
  await page.keyboard.press('G')
})

test('blocks stream into the long chat', async () => {
  const f = await frames(async () => {
    for (let i = 0; i < 120; i++) {
      hub.event('demo', 'long', i % 3 === 0 ? { kind: 'tool', tool: 'Bash', detail: `go test ${i}`, id: `s${i}` } : { kind: 'text', text: reply(i) })
      await page.waitForTimeout(33)
    }
    await page.waitForTimeout(300)
  })
  budget('streamBlocks', f, frameMisses(f))
})

test('small events at 60 a second, as text deltas would come', async () => {
  const f = await frames(async () => {
    for (let i = 0; i < 120; i++) {
      hub.event('demo', 'long', { kind: 'text', text: 'word '.repeat(5) })
      await page.waitForTimeout(16)
    }
    await page.waitForTimeout(300)
  })
  budget('streamSmallEvents', f, frameMisses(f))
})

// A big reply comes as the hub sends it: the text so far every 50 ms, then
// the text event, which renders from the blocks the stream left cached.
test('big replies', async () => {
  const big = Array.from({ length: 40 }, (_, i) => reply(i)).join('\n')
  const f = await frames(async () => {
    for (let i = 0; i < 5; i++) {
      for (let n = 400; n < big.length; n += 400) {
        hub.delta('demo', 'long', big.slice(0, n))
        await page.waitForTimeout(50)
      }
      hub.event('demo', 'long', { kind: 'text', text: big })
      await page.waitForTimeout(200)
    }
  })
  budget('bigReplies', f, frameMisses(f))
})

test('text deltas at 60 a second into the long chat', async () => {
  let text = ''
  const f = await frames(async () => {
    for (let i = 0; i < 120; i++) {
      text += i % 15 === 14 ? 'end of a paragraph.\n\n' : 'word '.repeat(5)
      hub.delta('demo', 'long', text)
      await page.waitForTimeout(16)
    }
    hub.delta('demo', 'long', '')
    await page.waitForTimeout(300)
  })
  budget('streamDeltas', f, frameMisses(f))
})

test('events into a thread not on screen', async () => {
  const f = await frames(async () => {
    for (let i = 0; i < 120; i++) {
      hub.event('demo', 'api', { kind: 'text', text: reply(i) })
      await page.waitForTimeout(33)
    }
    await page.waitForTimeout(300)
  })
  budget('streamOtherThread', f, frameMisses(f))
})

// A fold is a key, so the width changes at once: the windows are laid out
// again at their new width, with no animation. At full speed that drops no
// frame; on the slowed CPU the text of the windows wraps again in up to two
// frames, but never in a long task.
test('the rail folds and opens', async () => {
  const folds = 6
  const f = await frames(async () => {
    for (let i = 0; i < folds; i++) {
      await page.keyboard.press('Meta+b')
      await page.waitForTimeout(250)
    }
  })
  const misses: string[] = []
  if (f.over17 > 2 * folds) misses.push(`${f.over17} frames over 17 ms for ${folds} folds`)
  if (f.longTasks > 0) misses.push(`${f.longTasks} long tasks, up to ${f.longTaskMax} ms`)
  budget('railFold', f, misses)
})

test('type in the prompt', async () => {
  await page.keyboard.press('i')
  const l = await latency('the quick brown fox jumps over the lazy dog '.repeat(2))
  budget('typePrompt', l, l.p95 >= 16 ? [`key to frame p95 ${l.p95} ms, budget 16`] : [])
  await page.keyboard.press('Escape')
})

test('the artifact page: open, scroll, move and type', async () => {
  const doc = (fs.readFileSync(path.join(__dirname, '..', '..', 'docs', 'desktop.md'), 'utf8') + '\n\n').repeat(4)
  hub.artifact('demo', 'big', 'Big doc', doc)
  hub.event('demo', 'long', { kind: 'artifact', detail: 'big', text: 'Big doc', version: 1 })
  await page.locator('.b-artifact', { hasText: 'Big doc' }).last().click()
  const pg = page.locator('.window.artifact .doc-page')
  await expect(pg.locator('h1').first()).toBeVisible({ timeout: 30_000 })
  const box = await pg.boundingBox()
  await page.mouse.move(box!.x + box!.width / 2, box!.y + Math.min(box!.height, 600) / 2)
  const scroll = await frames(async () => {
    for (let i = 0; i < 120; i++) {
      await page.mouse.wheel(0, 60)
      await page.waitForTimeout(8)
    }
  })
  budget('pageScroll', scroll, frameMisses(scroll))
  const moves = await frames(async () => {
    for (let i = 0; i < 30; i++) await page.keyboard.press('j')
  })
  budget('pageMoves', moves, frameMisses(moves))
  await page.keyboard.press('o')
  const l = await latency('the quick brown fox jumps over the lazy dog '.repeat(2))
  budget('pageType', l, l.p95 >= 16 ? [`key to frame p95 ${l.p95} ms, budget 16`] : [])
  await page.keyboard.press('Escape')
})
