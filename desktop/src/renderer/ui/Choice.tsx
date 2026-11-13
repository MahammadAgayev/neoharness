import { useEffect, useState } from 'react'

import { store, useStore } from '../store'
import { Icon } from './Icon'

// The choices of Claude Code's /model and /effort pickers. The hub takes
// any name; these are the ones worth a row.
const options = {
  model: [
    { name: 'opus', detail: 'the most capable' },
    { name: 'sonnet', detail: 'fast and capable' },
    { name: 'haiku', detail: 'the fastest' },
    { name: 'default', detail: 'what the workspace sets' },
  ],
  effort: [
    { name: 'low', detail: '' },
    { name: 'medium', detail: '' },
    { name: 'high', detail: '' },
    { name: 'xhigh', detail: '' },
    { name: 'max', detail: '' },
    { name: 'default', detail: 'what the workspace sets' },
  ],
}

// Choice is the picker of a bare /model or /effort, or of a click on the
// model under the prompt. A row sends "/model <name>", which the hub keeps
// for the thread from its next turn.
export function Choice() {
  const c = useStore((s) => s.choice)
  const view = useStore((s) => (c ? s.threads[c.thread] : undefined))
  const [index, setIndex] = useState(0)
  const rows = c ? options[c.kind] : []
  const now = c?.kind === 'model' ? rows.findIndex((r) => view?.model?.includes(r.name)) : -1

  useEffect(() => {
    if (c) setIndex(Math.max(now, 0))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [c])

  if (!c) return null
  const close = () => {
    store.openChoice(null)
    store.focusPrompt()
  }
  const pick = (i: number) => {
    close()
    void store.send(c.thread, `/${c.kind} ${rows[i].name}`)
  }
  const onKey = (e: React.KeyboardEvent) => {
    const k = e.key
    if (k === 'Escape') close()
    else if (k === 'ArrowDown' || k === 'j' || (e.ctrlKey && k === 'n')) setIndex((i) => Math.min(i + 1, rows.length - 1))
    else if (k === 'ArrowUp' || k === 'k' || (e.ctrlKey && k === 'p')) setIndex((i) => Math.max(i - 1, 0))
    else if (k === 'Enter') pick(index)
    else return
    e.preventDefault()
    e.stopPropagation()
  }
  return (
    <div className="overlay" onMouseDown={close}>
      <div className="palette dialog choice" tabIndex={-1} ref={(el) => el?.focus()} onMouseDown={(e) => e.stopPropagation()} onKeyDown={onKey}>
        <div className="settings-head">
          <span className="settings-title">{c.kind === 'model' ? 'Model' : 'Effort'}</span>
          <span className="t-muted">for {store.at(c.thread).thread}, from its next turn</span>
        </div>
        <div className="palette-list">
          {rows.map((r, i) => (
            <div
              key={r.name}
              className={`palette-row ${i === index ? 'active' : ''}`}
              onMouseMove={() => i !== index && setIndex(i)}
              onClick={() => pick(i)}
            >
              <span className="glyph t-iris">{i === now && <Icon name="check" size={14} />}</span>
              <span className="palette-label">{r.name}</span>
              {r.detail && <span className="palette-detail">{r.detail}</span>}
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
