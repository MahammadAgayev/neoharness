import { useEffect, useRef, useState } from 'react'

import { validName } from '../../shared/model'
import { store, useStore } from '../store'

// NewWorkspace is the dialog of "New workspace": a name and a folder. :new
// does the same from the command line.
export function NewWorkspace() {
  const open = useStore((s) => s.newWorkspace)
  const workspaces = useStore((s) => s.workspaces)
  const [name, setName] = useState('')
  const [folder, setFolder] = useState('')
  const [busy, setBusy] = useState(false)
  const nameRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!open) return
    setName('')
    setFolder('')
    setBusy(false)
    nameRef.current?.focus()
  }, [open])

  if (!open) return null
  const close = () => store.openNewWorkspace(false)
  const taken = workspaces.some((w) => w.name === name)
  const problem = !name ? '' : taken ? `${name} is taken` : validName(name) ? 'lowercase letters, digits and dashes, up to 40' : ''
  const ready = !!name && !problem && !!folder.trim() && !busy

  const choose = async () => {
    const dir = await window.nh.pickFolder()
    if (dir) setFolder(store.short(dir))
    nameRef.current?.focus()
  }
  const create = async () => {
    if (!ready) return
    setBusy(true)
    const ok = await store.createWorkspace(name, folder.trim())
    if (ok) close()
    else setBusy(false)
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
          <span className="settings-title">New workspace</span>
          <span className="t-muted">a coordinator and its threads, in one folder</span>
        </div>
        <div className="dialog-body">
          <label className="dialog-field">
            <span>Name</span>
            <input
              ref={nameRef}
              className="field"
              value={name}
              placeholder="api"
              spellCheck={false}
              onChange={(e) => setName(e.target.value.toLowerCase().replace(/\s+/g, '-'))}
            />
            <span className={`dialog-note ${problem ? 't-love' : 't-muted'}`}>{problem || 'Lowercase letters, digits and dashes.'}</span>
          </label>
          <label className="dialog-field">
            <span>Folder</span>
            <div className="dialog-row">
              <input
                className="field"
                value={folder}
                placeholder="~/src/api"
                spellCheck={false}
                onChange={(e) => setFolder(e.target.value)}
              />
              <button type="button" className="btn" onClick={() => void choose()}>
                Choose…
              </button>
            </div>
            <span className="dialog-note t-muted">Where its agents work.</span>
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
