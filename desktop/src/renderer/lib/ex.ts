// Ex command lines: ":open api ~/src/api" is the command open with two words.
// A word in quotes may hold spaces.

export interface Ex {
  cmd: string
  bang: boolean
  args: string[]
  rest: string
}

export function parseEx(line: string): Ex | null {
  const s = line.replace(/^:/, '').trim()
  const m = /^([A-Za-z]+|!)(!?)\s*(.*)$/s.exec(s)
  if (!m) return null
  const rest = m[3]
  return { cmd: m[1], bang: m[2] === '!', args: words(rest), rest }
}

export function words(s: string): string[] {
  const out: string[] = []
  const re = /"([^"]*)"|'([^']*)'|(\S+)/g
  let m: RegExpExecArray | null
  while ((m = re.exec(s))) out.push(m[1] ?? m[2] ?? m[3])
  return out
}

// The commands :<Tab> completes, as the TUI knows them.
export const exCommands = [
  'new',
  'folder',
  'setlocal',
  'workspace',
  'workspaces',
  'restore',
  'open',
  'resolve',
  'reopen',
  'b',
  'ls',
  'split',
  'vsplit',
  'only',
  'close',
  'threads',
  'artifacts',
  'messages',
  'allow',
  'always',
  'deny',
  'stop',
  'w',
  'wq',
  'e',
  'q',
  'qa',
  'help',
  'colorscheme',
  'set',
  'settings',
]
