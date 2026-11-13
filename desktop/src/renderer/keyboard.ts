// The keymap. One listener sees every key before the page does and decides
// who gets it: an editor in Insert mode keeps its keys; Vim's Normal mode in
// the prompt shares them with the app (: and <leader> are the app's); and
// otherwise the window in focus and the app take them.

import { getCM } from '@replit/codemirror-vim'
import type { EditorView } from '@codemirror/view'

import { command } from './commands'
import { keyName, Sequencer } from './lib/keys'
import { store } from './store'
import { editorKeys, editors, handlers, typeahead, type WinAction } from './ui/registry'

type Action = { cmd: string } | { win: WinAction } | { move: 'h' | 'j' | 'k' | 'l' } | { fn: () => void }

const c = (cmd: string): Action => ({ cmd })
const w = (win: WinAction): Action => ({ win })

// app maps work everywhere outside Insert mode.
const appMaps: Record<string, Action> = {
  '<leader><leader>': c('everywhere'),
  '<leader>pb': c('buffers'),
  '<leader>pt': c('threads'),
  '<leader>pl': { fn: () => store.openPicker('threads', true) },
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
  '<S-Tab>': { fn: () => store.nextWindow(-1) },
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
}

const normal = new Sequencer<Action>({ ...appMaps, ...winMaps })
const promptNormal = new Sequencer<Action>(appMaps)
let timer: ReturnType<typeof setTimeout> | null = null
let lastShift = 0

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
  if (m.kind === 'action') return run(m.action, m.count) || true
  if (m.kind === 'pending') {
    // As Vim's timeoutlen: keys that are a map and start a longer one wait
    // a second, then run as the shorter map.
    timer = setTimeout(() => {
      const f = seq.flush()
      store.setPending('')
      if (f.kind === 'action') run(f.action, f.count)
    }, 1000)
    return true
  }
  return false
}

export function onKey(e: KeyboardEvent): void {
  const s = store.state
  const key = keyName(e)

  // Shift twice is JetBrains' Search Everywhere.
  if (e.key === 'Shift' && !e.repeat) {
    const now = Date.now()
    if (now - lastShift < 350 && !s.picker) {
      lastShift = 0
      command('everywhere').run()
      e.preventDefault()
      return
    }
    lastShift = now
    return
  }
  if (!key) return
  lastShift = 0

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
  // The picker and the command line are inputs of their own.
  if (s.picker || s.cmdline !== null) return

  if (key.startsWith('<D-')) {
    const a = macMaps[key]
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
    if (promptNormal.pending || key === '<Space>' || key.startsWith('<C-w') || /^<C-[hjkl]>$/.test(key) || key === '<Tab>') {
      if (feed(promptNormal, key)) {
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
  if (feed(normal, key)) e.preventDefault()
}

export function installKeys(): void {
  window.addEventListener('keydown', onKey, true)
  window.addEventListener('blur', () => {
    lastShift = 0
  })
}
