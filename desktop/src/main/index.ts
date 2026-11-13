import os from 'node:os'
import path from 'node:path'

import { app, BrowserWindow, clipboard, dialog, ipcMain, Menu, Notification, shell } from 'electron'

import type { CallName } from '../shared/api'
import { folders } from './folders'
import { call, Updates } from './hub'

let win: BrowserWindow | null = null
let updates: Updates | null = null
// Kept so a notification is not collected before the person clicks it.
const shown = new Set<Notification>()

function createWindow(): void {
  win = new BrowserWindow({
    width: 1440,
    height: 900,
    minWidth: 720,
    minHeight: 480,
    title: 'neoharness',
    backgroundColor: '#232136',
    titleBarStyle: 'hiddenInset',
    trafficLightPosition: { x: 14, y: 12 },
    show: false,
    webPreferences: {
      preload: path.join(__dirname, '../preload/index.js'),
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false,
      spellcheck: false,
    },
  })
  win.once('ready-to-show', () => win?.show())

  // The page never leaves the app; links go to the browser.
  win.webContents.setWindowOpenHandler(({ url }) => {
    openExternal(url)
    return { action: 'deny' }
  })
  win.webContents.on('will-navigate', (ev, url) => {
    if (url !== win?.webContents.getURL()) {
      ev.preventDefault()
      openExternal(url)
    }
  })

  if (process.env.ELECTRON_RENDERER_URL) {
    void win.loadURL(process.env.ELECTRON_RENDERER_URL)
  } else {
    void win.loadFile(path.join(__dirname, '../renderer/index.html'))
  }
  win.on('closed', () => {
    win = null
  })
}

function openExternal(url: string): void {
  if (/^(https?|mailto):/i.test(url)) void shell.openExternal(url)
}

function send(channel: string, ...args: unknown[]): void {
  win?.webContents.send(channel, ...args)
}

function menu(): void {
  // Edit roles give Cmd+C, Cmd+V and Cmd+A to the page; without them the
  // keys do nothing on macOS.
  Menu.setApplicationMenu(
    Menu.buildFromTemplate([
      { role: 'appMenu' },
      { role: 'editMenu' },
      {
        label: 'View',
        submenu: [
          { role: 'reload' },
          { role: 'toggleDevTools' },
          { type: 'separator' },
          { role: 'resetZoom' },
          { role: 'zoomIn' },
          { role: 'zoomOut' },
          { type: 'separator' },
          { role: 'togglefullscreen' },
        ],
      },
      { role: 'windowMenu' },
    ]),
  )
}

ipcMain.handle('hub:call', (_ev, name: CallName, args: unknown) => call(name, args))
ipcMain.handle('hub:start', () => updates?.start())
ipcMain.handle('folder:pick', async () => {
  const opts: Electron.OpenDialogOptions = { properties: ['openDirectory', 'createDirectory'], defaultPath: os.homedir() }
  const r = win ? await dialog.showOpenDialog(win, opts) : await dialog.showOpenDialog(opts)
  return r.canceled ? null : (r.filePaths[0] ?? null)
})
ipcMain.handle('folders', (_ev, word: string) => folders(word))
ipcMain.handle('open:external', (_ev, url: string) => openExternal(url))
ipcMain.on('clipboard:write', (_ev, text: string) => clipboard.writeText(text))
ipcMain.on('badge', (_ev, n: number) => app.dock?.setBadge(n > 0 ? String(n) : ''))
ipcMain.on('home', (ev) => {
  ev.returnValue = os.homedir()
})
ipcMain.on('notify', (_ev, title: string, body: string, target: { workspace: string; thread: string }) => {
  // Only when the window is not in front: there the toast says it already.
  if (win?.isFocused() || !Notification.isSupported()) return
  const n = new Notification({ title, body, silent: false })
  shown.add(n)
  n.on('click', () => {
    shown.delete(n)
    if (!win) createWindow()
    win?.show()
    win?.focus()
    send('open:target', target)
  })
  n.on('close', () => shown.delete(n))
  n.show()
})

app.setName('neoharness')
// Another home is another neoharness: its own window state and settings, so
// a scratch hub never changes the person's app.
if (process.env.NEOHARNESS_HOME) app.setPath('userData', path.join(process.env.NEOHARNESS_HOME, 'desktop'))

app.whenReady().then(() => {
  menu()
  createWindow()
  updates = new Updates(
    (u) => send('hub:update', u),
    (s, detail) => send('hub:status', s, detail),
  )
  updates.open()
  app.on('activate', () => {
    if (!win) createWindow()
  })
})

// The status goes again to a page that starts to listen, as it missed it.
ipcMain.on('page:ready', () => updates?.replay())

app.on('window-all-closed', () => {
  // The hub runs on; the app stays in the Dock, as macOS apps do.
})

app.on('before-quit', () => updates?.close())
