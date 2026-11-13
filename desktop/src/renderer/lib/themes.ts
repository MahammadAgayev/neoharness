// Color schemes. Every color in the app is a CSS variable of the scheme, so a
// new scheme is one more entry here. The names of the colors are the TUI's
// (internal/tui/theme.go): Love only for errors and things that need the
// person, Iris for "you type here".

export interface Palette {
  dark: boolean
  base: string
  surface: string
  overlay: string
  muted: string
  subtle: string
  text: string
  love: string
  gold: string
  rose: string
  pine: string
  foam: string
  iris: string
  hlLow: string
  hlMed: string
  hlHigh: string
}

// Rosé Pine, https://rosepinetheme.com/palette
export const schemes: Record<string, Palette> = {
  'rose-pine-moon': {
    dark: true,
    base: '#232136',
    surface: '#2a273f',
    overlay: '#393552',
    muted: '#6e6a86',
    subtle: '#908caa',
    text: '#e0def4',
    love: '#eb6f92',
    gold: '#f6c177',
    rose: '#ea9a97',
    pine: '#3e8fb0',
    foam: '#9ccfd8',
    iris: '#c4a7e7',
    hlLow: '#2a283e',
    hlMed: '#44415a',
    hlHigh: '#56526e',
  },
  'rose-pine': {
    dark: true,
    base: '#191724',
    surface: '#1f1d2e',
    overlay: '#26233a',
    muted: '#6e6a86',
    subtle: '#908caa',
    text: '#e0def4',
    love: '#eb6f92',
    gold: '#f6c177',
    rose: '#ebbcba',
    pine: '#31748f',
    foam: '#9ccfd8',
    iris: '#c4a7e7',
    hlLow: '#21202e',
    hlMed: '#403d52',
    hlHigh: '#524f67',
  },
  'rose-pine-dawn': {
    dark: false,
    base: '#faf4ed',
    surface: '#fffaf3',
    overlay: '#f2e9e1',
    muted: '#9893a5',
    subtle: '#797593',
    text: '#575279',
    love: '#b4637a',
    gold: '#ea9d34',
    rose: '#d7827e',
    pine: '#286983',
    foam: '#56949f',
    iris: '#907aa9',
    hlLow: '#f4ede8',
    hlMed: '#dfdad9',
    hlHigh: '#cecacd',
  },
}

// "system" follows the macOS appearance: the first scheme for dark, the
// second for light.
export const systemPair: [string, string] = ['rose-pine-moon', 'rose-pine-dawn']

export const schemeNames = ['system', ...Object.keys(schemes)]

export function resolveScheme(name: string, systemDark: boolean): Palette {
  if (name === 'system' || !schemes[name]) return schemes[systemPair[systemDark ? 0 : 1]]
  return schemes[name]
}

export function applyScheme(p: Palette, root: HTMLElement): void {
  // Rosé Pine keeps its text soft; a window of work reads sharper with the
  // text a step closer to black or white, and the muted text a step up.
  const ink = p.dark ? '#fff' : '#000'
  const vars: Record<string, string> = {
    '--base': p.base,
    '--surface': p.surface,
    '--overlay': p.overlay,
    // The shell (title bar, sidebar) sits under the panel the work is on.
    '--shell': p.dark ? `color-mix(in srgb, ${p.base} 70%, #000)` : p.overlay,
    '--panel': p.dark ? p.base : p.surface,
    '--muted': `color-mix(in srgb, ${p.muted} 55%, ${p.subtle})`,
    '--subtle': `color-mix(in srgb, ${p.subtle} 85%, ${ink})`,
    '--text': `color-mix(in srgb, ${p.text} ${p.dark ? 88 : 75}%, ${ink})`,
    '--love': p.love,
    '--gold': p.gold,
    '--rose': p.rose,
    '--pine': p.pine,
    '--foam': p.foam,
    '--iris': p.iris,
    '--hl-low': p.hlLow,
    '--hl-med': p.hlMed,
    '--hl-high': p.hlHigh,
  }
  for (const [k, v] of Object.entries(vars)) root.style.setProperty(k, v)
  root.style.colorScheme = p.dark ? 'dark' : 'light'
}
