// After an update the installed neoharness is newer than the hub that runs:
// the app restarts the hub on it, at once when no turn runs, else when the
// turns end. A fake neoharness says its version and records the restart.

import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

import { _electron as electron, expect, test } from '@playwright/test'

import { FakeHub } from './fakehub'

test('a newer neoharness restarts the hub once the turns end', async () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'nhrestart-'))
  const hub = new FakeHub(path.join(tmp, 'hub.sock'))
  hub.version = 'old0000'
  hub.workspace('demo', ['api'])
  hub.workspaces[0].counts = { running: 1 }
  await hub.listen()
  const marker = path.join(tmp, 'restarted')
  const bin = path.join(tmp, 'neoharness')
  fs.writeFileSync(bin, `#!/bin/sh\ncase "$*" in\nversion) echo new1111 ;;\n"hub restart") echo yes > '${marker}' ;;\nesac\n`, { mode: 0o755 })

  const app = await electron.launch({
    args: [path.join(__dirname, '..', 'out', 'main', 'index.js')],
    env: { ...process.env, NEOHARNESS_DESKTOP_TEST: process.env.NEOHARNESS_DESKTOP_TEST ?? 'shown', NEOHARNESS_HOME: tmp, NEOHARNESS_BIN: bin },
  })
  try {
    const page = await app.firstWindow()
    await expect(page.locator('.ws-row', { hasText: 'demo' })).toBeVisible({ timeout: 20_000 })
    // A turn runs: the hub waits.
    await expect(page.locator('.cmdline', { hasText: 'restarts on it when the turns end' })).toBeVisible()
    await page.waitForTimeout(500)
    expect(fs.existsSync(marker)).toBe(false)
    // The turn ends: now it restarts.
    hub.push({ kind: 'workspace', workspace: 'demo', summary: { ...hub.workspaces[0], counts: { idle: 1 } } })
    await expect.poll(() => fs.existsSync(marker), { timeout: 10_000 }).toBe(true)
  } finally {
    await app.close()
    await hub.close()
  }
})
