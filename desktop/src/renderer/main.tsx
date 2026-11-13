import { createRoot } from 'react-dom/client'

import type { Bridge } from '../shared/api'
import { installKeys } from './keyboard'
import { store } from './store'
import { App } from './ui/App'
import './styles.css'

declare global {
  interface Window {
    nh: Bridge
  }
}

installKeys()
store.start()
createRoot(document.getElementById('root')!).render(<App />)
