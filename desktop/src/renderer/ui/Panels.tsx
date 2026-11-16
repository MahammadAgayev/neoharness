import { useEffect, useRef } from 'react'

import type { Todo, Wait } from '../../shared/model'
import { commands } from '../commands'
import { keyName } from '../lib/keys'
import { store, useStore, type Win } from '../store'
import { handlers, type WinAction } from './registry'
import { Glyph, Icon } from './Icon'
import { age } from '../search'
import { CloseWindow } from './CloseWindow'
import { Echo } from './Echo'

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
      ['za  <leader>e', "open or close a turn's tool calls, or the todo list above the prompt"],
      ['<leader>T', 'open or close the todo list above the prompt: what is done, what is in hand, what is next'],
      ['y  <leader>y', 'yank the block, or the selection, to the clipboard'],
      ['v V  then j k  y', 'select blocks, then yank them; <Esc> ends it'],
      ['<C-c>  x', 'stop the running turn, of the chat or the thread on the row; <C-c> in Insert does nothing'],
      ["m{a-z}  '{a-z}  ''", 'mark a block or a thread row, go to the mark, go back'],
      ['K', "on a thread row: the thread's last reply, on the command line"],
    ],
  },
  {
    title: 'windows',
    rows: [
      ['<Tab>  <C-w>w', 'next window'],
      ['<C-h j k l>  <C-w>h…', 'window in a direction; <C-w>h works from Insert too'],
      ['<C-w>v  <C-w>s', 'split right / above'],
      ['<C-w>c  <C-w>o  <C-w>=', 'close, only, equal sizes'],
      [']b [b  <C-^>', 'next / previous thread, alternate'],
      [']q [q  :cn :cp', 'next / previous thing that waits for you, in any workspace: its thread opens at the card'],
      [':waits  :copen', 'the waits list, in a window under the threads list: <CR> goes there and the list stays, x dismisses a notice or a failure'],
      ['<C-o>  <C-i>  ⌘[  ⌘]', 'back and forward through what the window showed, from Insert too; or the arrows at the top'],
      ['<CR>  :b  :open', 'on the threads list: the thread opens in its place, and <C-o> brings the list back; on the waits list: in the chat window that had the focus last'],
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
      ['i a o', 'write in it; it stays rendered as you type, and saves as markdown'],
      ['h j k l  w b e  0 $  gg G  { }', 'move in Normal mode; a block is a line'],
      ['dd yy p  cw ciw  x  u <C-r>  v V  /', 'delete, yank and put blocks and words, undo, select, find'],
      ['# - 1. > ``` [ ]', 'at the start of a line: a heading, a list, a quote, code, a task'],
      ['/ on an empty line', 'in Insert: the block menu, filtered as you type: headings, lists, a table, code, a note, an artifact to link, a new page'],
      [':page <name>', 'make a page and link it at the cursor; an artifact that is there is only linked'],
      ['**b** *i* `c` ~~s~~', 'bold, italic, code and struck text as you type'],
      ['<Tab>  <S-Tab>', 'in a list: indent and outdent an item; in a table: the next cell, a new row at the end'],
      [':table col+  col-  row-', "in a table: add a column after the cursor's, take its column or row away"],
      [':table align l c r none', "align the cursor's column; a wide table grows to the pane"],
      ['click  gf  <CR>', 'open an artifact named in the page, or a link; <C-o> goes back; in Insert, ⌘-click'],
      [':w  ⌘S', 'save, merged with a newer version; the writer gets the diff'],
      ['<leader>  <C-w>  g  ]  [', 'what can follow shows at once, as which-key; Settings turns it off'],
      [']c [c  do dp', 'where you and an agent changed a block: next / previous one, take theirs'],
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
      [':setlocal reopen_compact=ask', 'when the coordinator reopens a thread past compactwarn: compact first, ask you, or off'],
      [':setlocal auto_compact=90', 'past this context %, a session compacts before its next turn; 0 never'],
      [':setlocal textwidth=90', 'the widest column of chats and artifacts, in characters; 0 the full width'],
      [':open <name> [where]', 'open a thread: folder or host:dir; from a chat it opens beside it'],
      [':resolve  :reopen  :stop', 'close or bring back a thread, stop its turn'],
      [':allow :always :deny', 'answer a permission; always covers what the card names (:deny <reason>)'],
      [':b <name>  :ls', 'switch buffer'],
      [':vsplit :split [name]', 'split with a buffer'],
      [':threads  :artifacts  :messages', 'threads list, artifacts, notification history'],
      [':workspaces  :workspace <n>', 'workspace list, switch workspace; ⌘1…⌘9 too'],
      [':restore [name]', 'bring back a deleted workspace; no name lists them'],
      ['j k  <CR>  x', 'on the workspace list: move, open, delete'],
      [':colorscheme <name>', 'system, rose-pine-moon, rose-pine, rose-pine-dawn'],
      [':set vim  :set novim', 'Vim keys in editors, or keys as in Claude Code'],
      [':set nosidebar  ⌘B', 'fold the sidebar to a rail of letters; the mouse resting on it shows it whole'],
      [':set fontsize=15  compactwarn=80', 'text size; the context % that says /compact'],
      [':settings  <leader>s  ⌘,', 'theme, text size, Vim keys, the leader and your own keys'],
      [':todo [name]', 'the todo list this thread, or another, keeps while it works; it follows the work, <C-o> goes back'],
      [':cost  :cost all  <leader>$', 'what this workspace, or every workspace, cost: by day, thread and model, and the turns that cost the most'],
      [':hub restart', 'restart the hub on the installed neoharness; after an update it does so on its own once no turn runs'],
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

// WaitsWindow is :waits, Vim's quickfix window for what needs you: <CR>
// goes there, x dismisses a notice or a failure.
// WaitBody reads as the board's detail row: a permission's tool in fg-2,
// then its command. The hub joins them as "Tool: command".
function WaitBody(props: { w: Wait }) {
  const text = props.w.text.replace(/\s+/g, ' ')
  const i = props.w.kind === 'permission' ? text.indexOf(': ') : -1
  if (i < 0) return <span className="wait-body">{text}</span>
  return (
    <span className="wait-body">
      <span className="t-subtle">{text.slice(0, i)}</span> {text.slice(i + 2)}
    </span>
  )
}

export function WaitsWindow(props: { win: Win; focused: boolean }) {
  const { win, focused } = props
  const waits = useStore((s) => s.waits)
  const open = useStore((s) => s.workspace)
  const cursor = Math.min(Math.max(win.cursor, 0), Math.max(waits.length - 1, 0))
  const el = useRef<HTMLDivElement>(null)
  const handle = (a: WinAction, count: number): boolean => {
    const set = (i: number) => store.updateWin(win.id, { cursor: Math.max(0, Math.min(i, waits.length - 1)) })
    switch (a) {
      case 'down':
        set(cursor + count)
        return true
      case 'up':
        set(cursor - count)
        return true
      case 'top':
        set(0)
        return true
      case 'bottom':
        set(waits.length - 1)
        return true
      case 'enter':
        void store.jumpWait(cursor)
        return true
      case 'stopHere':
        void store.dismissWait(cursor)
        return true
    }
    return false
  }
  const ref = useRef(handle)
  ref.current = handle
  useEffect(() => {
    handlers.set(win.id, (a, n) => ref.current(a, n))
    return () => void handlers.delete(win.id)
  }, [win.id])
  const now = Date.now()
  return (
    <div className="window waits" onMouseDown={() => store.focus(win.id)}>
      <div className={`statusline ${focused ? 'focused' : ''}`}>
        <span className="sl-name">waits</span>
        <Echo focused={focused} />
        <CloseWindow id={win.id} />
      </div>
      <div className="list board" ref={el} tabIndex={-1}>
        {waits.length === 0 && <div className="empty">nothing waits for you</div>}
        {waits.map((w, i) => (
          <div
            key={`${w.workspace}/${w.thread}/${w.id}`}
            className={`list-row ${focused && i === cursor ? 'cursor' : ''}`}
            onClick={() => {
              store.updateWin(win.id, { cursor: i })
              void store.jumpWait(i)
            }}
          >
            <span className={`glyph ${w.kind === 'failed' ? 't-love' : 't-gold'}`}>
              <Glyph g={w.kind === 'failed' ? '✗' : w.kind === 'notice' ? '↗' : '◆'} />
            </span>
            <span className="name">{w.workspace === open ? w.thread : `${w.workspace} · ${w.thread}`}</span>
            <span className={`wait-text ${w.kind === 'failed' ? 't-love' : 't-gold'}`}>{`${w.kind} · `}</span>
            <WaitBody w={w} />
            <span className="time">{age(w.since, now) || 'now'}</span>
          </div>
        ))}
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
        <Echo focused={props.focused} />
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

// TodoMark is a todo item's icon; thread glyphs are for threads only.
export function TodoMark(props: { state: Todo['state'] }) {
  return (
    <span className="glyph" data-state={props.state}>
      <Icon name={`todo-${props.state}`} size={14} />
    </span>
  )
}

export function TodoWindow(props: { win: Win; focused: boolean; thread: string }) {
  const todos = useStore((s) => s.threads[props.thread]?.todos) ?? []
  const el = useRef<HTMLDivElement>(null)
  useScrollKeys(props.win, el)
  const done = todos.filter((t) => t.state === 'done').length
  return (
    <div className="window todo" onMouseDown={() => store.focus(props.win.id)}>
      <div className={`statusline ${props.focused ? 'focused' : ''}`}>
        <span className="sl-name">{props.thread} todo</span>
        <Echo focused={props.focused} />
        <CloseWindow id={props.win.id} />
      </div>
      <div className="list" ref={el} tabIndex={-1}>
        {todos.length === 0 && <div className="empty">No todo list yet.</div>}
        {todos.map((t) => (
          <div key={t.id} className={`todo-row ${t.state}`}>
            <TodoMark state={t.state} />
            <span className="todo-text">{t.text}</span>
          </div>
        ))}
        {todos.length > 0 && (
          <div className="todo-count t-subtle">
            {done} of {todos.length} done
          </div>
        )}
      </div>
    </div>
  )
}
