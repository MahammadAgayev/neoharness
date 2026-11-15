// Enter on a thread in the list opens it in one thread window beside the
// coordinator, so the coordinator and the list stay, on the fake hub.

import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

import { _electron as electron, expect, test, type ElectronApplication, type Page } from '@playwright/test'

import { FakeHub } from './fakehub'

let tmp = ''
let hub: FakeHub
let app: ElectronApplication
let page: Page

test.beforeAll(async () => {
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'nhsplit-'))
  hub = new FakeHub(path.join(tmp, 'hub.sock'))
  hub.workspace('demo', ['api', 'web'])
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
})

test.afterAll(async () => {
  await app?.close()
  await hub?.close()
})

const columns = () => page.locator('.layout > .column').evaluateAll((cs) => cs.map((c) => (c.querySelector('.sl-name')?.textContent ?? '').trim()))

async function enterOn(name: string) {
  await page.keyboard.press('Escape')
  await page.keyboard.press('Escape')
  await page.keyboard.press('Control+w')
  await page.keyboard.press('l')
  const rows = page.locator('.window.threads .list-row')
  await expect(page.locator('.window.threads .list-row.cursor')).toBeVisible()
  const want = (await rows.allTextContents()).findIndex((t) => t.includes(name))
  await page.keyboard.press('g')
  await page.keyboard.press('g')
  for (let i = 0; i < want; i++) await page.keyboard.press('j')
  await page.keyboard.press('Enter')
}

test('Enter on a thread opens one thread window between the coordinator and the list, and a narrow window reuses the coordinator', async () => {
  await enterOn('api')
  await expect.poll(columns).toEqual(['coordinator', 'api', 'threads'])
  await expect(page.locator('.statusline.focused .sl-name')).toHaveText('api')
  await enterOn('web')
  await expect.poll(columns).toEqual(['coordinator', 'web', 'threads'])
  await page.keyboard.press('Escape')
  await page.keyboard.press('Escape')
  await page.keyboard.type(':q')
  await page.keyboard.press('Enter')
  await expect.poll(columns).toEqual(['coordinator', 'threads'])
  await page.setViewportSize({ width: 760, height: 860 })
  await enterOn('api')
  await expect.poll(columns).toEqual(['api', 'threads'])
})
