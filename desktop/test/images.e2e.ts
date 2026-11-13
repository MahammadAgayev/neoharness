// The app against the fake hub: an image pasted into the prompt.

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
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'nhimg-'))
  hub = new FakeHub(path.join(tmp, 'hub.sock'))
  hub.workspace('demo', [])
  await hub.listen()
  app = await electron.launch({
    args: [path.join(__dirname, '..', 'out', 'main', 'index.js')],
    env: { ...process.env, NEOHARNESS_DESKTOP_TEST: process.env.NEOHARNESS_DESKTOP_TEST ?? 'shown', NEOHARNESS_HOME: tmp, NEOHARNESS_BIN: '/usr/bin/false' },
  })
  page = await app.firstWindow()
  await page.setViewportSize({ width: 1200, height: 800 })
  await expect(page.locator('.ws-row', { hasText: 'demo' })).toBeVisible({ timeout: 20_000 })
  await page.keyboard.press('Enter')
  await expect(page.locator('.statusline .sl-name', { hasText: 'coordinator' })).toBeVisible()
})

test.afterAll(async () => {
  await app?.close()
  await hub?.close()
})

// paste sends the prompt a paste event with a PNG of that size, made in the
// page, as a screenshot on the clipboard is. The person's clipboard is not
// touched.
async function paste(width: number, height: number) {
  await page.evaluate(
    async ([w, h]) => {
      const c = new OffscreenCanvas(w, h)
      const g = c.getContext('2d')!
      // Noise, so the PNG of a big one is too big to send as it is.
      const img = g.createImageData(w, h)
      for (let i = 0; i < img.data.length; i++) img.data[i] = i % 4 === 3 ? 255 : Math.random() * 256
      g.putImageData(img, 0, 0)
      const blob = await c.convertToBlob({ type: 'image/png' })
      const dt = new DataTransfer()
      dt.items.add(new File([blob], 'image.png', { type: 'image/png' }))
      document.querySelector('.prompt-editor .cm-content')!.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }))
    },
    [width, height],
  )
}

const sent = () => hub.calls.filter((c) => c.path.endsWith('/messages')).at(-1)?.body as { text: string; images: string[] } | undefined

test('a pasted image shows as [image 1] and goes with the message', async () => {
  await page.keyboard.press('i')
  await page.keyboard.type('what is in ')
  await paste(8, 8)
  await expect(page.locator('.prompt .cm-image', { hasText: '[image 1]' })).toBeVisible()
  await page.keyboard.type('and ')
  await paste(3000, 2000)
  await expect(page.locator('.prompt .cm-image', { hasText: '[image 2]' })).toBeVisible()
  await page.waitForTimeout(250)
  await page.screenshot({ path: path.join(__dirname, '..', 'test-results', '60-pasted-images.png') })
  await page.keyboard.press('Enter')
  await expect.poll(() => sent()?.images?.length).toBe(2)
  expect(sent()!.text).toBe('what is in [image 1] and [image 2]')
  const big = Buffer.from(sent()!.images[1], 'base64')
  // A PNG or a JPEG, made smaller to what the model works at.
  expect(big.length).toBeLessThanOrEqual((5 << 20) * 0.75)
  if (big.subarray(1, 4).toString() === 'PNG') expect(big.readUInt32BE(16)).toBe(1568)
  await expect(page.locator('.prompt .cm-image')).toHaveCount(0)
})

test('an image whose token is deleted does not go', async () => {
  await page.keyboard.type('first ')
  await paste(8, 8)
  await expect(page.locator('.prompt .cm-image')).toHaveCount(1)
  for (let i = 0; i < '[image 1] '.length; i++) await page.keyboard.press('Backspace')
  await expect(page.locator('.prompt .cm-image')).toHaveCount(0)
  await page.keyboard.type('then ')
  await paste(8, 8)
  await expect(page.locator('.prompt .cm-image', { hasText: '[image 2]' })).toBeVisible()
  await page.keyboard.press('Enter')
  await expect.poll(() => sent()?.text).toBe('first then [image 1]')
  expect(sent()!.images.length).toBe(1)
})
