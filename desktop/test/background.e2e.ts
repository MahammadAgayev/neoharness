// A thread whose turn ended while its work runs on shows the background
// state on its board row and its status line, on the fake hub.

import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

import { _electron as electron, expect, test, type ElectronApplication, type Page } from '@playwright/test'

import { FakeHub, view } from './fakehub'

let tmp = ''
let hub: FakeHub
let app: ElectronApplication
let page: Page

test.beforeAll(async () => {
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'nhbg-'))
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

test('background work shows on the board row and the status line, and its end does not toast', async () => {
  hub.setView(view('demo', 'api', { state: 'running' }))
  hub.setView(view('demo', 'api', { state: 'background', background: 2 }))
  const row = page.locator('.window.threads .list-row', { hasText: 'api' })
  await expect(row).toContainText('2 in background')
  await expect(page.locator('.window.threads .group-head', { hasText: 'background · 1' })).toBeVisible()
  await expect(page.locator('.toast')).toHaveCount(0)
  await page.keyboard.press('Escape')
  await page.keyboard.type(':b api')
  await page.keyboard.press('Enter')
  await expect(page.locator('.statusline.focused .sl-state')).toContainText('2 in background')
})
