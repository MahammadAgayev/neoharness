// A real turn on a scratch hub, with a small model. It costs a little, so
// it runs only with NEOHARNESS_LIVE=1.

import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'

import { _electron as electron, expect, test } from '@playwright/test'

test.skip(!process.env.NEOHARNESS_LIVE, 'set NEOHARNESS_LIVE=1 to run a real turn')

function freePort(): Promise<number> {
  return new Promise((resolve) => {
    const s = net.createServer()
    s.listen(0, '127.0.0.1', () => {
      const port = (s.address() as net.AddressInfo).port
      s.close(() => resolve(port))
    })
  })
}

test('a thread runs a turn: tool calls, a reply in markdown, a finished toast', async () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'nhlive-'))
  const home = path.join(tmp, 'home')
  const dir = path.join(tmp, 'dir')
  fs.mkdirSync(home)
  fs.mkdirSync(dir)
  fs.writeFileSync(path.join(dir, 'main.go'), 'package main\n\nfunc main() { println("hi") }\n')
  fs.writeFileSync(
    path.join(home, 'init.lua'),
    `require("neoharness").setup({ mcp_port = ${await freePort()} })\nrequire("neoharness.agents").setup({ claude = { model = "haiku" } })\n`,
  )
  const bin = process.env.NEOHARNESS_BIN || path.join(tmp, 'neoharness')
  if (!process.env.NEOHARNESS_BIN) execFileSync('go', ['build', '-o', bin, './cmd/neoharness'], { cwd: path.resolve(__dirname, '../..') })
  const app = await electron.launch({
    args: [path.join(__dirname, '..', 'out', 'main', 'index.js')],
    env: { ...process.env, NEOHARNESS_DESKTOP_TEST: process.env.NEOHARNESS_DESKTOP_TEST ?? 'shown', NEOHARNESS_HOME: home, NEOHARNESS_BIN: bin },
  })
  const page = await app.firstWindow()
  await page.setViewportSize({ width: 1400, height: 860 })
  const shot = async (name: string) => {
    await page.waitForTimeout(250)
    await page.screenshot({ path: path.join(__dirname, '..', 'test-results', `${name}.png`) })
  }
  try {
    await expect(page.locator('.banner')).toHaveCount(0, { timeout: 30_000 })
    await page.keyboard.type(':')
    await page.keyboard.type(`new live ${dir}`)
    await page.keyboard.press('Enter')
    // :new leaves you writing to the coordinator.
    await expect(page.locator('.prompt .cm-focused')).toBeVisible()
    await page.keyboard.press('Escape')
    await page.keyboard.press('Escape')
    await page.keyboard.type(':')
    await page.keyboard.type(`open api ${dir}`)
    await page.keyboard.press('Enter')
    await expect(page.locator('.statusline .sl-name', { hasText: /^api$/ })).toBeVisible()
    await page.keyboard.type('Read main.go, then reply with a short markdown note: a heading, two bullets and the file in a go code block.')
    await shot('10-live-typed')
    await page.keyboard.press('Enter')
    const api = page.locator('.statusline', { has: page.locator('.sl-name', { hasText: /^api$/ }) }).locator('.sl-state')
    await expect(page.locator('.b-user', { hasText: /^Read main.go/ })).toBeVisible()
    await expect(api).toHaveText(/running/, { timeout: 30_000 })
    await shot('11-live-running')
    await expect(api).toHaveText(/idle/, { timeout: 120_000 })
    await expect(page.locator('.b-text')).toBeVisible()
    await shot('12-live-done')
    // Open the tool calls.
    await page.locator('.b-tools-head').first().click()
    await shot('13-live-tools')
  } finally {
    await app.close()
    try {
      execFileSync(bin, ['hub', 'stop'], { env: { ...process.env, NEOHARNESS_HOME: home } })
    } catch {
      // No hub ran.
    }
  }
})
