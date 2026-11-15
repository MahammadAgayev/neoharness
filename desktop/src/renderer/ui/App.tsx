import { lazy, Suspense, useEffect, useState } from 'react'

import { applyScheme, resolveScheme } from '../lib/themes'
import { store, useStore, type Win } from '../store'
import { ChatWindow } from './ChatWindow'
import { CommandLine, HubBanner, Picker, Toasts } from './Overlays'
import { KeyHints } from './KeyHints'
import { NewDoc } from './NewDoc'
import { NewWorkspace } from './NewWorkspace'
import { Tooltip } from './Tooltip'
import { Help, MessagesWindow, TodoWindow, WaitsWindow } from './Panels'
import { Icon } from './Icon'
import { Choice } from './Choice'
import { Sidebar } from './Sidebar'
import { ThreadsList } from './ThreadsList'
import { Workspaces } from './Workspaces'
import { Leave } from './Leave'

// The page editor, the settings and the cost dialog load after the first
// paint, each in a chunk of its own, so the app opens on the chat at once.
const loadArtifactWindow = () => import('./ArtifactWindow')
const ArtifactWindow = lazy(() => loadArtifactWindow().then((m) => ({ default: m.ArtifactWindow })))
const Settings = lazy(() => import('./Settings').then((m) => ({ default: m.Settings })))
const Cost = lazy(() => import('./Cost').then((m) => ({ default: m.Cost })))
// The page editor is fetched once the app is idle, so the first page opens
// without a wait.
requestIdleCallback(() => void loadArtifactWindow())

function WindowView(props: { win: Win; focused: boolean }) {
  const { win } = props
  switch (win.buf.kind) {
    case 'chat':
      return <ChatWindow key={win.buf.thread} {...props} />
    case 'threads':
      return <ThreadsList {...props} />
    case 'artifact':
      return (
        <Suspense key={win.buf.name} fallback={null}>
          <ArtifactWindow {...props} />
        </Suspense>
      )
    case 'messages':
      return <MessagesWindow {...props} />
    case 'waits':
      return <WaitsWindow {...props} />
    case 'todo':
      return <TodoWindow key={win.buf.thread} {...props} thread={win.buf.thread} />
  }
}

function Layout() {
  const layout = useStore((s) => s.layout)
  const workspace = useStore((s) => s.workspace)
  const focused = layout.cols.flatMap((c) => c.wins).find((w) => w.id === layout.focus)
  const title = focused?.buf.kind === 'chat' ? focused.buf.thread : focused?.buf.kind === 'artifact' ? focused.buf.name : focused?.buf.kind
  useEffect(() => {
    document.title = title ? `${workspace} · ${title}` : workspace || 'neoharness'
  }, [workspace, title])
  const [sizes, setSizes] = useState<Record<number, number>>({})
  // <C-w>= gives the columns their own widths back.
  useEffect(() => {
    const on = () => setSizes({})
    document.addEventListener('nh:equal', on)
    return () => document.removeEventListener('nh:equal', on)
  }, [])
  const drag = (colId: number, start: number, width: number) => (e: React.MouseEvent) => {
    e.preventDefault()
    const x0 = e.clientX
    const move = (ev: MouseEvent) => setSizes((s) => ({ ...s, [colId]: Math.max(240, width + (ev.clientX - x0) * start) }))
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
      document.body.classList.remove('resizing')
    }
    document.body.classList.add('resizing')
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }
  return (
    <div className="layout">
      {layout.cols.map((c, ci) => {
        // A column that only lists threads is narrow, as the TUI's pane.
        const narrow = c.wins.every((w) => w.buf.kind === 'threads')
        const size = sizes[c.id]
        const style = size ? { flex: `0 0 ${size}px` } : narrow ? { flex: '0 0 320px' } : { flex: '1 1 0' }
        return (
          <div key={c.id} className="column" style={style} id={`col-${c.id}`}>
            {ci > 0 && (
              <div
                className="resizer"
                onMouseDown={(e) => {
                  const el = document.getElementById(`col-${c.id}`)
                  drag(c.id, -1, el?.getBoundingClientRect().width ?? 320)(e)
                }}
              />
            )}
            {c.wins.map((w) => (
              <div key={w.id} className={`pane ${w.id === layout.focus ? 'focused' : ''}`}>
                <WindowView win={w} focused={w.id === layout.focus} />
              </div>
            ))}
          </div>
        )
      })}
    </div>
  )
}

function TitleBar() {
  const screen = useStore((s) => s.screen)
  const waiting = useStore((s) => s.waits.length)
  const sidebar = useStore((s) => s.settings.sidebar)
  const win = useStore((s) => s.layout.cols.flatMap((c) => c.wins).find((w) => w.id === s.layout.focus))
  // The buttons keep the focus where it is, as the keys do.
  const keep = (e: React.MouseEvent) => e.preventDefault()
  return (
    <div className="titlebar">
      <div className="titlebar-left">
        <button
          className="nav-btn"
          onMouseDown={keep}
          onClick={() => store.setSettings({ sidebar: !sidebar })}
          data-tip={sidebar ? 'Fold the sidebar' : 'Unfold the sidebar'}
          data-keys="⌘B"
        >
          <Icon name="panel-left" size={15} />
        </button>
        <button
          className="nav-btn nav-back"
          disabled={screen !== 'workspace' || !win?.back.length}
          onMouseDown={keep}
          onClick={() => store.goBack()}
          data-tip="Back"
          data-keys="⌘[  <C-o>"
        >
          <Icon name="arrow-left" size={15} />
        </button>
        <button
          className="nav-btn nav-fwd"
          disabled={screen !== 'workspace' || !win?.fwd.length}
          onMouseDown={keep}
          onClick={() => store.goForward()}
          data-tip="Forward"
          data-keys="⌘]  <C-i>"
        >
          <Icon name="arrow-right" size={15} />
        </button>
      </div>
      {waiting > 0 && (
        <button className="bar-waits" onClick={() => void store.ex('waits')} data-tip="What waits for you" data-keys="]q  :waits">
          ◆ {waiting} waiting
        </button>
      )}
    </div>
  )
}

export function App() {
  const scheme = useStore((s) => s.settings.scheme)
  const fontSize = useStore((s) => s.settings.fontSize)
  const screen = useStore((s) => s.screen)
  const sidebar = useStore((s) => s.settings.sidebar)
  const [dark, setDark] = useState(() => window.matchMedia('(prefers-color-scheme: dark)').matches)

  useEffect(() => {
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const on = () => setDark(mq.matches)
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [])
  useEffect(() => applyScheme(resolveScheme(scheme, dark), document.documentElement), [scheme, dark])
  useEffect(() => {
    document.documentElement.style.setProperty('--font-size', `${fontSize}px`)
  }, [fontSize])
  useEffect(() => {
    const root = document.documentElement.classList
    const blur = () => root.add('blurred')
    const focus = () => root.remove('blurred')
    window.addEventListener('blur', blur)
    window.addEventListener('focus', focus)
    return () => {
      window.removeEventListener('blur', blur)
      window.removeEventListener('focus', focus)
    }
  }, [])

  return (
    <div className="app">
      <TitleBar />
      <HubBanner />
      <main className="main">
        <Sidebar folded={!sidebar} />
        {screen === 'workspaces' ? <Workspaces /> : <Layout />}
      </main>
      <CommandLine />
      <Toasts />
      <Leave>
        <Picker />
        <Suspense fallback={null}>
          <Settings />
        </Suspense>
        <Help />
        <Suspense fallback={null}>
          <Cost />
        </Suspense>
        <Choice />
        <NewWorkspace />
        <NewDoc />
      </Leave>
      <Tooltip />
      <KeyHints />
    </div>
  )
}
