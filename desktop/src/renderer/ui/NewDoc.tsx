import { useEffect, useRef, useState } from 'react'

import { validName } from '../../shared/model'
import { store, useStore } from '../store'

// NewDoc names a document the person starts. It opens beside the chat to
// write in; the coordinator works from it and keeps its status. :doc does
// the same.
export function NewDoc() {
  const open = useStore((s) => s.newDoc)
  const artifacts = useStore((s) => s.artifacts)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!open) return
    setName('')
    setBusy(false)
    input.current?.focus()
  }, [open])

  if (!open) return null
  const close = () => store.openNewDoc(false)
  const taken = artifacts.some((a) => a.name === name)
  const problem = !name ? '' : taken ? `${name} is taken` : validName(name) ? 'lowercase letters, digits and dashes, up to 40' : ''
  const ready = !!name && !problem && !busy
  const create = async () => {
    if (!ready) return
    setBusy(true)
    close()
    if (!(await store.newDoc(name))) store.openNewDoc()
  }

  return (
    <div className="overlay" onMouseDown={close}>
      <form
        className="palette dialog"
        onMouseDown={(e) => e.stopPropagation()}
        onSubmit={(e) => {
          e.preventDefault()
          void create()
        }}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            close()
          }
        }}
      >
        <div className="settings-head">
          <span className="settings-title">New doc</span>
          <span className="t-muted">you write it; the coordinator works from it and keeps its status</span>
        </div>
        <div className="dialog-body">
          <label className="dialog-field">
            <span>Name</span>
            <input
              ref={input}
              className="field"
              value={name}
              placeholder="launch-plan"
              spellCheck={false}
              onChange={(e) => setName(e.target.value.toLowerCase().replace(/\s+/g, '-'))}
            />
            <span className={`dialog-note ${problem ? 't-love' : 't-muted'}`}>{problem || 'Lowercase letters, digits and dashes.'}</span>
          </label>
        </div>
        <div className="dialog-foot">
          <button type="button" className="btn ghost" onClick={close}>
            Cancel <kbd>esc</kbd>
          </button>
          <button type="submit" className="btn primary" disabled={!ready}>
            Create <kbd>↵</kbd>
          </button>
        </div>
      </form>
    </div>
  )
}
