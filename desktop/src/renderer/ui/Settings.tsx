import { useEffect, useRef, useState } from 'react'

import { commands } from '../commands'
import { keyName, split } from '../lib/keys'
import { schemeNames, schemes, systemPair } from '../lib/themes'
import { store, useStore, type UserMap } from '../store'

const schemeTitles: Record<string, string> = {
  system: 'System',
  'rose-pine-moon': 'Moon',
  'rose-pine': 'Night',
  'rose-pine-dawn': 'Dawn',
}

// Settings is the app's preferences, ⌘, as in every Mac app. It holds what
// the TUI reads from init.lua: the leader, the person's own maps, the
// colors and the options.
export function Settings() {
  const open = useStore((s) => s.settingsOpen)
  const st = useStore((s) => s.settings)
  const panel = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (open) panel.current?.querySelector<HTMLElement>('button, input, select')?.focus()
  }, [open])

  if (!open) return null
  const close = () => store.openSettings(false)
  const setMaps = (maps: UserMap[]) => store.setSettings({ maps })

  return (
    <div className="overlay" onMouseDown={close}>
      <div
        className="palette settings"
        ref={panel}
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          const k = keyName(e.nativeEvent)
          if (k === '<Esc>' || k === '<D-,>' || k === '<D-w>') {
            e.preventDefault()
            close()
          }
        }}
      >
        <div className="settings-head">
          <span className="settings-title">Settings</span>
          <span className="t-muted">saved as you change them · Esc closes</span>
        </div>
        <div className="settings-body">
          <h2>Appearance</h2>
          <Row title="Theme" detail="System follows the light or dark mode of macOS.">
            <div className="swatches">
              {schemeNames.map((n) => {
                const p = schemes[n === 'system' ? systemPair[0] : n]
                const q = schemes[n === 'system' ? systemPair[1] : n]
                return (
                  <button
                    key={n}
                    className={`swatch ${st.scheme === n ? 'active' : ''}`}
                    onClick={() => store.setSettings({ scheme: n })}
                    aria-pressed={st.scheme === n}
                  >
                    <span className="swatch-colors" style={{ background: `linear-gradient(135deg, ${p.base} 50%, ${q.base} 50%)` }}>
                      <span style={{ background: p.iris }} />
                      <span style={{ background: q.foam }} />
                    </span>
                    {schemeTitles[n] ?? n}
                  </button>
                )
              })}
            </div>
          </Row>
          <Row title="Text size" detail="9 to 28 points.">
            <input
              type="number"
              className="field small"
              min={9}
              max={28}
              value={st.fontSize}
              onChange={(e) => {
                const n = Number(e.target.value)
                if (n >= 9 && n <= 28) store.setSettings({ fontSize: n })
              }}
            />
          </Row>
          <Row title="Sidebar" detail="The workspaces on the left. ⌘B hides it for focus.">
            <Toggle on={st.sidebar} set={(sidebar) => store.setSettings({ sidebar })} />
          </Row>

          <h2>Keys</h2>
          <Row title="Vim keys" detail="Off: the prompt works as in Claude Code, Esc takes your message back.">
            <Toggle on={st.vim} set={(vim) => store.setSettings({ vim })} />
          </Row>
          <Row title="Leader" detail="Click, then press the key that starts the leader maps.">
            <KeyField
              value={st.leader}
              single
              label="leader key"
              set={(k) => {
                if (k) store.setSettings({ leader: k })
              }}
            />
          </Row>
          <Row title="Key hints" detail="After a key that starts others, as <leader>, show what can follow, as which-key.">
            <Toggle on={st.keyhints} set={(keyhints) => store.setSettings({ keyhints })} />
          </Row>
          <div className="maps">
            <div className="maps-head">
              <span>Your keys</span>
              <span className="t-muted">They win over the defaults. Nothing turns a default off.</span>
            </div>
            {st.maps.map((m, i) => (
              <MapRow
                key={i}
                map={m}
                leader={st.leader}
                set={(nm) => setMaps(st.maps.map((x, j) => (j === i ? nm : x)))}
                remove={() => setMaps(st.maps.filter((_, j) => j !== i))}
              />
            ))}
            <button className="btn ghost add-map" onClick={() => setMaps([...st.maps, { keys: '', run: commands[0].id }])}>
              + Add a key
            </button>
          </div>

          <h2>Conversation</h2>
          <Row title="Compact warning" detail="The context use, in percent, at which the line under the prompt says /compact. 0 is off.">
            <input
              type="number"
              className="field small"
              min={0}
              max={100}
              value={st.compactwarn}
              onChange={(e) => {
                const n = Number(e.target.value)
                if (n >= 0 && n <= 100) store.setSettings({ compactwarn: n })
              }}
            />
          </Row>
        </div>
      </div>
    </div>
  )
}

function Row(props: { title: string; detail: string; children: React.ReactNode }) {
  return (
    <div className="setting">
      <div className="setting-text">
        <div className="setting-title">{props.title}</div>
        <div className="setting-detail t-muted">{props.detail}</div>
      </div>
      <div className="setting-control">{props.children}</div>
    </div>
  )
}

function Toggle(props: { on: boolean; set: (on: boolean) => void }) {
  return <button className={`toggle ${props.on ? 'on' : ''}`} role="switch" aria-checked={props.on} onClick={() => props.set(!props.on)} />
}

// KeyField records the keys pressed in it, in Vim's notation. Backspace
// takes the last key back; Tab and Esc leave it.
function KeyField(props: { value: string; set: (keys: string) => void; leader?: string; single?: boolean; label: string }) {
  const [recording, setRecording] = useState(false)
  return (
    <button
      className={`field keys ${recording ? 'recording' : ''}`}
      aria-label={props.label}
      onFocus={() => setRecording(true)}
      onBlur={() => setRecording(false)}
      onKeyDown={(e) => {
        const k = keyName(e.nativeEvent)
        if (!k || k === '<Tab>' || k === '<S-Tab>') return
        e.preventDefault()
        if (k === '<Esc>') {
          e.currentTarget.blur()
          e.stopPropagation()
          return
        }
        if (props.single) {
          if (!k.startsWith('<D-') && k !== '<CR>' && k !== '<BS>') props.set(k)
          return
        }
        e.stopPropagation()
        const keys = split(props.value)
        if (k === '<BS>') keys.pop()
        else keys.push(keys.length === 0 && k === props.leader ? '<leader>' : k)
        props.set(keys.join(''))
      }}
    >
      {props.value ? split(props.value).map((k, i) => <kbd key={i}>{k}</kbd>) : <span className="t-muted">{recording ? 'press keys…' : 'click to record'}</span>}
    </button>
  )
}

function MapRow(props: { map: UserMap; leader: string; set: (m: UserMap) => void; remove: () => void }) {
  const { map } = props
  const ex = map.run.startsWith(':')
  return (
    <div className="map-row">
      <KeyField value={map.keys} leader={props.leader} label="keys" set={(keys) => props.set({ ...map, keys })} />
      <span className="t-muted">→</span>
      <select
        className="field"
        aria-label="action"
        value={ex ? ':' : map.run}
        onChange={(e) => props.set({ ...map, run: e.target.value })}
      >
        {commands.map((c) => (
          <option key={c.id} value={c.id}>
            {c.title}
          </option>
        ))}
        <option value=":">An ex command…</option>
        <option value="">Nothing</option>
      </select>
      {ex && (
        <input
          className="field ex"
          aria-label="ex command"
          placeholder=":colorscheme rose-pine-dawn"
          value={map.run}
          onChange={(e) => props.set({ ...map, run: e.target.value.startsWith(':') ? e.target.value : ':' + e.target.value })}
        />
      )}
      <button className="btn ghost icon" aria-label="remove" data-tip="Remove" onClick={props.remove}>
        ×
      </button>
    </div>
  )
}
