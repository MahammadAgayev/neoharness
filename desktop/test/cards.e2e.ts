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
  // NEOHARNESS_APP runs the packaged app instead: npm run dist first.
  const target = process.env.NEOHARNESS_APP
    ? { executablePath: process.env.NEOHARNESS_APP, args: [] }
    : { args: [path.join(__dirname, '..', 'out', 'main', 'index.js')] }
  app = await electron.launch({
    ...target,
    // The fake hub is up; false makes sure no real hub ever starts here.
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

test('a permission request toasts, shows a card, and <leader>a allows it', async () => {
  const perm = { id: 'p1', tool: 'Bash', detail: 'go test ./...', scope: 'go test' }
  hub.event('demo', 'api', { kind: 'permission', tool: 'Bash', detail: 'go test ./...', id: 'p1' })
  hub.setView(view('demo', 'api', { state: 'waiting', permission: perm }))
  const toast = page.locator('.toast', { hasText: 'needs permission · Bash' })
  await expect(toast).toBeVisible()
  await shot('20-permission-toast')
  await toast.click()
  await expect(page.locator('.card.permission button', { hasText: 'Allow' })).toBeVisible()
  await expect(page.locator('.card.permission button', { hasText: 'Always: go test' })).toBeVisible()
  await page.locator('.card.permission').hover()
  await shot('21-permission-card')
  // The thread is on screen now: its toast goes.
  await expect(toast).toHaveCount(0, { timeout: 3000 })
  await shot('21-permission-card')
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('a')
  await expect.poll(() => hub.calls.find((c) => c.path.endsWith('/answer'))?.body).toMatchObject({ id: 'p1', decision: 'allow' })
  await expect(page.locator('.row.k-permission', { hasText: '· allow' })).toBeVisible()
})

test('a permission with no scope offers no always', async () => {
  const perm = { id: 'p2', tool: 'Bash', detail: 'git push --force origin main' }
  hub.event('demo', 'api', { kind: 'permission', tool: 'Bash', detail: perm.detail, id: 'p2' })
  hub.setView(view('demo', 'api', { state: 'waiting', permission: perm }))
  await expect(page.locator('.card.permission button', { hasText: 'Deny' })).toBeVisible()
  await expect(page.locator('.card.permission button', { hasText: 'Always' })).toHaveCount(0)
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('x')
  await expect.poll(() => hub.calls.filter((c) => c.path.endsWith('/answer')).at(-1)?.body).toMatchObject({ id: 'p2', decision: 'deny' })
})

test('a question waits in a card like a permission; you answer by writing, or skip it', async () => {
  hub.event('demo', 'api', { kind: 'ask', id: 'q1', text: 'Which **port** should it listen on?\n(A) 8080\n(B) 9090' })
  hub.setView(view('demo', 'api', { state: 'waiting', waiting: 'Which port should it listen on?', questionId: 'q1' }))
  const card = page.locator('.card.question', { hasText: 'Which port' })
  await expect(card).toBeVisible()
  // Each choice on its own line, as the thread wrote it and as the TUI shows it.
  await expect(card.locator('br')).toHaveCount(2)
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('x')
  await expect.poll(() => hub.calls.filter((c) => c.path.endsWith('/answer')).at(-1)?.body).toMatchObject({ id: 'q1', decision: 'deny' })
  // Answered: the card is a plain block again.
  hub.setView(view('demo', 'api', { state: 'waiting', waiting: 'Which port should it listen on?' }))
  await expect(page.locator('.card.question')).toHaveCount(0)
  await expect(page.locator('.b-ask', { hasText: 'Which port' })).toBeVisible()
  await page.keyboard.press('i')
  await page.keyboard.type('8080')
  await page.keyboard.press('Enter')
  await expect.poll(() => hub.calls.find((c) => c.path.endsWith('/messages'))?.body).toMatchObject({ text: '8080' })
  await expect(page.locator('.b-user', { hasText: '8080' })).toBeVisible()
  await shot('22-question')
})

test('a question shows once, in the chat, and the messages it holds wait dim below it', async () => {
  const at = new Date().toISOString()
  // The question block of the test above is the one place it shows.
  hub.setView(
    view('demo', 'api', {
      state: 'waiting',
      waiting: 'Which port should it listen on?',
      queued: 1,
      pending: [{ at, kind: 'message', from: 'coordinator', text: 'stop, the person wants one thread' }],
    }),
  )
  await expect(page.locator('.pane.focused').getByText('Which port should it listen on?')).toHaveCount(1)
  await expect(page.locator('.b-queued', { hasText: 'stop, the person wants one thread' })).toBeVisible()
  hub.setView(view('demo', 'api', { state: 'idle' }))
})

test('/ completes commands in the prompt', async () => {
  await page.keyboard.type('/cl')
  await expect(page.locator('.cm-tooltip-autocomplete', { hasText: 'clear' })).toBeVisible()
  await shot('23-completion')
  // ⌃U clears the line in Insert, as in the TUI.
  await page.keyboard.press('Control+u')
  await expect(page.locator('.prompt .cm-focused .cm-placeholder')).toBeVisible()
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

test('a running tool call with a long command stays on one line, the count whole at its end', async () => {
  hub.setView(view('demo', 'api', { state: 'running' }))
  for (let i = 0; i < 8; i++) {
    hub.event('demo', 'api', { kind: 'tool', tool: 'Bash', detail: `cd /tmp/a/long/path/to/the/project; go test ./tui/ > /tmp/out-${i}.txt 2>&1`, id: `long${i}` })
  }
  await page.setViewportSize({ width: 800, height: 860 })
  const head = page.locator('.b-tools-head', { hasText: '8 tool calls' })
  await expect(head).toBeVisible()
  const line = await head.locator('.t-subtle').boundingBox()
  expect((await head.boundingBox())!.height).toBeLessThan(line!.height * 1.5)
  const denied = page.locator('.b-line', { hasText: '· deny' })
  expect((await denied.boundingBox())!.height).toBeLessThan(line!.height * 1.5)
  await shot('05-live-tool-narrow')
  await page.setViewportSize({ width: 1400, height: 860 })
  hub.event('demo', 'api', { kind: 'turn' })
  hub.setView(view('demo', 'api', { state: 'idle' }))
})

test('an artifact opens, edits, saves, and a newer version is merged or forced', async () => {
  hub.artifact('demo', 'plan', 'Health check plan', '# Plan\n\n- add /healthz\n- test it\n')
  hub.event('demo', 'api', { kind: 'artifact', detail: 'plan', text: 'Health check plan', version: 1 })
  await page.locator('.b-artifact', { hasText: 'Health check plan' }).click()
  const doc = page.locator('.window.artifact .doc-page')
  await expect(doc.locator('h1', { hasText: 'Plan' })).toBeVisible()
  await expect(doc.locator('li')).toHaveCount(2)
  await expect(doc).toBeFocused()
  await shot('24-artifact')
  // Normal mode moves; o opens a list item under the last one, and what
  // you type stays rendered: no markdown shows, in Insert either.
  await page.keyboard.press('G')
  await page.keyboard.press('o')
  await page.keyboard.type('ship it **now**')
  await expect(doc.locator('li')).toHaveCount(3)
  await expect(doc.locator('li strong', { hasText: 'now' })).toBeVisible()
  await expect(doc).not.toContainText('**')
  await page.keyboard.press('Escape')
  await expect(page.locator('.statusline .sl-name', { hasText: 'plan [+]' })).toBeVisible()
  // Normal mode types nothing.
  await page.keyboard.press('q')
  await expect(doc).not.toContainText('q')
  await page.keyboard.press('Meta+s')
  await expect.poll(() => hub.calls.find((c) => c.method === 'PUT')?.body).toMatchObject({ base: 1, text: '# Plan\n\n- add /healthz\n- test it\n- ship it **now**\n' })
  await expect(page.locator('.statusline .sl-name', { hasText: /^plan$/ })).toBeVisible()

  // The agent writes v3 while you edit: it changed another block, so it
  // merges into the page at once, and :w saves both.
  await page.keyboard.press('g')
  await page.keyboard.press('g')
  await page.keyboard.press('i')
  await page.keyboard.type('x')
  await page.keyboard.press('Escape')
  await expect(doc.locator('h1')).toHaveText('xPlan')
  const v2 = '# Plan\n\n- add /healthz\n- test it\n- ship it **now**\n'
  hub.artifact('demo', 'plan', 'Health check plan', v2 + '\n## Deploy\n', 3)
  await expect(page.locator('.cmdline', { hasText: 'wrote v3 · merged into your edit' })).toBeVisible()
  await expect(doc.locator('h2', { hasText: 'Deploy' })).toBeVisible()
  await expect(doc.locator('h1')).toHaveText('xPlan')
  await shot('25-artifact-merged')
  await page.keyboard.type(':')
  await page.keyboard.type('w')
  await page.keyboard.press('Enter')
  await expect
    .poll(() => hub.calls.filter((c) => c.method === 'PUT').pop()?.body)
    .toMatchObject({ base: 3, force: false, text: '# xPlan' + v2.slice('# Plan'.length) + '\n## Deploy\n' })
  await page.keyboard.press('Control+o')
  await expect(page.locator('.statusline .sl-name', { hasText: /^api$/ })).toBeVisible()
})

test('without Vim, keys work as in Claude Code: ⌃S stashes, Esc takes your message back', async () => {
  await page.keyboard.type(':')
  await page.keyboard.type('set novim')
  await page.keyboard.press('Enter')
  hub.setView(view('demo', 'api', { state: 'running' }))
  await page.keyboard.press('i')
  const prompt = page.locator('.window.chat', { has: page.locator('.sl-name', { hasText: /^api$/ }) }).locator('.prompt .cm-content')
  await expect(prompt).toBeFocused()
  await page.keyboard.type('a draft for later')
  await page.keyboard.press('Control+s')
  await expect(prompt.locator('.cm-placeholder')).toBeVisible()
  // Esc stops the running turn and brings back your last message; the
  // transcript no longer shows it.
  await page.keyboard.press('Escape')
  await expect.poll(() => hub.calls.some((c) => c.path.endsWith('/takeback'))).toBe(true)
  await expect(prompt).toHaveText('8080')
  await expect(page.locator('.b-user', { hasText: '8080' })).toHaveCount(0)
  await page.keyboard.type(' please')
  await page.keyboard.press('Enter')
  await expect.poll(() => hub.calls.filter((c) => c.path.endsWith('/messages')).pop()?.body).toMatchObject({ text: '8080 please' })
  // The stash comes back after the send.
  await expect(prompt).toHaveText('a draft for later')
  await shot('27-novim')
  await page.keyboard.press('Control+u')
  // Without Vim the prompt keeps ":", so leave it first.
  await prompt.blur()
  await page.keyboard.type(':')
  await page.keyboard.type('set vim')
  await page.keyboard.press('Enter')
})

test('v selects blocks and y yanks them to the clipboard', async () => {
  // The clipboard is the person's: put back what was on it.
  const saved = await app.evaluate(({ clipboard }) => clipboard.readText())
  try {
    await yankTwo()
  } finally {
    await app.evaluate(({ clipboard }, t) => clipboard.writeText(t), saved)
  }
})

async function yankTwo() {
  await page.locator('.b-ask').click()
  await normal()
  await page.locator('.b-ask').click()
  await page.keyboard.press('v')
  await page.keyboard.press('j')
  await expect(page.locator('.row.selected')).toHaveCount(2)
  await page.keyboard.press('y')
  await expect(page.locator('.row.selected')).toHaveCount(0)
  const clip = await app.evaluate(({ clipboard }) => clipboard.readText())
  expect(clip).toContain('Which **port** should it listen on?')
  // Its answer, 8080, was taken back with Esc above; the tool calls follow.
  expect(clip).toContain('go test ./tui/')
}

test('a selection holds the chat still as new blocks arrive', async () => {
  await page.locator('.b-ask').click()
  await normal()
  await page.keyboard.press('i')
  await page.keyboard.press('Escape')
  // <Esc> in the prompt's Normal puts the cursor on the last block.
  await page.keyboard.press('Escape')
  await page.keyboard.press('v')
  const sel = page.locator('.row.selected')
  await expect(sel).toHaveCount(1)
  for (let i = 0; i < 40; i++) hub.event('demo', 'api', { kind: 'text', text: `new block ${i}` })
  await expect(page.locator('.row', { hasText: 'new block 39' })).toHaveCount(1)
  await page.waitForTimeout(250)
  await expect(sel).toBeInViewport()
  await page.keyboard.press('Escape')
})

test('⌘, opens the settings: a theme, and a key you record runs an ex command', async () => {
  const base = () => page.evaluate(() => document.documentElement.style.getPropertyValue('--base'))
  await page.keyboard.press('Meta+Comma')
  await expect(page.locator('.settings')).toBeVisible()
  await expect(page.locator('.setting', { hasText: 'Vim keys' }).locator('.toggle')).toHaveAttribute('aria-checked', 'true')
  await page.locator('.swatch', { hasText: 'Dawn' }).click()
  await expect.poll(base).toBe('#faf4ed')
  await page.locator('.add-map').click()
  await page.locator('.map-row .field.keys').click()
  await page.keyboard.press('Space')
  await page.keyboard.press('z')
  await expect(page.locator('.map-row .field.keys kbd')).toHaveText(['<leader>', 'z'])
  await page.locator('.map-row select').selectOption(':')
  await page.locator('.map-row .ex').fill(':colorscheme rose-pine-moon')
  await shot('28-settings')
  // Auto compact is the hub's, so the field sets the open workspace's :setlocal.
  await page.getByLabel('auto compact').fill('85')
  await page.getByLabel('auto compact').press('Enter')
  await expect.poll(() => hub.calls.find((c) => c.path.endsWith('/local'))?.body).toMatchObject({ option: 'auto_compact', agent: '85' })
  await page.keyboard.press('Escape')
  await expect(page.locator('.settings')).toHaveCount(0)
  await page.keyboard.press('Space')
  await page.keyboard.press('z')
  await expect.poll(base).toBe('#232136')
})

test('<Tab> completes a folder in :new, one level at a time', async () => {
  fs.mkdirSync(path.join(tmp, 'projects', 'api'), { recursive: true })
  await page.keyboard.type(':')
  await page.keyboard.type(`new x ${tmp}/pro`)
  const line = page.locator('.cmdline input')
  await page.keyboard.press('Tab')
  await expect(line).toHaveValue(`new x ${tmp}/projects/`)
  await page.keyboard.press('Tab')
  await expect(line).toHaveValue(`new x ${tmp}/projects/api/`)
  await page.keyboard.press('Escape')
})

test("ma marks a block; G goes away and 'a comes back", async () => {
  await page.locator('.b-ask').click()
  await normal()
  await page.locator('.b-ask').click()
  await page.keyboard.press('m')
  await page.keyboard.press('a')
  await page.keyboard.press('g')
  await page.keyboard.press('g')
  await expect(page.locator('.row.cursor')).not.toContainText('Which port')
  await page.keyboard.press("'")
  await page.keyboard.press('a')
  await expect(page.locator('.row.cursor')).toContainText('Which port')
  await page.keyboard.press("'")
  await page.keyboard.press("'")
  await expect(page.locator('.row.cursor')).not.toContainText('Which port')
})

test('a right click on a thread offers its actions, and a hint shows on hover', async () => {
  // A native menu is out of Playwright's reach: a stub picks Resolve.
  await app.evaluate(({ ipcMain }) => {
    ipcMain.removeHandler('menu')
    ipcMain.handle('menu', (_e, items: { label: string }[]) => {
      ;(globalThis as { menuItems?: string[] }).menuItems = items.map((i) => i.label)
      return 'resolve'
    })
  })
  await page.keyboard.type(':')
  await page.keyboard.type('threads')
  await page.keyboard.press('Enter')
  await page.locator('.window.threads .list-row', { hasText: 'api' }).click({ button: 'right' })
  await expect.poll(() => hub.calls.some((c) => c.path.endsWith('/api/resolve'))).toBe(true)
  const labels = await app.evaluate(() => (globalThis as { menuItems?: string[] }).menuItems)
  expect(labels).toEqual(expect.arrayContaining(['Open', 'Open in a split on the right', 'Resolve', 'Copy the name', 'Delete…']))

  await page.locator('.sb-item', { hasText: 'demo' }).hover()
  await expect(page.locator('.tooltip')).toBeVisible()
  await shot('29-tooltip')
  await page.mouse.move(700, 400)
})

test('<leader> shows what can follow at once, as which-key, and <leader>s opens the settings', async () => {
  await page.keyboard.press('Space')
  const panel = page.locator('.keyhints')
  await expect(panel).toBeVisible()
  await expect(panel.locator('.keyhint', { hasText: /^cCoordinator$/ })).toBeVisible()
  await expect(panel.locator('.keyhint', { hasText: /^p\+pick · \d+$/ })).toBeVisible()
  await shot('36-keyhints')
  await page.keyboard.press('c')
  await expect(panel).toHaveCount(0)
  await page.keyboard.press('Space')
  await page.keyboard.press('s')
  await expect(page.locator('.settings')).toBeVisible()
  await page.keyboard.press('Escape')
  await page.keyboard.press('Space')
  await page.keyboard.press('h')
  const help = page.locator('.help-dialog')
  await expect(help).toBeVisible()
  await page.keyboard.press('j')
  await expect.poll(() => help.locator('.settings-body').evaluate((e) => e.scrollTop)).toBeGreaterThan(0)
  await page.keyboard.press('Escape')
  await expect(help).toHaveCount(0)
})

test('<C-o> goes back and <C-i> forward, as ⌘[ and ⌘]', async () => {
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('c')
  const name = page.locator('.statusline.focused .sl-name')
  await expect(name).toHaveText('coordinator')
  await page.keyboard.type(':')
  await page.keyboard.type('b api')
  await page.keyboard.press('Enter')
  await expect(name).toHaveText('api')
  await page.keyboard.press('Control+o')
  await expect(name).toHaveText('coordinator')
  await page.keyboard.press('Control+i')
  await expect(name).toHaveText('api')
  await page.keyboard.press('Meta+BracketLeft')
  await expect(name).toHaveText('coordinator')
  await page.keyboard.press('Meta+BracketRight')
  await expect(name).toHaveText('api')
  await page.keyboard.press('Control+o')
})

test('Ctrl V in search opens the thread on the right, with the caret in its prompt', async () => {
  const chats = page.locator('.window.chat')
  const before = await chats.count()
  await page.keyboard.press('Meta+k')
  await page.keyboard.type('demo coordinator')
  await page.keyboard.press('Control+v')
  await expect(chats).toHaveCount(before + 1)
  await expect(page.locator('.window.chat .prompt .cm-focused')).toHaveCount(1)
  await page.keyboard.press('Escape')
  await page.keyboard.press('Escape')
  await page.keyboard.press('Control+w')
  await page.keyboard.press('c')
})

test('New workspace opens a dialog: a name, a folder, Create', async () => {
  await page.locator('.sb-new').click()
  const dialog = page.locator('.dialog')
  await expect(dialog).toBeVisible()
  await page.keyboard.type('Bad Name!')
  await expect(dialog.locator('.dialog-note.t-love')).toBeVisible()
  await dialog.locator('input').first().fill('billing')
  await expect(dialog.locator('button[type=submit]')).toBeDisabled()
  await dialog.locator('input').nth(1).fill(tmp)
  await shot('37-new-workspace')
  await page.keyboard.press('Enter')
  await expect.poll(() => hub.calls.find((c) => c.method === 'POST' && c.path === '/workspaces')?.body).toEqual({ name: 'billing', pin: tmp })
  await expect(dialog).toHaveCount(0)
  await expect(page.locator('.sb-item.active', { hasText: 'billing' })).toBeVisible()
})

test('New doc starts a document beside the chat, ready to write', async () => {
  // The workspace made before leaves you writing to its coordinator.
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('d')
  const dialog = page.locator('.dialog', { hasText: 'New doc' })
  await expect(dialog).toBeVisible()
  await page.keyboard.type('launch plan')
  await expect(dialog.locator('input')).toHaveValue('launch-plan')
  await page.keyboard.press('Enter')
  await expect
    .poll(() => hub.calls.find((c) => c.method === 'PUT' && c.path.endsWith('/artifacts/launch-plan'))?.body)
    .toMatchObject({ base: 0, text: '# Launch plan\n\n' })
  const doc = page.locator('.window.artifact', { has: page.locator('.sl-name', { hasText: 'launch-plan' }) })
  await expect(doc.locator('.doc-page')).toBeFocused()
  await page.keyboard.type('- [ ] write the API')
  await expect(doc.locator('.sl-name', { hasText: 'launch-plan [+]' })).toBeVisible()
  await shot('38-new-doc')
  await page.keyboard.press('Escape')
  await page.keyboard.type(':')
  await page.keyboard.type('w')
  await page.keyboard.press('Enter')
  await expect
    .poll(() => hub.calls.filter((c) => c.method === 'PUT' && c.path.endsWith('/artifacts/launch-plan')).pop()?.body)
    .toMatchObject({ base: 1, text: '# Launch plan\n\n- [ ] write the API\n' })
  await expect(doc.locator('li.task', { hasText: 'write the API' })).toBeVisible()
  // :w saves from Visual mode too, where Vim's own command line takes the :.
  await page.keyboard.press('A')
  await page.keyboard.type(' now')
  await page.keyboard.press('Escape')
  await page.keyboard.press('v')
  await page.keyboard.type(':')
  await page.keyboard.type('w')
  await page.keyboard.press('Enter')
  await expect
    .poll(() => hub.calls.filter((c) => c.method === 'PUT' && c.path.endsWith('/artifacts/launch-plan')).pop()?.body)
    .toMatchObject({ text: '# Launch plan\n\n- [ ] write the API now\n' })
  await page.keyboard.press('Escape')
})

test('Ctrl V on a thread of another workspace shows it beside this one, and you write to it there', async () => {
  // billing is open now: demo's threads are of another workspace.
  await normal()
  await page.keyboard.press('Meta+k')
  await page.keyboard.type('demo coordinator')
  const foot = page.locator('.palette-foot')
  await expect(foot).toContainText('go to demo')
  await expect(foot).toContainText('split right')
  await page.keyboard.press('Control+v')
  const peek = page.locator('.window.chat', { has: page.locator('.sl-ws.peek', { hasText: 'demo' }) })
  await expect(peek).toHaveCount(1)
  await expect(page.locator('.sb-item.active', { hasText: 'billing' })).toBeVisible()
  await expect(peek.locator('.prompt .cm-focused')).toBeVisible()
  await expect(peek.locator('.cm-placeholder')).toHaveText(/^write to demo · coordinator/)
  await page.keyboard.type('hello from billing')
  await page.keyboard.press('Enter')
  await expect
    .poll(() => hub.calls.filter((c) => c.path.endsWith('/messages')).pop())
    .toMatchObject({ path: '/workspaces/demo/threads/coordinator/messages', body: { text: 'hello from billing' } })
  await expect(peek.locator('.b-user', { hasText: 'hello from billing' })).toBeVisible()
  await shot('39-peek')
  await normal()
  await page.keyboard.press('Control+w')
  await page.keyboard.press('c')
  await expect(peek).toHaveCount(0)
})

test('without Vim a window closes with ⌘W from its prompt, or with the × on its status line', async () => {
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('c')
  await page.keyboard.type(':')
  await page.keyboard.type('set novim')
  await page.keyboard.press('Enter')
  const panes = page.locator('.pane')
  const before = await panes.count()
  await page.keyboard.press('Meta+Backslash')
  await expect(panes).toHaveCount(before + 1)
  await page.locator('.pane.focused .prompt').click()
  await page.keyboard.press('Meta+w')
  await expect(panes).toHaveCount(before)
  await page.keyboard.press('Meta+Backslash')
  await expect(panes).toHaveCount(before + 1)
  const pane = page.locator('.pane.focused')
  await pane.hover()
  await expect(pane.locator('.sl-close')).toBeVisible()
  await shot('40-close-window')
  await pane.locator('.sl-close').click()
  await expect(panes).toHaveCount(before)
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
  await page.keyboard.type(':')
  await page.keyboard.type('set vim')
  await page.keyboard.press('Enter')
})

test('in a narrow column the prompt keeps its width and one line, with no sideways scroll, the model under it', async () => {
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('c')
  hub.setView(
    view('billing', 'coordinator', {
      location: { dir: '/tmp/fake', host: 'build-box.example.com' },
      model: 'claude-opus-5-5[1m]',
      context: 9000,
      contextWindow: 1000000,
      branch: 'main',
    }),
  )
  await page.keyboard.press('Meta+Backslash')
  await page.keyboard.press('Meta+Backslash')
  const prompts = page.locator('.pane .prompt')
  await expect(page.locator('.pane .prompt-info').first()).toContainText('opus-5-5')
  for (const p of await prompts.all()) {
    const editor = await p.locator('.prompt-editor').boundingBox()
    const box = await p.boundingBox()
    expect(editor!.width).toBeGreaterThan(box!.width * 0.6)
    expect(box!.height).toBeLessThan(60)
    // The cut placeholder must not make the prompt scroll sideways.
    const scroll = await p.locator('.cm-scroller').evaluate((el) => el.scrollWidth - el.clientWidth)
    expect(scroll).toBeLessThanOrEqual(0)
  }
  await shot('41-narrow-prompt')
  await page.keyboard.press('Meta+w')
  await page.keyboard.press('Meta+w')
})

test('/model picks from a list, as in Claude Code, and so does a click on the model under the prompt', async () => {
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('c')
  await page.keyboard.press('i')
  await page.keyboard.type('/model')
  await page.keyboard.press('Escape')
  await page.keyboard.press('i')
  await page.keyboard.press('Enter')
  const choice = page.locator('.choice')
  await expect(choice).toBeVisible()
  await expect(choice.locator('.palette-row.active')).toContainText('Opus')
  await expect(choice.locator('.palette-row.active')).toContainText('model-a-1 · The most capable')
  await expect(choice.locator('.palette-row', { hasText: 'Opus' }).locator('.icon')).toHaveCount(1)
  await expect(choice.locator('.palette-row')).toHaveCount(3)
  await shot('42-model-choice')
  await page.keyboard.press('j')
  await page.keyboard.press('Enter')
  await expect(choice).toHaveCount(0)
  await expect.poll(() => hub.calls.filter((c) => c.path.endsWith('/messages')).pop()?.body).toMatchObject({ text: '/model sonnet' })

  await page.locator('.pane.focused .prompt-info .pick').click()
  await expect(choice).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(choice).toHaveCount(0)

  // The prompt is back in Insert. The effort levels are those of the model
  // the thread runs.
  await page.keyboard.type('/effort')
  await page.keyboard.press('Enter')
  await expect(choice.locator('.palette-row')).toHaveText(['low', 'high', 'max', /default/])
  await expect(choice.locator('.palette-row.active')).toContainText('default')
  await page.keyboard.press('Escape')
  await expect(choice).toHaveCount(0)
})

test('with the list open, Enter and Tab take the command, and a half-typed one is never sent', async () => {
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('c')
  await page.keyboard.press('i')
  const sent = hub.calls.filter((c) => c.path.endsWith('/messages')).length
  const prompt = page.locator('.pane.focused .prompt .cm-content')
  await page.keyboard.type('/mod')
  await expect(page.locator('.cm-tooltip-autocomplete', { hasText: 'model' })).toBeVisible()
  await page.keyboard.press('Enter')
  await expect(prompt).toHaveText(/^\/model/)
  expect(hub.calls.filter((c) => c.path.endsWith('/messages')).length).toBe(sent)
  await page.keyboard.press('Control+u')
  await page.keyboard.type('/comp')
  await expect(page.locator('.cm-tooltip-autocomplete', { hasText: 'compact' })).toBeVisible()
  await page.keyboard.press('Tab')
  await expect(prompt).toHaveText(/^\/compact/)
  await page.keyboard.press('Control+u')
  await normal()
})

test('the sidebar resizes by its edge, keeps the width, and a double-click resets it', async () => {
  const bar = page.locator('.sidebar')
  const before = (await bar.boundingBox())!.width
  const edge = (await page.locator('.sb-resizer').boundingBox())!
  await page.mouse.move(edge.x + edge.width / 2, edge.y + 200)
  await page.mouse.down()
  await page.mouse.move(edge.x + edge.width / 2 + 80, edge.y + 200, { steps: 4 })
  await page.mouse.up()
  await expect.poll(async () => Math.round((await bar.boundingBox())!.width)).toBe(Math.round(before + 80))
  const saved = await page.evaluate(() => JSON.parse(localStorage.getItem('settings') || '{}').sidebarWidth)
  expect(saved).toBe(Math.round(before + 80))
  await page.locator('.sb-resizer').dblclick()
  await expect.poll(async () => Math.round((await bar.boundingBox())!.width)).toBe(200)
  await page.mouse.move(700, 400)
})

test('⌘B folds the sidebar to a rail; the mouse on the rail shows it whole over the windows', async () => {
  await normal()
  const bar = page.locator('.sidebar').first()
  const layout = page.locator('.layout')
  const x0 = (await layout.boundingBox())!.x
  await page.keyboard.press('Meta+b')
  await expect(bar).toHaveClass(/rail/)
  await expect.poll(async () => Math.round((await bar.boundingBox())!.width)).toBe(48)
  await expect(bar.locator('.sb-label', { hasText: 'demo' })).toBeHidden()
  const x1 = (await layout.boundingBox())!.x
  expect(x1).toBeLessThan(x0)
  await bar.hover({ position: { x: 20, y: 200 } })
  // The whole sidebar slides in over the rail, which keeps its place.
  const peek = page.locator('.sidebar.peek:not(.leaving)')
  await expect(peek).toBeVisible()
  await expect.poll(async () => Math.round((await peek.boundingBox())!.width)).toBe(200)
  await expect(peek.locator('.sb-label', { hasText: 'demo' })).toBeVisible()
  // Over the windows: they do not move.
  expect((await layout.boundingBox())!.x).toBe(x1)
  await shot('44-sidebar-peek')
  await page.mouse.move(700, 400)
  await expect(page.locator('.sidebar.peek')).toHaveCount(0)
  await expect(bar).toHaveClass(/rail/)
  await page.locator('.titlebar .nav-btn').first().click()
  await expect(bar).not.toHaveClass(/rail|peek/)
  await page.mouse.move(700, 400)
})

test('<C-w>h and <C-w>l move between a doc and the chat beside it, from Normal or Insert', async () => {
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('c')
  await page.keyboard.press('Meta+Backslash')
  await page.keyboard.type(':')
  await page.keyboard.type('b launch-plan')
  await page.keyboard.press('Enter')
  const name = page.locator('.statusline.focused .sl-name')
  await expect(name).toHaveText(/^launch-plan/)
  await page.locator('.pane.focused .doc-page').click()
  await page.keyboard.press('Control+w')
  await page.keyboard.press('h')
  await expect(name).toHaveText('coordinator')
  await page.keyboard.press('Control+w')
  await page.keyboard.press('l')
  await expect(name).toHaveText(/^launch-plan/)
  await page.keyboard.press('i')
  await page.keyboard.press('Control+w')
  await page.keyboard.press('h')
  await expect(name).toHaveText('coordinator')
  // Alone, <C-w> in Insert still deletes a word, as in Vim.
  await page.keyboard.press('i')
  const prompt = page.locator('.pane.focused .prompt .cm-content')
  await page.keyboard.type('keep this word')
  await page.keyboard.press('Control+w')
  await expect(prompt).toHaveText('keep this ')
  await page.keyboard.press('Control+u')
  await normal()
  await page.keyboard.press('Control+w')
  await page.keyboard.press('l')
  await page.keyboard.press('Control+w')
  await page.keyboard.press('c')
})

test('a doc opened from the coordinator goes back with <C-o> and the back button, and forward with <C-i>', async () => {
  await normal()
  await page.keyboard.press('Meta+1')
  await page.keyboard.press('Space')
  await page.keyboard.press('c')
  hub.artifact('demo', 'notes', 'Release notes', '# Notes\n\n- one\n')
  hub.event('demo', 'coordinator', { kind: 'artifact', detail: 'notes', text: 'Release notes', version: 1 })
  await page.locator('.pane.focused .b-artifact', { hasText: 'Release notes' }).click()
  const name = page.locator('.statusline.focused .sl-name')
  await expect(name).toHaveText(/^notes/)
  await page.keyboard.press('Control+o')
  await expect(name).toHaveText('coordinator')
  await page.keyboard.press('Control+i')
  await expect(name).toHaveText(/^notes/)
  // From Insert in the doc too.
  await page.keyboard.press('i')
  await page.keyboard.press('Control+o')
  await expect(name).toHaveText('coordinator')

  const back = page.locator('.titlebar .nav-back')
  const fwd = page.locator('.titlebar .nav-fwd')
  await expect(fwd).toBeEnabled()
  await fwd.hover()
  await expect(page.locator('.tooltip')).toContainText('Forward')
  await expect(page.locator('.tooltip')).toContainText('⌘]')
  await fwd.click()
  await expect(name).toHaveText(/^notes/)
  await expect(fwd).toBeDisabled()
  await back.click()
  await expect(name).toHaveText('coordinator')
  await shot('43-back-forward')
  await page.mouse.move(700, 400)
})

test('the send button turns to the accent with text, and a click sends', async () => {
  await normal()
  await page.keyboard.press('Meta+1')
  await page.keyboard.press('Space')
  await page.keyboard.press('c')
  const send = page.locator('.pane.focused .prompt-send')
  await expect(send).not.toHaveClass(/ready/)
  await page.keyboard.press('i')
  await page.keyboard.type('ship it')
  await expect(send).toHaveClass(/ready/)
  await shot('45-send-button')
  await send.click()
  await expect.poll(() => hub.calls.filter((c) => c.path.endsWith('/messages')).pop()?.body).toMatchObject({ text: 'ship it' })
  await expect(send).not.toHaveClass(/ready/)
})

test('<leader>$ shows what the workspace cost, Tab every workspace, and a turn opens its thread', async () => {
  await normal()
  await page.keyboard.press('Meta+1')
  await page.keyboard.press('Space')
  await page.keyboard.type('$')
  const d = page.locator('.cost-dialog')
  await expect(d).toBeVisible()
  await expect(d.locator('.cost-stat-value')).toHaveText(['$24.75', '$1.50', '$18.50'])
  await expect(d.locator('.cost-day')).toHaveCount(14)
  await expect(d.locator('.cost-day.today .cost-day-label')).toHaveText('today')
  await expect(d.locator('.cost-section', { hasText: 'by thread' }).locator('.cost-bar-row')).toHaveCount(3)
  await expect(d.locator('.cost-section', { hasText: 'by workspace' })).toHaveCount(0)
  await shot('46-cost')
  await page.keyboard.press('Tab')
  await expect(d.locator('.tab.active')).toHaveText('All workspaces')
  await expect(d.locator('.cost-section', { hasText: 'by workspace' }).locator('.cost-bar-row')).toHaveCount(2)
  await expect.poll(() => hub.calls.filter((c) => c.path.startsWith('/cost')).pop()?.path).toBe('/cost?workspace=')
  await d.locator('.cost-turn', { hasText: 'api' }).click()
  await expect(d).toHaveCount(0)
  await expect(page.locator('.statusline.focused .sl-name')).toHaveText('api')

  // From the rail's ⋯ menu, and Esc closes it. A native menu is out of
  // Playwright's reach: a stub picks Cost.
  await app.evaluate(({ ipcMain }) => {
    ipcMain.removeHandler('menu')
    ipcMain.handle('menu', (_e, items: { label: string }[]) => {
      ;(globalThis as { menuItems?: string[] }).menuItems = items.map((i) => i.label)
      return 'cost'
    })
  })
  await page.locator('.sb-btn', { hasText: '⋯' }).click()
  await expect(d).toBeVisible()
  const labels = await app.evaluate(() => (globalThis as { menuItems?: string[] }).menuItems)
  expect(labels).toEqual(expect.arrayContaining(['Artifacts', 'New doc', 'Cost', 'Settings', 'Help']))
  await page.keyboard.press('Escape')
  await expect(d).toHaveCount(0)

  // A workspace in the sidebar shows no cost; only the dialog does.
  await expect(page.locator('.sb-item', { hasText: 'demo' })).toBeVisible()
  await expect(page.locator('.sb-item', { hasText: 'demo' })).not.toContainText('$')
})

test("an idle thread's row is its name, and K shows its last reply", async () => {
  const row = page.locator('.board .list-row', { hasText: 'api' })
  hub.setView(view('demo', 'api', { state: 'idle', reply: 'Added /healthz and a test.' }))
  await expect(row).toHaveAttribute('data-tip', 'Added /healthz and a test.')
  await expect(page.locator('.board .list-need')).toHaveCount(0)
  await expect(page.locator('.group-head', { hasText: 'idle' })).toHaveText(/^idle · \d+$/)
  await page.locator('.window.threads .statusline').click()
  const i = await row.getAttribute('data-i')
  await page.keyboard.press('g')
  await page.keyboard.press('g')
  for (let n = 0; n < Number(i); n++) await page.keyboard.press('j')
  await page.keyboard.press('Shift+K')
  await expect(page.locator('.cmdline', { hasText: 'api: Added /healthz and a test.' })).toBeVisible()
})

test('a row that changes group waits while you are on the board, then settles; a failed thread has its own group', async () => {
  const names = () => page.locator('.board [data-name]').evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.name))
  const api = page.locator('.board .list-row', { hasText: 'api' })
  // The mouse rests on the sidebar, away from the list.
  await page.mouse.move(100, 500)
  // The cursor is on the board: api stays where it is.
  hub.setView(view('demo', 'api', { state: 'waiting', permission: { id: 'p9', tool: 'Bash', detail: 'go test ./...' } }))
  await expect(api.locator('.glyph')).toHaveClass(/t-gold/)
  await page.waitForTimeout(200)
  await expect(page.locator('.group-head', { hasText: 'waiting' })).toHaveCount(0)
  // Off the board, it moves to the top with what it needs, and a tint.
  await page.keyboard.press('Control+w')
  await page.keyboard.press('h')
  await page.mouse.move(100, 500)
  await expect(page.locator('.group-head').first()).toHaveText('waiting · 1')
  await expect.poll(names).toEqual(expect.arrayContaining(['api']))
  expect((await names())[0]).toBe('api')
  await expect(api).toHaveClass(/settle/)
  await expect(page.locator('.board .list-need')).toHaveText('permission · Bash go test ./...')
  await shot('47-board-settle')

  hub.setView(view('demo', 'api', { state: 'failed' }))
  await expect(page.locator('.group-head').first()).toHaveText('failed · 1')
  await expect(api.locator('.glyph')).toHaveClass(/t-love/)
  hub.setView(view('demo', 'api', { state: 'idle', reply: 'Added /healthz and a test.' }))
  await expect(page.locator('.group-head', { hasText: 'failed' })).toHaveCount(0)
})

test('the waits list: ]q and [q walk what needs you, :waits shows it, x dismisses a failure', async () => {
  const since = new Date(Date.now() - 4 * 60000).toISOString()
  hub.setWaits([
    { workspace: 'demo', thread: 'api', kind: 'permission', id: 'p1', text: 'Bash: go test ./...', since },
    { workspace: 'demo', thread: 'coordinator', kind: 'failed', id: 'failed', text: 'the model is overloaded', since },
  ])
  const pill = page.locator('.titlebar .pill')
  await expect(pill).toHaveText('◆ 2 waiting')
  const line = page.locator('.cmdline')
  const name = page.locator('.statusline.focused .sl-name')
  await normal()
  await page.keyboard.press(']')
  await page.keyboard.press('q')
  await expect(name).toHaveText('api')
  await expect(line).toContainText('(1 of 2) permission · Bash: go test ./...')
  await normal()
  await page.keyboard.press(']')
  await page.keyboard.press('q')
  await expect(name).toHaveText('coordinator')
  await expect(line).toContainText('(2 of 2) failed · the model is overloaded')
  await normal()
  await page.keyboard.press(']')
  await page.keyboard.press('q')
  await expect(line).toContainText('no more waits')
  await page.keyboard.press('[')
  await page.keyboard.press('q')
  await expect(line).toContainText('(1 of 2)')

  await normal()
  await page.keyboard.type(':waits')
  await page.keyboard.press('Enter')
  const rows = page.locator('.window.waits .list-row')
  await expect(rows).toHaveCount(2)
  await expect(rows.nth(1)).toContainText('failed · the model is overloaded')
  await shot('49-waits')
  await page.keyboard.press('j')
  await page.keyboard.press('x')
  await expect.poll(() => hub.calls.some((c) => c.method === 'DELETE' && c.path.endsWith('/coordinator/waits/failed'))).toBe(true)
  await expect(rows).toHaveCount(1)
  await expect(pill).toHaveText('◆ 1 waiting')
  await page.keyboard.press('x')
  await expect(line).toContainText('answer it in its thread')
  await page.keyboard.press('Enter')
  await expect(name).toHaveText('api')

  hub.setWaits([])
  await expect(pill).toHaveCount(0)
  await normal()
  await page.keyboard.press(']')
  await page.keyboard.press('q')
  await expect(line).toContainText('nothing waits for you')
  // Back to what the window showed before the list.
  await page.locator('.window.waits .statusline').click()
  await page.keyboard.press('Control+o')
  await expect(page.locator('.window.waits')).toHaveCount(0)
})

test('the recap card shows above the prompt after two minutes away; Esc, q or × closes it until a new recap', async () => {
  await page.keyboard.press('Escape')
  await page.keyboard.press('Space')
  await page.keyboard.press('c')
  const card = page.locator('.pane.focused .recap-card')
  let n = 0
  const recap = async (text: string) => {
    n++
    hub.setView(view('demo', 'coordinator', { state: 'idle', recap: text, recapAt: new Date(Date.now() + n * 1000).toISOString() }))
    await expect(page.locator('.pane.focused .prompt')).toBeVisible()
    // The page applies hub updates once a frame, or within 100 ms.
    await page.waitForTimeout(150)
  }
  // Away: the window loses focus, and comes back three minutes later.
  const away = () =>
    page.evaluate(() => {
      window.dispatchEvent(new Event('blur'))
      const real = Date.now
      Date.now = () => real() + 3 * 60_000
      window.dispatchEvent(new Event('focus'))
      Date.now = real
    })
  await recap('Cache added; review next.')
  await expect(card).toHaveCount(0)
  await away()
  await expect(card).toContainText('Cache added; review next.')
  // It sits on the prompt, where the eyes are, not at the top of the chat;
  // it is measured once it has risen in.
  await page.waitForTimeout(250)
  const cardBox = (await card.boundingBox())!
  const promptBox = (await page.locator('.pane.focused .prompt').boundingBox())!
  expect(promptBox.y - (cardBox.y + cardBox.height)).toBeGreaterThanOrEqual(0)
  expect(promptBox.y - (cardBox.y + cardBox.height)).toBeLessThan(40)
  await shot('44-recap-on-prompt')
  await page.keyboard.press('Escape')
  await page.keyboard.press('Escape')
  await expect(card).toHaveCount(0)
  await away()
  await expect(card).toHaveCount(0)

  await recap('Docs updated.')
  await away()
  await expect(card).toContainText('Docs updated.')
  await card.locator('.recap-close').click()
  await expect(card).toHaveCount(0)

  await recap('Store moved.')
  await away()
  await expect(card).toContainText('Store moved.')
  await page.locator('.pane.focused .prompt').click()
  await page.keyboard.type('go on')
  await page.keyboard.press('Enter')
  await expect(card).toHaveCount(0)

  // :recap shows it again, closed or not.
  await normal()
  await page.keyboard.type(':')
  await page.keyboard.type('recap')
  await page.keyboard.press('Enter')
  await expect(card).toContainText('Store moved.')
})

test('Copy on a message copies its markdown, and on a code block its code', async () => {
  const saved = await app.evaluate(({ clipboard }) => clipboard.readText())
  try {
    await page.keyboard.press('Escape')
    await page.keyboard.press('Space')
    await page.keyboard.press('c')
    hub.event('demo', 'coordinator', { kind: 'text', text: 'Run **this**:\n\n```sh\n./check.sh\n```' })
    const msg = page.locator('.pane.focused .copyable').last()
    // Copy waits for a hover, then shows at the end of the reply's last
    // line, so it takes no line of its own.
    const copy = msg.locator('.msg-copy')
    await page.mouse.move(700, 10)
    await expect(copy).toHaveCSS('opacity', '0')
    await msg.hover()
    await expect(copy).toHaveCSS('opacity', '1')
    const copyBox = (await copy.boundingBox())!
    const textBox = (await msg.locator('.md').boundingBox())!
    expect(copyBox.y + copyBox.height).toBeGreaterThanOrEqual(textBox.y + textBox.height - 1)
    expect(Math.abs(copyBox.x + copyBox.width - (textBox.x + textBox.width))).toBeLessThan(12)
    await copy.click()
    await expect.poll(() => app.evaluate(({ clipboard }) => clipboard.readText())).toBe('Run **this**:\n\n```sh\n./check.sh\n```')
    await expect(msg.locator('.msg-copy')).toHaveText('Copied')
    await msg.locator('pre.code').hover()
    await msg.locator('pre.code .copy').click()
    await expect.poll(() => app.evaluate(({ clipboard }) => clipboard.readText())).toBe('./check.sh')
  } finally {
    await app.evaluate(({ clipboard }, t) => clipboard.writeText(t), saved)
  }
})

test('blocks that come while you read above bring a "↓ new" chip, which goes to the end', async () => {
  await page.keyboard.press('Escape')
  await page.keyboard.press('Space')
  await page.keyboard.press('c')
  for (let i = 0; i < 30; i++) hub.event('demo', 'coordinator', { kind: 'text', text: `filler ${i}\n\nmore text to fill the view` })
  const t = page.locator('.pane.focused .transcript')
  await expect(page.locator('.pane.focused .row', { hasText: 'filler 29' })).toBeVisible()
  const chip = page.locator('.pane.focused .new-chip:not(.leaving)')
  // G goes to the end and follows it: no chip.
  await t.click()
  await page.keyboard.press('Shift+G')
  await expect(chip).toHaveCount(0)
  await t.hover()
  await page.mouse.wheel(0, -2000)
  await expect.poll(() => t.evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight)).toBeGreaterThan(200)
  hub.event('demo', 'coordinator', { kind: 'text', text: 'the newest' })
  await expect(chip).toBeVisible()
  await shot('48-new-chip')
  await chip.click()
  await expect(chip).toHaveCount(0)
  await expect.poll(() => t.evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight)).toBeLessThan(24)
})

test(':todo shows the thread\'s todo list, follows it, and <C-o> goes back', async () => {
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('c')
  const name = page.locator('.statusline.focused .sl-name')
  await page.keyboard.type(':')
  await page.keyboard.type('b api')
  await page.keyboard.press('Enter')
  await expect(name).toHaveText('api')
  await page.keyboard.type(':')
  await page.keyboard.type('todo')
  await page.keyboard.press('Enter')
  await expect(name).toHaveText('api todo')
  await expect(page.locator('.todo .empty')).toHaveText('No todo list yet.')
  hub.setView(
    view('demo', 'api', {
      todos: [
        { id: '1', text: 'Add the /healthz route', state: 'done' },
        { id: '2', text: 'Write its test', state: 'active' },
        { id: '3', text: 'Document it', state: 'pending' },
      ],
    }),
  )
  await expect(page.locator('.todo .todo-row')).toHaveText(['✓Add the /healthz route', '●Write its test', '○Document it'])
  await expect(page.locator('.todo .todo-row.done')).toHaveCount(1)
  await expect(page.locator('.todo .todo-row.done .todo-text')).toHaveCSS('text-decoration-line', 'line-through')
  await expect(page.locator('.todo .todo-count')).toHaveText('1 of 3 done')
  await shot('34-todo')
  await page.keyboard.press('Control+o')
  await expect(name).toHaveText('api')
  hub.setView(view('demo', 'api'))
})

test('the todo list shows in one line above the prompt and unfolds, as in the TUI', async () => {
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('c')
  const name = page.locator('.statusline.focused .sl-name')
  await page.keyboard.type(':')
  await page.keyboard.type('b api')
  await page.keyboard.press('Enter')
  await expect(name).toHaveText('api')
  const todos = [
    { id: '1', text: 'Add the /healthz route', state: 'done' as const },
    { id: '2', text: 'Write its test', state: 'active' as const },
    { id: '3', text: 'Document it', state: 'pending' as const },
  ]
  hub.setView(view('demo', 'api', { todos }))
  const line = page.locator('.window', { has: page.locator('.statusline.focused') }).locator('.todo-line')
  await expect(line.locator('.todo-head')).toHaveText('▸1 of 3 · Write its test')
  await expect(line.locator('.todo-item')).toHaveCount(0)
  await page.keyboard.press('Space')
  await page.keyboard.press('T')
  await expect(line.locator('.todo-head')).toHaveText('▾1 of 3')
  await expect(line.locator('.todo-item')).toHaveText(['✓Add the /healthz route', '●Write its test', '○Document it'])
  await expect(line.locator('.todo-item.done .todo-text')).toHaveCSS('text-decoration-line', 'line-through')
  await shot('43-todo-line')
  // za on the line folds it, as on a fold of tool calls; a click opens it.
  await page.keyboard.press('k')
  await page.keyboard.press('z')
  await page.keyboard.press('a')
  await expect(line.locator('.todo-item')).toHaveCount(0)
  await line.locator('.todo-head').click()
  await expect(line.locator('.todo-item')).toHaveCount(3)
  hub.setView(view('demo', 'api', { todos: todos.map((t) => ({ ...t, state: 'done' as const })) }))
  await expect(line).toHaveCount(0)
  hub.setView(view('demo', 'api'))
})

test('a notice waits while you type and shows when you leave Insert; what needs you shows at once, on top', async () => {
  hub.setView(view('demo', 'ledger'))
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('c')
  const prompt = page.locator('.pane.focused .prompt .cm-content')
  await page.keyboard.press('i')
  await expect(prompt).toBeFocused()
  const toast = (text: string) => page.locator('.toast:not(.leaving)', { hasText: text })
  hub.setView(view('demo', 'notes'))
  await page.waitForTimeout(1500)
  await expect(toast('notes')).toHaveCount(0)
  hub.setView(view('demo', 'ledger', { state: 'waiting', permission: { id: 'p7', tool: 'Bash', detail: 'psql' } }))
  await expect(toast('needs permission')).toBeVisible()
  await normal()
  await expect(toast('opened in')).toBeVisible()
  await expect(page.locator('.toast:not(.leaving)').first()).toContainText('needs permission')
  await shot('50-toasts-order')
  hub.setView(view('demo', 'ledger', { state: 'resolved' }))
  hub.setView(view('demo', 'notes', { state: 'resolved' }))
  await page.keyboard.press('Space')
  await page.keyboard.press('n')
  await page.keyboard.press('c')
})

test('a reply streams in a block at a time, open code shows plain, and the text event replaces it', async () => {
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('c')
  const stream = page.locator('.pane.focused .row.stream')
  hub.delta('demo', 'coordinator', 'First I read the code.')
  await expect(stream).toContainText('First I read the code.')
  hub.delta('demo', 'coordinator', 'First I read the code.\n\nThen:\n\n```go\nfunc main() {')
  await expect(stream.locator('pre.code')).toContainText('func main() {')
  await expect(stream.locator('pre.code .hljs')).toHaveCount(0)
  await expect(stream.locator('.md-block.settle-in')).toHaveCount(2)
  const final = 'First I read the code.\n\nThen:\n\n```go\nfunc main() {}\n```'
  hub.event('demo', 'coordinator', { kind: 'text', text: final })
  await expect(stream).toHaveCount(0)
  await expect(page.locator('.pane.focused .row').last().locator('pre.code .hljs')).toContainText('func main() {}')
  // An empty delta drops a reply cut short.
  hub.delta('demo', 'coordinator', 'half a')
  await expect(stream).toBeVisible()
  hub.delta('demo', 'coordinator', '')
  await expect(stream).toHaveCount(0)
})

test('a hub that stops says so and offers to start it', async () => {
  hub.stop()
  await expect(page.locator('.banner', { hasText: 'neoharness hub start' })).toBeVisible()
  await expect(page.locator('.banner button', { hasText: 'Start the hub' })).toBeVisible()
  await shot('26-hub-stopped')
})
