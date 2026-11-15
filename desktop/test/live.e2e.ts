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

test('an agent rewrites an artifact while you edit it, and :w keeps both', async () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'nhlive-'))
  const home = path.join(tmp, 'home')
  const dir = path.join(tmp, 'dir')
  fs.mkdirSync(home)
  fs.mkdirSync(dir)
  const guide =
    '# Guide\n\nHow the **rollout** goes, with `flags` and a [link](https://example.com).\n\n## Steps\n\n- [ ] build\n- [x] test\n\n| Step | Owner |\n|---|---|\n| build | api |\n| ship | ops |\n\n```go\nfunc main() {}\n```\n\n> Keep the old flag.\n'
  fs.writeFileSync(path.join(dir, 'guide.md'), guide)
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
  // NEOHARNESS_SHOTS names a folder for the screenshots, as /tmp.
  const shot = async (name: string) => {
    await page.waitForTimeout(250)
    await page.screenshot({ path: path.join(process.env.NEOHARNESS_SHOTS ?? path.join(__dirname, '..', 'test-results'), `${name}.png`) })
  }
  const ex = async (line: string) => {
    await page.keyboard.press('Escape')
    await page.keyboard.press('Escape')
    await page.keyboard.type(':')
    await expect(page.locator('.cmdline input')).toBeFocused()
    await page.keyboard.type(line)
    await page.keyboard.press('Enter')
  }
  const send = async (text: string) => {
    await page.keyboard.press('Escape')
    await page.keyboard.press('Escape')
    await page.keyboard.press('i')
    await page.keyboard.type(text)
    await page.keyboard.press('Enter')
  }
  try {
    await expect(page.locator('.banner')).toHaveCount(0, { timeout: 30_000 })
    await expect(page.locator('h1', { hasText: 'Workspaces' })).toBeVisible({ timeout: 30_000 })
    // Type only once the scratch hub answers.
    await expect.poll(() => fs.existsSync(path.join(home, 'hub.sock')), { timeout: 30_000 }).toBe(true)
    await expect(page.locator('.banner')).toHaveCount(0, { timeout: 30_000 })
    await ex(`new live ${dir}`)
    await expect(page.locator('.prompt .cm-focused')).toBeVisible()
    await ex(`open api ${dir}`)
    await expect(page.locator('.statusline .sl-name', { hasText: /^api$/ })).toBeVisible()
    const api = page.locator('.statusline', { has: page.locator('.sl-name', { hasText: /^api$/ }) }).locator('.sl-state')
    await send('Read guide.md and call write_artifact with name "guide", a short title, and the exact text of the file. Do nothing else.')
    await expect(api).toHaveText(/idle/, { timeout: 180_000 })
    await page.locator('.b-artifact').first().click()
    const doc = page.locator('.window.artifact .doc-page')
    await expect(doc.locator('table td', { hasText: 'build' })).toBeVisible()
    await shot('50-live-artifact')

    // You write at the end, rendered; the edit is not saved yet.
    await page.keyboard.press('G')
    await page.keyboard.press('o')
    await page.keyboard.type('A line the person added, with **bold**.')
    await page.keyboard.press('Escape')
    await expect(page.locator('.statusline .sl-name', { hasText: 'guide [+]' })).toBeVisible()
    await shot('51-live-edit')

    // The agent rewrites the artifact meanwhile.
    await ex('vsplit api')
    await send('Call read_artifact on "guide", then call write_artifact "guide" with the same text, except the first line is "# Rollout guide". Change nothing else.')
    await expect(page.locator('.cmdline', { hasText: /merged into your edit|changed the same blocks/ })).toBeVisible({ timeout: 180_000 })
    await expect(api.first()).toHaveText(/idle/, { timeout: 180_000 })
    await shot('52-live-agent-wrote')

    // The hub merged it into the page at once; where both changed a block,
    // you take theirs there. :w then saves both.
    await page.keyboard.press('Escape')
    await page.keyboard.press('Escape')
    await page.keyboard.press('Control+w')
    await page.keyboard.press('h')
    const theirs = page.locator('.doc-theirs')
    if (await theirs.count()) {
      await shot('53-live-conflict')
      for (let k = 0; k < 20 && (await theirs.count()) > 0; k++) await theirs.first().locator('button', { hasText: 'Take' }).click()
    }
    await expect(doc.locator('h1')).toHaveText('Rollout guide')
    await page.keyboard.type(':')
    await page.keyboard.type('w')
    await page.keyboard.press('Enter')
    await expect(page.locator('.statusline .sl-name', { hasText: /^guide$/ })).toBeVisible({ timeout: 10_000 })
    await expect(doc.locator('h1')).toHaveText('Rollout guide')
    await expect(doc.locator('p', { hasText: 'A line the person added' }).locator('strong')).toHaveText('bold')
    await shot('54-live-merged')
    const saved = fs.readFileSync(path.join(home, 'workspaces', 'live', 'artifacts', 'guide.md'), 'utf8')
    expect(saved).toContain('# Rollout guide')
    expect(saved).toContain('A line the person added, with **bold**.')
    expect(saved).toContain('| ship | ops |')
  } finally {
    await app.close()
    try {
      execFileSync(bin, ['hub', 'stop'], { env: { ...process.env, NEOHARNESS_HOME: home } })
    } catch {
      // No hub ran.
    }
  }
})
