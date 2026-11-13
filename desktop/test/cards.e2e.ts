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
  const perm = { id: 'p1', tool: 'Bash', detail: 'go test ./...' }
  hub.event('demo', 'api', { kind: 'permission', tool: 'Bash', detail: 'go test ./...', id: 'p1' })
  hub.setView(view('demo', 'api', { state: 'waiting', permission: perm }))
  const toast = page.locator('.toast', { hasText: 'needs permission · Bash' })
  await expect(toast).toBeVisible()
  await shot('20-permission-toast')
  await toast.click()
  await expect(page.locator('.card.permission button', { hasText: 'Allow' })).toBeVisible()
  // The thread is on screen now: its toast goes.
  await expect(toast).toHaveCount(0, { timeout: 3000 })
  await shot('21-permission-card')
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('a')
  await expect.poll(() => hub.calls.find((c) => c.path.endsWith('/answer'))?.body).toMatchObject({ id: 'p1', decision: 'allow' })
  await expect(page.locator('.row.k-permission', { hasText: '· allow' })).toBeVisible()
})

test('a question shows as a card you answer by writing', async () => {
  hub.event('demo', 'api', { kind: 'ask', text: 'Which **port** should it listen on?' })
  hub.setView(view('demo', 'api', { state: 'waiting', waiting: 'Which port should it listen on?' }))
  await expect(page.locator('.b-ask', { hasText: 'Which port' })).toBeVisible()
  await page.keyboard.press('i')
  await page.keyboard.type('8080')
  await page.keyboard.press('Enter')
  await expect.poll(() => hub.calls.find((c) => c.path.endsWith('/messages'))?.body).toMatchObject({ text: '8080' })
  await expect(page.locator('.b-user', { hasText: '8080' })).toBeVisible()
  await shot('22-question')
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

test('an artifact opens, edits, saves, and a newer version is merged or forced', async () => {
  hub.artifact('demo', 'plan', 'Health check plan', '# Plan\n\n- add /healthz\n- test it\n')
  hub.event('demo', 'api', { kind: 'artifact', detail: 'plan', text: 'Health check plan', version: 1 })
  await page.locator('.b-artifact', { hasText: 'Health check plan' }).click()
  // One document: rendered, with the markdown only under the cursor.
  await expect(page.locator('.window.artifact .cm-h1', { hasText: 'Plan' })).toBeVisible()
  await expect(page.locator('.window.artifact .cm-bullet')).toHaveCount(2)
  await shot('24-artifact')
  await page.keyboard.press('i')
  await expect(page.locator('.doc-editor .cm-focused')).toBeVisible()
  await page.keyboard.press('Meta+ArrowDown')
  await page.keyboard.press('Enter')
  await page.keyboard.type('- ship it')
  await page.keyboard.press('Escape')
  await expect(page.locator('.statusline .sl-name', { hasText: 'plan [+]' })).toBeVisible()
  await page.keyboard.press('Meta+s')
  await expect.poll(() => hub.calls.find((c) => c.method === 'PUT')?.body).toMatchObject({ base: 1, text: '# Plan\n\n- add /healthz\n- test it\n\n- ship it' })
  await expect(page.locator('.statusline .sl-name', { hasText: /^plan$/ })).toBeVisible()

  // The agent writes v3 while you edit: the save is refused, and you take
  // its changes one by one, then :w saves the result.
  await page.keyboard.press('i')
  await page.keyboard.press('Meta+ArrowUp')
  await page.keyboard.type('x')
  await page.keyboard.press('Escape')
  const v2 = '# Plan\n\n- add /healthz\n- test it\n\n- ship it'
  hub.artifact('demo', 'plan', 'Health check plan', v2 + '\n- deploy', 3)
  await expect(page.locator('.cmdline', { hasText: 'wrote v3 while you edit' })).toBeVisible()
  await page.keyboard.press('Meta+s')
  await expect(page.locator('.cmdline', { hasText: 'do takes a change' })).toBeVisible()
  await expect(page.locator('.conflict', { hasText: 'changed the same lines in v3' })).toBeVisible()
  await expect(page.locator('.hunk')).toHaveCount(2)
  await expect(page.locator('.hunk.active .diff-line.d-del')).toHaveText('-x# Plan')
  await page.keyboard.press(']')
  await page.keyboard.press('c')
  await expect(page.locator('.hunk.active .diff-line.d-add')).toHaveText('+- deploy')
  await shot('25-artifact-conflict')
  await page.keyboard.press('d')
  await page.keyboard.press('o')
  await expect(page.locator('.hunk')).toHaveCount(1)
  await page.keyboard.type(':')
  await page.keyboard.type('w')
  await page.keyboard.press('Enter')
  await expect
    .poll(() => hub.calls.filter((c) => c.method === 'PUT').pop()?.body)
    .toMatchObject({ base: 3, force: false, text: 'x' + v2 + '\n- deploy' })
  await expect(page.locator('.conflict')).toHaveCount(0)
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
  // Esc stops the running turn and brings back your last message.
  await page.keyboard.press('Escape')
  await expect.poll(() => hub.calls.some((c) => c.path.endsWith('/stop'))).toBe(true)
  await expect(prompt).toHaveText('8080')
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
  expect(clip).toContain('8080')
}

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
  await expect(doc.locator('.cm-focused')).toBeVisible()
  await page.keyboard.type('- [ ] write the API')
  await expect(doc.locator('.sl-name', { hasText: 'launch-plan [+]' })).toBeVisible()
  await shot('38-new-doc')
  await page.keyboard.press('Escape')
  await page.keyboard.type(':')
  await page.keyboard.type('w')
  await page.keyboard.press('Enter')
  await expect
    .poll(() => hub.calls.filter((c) => c.method === 'PUT' && c.path.endsWith('/artifacts/launch-plan')).pop()?.body)
    .toMatchObject({ base: 1, text: '# Launch plan\n\n- [ ] write the API' })
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
    .toMatchObject({ text: '# Launch plan\n\n- [ ] write the API now' })
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

test('in a narrow column the prompt keeps its width and one line, the model and cost under it', async () => {
  await normal()
  await page.keyboard.press('Space')
  await page.keyboard.press('c')
  hub.setView(
    view('billing', 'coordinator', {
      location: { dir: '/tmp/fake', host: 'build-box.example.com' },
      model: 'claude-opus-5-5[1m]',
      context: 9000,
      contextWindow: 1000000,
      cost: 0.75,
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
  await expect.poll(async () => Math.round((await bar.boundingBox())!.width)).toBe(216)
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
  await page.locator('.pane.focused .doc-editor .cm-content').click()
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

test('a hub that stops says so and offers to start it', async () => {
  hub.stop()
  await expect(page.locator('.banner', { hasText: 'neoharness hub start' })).toBeVisible()
  await expect(page.locator('.banner button', { hasText: 'Start the hub' })).toBeVisible()
  await shot('26-hub-stopped')
})
