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

test('in Visual mode h j k l move the cursor and the selection follows, as in Vim', async () => {
  hub.artifact('demo', 'visual', 'Visual', '# Visual\n\nalpha beta gamma\n\nsecond line here\n\nthird\n')
  hub.event('demo', 'api', { kind: 'artifact', detail: 'visual', text: 'Visual', version: 1 })
  await page.locator('.b-artifact', { hasText: 'Visual' }).click()
  const win = page.locator('.window.artifact', { has: page.locator('.sl-name', { hasText: 'visual' }) })
  const doc = win.locator('.doc-page')
  const selected = () => page.evaluate(() => window.getSelection()?.toString() ?? '')
  await doc.locator('p', { hasText: 'alpha' }).click()
  await normal()
  await page.keyboard.press('0')
  // v takes the letter under the cursor; l and h grow and shrink it.
  await page.keyboard.press('v')
  await expect.poll(selected).toBe('a')
  await page.keyboard.press('l')
  await page.keyboard.press('l')
  await expect.poll(selected).toBe('alp')
  await page.keyboard.press('h')
  await expect.poll(selected).toBe('al')
  // j goes down a line and the selection goes with it; k comes back.
  await page.keyboard.press('j')
  await expect.poll(selected).toMatch(/^alpha beta gamma\s+se$/)
  await page.keyboard.press('k')
  await expect.poll(selected).toBe('al')
  await page.keyboard.press('y')
  await expect.poll(selected).toBe('')
  // V takes whole blocks; j adds the next one.
  await page.keyboard.press('V')
  await expect.poll(selected).toBe('alpha beta gamma')
  await page.keyboard.press('j')
  await expect.poll(selected).toMatch(/^alpha beta gamma\s+second line here$/)
  await page.keyboard.press('k')
  await expect.poll(selected).toBe('alpha beta gamma')
  await page.keyboard.press('Escape')
  await page.keyboard.press('Control+o')
})

test('<Esc> puts the cursor on the last letter typed, and u takes back one Insert at a time, as in Vim', async () => {
  hub.artifact('demo', 'undo', 'Undo', '# Undo\n\nstart\n')
  hub.event('demo', 'api', { kind: 'artifact', detail: 'undo', text: 'Undo', version: 1 })
  await page.locator('.b-artifact', { hasText: 'Undo' }).click()
  const win = page.locator('.window.artifact', { has: page.locator('.sl-name', { hasText: 'undo' }) })
  const doc = win.locator('.doc-page')
  const p = doc.locator('p').first()
  await p.click()
  await normal()
  await page.keyboard.press('A')
  await page.keyboard.type(' ab')
  await page.waitForTimeout(700)
  await page.keyboard.type('cd')
  await page.keyboard.press('Escape')
  await expect(doc.locator('.doc-fat-cursor')).toHaveText('d')
  await page.keyboard.press('x')
  await expect(p).toHaveText('start abc')
  // x is its own step, and the slow Insert before it is one more.
  await page.keyboard.press('u')
  await expect(p).toHaveText('start abcd')
  await page.keyboard.press('u')
  await expect(p).toHaveText('start')
  await page.keyboard.press('Control+o')
})

test('Enter in a table goes to the cell below, adds a row at the end, and leaves on an empty row', async () => {
  hub.artifact('demo', 'grid', 'Grid', '# Grid\n\n| a | b |\n|---|---|\n| 1 | 2 |\n')
  hub.event('demo', 'api', { kind: 'artifact', detail: 'grid', text: 'Grid', version: 1 })
  await page.locator('.b-artifact', { hasText: 'Grid' }).click()
  const win = page.locator('.window.artifact', { has: page.locator('.sl-name', { hasText: 'grid' }) })
  const doc = win.locator('.doc-page')
  await doc.locator('td', { hasText: '1' }).click()
  await normal()
  await page.keyboard.press('A')
  // A cell is one line in markdown, so Shift-Enter adds no break.
  await page.keyboard.press('Shift+Enter')
  await page.keyboard.press('Enter')
  await page.keyboard.type('x')
  await page.keyboard.press('Enter')
  await page.keyboard.press('Enter')
  await page.keyboard.type('after')
  await expect(doc.locator('tr')).toHaveCount(3)
  await expect(doc.locator('p', { hasText: 'after' })).toBeVisible()
  await page.keyboard.press('Meta+s')
  await expect
    .poll(() => hub.calls.filter((c) => c.method === 'PUT' && c.path.endsWith('/artifacts/grid')).pop()?.body)
    .toMatchObject({ text: '# Grid\n\n| a | b |\n|---|---|\n| 1 | 2 |\n| x |  |\n\nafter\n' })
  await normal()
  await page.keyboard.press('Control+o')
})

test('Enter in a list keeps the sub-items where they are, and on an empty item leaves the list', async () => {
  hub.artifact('demo', 'lists', 'Lists', '# Lists\n\n- one\n- two\n  - nested\n- three\n')
  hub.event('demo', 'api', { kind: 'artifact', detail: 'lists', text: 'Lists', version: 1 })
  await page.locator('.b-artifact', { hasText: 'Lists' }).click()
  const win = page.locator('.window.artifact', { has: page.locator('.sl-name', { hasText: 'lists' }) })
  const doc = win.locator('.doc-page')
  const saved = async (text: string) => {
    await page.keyboard.press('Meta+s')
    await expect
      .poll(() => hub.calls.filter((c) => c.method === 'PUT' && c.path.endsWith('/artifacts/lists')).pop()?.body)
      .toMatchObject({ text })
  }
  // At the end of an item with sub-items, the new item is the first of them.
  await doc.locator('li p', { hasText: /^two$/ }).click()
  await normal()
  await page.keyboard.press('A')
  await page.keyboard.press('Enter')
  await page.keyboard.type('new')
  await saved('# Lists\n\n- one\n- two\n  - new\n  - nested\n- three\n')
  // Enter on an empty nested item takes it out one level.
  await page.keyboard.press('Enter')
  await page.keyboard.press('Enter')
  await page.keyboard.type('back')
  await saved('# Lists\n\n- one\n- two\n  - new\n- back\n  - nested\n- three\n')
  // Enter on an empty item at the top leaves the list.
  await doc.locator('li p', { hasText: /^three$/ }).click()
  await normal()
  await page.keyboard.press('A')
  await page.keyboard.press('Enter')
  await page.keyboard.press('Enter')
  await page.keyboard.type('para')
  await saved('# Lists\n\n- one\n- two\n  - new\n- back\n  - nested\n- three\n\npara\n')
  await normal()
  await page.keyboard.press('Control+o')
})

test('Backspace right after a markdown shortcut gives back the text you typed', async () => {
  hub.artifact('demo', 'rules', 'Rules', '# Rules\n\nend\n')
  hub.event('demo', 'api', { kind: 'artifact', detail: 'rules', text: 'Rules', version: 1 })
  await page.locator('.b-artifact', { hasText: 'Rules' }).click()
  const win = page.locator('.window.artifact', { has: page.locator('.sl-name', { hasText: 'rules' }) })
  const doc = win.locator('.doc-page')
  await doc.locator('p', { hasText: 'end' }).click()
  await normal()
  await page.keyboard.press('o')
  await page.keyboard.type('1. ')
  await expect(doc.locator('ol')).toHaveCount(1)
  await page.keyboard.press('Backspace')
  await expect(doc.locator('ol')).toHaveCount(0)
  await expect(doc.locator('p', { hasText: /^1\. $/ })).toHaveCount(1)
  await page.keyboard.press('Enter')
  await page.keyboard.type('---')
  await expect(doc.locator('hr')).toHaveCount(1)
  await page.keyboard.press('Backspace')
  await expect(doc.locator('hr')).toHaveCount(0)
  await expect(doc.locator('p', { hasText: /^---$/ })).toHaveCount(1)
  await normal()
  await page.keyboard.press('Control+o')
})

test('a mouse drag selects in Visual mode, so d takes the words away, as with Vim and a mouse', async () => {
  hub.artifact('demo', 'drag', 'Drag', '# Drag\n\nfirst second third\n')
  hub.event('demo', 'api', { kind: 'artifact', detail: 'drag', text: 'Drag', version: 1 })
  await page.locator('.b-artifact', { hasText: 'Drag' }).click()
  const win = page.locator('.window.artifact', { has: page.locator('.sl-name', { hasText: 'drag' }) })
  const doc = win.locator('.doc-page')
  await doc.locator('h1').click()
  await normal()
  const p = doc.locator('p', { hasText: 'third' })
  const box = (await p.boundingBox())!
  const w = await p.evaluate((e) => {
    const r = document.createRange()
    r.setStart(e.firstChild!, 0)
    r.setEnd(e.firstChild!, 'first '.length)
    return r.getBoundingClientRect().width
  })
  await page.mouse.move(box.x + 1, box.y + box.height / 2)
  await page.mouse.down()
  await page.mouse.move(box.x + w / 2, box.y + box.height / 2, { steps: 4 })
  await page.mouse.move(box.x + w - 1, box.y + box.height / 2, { steps: 4 })
  await page.mouse.up()
  await page.keyboard.press('d')
  await expect(p).toHaveText('second third')
  await expect(doc.locator('.doc-fat-cursor')).toHaveCount(1)
  await page.keyboard.press('u')
  await expect(p).toHaveText('first second third')
  // A click leaves Visual mode, and the cursor goes where you clicked.
  await page.keyboard.press('v')
  await page.keyboard.press('e')
  await page.mouse.click(box.x + 1, box.y + box.height / 2)
  await page.keyboard.press('x')
  await expect(p).toHaveText('irst second third')
  await page.keyboard.press('u')
  await normal()
  await page.keyboard.press('Control+o')
})

test('j scrolls the page under a still cursor, kept a few lines from the edge, as Vim with scrolloff', async () => {
  const blocks = Array.from({ length: 80 }, (_, i) => (i % 9 === 0 ? `## Part ${i}` : `line ${i} ${'word '.repeat(i % 5)}`))
  hub.artifact('demo', 'long', 'Long', '# Long\n\n' + blocks.join('\n\n') + '\n')
  hub.event('demo', 'api', { kind: 'artifact', detail: 'long', text: 'Long', version: 1 })
  await page.locator('.b-artifact', { hasText: 'Long' }).click()
  const win = page.locator('.window.artifact', { has: page.locator('.sl-name', { hasText: 'long' }) })
  const doc = win.locator('.doc-page')
  await doc.locator('h1', { hasText: 'Long' }).click()
  await normal()
  const edge = await win.locator('.doc-editor').evaluate((e) => e.getBoundingClientRect().bottom)
  const ys: number[] = []
  for (let i = 0; i < 50; i++) {
    await page.keyboard.press('j')
    ys.push(await doc.locator('.doc-fat-cursor').evaluate((e) => e.getBoundingClientRect().bottom))
  }
  const still = ys.slice(30)
  expect(Math.max(...still) - Math.min(...still)).toBeLessThan(4)
  expect(edge - still[0]).toBeGreaterThan(60)
  const top = await win.locator('.doc-editor').evaluate((e) => e.getBoundingClientRect().top)
  const up: number[] = []
  for (let i = 0; i < 45; i++) {
    await page.keyboard.press('k')
    up.push(await doc.locator('.doc-fat-cursor').evaluate((e) => e.getBoundingClientRect().top))
  }
  const stillUp = up.slice(30)
  expect(Math.max(...stillUp) - Math.min(...stillUp)).toBeLessThan(4)
  expect(stillUp[0] - top).toBeGreaterThan(60)
  await page.keyboard.press('Control+o')
})

test('a click on an artifact named in the page opens it, and Back returns; gf and <CR> open it too', async () => {
  hub.artifact('demo', 'target-doc', 'Target', '# Target\n\nThe other page.\n')
  hub.artifact('demo', 'linker', 'Linker', '# Linker\n\nRead `target-doc` first.\n\nThen target-doc again, in prose.\n')
  hub.event('demo', 'api', { kind: 'artifact', detail: 'target-doc', text: 'Target', version: 1 })
  hub.event('demo', 'api', { kind: 'artifact', detail: 'linker', text: 'Linker', version: 1 })
  await page.locator('.b-artifact', { hasText: 'Linker' }).click()
  const name = page.locator('.statusline.focused .sl-name')
  await expect(name).toHaveText('linker')
  const doc = page.locator('.window.artifact .doc-page')
  // A plain click, in Normal mode, as on a link in a page you read.
  await doc.locator('code[data-artifact="target-doc"], [data-artifact="target-doc"]').first().click()
  await expect(name).toHaveText('target-doc')
  await page.keyboard.press('Control+o')
  await expect(name).toHaveText('linker')
  // A name in prose is a link too, as in the TUI.
  await doc.locator('p', { hasText: 'in prose' }).locator('[data-artifact="target-doc"]').click()
  await expect(name).toHaveText('target-doc')
  await page.keyboard.press('Control+o')
  await expect(name).toHaveText('linker')
  // gf and <CR> on the name open it, as in the TUI.
  await page.keyboard.press('/')
  await page.keyboard.type('target-doc')
  await page.keyboard.press('Enter')
  await page.keyboard.press('g')
  await page.keyboard.press('f')
  await expect(name).toHaveText('target-doc')
  await page.keyboard.press('Control+o')
  await expect(name).toHaveText('linker')
  await page.keyboard.press('/')
  await page.keyboard.type('target-doc')
  await page.keyboard.press('Enter')
  await page.keyboard.press('Enter')
  await expect(name).toHaveText('target-doc')
  await page.keyboard.press('Control+o')
  await expect(name).toHaveText('linker')
  await page.keyboard.press('Control+o')
})
