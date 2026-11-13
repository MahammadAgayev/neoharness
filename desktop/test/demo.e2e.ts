// Records docs/demo/desktop.gif: the app on the fake hub, so no real data
// shows. A coordinator opens three threads, one asks for permission, and a
// plan opens from the artifacts picker. Needs ffmpeg.
//
//   npm run build && NEOHARNESS_DEMO=1 npx playwright test demo

import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

import { _electron as electron, expect, test } from '@playwright/test'

import { FakeHub, view } from './fakehub'

// The page is shorter than the video, whose last rows the window does not
// fill; ffmpeg crops to the page.
const size = { width: 1440, height: 900 }
const shown = { width: 1440, height: 860 }

test('records the desktop demo', async () => {
  test.skip(!process.env.NEOHARNESS_DEMO, 'set NEOHARNESS_DEMO=1 to record docs/demo/desktop.gif')
  test.setTimeout(180_000)
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'nhdemo-'))
  const hub = new FakeHub(path.join(tmp, 'hub.sock'))
  hub.workspace('payments', [])
  hub.workspace('search', ['indexer'])
  hub.workspace('infra', [])
  hub.workspaces[0].counts = {}
  hub.workspaces[0].cost = 3.4
  hub.workspaces[1].counts = { running: 1 }
  hub.workspaces[0].activity = [0, 0, 1, 3, 2, 4, 6, 5, 8, 7, 4, 3, 5, 9, 6, 4, 2, 3, 5, 7, 8, 6, 4, 3]
  hub.setView(view('payments', 'coordinator', { model: 'claude-opus-5-5', context: 9000, contextWindow: 400000, cost: 0.12, branch: 'main' }))
  await hub.listen()

  const video = path.join(tmp, 'video')
  const launched = Date.now()
  const app = await electron.launch({
    args: [path.join(__dirname, '..', 'out', 'main', 'index.js')],
    env: { ...process.env, NEOHARNESS_DESKTOP_TEST: process.env.NEOHARNESS_DESKTOP_TEST ?? 'shown', NEOHARNESS_HOME: tmp, NEOHARNESS_BIN: '/usr/bin/false' },
    recordVideo: { dir: video, size },
  })
  const page = await app.firstWindow()
  await page.setViewportSize(shown)
  const wait = (ms: number) => page.waitForTimeout(ms)
  const co = (e: Parameters<FakeHub['event']>[2]) => hub.event('payments', 'coordinator', e)
  const th = (name: string, v: Parameters<typeof view>[2]) => hub.setView(view('payments', name, { model: 'claude-sonnet-5-5', contextWindow: 400000, ...v }))
  let start = 0
  try {
    await expect(page.locator('.ws-row', { hasText: 'payments' })).toBeVisible({ timeout: 20_000 })
    await page.keyboard.type(':colorscheme rose-pine-moon')
    await page.keyboard.press('Enter')
    await wait(500)
    start = (Date.now() - launched) / 1000

    await wait(1500)
    await page.keyboard.press('Enter')
    await expect(page.locator('.statusline .sl-name', { hasText: 'coordinator' })).toBeVisible()
    await wait(800)
    await page.keyboard.press('i')
    await page.keyboard.type('Add a health check to the API, migrate the ledger table, and fix the flaky checkout test.', { delay: 28 })
    await wait(500)
    await page.keyboard.press('Enter')
    await wait(400)
    hub.setView(view('payments', 'coordinator', { state: 'running', model: 'claude-opus-5-5', context: 14000, contextWindow: 400000, cost: 0.2, branch: 'main' }))
    await wait(900)
    for (const [i, name, progress] of [
      ['1', 'api', 'reading the router'],
      ['2', 'ledger-migration', 'writing migrate.sql'],
      ['3', 'flaky-test', 'running checkout tests 20 times'],
    ]) {
      co({ kind: 'tool', tool: 'mcp__neoharness__open_thread', detail: name, id: i })
      th(name, { state: 'running', progress })
      await wait(700)
    }
    co({
      kind: 'text',
      text: 'I split this into three threads:\n\n- **api** adds `GET /healthz` and a test\n- **ledger-migration** writes the migration; it asks before it touches the database\n- **flaky-test** finds why checkout fails one run in ten',
    })
    co({ kind: 'turn' })
    hub.setView(view('payments', 'coordinator', { model: 'claude-opus-5-5', context: 21000, contextWindow: 400000, cost: 0.31, branch: 'main' }))
    await wait(1800)
    th('api', { state: 'running', progress: 'adding the /healthz route', context: 38000, cost: 0.4 })
    await wait(900)

    // The migration needs the person: a toast, and the card in the thread.
    for (const e of [
      { kind: 'message', from: 'coordinator', text: 'Write and run a migration that adds `settled_at` to the ledger table.' },
      { kind: 'tool', tool: 'Read', detail: 'db/schema.sql', id: 'r1' },
      { kind: 'tool', tool: 'Write', detail: 'db/migrate.sql', id: 'w1' },
      { kind: 'text', text: 'The migration adds the column, then backfills it in batches of 1000.' },
      { kind: 'permission', tool: 'Bash', detail: 'psql -f db/migrate.sql', id: 'p1' },
    ] as const)
      hub.event('payments', 'ledger-migration', e)
    th('ledger-migration', { state: 'waiting', permission: { id: 'p1', tool: 'Bash', detail: 'psql -f db/migrate.sql' }, context: 52000, cost: 0.55 })
    const toast = page.locator('.toast', { hasText: 'needs permission' })
    await expect(toast).toBeVisible()
    await wait(1600)
    await toast.click()
    await expect(page.locator('.card.permission button', { hasText: 'Allow' })).toBeVisible()
    await wait(1800)
    await page.keyboard.press('Escape')
    await page.keyboard.press('Escape')
    await page.keyboard.press('Space')
    await wait(500)
    await page.keyboard.press('a')
    await wait(700)
    hub.event('payments', 'ledger-migration', { kind: 'tool', tool: 'Bash', detail: 'psql -f db/migrate.sql', id: 'b1' })
    await wait(900)
    hub.event('payments', 'ledger-migration', { kind: 'text', text: 'Done: `settled_at` exists and **2,418** rows are backfilled.' })
    hub.event('payments', 'ledger-migration', { kind: 'turn' })
    th('ledger-migration', { state: 'idle', progress: 'migration ran', context: 61000, cost: 0.71 })
    th('flaky-test', { state: 'resolved' })
    await wait(2000)

    // A plan the coordinator wrote, from the artifacts picker.
    hub.artifact(
      'payments',
      'health-plan',
      'Health check and ledger plan',
      '# Health check and ledger plan\n\n## Health check\n\n1. Route `GET /healthz`\n2. Return the build and the database status\n\n```go\nfunc health(w http.ResponseWriter, r *http.Request) {\n\tw.WriteHeader(http.StatusOK)\n}\n```\n\n## Ledger\n\n| Step | Risk |\n|---|---|\n| add column | low |\n| backfill | medium |\n\n> [!NOTE]\n> The backfill runs in batches of 1000.\n',
      2,
    )
    co({ kind: 'artifact', detail: 'health-plan', text: 'Health check and ledger plan', version: 2 })
    await wait(600)
    await page.keyboard.press('Space')
    await page.keyboard.press('p')
    await page.keyboard.press('a')
    await expect(page.locator('.palette')).toBeVisible()
    await wait(700)
    await page.keyboard.type('health', { delay: 120 })
    await wait(1000)
    await page.keyboard.press('Enter')
    await expect(page.locator('.window.artifact .cm-h1')).toBeVisible()
    await wait(3000)
  } finally {
    await app.close()
    await hub.close()
  }

  const webm = fs.readdirSync(video).find((f) => f.endsWith('.webm'))!
  const out = path.join(__dirname, '..', '..', 'docs', 'demo', 'desktop.gif')
  const filters = `crop=${shown.width}:${shown.height}:0:0,fps=10,scale=1100:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=96:stats_mode=diff[p];[b][p]paletteuse=dither=none:diff_mode=rectangle`
  execFileSync('ffmpeg', ['-y', '-loglevel', 'error', '-ss', String(start), '-i', path.join(video, webm), '-vf', filters, out])
})
