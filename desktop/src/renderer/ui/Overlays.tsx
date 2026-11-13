import { useEffect, useMemo, useRef, useState } from 'react'

import { exCommands } from '../lib/ex'
import { items as searchItems, tabs, type Item } from '../search'
import { store, useStore } from '../store'

export function Picker() {
  const p = useStore((s) => s.picker)
  // The items depend on all of the state; the picker is open briefly.
  useStore((s) => s)
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const [confirmDel, setConfirmDel] = useState<Item | null>(null)
  const items = useMemo(() => (p ? searchItems(p.tab, p.query) : []), [p, store.state.threads, store.state.chats, store.state.artifacts])
  const index = p ? Math.min(p.index, Math.max(items.length - 1, 0)) : 0

  useEffect(() => {
    input.current?.focus()
  }, [p?.tab, !!p])
  useEffect(() => {
    list.current?.querySelector(`[data-i="${index}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [index])
  if (!p) return null
  const active = items[index] as Item | undefined

  // Artifacts, text, buffers and the outline are of the open workspace.
  const usable = store.state.workspace ? tabs : tabs.filter((t) => !['artifacts', 'text', 'buffers', 'outline'].includes(t.tab))
  const shown = p.only ? tabs.filter((t) => t.tab === p.tab) : usable
  const choose = (it: Item | undefined, how?: 'right' | 'above') => {
    if (!it) return
    if (how && !it.split) return
    store.closePicker()
    if (how && it.split) it.split(how === 'right')
    else it.run()
  }
  const cycle = (d: number) => {
    const i = usable.findIndex((t) => t.tab === p.tab)
    store.setPicker({ tab: usable[(i + d + usable.length) % usable.length].tab, index: 0, only: false })
  }
  const onKey = (e: React.KeyboardEvent) => {
    e.stopPropagation()
    if (confirmDel) {
      e.preventDefault()
      if (e.key === 'y') {
        confirmDel.del?.()
        store.closePicker()
      }
      setConfirmDel(null)
      return
    }
    const ctrl = e.ctrlKey
    if (e.key === 'Escape' || (ctrl && e.key === 'c')) store.closePicker()
    else if (e.key === 'Enter') choose(items[index])
    else if (e.key === 'ArrowDown' || (ctrl && (e.key === 'n' || e.key === 'j'))) store.setPicker({ index: Math.min(index + 1, items.length - 1) })
    else if (e.key === 'ArrowUp' || (ctrl && (e.key === 'p' || e.key === 'k'))) store.setPicker({ index: Math.max(index - 1, 0) })
    else if (e.key === 'Tab') cycle(e.shiftKey ? -1 : 1)
    else if (ctrl && e.key === 'v') choose(items[index], 'right')
    else if (ctrl && e.key === 'x') choose(items[index], 'above')
    else if (ctrl && e.key === 'd') {
      if (items[index]?.del) setConfirmDel(items[index])
    } else return
    e.preventDefault()
  }

  let section = ''
  return (
    <div className="overlay" onMouseDown={() => store.closePicker()}>
      <div className="palette" onMouseDown={(e) => e.stopPropagation()}>
        <div className="palette-input">
          <svg width="16" height="16" viewBox="0 0 16 16" className="t-muted">
            <circle cx="7" cy="7" r="5" fill="none" stroke="currentColor" strokeWidth="1.5" />
            <path d="M11 11l3.5 3.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
          </svg>
          <input
            ref={input}
            value={p.query}
            placeholder={p.tab === 'all' ? 'Search threads, artifacts, text, commands…' : `Search ${tabs.find((t) => t.tab === p.tab)?.title.toLowerCase()}…`}
            onChange={(e) => store.setPicker({ query: e.target.value, index: 0 })}
            onKeyDown={onKey}
            spellCheck={false}
          />
        </div>
        {shown.length > 1 && (
          <div className="palette-tabs">
            {shown.map((t) => (
              <button
                key={t.tab}
                className={`tab ${t.tab === p.tab ? 'active' : ''}`}
                onMouseDown={(e) => {
                  e.preventDefault()
                  store.setPicker({ tab: t.tab, index: 0 })
                }}
              >
                {t.title}
              </button>
            ))}
            <span className="tabs-hint t-muted">
              <kbd>Tab</kbd>
            </span>
          </div>
        )}
        <div className="palette-list" ref={list}>
          {items.length === 0 && (
            <div className="palette-empty t-muted">{p.tab === 'text' && p.query.length < 2 ? 'Type to search every transcript' : 'No results'}</div>
          )}
          {items.map((it, i) => {
            const head = p.tab === 'all' && it.section !== section ? it.section : ''
            section = it.section
            return (
              <div key={it.key}>
                {head && <div className="palette-section">{head}</div>}
                <div
                  data-i={i}
                  className={`palette-row ${i === index ? 'active' : ''}`}
                  onMouseMove={() => i !== index && store.setPicker({ index: i })}
                  onClick={() => choose(it)}
                >
                  <span className={`glyph t-${it.tone}`}>{it.glyph}</span>
                  <span className="palette-label">{it.label}</span>
                  {it.detail && <span className="palette-detail">{it.detail}</span>}
                  {it.hint && <kbd className="palette-hint">{it.hint}</kbd>}
                </div>
              </div>
            )
          })}
        </div>
        <div className="palette-foot t-muted">
          {confirmDel ? (
            <span className="t-gold">delete {confirmDel.label}? y/n</span>
          ) : (
            <>
              <span>
                <kbd>↵</kbd> {active?.open ?? 'open'}
              </span>
              {active?.split && (
                <>
                  <span>
                    <kbd>Ctrl V</kbd> split right
                  </span>
                  <span>
                    <kbd>Ctrl X</kbd> split above
                  </span>
                </>
              )}
              {active?.del && (
                <span>
                  <kbd>Ctrl D</kbd> delete
                </span>
              )}
              <span>
                <kbd>esc</kbd> close
              </span>
            </>
          )}
        </div>
      </div>
    </div>
  )
}

export function Toasts() {
  const toasts = useStore((s) => s.toasts)
  const shown = toasts.slice(-3).reverse()
  return (
    <div className="toasts">
      {shown.map((t) => (
        <div key={t.id} className={`toast b-${t.tone}`} onClick={() => void store.goThread(t.workspace, t.thread)}>
          <div className="toast-head">
            <span className={`t-${t.tone}`}>{t.glyph}</span>
            <span className="t-subtle">{t.workspace} ·</span>
            <span className="name">{t.thread}</span>
          </div>
          <div className="toast-body">{t.body}</div>
        </div>
      ))}
    </div>
  )
}

// complete is <Tab> on the command line: ex command names, then thread,
// workspace and artifact names.
async function complete(line: string): Promise<string[]> {
  const s = store.state
  const m = /^(\w*)$/.exec(line)
  if (m) return exCommands.filter((c) => c.startsWith(m[1])).map((c) => c + ' ')
  const [cmd, ...rest] = line.split(' ')
  const word = rest[rest.length - 1] ?? ''
  const head = line.slice(0, line.length - word.length)
  let names: string[] = []
  // host:dir is a folder on another machine; only local ones complete.
  if (((cmd === 'new' || cmd === 'open') && rest.length === 2) || (cmd === 'folder' && rest.length === 1)) {
    return /^[^/~]*:/.test(word) ? [] : (await window.nh.folders(word)).map((f) => head + f)
  }
  if (['workspace', 'restore'].includes(cmd)) names = s.workspaces.map((w) => w.name)
  else if (['b', 'buffer', 'split', 'vsplit', 'sp', 'vs', 'resolve', 'reopen', 'stop'].includes(cmd)) names = [...s.order, ...s.artifacts.map((a) => a.name)]
  else if (cmd === 'colorscheme' || cmd === 'colo') names = ['system', 'rose-pine-moon', 'rose-pine', 'rose-pine-dawn']
  else if (cmd === 'set') names = ['vim', 'novim', 'fontsize=', 'compactwarn=']
  else if (cmd === 'setlocal') {
    const agents = s.workspaces.find((w) => w.name === s.workspace)?.agentNames ?? []
    names = ['coordinator&', 'threads&', ...agents.flatMap((a) => [`coordinator=${a}`, `threads=${a}`])]
  }
  return names.filter((n) => n.startsWith(word)).map((n) => head + n)
}

export function CommandLine() {
  const cmdline = useStore((s) => s.cmdline)
  const message = useStore((s) => s.message)
  const confirm = useStore((s) => s.confirm)
  const pending = useStore((s) => s.pending)
  const insert = useStore((s) => s.insert)
  const vimOn = useStore((s) => s.settings.vim)
  const input = useRef<HTMLInputElement>(null)
  const hist = useRef<string[]>(JSON.parse(localStorage.getItem('exhistory') || '[]'))
  const hi = useRef(-1)
  const comp = useRef<{ list: string[]; i: number } | null>(null)

  useEffect(() => {
    if (cmdline !== null) input.current?.focus()
  }, [cmdline !== null])

  if (cmdline !== null) {
    return (
      <div className="cmdline">
        <span className="t-iris">:</span>
        <input
          ref={input}
          value={cmdline}
          spellCheck={false}
          onChange={(e) => {
            comp.current = null
            store.setCmdline(e.target.value)
          }}
          onBlur={() => store.setCmdline(null)}
          onKeyDown={(e) => {
            e.stopPropagation()
            if (e.key === 'Escape' || (e.key === 'Backspace' && cmdline === '')) {
              e.preventDefault()
              store.setCmdline(null)
            } else if (e.key === 'Enter') {
              e.preventDefault()
              if (cmdline.trim()) {
                hist.current = [cmdline, ...hist.current.filter((h) => h !== cmdline)].slice(0, 100)
                localStorage.setItem('exhistory', JSON.stringify(hist.current))
              }
              hi.current = -1
              void store.ex(cmdline)
            } else if (e.key === 'Tab') {
              e.preventDefault()
              const back = e.shiftKey
              void (async () => {
                if (!comp.current) {
                  const list = await complete(cmdline)
                  if (store.state.cmdline !== cmdline) return
                  comp.current = { list, i: -1 }
                }
                const c = comp.current
                if (!c.list.length) {
                  comp.current = null
                  return
                }
                c.i = (c.i + (back ? -1 : 1) + c.list.length) % c.list.length
                store.setCmdline(c.list[c.i])
                // With one candidate there is nothing to cycle: the next <Tab>
                // starts from the new text, so a folder goes one level deeper.
                if (c.list.length === 1) comp.current = null
              })()
            } else if (e.key === 'ArrowUp' || (e.ctrlKey && e.key === 'p')) {
              e.preventDefault()
              hi.current = Math.min(hi.current + 1, hist.current.length - 1)
              if (hist.current[hi.current] !== undefined) store.setCmdline(hist.current[hi.current])
            } else if (e.key === 'ArrowDown' || (e.ctrlKey && e.key === 'n')) {
              e.preventDefault()
              hi.current = Math.max(hi.current - 1, -1)
              store.setCmdline(hi.current < 0 ? '' : hist.current[hi.current])
            }
          }}
        />
      </div>
    )
  }
  return (
    <div className="cmdline">
      {confirm ? (
        <span className="t-gold">{confirm.text}</span>
      ) : message?.text ? (
        <span className={`t-${message.tone} ellipsis`}>{message.text}</span>
      ) : (
        <span className="t-muted">{vimOn && insert ? '-- insert --' : ''}</span>
      )}
      <span className="sl-fill" />
      {pending && <span className="t-muted mono">{pending}</span>}
    </div>
  )
}

export function HubBanner() {
  const status = useStore((s) => s.status)
  const detail = useStore((s) => s.statusDetail)
  if (status === 'connected') return null
  if (status === 'connecting') return <div className="banner t-muted">connecting to the hub…</div>
  return (
    <div className={`banner ${status === 'stopped' ? 'b-love' : ''}`}>
      <span className={status === 'stopped' ? 't-love' : 't-gold'}>{status === 'stopped' ? '■' : '◌'}</span>
      <span>{detail || (status === 'stopped' ? 'the hub stopped' : 'lost the hub; reconnecting…')}</span>
      {status === 'stopped' && (
        <button className="btn small primary" onClick={() => void store.startHub()}>
          Start the hub
        </button>
      )}
    </div>
  )
}
