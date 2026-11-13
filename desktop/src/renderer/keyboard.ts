// The keymap. One listener sees every key before the page does and decides
// who gets it: an editor in Insert mode keeps its keys; Vim's Normal mode in
// the prompt shares them with the app (: and <leader> are the app's); and
// otherwise the window in focus and the app take them.

import { getCM } from '@replit/codemirror-vim'
import type { EditorView } from '@codemirror/view'

import { command, commands } from './commands'
import { keyName, Sequencer } from './lib/keys'
import { store, type Hint, type Settings } from './store'
import { editorKeys, editors, handlers, typeahead, type WinAction } from './ui/registry'

// desc names an fn action in the key hints.
type Action = { cmd: string } | { win: WinAction } | { move: 'h' | 'j' | 'k' | 'l' } | { fn: () => void; desc?: string }

const c = (cmd: string): Action => ({ cmd })
const w = (win: WinAction): Action => ({ win })

// app maps work everywhere outside Insert mode.
const appMaps: Record<string, Action> = {
  '<leader><leader>': c('everywhere'),
  '<leader>pb': c('buffers'),
  '<leader>pt': c('threads'),
  '<leader>pl': c('local-threads'),
  '<leader>pa': c('artifacts'),
  '<leader>ps': c('grep'),
  '<leader>pm': c('notifications'),
  '<leader>pv': c('workspaces'),
  '<leader>w': c('workspace-pick'),
  '<leader>t': c('threads-pane'),
  '<leader>c': c('coordinator'),
  '<leader>nc': c('dismiss'),
  '<leader>nh': c('messages'),
  '<leader>a': c('allow'),
  '<leader>A': c('always'),
  '<leader>x': c('deny'),
  '<leader>e': w('fold'),
  '<leader>y': w('yank'),
  gO: c('outline'),
  '<C-w>v': c('vsplit'),
  '<C-w>s': c('split'),
  '<C-w>c': c('close'),
  '<C-w>q': c('close'),
  '<C-w>o': c('only'),
  '<C-w>=': { fn: () => document.dispatchEvent(new CustomEvent('nh:equal')), desc: 'Equal sizes' },
  '<C-w>w': c('next-window'),
  '<C-w>h': { move: 'h' },
  '<C-w>j': { move: 'j' },
  '<C-w>k': { move: 'k' },
  '<C-w>l': { move: 'l' },
  '<C-h>': { move: 'h' },
  '<C-j>': { move: 'j' },
  '<C-k>': { move: 'k' },
  '<C-l>': { move: 'l' },
  '<Tab>': c('next-window'),
  '<S-Tab>': { fn: () => store.nextWindow(-1), desc: 'Previous window' },
  '<C-o>': c('back'),
  '<C-^>': c('alternate'),
  '<C-6>': c('alternate'),
  ']b': c('next-thread'),
  '[b': c('prev-thread'),
}

// window maps act on what the window in focus shows.
const winMaps: Record<string, Action> = {
  j: w('down'),
  '<Down>': w('down'),
  k: w('up'),
  '<Up>': w('up'),
  gg: w('top'),
  G: w('bottom'),
  '<C-d>': w('halfDown'),
  '<C-u>': w('halfUp'),
  '<C-f>': w('pageDown'),
  '<C-b>': w('pageUp'),
  '<PageDown>': w('pageDown'),
  '<PageUp>': w('pageUp'),
  ']c': w('nextHunk'),
  '[c': w('prevHunk'),
  do: w('takeHunk'),
  dp: w('takeHunk'),
  ']m': w('nextMsg'),
  '[m': w('prevMsg'),
  '<CR>': w('enter'),
  za: w('fold'),
  yy: w('yank'),
  y: w('yank'),
  i: w('insert'),
  a: w('insert'),
  I: w('insert'),
  o: w('insert'),
  O: w('insert'),
  A: w('append'),
  x: w('stopHere'),
  u: w('takeBack'),
  p: w('put'),
  gx: w('link'),
  gf: w('link'),
  '<C-v>': w('splitRight'),
  '<C-x>': w('splitAbove'),
  '/': w('search'),
  n: w('searchNext'),
  N: w('searchPrev'),
  v: w('visual'),
  V: w('visual'),
  '<Esc>': w('escape'),
}

// mac maps are the shortcuts of a macOS app; they work in every mode.
const macMaps: Record<string, Action> = {
  '<D-k>': c('everywhere'),
  '<D-p>': c('threads'),
  '<D-S-f>': c('grep'),
  '<D-f>': w('search'),
  '<D-.>': c('stop'),
  '<D-w>': c('close'),
  '<D-n>': c('open-thread'),
  '<D-s>': c('save'),
  '<D-\\>': c('vsplit'),
  '<D-/>': c('help'),
  '<D-[>': c('back'),
  '<D-b>': c('sidebar'),
  '<D-,>': c('settings'),
}

// marks are per buffer: the block of a chat or the row of the threads list
// a letter names, as m{a-z} and '{a-z} in the TUI. ' is where the last
// jump came from. An editor has Vim's own marks.
const marks = new Map<string, Record<string, number>>()

function mark(op: 'm' | "'", r: string): void {
  const w = store.cur()
  if (store.state.screen !== 'workspace' || !w || (w.buf.kind !== 'chat' && w.buf.kind !== 'threads')) {
    store.say('marks are for a chat or the threads list', 'muted')
    return
  }
  const k = `${store.state.workspace}\0${w.buf.kind === 'chat' ? w.buf.thread : ''}`
  const m = marks.get(k) ?? {}
  marks.set(k, m)
  if (op === 'm') {
    m[r] = w.cursor
    return
  }
  const to = m[r]
  if (to === undefined) {
    if (r !== "'") store.say('mark not set', 'muted')
    return
  }
  m["'"] = w.cursor
  store.updateWin(w.id, to < 0 ? { cursor: -1, follow: true } : { cursor: to, follow: false })
}

for (const r of 'abcdefghijklmnopqrstuvwxyz') {
  winMaps['m' + r] = { fn: () => mark('m', r), desc: 'Set a mark' }
  winMaps["'" + r] = { fn: () => mark("'", r), desc: 'Go to the mark' }
  winMaps['`' + r] = { fn: () => mark("'", r), desc: 'Go to the mark' }
}
winMaps["''"] = { fn: () => mark("'", "'"), desc: 'Back to where the jump came from' }
winMaps['``'] = { fn: () => mark("'", "'"), desc: 'Back to where the jump came from' }

function userAction(run: string): Action | null {
  if (run === '') return { fn: () => {}, desc: 'Nothing' }
  if (run === ':') return null
  if (run.startsWith(':')) return { fn: () => void store.ex(run.slice(1)), desc: run }
  return commands.some((x) => x.id === run) ? c(run) : null
}

// The maps are built again when the settings change: the leader and the
// person's own maps, which win over the defaults.
let built: { settings: Settings; normal: Sequencer<Action>; prompt: Sequencer<Action>; mac: Record<string, Action> } | null = null

function keymaps() {
  const settings = store.state.settings
  if (built?.settings === settings) return built
  const user: Record<string, Action> = {}
  const mac = { ...macMaps }
  for (const m of settings.maps) {
    const a = userAction(m.run)
    if (!m.keys || !a) continue
    if (m.keys.startsWith('<D-')) mac[m.keys] = a
    else user[m.keys] = a
  }
  built = {
    settings,
    normal: new Sequencer<Action>({ ...appMaps, ...winMaps, ...user }, settings.leader),
    prompt: new Sequencer<Action>({ ...appMaps, ...user }, settings.leader),
    mac,
  }
  return built
}

let timer: ReturnType<typeof setTimeout> | null = null

const winLabels: Record<WinAction, string> = {
  down: 'Down',
  up: 'Up',
  top: 'Top',
  bottom: 'Bottom',
  halfDown: 'Half a page down',
  halfUp: 'Half a page up',
  pageDown: 'Page down',
  pageUp: 'Page up',
  nextMsg: 'Next message or heading',
  prevMsg: 'Previous message or heading',
  enter: 'Open, send now, or edit again',
  fold: 'Open or close the tool calls',
  yank: 'Yank',
  insert: 'Write',
  append: 'Write at the end',
  stopHere: 'Stop the turn',
  takeBack: 'Take your message back',
  put: 'Put the yank in the prompt',
  link: 'Open the link',
  splitRight: 'Open in a split on the right',
  splitAbove: 'Open in a split above',
  delete: 'Delete',
  search: 'Search',
  searchNext: 'Next match',
  searchPrev: 'Previous match',
  visual: 'Select blocks',
  escape: 'Stop selecting',
  nextHunk: 'Next change',
  prevHunk: 'Previous change',
  takeHunk: 'Take the change',
}

const moveLabels = { h: 'Window on the left', j: 'Window below', k: 'Window above', l: 'Window on the right' }

function describe(a: Action): string {
  if ('cmd' in a) return command(a.cmd).title
  if ('win' in a) return winLabels[a.win]
  if ('move' in a) return moveLabels[a.move]
  return a.desc ?? ''
}

// hints are what which-key shows after a key that starts other maps: the
// next key and what it does, or how many maps go on from it. a to z with
// the same action show as one row.
// groupNames name the groups of maps under the leader.
const groupNames: Record<string, string> = { p: 'pick', n: 'notifications' }

function hints(seq: Sequencer<Action>): Hint[] {
  const atLeader = seq.pending === store.state.settings.leader
  const groups = new Map<string, { label?: string; more: number }>()
  for (const { keys, action } of seq.next()) {
    const g = groups.get(keys[0]) ?? { more: 0 }
    if (keys.length === 1) g.label = describe(action)
    else g.more++
    groups.set(keys[0], g)
  }
  const rows = [...groups].map(([key, g]) => ({
    key,
    label: g.label ?? (atLeader && groupNames[key] ? `+${groupNames[key]} · ${g.more}` : `+${g.more} more`),
  }))
  const letters = new Map<string, number>()
  for (const r of rows) if (/^[a-z]$/.test(r.key)) letters.set(r.label, (letters.get(r.label) ?? 0) + 1)
  const out: Hint[] = []
  for (const r of rows) {
    if (/^[a-z]$/.test(r.key) && letters.get(r.label)! > 3) {
      if (!out.some((o) => o.key === 'a–z' && o.label === r.label)) out.push({ key: 'a–z', label: r.label })
    } else out.push(r)
  }
  return out.sort((a, b) => a.key.localeCompare(b.key))
}

function run(a: Action, count: number): boolean {
  if ('cmd' in a) {
    command(a.cmd).run()
    return true
  }
  if ('fn' in a) {
    a.fn()
    return true
  }
  if ('move' in a) {
    store.move(a.move)
    return true
  }
  const id = store.state.screen === 'workspaces' ? 0 : store.cur()?.id
  const h = id === undefined ? undefined : handlers.get(id)
  return h ? h(a.win, count) : false
}

function focusedEditor(): EditorView | null {
  for (const v of editors) if (v.hasFocus) return v
  return null
}

function vimState(v: EditorView) {
  return getCM(v)?.state.vim as
    | { insertMode: boolean; visualMode: boolean; inputState: { operator?: string | null; keyBuffer: string[]; motion?: string | null } }
    | undefined
}

// busy says Vim is in the middle of a command: after d it waits for a
// motion, after f for a letter. Those keys are Vim's.
function vimBusy(v: EditorView): boolean {
  const st = vimState(v)
  if (!st) return false
  return st.insertMode || st.visualMode || !!st.inputState.operator || st.inputState.keyBuffer.length > 0
}

function feed(seq: Sequencer<Action>, key: string): boolean {
  if (timer) clearTimeout(timer)
  const m = seq.feed(key)
  store.setPending(seq.pending)
  store.setHints(m.kind === 'pending' && /\D/.test(seq.pending) ? hints(seq) : null)
  if (m.kind === 'action') return run(m.action, m.count) || true
  if (m.kind === 'pending') {
    // With key hints on, keys that are no map of their own wait for the
    // next key while the hints show, as which-key does.
    if (store.state.settings.keyhints && !seq.exact) return true
    // As Vim's timeoutlen: keys that are a map and start a longer one wait
    // a second, then run as the shorter map.
    timer = setTimeout(() => {
      const f = seq.flush()
      store.setPending('')
      store.setHints(null)
      if (f.kind === 'action') run(f.action, f.count)
    }, 1000)
    return true
  }
  return false
}

export function onKey(e: KeyboardEvent): void {
  const s = store.state
  const key = keyName(e)

  if (!key) return

  if (typeahead.waiting && !focusedEditor() && !s.picker && s.cmdline === null) {
    if (key.length === 1 || key === '<Space>' || key === '<CR>') {
      e.preventDefault()
      if (key === '<CR>') typeahead.send = true
      else if (!typeahead.send) typeahead.text += key === '<Space>' ? ' ' : key
      return
    }
  }
  if (s.confirm) {
    e.preventDefault()
    store.answerConfirm(key === 'y' || key === 'Y')
    return
  }
  // The picker, the command line and the settings are inputs of their own.
  if (s.picker || s.cmdline !== null || s.settingsOpen) return

  const km = keymaps()
  if (key.startsWith('<D-')) {
    const a = km.mac[key]
    if (a) {
      e.preventDefault()
      run(a, 1)
    } else if (/^<D-[1-9]>$/.test(key)) {
      e.preventDefault()
      const ws = s.workspaces[Number(key[3]) - 1]
      if (ws) void store.openWorkspace(ws.name)
    }
    return
  }

  const ed = focusedEditor()
  if (ed) {
    // Without Vim an editor keeps every key; its own <Esc> leaves it.
    if (!s.settings.vim) return
    if (vimBusy(ed)) return
    if (editorKeys.get(ed)?.(key, false)) {
      e.preventDefault()
      e.stopPropagation()
      return
    }
    // Normal mode in an editor: the app's maps first, the rest is Vim's.
    if (key === ':') {
      e.preventDefault()
      e.stopPropagation()
      store.openCmdline()
      return
    }
    if (km.prompt.pending || key === s.settings.leader || key.startsWith('<C-w') || /^<C-[hjkl]>$/.test(key) || key === '<Tab>') {
      if (feed(km.prompt, key)) {
        e.preventDefault()
        e.stopPropagation()
      }
      return
    }
    return
  }

  if (s.message && key === '<Esc>') store.say('')
  if (key === ':') {
    e.preventDefault()
    store.openCmdline()
    return
  }
  if (feed(km.normal, key)) e.preventDefault()
}

export function installKeys(): void {
  window.addEventListener('keydown', onKey, true)
}
