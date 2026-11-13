import { useEffect, useRef } from 'react'

import { commands } from '../commands'
import { keyName } from '../lib/keys'
import { store, useStore, type Win } from '../store'
import { handlers, type WinAction } from './registry'
import { Glyph } from './Icon'
import { CloseWindow } from './CloseWindow'

function useScrollKeys(win: Win, el: React.RefObject<HTMLDivElement | null>) {
  const handle = (a: WinAction, count: number): boolean => {
    const e = el.current
    if (!e) return false
    const by: Partial<Record<WinAction, number>> = {
      down: 24 * count,
      up: -24 * count,
      halfDown: e.clientHeight / 2,
      halfUp: -e.clientHeight / 2,
      pageDown: e.clientHeight - 40,
      pageUp: -(e.clientHeight - 40),
    }
    if (a === 'top') e.scrollTo({ top: 0 })
    else if (a === 'bottom') e.scrollTo({ top: e.scrollHeight })
    else if (by[a] !== undefined) e.scrollBy({ top: by[a] })
    else return false
    return true
  }
  const ref = useRef(handle)
  ref.current = handle
  useEffect(() => {
    handlers.set(win.id, (a, n) => ref.current(a, n))
    return () => void handlers.delete(win.id)
  }, [win.id])
}

const sections: { title: string; rows: [string, string][] }[] = [
  {
    title: 'write',
    rows: [
      ['i a o', 'write to the thread in this window'],
      ['<CR>', 'send; on a queued message: send it now, stopping the running turn'],
      ['<CR> on your message', 'it comes back to the prompt to edit and send again'],
      ['u', 'with an empty prompt: your last unanswered message back; a queued one leaves the queue, a running turn stops'],
      ['<Esc>', 'without Vim: as u, as in Claude Code; again goes to the transcript'],
      ['Ctrl S  Ctrl X Ctrl S', 'stash the draft; it comes back after you send'],
      ['!<command>', "run it in the thread's folder; the output goes with your next message"],
      ['⇧<CR>  <C-j>', 'new line'],
      ['<C-u>  <C-w>', 'delete to the start of the line, a word back (in Insert)'],
      ['/  @', 'complete a command, a thread or a file'],
      ['p', 'put the last yank in the prompt'],
    ],
  },
  {
    title: 'move',
    rows: [
      ['j k  gg G  <C-d> <C-u>', 'blocks, ends, half pages; in the prompt, k on the first line goes up'],
      [']m [m', 'next / previous message, or heading in an artifact'],
      ['gO', 'outline: a message in a chat, a heading in an artifact'],
      ['/ n N', 'search the chat, next, back'],
      ['za  <leader>e', "open or close a turn's tool calls"],
      ['y  <leader>y', 'yank the block, or the selection, to the clipboard'],
      ['v V  then j k  y', 'select blocks, then yank them; <Esc> ends it'],
      ['x', 'stop the running turn'],
      ["m{a-z}  '{a-z}  ''", 'mark a block or a thread row, go to the mark, go back'],
    ],
  },
  {
    title: 'windows',
    rows: [
      ['<Tab>  <C-w>w', 'next window'],
      ['<C-h j k l>  <C-w>h…', 'window in a direction'],
      ['<C-w>v  <C-w>s', 'split right / above'],
      ['<C-w>c  <C-w>o  <C-w>=', 'close, only, equal sizes'],
      [']b [b  <C-^>', 'next / previous thread, alternate'],
      ['<C-o>  <C-i>  ⌘[  ⌘]', 'back and forward through what the window showed'],
      ['<C-v>  <C-x>', 'thread in a split on the right / above (threads list, search)'],
      ['<C-d>', 'delete the thread or workspace, after y (search)'],
    ],
  },
  {
    title: 'search everywhere  ⌘K  <leader><leader>',
    rows: [
      ['<Tab>  ⇧<Tab>', 'next / previous tab: all, threads, artifacts, text, commands, …'],
      ['<C-n> <C-p>  ↑ ↓', 'next / previous result'],
      ['<CR>  <C-v>  <C-x>', 'open, split right, split above; a split keeps your workspace'],
      ['<leader>pt  <leader>pa  <leader>ps', 'open on threads, artifacts, text'],
    ],
  },
  {
    title: 'an artifact',
    rows: [
      ['<leader>d  ⌘⇧N  :doc <name>', 'start a doc you write; the coordinator works from it and keeps its status'],
      ['i a o', 'write in it: it shows rendered, and the markdown only on the lines under the cursor'],
      ['⌘-click', 'open an artifact named in code, or a link'],
      [':w  ⌘S', 'save, merged with a newer version; the writer gets the diff'],
      ['<leader>  <C-w>  g  ]  [', 'what can follow shows at once, as which-key; Settings turns it off'],
      [']c [c  do dp', 'in a refused save: next / previous change, take the change of the newer version'],
      [':w!  :e!', 'save over a newer version, take the newer version'],
      ['<Esc>', 'leave Insert; an unsaved change shows [+]'],
      [':wq  :q!', 'save and close, drop the changes and close'],
    ],
  },
  {
    title: 'commands',
    rows: [
      [':new <name> [folder]', 'create a workspace; no folder opens a folder picker; <Tab> completes a ~/ or / folder'],
      [':folder [folder]', "change the workspace's folder"],
      [':setlocal coordinator=<agent>', "this workspace's coordinator runs that agent; threads= for its threads"],
      [':open <name> [where]', 'open a thread: folder or host:dir'],
      [':resolve  :reopen  :stop', 'close or bring back a thread, stop its turn'],
      [':allow :always :deny', 'answer a permission (:deny <reason>)'],
      [':b <name>  :ls', 'switch buffer'],
      [':vsplit :split [name]', 'split with a buffer'],
      [':threads  :artifacts  :messages', 'threads list, artifacts, notification history'],
      [':workspaces  :workspace <n>', 'workspace list, switch workspace; ⌘1…⌘9 too'],
      [':restore [name]', 'bring back a deleted workspace; no name lists them'],
      [':colorscheme <name>', 'system, rose-pine-moon, rose-pine, rose-pine-dawn'],
      [':set vim  :set novim', 'Vim keys in editors, or keys as in Claude Code'],
      [':set nosidebar  ⌘B', 'hide the sidebar of workspaces, for focus'],
      [':set fontsize=15  compactwarn=80', 'text size; the context % that says /compact'],
      [':settings  <leader>s  ⌘,', 'theme, text size, Vim keys, the leader and your own keys'],
      [':q', 'close the window; ⌘Q quits, and the hub and its turns run on'],
    ],
  },
]

// Help is the keys and commands, in a dialog as the settings: <leader>h,
// ⌘/ or :help opens it, Esc closes it, j and k scroll it.
export function Help() {
  const open = useStore((s) => s.helpOpen)
  const leader = useStore((s) => s.settings.leader)
  const el = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (open) el.current?.focus()
  }, [open])
  if (!open) return null
  const close = () => store.openHelp(false)
  const onKey = (e: React.KeyboardEvent) => {
    const k = keyName(e.nativeEvent)
    const d = el.current
    if (!d || !k) return
    const by: Record<string, number> = {
      j: 40,
      k: -40,
      '<Down>': 40,
      '<Up>': -40,
      '<C-d>': d.clientHeight / 2,
      '<C-u>': -d.clientHeight / 2,
      '<Space>': d.clientHeight - 40,
    }
    if (k === '<Esc>' || k === 'q' || k === '<D-/>' || k === '<D-w>') close()
    else if (k === 'g') d.scrollTo({ top: 0 })
    else if (k === 'G') d.scrollTo({ top: d.scrollHeight })
    else if (by[k] !== undefined) d.scrollBy({ top: by[k] })
    else return
    e.preventDefault()
    e.stopPropagation()
  }
  return (
    <div className="overlay" onMouseDown={close}>
      <div className="palette settings help-dialog" onMouseDown={(e) => e.stopPropagation()} onKeyDown={onKey}>
        <div className="settings-head">
          <span className="settings-title">Keys and commands</span>
          <span className="t-muted">{`the leader is ${leader === '<Space>' ? 'Space' : leader} · Esc closes`}</span>
        </div>
        <div className="settings-body help-inner" ref={el} tabIndex={-1}>
          {sections.map((s) => (
            <section key={s.title}>
              <h2>{s.title}</h2>
              {s.rows.map(([k, d]) => (
                <div className="help-row" key={k + d}>
                  <span className="help-key">{k}</span>
                  <span className="help-what">{d}</span>
                </div>
              ))}
            </section>
          ))}
          <section>
            <h2>every command</h2>
            {commands.map((c) => (
              <div className="help-row" key={c.id}>
                <span className="help-key">{c.keys.join('  ')}</span>
                <span className="help-what">{c.title}</span>
              </div>
            ))}
          </section>
        </div>
      </div>
    </div>
  )
}

export function MessagesWindow(props: { win: Win; focused: boolean }) {
  const log = useStore((s) => s.log)
  const el = useRef<HTMLDivElement>(null)
  useScrollKeys(props.win, el)
  return (
    <div className="window messages" onMouseDown={() => store.focus(props.win.id)}>
      <div className={`statusline ${props.focused ? 'focused' : ''}`}>
        <span className="sl-name">messages</span>
        <CloseWindow id={props.win.id} />
      </div>
      <div className="list" ref={el} tabIndex={-1}>
        {log.length === 0 && <div className="empty">No notifications yet.</div>}
        {log
          .slice()
          .reverse()
          .map((t) => (
            <div key={t.id} className="list-row" onClick={() => void store.goThread(t.workspace, t.thread)}>
              <span className={`glyph t-${t.tone}`}>
                <Glyph g={t.glyph} />
              </span>
              <div className="list-main">
                <div className="list-title">
                  <span className="t-subtle">{t.workspace} · </span>
                  <span className="name">{t.thread}</span>
                  <span className="t-muted right">{new Date(t.at).toLocaleTimeString()}</span>
                </div>
                <div className="list-detail t-subtle">{t.body}</div>
              </div>
            </div>
          ))}
      </div>
    </div>
  )
}
