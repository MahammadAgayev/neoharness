// The artifact as a rendered page you write in, on the fake hub: what it
// shows, Vim's Normal mode over it, and the markdown it saves.

import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

import { _electron as electron, expect, test, type ElectronApplication, type Page } from '@playwright/test'

import { FakeHub } from './fakehub'

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
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'nhdoc-'))
  hub = new FakeHub(path.join(tmp, 'hub.sock'))
  hub.workspace('demo', ['api'])
  hub.event('demo', 'api', { kind: 'message', from: 'user', text: 'write the rollout' })
  hub.event('demo', 'api', { kind: 'turn' })
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
  await page.locator('.window.threads .list-row', { hasText: 'api' }).click()
  await expect(page.locator('.statusline .sl-name', { hasText: /^api$/ })).toBeVisible()
})

test.afterAll(async () => {
  await app?.close()
  await hub?.close()
})

test('an artifact edits as a rendered page: tables, tasks, code and Vim motions, saved as the same markdown', async () => {
  const text =
    '# Rollout\n\nIntro with `code` and a [link](https://example.com).\n\n| Step | Owner |\n|------|:-----:|\n| build | api |\n| ship | ops |\n\n- [ ] canary\n- [x] tests\n\n```go\nfunc main() {}\n```\n\n> [!NOTE]\n> Keep the old flag.\n'
  hub.artifact('demo', 'rollout', 'Rollout', text)
  hub.event('demo', 'api', { kind: 'artifact', detail: 'rollout', text: 'Rollout', version: 1 })
  await page.locator('.b-artifact', { hasText: 'Rollout' }).click()
  const win = page.locator('.window.artifact', { has: page.locator('.sl-name', { hasText: 'rollout' }) })
  const doc = win.locator('.doc-page')
  await expect(doc.locator('table td', { hasText: 'build' })).toBeVisible()
  await expect(doc.locator('li.task')).toHaveCount(2)
  await expect(doc.locator('li.task.done')).toHaveCount(1)
  await expect(doc.locator('li.task.done .task-body')).toHaveCSS('text-decoration-line', 'line-through')
  await expect(doc.locator('li.task:not(.done) .task-body')).toHaveCSS('text-decoration-line', 'none')
  await expect(doc.locator('pre.code .hljs-keyword', { hasText: 'func' })).toBeVisible()
  await expect(doc.locator('blockquote.alert.alert-note')).toBeVisible()
  // [!NOTE] shows as the alert's title, not as the text it is.
  await expect(doc.locator('.alert-mark[data-label="Note"]')).toHaveCSS('font-size', '0px')
  await shot('39-artifact-page')
  // A click on a task's box checks it; only its list changes in the text.
  await doc.locator('li.task .task-box').first().click()
  await expect(doc.locator('li.task.done')).toHaveCount(2)
  await expect(win.locator('.sl-name', { hasText: 'rollout [+]' })).toBeVisible()
  // In a table cell, Vim's w and cw change one word.
  await doc.locator('td', { hasText: 'ship' }).click()
  await page.keyboard.press('Escape')
  await page.keyboard.press('0')
  await page.keyboard.press('c')
  await page.keyboard.press('w')
  await page.keyboard.type('deploy')
  await page.keyboard.press('Escape')
  await expect(doc.locator('td', { hasText: 'deploy' })).toBeVisible()
  // u takes the change back, <C-r> does it again.
  await page.keyboard.press('u')
  await expect(doc.locator('td', { hasText: 'ship' })).toBeVisible()
  await page.keyboard.press('Control+r')
  await expect(doc.locator('td', { hasText: 'deploy' })).toBeVisible()
  // / finds a word and the cursor goes to it; dd takes its block away.
  await page.keyboard.press('/')
  await page.keyboard.type('Intro')
  await page.keyboard.press('Enter')
  await page.keyboard.press('d')
  await page.keyboard.press('d')
  await expect(doc).not.toContainText('Intro with')
  await page.keyboard.press('Meta+s')
  await expect
    .poll(() => hub.calls.filter((c) => c.method === 'PUT' && c.path.endsWith('/artifacts/rollout')).pop()?.body)
    .toMatchObject({
      text: '# Rollout\n\n| Step | Owner |\n|---|:---:|\n| build | api |\n| deploy | ops |\n\n- [x] canary\n- [x] tests\n\n```go\nfunc main() {}\n```\n\n> [!NOTE]\n> Keep the old flag.\n',
    })
  await normal()
  await page.keyboard.press('Control+o')
})

test('the page cursor is a block in Normal and Visual mode and a line in Insert, as in Vim', async () => {
  hub.artifact('demo', 'cursor', 'Cursor', '# Cursor\n\nalpha beta\n')
  hub.event('demo', 'api', { kind: 'artifact', detail: 'cursor', text: 'Cursor', version: 1 })
  await page.locator('.b-artifact', { hasText: 'Cursor' }).click()
  const win = page.locator('.window.artifact', { has: page.locator('.sl-name', { hasText: 'cursor' }) })
  const doc = win.locator('.doc-page')
  await doc.locator('p', { hasText: 'alpha' }).click()
  await normal()
  await page.keyboard.press('0')
  const fat = doc.locator('.doc-fat-cursor')
  await expect(fat).toHaveCount(1)
  await expect(fat).toHaveText('a')
  await expect(doc).toHaveCSS('caret-color', 'rgba(0, 0, 0, 0)')
  await page.keyboard.press('w')
  await expect(fat).toHaveText('b')
  await shot('41-artifact-normal-cursor')
  await page.keyboard.press('v')
  await page.keyboard.press('l')
  await expect(fat).toHaveCount(1)
  await expect(fat).toHaveText('e')
  await shot('42-artifact-visual-cursor')
  await page.keyboard.press('Escape')
  await page.keyboard.press('i')
  await expect(fat).toHaveCount(0)
  await expect(doc).not.toHaveCSS('caret-color', 'rgba(0, 0, 0, 0)')
  // On an empty block the block cursor still shows, one space wide.
  await page.keyboard.press('Escape')
  await page.keyboard.press('o')
  await page.keyboard.press('Escape')
  await expect(fat).toHaveCount(1)
  await page.keyboard.press('u')
  await normal()
  await page.keyboard.press('Control+o')
})

test('a done task in a reply is struck through, an open one is not', async () => {
  hub.event('demo', 'api', { kind: 'text', text: 'Where it stands:\n\n- [x] ship the fix\n- [ ] write the docs\n' })
  const done = page.locator('.md li', { hasText: 'ship the fix' })
  await expect(done).toHaveCSS('text-decoration-line', 'line-through')
  await expect(page.locator('.md li', { hasText: 'write the docs' })).toHaveCSS('text-decoration-line', 'none')
})
