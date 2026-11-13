// The app end to end, on a scratch hub: its own NEOHARNESS_HOME and MCP
// port, never the person's ~/.neoharness. No message goes to a model.

import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'

import { _electron as electron, expect, test, type ElectronApplication, type Page } from '@playwright/test'

const repo = path.resolve(__dirname, '../..')
let tmp = ''
let bin = ''
let app: ElectronApplication
let page: Page

function freePort(): Promise<number> {
  return new Promise((resolve) => {
    const s = net.createServer()
    s.listen(0, '127.0.0.1', () => {
      const port = (s.address() as net.AddressInfo).port
      s.close(() => resolve(port))
    })
  })
}

async function ex(line: string) {
  await page.keyboard.press('Escape')
  await page.keyboard.press('Escape')
  await page.keyboard.type(':')
  await expect(page.locator('.cmdline input')).toBeFocused()
  await page.keyboard.type(line)
  await page.keyboard.press('Enter')
}

async function shot(name: string) {
  // Let the open animations end.
  await page.waitForTimeout(250)
  await page.screenshot({ path: path.join(__dirname, '..', 'test-results', `${name}.png`) })
}

test.beforeAll(async () => {
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'nhdesk-'))
  const home = path.join(tmp, 'home')
  fs.mkdirSync(path.join(tmp, 'dir', 'api'), { recursive: true })
  fs.mkdirSync(home)
  fs.writeFileSync(
    path.join(home, 'init.lua'),
    `require("neoharness").setup({ mcp_port = ${await freePort()} })\nrequire("neoharness.agents").setup({ claude = { model = "haiku" } })\n`,
  )
  bin = process.env.NEOHARNESS_BIN || path.join(tmp, 'neoharness')
  if (!process.env.NEOHARNESS_BIN) execFileSync('go', ['build', '-o', bin, './cmd/neoharness'], { cwd: repo })
  app = await electron.launch({
    args: [path.join(__dirname, '..', 'out', 'main', 'index.js')],
    env: { ...process.env, NEOHARNESS_DESKTOP_HIDDEN: '1', NEOHARNESS_HOME: home, NEOHARNESS_BIN: bin },
  })
  page = await app.firstWindow()
  await page.setViewportSize({ width: 1400, height: 860 })
})

test.afterAll(async () => {
  await app?.close()
  if (tmp) {
    try {
      execFileSync(bin, ['hub', 'stop'], { env: { ...process.env, NEOHARNESS_HOME: path.join(tmp, 'home') } })
    } catch {
      // No hub ran.
    }
  }
})

test('the hub starts, a workspace and a thread open, search everywhere finds them', async () => {
  // The app started the scratch hub, not the real one.
  await expect(page.getByRole('heading', { name: 'Workspaces' })).toBeVisible({ timeout: 30_000 })
  await expect(page.locator('.banner')).toHaveCount(0, { timeout: 30_000 })
  expect(fs.existsSync(path.join(tmp, 'home', 'hub.sock'))).toBe(true)
  await shot('01-workspaces-empty')

  await ex(`new demo ${path.join(tmp, 'dir')}`)
  await expect(page.locator('.statusline .sl-name', { hasText: 'coordinator' })).toBeVisible()
  await expect(page.locator('.prompt .cm-focused')).toBeVisible()
  await expect(page.locator('.statusline .sl-name', { hasText: 'threads' })).toBeVisible()
  await shot('02-workspace')

  await ex(`open api ${path.join(tmp, 'dir', 'api')}`)
  await expect(page.locator('.statusline .sl-name', { hasText: /^api$/ })).toBeVisible()
  await shot('03-thread')

  // <leader>t brings the threads list back in a column of its own.
  await page.keyboard.press('Escape')
  await page.keyboard.press('Escape')
  await page.keyboard.press('Space')
  await page.keyboard.press('t')
  await expect(page.locator('.list-row .name', { hasText: 'api' })).toBeVisible()

  await page.keyboard.press('Meta+k')
  await expect(page.locator('.palette')).toBeVisible()
  await page.keyboard.type('api')
  await expect(page.locator('.palette-row .palette-label', { hasText: 'demo · api' })).toBeVisible()
  await shot('04-search-everywhere')
  await page.keyboard.press('Tab')
  await expect(page.locator('.tab.active', { hasText: 'Threads' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.locator('.palette')).toHaveCount(0)

  await ex('colorscheme rose-pine-dawn')
  await expect.poll(() => page.evaluate(() => document.documentElement.style.getPropertyValue('--base'))).toBe('#faf4ed')
  await shot('05-dawn')
  await ex('colorscheme rose-pine-moon')

  await ex('help')
  await expect(page.locator('.help-title')).toBeVisible()
  await shot('06-help')
})

test('the workspaces screen lists the workspace and opens it', async () => {
  await ex('workspaces')
  await expect(page.locator('.ws-row .ws-name', { hasText: 'demo' })).toBeVisible()
  await shot('07-workspaces')
  await page.keyboard.press('Enter')
  await expect(page.locator('.statusline .sl-name', { hasText: 'coordinator' })).toBeVisible()
})
