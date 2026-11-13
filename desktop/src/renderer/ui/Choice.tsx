import { useEffect, useState } from 'react'

import type { Choices } from '../../shared/model'
import { store, useStore } from '../store'
import { Icon } from './Icon'

interface Row {
  name: string
  label: string
  detail: string
}

// rows are the choices the agent gives, as its own picker shows them, and
// default, which goes back to what the workspace sets.
function rows(kind: 'model' | 'effort', c: Choices): Row[] {
  const def = { name: 'default', label: 'default', detail: 'what the workspace sets' }
  if (kind === 'model') {
    const out = c.models.map((m) => ({
      name: m.name,
      label: m.label || m.name,
      detail: [m.version !== m.name && m.version, m.description].filter(Boolean).join(' · '),
    }))
    return out.some((r) => r.name === 'default') ? out : [...out, def]
  }
  // The levels of the model the thread runs, when the agent says them.
  const levels = c.models.find((m) => m.name === (c.model || 'default'))?.efforts ?? c.efforts
  return [...levels.map((l) => ({ name: l, label: l, detail: '' })), def]
}

// Choice is the picker of a bare /model or /effort, or of a click on the
// model under the prompt. A row sends "/model <name>", which the hub keeps
// for the thread from its next turn.
export function Choice() {
  const c = useStore((s) => s.choice)
  const [got, setGot] = useState<Choices | null>(null)
  const [failed, setFailed] = useState(false)
  const [index, setIndex] = useState(0)
  const list = c && got ? rows(c.kind, got) : []
  const now = c && got ? list.findIndex((r) => r.name === ((c.kind === 'model' ? got.model : got.effort) || 'default')) : -1

  useEffect(() => {
    setGot(null)
    setFailed(false)
    if (!c) return
    let live = true
    // The agent may ask its CLI the first time: the list comes in a moment.
    store
      .choices(c.thread)
      .then((r) => live && setGot(r))
      .catch(() => live && setFailed(true))
    return () => {
      live = false
    }
  }, [c])

  useEffect(() => setIndex(Math.max(now, 0)), [got, now])

  if (!c) return null
  const close = () => {
    store.openChoice(null)
    store.focusPrompt()
  }
  const pick = (i: number) => {
    if (!list[i]) return
    close()
    void store.send(c.thread, `/${c.kind} ${list[i].name}`)
  }
  const onKey = (e: React.KeyboardEvent) => {
    const k = e.key
    if (k === 'Escape') close()
    else if (k === 'ArrowDown' || k === 'j' || (e.ctrlKey && k === 'n')) setIndex((i) => Math.min(i + 1, list.length - 1))
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
          {!got && <div className="palette-empty t-muted">{failed ? 'the hub did not answer' : 'asking the agent…'}</div>}
          {list.map((r, i) => (
            <div
              key={r.name}
              className={`palette-row ${i === index ? 'active' : ''}`}
              onMouseMove={() => i !== index && setIndex(i)}
              onClick={() => pick(i)}
            >
              <span className="glyph t-iris">{i === now && <Icon name="check" size={14} />}</span>
              <span className="palette-label">{r.label}</span>
              {r.detail && <span className="palette-detail">{r.detail}</span>}
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
