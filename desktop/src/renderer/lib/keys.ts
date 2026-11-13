// Keys in Vim's notation, and maps of key sequences to actions, as the TUI
// reads them: "<leader>pt", "]m", "<C-w>v", "gg".

export function keyName(e: Pick<KeyboardEvent, 'key' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey'>): string | null {
  const special: Record<string, string> = {
    Enter: 'CR',
    Escape: 'Esc',
    Tab: 'Tab',
    Backspace: 'BS',
    Delete: 'Del',
    ' ': 'Space',
    ArrowUp: 'Up',
    ArrowDown: 'Down',
    ArrowLeft: 'Left',
    ArrowRight: 'Right',
    Home: 'Home',
    End: 'End',
    PageUp: 'PageUp',
    PageDown: 'PageDown',
  }
  const k = e.key
  if (['Shift', 'Control', 'Alt', 'Meta', 'CapsLock', 'Dead', 'Unidentified'].includes(k)) return null
  const mods = (e.metaKey ? 'D-' : '') + (e.ctrlKey ? 'C-' : '') + (e.altKey && special[k] ? 'M-' : '')
  if (special[k]) {
    const shift = e.shiftKey && k !== ' ' ? 'S-' : ''
    const name = special[k]
    return mods || shift || name !== 'Space' ? `<${mods}${shift}${name}>` : '<Space>'
  }
  if (k.length !== 1) return null
  if (e.ctrlKey || e.metaKey) return `<${mods}${e.shiftKey && /^[a-z]$/i.test(k) ? 'S-' : ''}${k.toLowerCase()}>`
  return k
}

export interface Keymap<A> {
  [seq: string]: A
}

// split reads a sequence into keys: "<leader>pt" is <leader> p t.
export function split(seq: string): string[] {
  const out: string[] = []
  for (let i = 0; i < seq.length; ) {
    if (seq[i] === '<') {
      const j = seq.indexOf('>', i)
      if (j > i + 1) {
        out.push(seq.slice(i, j + 1))
        i = j + 1
        continue
      }
    }
    out.push(seq[i])
    i++
  }
  return out
}

export type Match<A> = { kind: 'action'; action: A; count: number } | { kind: 'pending' } | { kind: 'none' }

// Sequencer collects keys until they name an action, as Vim waits for the
// rest of a map. A count before the keys repeats the action: 3j.
export class Sequencer<A> {
  private keys: string[] = []
  private count = ''
  private maps = new Map<string, A>()
  private prefixes = new Set<string>()

  constructor(maps: Keymap<A>, leader = '<Space>') {
    for (const [seq, a] of Object.entries(maps)) {
      const keys = split(seq.replaceAll('<leader>', leader))
      this.maps.set(keys.join(''), a)
      for (let i = 1; i < keys.length; i++) this.prefixes.add(keys.slice(0, i).join(''))
    }
  }

  get pending(): string {
    return this.count + this.keys.join('')
  }

  reset(): void {
    this.keys = []
    this.count = ''
  }

  feed(key: string): Match<A> {
    if (this.keys.length === 0 && /^[0-9]$/.test(key) && (key !== '0' || this.count !== '')) {
      this.count += key
      return { kind: 'pending' }
    }
    this.keys.push(key)
    const seq = this.keys.join('')
    const a = this.maps.get(seq)
    if (a !== undefined && !this.prefixes.has(seq)) {
      const count = Math.max(parseInt(this.count || '1', 10), 1)
      this.reset()
      return { kind: 'action', action: a, count }
    }
    if (this.prefixes.has(seq)) return { kind: 'pending' }
    this.reset()
    return { kind: 'none' }
  }

  // exact says the keys typed so far are a map of their own.
  get exact(): boolean {
    return this.maps.has(this.keys.join(''))
  }

  // next lists the maps that can follow the keys typed so far, as
  // which-key shows them; each has the keys still to type.
  next(): { keys: string[]; action: A }[] {
    const seq = this.keys.join('')
    const out: { keys: string[]; action: A }[] = []
    for (const [k, a] of this.maps) if (k !== seq && k.startsWith(seq)) out.push({ keys: split(k.slice(seq.length)), action: a })
    return out
  }

  // flush ends a wait that timed out: keys that are a map and also start a
  // longer one run as the shorter map.
  flush(): Match<A> {
    const a = this.maps.get(this.keys.join(''))
    const count = Math.max(parseInt(this.count || '1', 10), 1)
    this.reset()
    return a === undefined ? { kind: 'none' } : { kind: 'action', action: a, count }
  }
}
