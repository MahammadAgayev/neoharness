// Every action of the app, with its keys. The keymap, the palette and the
// help screen all read this list, so a new action shows in all three.

import { store } from './store'

export interface Command {
  id: string
  title: string
  // keys are the Vim keys and the macOS shortcuts that run it, as shown.
  keys: string[]
  // ex prefills the command line for actions that need words.
  ex?: string
  run: () => void
}

const s = store

export const commands: Command[] = [
  { id: 'everywhere', title: 'Search everywhere', keys: ['⌘K', '<leader><leader>'], run: () => s.openPicker('all') },
  { id: 'buffers', title: 'Pick a buffer', keys: ['<leader>pb'], run: () => s.openPicker('buffers', true) },
  { id: 'threads', title: 'Pick a thread of any workspace', keys: ['<leader>pt', '⌘P'], run: () => s.openPicker('threads', true) },
  { id: 'local-threads', title: 'Pick a thread of this workspace', keys: ['<leader>pl'], run: () => s.openPicker('threads', true) },
  { id: 'artifacts', title: 'Pick an artifact', keys: ['<leader>pa', ':artifacts'], run: () => s.openPicker('artifacts', true) },
  { id: 'grep', title: 'Search every transcript', keys: ['<leader>ps', '⌘⇧F'], run: () => s.openPicker('text', true) },
  { id: 'notifications', title: 'Pick a notification', keys: ['<leader>pm'], run: () => s.openPicker('notifications', true) },
  { id: 'workspace-pick', title: 'Pick a workspace', keys: ['<leader>w', ':workspace'], run: () => s.openPicker('workspaces', true) },
  { id: 'workspaces', title: 'Workspaces screen', keys: ['<leader>pv', ':workspaces'], run: () => s.showWorkspaces() },
  { id: 'outline', title: 'Outline: messages or headings', keys: ['gO'], run: () => s.openPicker('outline', true) },
  { id: 'threads-pane', title: 'Threads list', keys: ['<leader>t', ':threads'], run: () => s.threadsPane() },
  { id: 'coordinator', title: 'Coordinator', keys: ['<leader>c'], run: () => s.coordinator() },
  { id: 'new-workspace', title: 'New workspace…', keys: [':new <name> [folder]'], ex: 'new ', run: () => s.openCmdline('new ') },
  { id: 'folder', title: "Change the workspace's folder…", keys: [':folder [folder]'], run: () => void s.setFolder() },
  { id: 'setlocal', title: "Set this workspace's agents…", keys: [':setlocal coordinator=<agent>'], run: () => s.openCmdline('setlocal ') },
  { id: 'restore', title: 'Restore a deleted workspace…', keys: [':restore [name]'], run: () => s.openCmdline('restore ') },
  { id: 'open-thread', title: 'Open a thread…', keys: [':open <name> [folder|host:dir]', '⌘N'], run: () => s.openCmdline('open ') },
  { id: 'resolve', title: 'Resolve the thread', keys: [':resolve'], run: () => void s.resolve() },
  { id: 'reopen', title: 'Reopen the thread', keys: [':reopen'], run: () => void s.reopen() },
  { id: 'stop', title: 'Stop the running turn', keys: ['x on the turn', '⌘.', ':stop'], run: () => void s.stop() },
  { id: 'delete-thread', title: 'Delete the thread…', keys: ['<C-d> in a list'], run: () => s.askDeleteThread(s.target()) },
  { id: 'allow', title: 'Allow the permission', keys: ['<leader>a', ':allow'], run: () => void s.answer('allow') },
  { id: 'always', title: 'Allow always', keys: ['<leader>A', ':always'], run: () => void s.answer('always') },
  { id: 'deny', title: 'Deny the permission', keys: ['<leader>x', ':deny [reason]'], run: () => void s.answer('deny') },
  { id: 'dismiss', title: 'Dismiss notifications', keys: ['<leader>nc'], run: () => s.dismissToasts() },
  { id: 'messages', title: 'Notification history', keys: ['<leader>nh', ':messages'], run: () => void s.ex('messages') },
  { id: 'vsplit', title: 'Split right', keys: ['<C-w>v', ':vsplit [name]', '⌘\\'], run: () => s.split(true) },
  { id: 'split', title: 'Split above', keys: ['<C-w>s', ':split [name]'], run: () => s.split(false) },
  { id: 'close', title: 'Close the window', keys: ['<C-w>c', ':q', '⌘W'], run: () => void s.ex('q') },
  { id: 'only', title: 'Only this window', keys: ['<C-w>o', ':only'], run: () => s.only() },
  { id: 'next-window', title: 'Next window', keys: ['<Tab>', '<C-w>w'], run: () => s.nextWindow(1) },
  { id: 'back', title: 'Back to the buffer before', keys: ['<C-o>'], run: () => s.goBack() },
  { id: 'alternate', title: 'Alternate buffer', keys: ['<C-^>'], run: () => s.alternate() },
  { id: 'next-thread', title: 'Next thread', keys: [']b'], run: () => s.cycleThread(1) },
  { id: 'prev-thread', title: 'Previous thread', keys: ['[b'], run: () => s.cycleThread(-1) },
  { id: 'save', title: 'Save the artifact', keys: [':w', '⌘S'], run: () => void s.ex('w') },
  { id: 'edit', title: 'Edit the artifact', keys: ['i a o', ':e'], run: () => void s.ex('e') },
  { id: 'revert', title: "Take the agent's version", keys: [':e!'], run: () => void s.ex('e!') },
  { id: 'scheme', title: 'Color scheme…', keys: [':colorscheme <name>'], run: () => s.openCmdline('colorscheme ') },
  { id: 'vim', title: 'Vim mode on or off', keys: [':set vim', ':set novim'], run: () => s.setSettings({ vim: !s.state.settings.vim }) },
  { id: 'start-hub', title: 'Start the hub', keys: ['neoharness hub start'], run: () => void s.startHub() },
  { id: 'help', title: 'Keys and commands', keys: [':help', '⌘/'], run: () => void s.ex('help') },
]

export function command(id: string): Command {
  const c = commands.find((x) => x.id === id)
  if (!c) throw new Error(`no command ${id}`)
  return c
}
