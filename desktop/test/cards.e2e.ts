// The app against the fake hub: permission cards, questions, artifacts and
// their merge, completion, the queue, and a hub that stops.

import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

import { _electron as electron, expect, test, type ElectronApplication, type Page } from '@playwright/test'

import { FakeHub, view } from './fakehub'

let tmp = ''
let hub: FakeHub
let app: ElectronApplication
let page: Page

const shot = async (name: string) => {
  await page.waitForTimeout(250)
  await page.screenshot({ path: path.join(__dirname, '..', 'test-results', `${name}.png`) })
}

async function normal() {
  await page.keyboard.press('Escape')
  await page.keyboard.press('Escape')
}

test.beforeAll(async () => {
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'nhfake-'))
  hub = new FakeHub(path.join(tmp, 'hub.sock'))
  hub.workspace('demo', ['api'])
  hub.event('demo', 'api', { kind: 'message', from: 'user', text: 'add a health check' })
  hub.event('demo', 'api', { kind: 'tool', tool: 'Read', detail: 'server.go', id: '1' })
  hub.event('demo', 'api', { kind: 'text', text: 'I read `server.go`. The router is in **one** place.' })
  hub.event('demo', 'api', { kind: 'turn' })
  await hub.listen()
  app = await electron.launch({
    args: [path.join(__dirname, '..', 'out', 'main', 'index.js')],
    // The fake hub is up; false makes sure no real hub ever starts here.
    env: { ...process.env, NEOHARNESS_HOME: tmp, NEOHARNESS_BIN: '/usr/bin/false' },
  })
  page = await app.firstWindow()
  await page.setViewportSize({ width: 1400, height: 860 })
  await expect(page.locator('.ws-row', { hasText: 'demo' })).toBeVisible({ timeout: 20_000 })
  await page.keyboard.press('Enter')
  await expect(page.locator('.statusline .sl-name', { hasText: 'coordinator' })).toBeVisible()
})

test.afterAll(async () => {
  await app?.close()
  await hub?.close()
})

test('a permission request toasts, shows a card, and <leader>a allows it', async () => {
  const perm = { id: 'p1', tool: 'Bash', detail: 'go test ./...' }
  hub.event('demo', 'api', { kind: 'permission', tool: 'Bash', detail: 'go test ./...', id: 'p1' })
  hub.setView(view('demo', 'api', { state: 'waiting', permission: perm }))
  const toast = page.locator('.toast', { hasText: 'needs permission · Bash' })
  await expect(toast).toBeVisible()
  await shot('20-permission-toast')
  await toast.click()
  await expect(page.locator('.card.permission button', { hasText: 'Allow' })).toBeVisible()
  // The thread is on screen now: its toast goes.
  await expect(toast).toHaveCount(0, { timeout: 3000 })
  await shot('21-permission-card')
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('a')
  await expect.poll(() => hub.calls.find((c) => c.path.endsWith('/answer'))?.body).toMatchObject({ id: 'p1', decision: 'allow' })
  await expect(page.locator('.row.k-permission', { hasText: '· allow' })).toBeVisible()
})

test('a question shows as a card you answer by writing', async () => {
  hub.event('demo', 'api', { kind: 'ask', text: 'Which **port** should it listen on?' })
  hub.setView(view('demo', 'api', { state: 'waiting', waiting: 'Which port should it listen on?' }))
  await expect(page.locator('.b-ask', { hasText: 'Which port' })).toBeVisible()
  await page.keyboard.press('i')
  await page.keyboard.type('8080')
  await page.keyboard.press('Enter')
  await expect.poll(() => hub.calls.find((c) => c.path.endsWith('/messages'))?.body).toMatchObject({ text: '8080' })
  await expect(page.locator('.b-user', { hasText: '8080' })).toBeVisible()
  await shot('22-question')
})

test('/ completes commands in the prompt', async () => {
  await page.keyboard.type('/cl')
  await expect(page.locator('.cm-tooltip-autocomplete', { hasText: 'clear' })).toBeVisible()
  await shot('23-completion')
  await page.keyboard.press('Escape')
  await page.keyboard.press('Control+u')
  await normal()
})

test('queued messages show dim, and <CR> on one sends it now', async () => {
  const at = new Date().toISOString()
  hub.setView(view('demo', 'api', { state: 'running', queued: 1, pending: [{ at, kind: 'message', from: 'user', text: 'also add a test' }] }))
  await expect(page.locator('.b-queued', { hasText: 'also add a test' })).toBeVisible()
  await page.locator('.b-queued').click()
  await expect.poll(() => hub.calls.filter((c) => c.path.endsWith('/messages')).pop()?.body).toMatchObject({ text: '', now: true })
  hub.setView(view('demo', 'api', { state: 'idle' }))
})

test('an artifact opens, edits, saves, and a newer version is merged or forced', async () => {
  hub.artifact('demo', 'plan', 'Health check plan', '# Plan\n\n- add /healthz\n- test it\n')
  hub.event('demo', 'api', { kind: 'artifact', detail: 'plan', text: 'Health check plan', version: 1 })
  await page.locator('.b-artifact', { hasText: 'Health check plan' }).click()
  await expect(page.locator('.doc-title', { hasText: 'Health check plan' })).toBeVisible()
  await shot('24-artifact')
  await page.keyboard.press('i')
  await expect(page.locator('.source-editor .cm-focused')).toBeVisible()
  await page.keyboard.press('Meta+ArrowDown')
  await page.keyboard.press('Enter')
  await page.keyboard.type('- ship it')
  await page.keyboard.press('Escape')
  await expect(page.locator('.statusline .sl-name', { hasText: 'plan [+]' })).toBeVisible()
  await page.keyboard.press('Meta+s')
  await expect.poll(() => hub.calls.find((c) => c.method === 'PUT')?.body).toMatchObject({ base: 1, text: '# Plan\n\n- add /healthz\n- test it\n\n- ship it' })
  await expect(page.locator('.statusline .sl-name', { hasText: /^plan$/ })).toBeVisible()

  // The agent writes v3 while you edit: the save is refused, :w! forces it.
  await page.keyboard.press('i')
  await page.keyboard.type('x')
  await page.keyboard.press('Escape')
  hub.artifact('demo', 'plan', 'Health check plan', '# Plan\n\nnew\n', 3)
  await expect(page.locator('.cmdline', { hasText: 'wrote v3 while you edit' })).toBeVisible()
  await page.keyboard.press('Meta+s')
  await expect(page.locator('.cmdline', { hasText: ':w! saves over it' })).toBeVisible()
  await expect(page.locator('.conflict', { hasText: 'changed the same lines in v3' })).toBeVisible()
  await expect(page.locator('.diff-line.d-del', { hasText: 'new' })).toBeVisible()
  await shot('25-artifact-conflict')
  await page.keyboard.type(':')
  await page.keyboard.type('w!')
  await page.keyboard.press('Enter')
  await expect.poll(() => hub.calls.filter((c) => c.method === 'PUT').pop()?.body).toMatchObject({ force: true })
  await expect(page.locator('.conflict')).toHaveCount(0)
  await page.keyboard.press('Control+o')
  await expect(page.locator('.statusline .sl-name', { hasText: /^api$/ })).toBeVisible()
})

test('a hub that stops says so and offers to start it', async () => {
  hub.stop()
  await expect(page.locator('.banner', { hasText: 'neoharness hub start' })).toBeVisible()
  await expect(page.locator('.banner button', { hasText: 'Start the hub' })).toBeVisible()
  await shot('26-hub-stopped')
})
