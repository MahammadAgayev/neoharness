// A full scene on the fake hub, for a look at the whole app: three
// workspaces, threads in every state, a coordinator conversation.

import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

import { _electron as electron, expect, test } from '@playwright/test'

import { FakeHub, view } from './fakehub'

test('the whole app, populated', async () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'nhshow-'))
  const hub = new FakeHub(path.join(tmp, 'hub.sock'))
  hub.workspace('payments', ['api', 'ledger-migration', 'docs', 'flaky-test'])
  hub.workspace('search', ['indexer'])
  hub.workspace('infra', [])
  hub.workspaces[1].counts = { running: 1 }
  hub.workspaces[0].activity = [0, 0, 1, 3, 2, 4, 6, 5, 8, 7, 4, 3, 5, 9, 6, 4, 2, 3, 5, 7, 8, 6, 4, 3]
  const t = (n: string, v: Parameters<typeof view>[2]) => {
    const list = hub.threads.payments
    list[list.findIndex((x) => x.name === n)] = view('payments', n, v)
  }
  t('api', { state: 'running', progress: 'adding the /healthz route', model: 'claude-sonnet-5-5', context: 41000, contextWindow: 200000, cost: 0.42, branch: 'health-check' })
  t('ledger-migration', { state: 'waiting', permission: { id: 'p', tool: 'Bash', detail: 'psql -f migrate.sql' } })
  t('docs', { state: 'idle', progress: 'README updated' })
  t('flaky-test', { state: 'resolved' })
  t('coordinator', { state: 'idle', model: 'claude-opus-5-5', context: 12000, contextWindow: 200000, cost: 0.18 })
  const c = (e: Parameters<FakeHub['event']>[2]) => hub.event('payments', 'coordinator', e)
  c({ kind: 'message', from: 'user', text: 'Add a health check to the API, migrate the ledger table, and fix the flaky checkout test.' })
  c({ kind: 'tool', tool: 'mcp__neoharness__open_thread', detail: 'api', id: '1' })
  c({ kind: 'tool', tool: 'mcp__neoharness__open_thread', detail: 'ledger-migration', id: '2' })
  c({ kind: 'tool', tool: 'mcp__neoharness__open_thread', detail: 'flaky-test', id: '3' })
  c({
    kind: 'text',
    text: 'I split this into three threads:\n\n- **api** adds `GET /healthz` and a test\n- **ledger-migration** writes and runs the migration; it will ask before it touches the database\n- **flaky-test** finds why checkout fails one run in ten\n\nThe plan is in `health-plan`.',
  })
  c({ kind: 'artifact', detail: 'health-plan', text: 'Health check and ledger plan', version: 2 })
  c({ kind: 'turn' })
  c({ kind: 'message', from: 'flaky-test', text: 'Fixed: the test shared a clock with the cart timeout. Resolved.' })
  c({ kind: 'notice', text: 'ledger-migration needs your OK to run psql on staging', needsYou: true })
  hub.artifact('payments', 'health-plan', 'Health check and ledger plan', '# Plan\n\n## Health check\n\n1. Route `GET /healthz`\n2. Return the build and DB status\n\n```go\nfunc health(w http.ResponseWriter, r *http.Request) {\n\tw.WriteHeader(http.StatusOK)\n}\n```\n\n## Ledger\n\n| Step | Risk |\n|---|---|\n| add column | low |\n| backfill | medium |\n\n> [!NOTE]\n> The backfill runs in batches of 1000.\n', 2)
  await hub.listen()

  const app = await electron.launch({
    args: [path.join(__dirname, '..', 'out', 'main', 'index.js')],
    env: { ...process.env, NEOHARNESS_DESKTOP_TEST: process.env.NEOHARNESS_DESKTOP_TEST ?? 'shown', NEOHARNESS_HOME: tmp, NEOHARNESS_BIN: '/usr/bin/false' },
  })
  const page = await app.firstWindow()
  await page.setViewportSize({ width: 1440, height: 900 })
  const shot = async (name: string) => {
    await page.waitForTimeout(300)
    await page.screenshot({ path: path.join(__dirname, '..', 'test-results', `${name}.png`) })
  }
  try {
    await expect(page.locator('.ws-row', { hasText: 'payments' })).toBeVisible({ timeout: 20_000 })
    await page.keyboard.type(':')
    await page.keyboard.type('colorscheme rose-pine-moon')
    await page.keyboard.press('Enter')
    await shot('30-show-workspaces')
    // With no workspace open, search offers only what spans workspaces.
    await page.keyboard.press('Meta+k')
    await expect(page.locator('.palette-tabs .tab')).toHaveText(['All', 'Threads', 'Commands', 'Workspaces', 'Notifications'])
    await page.keyboard.press('Escape')
    await page.keyboard.press('Enter')
    await expect(page.locator('.list-row .name', { hasText: 'ledger-migration' })).toBeVisible()
    await shot('31-show-workspace')
    await page.keyboard.press('Meta+k')
    await expect(page.locator('.palette-tabs .tab')).toHaveText(['All', 'Threads', 'Artifacts', 'Text', 'Commands', 'Workspaces', 'Notifications'])
    await shot('32-show-search')
    await page.keyboard.type('ledger')
    await shot('33-show-search-query')
    await page.keyboard.press('Escape')
    await page.locator('code[data-artifact="health-plan"]').click()
    await expect(page.locator('.window.artifact .cm-h1')).toBeVisible()
    await shot('34-show-artifact')
    await page.keyboard.type(':')
    await page.keyboard.type('help')
    await page.keyboard.press('Enter')
    await shot('35-show-help')
  } finally {
    await app.close()
    await hub.close()
  }
})
