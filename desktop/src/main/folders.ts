import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

// folders lists the folders that complete word, in the form it was typed:
// "~/Doc" gives "~/Documents/", as folderCompletions in the TUI. A relative
// word has no completions: the app has no working folder of its own.
export function folders(word: string, home = os.homedir()): string[] {
  if (word === '') return ['~/', '/']
  if (word === '~') return ['~/']
  if (!word.startsWith('/') && !word.startsWith('~/')) return []
  const cut = word.lastIndexOf('/') + 1
  const dir = word.slice(0, cut)
  const base = word.slice(cut)
  const read = dir.startsWith('~/') ? path.join(home, dir.slice(2)) : dir
  let entries: fs.Dirent[]
  try {
    entries = fs.readdirSync(read, { withFileTypes: true })
  } catch {
    return []
  }
  return entries
    .filter((e) => e.isDirectory() && e.name.startsWith(base) && (!e.name.startsWith('.') || base.startsWith('.')))
    .map((e) => dir + e.name + '/')
    .sort()
}
