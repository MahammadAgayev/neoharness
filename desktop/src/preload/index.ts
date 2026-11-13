import { contextBridge, ipcRenderer, type IpcRendererEvent } from 'electron'

import type { Bridge } from '../shared/api'

function listen<A extends unknown[]>(channel: string, fn: (...a: A) => void): () => void {
  const h = (_ev: IpcRendererEvent, ...a: unknown[]) => fn(...(a as A))
  ipcRenderer.on(channel, h)
  return () => ipcRenderer.removeListener(channel, h)
}

const bridge: Bridge = {
  call: (name, args) => ipcRenderer.invoke('hub:call', name, args),
  onUpdate: (fn) => listen('hub:update', fn),
  onStatus: (fn) => listen('hub:status', fn),
  startHub: () => ipcRenderer.invoke('hub:start'),
  restartHub: () => ipcRenderer.invoke('hub:restart'),
  installedVersion: () => ipcRenderer.invoke('hub:version'),
  pickFolder: () => ipcRenderer.invoke('folder:pick'),
  menu: (items) => ipcRenderer.invoke('menu', items),
  folders: (word) => ipcRenderer.invoke('folders', word),
  openExternal: (url) => ipcRenderer.invoke('open:external', url),
  notify: (title, body, target) => ipcRenderer.send('notify', title, body, target),
  onOpenTarget: (fn) => listen('open:target', fn),
  setBadge: (n) => ipcRenderer.send('badge', n),
  copy: (text) => ipcRenderer.send('clipboard:write', text),
  home: () => ipcRenderer.sendSync('home'),
  ready: () => ipcRenderer.send('page:ready'),
}

contextBridge.exposeInMainWorld('nh', bridge)
